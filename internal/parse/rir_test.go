package parse

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

func collectRIR(t *testing.T, fixture string) ([]store.Assignment, Stats) {
	t.Helper()
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []store.Assignment
	stats, err := RIR(context.Background(), f, func(a store.Assignment) error {
		out = append(out, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out, stats
}

func TestRIRParityFixture(t *testing.T) {
	records, stats := collectRIR(t, "../../testdata/test_rir_data")
	if stats.Records != 11 {
		t.Fatalf("records = %d, want 11", stats.Records)
	}
	want := map[string]string{
		"MM/ipv4/203.81.64.0":  "203.81.95.255",
		"MM/ipv4/203.81.160.0": "203.81.175.255",
		"KP/ipv4/175.45.176.0": "175.45.179.255",
		"JP/ipv6/2001:200::":   "2001:200:1fff:ffff:ffff:ffff:ffff:ffff",
		"PL/ipv4/193.9.25.0":   "193.9.25.255",
		"HU/ipv4/193.9.26.0":   "193.9.27.255",
	}
	got := make(map[string]string)
	for _, a := range records {
		if a.NumType == netutil.ASN {
			if a.Start.Cmp(a.End) != 0 {
				t.Errorf("ASN record should be a single ASN: %s-%s", a.Start, a.End)
			}
			continue
		}
		start, err := netutil.IntToAddr(a.Start, a.NumType.Bits())
		if err != nil {
			t.Fatal(err)
		}
		end, err := netutil.IntToAddr(a.End, a.NumType.Bits())
		if err != nil {
			t.Fatal(err)
		}
		got[fmt.Sprintf("%s/%s/%s", a.CountryCode, a.NumType, start)] = end.String()
	}
	for key, end := range want {
		if got[key] != end {
			t.Errorf("%s end = %q, want %q", key, got[key], end)
		}
	}
}

func TestRIRFormats(t *testing.T) {
	input := `# comment
2|test|123|5|20200101|20200102|+0000
test|*|ipv4|*|2|summary
test|US|ipv4|10.0.0.0|3|20200101|assigned|opaque|extension
test|US|asn|64500|4|20200101|allocated
test|DE|ipv6|2001:db8::|48|20200101|assigned
test||ipv6|2001:db9::|32|20200101|reserved
test|ZZ|ipv4|bad|1|20200101|assigned
`
	var out []store.Assignment
	stats, err := RIR(context.Background(), strings.NewReader(input), func(a store.Assignment) error {
		out = append(out, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 4 || stats.Skipped != 3 {
		t.Fatalf("stats = %+v", stats)
	}
	if out[0].End.String() != "167772162" { // 10.0.0.2
		t.Fatalf("ipv4 end = %s", out[0].End)
	}
	if out[0].OpaqueID != "opaque" || out[0].Status != "assigned" {
		t.Fatalf("metadata = %+v", out[0])
	}
	if out[1].Start.String() != "64500" || out[1].End.String() != "64503" {
		t.Fatalf("asn range = %s-%s", out[1].Start, out[1].End)
	}
	if out[2].NumType != "ipv6" {
		t.Fatalf("ipv6 type = %s", out[2].NumType)
	}
	if out[2].End.Cmp(out[2].Start) <= 0 {
		t.Fatal("ipv6 end should be after start")
	}
	if out[3].CountryCode != "*" || out[3].Status != "reserved" {
		t.Fatalf("reserved record = %+v", out[3])
	}
}
