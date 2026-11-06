package netutil

import (
	"math/big"
	"testing"
)

func TestSummarizeIPv4(t *testing.T) {
	start, ok := new(big.Int).SetString("167772161", 10) // 10.0.0.1
	if !ok {
		t.Fatal("bad test constant")
	}
	end, _ := new(big.Int).SetString("167772166", 10) // 10.0.0.6
	got, err := Summarize(start, end, 32)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.1/32", "10.0.0.2/31", "10.0.0.4/31", "10.0.0.6/32"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSummarizeWholeSpace(t *testing.T) {
	start := big.NewInt(0)
	end := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	got, err := Summarize(start, end, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].String() != "::/0" {
		t.Fatalf("got %v", got)
	}
}

func TestSummarizeTopAddress(t *testing.T) {
	n, _, err := ParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Summarize(n, n, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].String() != "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128" {
		t.Fatalf("got %v", got)
	}
}

func TestHexKeyOrdering(t *testing.T) {
	max, _, err := ParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")
	if err != nil {
		t.Fatal(err)
	}
	key := HexKey(max, IPv6)
	next := NextKey(max, IPv6)
	if !(key < next) {
		t.Fatalf("max key %q must sort before its exclusive end %q", key, next)
	}
	back, err := ParseHexKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if back.Cmp(max) != 0 {
		t.Fatalf("round trip failed: %s != %s", back, max)
	}
}

func TestParseAddrUnmapsIPv4InIPv6(t *testing.T) {
	n, width, err := ParseAddr("::ffff:8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if width != 32 || n.Uint64() != 0x08080808 {
		t.Fatalf("width=%d n=%s", width, n)
	}
}

func TestParseASN(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uint32
	}{
		{"AS15169", 15169},
		{"as15169", 15169},
		{"15169", 15169},
		{"0", 0},
	} {
		got, err := ParseASN(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseASN(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	if _, err := ParseASN("AS999999999999"); err == nil {
		t.Fatal("expected overflow error")
	}
}
