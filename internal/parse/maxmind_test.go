package parse

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

func TestMaxMindLegacyCSV(t *testing.T) {
	input := `"start","end","startnum","endnum","cc","name"
"1.0.0.0","1.0.0.255","16777216","16777471","AU","Australia"
"2001:200::","2001:200:ffff:ffff:ffff:ffff:ffff:ffff","","","JP","Japan"
"garbage"
`
	var out []store.Assignment
	stats, err := legacyCSV(context.Background(), strings.NewReader(input), "test.csv", func(a store.Assignment) error {
		out = append(out, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 2 {
		t.Fatalf("records = %d, want 2", stats.Records)
	}
	if out[0].CountryCode != "AU" || out[0].NumType != netutil.IPv4 {
		t.Fatalf("first = %+v", out[0])
	}
	if out[1].CountryCode != "JP" || out[1].NumType != netutil.IPv6 {
		t.Fatalf("second = %+v", out[1])
	}
}

func TestMaxMindGeoLite2Zip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "GeoLite2-Country-CSV.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	writeZip := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	writeZip("GeoLite2-Country-CSV_20260101/GeoLite2-Country-Locations-en.csv",
		`geoname_id,locale_code,continent_code,continent_name,country_iso_code,country_name,is_in_european_union
1,en,NA,North America,US,United States,0
2,en,AS,Asia,JP,Japan,0
`)
	writeZip("GeoLite2-Country-CSV_20260101/GeoLite2-Country-Blocks-IPv4.csv",
		`network,geoname_id,registered_country_geoname_id,represented_country_geoname_id,is_anonymous_proxy,is_satellite_provider,is_anycast
8.8.8.0/24,1,1,,0,0,0
1.0.16.0/20,2,,,0,0,0
`)
	writeZip("GeoLite2-Country-CSV_20260101/GeoLite2-Country-Blocks-IPv6.csv",
		`network,geoname_id,registered_country_geoname_id,represented_country_geoname_id,is_anonymous_proxy,is_satellite_provider,is_anycast
2001:200::/32,2,2,,0,0,0
`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	var out []store.Assignment
	stats, err := MaxMind(context.Background(), path, func(a store.Assignment) error {
		out = append(out, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 3 {
		t.Fatalf("records = %d, want 3", stats.Records)
	}
	got := make(map[string]string)
	for _, a := range out {
		start, _ := netutil.IntToAddr(a.Start, a.NumType.Bits())
		end, _ := netutil.IntToAddr(a.End, a.NumType.Bits())
		got[string(a.NumType)+"/"+a.CountryCode+"/"+start.String()] = end.String()
	}
	if got["ipv4/US/8.8.8.0"] != "8.8.8.255" {
		t.Fatalf("IPv4 US = %v", got)
	}
	if got["ipv4/JP/1.0.16.0"] != "1.0.31.255" {
		t.Fatalf("IPv4 JP = %v", got)
	}
	if got["ipv6/JP/2001:200::"] != "2001:200:ffff:ffff:ffff:ffff:ffff:ffff" {
		t.Fatalf("IPv6 JP = %v", got)
	}
}
