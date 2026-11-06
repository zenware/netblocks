// Package sources describes the upstream datasets netblocks knows how to
// download and import.
package sources

import (
	"fmt"
	"net/url"
	"path"
)

// RIR describes a Regional Internet Registry delegation file.
type RIR struct {
	Registry    string
	Description string
	URL         string
}

// RIRFiles are the NRO extended delegation statistics files. ARIN publishes
// the extended variant (status and opaque-id columns); the others publish the
// standard variant.
var RIRFiles = []RIR{
	{"arin", "Canada, United States, and parts of the Caribbean", "https://ftp.arin.net/pub/stats/arin/delegated-arin-extended-latest"},
	{"ripencc", "Europe, Russia, the Middle East, and Central Asia", "https://ftp.ripe.net/pub/stats/ripencc/delegated-ripencc-latest"},
	{"afrinic", "Africa", "https://ftp.afrinic.net/pub/stats/afrinic/delegated-afrinic-latest"},
	{"apnic", "Asia and the Pacific", "https://ftp.apnic.net/stats/apnic/delegated-apnic-latest"},
	{"lacnic", "Latin America and parts of the Caribbean", "https://ftp.lacnic.net/pub/stats/lacnic/delegated-lacnic-latest"},
}

// LIRURLs are RIPE's split inetnum and inet6num databases. They are large;
// --init-lir can take several minutes.
var LIRURLs = []string{
	"https://ftp.ripe.net/ripe/dbase/split/ripe.db.inetnum.gz",
	"https://ftp.ripe.net/ripe/dbase/split/ripe.db.inet6num.gz",
}

const (
	// CountryCodesURL is a maintained ISO 3166-1 list (Name,Code CSV).
	CountryCodesURL = "https://raw.githubusercontent.com/datasets/country-list/master/data.csv"

	// ASNDescriptionURL maps ASNs to organization names.
	ASNDescriptionURL = "https://www.cidr-report.org/as2.0/autnums.html"

	// ASNAssignmentURL is a full BGP RIB snapshot from RouteViews.
	ASNAssignmentURL = "https://archive.routeviews.org/oix-route-views/oix-full-snapshot-latest.dat.bz2"

	// MaxMindGeoLite2URL is templated with a license key.
	MaxMindGeoLite2URL = "https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-Country-CSV&license_key=%s&suffix=zip"
	MaxMindZipName     = "GeoLite2-Country-CSV.zip"

	// CountryCodesFile is the cache filename for CountryCodesURL.
	CountryCodesFile = "country-list.csv"
)

// MaxMindURL returns the GeoLite2 download URL for a license key.
func MaxMindURL(licenseKey string) string {
	return fmt.Sprintf(MaxMindGeoLite2URL, url.QueryEscape(licenseKey))
}

// Filename returns the cache filename for a URL.
func Filename(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return path.Base(rawURL)
	}
	return path.Base(u.Path)
}

// Source type names stored in the assignments table.
const (
	SourceRIR     = "rir"
	SourceLIR     = "lir"
	SourceMaxMind = "maxmind"
	SourceBGP     = "bgp_snapshot"
)

// AssignmentsSourceTypes are source types stored in the assignments table.
var AssignmentsSourceTypes = []string{SourceRIR, SourceLIR, SourceMaxMind}

// DefaultLookupSources is the order used for reverse IP lookups.
var DefaultLookupSources = []string{SourceMaxMind, SourceRIR, SourceLIR}
