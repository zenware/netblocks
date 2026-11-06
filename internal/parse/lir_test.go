package parse

import (
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

func TestLIRParityFixture(t *testing.T) {
	f, err := os.Open("../../testdata/test_lir_data.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()

	var out []store.Assignment
	stats, err := LIR(context.Background(), gz, func(a store.Assignment) error {
		out = append(out, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 9 {
		t.Fatalf("records = %d, want 9", stats.Records)
	}
	want := map[string]string{
		"IT/80.16.151.184": "80.16.151.191",
		"IT/80.16.151.180": "80.16.151.183",
		"DE/213.95.6.32":   "213.95.6.47",
		"RO/128.0.0.0":     "128.0.0.255",
		"SE/85.195.129.0":  "85.195.129.255",
		"EU/159.245.0.0":   "159.245.255.255",
	}
	got := make(map[string]string)
	ipv6 := make(map[string]string)
	for _, a := range out {
		start, err := netutil.IntToAddr(a.Start, a.NumType.Bits())
		if err != nil {
			t.Fatal(err)
		}
		end, err := netutil.IntToAddr(a.End, a.NumType.Bits())
		if err != nil {
			t.Fatal(err)
		}
		if a.NumType == netutil.IPv4 {
			got[fmt.Sprintf("%s/%s", a.CountryCode, start)] = end.String()
		} else {
			ipv6[fmt.Sprintf("%s/%s", a.CountryCode, start)] = end.String()
		}
	}
	for key, end := range want {
		if got[key] != end {
			t.Errorf("%s end = %q, want %q", key, got[key], end)
		}
	}
	if ipv6["DE/2001:658:21a::"] == "" || ipv6["DE/2001:67c:320::"] == "" || ipv6["FI/2001:670:85::"] == "" {
		t.Fatalf("missing IPv6 records: %v", ipv6)
	}
}

func TestLIRSkipsPlaceholders(t *testing.T) {
	input := `inetnum: 0.0.0.0 - 255.255.255.255
netname: IANA-BLK
country: EU
status: ALLOCATED UNSPECIFIED

inetnum: 0.0.0.0 - 1.178.111.255
netname: NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK
country: EU
status: ALLOCATED UNSPECIFIED

inet6num: ::/0
netname: IANA-BLK
country: EU
status: ALLOCATED-BY-RIR

inetnum: 10.0.0.0 - 10.0.0.255
netname: REAL-NET
country: DE
status: ASSIGNED PA
`
	var out []store.Assignment
	stats, err := LIR(context.Background(), strings.NewReader(input), func(a store.Assignment) error {
		out = append(out, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 1 || stats.Skipped != 3 || len(out) != 1 || out[0].CountryCode != "DE" {
		t.Fatalf("stats=%+v records=%+v", stats, out)
	}
}

func TestLIRCountryBeforeRange(t *testing.T) {
	input := "country: DE\ninetnum: 10.0.0.0 - 10.0.0.255\nstatus: ASSIGNED\n\n"
	var out []store.Assignment
	stats, err := LIR(context.Background(), strings.NewReader(input), func(a store.Assignment) error {
		out = append(out, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 1 || out[0].CountryCode != "DE" || out[0].Status != "ASSIGNED" {
		t.Fatalf("stats=%+v records=%+v", stats, out)
	}
}
