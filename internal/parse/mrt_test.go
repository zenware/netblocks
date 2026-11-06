package parse_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/parse"
	"github.com/zenware/netblocks/internal/store"
)

func mrtRecord(typ, subtype uint16, body []byte) []byte {
	b := make([]byte, 12+len(body))
	binary.BigEndian.PutUint16(b[4:6], typ)
	binary.BigEndian.PutUint16(b[6:8], subtype)
	binary.BigEndian.PutUint32(b[8:12], uint32(len(body)))
	copy(b[12:], body)
	return b
}

func ribIPv6Record(prefixLen int, prefix []byte, entries [][]byte) []byte {
	b := make([]byte, 5, 7+len(prefix))
	b[4] = byte(prefixLen)
	b = append(b, prefix...)
	var count [2]byte
	binary.BigEndian.PutUint16(count[:], uint16(len(entries)))
	b = append(b, count[:]...)
	for _, e := range entries {
		b = append(b, e...)
	}
	return b
}

func ribEntry(attrs []byte) []byte {
	b := make([]byte, 8, 8+len(attrs))
	binary.BigEndian.PutUint16(b[6:8], uint16(len(attrs)))
	return append(b, attrs...)
}

func attr(flags, typ byte, value []byte) []byte {
	return append([]byte{flags, typ, byte(len(value))}, value...)
}

func asPath(asnSize int, asns ...uint32) []byte {
	value := []byte{2, byte(len(asns))}
	for _, asn := range asns {
		if asnSize == 2 {
			value = binary.BigEndian.AppendUint16(value, uint16(asn))
			continue
		}
		value = binary.BigEndian.AppendUint32(value, asn)
	}
	return value
}

func asPath4(asns ...uint32) []byte {
	return attr(0x40, 2, asPath(4, asns...))
}

func asPath2(asns ...uint32) []byte {
	return attr(0x40, 2, asPath(2, asns...))
}

func as4Path(asns ...uint32) []byte {
	return attr(0xc0, 17, asPath(4, asns...))
}

func TestMRTIPv6RIB(t *testing.T) {
	ctx := context.Background()
	prefix := []byte{0x20, 0x01, 0x0d, 0xb8}
	stream := mrtRecord(13, 1, nil)
	stream = append(stream, mrtRecord(13, 4, ribIPv6Record(32, prefix, [][]byte{
		ribEntry(asPath4(64500, 15169)),
	}))...)

	var got []store.ASNAnnouncement
	stats, err := parse.MRT(ctx, bytes.NewReader(stream), func(a store.ASNAnnouncement) error {
		got = append(got, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 1 || stats.Skipped != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(got) != 1 {
		t.Fatalf("announcements = %+v", got)
	}
	wantStart, wantEnd, _, err := netutil.ParsePrefix("2001:db8::/32")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Start.Cmp(wantStart) != 0 || got[0].End.Cmp(wantEnd) != 0 {
		t.Fatalf("range = [%s, %s], want [%s, %s]", got[0].Start, got[0].End, wantStart, wantEnd)
	}
	if got[0].ASN != 15169 || got[0].NumType != netutil.IPv6 {
		t.Fatalf("announcement = %+v", got[0])
	}
	if got[0].SourceType != "bgp_snapshot" || got[0].SourceName != "routeviews6" {
		t.Fatalf("source = %s/%s", got[0].SourceType, got[0].SourceName)
	}
}

func TestMRTAS4Path(t *testing.T) {
	prefix := []byte{0x20, 0x01, 0x0d, 0xb8}
	attrs := append(asPath2(64500, 23456), as4Path(64500, 396982)...)
	stream := mrtRecord(13, 4, ribIPv6Record(32, prefix, [][]byte{ribEntry(attrs)}))

	var got []store.ASNAnnouncement
	if _, err := parse.MRT(context.Background(), bytes.NewReader(stream), func(a store.ASNAnnouncement) error {
		got = append(got, a)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ASN != 396982 {
		t.Fatalf("announcements = %+v", got)
	}
}

func TestMRTIgnoresPeerIndexTable(t *testing.T) {
	stream := mrtRecord(13, 1, nil)
	stats, err := parse.MRT(context.Background(), bytes.NewReader(stream), func(store.ASNAnnouncement) error {
		t.Fatal("callback invoked for PEER_INDEX_TABLE")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 0 || stats.Skipped != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestMRTMalformedRecords(t *testing.T) {
	prefix := []byte{0x20, 0x01, 0x0d, 0xb8}
	records := map[string][]byte{
		"prefix length": mrtRecord(13, 4, ribIPv6Record(129, make([]byte, 17), nil)),
		"entry header":  mrtRecord(13, 4, ribIPv6Record(32, prefix, [][]byte{{0, 0}})),
		"attribute length": mrtRecord(13, 4, ribIPv6Record(32, prefix, [][]byte{
			append([]byte{0, 0, 0, 0, 0, 0, 0, 10}, 1, 2, 3),
		})),
	}
	for name, stream := range records {
		t.Run(name, func(t *testing.T) {
			stats, err := parse.MRT(context.Background(), bytes.NewReader(stream), func(store.ASNAnnouncement) error {
				t.Fatal("callback invoked for malformed record")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if stats.Records != 0 || stats.Skipped != 1 {
				t.Fatalf("stats = %+v", stats)
			}
		})
	}
}

func TestMRTCallbackError(t *testing.T) {
	sentinel := errors.New("stop")
	prefix := []byte{0x20, 0x01, 0x0d, 0xb8}
	stream := mrtRecord(13, 4, ribIPv6Record(32, prefix, [][]byte{ribEntry(asPath4(15169))}))
	_, err := parse.MRT(context.Background(), bytes.NewReader(stream), func(store.ASNAnnouncement) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v", err)
	}
}

func TestMRTUsesLaterEntry(t *testing.T) {
	prefix := []byte{0x20, 0x01, 0x0d, 0xb8}
	stream := mrtRecord(13, 4, ribIPv6Record(32, prefix, [][]byte{
		ribEntry(attr(0x40, 5, []byte{0, 0, 0, 100})),
		ribEntry(asPath4(64500, 15169)),
	}))

	var got []store.ASNAnnouncement
	if _, err := parse.MRT(context.Background(), bytes.NewReader(stream), func(a store.ASNAnnouncement) error {
		got = append(got, a)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ASN != 15169 {
		t.Fatalf("announcements = %+v", got)
	}
}
