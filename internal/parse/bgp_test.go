package parse

import (
	"context"
	"strings"
	"testing"

	"github.com/zenware/netblocks/internal/store"
)

func TestBGP(t *testing.T) {
	input := `Status codes: s suppressed, d damped, h history, * valid, > best, i - internal
   Network            Next Hop            Metric LocPrf Weight Path
*  1.0.0.0/24         12.0.1.63                0      0      0 7018 13335 i
*  1.0.0.0/24         37.139.139.17            0      0      0 57866 13335 i
*> 2001:db8::/32     2001:db8::1              0      0      0 64500 64501 ?
*  10.0.0.0/8        1.1.1.1                  0      0      0 {64500,64501} 64502 e
`
	var out []store.ASNAnnouncement
	stats, err := BGP(context.Background(), strings.NewReader(input), func(a store.ASNAnnouncement) error {
		out = append(out, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 4 {
		t.Fatalf("records = %d, want 4", stats.Records)
	}
	if out[0].ASN != 13335 || out[0].NumType != "ipv4" || out[0].Start.String() != "16777216" {
		t.Fatalf("first announcement = %+v", out[0])
	}
	if out[1].ASN != 13335 {
		t.Fatalf("second announcement = %+v", out[1])
	}
	if out[2].ASN != 64501 || out[2].NumType != "ipv6" {
		t.Fatalf("third announcement = %+v", out[2])
	}
	if out[3].ASN != 64502 {
		t.Fatalf("fourth announcement = %+v", out[3])
	}
}

func TestBGPRejectsGarbage(t *testing.T) {
	input := "this is not bgp\n* short\n* 1.0.0.0/24 1.1.1.1 0 0 0 1 i\n"
	stats, err := BGP(context.Background(), strings.NewReader(input), func(a store.ASNAnnouncement) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 1 {
		t.Fatalf("records = %d, want 1", stats.Records)
	}
	if stats.Skipped != 1 {
		t.Fatalf("skipped = %d", stats.Skipped)
	}
}
