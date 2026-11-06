package parse

import (
	"context"
	"strings"
	"testing"
)

func TestASNDescriptions(t *testing.T) {
	input := `<html><body><pre>
<a href="/cgi-bin/as-report?as=AS1&view=2.0">AS1    </a> LVLT-1 - Level 3 Parent, LLC, US
<a href="/cgi-bin/as-report?as=AS13335&view=2.0">AS13335    </a> CLOUDFLARENET &amp; Friends, US
<a href="/cgi-bin/as-report?as=AS99999&view=2.0">AS99999</a>
</pre></body></html>
`
	got := make(map[uint32]string)
	stats, err := ASNDescriptions(context.Background(), strings.NewReader(input), func(asn uint32, description string) error {
		got[asn] = description
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Records != 2 {
		t.Fatalf("records = %d", stats.Records)
	}
	if got[1] != "LVLT-1 - Level 3 Parent, LLC, US" {
		t.Fatalf("AS1 = %q", got[1])
	}
	if got[13335] != "CLOUDFLARENET & Friends, US" {
		t.Fatalf("AS13335 = %q", got[13335])
	}
}
