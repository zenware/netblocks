// Command netblocks returns IP netblocks and ASNs for a country, and provides
// reverse IP/ASN lookups from RIR, RIPE LIR, MaxMind, and BGP data.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zenware/netblocks/internal/app"
)

const version = "1.0.0"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("netblocks", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = usage(fs)

	var (
		cacheDir  = app.DefaultCacheDir()
		verbose   bool
		userAgent = app.DefaultUserAgent()
		quiet     bool
		jsonOut   bool
		offline   bool
		retries   = 3
		timeout   time.Duration
		versionF  bool
		hack      bool

		initRIR         bool
		reloadRIR       bool
		initLIR         bool
		reloadLIR       bool
		initMaxMind     bool
		reloadMaxMind   bool
		importMaxMind   string
		downloadCC      bool
		eraseCache      bool
		initASNDesc     bool
		reloadASNDesc   bool
		initASNAssign   bool
		reloadASNAssign bool
		initAll         bool
		legacyCC        string

		ipv4           string
		ipv6           string
		asn            string
		code           string
		name           string
		compare        string
		compareAll     bool
		whatCC         string
		lookupOrgIP    string
		lookupOrgRange bool
		rangeStart     string
		rangeEnd       string

		exportGeoIP bool
		geoV4       string
		geoV6       string
		geoASN      string
		geoASNv6    string

		listCC        bool
		reservedCount bool
		stats         bool

		sourceFlag    string
		asnSourceFlag string
		merge         = true
		noMerge       bool
		expandASNs    = true
		noExpandASNs  bool
		maxmindKey    string
	)
	fs.StringVar(&cacheDir, "c", cacheDir, "cache directory")
	fs.StringVar(&cacheDir, "cache-dir", cacheDir, "cache directory (same as -c)")
	fs.BoolVar(&verbose, "v", false, "be verbose")
	fs.BoolVar(&verbose, "verbose", false, "be verbose (same as -v)")
	fs.StringVar(&userAgent, "user-agent", userAgent, "User-Agent used when fetching files")
	fs.BoolVar(&quiet, "quiet", false, "suppress informational output")
	fs.BoolVar(&jsonOut, "json", false, "write machine-readable JSON for lookup commands")
	fs.BoolVar(&offline, "offline", false, "never download; use only cached files")
	fs.IntVar(&retries, "retries", 3, "download retry attempts")
	fs.DurationVar(&timeout, "timeout", 0, "overall timeout (0 means none)")
	fs.BoolVar(&versionF, "version", false, "print version and exit")
	fs.BoolVar(&hack, "x", false, "hack the internet")
	fs.BoolVar(&hack, "hack-the-internet", false, "hack the internet")

	fs.BoolVar(&initRIR, "i", false, "download and import RIR delegation data")
	fs.BoolVar(&initRIR, "init-rir", false, "download and import RIR delegation data")
	fs.BoolVar(&reloadRIR, "d", false, "import already-downloaded RIR delegation data")
	fs.BoolVar(&reloadRIR, "reload-rir", false, "import already-downloaded RIR delegation data")
	fs.BoolVar(&initLIR, "l", false, "download and import RIPE LIR data (large)")
	fs.BoolVar(&initLIR, "init-lir", false, "download and import RIPE LIR data (large)")
	fs.BoolVar(&reloadLIR, "z", false, "import already-downloaded RIPE LIR data")
	fs.BoolVar(&reloadLIR, "reload-lir", false, "import already-downloaded RIPE LIR data")
	fs.BoolVar(&initMaxMind, "m", false, "download and import MaxMind GeoLite2 (license required)")
	fs.BoolVar(&initMaxMind, "init-maxmind", false, "download and import MaxMind GeoLite2 (license required)")
	fs.BoolVar(&reloadMaxMind, "g", false, "import already-downloaded MaxMind data")
	fs.BoolVar(&reloadMaxMind, "reload-maxmind", false, "import already-downloaded MaxMind data")
	fs.StringVar(&importMaxMind, "r", "", "import a MaxMind file or directory")
	fs.StringVar(&importMaxMind, "import-maxmind", "", "import a MaxMind file or directory")
	fs.BoolVar(&downloadCC, "o", false, "download the ISO country code list")
	fs.BoolVar(&downloadCC, "download-cc", false, "download the ISO country code list")
	fs.BoolVar(&eraseCache, "e", false, "erase the local database cache")
	fs.BoolVar(&eraseCache, "erase-cache", false, "erase the local database cache")
	fs.BoolVar(&initASNDesc, "j", false, "download and import ASN descriptions")
	fs.BoolVar(&initASNDesc, "init-asn-descriptions", false, "download and import ASN descriptions")
	fs.BoolVar(&reloadASNDesc, "k", false, "import already-downloaded ASN descriptions")
	fs.BoolVar(&reloadASNDesc, "reload-asn-descriptions", false, "import already-downloaded ASN descriptions")
	fs.BoolVar(&initASNAssign, "y", false, "download and import a BGP snapshot (large)")
	fs.BoolVar(&initASNAssign, "init-asn-assignments", false, "download and import a BGP snapshot (large)")
	fs.BoolVar(&reloadASNAssign, "u", false, "import already-downloaded BGP snapshot")
	fs.BoolVar(&reloadASNAssign, "reload-asn-assignments", false, "import already-downloaded BGP snapshot")
	fs.BoolVar(&initAll, "init", false, "download and import all datasets")
	fs.StringVar(&legacyCC, "cc", "", "country code to look up (same as -t)")

	fs.StringVar(&ipv4, "4", "", "look up the country for an IPv4 address")
	fs.StringVar(&ipv4, "ipv4", "", "look up the country for an IPv4 address")
	fs.StringVar(&ipv6, "6", "", "look up the country for an IPv6 address")
	fs.StringVar(&ipv6, "ipv6", "", "look up the country for an IPv6 address")
	fs.StringVar(&asn, "a", "", "look up the country and description for an ASN")
	fs.StringVar(&asn, "asn", "", "look up the country and description for an ASN")
	fs.StringVar(&code, "t", "", "list allocations for CC[:ipv4|ipv6|asn]")
	fs.StringVar(&code, "code", "", "list allocations for CC[:ipv4|ipv6|asn]")
	fs.StringVar(&name, "n", "", "list allocations for a country name[:type]")
	fs.StringVar(&name, "name", "", "list allocations for a country name[:type]")
	fs.StringVar(&compare, "p", "", "compare overlapping assignments for a country")
	fs.StringVar(&compare, "compare", "", "compare overlapping assignments for a country")
	fs.BoolVar(&compareAll, "compare-all", false, "show all overlapping assignments, not just country-code conflicts")
	fs.StringVar(&whatCC, "w", "", "print the country name for a code")
	fs.StringVar(&whatCC, "what-country", "", "print the country name for a code")
	fs.StringVar(&lookupOrgIP, "lookup-org-by-ip", "", "look up ASN and description for an IP")
	fs.BoolVar(&lookupOrgRange, "lookup-org-by-range", false, "look up announcements inside --range-start/--range-end")
	fs.StringVar(&rangeStart, "range-start", "", "range start address")
	fs.StringVar(&rangeEnd, "range-end", "", "range end address")

	fs.BoolVar(&exportGeoIP, "export-geoip", false, "export GeoIP-compatible CSV files")
	fs.StringVar(&geoV4, "geoip-v4-file", "", "IPv4 GeoIP CSV output file")
	fs.StringVar(&geoV6, "geoip-v6-file", "", "IPv6 GeoIP CSV output file")
	fs.StringVar(&geoASN, "geoip-asn-file", "", "ASN GeoIP CSV output file")
	fs.StringVar(&geoASNv6, "geoip-asn-v6-file", "", "IPv6 ASN GeoIP CSV output file")

	fs.BoolVar(&listCC, "list-cc", false, "list known ISO country codes")
	fs.BoolVar(&reservedCount, "reserved-count", false, "count reserved/available RIR records")
	fs.BoolVar(&stats, "stats", false, "show cache row counts")

	fs.StringVar(&sourceFlag, "source", "", "comma-separated sources: rir,lir,maxmind,all")
	fs.StringVar(&asnSourceFlag, "asn-source", "", "comma-separated BGP sources: routeviews,routeviews6,all")
	fs.BoolVar(&merge, "merge", true, "merge adjacent/overlapping blocks")
	fs.BoolVar(&noMerge, "no-merge", false, "do not merge blocks")
	fs.BoolVar(&expandASNs, "expand-asns", true, "expand multi-ASN allocations")
	fs.BoolVar(&noExpandASNs, "no-expand-asns", false, "only print the first ASN of each allocation")
	fs.StringVar(&maxmindKey, "maxmind-license-key", "", "MaxMind license key (or MAXMIND_LICENSE_KEY)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if versionF {
		fmt.Printf("netblocks %s\n", version)
		return 0
	}
	if hack {
		fmt.Println("all your bases are belong to us!")
		return 0
	}

	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	selected := make([]string, 0, 2)
	add := func(mode string, active bool) {
		if active {
			selected = append(selected, mode)
		}
	}
	add("init-all", initAll)
	add("init-rir", initRIR)
	add("reload-rir", reloadRIR)
	add("init-lir", initLIR)
	add("reload-lir", reloadLIR)
	add("init-maxmind", initMaxMind)
	add("reload-maxmind", reloadMaxMind)
	add("import-maxmind", set["r"] || set["import-maxmind"])
	add("download-cc", downloadCC)
	add("erase-cache", eraseCache)
	add("init-asn-descriptions", initASNDesc)
	add("reload-asn-descriptions", reloadASNDesc)
	add("init-asn-assignments", initASNAssign)
	add("reload-asn-assignments", reloadASNAssign)
	add("list-cc", listCC)
	add("reserved-count", reservedCount)
	add("stats", stats)
	add("export-geoip", exportGeoIP)

	ip4Set := set["4"] || set["ipv4"]
	ip6Set := set["6"] || set["ipv6"]
	if ip4Set && ip6Set {
		fmt.Fprintln(os.Stderr, "netblocks: only one of -4 and -6 may be used")
		return 2
	}
	if ip4Set || ip6Set {
		selected = append(selected, "ip")
	}
	add("asn", set["a"] || set["asn"] || asn != "")
	add("code", set["t"] || set["code"] || set["cc"] || code != "" || legacyCC != "")
	add("name", set["n"] || set["name"] || name != "")
	add("compare", set["p"] || set["compare"] || compare != "")
	add("what-country", set["w"] || set["what-country"] || whatCC != "")
	add("lookup-org-by-ip", set["lookup-org-by-ip"] || lookupOrgIP != "")
	add("lookup-org-by-range", lookupOrgRange)

	if len(selected) > 1 {
		fmt.Fprintf(os.Stderr, "netblocks: only one cache, lookup, or export mode is allowed (got %s)\n", strings.Join(selected, ", "))
		return 2
	}
	if len(selected) == 0 {
		fs.Usage()
		return 2
	}

	opts := app.Options{
		Mode:           selected[0],
		CacheDir:       cacheDir,
		Out:            os.Stdout,
		Err:            os.Stderr,
		UserAgent:      userAgent,
		Verbose:        verbose,
		Quiet:          quiet,
		JSON:           jsonOut,
		Offline:        offline,
		Retries:        retries,
		Merge:          merge && !noMerge,
		ExpandASNs:     expandASNs && !noExpandASNs,
		CompareAll:     compareAll,
		ImportMaxMind:  importMaxMind,
		MaxMindLicense: maxmindKey,
		GeoIPV4File:    geoV4,
		GeoIPV6File:    geoV6,
		GeoIPASNFile:   geoASN,
		GeoIPASNv6File: geoASNv6,
		IP:             firstNonEmpty(ipv4, ipv6),
		ASN:            asn,
		Code:           firstNonEmpty(code, legacyCC),
		Name:           name,
		Compare:        compare,
		WhatCountry:    whatCC,
		LookupOrgIP:    lookupOrgIP,
		RangeStart:     rangeStart,
		RangeEnd:       rangeEnd,
	}
	if set["code"] || set["cc"] || set["t"] {
		opts.Code, opts.TypeFilter = splitSpec(opts.Code)
	}
	if set["name"] || set["n"] {
		opts.Name, opts.TypeFilter = splitSpec(opts.Name)
	}
	if lookupOrgRange && (opts.RangeStart == "" || opts.RangeEnd == "") {
		fmt.Fprintln(os.Stderr, "netblocks: --lookup-org-by-range requires --range-start and --range-end")
		return 2
	}
	if !lookupOrgRange && (opts.RangeStart != "" || opts.RangeEnd != "") {
		fmt.Fprintln(os.Stderr, "netblocks: --range-start/--range-end require --lookup-org-by-range")
		return 2
	}
	if opts.ImportMaxMind == "" && (set["r"] || set["import-maxmind"]) {
		fmt.Fprintln(os.Stderr, "netblocks: -r/--import-maxmind requires a file or directory")
		return 2
	}
	if sourceFlag != "" {
		srcs, err := app.ParseSources(sourceFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "netblocks: %v\n", err)
			return 2
		}
		opts.Sources = srcs
	}
	if asnSourceFlag != "" {
		names, err := app.ParseBGPSources(asnSourceFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "netblocks: %v\n", err)
			return 2
		}
		opts.BGPSources = names
	}
	if len(opts.BGPSources) > 0 {
		switch opts.Mode {
		case "init-asn-assignments", "reload-asn-assignments", "init-all":
		default:
			fmt.Fprintln(os.Stderr, "netblocks: --asn-source requires -y, -u, or --init")
			return 2
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := app.Run(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "netblocks: %v\n", err)
		return 1
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func splitSpec(s string) (value, filter string) {
	value = strings.TrimSpace(s)
	if i := strings.IndexByte(value, ':'); i >= 0 {
		filter = strings.TrimSpace(value[i+1:])
		value = strings.TrimSpace(value[:i])
	}
	return value, filter
}

func usage(fs *flag.FlagSet) func() {
	return func() {
		fmt.Fprintf(fs.Output(), `netblocks %s - country netblock and ASN lookup

Usage:
  netblocks [options]

Cache modes:
  -i, --init-rir              download and import RIR delegation data
  -d, --reload-rir            import cached RIR delegation data
  -l, --init-lir              download and import RIPE LIR data (large)
  -z, --reload-lir            import cached RIPE LIR data
  -m, --init-maxmind          download and import MaxMind GeoLite2 (license)
  -g, --reload-maxmind        import cached MaxMind data
  -r, --import-maxmind FILE   import a MaxMind zip/directory/CSV
  -o, --download-cc           download the ISO country list
  -j, --init-asn-descriptions download and import ASN descriptions
  -k, --reload-asn-descriptions
  -y, --init-asn-assignments  download and import a BGP snapshot (large)
  -u, --reload-asn-assignments
  -e, --erase-cache           erase the local database cache
      --init                  download and import all datasets

Lookup modes:
  -4, --ipv4 ADDR             look up country for an IPv4 address
  -6, --ipv6 ADDR             look up country for an IPv6 address
  -a, --asn ASN               look up country/description for an ASN
  -t, --code CC[:type]        list allocations for a country
  -n, --name NAME[:type]      list allocations for a country name
  -p, --compare CC            compare data sources (conflicts by default)
      --compare-all           show all overlaps, not just conflicts
  -w, --what-country CC       print the name for a country code
      --lookup-org-by-ip IP   look up ASN and description for an IP
      --lookup-org-by-range   look up announcements inside a range
      --range-start ADDR      range start (with --lookup-org-by-range)
      --range-end ADDR        range end (with --lookup-org-by-range)

Export:
      --export-geoip          export GeoIP-compatible CSV files
      --geoip-v4-file FILE    IPv4 country CSV (default GeoIPCountryWhois.csv)
      --geoip-v6-file FILE    IPv6 country CSV (default v6.csv)
      --geoip-asn-file FILE   IPv4 ASN CSV (default GeoIPASNum.csv)
      --geoip-asn-v6-file FILE IPv6 ASN CSV (default GeoIPASNumv6.csv)

Options:
  -c, --cache-dir DIR         cache directory (default %s)
  -v, --verbose               be verbose
      --user-agent VALUE      User-Agent used when fetching
      --json                  machine-readable output for lookups
      --source LIST           rir,lir,maxmind (default is mode-specific)
      --asn-source LIST       routeviews,routeviews6 (default routeviews)
      --offline               never download
      --retries N             download retry attempts (default 3)
      --timeout DURATION      overall timeout (for example 30m)
      --merge / --no-merge    merge adjacent blocks (default: merge)
      --expand-asns           expand multi-ASN allocations (default: true)
      --no-expand-asns        print only the first ASN of each allocation
      --maxmind-license-key K MaxMind license key
      --list-cc               list known ISO country codes
      --reserved-count        count reserved/available RIR records
      --stats                 show cache row counts
      --version               print version
  -h, --help                  show this help

Examples:
  netblocks -i                         # build the RIR cache
  netblocks -t MM                      # all IPv4, IPv6, and ASN allocations
  netblocks -t MM:ipv4                 # only IPv4 allocations
  netblocks -n "South Korea":ipv6      # by country name
  netblocks -4 8.8.8.8                 # reverse lookup
  netblocks -a AS15169                 # ASN lookup
  netblocks -p MM                      # compare sources
  netblocks --json -4 8.8.8.8          # machine-readable output

`, version, app.DefaultCacheDir())
		fs.PrintDefaults()
	}
}
