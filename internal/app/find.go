package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type asnMatchJSON struct {
	ASN         uint32 `json:"asn"`
	Description string `json:"description"`
}

// findASN searches cached ASN descriptions and prints one ASN per match.
func (a *App) findASN(ctx context.Context) error {
	pattern := strings.TrimSpace(a.opts.FindASN)
	terms := strings.Fields(pattern)
	if len(terms) == 0 {
		return errors.New("empty --find-asn pattern")
	}
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	// Fetch one extra row to distinguish "exactly at the limit" from
	// "truncated".
	queryLimit := a.opts.FindLimit
	if queryLimit > 0 {
		queryLimit++
	}
	matches, err := st.FindASNDescriptions(ctx, terms, queryLimit)
	if err != nil {
		return err
	}
	truncated := false
	if a.opts.FindLimit > 0 && len(matches) > a.opts.FindLimit {
		matches = matches[:a.opts.FindLimit]
		truncated = true
	}

	if a.opts.CSV || a.opts.TSV {
		rows := make([][]string, len(matches))
		for i, match := range matches {
			rows[i] = []string{fmt.Sprintf("%d", match.ASN), match.Description}
		}
		return a.writeTable([]string{"asn", "description"}, rows)
	}
	if a.opts.JSON {
		results := make([]asnMatchJSON, 0, len(matches))
		for _, match := range matches {
			results = append(results, asnMatchJSON{ASN: match.ASN, Description: match.Description})
		}
		return a.writeJSON(map[string]any{
			"query":     "find-asn",
			"match":     pattern,
			"results":   results,
			"truncated": truncated,
		})
	}
	if len(matches) == 0 {
		fmt.Fprintf(a.opts.Out, "No ASN descriptions matched %q.\n", pattern)
		return nil
	}
	for _, match := range matches {
		fmt.Fprintf(a.opts.Out, "AS%d\t%s\n", match.ASN, match.Description)
	}
	if truncated {
		fmt.Fprintf(a.opts.Out, "... showing first %d matches (use --find-limit 0 to show all)\n", len(matches))
	}
	return nil
}
