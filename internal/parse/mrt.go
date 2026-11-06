package parse

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net/netip"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/sources"
	"github.com/zenware/netblocks/internal/store"
)

const (
	mrtHeaderLen             = 12
	mrtTypeTableDumpV2       = 13
	mrtSubtypeRIBIPv6Unicast = 4
	mrtMaxRecordLen          = 4 << 20
	mrtAttributeFlagExtended = 0x10
	mrtAttributeTypeASPath   = 2
	mrtAttributeTypeAS4Path  = 17
	mrtASSet                 = 1
	mrtASSequence            = 2
)

// MRT parses RouteViews TABLE_DUMP_V2 RIB_IPV6_UNICAST records.
func MRT(ctx context.Context, r io.Reader, fn func(store.ASNAnnouncement) error) (Stats, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var stats Stats
	var header [mrtHeaderLen]byte
	var body []byte
	recordNo := 0
	for {
		n, err := io.ReadFull(br, header[:])
		if err != nil {
			if n == 0 && err == io.EOF {
				return stats, nil
			}
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return stats, fmt.Errorf("truncated MRT header: %w", err)
			}
			return stats, err
		}
		recordNo++
		if recordNo%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
		}
		typ := binary.BigEndian.Uint16(header[4:6])
		subtype := binary.BigEndian.Uint16(header[6:8])
		length := binary.BigEndian.Uint32(header[8:12])
		if length > mrtMaxRecordLen {
			return stats, fmt.Errorf("MRT record %d: length %d exceeds limit", recordNo, length)
		}
		if uint32(cap(body)) < length {
			body = make([]byte, length)
		} else {
			body = body[:length]
		}
		if _, err := io.ReadFull(br, body); err != nil {
			return stats, fmt.Errorf("MRT record %d: truncated body: %w", recordNo, err)
		}
		if typ != mrtTypeTableDumpV2 || subtype != mrtSubtypeRIBIPv6Unicast {
			continue
		}
		ann, ok := parseMRTRIBIPv6(body)
		if !ok {
			stats.Skipped++
			continue
		}
		if err := fn(ann); err != nil {
			return stats, err
		}
		stats.Records++
	}
}

// parseMRTRIBIPv6 decodes one RIB_IPV6_UNICAST body. It uses the first entry
// that carries a usable origin ASN.
func parseMRTRIBIPv6(body []byte) (store.ASNAnnouncement, bool) {
	if len(body) < 5 {
		return store.ASNAnnouncement{}, false
	}
	prefixLen := int(body[4])
	if prefixLen > 128 {
		return store.ASNAnnouncement{}, false
	}
	prefixBytes := (prefixLen + 7) / 8
	off := 5 + prefixBytes
	if len(body) < off+2 {
		return store.ASNAnnouncement{}, false
	}
	entryCount := int(binary.BigEndian.Uint16(body[off:]))
	off += 2
	for range entryCount {
		if len(body) < off+8 {
			return store.ASNAnnouncement{}, false
		}
		attrLen := int(binary.BigEndian.Uint16(body[off+6:]))
		off += 8
		if len(body) < off+attrLen {
			return store.ASNAnnouncement{}, false
		}
		asn, ok := originASN(body[off : off+attrLen])
		off += attrLen
		if !ok || asn == 0 {
			continue
		}
		var addrBytes [16]byte
		copy(addrBytes[:], body[5:5+prefixBytes])
		p := netip.PrefixFrom(netip.AddrFrom16(addrBytes), prefixLen).Masked()
		start := netutil.AddrToInt(p.Addr())
		hostBits := 128 - prefixLen
		end := new(big.Int).Add(start, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(hostBits)), big.NewInt(1)))
		return store.ASNAnnouncement{
			Start:      start,
			End:        end,
			NumType:    netutil.IPv6,
			ASN:        asn,
			SourceType: sources.SourceBGP,
			SourceName: sources.BGPNameRouteViews6,
		}, true
	}
	return store.ASNAnnouncement{}, false
}

// originASN walks BGP path attributes and returns the last ASN of the path.
// AS4_PATH wins over a 4-byte AS_PATH, which wins over a 2-byte AS_PATH.
func originASN(attrs []byte) (uint32, bool) {
	var asPath, as4Path []byte
	for len(attrs) > 0 {
		if len(attrs) < 3 {
			return 0, false
		}
		flags := attrs[0]
		typ := attrs[1]
		length := int(attrs[2])
		attrs = attrs[3:]
		if flags&mrtAttributeFlagExtended != 0 {
			if len(attrs) < 1 {
				return 0, false
			}
			length = length<<8 | int(attrs[0])
			attrs = attrs[1:]
		}
		if len(attrs) < length {
			return 0, false
		}
		value := attrs[:length]
		attrs = attrs[length:]
		switch typ {
		case mrtAttributeTypeASPath:
			asPath = value
		case mrtAttributeTypeAS4Path:
			as4Path = value
		}
	}
	if as4Path != nil {
		if asn, ok := pathOrigin(as4Path, 4); ok {
			return asn, true
		}
	}
	if asPath != nil {
		if asn, ok := pathOrigin(asPath, 4); ok {
			return asn, true
		}
		if asn, ok := pathOrigin(asPath, 2); ok {
			return asn, true
		}
	}
	return 0, false
}

// pathOrigin returns the last ASN of the last non-empty segment in an
// AS_PATH/AS4_PATH value.
func pathOrigin(value []byte, asnSize int) (uint32, bool) {
	var (
		origin uint32
		found  bool
	)
	for len(value) > 0 {
		if len(value) < 2 {
			return 0, false
		}
		segType := value[0]
		count := int(value[1])
		value = value[2:]
		if segType != mrtASSet && segType != mrtASSequence {
			return 0, false
		}
		n := count * asnSize
		if len(value) < n {
			return 0, false
		}
		if count > 0 {
			last := value[n-asnSize : n]
			switch asnSize {
			case 4:
				origin = binary.BigEndian.Uint32(last)
			case 2:
				origin = uint32(binary.BigEndian.Uint16(last))
			}
			found = true
		}
		value = value[n:]
	}
	if !found {
		return 0, false
	}
	return origin, true
}
