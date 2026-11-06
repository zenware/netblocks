package parse

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/sources"
	"github.com/zenware/netblocks/internal/store"
)

// ASNFunc inserts one announcement.
type ASNFunc func(store.ASNAnnouncement) error

// MaxMindASN imports GeoLite2-ASN CSV data from a .zip, a .csv, or a
// directory containing blocks CSVs. The autonomous_system_organization
// column is ignored; ASN descriptions come from cidr-report.
func MaxMindASN(ctx context.Context, path string, fn ASNFunc) (Stats, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Stats{}, err
	}
	if fi.IsDir() {
		return maxMindASNDir(ctx, path, fn)
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return maxMindASNZip(ctx, path, fn)
	case strings.HasSuffix(lower, ".csv"):
		f, err := os.Open(path)
		if err != nil {
			return Stats{}, err
		}
		defer f.Close()
		return readGeoLite2ASNBlocks(ctx, f, fn)
	default:
		return Stats{}, fmt.Errorf("unsupported MaxMind ASN file %s (expected .zip, .csv, or a directory)", path)
	}
}

func maxMindASNDir(ctx context.Context, dir string, fn ASNFunc) (Stats, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Stats{}, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if isGeoLite2ASNBlocksName(e.Name()) {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	if len(paths) == 0 {
		return Stats{}, fmt.Errorf("no GeoLite2 ASN blocks CSVs found in %s", dir)
	}
	var total Stats
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return total, err
		}
		stats, err := readGeoLite2ASNBlocks(ctx, f, fn)
		f.Close()
		if err != nil {
			return total, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		total.Records += stats.Records
		total.Skipped += stats.Skipped
	}
	return total, nil
}

func maxMindASNZip(ctx context.Context, path string, fn ASNFunc) (Stats, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return Stats{}, err
	}
	defer zr.Close()
	var entries []*zip.File
	for _, f := range zr.File {
		if isGeoLite2ASNBlocksName(filepath.Base(f.Name)) {
			entries = append(entries, f)
		}
	}
	if len(entries) == 0 {
		return Stats{}, fmt.Errorf("%s does not contain GeoLite2 ASN blocks CSVs", path)
	}
	var total Stats
	for _, entry := range entries {
		rc, err := entry.Open()
		if err != nil {
			return total, err
		}
		stats, err := readGeoLite2ASNBlocks(ctx, rc, fn)
		rc.Close()
		if err != nil {
			return total, fmt.Errorf("%s: %w", filepath.Base(entry.Name), err)
		}
		total.Records += stats.Records
		total.Skipped += stats.Skipped
	}
	return total, nil
}

func isGeoLite2ASNBlocksName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "blocks-ipv4.csv") || strings.Contains(lower, "blocks-ipv6.csv")
}

func readGeoLite2ASNBlocks(ctx context.Context, r io.Reader, fn ASNFunc) (Stats, error) {
	var stats Stats
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	header, err := cr.Read()
	if err != nil {
		return stats, err
	}
	idx := make(map[string]int)
	for i, h := range header {
		idx[strings.TrimSpace(strings.ToLower(h))] = i
	}
	netIdx, ok := idx["network"]
	if !ok {
		return stats, errors.New("ASN blocks CSV is missing network or autonomous_system_number column")
	}
	asnIdx, ok := idx["autonomous_system_number"]
	if !ok {
		return stats, errors.New("ASN blocks CSV is missing network or autonomous_system_number column")
	}
	line := 0
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		line++
		if line%10000 == 0 {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
		}
		if err != nil {
			if recoverableCSVError(err) {
				stats.Skipped++
				continue
			}
			return stats, err
		}
		if len(row) <= netIdx || len(row) <= asnIdx {
			stats.Skipped++
			continue
		}
		start, end, width, err := netutil.ParsePrefix(strings.TrimSpace(row[netIdx]))
		if err != nil {
			stats.Skipped++
			continue
		}
		asn, err := strconv.ParseUint(strings.TrimSpace(row[asnIdx]), 10, 32)
		if err != nil || asn == 0 {
			stats.Skipped++
			continue
		}
		if err := fn(store.ASNAnnouncement{
			Start:      start,
			End:        end,
			NumType:    netutil.TypeForBits(width),
			ASN:        uint32(asn),
			SourceType: sources.SourceMaxMind,
			SourceName: sources.SourceMaxMind,
		}); err != nil {
			return stats, err
		}
		stats.Records++
	}
	return stats, nil
}
