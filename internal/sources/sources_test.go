package sources_test

import (
	"testing"
	"time"

	"github.com/zenware/netblocks/internal/sources"
)

func TestRouteViews6RIBURL(t *testing.T) {
	cases := []struct {
		now  string
		want string
	}{
		{"2026-11-06T18:38:00Z", "https://archive.routeviews.org/route-views6/bgpdata/2026.11/RIBS/rib.20261106.1600.bz2"},
		{"2026-11-01T00:30:00Z", "https://archive.routeviews.org/route-views6/bgpdata/2026.10/RIBS/rib.20261031.2200.bz2"},
	}
	for _, c := range cases {
		now, err := time.Parse(time.RFC3339, c.now)
		if err != nil {
			t.Fatal(err)
		}
		if got := sources.RouteViews6RIBURL(now); got != c.want {
			t.Errorf("RouteViews6RIBURL(%s) = %q, want %q", c.now, got, c.want)
		}
	}
}
