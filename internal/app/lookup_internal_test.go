package app

import (
	"math/big"
	"testing"

	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

func TestASNStringsExpansion(t *testing.T) {
	list := []store.Assignment{{
		Start: big.NewInt(64500), End: big.NewInt(64502), NumType: netutil.ASN, CountryCode: "US",
	}}
	got, err := asnStrings(list, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 64500 || got[2] != 64502 {
		t.Fatalf("expanded = %v", got)
	}
	got, err = asnStrings(list, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 64500 {
		t.Fatalf("unexpanded = %v", got)
	}
}

func TestBlockStringsMerge(t *testing.T) {
	start, end, _, err := netutil.ParsePrefix("10.0.0.0/25")
	if err != nil {
		t.Fatal(err)
	}
	start2, end2, _, err := netutil.ParsePrefix("10.0.0.128/25")
	if err != nil {
		t.Fatal(err)
	}
	list := []store.Assignment{
		{Start: start, End: end, NumType: netutil.IPv4, CountryCode: "US"},
		{Start: start2, End: end2, NumType: netutil.IPv4, CountryCode: "US"},
	}
	got, err := blockStrings(list, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "10.0.0.0/24" {
		t.Fatalf("merged = %v", got)
	}
	got, err = blockStrings(list, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("unmerged = %v", got)
	}
}
