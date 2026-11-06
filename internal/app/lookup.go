package app

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/sources"
	"github.com/zenware/netblocks/internal/store"
)

type countryResult struct {
	Query       string   `json:"query"`
	CountryCode string   `json:"country_code"`
	CountryName string   `json:"country_name,omitempty"`
	IPv4        []string `json:"ipv4,omitempty"`
	IPv6        []string `json:"ipv6,omitempty"`
	ASNs        []uint32 `json:"asns,omitempty"`
}

type sourceCountry struct {
	Source      string `json:"source"`
	CountryCode string `json:"country_code"`
	CountryName string `json:"country_name,omitempty"`
	Network     string `json:"network,omitempty"`
}

type asnDetail struct {
	ASN         uint32 `json:"asn"`
	Description string `json:"description,omitempty"`
	Network     string `json:"network,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	CountryName string `json:"country_name,omitempty"`
	Prefixes    int64  `json:"announced_prefixes,omitempty"`
}

type asnAnnouncement struct {
	Network    string `json:"network"`
	Family     string `json:"family"`
	Source     string `json:"source"`
	SourceType string `json:"source_type"`
}

type prefixGroup struct {
	numType       netutil.NumType
	announcements []store.ASNAnnouncement
	total         int
	truncated     bool
}

type ipResult struct {
	Query    string          `json:"query"`
	IP       string          `json:"ip"`
	Family   string          `json:"family"`
	Sources  []sourceCountry `json:"sources,omitempty"`
	ASN      []asnDetail     `json:"asn,omitempty"`
	Conflict bool            `json:"conflict,omitempty"`
}

func (a *App) lookupCode(ctx context.Context) error {
	code := strings.ToUpper(strings.TrimSpace(a.opts.Code))
	if len(code) != 2 {
		return fmt.Errorf("%q is not a two-letter country code", a.opts.Code)
	}
	return a.lookupCountry(ctx, code)
}

func (a *App) lookupName(ctx context.Context) error {
	d, err := a.loadCountries()
	if err != nil {
		return err
	}
	name := strings.TrimSpace(a.opts.Name)
	code, ok := d.Code(name)
	if !ok {
		if candidates := d.Find(name); len(candidates) > 0 {
			var parts []string
			for _, c := range candidates {
				parts = append(parts, c.Code+" ("+c.Name+")")
			}
			return fmt.Errorf("country name %q is ambiguous; try one of: %s", name, strings.Join(parts, ", "))
		}
		return fmt.Errorf("no country matches %q", name)
	}
	return a.lookupCountry(ctx, code)
}

func (a *App) lookupCountry(ctx context.Context, code string) error {
	types, err := selectedTypes(a.opts.TypeFilter)
	if err != nil {
		return err
	}
	srcs, err := a.assignmentSources([]string{sources.SourceRIR})
	if err != nil {
		return err
	}
	a.warnLookupState(ctx)
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	d, err := a.loadCountries()
	if err != nil {
		return err
	}
	result := countryResult{Query: "country", CountryCode: code}
	if name, ok := d.Name(code); ok {
		result.CountryName = name
	} else if code == "*" {
		result.CountryName = "unassigned/reserved"
	}
	var textParts []string
	found := false
	for _, numType := range types {
		list, err := st.AssignmentsByCountry(ctx, numType, code, srcs, store.AssignmentFilter{
			Statuses:        a.opts.Statuses,
			ExcludeReserved: a.opts.ExcludeReserved,
		})
		if err != nil {
			return err
		}
		if len(list) == 0 {
			continue
		}
		found = true
		switch numType {
		case netutil.ASN:
			asns, err := asnStrings(list, a.opts.ExpandASNs)
			if err != nil {
				return err
			}
			result.ASNs = asns
			for _, asn := range asns {
				textParts = append(textParts, fmt.Sprintf("%d", asn))
			}
		default:
			blocks, err := blockStrings(list, a.opts.Merge)
			if err != nil {
				return err
			}
			if numType == netutil.IPv4 {
				result.IPv4 = blocks
			} else {
				result.IPv6 = blocks
			}
			textParts = append(textParts, blocks...)
		}
	}
	if a.opts.JSON {
		return a.writeJSON(result)
	}
	if !found {
		a.logf("no allocations found for %s", code)
		return nil
	}
	for _, part := range textParts {
		fmt.Fprintln(a.opts.Out, part)
	}
	return nil
}

func selectedTypes(filter string) ([]netutil.NumType, error) {
	filter = strings.ToLower(strings.TrimSpace(filter))
	switch filter {
	case "":
		return []netutil.NumType{netutil.IPv4, netutil.IPv6, netutil.ASN}, nil
	case "ipv4":
		return []netutil.NumType{netutil.IPv4}, nil
	case "ipv6":
		return []netutil.NumType{netutil.IPv6}, nil
	case "asn":
		return []netutil.NumType{netutil.ASN}, nil
	default:
		return nil, fmt.Errorf("invalid number type %q (valid: ipv4, ipv6, asn)", filter)
	}
}

func (a *App) lookupIP(ctx context.Context) error {
	ipText := strings.TrimSpace(a.opts.IP)
	n, width, err := netutil.ParseAddr(ipText)
	if err != nil {
		return fmt.Errorf("%q is not a valid IP address", ipText)
	}
	numType := netutil.TypeForBits(width)
	family := "ipv6"
	if width == 32 {
		family = "ipv4"
	}
	srcs, err := a.assignmentSources(sources.DefaultLookupSources)
	if err != nil {
		return err
	}
	a.warnLookupState(ctx)
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	d, err := a.loadCountries()
	if err != nil {
		return err
	}

	result := ipResult{Query: "ip", IP: ipText, Family: family}
	var maxmindCC, rirCC string
	rirMissing := false
	for _, src := range srcs {
		assigned, ok, err := st.CountryAt(ctx, numType, src, n)
		if err != nil {
			return err
		}
		if !ok {
			if src == sources.SourceRIR {
				rirMissing = true
			}
			continue
		}
		cc := assigned.CountryCode
		name, _ := d.Name(cc)
		result.Sources = append(result.Sources, sourceCountry{
			Source:      src,
			CountryCode: cc,
			CountryName: name,
			Network:     networkString(assigned.Start, assigned.End, numType),
		})
		switch src {
		case sources.SourceMaxMind:
			maxmindCC = cc
		case sources.SourceRIR:
			rirCC = cc
		}
	}
	if maxmindCC != "" && rirCC != "" && maxmindCC != rirCC {
		result.Conflict = true
	}

	orgs, err := st.OrgForIP(ctx, numType, n)
	if err != nil {
		return err
	}
	for _, org := range orgs {
		detail := asnDetail{
			ASN:         org.ASN,
			Description: org.Description,
			Network:     networkString(org.Start, org.End, numType),
		}
		result.ASN = append(result.ASN, detail)
	}

	if a.opts.JSON {
		return a.writeJSON(result)
	}
	fmt.Fprintf(a.opts.Out, "Reverse lookup for: %s\n", ipText)
	for _, src := range result.Sources {
		label := sourceLabel(src.Source)
		fmt.Fprintf(a.opts.Out, "%s country code: %s\n", label, src.CountryCode)
		if src.CountryName != "" {
			fmt.Fprintf(a.opts.Out, "%s country name: %s\n", label, src.CountryName)
		}
	}
	if rirMissing {
		fmt.Fprintln(a.opts.Out, "Not found in RIR db")
	}
	for _, info := range result.ASN {
		if info.Description != "" {
			fmt.Fprintf(a.opts.Out, "ASN: AS%d - %s (%s)\n", info.ASN, info.Description, info.Network)
		} else {
			fmt.Fprintf(a.opts.Out, "ASN: AS%d (%s)\n", info.ASN, info.Network)
		}
	}
	if result.Conflict {
		fmt.Fprintln(a.opts.Out, "It appears that the RIR data conflicts with MaxMind's data.  MaxMind's data is likely closer to being correct due to sub-delegation issues with LIR databases.")
	}
	return nil
}

func (a *App) lookupASN(ctx context.Context) error {
	asn, err := netutil.ParseASN(a.opts.ASN)
	if err != nil {
		return err
	}
	a.warnLookupState(ctx)
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	d, err := a.loadCountries()
	if err != nil {
		return err
	}
	n := new(big.Int).SetUint64(uint64(asn))
	assigned, found, err := st.CountryAt(ctx, netutil.ASN, sources.SourceRIR, n)
	if err != nil {
		return err
	}
	description, err := st.ASNDescription(ctx, asn)
	if err != nil {
		return err
	}
	count, err := st.ASNAnnouncementCount(ctx, asn)
	if err != nil {
		return err
	}
	// BGP announcements win; MaxMind ASN data fills in when the BGP snapshot
	// has no announcements for this ASN.
	useMaxMind := false
	if count == 0 {
		mm, err := st.MaxMindASNCount(ctx, asn)
		if err != nil {
			return err
		}
		if mm > 0 {
			count = mm
			useMaxMind = true
		}
	}
	result := asnDetail{ASN: asn, Description: description, Prefixes: count}
	if found {
		result.CountryCode = assigned.CountryCode
		result.CountryName, _ = d.Name(assigned.CountryCode)
	}
	var announcements []asnAnnouncement
	var groups []prefixGroup
	truncatedAny := false
	if a.opts.Prefixes || a.opts.JSON {
		announcements = make([]asnAnnouncement, 0)
		for _, numType := range []netutil.NumType{netutil.IPv4, netutil.IPv6} {
			var list []store.ASNAnnouncement
			if useMaxMind {
				list, err = st.MaxMindASNForASN(ctx, asn, numType)
			} else {
				list, err = st.AnnouncementsForASN(ctx, asn, numType)
			}
			if err != nil {
				return err
			}
			group := prefixGroup{numType: numType, announcements: list, total: len(list)}
			if a.opts.PrefixLimit > 0 && len(list) > a.opts.PrefixLimit {
				group.announcements = list[:a.opts.PrefixLimit]
				group.truncated = true
				truncatedAny = true
			}
			groups = append(groups, group)
			for _, ann := range group.announcements {
				announcements = append(announcements, asnAnnouncement{
					Network:    networkString(ann.Start, ann.End, numType),
					Family:     string(numType),
					Source:     ann.SourceName,
					SourceType: ann.SourceType,
				})
			}
		}
	}
	if a.opts.JSON {
		payload := map[string]any{
			"query":              "asn",
			"asn":                asn,
			"country_code":       result.CountryCode,
			"country_name":       result.CountryName,
			"description":        result.Description,
			"announced_prefixes": result.Prefixes,
			"announcements":      announcements,
		}
		if truncatedAny {
			payload["announcements_truncated"] = true
		}
		return a.writeJSON(payload)
	}
	if found {
		fmt.Fprintf(a.opts.Out, "AS country code: %s\n", result.CountryCode)
		if result.CountryName != "" {
			fmt.Fprintf(a.opts.Out, "AS country name: %s\n", result.CountryName)
		}
	} else {
		fmt.Fprintf(a.opts.Out, "AS%d not found in RIR delegation data.\n", asn)
	}
	if description != "" {
		fmt.Fprintf(a.opts.Out, "AS description: %s\n", description)
	}
	if count > 0 {
		fmt.Fprintf(a.opts.Out, "AS announced prefixes: %d\n", count)
	}
	if a.opts.Prefixes {
		for _, group := range groups {
			if len(group.announcements) == 0 {
				continue
			}
			if group.truncated {
				fmt.Fprintf(a.opts.Out, "%s prefixes (showing %d of %d):\n", familyLabel(group.numType), len(group.announcements), group.total)
			} else {
				fmt.Fprintf(a.opts.Out, "%s prefixes:\n", familyLabel(group.numType))
			}
			for _, ann := range group.announcements {
				fmt.Fprintf(a.opts.Out, "  %s\n", networkString(ann.Start, ann.End, group.numType))
			}
		}
	}
	return nil
}

func (a *App) whatCountry(ctx context.Context) error {
	code := strings.ToUpper(strings.TrimSpace(a.opts.WhatCountry))
	d, err := a.loadCountries()
	if err != nil {
		return err
	}
	name, ok := d.Name(code)
	if !ok {
		if a.opts.JSON {
			return a.writeJSON(map[string]any{"code": code, "known": false})
		}
		return fmt.Errorf("Hmm, %s? We're not sure either. Are you sure that's a country code?", code)
	}
	if a.opts.JSON {
		return a.writeJSON(map[string]any{"code": code, "name": name, "known": true})
	}
	fmt.Fprintf(a.opts.Out, "Hmm...%s? That would be %s.\n", code, name)
	return nil
}

func (a *App) lookupOrgByIP(ctx context.Context) error {
	ipText := strings.TrimSpace(a.opts.LookupOrgIP)
	n, width, err := netutil.ParseAddr(ipText)
	if err != nil {
		return fmt.Errorf("%q is not a valid IP address", ipText)
	}
	numType := netutil.TypeForBits(width)
	a.warnLookupState(ctx)
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	orgs, err := st.OrgForIP(ctx, numType, n)
	if err != nil {
		return err
	}
	if a.opts.JSON {
		return a.writeJSON(map[string]any{
			"query": "lookup-org-by-ip",
			"ip":    ipText,
			"orgs":  orgDetails(orgs, numType),
		})
	}
	if len(orgs) == 0 {
		fmt.Fprintf(a.opts.Out, "Did not find any matching announcements containing %s.\n", ipText)
		return nil
	}
	for _, org := range orgs {
		fmt.Fprintf(a.opts.Out, "%s in %s announced by AS%d", ipText, networkString(org.Start, org.End, numType), org.ASN)
		if org.Description != "" {
			fmt.Fprintf(a.opts.Out, " - %s", org.Description)
		}
		fmt.Fprintln(a.opts.Out)
	}
	return nil
}

func (a *App) lookupOrgByRange(ctx context.Context) error {
	start, startWidth, err := netutil.ParseAddr(a.opts.RangeStart)
	if err != nil {
		return fmt.Errorf("%q is not a valid range start", a.opts.RangeStart)
	}
	end, endWidth, err := netutil.ParseAddr(a.opts.RangeEnd)
	if err != nil {
		return fmt.Errorf("%q is not a valid range end", a.opts.RangeEnd)
	}
	if startWidth != endWidth {
		return errors.New("range start and end must be the same address family")
	}
	if start.Cmp(end) > 0 {
		start, end = end, start
	}
	numType := netutil.TypeForBits(startWidth)
	a.warnLookupState(ctx)
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	orgs, err := st.OrgForRange(ctx, numType, start, end)
	if err != nil {
		return err
	}
	if a.opts.JSON {
		return a.writeJSON(map[string]any{
			"query":       "lookup-org-by-range",
			"range_start": a.opts.RangeStart,
			"range_end":   a.opts.RangeEnd,
			"orgs":        orgDetails(orgs, numType),
		})
	}
	if len(orgs) == 0 {
		fmt.Fprintf(a.opts.Out, "Did not find any matching announcements in range %s %s.\n", a.opts.RangeStart, a.opts.RangeEnd)
		return nil
	}
	for _, org := range orgs {
		fmt.Fprintf(a.opts.Out, "%s announced by AS%d", networkString(org.Start, org.End, numType), org.ASN)
		if org.Description != "" {
			fmt.Fprintf(a.opts.Out, " - %s", org.Description)
		}
		fmt.Fprintln(a.opts.Out)
	}
	return nil
}

func orgDetails(orgs []store.Org, numType netutil.NumType) []asnDetail {
	out := make([]asnDetail, 0, len(orgs))
	for _, org := range orgs {
		out = append(out, asnDetail{
			ASN:         org.ASN,
			Description: org.Description,
			Network:     networkString(org.Start, org.End, numType),
		})
	}
	return out
}

func sourceLabel(source string) string {
	switch source {
	case sources.SourceRIR:
		return "RIR"
	case sources.SourceLIR:
		return "LIR"
	case sources.SourceMaxMind:
		return "MaxMind"
	default:
		return source
	}
}

func coalesce(list []store.Assignment) []store.Assignment {
	if len(list) == 0 {
		return nil
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Start.Cmp(list[j].Start) < 0 })
	out := make([]store.Assignment, 0, len(list))
	for _, a := range list {
		if len(out) == 0 {
			out = append(out, a)
			continue
		}
		last := &out[len(out)-1]
		next := new(big.Int).Add(last.End, big.NewInt(1))
		if a.Start.Cmp(next) <= 0 {
			if a.End.Cmp(last.End) > 0 {
				last.End = a.End
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func blockStrings(list []store.Assignment, merge bool) ([]string, error) {
	if merge {
		list = coalesce(list)
	}
	var out []string
	seen := make(map[string]bool)
	for _, a := range list {
		prefixes, err := netutil.Summarize(a.Start, a.End, a.NumType.Bits())
		if err != nil {
			return nil, err
		}
		for _, p := range prefixes {
			s := p.String()
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func asnStrings(list []store.Assignment, expand bool) ([]uint32, error) {
	seen := make(map[uint32]bool)
	var out []uint32
	var total uint64
	for _, a := range list {
		start := a.Start.Uint64()
		end := a.End.Uint64()
		if !expand || a.Start.Cmp(a.End) == 0 {
			if !seen[uint32(start)] {
				seen[uint32(start)] = true
				out = append(out, uint32(start))
			}
			continue
		}
		count := end - start + 1
		total += count
		if total > 5_000_000 {
			return nil, fmt.Errorf("ASN range expansion exceeds 5,000,000 entries; use --no-expand-asns")
		}
		for v := start; ; v++ {
			if !seen[uint32(v)] {
				seen[uint32(v)] = true
				out = append(out, uint32(v))
			}
			if v == end {
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func familyLabel(numType netutil.NumType) string {
	if numType == netutil.IPv6 {
		return "IPv6"
	}
	return "IPv4"
}

func networkString(start, end *big.Int, numType netutil.NumType) string {
	if numType == netutil.ASN {
		return asnRangeString(start, end)
	}
	prefixes, err := netutil.Summarize(start, end, numType.Bits())
	if err != nil || len(prefixes) == 0 {
		return rangeString(start, end, numType)
	}
	parts := make([]string, len(prefixes))
	for i, p := range prefixes {
		parts[i] = p.String()
	}
	return strings.Join(parts, ",")
}

func rangeString(start, end *big.Int, numType netutil.NumType) string {
	if numType == netutil.ASN {
		return asnRangeString(start, end)
	}
	width := numType.Bits()
	s, err := netutil.IntToAddr(start, width)
	if err != nil {
		return "?"
	}
	if start.Cmp(end) == 0 {
		return s.String()
	}
	e, err := netutil.IntToAddr(end, width)
	if err != nil {
		return s.String()
	}
	return s.String() + "-" + e.String()
}

func asnRangeString(start, end *big.Int) string {
	if start.Cmp(end) == 0 {
		return fmt.Sprintf("AS%d", start.Uint64())
	}
	return fmt.Sprintf("AS%d-AS%d", start.Uint64(), end.Uint64())
}
