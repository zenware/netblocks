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

	blocks, err := st.AssignmentsByCountry(ctx, netutil.IPv4, "MM", []string{"rir"}, store.AssignmentFilter{})
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

func TestStoreMeta(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if value, ok, err := st.GetMeta(ctx, "missing"); err != nil || ok || value != "" {
		t.Fatalf("missing meta = %q, %v, %v", value, ok, err)
	}
	if err := st.SetMeta(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	if value, ok, err := st.GetMeta(ctx, "k"); err != nil || !ok || value != "v" {
		t.Fatalf("meta = %q, %v, %v", value, ok, err)
	}
}

func TestAnnouncementsForASN(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	insert := func(w *store.ASNWriter, cidr string, numType netutil.NumType, asn uint32, source string) error {
		start, end := ipRange(t, cidr)
		return w.Insert(store.ASNAnnouncement{
			Start: start, End: end, NumType: numType, ASN: asn,
			SourceType: "bgp_snapshot", SourceName: source,
		})
	}
	err = st.BulkASNAnnouncements(ctx, "bgp_snapshot", []string{"routeviews", "routeviews6"}, func(w *store.ASNWriter) error {
		if err := insert(w, "1.0.0.0/24", netutil.IPv4, 15169, "routeviews"); err != nil {
			return err
		}
		if err := insert(w, "1.0.2.0/24", netutil.IPv4, 15169, "routeviews"); err != nil {
			return err
		}
		if err := insert(w, "2001:db8::/32", netutil.IPv6, 15169, "routeviews6"); err != nil {
			return err
		}
		return insert(w, "8.8.8.0/24", netutil.IPv4, 13335, "routeviews")
	})
	if err != nil {
		t.Fatal(err)
	}

	v4, err := st.AnnouncementsForASN(ctx, 15169, netutil.IPv4)
	if err != nil {
		t.Fatal(err)
	}
	if len(v4) != 2 {
		t.Fatalf("ipv4 announcements = %+v", v4)
	}
	wantStarts := []string{"1.0.0.0", "1.0.2.0"}
	for i, ann := range v4 {
		start, _, err := netutil.ParseAddr(wantStarts[i])
		if err != nil {
			t.Fatal(err)
		}
		if ann.Start.Cmp(start) != 0 {
			t.Fatalf("announcement %d start = %s, want %s", i, ann.Start, wantStarts[i])
		}
		if ann.ASN != 15169 || ann.NumType != netutil.IPv4 || ann.SourceName != "routeviews" {
			t.Fatalf("announcement %d = %+v", i, ann)
		}
	}

	v6, err := st.AnnouncementsForASN(ctx, 15169, netutil.IPv6)
	if err != nil {
		t.Fatal(err)
	}
	if len(v6) != 1 || v6[0].ASN != 15169 || v6[0].NumType != netutil.IPv6 || v6[0].SourceName != "routeviews6" {
		t.Fatalf("ipv6 announcements = %+v", v6)
	}

	none, err := st.AnnouncementsForASN(ctx, 64500, netutil.IPv4)
	if err != nil || len(none) != 0 {
		t.Fatalf("unknown ASN announcements = %+v, %v", none, err)
	}
}

func TestEachComparison(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	insert := func(w *store.AssignmentWriter, cidr, cc, source string) error {
		start, end := ipRange(t, cidr)
		return w.Insert(store.Assignment{
			Start: start, End: end, NumType: netutil.IPv4,
			CountryCode: cc, SourceType: source, SourceName: "test",
		})
	}
	if err := st.BulkAssignments(ctx, "rir", func(w *store.AssignmentWriter) error {
		return insert(w, "203.81.64.0/19", "MM", "rir")
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.BulkAssignments(ctx, "lir", func(w *store.AssignmentWriter) error {
		if err := insert(w, "203.81.64.0/19", "MM", "lir"); err != nil {
			return err
		}
		return insert(w, "203.81.64.0/24", "IN", "lir")
	}); err != nil {
		t.Fatal(err)
	}

	types := []netutil.NumType{netutil.IPv4}
	want, err := st.Conflicts(ctx, "MM", types, []string{"rir"}, []string{"lir"})
	if err != nil {
		t.Fatal(err)
	}
	var got []store.Comparison
	err = st.EachComparison(ctx, "MM", types, []string{"rir"}, []string{"lir"}, true, func(c store.Comparison) error {
		got = append(got, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("streamed %d comparisons, want %d", len(got), len(want))
	}
	for i := range want {
		x, y := got[i], want[i]
		if x.First.Start.Cmp(y.First.Start) != 0 || x.First.End.Cmp(y.First.End) != 0 ||
			x.First.SourceType != y.First.SourceType || x.First.CountryCode != y.First.CountryCode || x.First.NumType != y.First.NumType {
			t.Fatalf("first %d = %+v, want %+v", i, x.First, y.First)
		}
		if x.Second.Start.Cmp(y.Second.Start) != 0 || x.Second.End.Cmp(y.Second.End) != 0 ||
			x.Second.SourceType != y.Second.SourceType || x.Second.CountryCode != y.Second.CountryCode || x.Second.NumType != y.Second.NumType {
			t.Fatalf("second %d = %+v, want %+v", i, x.Second, y.Second)
		}
	}

	stop := errors.New("stop")
	err = st.EachComparison(ctx, "MM", types, []string{"rir"}, []string{"lir"}, true, func(store.Comparison) error {
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("callback error = %v, want %v", err, stop)
	}
}

func TestMaxMindASNStore(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	err = st.BulkMaxMindASN(ctx, func(w *store.MaxMindASNWriter) error {
		v4Start, v4End := ipRange(t, "8.8.4.0/22")
		if err := w.Insert(store.ASNAnnouncement{
			Start: v4Start, End: v4End, NumType: netutil.IPv4, ASN: 15169,
			SourceType: "maxmind", SourceName: "maxmind",
		}); err != nil {
			return err
		}
		v6Start, v6End := ipRange(t, "2001:db8::/32")
		return w.Insert(store.ASNAnnouncement{
			Start: v6Start, End: v6End, NumType: netutil.IPv6, ASN: 15169,
			SourceType: "maxmind", SourceName: "maxmind",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := st.MaxMindASNCount(ctx, 15169); err != nil || n != 2 {
		t.Fatalf("count = %d, %v", n, err)
	}
	v4, err := st.MaxMindASNForASN(ctx, 15169, netutil.IPv4)
	if err != nil || len(v4) != 1 {
		t.Fatalf("ipv4 = %+v, %v", v4, err)
	}
	if v4[0].ASN != 15169 || v4[0].NumType != netutil.IPv4 || v4[0].SourceType != "maxmind" || v4[0].SourceName != "maxmind" {
		t.Fatalf("ipv4 announcement = %+v", v4[0])
	}
	start, _ := netutil.IntToAddr(v4[0].Start, 32)
	end, _ := netutil.IntToAddr(v4[0].End, 32)
	if start.String() != "8.8.4.0" || end.String() != "8.8.7.255" {
		t.Fatalf("ipv4 range = %s-%s", start, end)
	}
	if none, err := st.MaxMindASNForASN(ctx, 64500, netutil.IPv4); err != nil || len(none) != 0 {
		t.Fatalf("unknown ASN = %+v, %v", none, err)
	}

	err = st.BulkMaxMindASN(ctx, func(*store.MaxMindASNWriter) error { return nil })
	if err == nil {
		t.Fatal("empty import should fail")
	}
	if n, err := st.MaxMindASNCount(ctx, 15169); err != nil || n != 2 {
		t.Fatalf("count after failed import = %d, %v", n, err)
	}
}

func TestAssignmentFilter(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	err = st.BulkAssignments(ctx, "rir", func(w *store.AssignmentWriter) error {
		insert := func(cidr, cc, status string) error {
			start, end := ipRange(t, cidr)
			return w.Insert(store.Assignment{
				Start: start, End: end, NumType: netutil.IPv4,
				CountryCode: cc, SourceType: "rir", SourceName: "test", Status: status,
			})
		}
		if err := insert("10.0.0.0/8", "MM", "assigned"); err != nil {
			return err
		}
		if err := insert("172.16.0.0/12", "MM", "reserved"); err != nil {
			return err
		}
		return insert("192.0.2.0/24", "*", "reserved")
	})
	if err != nil {
		t.Fatal(err)
	}

	sources := []string{"rir"}
	all, err := st.AssignmentsByCountry(ctx, netutil.IPv4, "MM", sources, store.AssignmentFilter{})
	if err != nil || len(all) != 2 {
		t.Fatalf("unfiltered = %d, %v", len(all), err)
	}
	reserved, err := st.AssignmentsByCountry(ctx, netutil.IPv4, "MM", sources, store.AssignmentFilter{Statuses: []string{"reserved"}})
	if err != nil || len(reserved) != 1 {
		t.Fatalf("reserved = %d, %v", len(reserved), err)
	}
	start, _ := netutil.IntToAddr(reserved[0].Start, 32)
	if start.String() != "172.16.0.0" {
		t.Fatalf("reserved start = %s", start)
	}
	assigned, err := st.AssignmentsByCountry(ctx, netutil.IPv4, "MM", sources, store.AssignmentFilter{ExcludeReserved: true})
	if err != nil || len(assigned) != 1 {
		t.Fatalf("exclude reserved = %d, %v", len(assigned), err)
	}
	start, _ = netutil.IntToAddr(assigned[0].Start, 32)
	if start.String() != "10.0.0.0" {
		t.Fatalf("assigned start = %s", start)
	}
}

func TestFindASNDescriptions(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	err = st.BulkASNDescriptions(ctx, func(insert func(asn uint32, source, description string) error) error {
		rows := []struct {
			asn          uint32
			source, desc string
		}{
			{15169, "cidr_report", "GOOGLE, US"},
			{15169, "other", "GOOGLE LLC"},
			{13335, "cidr_report", "CLOUDFLARENET, US"},
			{64500, "cidr_report", "TEST-NET 100%"},
		}
		for _, row := range rows {
			if err := insert(row.asn, row.source, row.desc); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	find := func(terms ...string) []store.ASNDescriptionMatch {
		t.Helper()
		matches, err := st.FindASNDescriptions(ctx, terms, 0)
		if err != nil {
			t.Fatal(err)
		}
		return matches
	}

	google := find("google")
	if len(google) != 1 || google[0].ASN != 15169 || google[0].Description != "GOOGLE, US" {
		t.Fatalf("google = %+v", google)
	}
	us := find("us")
	if len(us) != 2 || us[0].ASN != 13335 || us[1].ASN != 15169 {
		t.Fatalf("us = %+v", us)
	}
	multi := find("cloudflare", "us")
	if len(multi) != 1 || multi[0].ASN != 13335 {
		t.Fatalf("multi = %+v", multi)
	}
	percent := find("100%")
	if len(percent) != 1 || percent[0].ASN != 64500 {
		t.Fatalf("percent = %+v", percent)
	}
	if none := find("nope"); len(none) != 0 {
		t.Fatalf("nope = %+v", none)
	}
	limited, err := st.FindASNDescriptions(ctx, []string{"us"}, 1)
	if err != nil || len(limited) != 1 || limited[0].ASN != 13335 {
		t.Fatalf("limited = %+v, %v", limited, err)
	}
	if _, err := st.FindASNDescriptions(ctx, nil, 0); err == nil {
		t.Fatal("empty term list should fail")
	}
}
