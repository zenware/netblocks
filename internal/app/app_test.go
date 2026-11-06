package app_test

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zenware/netblocks/internal/app"
	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

func copyFixture(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeLIRFixture(t *testing.T, dst string) {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/test_lir_data.gz")
	if err != nil {
		t.Fatal(err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(gr)
	if err != nil {
		t.Fatal(err)
	}
	gr.Close()
	data = append(data, []byte("\ninetnum: 203.81.64.0 - 203.81.95.255\ncountry: MM\nstatus: ASSIGNED PA\n\ninetnum: 203.81.64.0 - 203.81.64.255\ncountry: IN\nstatus: ASSIGNED PA\n\n")...)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copyFixture(t, "../../testdata/test_rir_data", filepath.Join(dir, "delegated-apnic-latest"))
	writeLIRFixture(t, filepath.Join(dir, "ripe.db.inetnum.gz"))
	if _, _, err := runApp(t, dir, app.Options{Mode: "reload-rir"}); err != nil {
		t.Fatalf("reload-rir: %v", err)
	}
	if _, _, err := runApp(t, dir, app.Options{Mode: "reload-lir"}); err != nil {
		t.Fatalf("reload-lir: %v", err)
	}
	return dir
}

func runApp(t *testing.T, cacheDir string, opts app.Options) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	opts.CacheDir = cacheDir
	opts.Out = &out
	opts.Err = &errOut
	opts.Offline = true
	err := app.Run(context.Background(), opts)
	return out.String(), errOut.String(), err
}

func TestLookupModes(t *testing.T) {
	dir := setupCache(t)

	out, _, err := runApp(t, dir, app.Options{Mode: "code", Code: "MM", Merge: true, ExpandASNs: true})
	if err != nil {
		t.Fatal(err)
	}
	if out != "203.81.64.0/19\n203.81.160.0/20\n" {
		t.Fatalf("MM output = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "code", Code: "JP", TypeFilter: "ipv6", Merge: false, ExpandASNs: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "2001:200::/35\n2001:200:2000::/35\n2001:200:4000::/34\n2001:200:8000::/33\n"
	if out != want {
		t.Fatalf("JP no-merge = %q, want %q", out, want)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "code", Code: "JP", TypeFilter: "ipv6", Merge: true, ExpandASNs: true})
	if err != nil {
		t.Fatal(err)
	}
	if out != "2001:200::/32\n" {
		t.Fatalf("JP merged = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "name", Name: "Myanmar", Merge: true, ExpandASNs: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "203.81.64.0/19") {
		t.Fatalf("name output = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "asn", ASN: "681"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "AS country code: NZ") || !strings.Contains(out, "New Zealand") {
		t.Fatalf("asn output = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "what-country", WhatCountry: "MM"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Myanmar") {
		t.Fatalf("what-country output = %q", out)
	}
}

func TestReverseLookups(t *testing.T) {
	dir := setupCache(t)

	out, _, err := runApp(t, dir, app.Options{Mode: "ip", IP: "175.45.176.100"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "RIR country code: KP") {
		t.Fatalf("RIR reverse = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "ip", IP: "80.16.151.184"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "LIR country code: IT") || !strings.Contains(out, "Not found in RIR db") {
		t.Fatalf("LIR reverse = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "ip", IP: "128.0.0.0", Sources: []string{"lir"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "LIR country code: RO") {
		t.Fatalf("LIR RO reverse = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "ip", IP: "2001:670:85::", Sources: []string{"lir"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "LIR country code: FI") {
		t.Fatalf("LIR FI reverse = %q", out)
	}
}

func TestJSONOutput(t *testing.T) {
	dir := setupCache(t)
	out, _, err := runApp(t, dir, app.Options{Mode: "code", Code: "MM", JSON: true, Merge: true, ExpandASNs: true})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Query       string   `json:"query"`
		CountryCode string   `json:"country_code"`
		CountryName string   `json:"country_name"`
		IPv4        []string `json:"ipv4"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if result.Query != "country" || result.CountryCode != "MM" || result.CountryName != "Myanmar" || len(result.IPv4) != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestCompareStatsAndExport(t *testing.T) {
	dir := setupCache(t)

	out, _, err := runApp(t, dir, app.Options{Mode: "compare", Compare: "MM"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "IN") || !strings.Contains(out, "Assignments in 'rir'") {
		t.Fatalf("conflict compare output = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "compare", Compare: "MM", CompareAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Assignments in 'rir'") || !strings.Contains(out, "MM") {
		t.Fatalf("compare-all output = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "reserved-count"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "assigned") {
		t.Fatalf("reserved output = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "stats"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "rir") || !strings.Contains(out, "lir") {
		t.Fatalf("stats output = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "list-cc", JSON: false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "MM\tMyanmar") {
		t.Fatalf("list-cc output missing MM")
	}

	v4 := filepath.Join(t.TempDir(), "v4.csv")
	v6 := filepath.Join(t.TempDir(), "v6.csv")
	asn := filepath.Join(t.TempDir(), "asn.csv")
	_, errOut, err := runApp(t, dir, app.Options{Mode: "export-geoip", GeoIPV4File: v4, GeoIPV6File: v6, GeoIPASNFile: asn, Merge: true})
	if err != nil {
		t.Fatal(err)
	}
	v4Data, err := os.ReadFile(v4)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(v4Data), "203.81.64.0") || !strings.Contains(string(v4Data), "MM") {
		t.Fatalf("v4 export = %q", v4Data)
	}
	v6Data, err := os.ReadFile(v6)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(v6Data), "2001:200::") || !strings.Contains(string(v6Data), "JP") {
		t.Fatalf("v6 export = %q", v6Data)
	}
	if _, err := os.Stat(asn); !os.IsNotExist(err) {
		t.Fatalf("empty ASN export should have been removed (stderr=%q)", errOut)
	}
}

func writeMaxMindZip(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	write := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("GeoLite2-Country-CSV_20260101/GeoLite2-Country-Locations-en.csv",
		"geoname_id,locale_code,continent_code,continent_name,country_iso_code,country_name,is_in_european_union\n"+
			"1,en,NA,North America,US,United States,0\n")
	write("GeoLite2-Country-CSV_20260101/GeoLite2-Country-Blocks-IPv4.csv",
		"network,geoname_id,registered_country_geoname_id,represented_country_geoname_id,is_anonymous_proxy,is_satellite_provider,is_anycast\n"+
			"8.8.8.0/24,1,1,,0,0,0\n")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMaxMindImport(t *testing.T) {
	dir := setupCache(t)
	zipPath := filepath.Join(dir, "GeoLite2-Country-CSV.zip")
	writeMaxMindZip(t, zipPath)

	if _, _, err := runApp(t, dir, app.Options{Mode: "reload-maxmind"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := runApp(t, dir, app.Options{Mode: "code", Code: "US", TypeFilter: "ipv4", Sources: []string{"maxmind"}, Merge: true})
	if err != nil {
		t.Fatal(err)
	}
	if out != "8.8.8.0/24\n" {
		t.Fatalf("maxmind blocks = %q", out)
	}
	out, _, err = runApp(t, dir, app.Options{Mode: "ip", IP: "8.8.8.8"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "MaxMind country code: US") {
		t.Fatalf("reverse maxmind = %q", out)
	}

	// -r/--import-maxmind must work with an explicit path.
	if _, _, err := runApp(t, dir, app.Options{Mode: "import-maxmind", ImportMaxMind: zipPath}); err != nil {
		t.Fatal(err)
	}
}

func TestASNDescriptionImport(t *testing.T) {
	dir := setupCache(t)
	html := `<a href="/cgi-bin/as-report?as=AS681&view=2.0">AS681</a> APNIC-TEST, NZ`
	if err := os.WriteFile(filepath.Join(dir, "autnums.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runApp(t, dir, app.Options{Mode: "reload-asn-descriptions"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := runApp(t, dir, app.Options{Mode: "asn", ASN: "681"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "AS description: APNIC-TEST, NZ") {
		t.Fatalf("asn description = %q", out)
	}
}

func TestNoExpandASNs(t *testing.T) {
	dir := setupCache(t)
	// The fixture cache has single-ASN records; this asserts the flag path.
	out, _, err := runApp(t, dir, app.Options{Mode: "code", Code: "JP", TypeFilter: "asn", ExpandASNs: false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected ASN output")
	}
}

func TestIPv6BGPImportAndExport(t *testing.T) {
	dir := setupCache(t)
	copyFixture(t, "../../testdata/test_bgp_mrt.bz2", filepath.Join(dir, "route-views6-rib.bz2"))

	if _, _, err := runApp(t, dir, app.Options{
		Mode:       "reload-asn-assignments",
		BGPSources: []string{"routeviews6"},
	}); err != nil {
		t.Fatal(err)
	}

	out, _, err := runApp(t, dir, app.Options{Mode: "lookup-org-by-ip", LookupOrgIP: "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "announced by AS15169") {
		t.Fatalf("ipv6 lookup = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "stats"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "bgp_snapshot") || !strings.Contains(out, "ipv6") {
		t.Fatalf("stats = %q", out)
	}

	exportDir := t.TempDir()
	v4 := filepath.Join(exportDir, "v4.csv")
	v6 := filepath.Join(exportDir, "v6.csv")
	asn4 := filepath.Join(exportDir, "asn4.csv")
	asn6 := filepath.Join(exportDir, "asn6.csv")
	if _, _, err := runApp(t, dir, app.Options{
		Mode:           "export-geoip",
		GeoIPV4File:    v4,
		GeoIPV6File:    v6,
		GeoIPASNFile:   asn4,
		GeoIPASNv6File: asn6,
		Merge:          true,
	}); err != nil {
		t.Fatal(err)
	}
	asn6Data, err := os.ReadFile(asn6)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(asn6Data), "2001:db8::") || !strings.Contains(string(asn6Data), "AS15169") {
		t.Fatalf("ipv6 ASN export = %q", asn6Data)
	}
}

func TestASNPrefixes(t *testing.T) {
	dir := setupCache(t)
	ctx := context.Background()
	st, err := store.Open(filepath.Join(dir, "netblocks.db"))
	if err != nil {
		t.Fatal(err)
	}
	insert := func(w *store.ASNWriter, cidr string, numType netutil.NumType, asn uint32, source string) error {
		start, end, _, err := netutil.ParsePrefix(cidr)
		if err != nil {
			return err
		}
		return w.Insert(store.ASNAnnouncement{
			Start: start, End: end, NumType: numType, ASN: asn,
			SourceType: "bgp_snapshot", SourceName: source,
		})
	}
	err = st.BulkASNAnnouncements(ctx, "bgp_snapshot", []string{"routeviews", "routeviews6"}, func(w *store.ASNWriter) error {
		if err := insert(w, "8.8.8.0/24", netutil.IPv4, 15169, "routeviews"); err != nil {
			return err
		}
		if err := insert(w, "8.8.4.0/22", netutil.IPv4, 15169, "routeviews"); err != nil {
			return err
		}
		if err := insert(w, "2001:db8::/32", netutil.IPv6, 15169, "routeviews6"); err != nil {
			return err
		}
		return insert(w, "1.1.1.0/24", netutil.IPv4, 13335, "routeviews")
	})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	out, _, err := runApp(t, dir, app.Options{Mode: "asn", ASN: "15169", Prefixes: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AS announced prefixes: 3", "IPv4 prefixes:", "8.8.8.0/24", "8.8.4.0/22", "IPv6 prefixes:", "2001:db8::/32"} {
		if !strings.Contains(out, want) {
			t.Fatalf("prefix output missing %q:\n%s", want, out)
		}
	}

	raw, _, err := runApp(t, dir, app.Options{Mode: "asn", ASN: "15169", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		AnnouncedPrefixes int64 `json:"announced_prefixes"`
		Announcements     []struct {
			Network    string `json:"network"`
			Family     string `json:"family"`
			Source     string `json:"source"`
			SourceType string `json:"source_type"`
		} `json:"announcements"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if decoded.AnnouncedPrefixes != 3 {
		t.Fatalf("announced_prefixes = %d", decoded.AnnouncedPrefixes)
	}
	if len(decoded.Announcements) != 3 {
		t.Fatalf("announcements = %+v", decoded.Announcements)
	}
	first := decoded.Announcements[0]
	if first.Network != "8.8.4.0/22" || first.Family != "ipv4" || first.Source != "routeviews" || first.SourceType != "bgp_snapshot" {
		t.Fatalf("first announcement = %+v", first)
	}
	if decoded.Announcements[1].Network != "8.8.8.0/24" {
		t.Fatalf("second announcement = %+v", decoded.Announcements[1])
	}
	last := decoded.Announcements[2]
	if last.Network != "2001:db8::/32" || last.Family != "ipv6" || last.Source != "routeviews6" {
		t.Fatalf("ipv6 announcement = %+v", last)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "asn", ASN: "15169", Prefixes: true, PrefixLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "IPv4 prefixes (showing 1 of 2):") {
		t.Fatalf("limited output = %q", out)
	}

	raw, _, err = runApp(t, dir, app.Options{Mode: "asn", ASN: "64500", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if decoded.AnnouncedPrefixes != 0 {
		t.Fatalf("announced_prefixes = %d", decoded.AnnouncedPrefixes)
	}
	if !strings.Contains(raw, `"announcements":[]`) {
		t.Fatalf("empty announcements not encoded as []: %s", raw)
	}
}

func TestCacheInfo(t *testing.T) {
	dir := setupCache(t)

	out, errOut, err := runApp(t, dir, app.Options{Mode: "info"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Cache database:", "Schema version: 2", "Database size:", "Rows:",
		"last_rir_import", "last_lir_import", "delegated-apnic-latest", "ripe.db.inetnum.gz",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("info output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(errOut, "warning:") {
		t.Fatalf("unexpected warnings: %s", errOut)
	}

	raw, _, err := runApp(t, dir, app.Options{Mode: "info", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		SchemaVersion string            `json:"schema_version"`
		DatabasePath  string            `json:"database_path"`
		DatabaseSize  int64             `json:"database_size"`
		Rows          []store.Stat      `json:"rows"`
		Imports       map[string]string `json:"imports"`
		Files         []struct {
			Name       string `json:"name"`
			Size       int64  `json:"size"`
			Modified   string `json:"modified"`
			AgeSeconds int64  `json:"age_seconds"`
		} `json:"files"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if report.SchemaVersion != "2" {
		t.Fatalf("schema_version = %q", report.SchemaVersion)
	}
	if !filepath.IsAbs(report.DatabasePath) || !strings.HasSuffix(report.DatabasePath, "netblocks.db") {
		t.Fatalf("database_path = %q", report.DatabasePath)
	}
	if report.Imports["last_rir_import"] == "" {
		t.Fatalf("imports = %+v", report.Imports)
	}
	names := make(map[string]bool)
	for _, f := range report.Files {
		names[f.Name] = true
	}
	if !names["delegated-apnic-latest"] || !names["ripe.db.inetnum.gz"] {
		t.Fatalf("files = %+v", report.Files)
	}

	apnic := filepath.Join(dir, "delegated-apnic-latest")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(apnic, future, future); err != nil {
		t.Fatal(err)
	}
	_, errOut, err = runApp(t, dir, app.Options{Mode: "info"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "is newer than the last RIR import") {
		t.Fatalf("missing stale warning: %s", errOut)
	}

	fresh := t.TempDir()
	raw, _, err = runApp(t, fresh, app.Options{Mode: "info", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	report.Imports = nil
	report.Files = nil
	report.Warnings = nil
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if report.SchemaVersion != "2" || len(report.Imports) != 0 || len(report.Files) != 0 {
		t.Fatalf("fresh report = %+v", report)
	}
	if !strings.Contains(raw, `"files":[]`) {
		t.Fatalf("fresh files not encoded as []: %s", raw)
	}
}

func TestCompareGrouping(t *testing.T) {
	dir := setupCache(t)
	ctx := context.Background()
	st, err := store.Open(filepath.Join(dir, "netblocks.db"))
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct{ cidr, cc string }{
		{"203.81.64.0/24", "IN"},
		{"203.81.65.0/24", "IN"},
		{"203.81.66.0/24", "IN"},
		{"203.81.67.0/24", "DE"},
		{"203.81.68.0/24", "FR"},
	}
	err = st.BulkAssignments(ctx, "lir", func(w *store.AssignmentWriter) error {
		for _, row := range rows {
			start, end, _, err := netutil.ParsePrefix(row.cidr)
			if err != nil {
				return err
			}
			if err := w.Insert(store.Assignment{
				Start: start, End: end, NumType: netutil.IPv4,
				CountryCode: row.cc, SourceType: "lir", SourceName: "test", Status: "assigned",
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	out, _, err := runApp(t, dir, app.Options{Mode: "compare", Compare: "MM"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "* IN lir: 3 ranges (203.81.64.0/24, 203.81.65.0/24, 203.81.66.0/24)") {
		t.Fatalf("grouped conflict output missing IN group:\n%s", out)
	}
	if strings.Contains(out, "* IN 203.81.64.0/24 lir") {
		t.Fatalf("conflict output still prints per-pair lines:\n%s", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "compare", Compare: "MM", CompareLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "* DE lir: 1 ranges (203.81.67.0/24)") {
		t.Fatalf("limited output missing first group:\n%s", out)
	}
	if !strings.Contains(out, "... 2 more conflict groups") {
		t.Fatalf("limited output missing suppressed count:\n%s", out)
	}
	if strings.Contains(out, "* IN lir:") {
		t.Fatalf("limited output still prints suppressed groups:\n%s", out)
	}

	raw, _, err := runApp(t, dir, app.Options{Mode: "compare", Compare: "MM", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Comparisons []json.RawMessage `json:"comparisons"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if len(decoded.Comparisons) != 5 {
		t.Fatalf("comparisons = %d, want 5", len(decoded.Comparisons))
	}
}

func TestMaxMindASNFallback(t *testing.T) {
	dir := setupCache(t)
	asnDir := t.TempDir()
	header := "network,autonomous_system_number,autonomous_system_organization\n"
	if err := os.WriteFile(filepath.Join(asnDir, "GeoLite2-ASN-Blocks-IPv4.csv"),
		[]byte(header+"8.8.4.0/22,15169,Google LLC\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(asnDir, "GeoLite2-ASN-Blocks-IPv6.csv"),
		[]byte(header+"2001:db8::/32,15169,Google LLC\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := runApp(t, dir, app.Options{Mode: "import-maxmind", ImportMaxMind: asnDir, MaxMindASN: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "MaxMind ASN import complete: 2 records") {
		t.Fatalf("import output = %q", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "asn", ASN: "15169", Prefixes: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AS announced prefixes: 2", "IPv4 prefixes:", "8.8.4.0/22", "IPv6 prefixes:", "2001:db8::/32"} {
		if !strings.Contains(out, want) {
			t.Fatalf("asn text missing %q:\n%s", want, out)
		}
	}

	type asnJSON struct {
		AnnouncedPrefixes int64 `json:"announced_prefixes"`
		Announcements     []struct {
			Network    string `json:"network"`
			Source     string `json:"source"`
			SourceType string `json:"source_type"`
		} `json:"announcements"`
	}
	raw, _, err := runApp(t, dir, app.Options{Mode: "asn", ASN: "15169", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	var decoded asnJSON
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if decoded.AnnouncedPrefixes != 2 || len(decoded.Announcements) != 2 {
		t.Fatalf("maxmind announcements = %+v", decoded)
	}
	for _, ann := range decoded.Announcements {
		if ann.Source != "maxmind" || ann.SourceType != "maxmind" {
			t.Fatalf("announcement source = %+v", ann)
		}
	}

	ctx := context.Background()
	st, err := store.Open(filepath.Join(dir, "netblocks.db"))
	if err != nil {
		t.Fatal(err)
	}
	start, end, _, err := netutil.ParsePrefix("8.8.8.0/24")
	if err != nil {
		t.Fatal(err)
	}
	err = st.BulkASNAnnouncements(ctx, "bgp_snapshot", []string{"routeviews"}, func(w *store.ASNWriter) error {
		return w.Insert(store.ASNAnnouncement{
			Start: start, End: end, NumType: netutil.IPv4, ASN: 15169,
			SourceType: "bgp_snapshot", SourceName: "routeviews",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	raw, _, err = runApp(t, dir, app.Options{Mode: "asn", ASN: "15169", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	decoded = asnJSON{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if decoded.AnnouncedPrefixes != 1 || len(decoded.Announcements) != 1 {
		t.Fatalf("bgp announcements = %+v", decoded)
	}
	if decoded.Announcements[0].Network != "8.8.8.0/24" || decoded.Announcements[0].Source != "routeviews" {
		t.Fatalf("bgp announcement = %+v", decoded.Announcements[0])
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "stats"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "maxmind") {
		t.Fatalf("stats missing maxmind row:\n%s", out)
	}
	out, _, err = runApp(t, dir, app.Options{Mode: "info"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "last_maxmind_asn_import") {
		t.Fatalf("info missing ASN import key:\n%s", out)
	}
}

func TestBatch(t *testing.T) {
	dir := setupCache(t)
	ipsPath := filepath.Join(dir, "ips.txt")
	if err := os.WriteFile(ipsPath, []byte("203.81.64.1\n2001:200::1\nnot-an-ip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := runApp(t, dir, app.Options{Mode: "batch", Input: ipsPath, InputType: "ip"})
	if err == nil || err.Error() != "1 of 3 inputs failed" {
		t.Fatalf("batch error = %v", err)
	}
	for _, want := range []string{"> 203.81.64.1", "MM", "> 2001:200::1", "JP"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text batch missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "not-an-ip") {
		t.Fatalf("stderr missing failed line: %q", errOut)
	}

	raw, _, err := runApp(t, dir, app.Options{Mode: "batch", Input: ipsPath, InputType: "ip", JSON: true})
	if err == nil || err.Error() != "1 of 3 inputs failed" {
		t.Fatalf("json batch error = %v", err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if len(items) != 2 {
		t.Fatalf("json batch items = %d, want 2", len(items))
	}
	var first struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(items[0], &first); err != nil || first.Query != "ip" {
		t.Fatalf("first item = %+v, %v", first, err)
	}

	raw, _, err = runApp(t, dir, app.Options{Mode: "batch", Input: ipsPath, InputType: "ip", JSONL: true})
	if err == nil || err.Error() != "1 of 3 inputs failed" {
		t.Fatalf("jsonl batch error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) != 2 {
		t.Fatalf("jsonl lines = %d, want 2:\n%s", len(lines), raw)
	}
	for i, line := range lines {
		var obj struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err != nil || obj.Query != "ip" {
			t.Fatalf("jsonl line %d = %q, %v", i, line, err)
		}
	}

	countriesPath := filepath.Join(dir, "countries.txt")
	if err := os.WriteFile(countriesPath, []byte("MM:ipv4\nJP:ipv6\nZZ\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err = runApp(t, dir, app.Options{Mode: "batch", Input: countriesPath, InputType: "country"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"> MM:ipv4", "> JP:ipv6", "> ZZ"} {
		if !strings.Contains(out, want) {
			t.Fatalf("country batch missing %q:\n%s", want, out)
		}
	}
	raw, _, err = runApp(t, dir, app.Options{Mode: "batch", Input: countriesPath, InputType: "country", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	items = nil
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if len(items) != 3 {
		t.Fatalf("country json items = %d, want 3", len(items))
	}

	asnsPath := filepath.Join(dir, "asns.txt")
	if err := os.WriteFile(asnsPath, []byte("AS173\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _, err = runApp(t, dir, app.Options{Mode: "batch", Input: asnsPath, InputType: "asn", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	var asnItems []struct {
		Query string `json:"query"`
		ASN   uint32 `json:"asn"`
	}
	if err := json.Unmarshal([]byte(raw), &asnItems); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if len(asnItems) != 1 || asnItems[0].Query != "asn" || asnItems[0].ASN != 173 {
		t.Fatalf("asn batch = %+v", asnItems)
	}

	emptyPath := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(emptyPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err = runApp(t, dir, app.Options{Mode: "batch", Input: emptyPath, InputType: "ip", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	if out != "[]\n" {
		t.Fatalf("empty json batch = %q", out)
	}
}

func TestCountryFilters(t *testing.T) {
	dir := setupCache(t)

	out, _, err := runApp(t, dir, app.Options{Mode: "code", Code: "JP", Statuses: []string{"assigned"}})
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("JP assigned output = %q, want empty", out)
	}

	out, _, err = runApp(t, dir, app.Options{Mode: "code", Code: "JP", TypeFilter: "ipv6", Statuses: []string{"allocated"}, Merge: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2001:200::/32") {
		t.Fatalf("JP allocated output = %q", out)
	}

	ctx := context.Background()
	st, err := store.Open(filepath.Join(dir, "netblocks.db"))
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct{ cidr, cc, status string }{
		{"203.81.64.0/24", "MM", "reserved"},
		{"203.81.65.0/24", "MM", "assigned"},
	}
	err = st.BulkAssignments(ctx, "lir", func(w *store.AssignmentWriter) error {
		for _, row := range rows {
			start, end, _, err := netutil.ParsePrefix(row.cidr)
			if err != nil {
				return err
			}
			if err := w.Insert(store.Assignment{
				Start: start, End: end, NumType: netutil.IPv4,
				CountryCode: row.cc, SourceType: "lir", SourceName: "test", Status: row.status,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	base := app.Options{Mode: "code", Code: "MM", TypeFilter: "ipv4", Sources: []string{"lir"}, Merge: true}
	out, _, err = runApp(t, dir, base)
	if err != nil {
		t.Fatal(err)
	}
	if out != "203.81.64.0/23\n" {
		t.Fatalf("unfiltered MM lir = %q", out)
	}

	excluded := base
	excluded.ExcludeReserved = true
	out, _, err = runApp(t, dir, excluded)
	if err != nil {
		t.Fatal(err)
	}
	if out != "203.81.65.0/24\n" {
		t.Fatalf("exclude-reserved MM lir = %q", out)
	}

	onlyReserved := base
	onlyReserved.Statuses = []string{"reserved"}
	out, _, err = runApp(t, dir, onlyReserved)
	if err != nil {
		t.Fatal(err)
	}
	if out != "203.81.64.0/24\n" {
		t.Fatalf("status reserved MM lir = %q", out)
	}
}
