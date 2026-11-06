package parse

import (
	"bufio"
	"context"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"

	"github.com/zenware/netblocks/internal/netutil"
)

var asnDescriptionRE = regexp.MustCompile(`(?i)as=AS(\d+)&view=2\.0">[^<]*</a>(.*)$`)

// ASNDescriptions parses the cidr-report.org autnums.html mapping.
func ASNDescriptions(ctx context.Context, r io.Reader, fn func(asn uint32, description string) error) (Stats, error) {
	var stats Stats
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if lineNo%10000 == 0 {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
		}
		line := strings.TrimSpace(sc.Text())
		m := asnDescriptionRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		asn, err := netutil.ParseASN(m[1])
		if err != nil {
			stats.Skipped++
			continue
		}
		description := strings.TrimSpace(html.UnescapeString(m[2]))
		description = strings.TrimSpace(strings.TrimPrefix(description, "-"))
		if description == "" {
			stats.Skipped++
			continue
		}
		if err := fn(asn, description); err != nil {
			return stats, fmt.Errorf("line %d: %w", lineNo, err)
		}
		stats.Records++
	}
	if err := sc.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}
