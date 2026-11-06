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

	"github.com/zenware/netblocks/internal/app"
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
