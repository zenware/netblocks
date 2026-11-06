// Package sources describes the upstream datasets netblocks knows how to
// download and import.
package sources

import (
	"fmt"
	"net/url"
	"path"
	"time"
)

// BGP source names, used as the source_name column for BGP announcements.
const (
	BGPNameRouteViews  = "routeviews"
	BGPNameRouteViews6 = "routeviews6"
)

// BGPFormat is the on-disk encoding of a BGP snapshot.
type BGPFormat string

const (
	BGPText BGPFormat = "text"
	BGPMRT  BGPFormat = "mrt"
)

// BGPSource describes one BGP RIB snapshot source.
type BGPSource struct {
	Name        string
	Description string
	URL         string // empty when the URL is time-dependent
	Filename    string // stable cache filename
	Format      BGPFormat
}

// BGPSources are the BGP snapshots netblocks can download and import.
var BGPSources = []BGPSource{
	{
		Name:        BGPNameRouteViews,
		Description: "RouteViews OIX full snapshot (IPv4 text)",
		URL:         ASNAssignmentURL,
		Filename:    "oix-full-snapshot-latest.dat.bz2",
		Format:      BGPText,
	},
	{
		Name:        BGPNameRouteViews6,
		Description: "RouteViews route-views6 RIB (IPv6 MRT)",
		Filename:    "route-views6-rib.bz2",
		Format:      BGPMRT,
	},
}

// BGPSourceByName returns the BGP source with the given name.
func BGPSourceByName(name string) (BGPSource, bool) {
	for _, src := range BGPSources {
		if src.Name == name {
			return src, true
		}
	}
	return BGPSource{}, false
}

// RouteViews6RIBURL returns the RIB URL for the latest 2-hour slot at or
// before t-2h, leaving time for the archive to publish.
func RouteViews6RIBURL(t time.Time) string {
	slot := t.UTC().Add(-2 * time.Hour).Truncate(2 * time.Hour)
	return fmt.Sprintf("https://archive.routeviews.org/route-views6/bgpdata/%s/RIBS/rib.%s.bz2",
		slot.Format("2006.01"), slot.Format("20060102.1504"))
}

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

	// ASNAssignmentURL is the RouteViews OIX full BGP RIB snapshot in text
	// form. Despite its name it carries IPv4 announcements only.
	ASNAssignmentURL = "https://archive.routeviews.org/oix-route-views/oix-full-snapshot-latest.dat.bz2"

	// MaxMindGeoLite2URL is templated with a license key.
	MaxMindGeoLite2URL = "https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-Country-CSV&license_key=%s&suffix=zip"
	MaxMindZipName     = "GeoLite2-Country-CSV.zip"

	// MaxMindGeoLite2ASNURL is templated with a license key.
	MaxMindGeoLite2ASNURL = "https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-ASN-CSV&license_key=%s&suffix=zip"
	MaxMindASNZipName     = "GeoLite2-ASN-CSV.zip"

	// CountryCodesFile is the cache filename for CountryCodesURL.
	CountryCodesFile = "country-list.csv"
)

// MaxMindURL returns the GeoLite2 download URL for a license key.
func MaxMindURL(licenseKey string) string {
	return fmt.Sprintf(MaxMindGeoLite2URL, url.QueryEscape(licenseKey))
}

// MaxMindASNURL returns the GeoLite2 ASN download URL for a license key.
func MaxMindASNURL(licenseKey string) string {
	return fmt.Sprintf(MaxMindGeoLite2ASNURL, url.QueryEscape(licenseKey))
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
