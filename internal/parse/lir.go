package parse

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/zenware/netblocks/internal/countries"
	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

// LIR parses a RIPE split inetnum/inet6num file in RPSL format.
//
// Each object is a blank-line separated block. The first valid country
// attribute wins; later country attributes are treated as alternates.
func LIR(ctx context.Context, r io.Reader, fn AssignmentFunc) (Stats, error) {
	var stats Stats
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)

	var (
		start, end   *big.Int
		numType      netutil.NumType
		country      string
		status       string
		netname      string
		recordSeen   bool
		invalidRange bool
	)
	flush := func() error {
		defer func() {
			start, end, numType, country, status, netname = nil, nil, "", "", "", ""
			recordSeen, invalidRange = false, false
		}()
		if !recordSeen || invalidRange || start == nil || end == nil {
			return nil
		}
		if lirPlaceholder(netname, start, end, numType) {
			stats.Skipped++
			return nil
		}
		if country == "" {
			stats.Skipped++
			return nil
		}
		if start.Cmp(end) > 0 {
			stats.Skipped++
			return nil
		}
		a := store.Assignment{
			Start:       start,
			End:         end,
			NumType:     numType,
			CountryCode: country,
			SourceType:  "lir",
			SourceName:  "ripencc",
			Status:      status,
		}
		if err := fn(a); err != nil {
			return err
		}
		stats.Records++
		return nil
	}

	lineNo := 0
	for sc.Scan() {
		lineNo++
		if lineNo%10000 == 0 {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
		}
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return stats, fmt.Errorf("line %d: %w", lineNo, err)
			}
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "%") {
			continue
		}
		// Continuation lines belong to the previous attribute.
		if line[0] == ' ' || line[0] == '\t' || line[0] == '+' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "inetnum":
			if start != nil {
				// A new range before a blank line: flush the old object.
				if err := flush(); err != nil {
					return stats, fmt.Errorf("line %d: %w", lineNo, err)
				}
			}
			recordSeen = true
			a, b, err := parseIPv4Range(value)
			if err != nil {
				invalidRange = true
				continue
			}
			start, end, numType = a, b, netutil.IPv4
		case "inet6num":
			if start != nil {
				if err := flush(); err != nil {
					return stats, fmt.Errorf("line %d: %w", lineNo, err)
				}
			}
			recordSeen = true
			a, b, _, err := netutil.ParsePrefix(value)
			if err != nil {
				invalidRange = true
				continue
			}
			start, end, numType = a, b, netutil.IPv6
		case "country":
			if country == "" {
				cc := countries.NormalizeCode(value)
				if len(cc) == 2 || cc == "*" {
					country = cc
				}
			}
		case "status":
			if status == "" {
				status = value
			}
		case "netname":
			if netname == "" {
				netname = value
			}
		}
	}
	if err := sc.Err(); err != nil {
		return stats, err
	}
	if err := flush(); err != nil {
		return stats, err
	}
	return stats, nil
}

func lirPlaceholder(netname string, start, end *big.Int, numType netutil.NumType) bool {
	switch strings.ToUpper(strings.TrimSpace(netname)) {
	case "IANA-BLK", "NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK":
		return true
	}
	// A record covering the entire address space is never a country
	// assignment, even when RIPE labels it with a country code.
	if start.Sign() == 0 {
		limit := new(big.Int).Lsh(big.NewInt(1), uint(numType.Bits()))
		if end.Cmp(new(big.Int).Sub(limit, big.NewInt(1))) == 0 {
			return true
		}
	}
	return false
}

func parseIPv4Range(s string) (start, end *big.Int, err error) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("invalid inetnum %q", s)
	}
	start, width, err := netutil.ParseAddr(parts[0])
	if err != nil {
		return nil, nil, err
	}
	if width != 32 {
		return nil, nil, fmt.Errorf("inetnum %q is not IPv4", s)
	}
	end, width, err = netutil.ParseAddr(parts[1])
	if err != nil {
		return nil, nil, err
	}
	if width != 32 {
		return nil, nil, fmt.Errorf("inetnum %q is not IPv4", s)
	}
	return start, end, nil
}
