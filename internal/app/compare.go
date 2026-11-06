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
	if a.opts.JSON {
		return a.compareJSON(ctx, st, cc, types, firstSources, secondSources)
	}
	return a.compareText(ctx, st, cc, types, firstSources, secondSources)
}

// isSelfPair reports whether a comparison pairs an assignment with itself;
// --compare-all lets a source compare with itself and those pairs carry no
// information.
func isSelfPair(r store.Comparison) bool {
	return r.First.SourceType == r.Second.SourceType &&
		r.First.Start.Cmp(r.Second.Start) == 0 &&
		r.First.End.Cmp(r.Second.End) == 0
}

func (a *App) compareJSON(ctx context.Context, st *store.Store, cc string, types []netutil.NumType, firstSources, secondSources []string) error {
	var (
		results []store.Comparison
		err     error
	)
	if a.opts.CompareAll {
		results, err = st.Compare(ctx, cc, types, firstSources, secondSources)
	} else {
		results, err = st.Conflicts(ctx, cc, types, firstSources, secondSources)
	}
	if err != nil {
		return err
	}
	filtered := results[:0]
	for _, r := range results {
		if isSelfPair(r) {
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

type firstRangeKey struct {
	numType, source string
	start, end      string
}

type conflictGroupKey struct {
	source, country string
}

type conflictGroup struct {
	count    int
	examples []store.Assignment
}

// compareText streams comparisons in store order and prints them. Default
// conflict mode groups conflicts per first range by second source and
// country; --compare-all prints one line per overlapping pair.
func (a *App) compareText(ctx context.Context, st *store.Store, cc string, types []netutil.NumType, firstSources, secondSources []string) error {
	var (
		started    bool
		lastSource string
		haveKey    bool
		curKey     firstRangeKey
		// --compare-all state, reset for every first range.
		seen      map[string]bool
		conflicts int
		// conflict mode state, reset for every first range.
		groups map[conflictGroupKey]*conflictGroup
		order  []conflictGroupKey
	)

	header := func() {
		if started {
			return
		}
		started = true
		fmt.Fprintln(a.opts.Out, "Comparing assignments with overlapping assignments in other data sources...")
		fmt.Fprintln(a.opts.Out)
		fmt.Fprintln(a.opts.Out, "Legend:")
		if a.opts.CompareAll {
			fmt.Fprintf(a.opts.Out, "  '<' = overlapping range from the same source\n")
			fmt.Fprintf(a.opts.Out, "  '>' = overlapping assignment with the same country code %s\n", cc)
			fmt.Fprintf(a.opts.Out, "  '*' = overlapping assignment with a different country code\n")
			fmt.Fprintf(a.opts.Out, "  '#' = additional country code conflicts\n")
			return
		}
		fmt.Fprintf(a.opts.Out, "  '*' = conflicting assignments in another source, grouped by source and country\n")
	}

	flush := func() {
		if !haveKey {
			return
		}
		haveKey = false
		if a.opts.CompareAll {
			return
		}
		keys := make([]conflictGroupKey, len(order))
		copy(keys, order)
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].source != keys[j].source {
				return keys[i].source < keys[j].source
			}
			return keys[i].country < keys[j].country
		})
		shown := keys
		hidden := 0
		if a.opts.CompareLimit > 0 && len(keys) > a.opts.CompareLimit {
			shown = keys[:a.opts.CompareLimit]
			hidden = len(keys) - a.opts.CompareLimit
		}
		for _, key := range shown {
			group := groups[key]
			parts := make([]string, len(group.examples))
			for i, example := range group.examples {
				parts[i] = networkString(example.Start, example.End, example.NumType)
			}
			examples := strings.Join(parts, ", ")
			if group.count > len(group.examples) {
				examples += ", ..."
			}
			fmt.Fprintf(a.opts.Out, "    * %s %s: %d ranges (%s)\n", key.country, key.source, group.count, examples)
		}
		if hidden > 0 {
			fmt.Fprintf(a.opts.Out, "    ... %d more conflict groups\n", hidden)
		}
	}

	err := st.EachComparison(ctx, cc, types, firstSources, secondSources, !a.opts.CompareAll, func(r store.Comparison) error {
		if isSelfPair(r) {
			return nil
		}
		header()
		key := firstRangeKey{
			numType: string(r.First.NumType),
			source:  r.First.SourceType,
			start:   r.First.Start.String(),
			end:     r.First.End.String(),
		}
		if !haveKey || key != curKey {
			flush()
			if key.source != lastSource {
				fmt.Fprintf(a.opts.Out, "\nAssignments in '%s':\n", key.source)
				lastSource = key.source
			}
			fmt.Fprintf(a.opts.Out, "  %s %s (%s)\n", r.First.CountryCode, rangeString(r.First.Start, r.First.End, r.First.NumType), r.First.NumType)
			curKey = key
			haveKey = true
			seen = make(map[string]bool)
			conflicts = 0
			groups = make(map[conflictGroupKey]*conflictGroup)
			order = nil
		}
		if a.opts.CompareAll {
			secondKey := r.Second.SourceType + "\x00" + r.Second.Start.String() + "\x00" + r.Second.End.String() + "\x00" + r.Second.CountryCode
			if seen[secondKey] {
				return nil
			}
			seen[secondKey] = true
			marker := compareMarker(r, conflicts)
			if marker == "*" || marker == "#" {
				conflicts++
			}
			fmt.Fprintf(a.opts.Out, "    %s %s %s %s\n", marker, r.Second.CountryCode, rangeString(r.Second.Start, r.Second.End, r.Second.NumType), r.Second.SourceType)
			return nil
		}
		groupKey := conflictGroupKey{source: r.Second.SourceType, country: r.Second.CountryCode}
		group, ok := groups[groupKey]
		if !ok {
			group = &conflictGroup{}
			groups[groupKey] = group
			order = append(order, groupKey)
		}
		group.count++
		if len(group.examples) < 3 {
			group.examples = append(group.examples, r.Second)
		}
		return nil
	})
	if err != nil {
		return err
	}
	flush()
	if !started {
		if a.opts.CompareAll {
			fmt.Fprintln(a.opts.Out, "No overlapping assignments found.")
		} else {
			fmt.Fprintln(a.opts.Out, "No country-code conflicts found.")
		}
		return nil
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
