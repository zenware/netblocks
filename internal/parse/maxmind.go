package parse

import (
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zenware/netblocks/internal/countries"
	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

// MaxMind imports a MaxMind-compatible country database. It accepts:
//   - a GeoLite2 Country CSV .zip,
//   - a directory containing GeoLite2 blocks and locations CSVs,
//   - a legacy GeoIPCountryCSV/GeoIPv6 CSV (optionally gzip-compressed).
func MaxMind(ctx context.Context, path string, fn AssignmentFunc) (Stats, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Stats{}, err
	}
	if fi.IsDir() {
		return maxMindDir(ctx, path, fn)
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return maxMindZip(ctx, path, fn)
	case strings.HasSuffix(lower, ".gz"):
		f, err := os.Open(path)
		if err != nil {
			return Stats{}, err
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			return Stats{}, err
		}
		defer zr.Close()
		return legacyCSV(ctx, zr, filepath.Base(path), fn)
	case strings.HasSuffix(lower, ".csv"):
		f, err := os.Open(path)
		if err != nil {
			return Stats{}, err
		}
		defer f.Close()
		br := bufio.NewReader(f)
		header, _ := br.Peek(64)
		if isGeoLite2BlocksHeader(header) {
			return Stats{}, fmt.Errorf("%s is a GeoLite2 blocks file; provide the directory containing the locations CSV or a GeoLite2 zip", path)
		}
		return legacyCSV(ctx, br, filepath.Base(path), fn)
	default:
		return Stats{}, fmt.Errorf("unsupported MaxMind file %s (expected .zip, .csv, .csv.gz, or a directory)", path)
	}
}

func isGeoLite2BlocksHeader(b []byte) bool {
	first := strings.ToLower(strings.SplitN(string(b), "\n", 2)[0])
	return strings.HasPrefix(first, "network,")
}

func maxMindDir(ctx context.Context, dir string, fn AssignmentFunc) (Stats, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Stats{}, err
	}
	var locationsPath string
	var blocks []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)
		switch {
		case strings.Contains(lower, "locations-en.csv"):
			locationsPath = filepath.Join(dir, name)
		case strings.Contains(lower, "locations") && strings.HasSuffix(lower, ".csv") && locationsPath == "":
			locationsPath = filepath.Join(dir, name)
		case strings.Contains(lower, "blocks-ipv4.csv"), strings.Contains(lower, "blocks-ipv6.csv"):
			blocks = append(blocks, filepath.Join(dir, name))
		}
	}
	if locationsPath == "" || len(blocks) == 0 {
		// Fall back to legacy CSV files in the directory.
		var stats Stats
		legacy := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			lower := strings.ToLower(e.Name())
			if strings.Contains(lower, "geoipcountrycsv") || strings.Contains(lower, "geoipv6") {
				s, err := MaxMind(ctx, filepath.Join(dir, e.Name()), fn)
				if err != nil {
					return stats, err
				}
				stats.Records += s.Records
				stats.Skipped += s.Skipped
				legacy++
			}
		}
		if legacy > 0 {
			return stats, nil
		}
		return Stats{}, fmt.Errorf("no GeoLite2 locations/blocks CSVs found in %s", dir)
	}
	lf, err := os.Open(locationsPath)
	if err != nil {
		return Stats{}, err
	}
	locations, err := readLocations(lf)
	lf.Close()
	if err != nil {
		return Stats{}, err
	}
	var stats Stats
	for _, blockPath := range blocks {
		f, err := os.Open(blockPath)
		if err != nil {
			return stats, err
		}
		s, err := readGeoLite2Blocks(ctx, f, locations, fn)
		f.Close()
		stats.Records += s.Records
		stats.Skipped += s.Skipped
		if err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func maxMindZip(ctx context.Context, path string, fn AssignmentFunc) (Stats, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return Stats{}, err
	}
	defer zr.Close()

	var locationsFile *zip.File
	var blocks []*zip.File
	for _, f := range zr.File {
		name := strings.ToLower(filepath.Base(f.Name))
		switch {
		case strings.Contains(name, "locations-en.csv"):
			locationsFile = f
		case strings.Contains(name, "locations") && strings.HasSuffix(name, ".csv") && locationsFile == nil:
			locationsFile = f
		case strings.Contains(name, "blocks-ipv4.csv"), strings.Contains(name, "blocks-ipv6.csv"):
			blocks = append(blocks, f)
		}
	}
	if locationsFile == nil || len(blocks) == 0 {
		// Some legacy zips contain a single CSV with the legacy schema.
		for _, f := range zr.File {
			if strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
				rc, err := f.Open()
				if err != nil {
					return Stats{}, err
				}
				s, legacyErr := legacyCSV(ctx, rc, filepath.Base(path), fn)
				rc.Close()
				if legacyErr == nil && s.Records > 0 {
					return s, nil
				}
			}
		}
		return Stats{}, fmt.Errorf("%s does not contain GeoLite2 country blocks and locations CSVs", path)
	}

	rc, err := locationsFile.Open()
	if err != nil {
		return Stats{}, err
	}
	locations, err := readLocations(rc)
	rc.Close()
	if err != nil {
		return Stats{}, err
	}
	var stats Stats
	for _, f := range blocks {
		rc, err := f.Open()
		if err != nil {
			return stats, err
		}
		s, err := readGeoLite2Blocks(ctx, rc, locations, fn)
		rc.Close()
		stats.Records += s.Records
		stats.Skipped += s.Skipped
		if err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func readLocations(r io.Reader) (map[uint64]string, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	header, err := cr.Read()
	if err != nil {
		return nil, err
	}
	idIdx, ccIdx := -1, -1
	for i, h := range header {
		switch strings.TrimSpace(strings.ToLower(h)) {
		case "geoname_id":
			idIdx = i
		case "country_iso_code":
			ccIdx = i
		}
	}
	if idIdx < 0 || ccIdx < 0 {
		return nil, errors.New("locations CSV is missing geoname_id or country_iso_code")
	}
	out := make(map[uint64]string)
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if recoverableCSVError(err) {
				continue
			}
			return nil, err
		}
		if len(row) <= idIdx || len(row) <= ccIdx {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSpace(row[idIdx]), 10, 64)
		if err != nil {
			continue
		}
		cc := countries.NormalizeCode(row[ccIdx])
		if cc != "" && cc != "*" {
			out[id] = cc
		}
	}
	return out, nil
}

func readGeoLite2Blocks(ctx context.Context, r io.Reader, locations map[uint64]string, fn AssignmentFunc) (Stats, error) {
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
		return stats, errors.New("blocks CSV is missing network column")
	}
	idxs := []int{}
	for _, key := range []string{"geoname_id", "registered_country_geoname_id", "represented_country_geoname_id"} {
		if i, ok := idx[key]; ok {
			idxs = append(idxs, i)
		}
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
		if len(row) <= netIdx {
			stats.Skipped++
			continue
		}
		start, end, width, err := netutil.ParsePrefix(row[netIdx])
		if err != nil {
			stats.Skipped++
			continue
		}
		cc := geonameCountry(row, idxs, locations)
		if cc == "" {
			stats.Skipped++
			continue
		}
		numType := netutil.TypeForBits(width)
		if err := fn(store.Assignment{
			Start:       start,
			End:         end,
			NumType:     numType,
			CountryCode: cc,
			SourceType:  "maxmind",
			SourceName:  "maxmind",
		}); err != nil {
			return stats, err
		}
		stats.Records++
	}
	return stats, nil
}

func recoverableCSVError(err error) bool {
	var parseErr *csv.ParseError
	if !errors.As(err, &parseErr) {
		return false
	}
	return errors.Is(parseErr.Err, csv.ErrFieldCount) ||
		errors.Is(parseErr.Err, csv.ErrBareQuote) ||
		errors.Is(parseErr.Err, csv.ErrQuote)
}

func geonameCountry(row []string, idxs []int, locations map[uint64]string) string {
	for _, i := range idxs {
		if i >= len(row) {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSpace(row[i]), 10, 64)
		if err != nil {
			continue
		}
		if cc := locations[id]; cc != "" {
			return cc
		}
	}
	return ""
}

// legacyCSV parses the old MaxMind GeoIPCountryWhois/GeoIPv6 six-column CSV.
func legacyCSV(ctx context.Context, r io.Reader, sourceName string, fn AssignmentFunc) (Stats, error) {
	var stats Stats
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true
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
		if len(row) < 5 {
			stats.Skipped++
			continue
		}
		startStr := strings.TrimSpace(row[0])
		endStr := strings.TrimSpace(row[1])
		startNumStr := strings.TrimSpace(row[2])
		endNumStr := strings.TrimSpace(row[3])
		cc := countries.NormalizeCode(row[4])
		if cc == "" {
			stats.Skipped++
			continue
		}
		var start, end *big.Int
		var width int
		start, width, err = netutil.ParseAddr(startStr)
		if err != nil {
			if startNumStr == "" {
				stats.Skipped++
				continue
			}
			var ok bool
			start, ok = new(big.Int).SetString(startNumStr, 10)
			if !ok {
				stats.Skipped++
				continue
			}
			// IPv6 legacy rows use colon-separated addresses; without a
			// parseable start address the width cannot be inferred safely.
			if endStr == "" {
				stats.Skipped++
				continue
			}
			if _, width, err = netutil.ParseAddr(endStr); err != nil {
				stats.Skipped++
				continue
			}
		}
		if endNumStr != "" {
			end, _ = new(big.Int).SetString(endNumStr, 10)
		}
		if end == nil {
			end, _, err = netutil.ParseAddr(endStr)
			if err != nil {
				stats.Skipped++
				continue
			}
		}
		if width == 0 {
			width = 32
		}
		if start.Cmp(end) > 0 {
			stats.Skipped++
			continue
		}
		numType := netutil.TypeForBits(width)
		if err := fn(store.Assignment{
			Start:       start,
			End:         end,
			NumType:     numType,
			CountryCode: cc,
			SourceType:  "maxmind",
			SourceName:  sourceName,
		}); err != nil {
			return stats, err
		}
		stats.Records++
	}
	return stats, nil
}
