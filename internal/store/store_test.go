package store_test

import (
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

func bi(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad bigint " + s)
	}
	return n
}

func ipRange(t *testing.T, cidr string) (*big.Int, *big.Int) {
	t.Helper()
	start, end, _, err := netutil.ParsePrefix(cidr)
	if err != nil {
		t.Fatal(err)
	}
	return start, end
}

func TestStoreQueries(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	err = st.BulkAssignments(ctx, "rir", func(w *store.AssignmentWriter) error {
		insert := func(cidr, cc, typ string) error {
			start, end := ipRange(t, cidr)
			return w.Insert(store.Assignment{
				Start: start, End: end, NumType: netutil.NumType(typ),
				CountryCode: cc, SourceType: "rir", SourceName: "test",
			})
		}
		if err := insert("203.81.64.0/19", "MM", "ipv4"); err != nil {
			return err
		}
		if err := insert("2001:200::/35", "JP", "ipv6"); err != nil {
			return err
		}
		if err := insert("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128", "ZZ", "ipv6"); err != nil {
			return err
		}
		return w.Insert(store.Assignment{
			Start: bi("681"), End: bi("681"), NumType: netutil.ASN,
			CountryCode: "NZ", SourceType: "rir", SourceName: "apnic",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	err = st.BulkAssignments(ctx, "lir", func(w *store.AssignmentWriter) error {
		start, end := ipRange(t, "203.81.64.0/19")
		if err := w.Insert(store.Assignment{
			Start: start, End: end, NumType: netutil.IPv4,
			CountryCode: "MM", SourceType: "lir", SourceName: "ripencc",
		}); err != nil {
			return err
		}
		start, end = ipRange(t, "203.81.64.0/24")
		return w.Insert(store.Assignment{
			Start: start, End: end, NumType: netutil.IPv4,
			CountryCode: "IN", SourceType: "lir", SourceName: "ripencc",
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	blocks, err := st.AssignmentsByCountry(ctx, netutil.IPv4, "MM", []string{"rir"})
	if err != nil || len(blocks) != 1 {
		t.Fatalf("blocks = %v, %v", blocks, err)
	}
	endAddr, _ := netutil.IntToAddr(blocks[0].End, 32)
	if endAddr.String() != "203.81.95.255" {
		t.Fatalf("end = %s", endAddr)
	}

	addr, _, _ := netutil.ParseAddr("203.81.65.1")
	got, ok, err := st.CountryAt(ctx, netutil.IPv4, "lir", addr)
	if err != nil || !ok || got.CountryCode != "MM" {
		t.Fatalf("CountryAt = %+v, %v, %v", got, ok, err)
	}

	maxAddr, _, _ := netutil.ParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")
	if got, ok, _ := st.CountryAt(ctx, netutil.IPv6, "rir", maxAddr); !ok || got.CountryCode != "ZZ" {
		t.Fatalf("top IPv6 lookup = %+v, %v", got, ok)
	}

	asn681 := bi("681")
	if got, ok, _ := st.CountryAt(ctx, netutil.ASN, "rir", asn681); !ok || got.CountryCode != "NZ" {
		t.Fatalf("ASN lookup = %+v, %v", got, ok)
	}

	overlaps, err := st.Overlaps(ctx, netutil.IPv4, "lir", blocks[0].Start, blocks[0].End)
	if err != nil || len(overlaps) != 2 {
		t.Fatalf("overlaps = %v, %v", overlaps, err)
	}
	comparisons, err := st.Compare(ctx, "MM", []netutil.NumType{netutil.IPv4}, []string{"rir"}, []string{"lir"})
	if err != nil || len(comparisons) < 2 {
		t.Fatalf("comparisons = %v, %v", comparisons, err)
	}
	conflicts, err := st.Conflicts(ctx, "MM", []netutil.NumType{netutil.IPv4}, []string{"rir"}, []string{"lir"})
	if err != nil || len(conflicts) < 1 {
		t.Fatalf("conflicts = %v, %v", conflicts, err)
	}
	for _, c := range conflicts {
		if c.Second.CountryCode == "MM" {
			t.Fatalf("same-country pair returned as conflict: %+v", c)
		}
	}
}

func TestStoreASNAndBGP(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	err = st.BulkASNDescriptions(ctx, func(insert func(asn uint32, source, description string) error) error {
		if err := insert(13335, "cidr_report", "CLOUDFLARENET, US"); err != nil {
			return err
		}
		return insert(64500, "cidr_report", "TEST-AS")
	})
	if err != nil {
		t.Fatal(err)
	}
	start, end := ipRange(t, "8.8.8.0/24")
	err = st.BulkASNAnnouncements(ctx, "bgp_snapshot", []string{"routeviews"}, func(w *store.ASNWriter) error {
		if err := w.Insert(store.ASNAnnouncement{
			Start: start, End: new(big.Int).Lsh(big.NewInt(1), 32), NumType: netutil.IPv4, ASN: 13335, SourceType: "bgp_snapshot", SourceName: "routeviews",
		}); err == nil {
			t.Fatal("out-of-range IPv4 announcement should fail")
		}
		if err := w.Insert(store.ASNAnnouncement{
			Start: start, End: end, NumType: netutil.IPv4, ASN: 13335, SourceType: "bgp_snapshot", SourceName: "routeviews",
		}); err != nil {
			return err
		}
		return w.Insert(store.ASNAnnouncement{
			Start: start, End: end, NumType: netutil.IPv4, ASN: 64500, SourceType: "bgp_snapshot", SourceName: "routeviews",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	addr, _, _ := netutil.ParseAddr("8.8.8.8")
	orgs, err := st.OrgForIP(ctx, netutil.IPv4, addr)
	if err != nil || len(orgs) != 1 {
		t.Fatalf("orgs = %+v, %v", orgs, err)
	}
	if orgs[0].ASN != 13335 || orgs[0].Description != "CLOUDFLARENET, US" {
		t.Fatalf("org = %+v", orgs[0])
	}
	if count, _ := st.ASNAnnouncementCount(ctx, 13335); count != 1 {
		t.Fatalf("announcement count = %d", count)
	}
	desc, err := st.ASNDescription(ctx, 64500)
	if err != nil || desc != "TEST-AS" {
		t.Fatalf("description = %q, %v", desc, err)
	}

	v6Start, v6End := ipRange(t, "2001:db8::/32")
	err = st.BulkASNAnnouncements(ctx, "bgp_snapshot", []string{"routeviews6"}, func(w *store.ASNWriter) error {
		return w.Insert(store.ASNAnnouncement{
			Start: v6Start, End: v6End, NumType: netutil.IPv6, ASN: 15169, SourceType: "bgp_snapshot", SourceName: "routeviews6",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	v6Addr, _, _ := netutil.ParseAddr("2001:db8::1")
	orgs, err = st.OrgForIP(ctx, netutil.IPv6, v6Addr)
	if err != nil || len(orgs) != 1 || orgs[0].ASN != 15169 {
		t.Fatalf("ipv6 orgs = %+v, %v", orgs, err)
	}

	// Replacing the IPv4 source must leave the IPv6 source untouched.
	err = st.BulkASNAnnouncements(ctx, "bgp_snapshot", []string{"routeviews"}, func(w *store.ASNWriter) error {
		return w.Insert(store.ASNAnnouncement{
			Start: start, End: end, NumType: netutil.IPv4, ASN: 13335, SourceType: "bgp_snapshot", SourceName: "routeviews",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	orgs, err = st.OrgForIP(ctx, netutil.IPv4, addr)
	if err != nil || len(orgs) != 1 || orgs[0].ASN != 13335 {
		t.Fatalf("ipv4 orgs after reload = %+v, %v", orgs, err)
	}
	orgs, err = st.OrgForIP(ctx, netutil.IPv6, v6Addr)
	if err != nil || len(orgs) != 1 || orgs[0].ASN != 15169 {
		t.Fatalf("ipv6 orgs after ipv4 reload = %+v, %v", orgs, err)
	}

	stats, err := st.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ipv4Stat, ipv6Stat bool
	for _, s := range stats {
		if s.SourceType != "bgp_snapshot" {
			continue
		}
		switch s.NumType {
		case "ipv4":
			ipv4Stat = ipv4Stat || s.Count > 0
		case "ipv6":
			ipv6Stat = ipv6Stat || s.Count > 0
		}
	}
	if !ipv4Stat || !ipv6Stat {
		t.Fatalf("bgp stats = %+v", stats)
	}
}

func TestStoreStatsAndCountries(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	err = st.BulkAssignments(ctx, "rir", func(w *store.AssignmentWriter) error {
		start, end := ipRange(t, "10.0.0.0/8")
		if err := w.Insert(store.Assignment{Start: start, End: end, NumType: netutil.IPv4, CountryCode: "US", SourceType: "rir", SourceName: "arin", Status: "assigned"}); err != nil {
			return err
		}
		start, end = ipRange(t, "192.0.2.0/24")
		return w.Insert(store.Assignment{Start: start, End: end, NumType: netutil.IPv4, CountryCode: "*", SourceType: "rir", SourceName: "arin", Status: "reserved"})
	})
	if err != nil {
		t.Fatal(err)
	}
	codes, err := st.CountryCodes(ctx)
	if err != nil || len(codes) != 1 || codes[0] != "US" {
		t.Fatalf("codes = %v, %v", codes, err)
	}
	counts, err := st.StatusCounts(ctx)
	if err != nil || counts["assigned"] != 1 || counts["reserved"] != 1 {
		t.Fatalf("counts = %v, %v", counts, err)
	}
	if n, err := st.ReservedCount(ctx); err != nil || n != 1 {
		t.Fatalf("reserved = %d, %v", n, err)
	}
	if n, err := st.CountAssignments(ctx); err != nil || n != 2 {
		t.Fatalf("count = %d, %v", n, err)
	}
	stats, err := st.Stats(ctx)
	if err != nil || len(stats) < 1 {
		t.Fatalf("stats = %v, %v", stats, err)
	}
}

func TestSchemaMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMeta(context.Background(), "schema_version", "1"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	_, err = store.Open(path)
	var schemaErr *store.SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("expected SchemaError, got %v", err)
	}
	if err := store.Erase(path); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
}
