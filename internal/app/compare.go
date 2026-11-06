package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/sources"
	"github.com/zenware/netblocks/internal/store"
)

type assignmentJSON struct {
	NumType     string `json:"type"`
	Source      string `json:"source"`
	CountryCode string `json:"country_code"`
	Range       string `json:"range"`
}

type comparisonJSON struct {
	First  assignmentJSON `json:"first"`
	Second assignmentJSON `json:"second"`
	Marker string         `json:"marker"`
}

func (a *App) compare(ctx context.Context) error {
	cc := strings.ToUpper(strings.TrimSpace(a.opts.Compare))
	if len(cc) != 2 {
		return fmt.Errorf("%q is not a two-letter country code", a.opts.Compare)
	}
	firstSources, secondSources, err := a.compareSources()
	if err != nil {
		return err
	}
	a.warnLookupState(ctx)
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	types := []netutil.NumType{netutil.IPv4, netutil.IPv6, netutil.ASN}
	var results []store.Comparison
	if a.opts.CompareAll {
		results, err = st.Compare(ctx, cc, types, firstSources, secondSources)
	} else {
		results, err = st.Conflicts(ctx, cc, types, firstSources, secondSources)
	}
	if err != nil {
		return err
	}
	// Suppress exact self-pairs when --compare-all lets a source compare with
	// itself; they carry no information.
	filtered := results[:0]
	for _, r := range results {
		if r.First.SourceType == r.Second.SourceType &&
			r.First.Start.Cmp(r.Second.Start) == 0 &&
			r.First.End.Cmp(r.Second.End) == 0 {
			continue
		}
		filtered = append(filtered, r)
	}
	results = filtered
	sort.SliceStable(results, func(i, j int) bool {
		x, y := results[i], results[j]
		if x.First.NumType != y.First.NumType {
			return x.First.NumType < y.First.NumType
		}
		if x.First.SourceType != y.First.SourceType {
			return x.First.SourceType < y.First.SourceType
		}
		if c := x.First.Start.Cmp(y.First.Start); c != 0 {
			return c < 0
		}
		if x.Second.SourceType != y.Second.SourceType {
			return x.Second.SourceType < y.Second.SourceType
		}
		return x.Second.Start.Cmp(y.Second.Start) < 0
	})

	if a.opts.JSON {
		mode := "conflicts"
		if a.opts.CompareAll {
			mode = "all"
		}
		out := make([]comparisonJSON, 0, len(results))
		for _, r := range results {
			out = append(out, comparisonJSON{
				First:  toAssignmentJSON(r.First),
				Second: toAssignmentJSON(r.Second),
				Marker: compareMarker(r, 0),
			})
		}
		return a.writeJSON(map[string]any{
			"query":        "compare",
			"country_code": cc,
			"mode":         mode,
			"comparisons":  out,
		})
	}
	if len(results) == 0 {
		if a.opts.CompareAll {
			fmt.Fprintln(a.opts.Out, "No overlapping assignments found.")
		} else {
			fmt.Fprintln(a.opts.Out, "No country-code conflicts found.")
		}
		return nil
	}

	fmt.Fprintln(a.opts.Out, "Comparing assignments with overlapping assignments in other data sources...")
	fmt.Fprintln(a.opts.Out)
	fmt.Fprintln(a.opts.Out, "Legend:")
	if a.opts.CompareAll {
		fmt.Fprintf(a.opts.Out, "  '<' = overlapping range from the same source\n")
		fmt.Fprintf(a.opts.Out, "  '>' = overlapping assignment with the same country code %s\n", cc)
		fmt.Fprintf(a.opts.Out, "  '*' = overlapping assignment with a different country code\n")
		fmt.Fprintf(a.opts.Out, "  '#' = additional country code conflicts\n")
	} else {
		fmt.Fprintf(a.opts.Out, "  '*' = first conflicting assignment for this range\n")
		fmt.Fprintf(a.opts.Out, "  '#' = additional conflicting assignment for this range\n")
	}

	type groupKey struct {
		numType, source string
		start, end      string
	}
	var order []groupKey
	groups := make(map[groupKey][]store.Comparison)
	for _, r := range results {
		key := groupKey{
			numType: string(r.First.NumType),
			source:  r.First.SourceType,
			start:   r.First.Start.String(),
			end:     r.First.End.String(),
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], r)
	}
	lastSource := ""
	for _, key := range order {
		if key.source != lastSource {
			fmt.Fprintf(a.opts.Out, "\nAssignments in '%s':\n", key.source)
			lastSource = key.source
		}
		first := groups[key][0].First
		fmt.Fprintf(a.opts.Out, "  %s %s (%s)\n", first.CountryCode, rangeString(first.Start, first.End, first.NumType), first.NumType)
		seen := make(map[string]bool)
		conflicts := 0
		for _, r := range groups[key] {
			secondKey := r.Second.SourceType + "\x00" + r.Second.Start.String() + "\x00" + r.Second.End.String() + "\x00" + r.Second.CountryCode
			if seen[secondKey] {
				continue
			}
			seen[secondKey] = true
			marker := compareMarker(r, conflicts)
			if marker == "*" || marker == "#" {
				conflicts++
			}
			fmt.Fprintf(a.opts.Out, "    %s %s %s %s\n", marker, r.Second.CountryCode, rangeString(r.Second.Start, r.Second.End, r.Second.NumType), r.Second.SourceType)
		}
	}
	fmt.Fprintln(a.opts.Out)
	return nil
}

func (a *App) compareSources() (first, second []string, err error) {
	all := sources.AssignmentsSourceTypes
	first = a.opts.Sources
	if len(first) == 0 {
		first = []string{sources.SourceRIR}
	}
	inFirst := make(map[string]bool, len(first))
	for _, src := range first {
		if src == sources.SourceBGP {
			return nil, nil, fmt.Errorf("bgp_snapshot is not an assignment source")
		}
		inFirst[src] = true
	}
	for _, src := range all {
		if !inFirst[src] {
			second = append(second, src)
		}
	}
	if len(second) == 0 {
		second = all
	}
	return first, second, nil
}

func compareMarker(r store.Comparison, conflicts int) string {
	if r.First.SourceType == r.Second.SourceType {
		return "<"
	}
	if r.First.CountryCode == r.Second.CountryCode {
		return ">"
	}
	if conflicts == 0 {
		return "*"
	}
	return "#"
}

func toAssignmentJSON(a store.Assignment) assignmentJSON {
	return assignmentJSON{
		NumType:     string(a.NumType),
		Source:      a.SourceType,
		CountryCode: a.CountryCode,
		Range:       rangeString(a.Start, a.End, a.NumType),
	}
}
