package parse

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/sources"
	"github.com/zenware/netblocks/internal/store"
)

var asnTokenRE = regexp.MustCompile(`\d+`)

// BGP parses the textual "show ip bgp" style RouteViews snapshot. Only
// announcement lines matter; headers and comments are ignored.
//
// Example:
//
//   - 1.0.0.0/24  12.0.1.63  0  0  0 7018 13335 i
func BGP(ctx context.Context, r io.Reader, fn func(store.ASNAnnouncement) error) (Stats, error) {
	var stats Stats
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if lineNo%10000 == 0 {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			stats.Skipped++
			continue
		}
		prefix := fields[1]
		start, end, width, err := netutil.ParsePrefix(prefix)
		if err != nil {
			stats.Skipped++
			continue
		}
		originCode := strings.ToLower(fields[len(fields)-1])
		if originCode != "i" && originCode != "e" && originCode != "?" {
			stats.Skipped++
			continue
		}
		// The AS path follows the metric, local preference, and weight
		// columns. Field positions are stable in RouteViews text dumps.
		path := fields[6 : len(fields)-1]
		var asn uint32
		found := false
		for i := len(path) - 1; i >= 0; i-- {
			matches := asnTokenRE.FindAllString(path[i], -1)
			if len(matches) == 0 {
				continue
			}
			candidate, err := netutil.ParseASN(matches[len(matches)-1])
			if err == nil {
				asn = candidate
				found = true
				break
			}
		}
		if !found || asn == 0 {
			stats.Skipped++
			continue
		}
		numType := netutil.TypeForBits(width)
		if err := fn(store.ASNAnnouncement{
			Start:      start,
			End:        end,
			NumType:    numType,
			ASN:        asn,
			SourceType: sources.SourceBGP,
			SourceName: sources.BGPNameRouteViews,
		}); err != nil {
			return stats, fmt.Errorf("line %d: %w", lineNo, err)
		}
		stats.Records++
	}
	if err := sc.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}
