// Package netutil contains IP address, range, and prefix arithmetic shared by
// the parsers, the cache, and the query layer.
package netutil

import (
	"fmt"
	"math/big"
	"math/bits"
	"net/netip"
	"strconv"
	"strings"
)

// NumType names the three kinds of numbers stored in the cache.
type NumType string

const (
	IPv4 NumType = "ipv4"
	IPv6 NumType = "ipv6"
	ASN  NumType = "asn"
)

// Bits returns the width used when encoding a number type as an integer.
func (t NumType) Bits() int {
	if t == IPv6 {
		return 128
	}
	return 32
}

// KeyWidth is the fixed width of the hexadecimal keys used in SQLite. One
// extra nibble is reserved so that end+1 remains representable for a /32 or
// /128 at the top of the address space.
func (t NumType) KeyWidth() int {
	if t == IPv6 {
		return 34
	}
	return 10
}

// Valid reports whether t is one of the supported number types.
func (t NumType) Valid() bool {
	return t == IPv4 || t == IPv6 || t == ASN
}

// TypeForBits returns the number type for an address width.
func TypeForBits(width int) NumType {
	if width == 128 {
		return IPv6
	}
	return IPv4
}

// AddrToInt converts an address to its integer representation.
func AddrToInt(a netip.Addr) *big.Int {
	a = a.Unmap()
	if a.Is4() {
		b := a.As4()
		return new(big.Int).SetBytes(b[:])
	}
	b := a.As16()
	return new(big.Int).SetBytes(b[:])
}

// IntToAddr converts an integer to an address of the given width.
func IntToAddr(n *big.Int, width int) (netip.Addr, error) {
	if n == nil || n.Sign() < 0 {
		return netip.Addr{}, fmt.Errorf("negative address value")
	}
	switch width {
	case 32:
		if n.BitLen() > 32 {
			return netip.Addr{}, fmt.Errorf("value %s does not fit in IPv4", n)
		}
		var b [4]byte
		n.FillBytes(b[:])
		return netip.AddrFrom4(b), nil
	case 128:
		if n.BitLen() > 128 {
			return netip.Addr{}, fmt.Errorf("value %s does not fit in IPv6", n)
		}
		var b [16]byte
		n.FillBytes(b[:])
		return netip.AddrFrom16(b), nil
	default:
		return netip.Addr{}, fmt.Errorf("unsupported address width %d", width)
	}
}

// ParseAddr parses an address and returns its integer value and width.
func ParseAddr(s string) (*big.Int, int, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return nil, 0, err
	}
	a = a.Unmap()
	if a.Is4() {
		return AddrToInt(a), 32, nil
	}
	return AddrToInt(a), 128, nil
}

// ParsePrefix parses a CIDR prefix and returns its value range and width.
func ParsePrefix(s string) (start, end *big.Int, width int, err error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return nil, nil, 0, err
	}
	p = p.Masked()
	width = p.Addr().BitLen()
	start = AddrToInt(p.Addr())
	hostBits := width - p.Bits()
	size := new(big.Int).Lsh(big.NewInt(1), uint(hostBits))
	end = new(big.Int).Sub(new(big.Int).Add(start, size), big.NewInt(1))
	return start, end, width, nil
}

// Summarize decomposes an inclusive integer range into the smallest possible
// list of CIDR prefixes.
func Summarize(start, end *big.Int, width int) ([]netip.Prefix, error) {
	if width != 32 && width != 128 {
		return nil, fmt.Errorf("unsupported address width %d", width)
	}
	if start == nil || end == nil || start.Sign() < 0 || end.Sign() < 0 {
		return nil, fmt.Errorf("invalid range")
	}
	if start.Cmp(end) > 0 {
		return nil, fmt.Errorf("range start %s is after end %s", start, end)
	}
	limit := new(big.Int).Lsh(big.NewInt(1), uint(width))
	if end.Cmp(limit) >= 0 {
		return nil, fmt.Errorf("range end %s exceeds %d-bit address space", end, width)
	}

	cur := new(big.Int).Set(start)
	remaining := new(big.Int).Sub(end, start)
	remaining.Add(remaining, big.NewInt(1))
	out := make([]netip.Prefix, 0, 4)
	for remaining.Sign() > 0 {
		exp := trailingZeros(cur, width)
		if maxExp := remaining.BitLen() - 1; exp > maxExp {
			exp = maxExp
		}
		addr, err := IntToAddr(cur, width)
		if err != nil {
			return nil, err
		}
		p := netip.PrefixFrom(addr, width-exp).Masked()
		out = append(out, p)
		step := new(big.Int).Lsh(big.NewInt(1), uint(exp))
		cur.Add(cur, step)
		remaining.Sub(remaining, step)
	}
	return out, nil
}

func trailingZeros(n *big.Int, width int) int {
	if n.Sign() == 0 {
		return width
	}
	count := 0
	for _, w := range n.Bits() {
		if w == 0 {
			count += bits.UintSize
			continue
		}
		count += bits.TrailingZeros(uint(w))
		break
	}
	if count > width {
		count = width
	}
	return count
}

// HexKey renders n as a fixed-width lowercase hexadecimal key. Fixed width
// makes lexical ordering in SQLite identical to numeric ordering.
func HexKey(n *big.Int, t NumType) string {
	w := t.KeyWidth()
	s := n.Text(16)
	if len(s) < w {
		return strings.Repeat("0", w-len(s)) + s
	}
	return s
}

// ParseHexKey parses a hexadecimal key produced by HexKey.
func ParseHexKey(s string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(strings.TrimSpace(s), 16)
	if !ok || n == nil {
		return nil, fmt.Errorf("invalid hexadecimal key %q", s)
	}
	return n, nil
}

// NextKey returns the exclusive end key for an inclusive range.
func NextKey(end *big.Int, t NumType) string {
	return HexKey(new(big.Int).Add(end, big.NewInt(1)), t)
}

// ParseASN accepts "AS123", "as123", or "123" and returns the numeric ASN.
func ParseASN(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == 'a' || s[0] == 'A') && (s[1] == 's' || s[1] == 'S') {
		s = s[2:]
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid ASN %q", s)
	}
	return uint32(v), nil
}

// FormatASN renders an ASN in the conventional ASxxxx form.
func FormatASN(asn uint32) string {
	return "AS" + strconv.FormatUint(uint64(asn), 10)
}
