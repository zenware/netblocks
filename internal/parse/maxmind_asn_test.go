package parse

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

func TestMaxMindASN(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "GeoLite2-ASN-CSV.zip")
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
	writeZip("GeoLite2-ASN-CSV_20260101/GeoLite2-ASN-Blocks-IPv4.csv",
		`network,autonomous_system_number,autonomous_system_organization
8.8.8.0/24,15169,Google LLC
1.0.16.0/20,13335,Cloudflare
192.0.2.0/24,0,
not-a-prefix,1,x
`)
	writeZip("GeoLite2-ASN-CSV_20260101/GeoLite2-ASN-Blocks-IPv6.csv",
		`network,autonomous_system_number,autonomous_system_organization
2001:db8::/32,15169,Google LLC
`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	var out []store.ASNAnnouncement
	stats, err := MaxMindASN(context.Background(), path, func(a store.ASNAnnouncement) error {
		out = append(out, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 3 || stats.Skipped != 2 {
		t.Fatalf("stats = %+v, want 3 records and 2 skipped", stats)
	}
	got := make(map[string]string)
	for _, a := range out {
		start, _ := netutil.IntToAddr(a.Start, a.NumType.Bits())
		end, _ := netutil.IntToAddr(a.End, a.NumType.Bits())
		got[fmt.Sprintf("%s/%s/AS%d", a.NumType, start, a.ASN)] = end.String()
		if a.SourceType != "maxmind" || a.SourceName != "maxmind" {
			t.Fatalf("source = %q/%q", a.SourceType, a.SourceName)
		}
	}
	if got["ipv4/8.8.8.0/AS15169"] != "8.8.8.255" {
		t.Fatalf("IPv4 AS15169 = %v", got)
	}
	if got["ipv4/1.0.16.0/AS13335"] != "1.0.31.255" {
		t.Fatalf("IPv4 AS13335 = %v", got)
	}
	if got["ipv6/2001:db8::/AS15169"] != "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff" {
		t.Fatalf("IPv6 AS15169 = %v", got)
	}
}
