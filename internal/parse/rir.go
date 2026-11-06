// Package parse implements the dataset parsers used by the cache loader.
package parse

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"

	"github.com/zenware/netblocks/internal/countries"
	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

// Stats summarizes a parse run.
type Stats struct {
	Records int64
	Skipped int64
}

// Parser inserts one record at a time; app wires this to store writers.
type AssignmentFunc func(store.Assignment) error

// RIR parses an NRO extended delegation statistics file.
//
// Format:
//
//	version|registry|serial|records|startdate|enddate|UTCoffset
//	registry|cc|type|start|value|date|status[|opaque-id[|extensions...]]
//
// IPv4 "value" is the number of addresses, IPv6 "value" is the prefix length,
// and ASN "value" is the number of ASNs beginning at "start".
func RIR(ctx context.Context, r io.Reader, fn AssignmentFunc) (Stats, error) {
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
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 6 {
			stats.Skipped++
			continue
		}
		registry := strings.TrimSpace(fields[0])
		country := countries.NormalizeCode(fields[1])
		numType := strings.ToLower(strings.TrimSpace(fields[2]))
		startStr := strings.TrimSpace(fields[3])
		valueStr := strings.TrimSpace(fields[4])
		date := strings.TrimSpace(fields[5])
		status := ""
		if len(fields) >= 7 {
			status = strings.TrimSpace(fields[6])
		}
		opaque := ""
		if len(fields) >= 8 {
			opaque = strings.TrimSpace(fields[7])
		}

		// Version and summary lines are not records.
		if strings.EqualFold(date, "summary") || startStr == "*" || (numType != "ipv4" && numType != "ipv6" && numType != "asn") {
			stats.Skipped++
			continue
		}
		// ARIN marks reserved/available ranges with an empty country code.
		// Normalize those to "*" so --reserved-count and -t '*' can see them.
		if country == "" {
			country = "*"
		}

		a := store.Assignment{
			CountryCode: country,
			SourceType:  "rir",
			SourceName:  registry,
			Status:      status,
			StartDate:   date,
			OpaqueID:    opaque,
		}
		var err error
		switch numType {
		case "asn":
			a.NumType = netutil.ASN
			var start uint32
			start, err = netutil.ParseASN(startStr)
			if err != nil {
				stats.Skipped++
				continue
			}
			count, countErr := parseUint(valueStr)
			if countErr != nil || count == 0 {
				stats.Skipped++
				continue
			}
			a.Start = big.NewInt(int64(start))
			a.End = new(big.Int).Add(a.Start, new(big.Int).SetUint64(count-1))
			if a.End.BitLen() > 32 {
				stats.Skipped++
				continue
			}
		case "ipv4":
			a.NumType = netutil.IPv4
			var width int
			a.Start, width, err = netutil.ParseAddr(startStr)
			if err != nil || width != 32 {
				stats.Skipped++
				continue
			}
			count, countErr := parseUint(valueStr)
			if countErr != nil || count == 0 {
				stats.Skipped++
				continue
			}
			a.End = new(big.Int).Add(a.Start, new(big.Int).SetUint64(count-1))
			limit := new(big.Int).Lsh(big.NewInt(1), 32)
			if a.End.Cmp(limit) >= 0 {
				stats.Skipped++
				continue
			}
		case "ipv6":
			a.NumType = netutil.IPv6
			prefix := startStr
			if !strings.Contains(prefix, "/") {
				prefix = startStr + "/" + valueStr
			}
			a.Start, a.End, _, err = netutil.ParsePrefix(prefix)
			if err != nil {
				stats.Skipped++
				continue
			}
		}
		if err != nil || a.Start == nil || a.End == nil {
			stats.Skipped++
			continue
		}
		if err := fn(a); err != nil {
			return stats, fmt.Errorf("line %d: %w", lineNo, err)
		}
		stats.Records++
	}
	if err := sc.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}

func parseUint(s string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSpace(s), 10, 64)
}
