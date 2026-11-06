package export_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zenware/netblocks/internal/countries"
	"github.com/zenware/netblocks/internal/export"
	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

func TestCountryCSVMergesAdjacentSameCountry(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	err = st.BulkAssignments(ctx, "rir", func(w *store.AssignmentWriter) error {
		for i, cidr := range []string{"1.0.0.0/24", "1.0.1.0/24", "1.0.2.0/24"} {
			start, end, _, err := netutil.ParsePrefix(cidr)
			if err != nil {
				return err
			}
			cc := "AU"
			if i == 2 {
				cc = "NZ"
			}
			if err := w.Insert(store.Assignment{Start: start, End: end, NumType: netutil.IPv4, CountryCode: cc, SourceType: "rir", SourceName: "apnic"}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := countries.Load("")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	stats, err := export.CountryCSV(ctx, st, &buf, netutil.IPv4, []string{"rir"}, d)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 2 {
		t.Fatalf("rows = %d", stats.Rows)
	}
	records, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %v", records)
	}
	if records[0][0] != "1.0.0.0" || records[0][1] != "1.0.1.255" || records[0][4] != "AU" {
		t.Fatalf("first record = %v", records[0])
	}
	if records[1][0] != "1.0.2.0" || records[1][4] != "NZ" || records[1][5] != "New Zealand" {
		t.Fatalf("second record = %v", records[1])
	}
}

func TestASNCSVMergesAdjacentSameASN(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	err = st.BulkASNAnnouncements(ctx, "bgp_snapshot", []string{"routeviews"}, func(w *store.ASNWriter) error {
		for i, cidr := range []string{"1.0.0.0/24", "1.0.1.0/24", "1.0.2.0/24"} {
			start, end, _, err := netutil.ParsePrefix(cidr)
			if err != nil {
				return err
			}
			asn := uint32(13335)
			if i == 2 {
				asn = 64500
			}
			if err := w.Insert(store.ASNAnnouncement{Start: start, End: end, NumType: netutil.IPv4, ASN: asn, SourceType: "bgp_snapshot", SourceName: "routeviews"}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	stats, err := export.ASNCSV(ctx, st, &buf, netutil.IPv4)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 2 {
		t.Fatalf("rows = %d", stats.Rows)
	}
	records, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if records[0][0] != "1.0.0.0" || records[0][1] != "1.0.1.255" || records[0][4] != "AS13335" {
		t.Fatalf("first record = %v", records[0])
	}
	if records[1][4] != "AS64500" {
		t.Fatalf("second record = %v", records[1])
	}
}
