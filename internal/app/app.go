// Package app wires cache management, parsers, the store, and lookups
// together behind the command-line interface.
package app

import (
	"compress/bzip2"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zenware/netblocks/internal/cache"
	"github.com/zenware/netblocks/internal/countries"
	"github.com/zenware/netblocks/internal/export"
	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/parse"
	"github.com/zenware/netblocks/internal/sources"
	"github.com/zenware/netblocks/internal/store"
)

// Options contains every setting the CLI can pass to the application.
type Options struct {
	Mode     string
	CacheDir string

	Out io.Writer
	Err io.Writer

	UserAgent string
	Verbose   bool
	Quiet     bool
	JSON      bool
	Offline   bool
	Retries   int

	Sources    []string
	BGPSources []string
	Merge      bool
	ExpandASNs bool
	CompareAll bool
	TypeFilter string

	IP             string
	ASN            string
	Prefixes       bool
	PrefixLimit    int
	Code           string
	Name           string
	Compare        string
	WhatCountry    string
	LookupOrgIP    string
	RangeStart     string
	RangeEnd       string
	ImportMaxMind  string
	MaxMindLicense string

	GeoIPV4File    string
	GeoIPV6File    string
	GeoIPASNFile   string
	GeoIPASNv6File string
}

// App holds runtime state for one command invocation.
type App struct {
	opts    Options
	cache   *cache.Client
	country *countries.Data
	st      *store.Store
}

// DefaultCacheDir returns $NETBLOCKS_CACHE or ~/.netblocks.
func DefaultCacheDir() string {
	if dir := os.Getenv("NETBLOCKS_CACHE"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".netblocks"
	}
	return filepath.Join(home, ".netblocks")
}

func expandHome(dir string) string {
	if dir == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return dir
	}
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(dir, "~/"))
		}
	}
	return dir
}

// DefaultUserAgent matches blockfinder's default so upstream servers see a
// familiar client.
func DefaultUserAgent() string {
	return "Mozilla/5.0 (Windows NT 6.1; rv:17.0) Gecko/20100101 Firefox/17.0"
}

// Run executes one command.
func Run(ctx context.Context, opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	if opts.CacheDir == "" {
		opts.CacheDir = DefaultCacheDir()
	}
	opts.CacheDir = expandHome(opts.CacheDir)
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultUserAgent()
	}
	a := &App{opts: opts}
	a.cache = cache.New(opts.CacheDir, opts.UserAgent)
	a.cache.Retries = opts.Retries
	a.cache.Offline = opts.Offline
	a.cache.Verbose = opts.Verbose
	a.cache.Quiet = opts.Quiet
	a.cache.Stderr = opts.Err
	defer a.closeStore()

	if opts.Mode == "" {
		return errors.New("no operation selected")
	}
	switch opts.Mode {
	case "erase-cache":
		return a.eraseCache()
	case "download-cc":
		return a.downloadCountryCodes(ctx)
	case "init-rir":
		return a.initRIR(ctx)
	case "reload-rir":
		return a.reloadRIR(ctx)
	case "init-lir":
		return a.initLIR(ctx)
	case "reload-lir":
		return a.reloadLIR(ctx)
	case "init-maxmind":
		return a.initMaxMind(ctx)
	case "reload-maxmind":
		return a.reloadMaxMind(ctx)
	case "import-maxmind":
		return a.importMaxMind(ctx, opts.ImportMaxMind)
	case "init-asn-descriptions":
		return a.initASNDescriptions(ctx)
	case "reload-asn-descriptions":
		return a.reloadASNDescriptions(ctx)
	case "init-asn-assignments":
		return a.initASNAssignments(ctx)
	case "reload-asn-assignments":
		return a.reloadASNAssignments(ctx)
	case "init-all":
		return a.initAll(ctx)
	case "code":
		return a.lookupCode(ctx)
	case "name":
		return a.lookupName(ctx)
	case "ip":
		return a.lookupIP(ctx)
	case "asn":
		return a.lookupASN(ctx)
	case "compare":
		return a.compare(ctx)
	case "what-country":
		return a.whatCountry(ctx)
	case "lookup-org-by-ip":
		return a.lookupOrgByIP(ctx)
	case "lookup-org-by-range":
		return a.lookupOrgByRange(ctx)
	case "export-geoip":
		return a.exportGeoIP(ctx)
	case "list-cc":
		return a.listCountryCodes(ctx)
	case "reserved-count":
		return a.reservedCount(ctx)
	case "stats":
		return a.stats(ctx)
	case "info":
		return a.cacheInfo(ctx)
	default:
		return fmt.Errorf("unknown operation %q", opts.Mode)
	}
}

func (a *App) dbPath() string {
	return filepath.Join(a.opts.CacheDir, "netblocks.db")
}

func (a *App) openStore(ctx context.Context) (*store.Store, error) {
	if a.st != nil {
		return a.st, nil
	}
	st, err := store.Open(a.dbPath())
	var schemaErr *store.SchemaError
	if errors.As(err, &schemaErr) {
		a.warnf("%v; recreating cache database", err)
		if eraseErr := store.Erase(a.dbPath()); eraseErr != nil {
			return nil, eraseErr
		}
		st, err = store.Open(a.dbPath())
	}
	if err != nil {
		return nil, err
	}
	a.st = st
	return st, nil
}

func (a *App) closeStore() {
	if a.st != nil {
		_ = a.st.Close()
		a.st = nil
	}
}

func (a *App) loadCountries() (*countries.Data, error) {
	if a.country != nil {
		return a.country, nil
	}
	d, err := countries.Load(a.cache.Path(sources.CountryCodesFile))
	if err != nil {
		a.warnf("ignoring cached country list: %v", err)
		d, err = countries.Load("")
	}
	if err != nil {
		return nil, err
	}
	a.country = d
	return d, nil
}

func (a *App) printf(format string, args ...any) {
	if !a.opts.Quiet {
		fmt.Fprintf(a.opts.Out, format, args...)
	}
}

func (a *App) logf(format string, args ...any) {
	if a.opts.Verbose {
		fmt.Fprintf(a.opts.Err, format+"\n", args...)
	}
}

func (a *App) warnf(format string, args ...any) {
	fmt.Fprintf(a.opts.Err, "warning: "+format+"\n", args...)
}

func (a *App) writeJSON(v any) error {
	enc := json.NewEncoder(a.opts.Out)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// ParseSources parses and validates a comma-separated source list.
func ParseSources(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(s, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if part == "all" {
			for _, src := range sources.DefaultLookupSources {
				if !seen[src] {
					seen[src] = true
					out = append(out, src)
				}
			}
			continue
		}
		if part == "bgp" {
			part = sources.SourceBGP
		}
		switch part {
		case sources.SourceRIR, sources.SourceLIR, sources.SourceMaxMind, sources.SourceBGP:
		default:
			return nil, fmt.Errorf("unknown source %q (valid: rir, lir, maxmind, bgp_snapshot, all)", part)
		}
		if !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("empty source list")
	}
	return out, nil
}

// ParseBGPSources parses and validates a comma-separated BGP source list.
func ParseBGPSources(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(s, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if part == "all" {
			for _, src := range sources.BGPSources {
				if !seen[src.Name] {
					seen[src.Name] = true
					out = append(out, src.Name)
				}
			}
			continue
		}
		if _, ok := sources.BGPSourceByName(part); !ok {
			return nil, fmt.Errorf("unknown BGP source %q (valid: routeviews, routeviews6, all)", part)
		}
		if !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("empty BGP source list")
	}
	return out, nil
}

func (a *App) bgpSources() ([]sources.BGPSource, error) {
	names := a.opts.BGPSources
	if len(names) == 0 {
		names = []string{sources.BGPNameRouteViews}
	}
	out := make([]sources.BGPSource, 0, len(names))
	for _, name := range names {
		src, ok := sources.BGPSourceByName(name)
		if !ok {
			return nil, fmt.Errorf("unknown BGP source %q", name)
		}
		out = append(out, src)
	}
	return out, nil
}

// downloadBGPSource fetches one BGP snapshot into the cache.
func (a *App) downloadBGPSource(ctx context.Context, src sources.BGPSource) (string, error) {
	if src.URL != "" {
		return a.cache.DownloadTo(ctx, src.URL, src.Filename)
	}
	if src.Name == sources.BGPNameRouteViews6 {
		now := time.Now()
		if a.opts.Offline {
			// Offline mode only checks the stable cache filename.
			return a.cache.DownloadTo(ctx, sources.RouteViews6RIBURL(now), src.Filename)
		}
		var lastErr error
		for i := range 6 {
			u := sources.RouteViews6RIBURL(now.Add(-time.Duration(i) * 2 * time.Hour))
			a.logf("trying %s", u)
			path, err := a.cache.DownloadTo(ctx, u, src.Filename)
			if err == nil {
				return path, nil
			}
			lastErr = err
		}
		return "", fmt.Errorf("%s: %w", src.Name, lastErr)
	}
	return "", fmt.Errorf("BGP source %q has no URL", src.Name)
}

func (a *App) assignmentSources(defaults []string) ([]string, error) {
	srcs := a.opts.Sources
	if len(srcs) == 0 {
		srcs = defaults
	}
	for _, src := range srcs {
		if src == sources.SourceBGP {
			return nil, errors.New("bgp_snapshot is not an assignment source; use rir, lir, or maxmind")
		}
	}
	return srcs, nil
}

func (a *App) warnLookupState(ctx context.Context) {
	st, err := a.openStore(ctx)
	if err != nil {
		return
	}
	if n, err := st.CountAssignments(ctx); err == nil && n == 0 {
		a.warnf("cache has no assignment data; run `netblocks -i` first")
		return
	}
	for _, rir := range sources.RIRFiles {
		if age, ok := a.cache.Age(sources.Filename(rir.URL)); ok && age > 24*time.Hour {
			a.warnf("cached RIR files are older than 24 hours; run `netblocks -i` to update")
			return
		}
	}
}

func (a *App) eraseCache() error {
	a.closeStore()
	if err := store.Erase(a.dbPath()); err != nil {
		return err
	}
	a.printf("Erased cache database %s\n", a.dbPath())
	return nil
}

func (a *App) downloadCountryCodes(ctx context.Context) error {
	path, err := a.cache.DownloadTo(ctx, sources.CountryCodesURL, sources.CountryCodesFile)
	if err != nil {
		return err
	}
	d, err := countries.Load(path)
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("downloaded country list is invalid: %w", err)
	}
	a.printf("Downloaded %d country codes to %s\n", len(d.List()), path)
	return nil
}

func (a *App) initRIR(ctx context.Context) error {
	for _, rir := range sources.RIRFiles {
		a.printf("Downloading %s delegation data...\n", rir.Registry)
		if _, err := a.cache.DownloadAndVerify(ctx, rir.URL); err != nil {
			return fmt.Errorf("%s: %w", rir.Registry, err)
		}
	}
	return a.importRIR(ctx, true)
}

func (a *App) reloadRIR(ctx context.Context) error {
	return a.importRIR(ctx, false)
}

func (a *App) importRIR(ctx context.Context, requireAll bool) error {
	type file struct {
		registry string
		path     string
	}
	var files []file
	var missing []string
	for _, rir := range sources.RIRFiles {
		path := a.cache.Path(sources.Filename(rir.URL))
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, sources.Filename(rir.URL))
			continue
		}
		files = append(files, file{registry: rir.Registry, path: path})
	}
	if len(files) == 0 {
		return fmt.Errorf("no cached RIR files in %s; run `netblocks -i` first", a.opts.CacheDir)
	}
	if len(missing) > 0 {
		if requireAll {
			return fmt.Errorf("missing RIR files: %s", strings.Join(missing, ", "))
		}
		a.warnf("missing RIR files (skipping): %s", strings.Join(missing, ", "))
	}
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	var total parse.Stats
	err = st.BulkAssignments(ctx, sources.SourceRIR, func(w *store.AssignmentWriter) error {
		for _, f := range files {
			fh, err := os.Open(f.path)
			if err != nil {
				return err
			}
			stats, err := parse.RIR(ctx, fh, w.Insert)
			fh.Close()
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(f.path), err)
			}
			a.logf("parsed %s: %d records, %d skipped", filepath.Base(f.path), stats.Records, stats.Skipped)
			total.Records += stats.Records
			total.Skipped += stats.Skipped
			a.printf("Imported %s: %d records\n", f.registry, stats.Records)
		}
		return nil
	})
	if err != nil {
		return err
	}
	_ = st.SetMeta(ctx, "last_rir_import", time.Now().UTC().Format(time.RFC3339))
	a.printf("RIR import complete: %d records (%d skipped)\n", total.Records, total.Skipped)
	return nil
}

func (a *App) initLIR(ctx context.Context) error {
	for _, rawURL := range sources.LIRURLs {
		a.printf("Downloading %s (large file)...\n", sources.Filename(rawURL))
		if _, err := a.cache.Download(ctx, rawURL); err != nil {
			return err
		}
	}
	return a.importLIR(ctx, true)
}

func (a *App) reloadLIR(ctx context.Context) error {
	return a.importLIR(ctx, false)
}

func (a *App) importLIR(ctx context.Context, requireAll bool) error {
	var paths []string
	var missing []string
	for _, rawURL := range sources.LIRURLs {
		path := a.cache.Path(sources.Filename(rawURL))
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, sources.Filename(rawURL))
			continue
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return fmt.Errorf("no cached LIR files in %s; run `netblocks -l` first", a.opts.CacheDir)
	}
	if len(missing) > 0 {
		if requireAll {
			return fmt.Errorf("missing LIR files: %s", strings.Join(missing, ", "))
		}
		a.warnf("missing LIR files (skipping): %s", strings.Join(missing, ", "))
	}
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	var total parse.Stats
	err = st.BulkAssignments(ctx, sources.SourceLIR, func(w *store.AssignmentWriter) error {
		for _, path := range paths {
			fh, err := os.Open(path)
			if err != nil {
				return err
			}
			gz, err := gzip.NewReader(fh)
			if err != nil {
				fh.Close()
				return fmt.Errorf("%s: %w", filepath.Base(path), err)
			}
			stats, err := parse.LIR(ctx, gz, w.Insert)
			gz.Close()
			fh.Close()
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(path), err)
			}
			a.logf("parsed %s: %d records, %d skipped", filepath.Base(path), stats.Records, stats.Skipped)
			total.Records += stats.Records
			total.Skipped += stats.Skipped
			a.printf("Imported %s: %d records\n", filepath.Base(path), stats.Records)
		}
		return nil
	})
	if err != nil {
		return err
	}
	_ = st.SetMeta(ctx, "last_lir_import", time.Now().UTC().Format(time.RFC3339))
	a.printf("LIR import complete: %d records (%d skipped)\n", total.Records, total.Skipped)
	return nil
}

func (a *App) initASNDescriptions(ctx context.Context) error {
	a.printf("Downloading ASN descriptions...\n")
	if _, err := a.cache.Download(ctx, sources.ASNDescriptionURL); err != nil {
		return err
	}
	return a.importASNDescriptions(ctx)
}

func (a *App) reloadASNDescriptions(ctx context.Context) error {
	return a.importASNDescriptions(ctx)
}

func (a *App) importASNDescriptions(ctx context.Context) error {
	path := a.cache.Path(sources.Filename(sources.ASNDescriptionURL))
	fh, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("no cached ASN descriptions; run `netblocks -j` first: %w", err)
	}
	defer fh.Close()
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	var stats parse.Stats
	err = st.BulkASNDescriptions(ctx, func(insert func(asn uint32, source, description string) error) error {
		s, err := parse.ASNDescriptions(ctx, fh, func(asn uint32, description string) error {
			return insert(asn, "cidr_report", description)
		})
		if err != nil {
			return err
		}
		stats = s
		return nil
	})
	if err != nil {
		return err
	}
	_ = st.SetMeta(ctx, "last_asn_description_import", time.Now().UTC().Format(time.RFC3339))
	a.printf("ASN description import complete: %d records\n", stats.Records)
	return nil
}

func (a *App) initASNAssignments(ctx context.Context) error {
	list, err := a.bgpSources()
	if err != nil {
		return err
	}
	for _, src := range list {
		a.printf("Downloading %s...\n", src.Description)
		if _, err := a.downloadBGPSource(ctx, src); err != nil {
			return fmt.Errorf("%s: %w", src.Name, err)
		}
	}
	return a.importASNAssignments(ctx, true)
}

func (a *App) reloadASNAssignments(ctx context.Context) error {
	return a.importASNAssignments(ctx, false)
}

func (a *App) importASNAssignments(ctx context.Context, requireAll bool) error {
	list, err := a.bgpSources()
	if err != nil {
		return err
	}
	type cached struct {
		src  sources.BGPSource
		path string
	}
	var files []cached
	var missing []string
	for _, src := range list {
		path := a.cache.Path(src.Filename)
		if _, err := os.Stat(path); err == nil {
			files = append(files, cached{src: src, path: path})
			continue
		}
		missing = append(missing, src.Filename)
	}
	if len(files) == 0 {
		return fmt.Errorf("no cached BGP snapshots in %s; run `netblocks -y` first", a.opts.CacheDir)
	}
	if len(missing) > 0 {
		if requireAll {
			return fmt.Errorf("missing BGP snapshots: %s", strings.Join(missing, ", "))
		}
		a.warnf("missing BGP snapshots (skipping): %s", strings.Join(missing, ", "))
	}

	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	type imported struct {
		src      sources.BGPSource
		stats    parse.Stats
		inserted int64
	}
	var results []imported
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.src.Name
	}
	err = st.BulkASNAnnouncements(ctx, sources.SourceBGP, names, func(w *store.ASNWriter) error {
		for _, f := range files {
			before := w.Count
			fh, err := os.Open(f.path)
			if err != nil {
				return err
			}
			var stats parse.Stats
			switch f.src.Format {
			case sources.BGPText:
				stats, err = parse.BGP(ctx, bzip2.NewReader(fh), w.Insert)
			case sources.BGPMRT:
				stats, err = parse.MRT(ctx, bzip2.NewReader(fh), w.Insert)
			default:
				err = fmt.Errorf("unsupported BGP format %q", f.src.Format)
			}
			fh.Close()
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(f.path), err)
			}
			results = append(results, imported{src: f.src, stats: stats, inserted: w.Count - before})
		}
		return nil
	})
	if err != nil {
		return err
	}
	_ = st.SetMeta(ctx, "last_asn_assignment_import", time.Now().UTC().Format(time.RFC3339))
	for _, r := range results {
		a.printf("BGP import (%s) complete: %d unique prefixes (%d paths, %d skipped)\n", r.src.Name, r.inserted, r.stats.Records, r.stats.Skipped)
	}
	return nil
}

func (a *App) initMaxMind(ctx context.Context) error {
	license := a.opts.MaxMindLicense
	if license == "" {
		license = os.Getenv("MAXMIND_LICENSE_KEY")
	}
	if license == "" {
		return errors.New("MaxMind GeoLite2 requires a license key; set MAXMIND_LICENSE_KEY, pass --maxmind-license-key, or import a downloaded file with -r FILE")
	}
	a.printf("Downloading MaxMind GeoLite2 Country CSV...\n")
	path, err := a.cache.DownloadTo(ctx, sources.MaxMindURL(license), sources.MaxMindZipName)
	if err != nil {
		return err
	}
	return a.importMaxMindPaths(ctx, []string{path})
}

func (a *App) reloadMaxMind(ctx context.Context) error {
	geoPath := a.cache.Path(sources.MaxMindZipName)
	if _, err := os.Stat(geoPath); err == nil {
		return a.importMaxMindPaths(ctx, []string{geoPath})
	}
	legacy := []string{
		a.cache.Path("GeoIPCountryCSV.zip"),
		a.cache.Path("GeoIPv6.csv.gz"),
	}
	var paths []string
	for _, path := range legacy {
		if _, err := os.Stat(path); err == nil {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("no cached MaxMind data in %s; run `netblocks -m` (license required) or `-r FILE`", a.opts.CacheDir)
	}
	return a.importMaxMindPaths(ctx, paths)
}

func (a *App) importMaxMind(ctx context.Context, path string) error {
	if path == "" {
		return errors.New("--import-maxmind requires a file or directory")
	}
	return a.importMaxMindPaths(ctx, []string{path})
}

func (a *App) importMaxMindPaths(ctx context.Context, paths []string) error {
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	var total parse.Stats
	err = st.BulkAssignments(ctx, sources.SourceMaxMind, func(w *store.AssignmentWriter) error {
		for _, path := range paths {
			stats, err := parse.MaxMind(ctx, path, w.Insert)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			a.logf("parsed %s: %d records, %d skipped", path, stats.Records, stats.Skipped)
			total.Records += stats.Records
			total.Skipped += stats.Skipped
		}
		return nil
	})
	if err != nil {
		return err
	}
	_ = st.SetMeta(ctx, "last_maxmind_import", time.Now().UTC().Format(time.RFC3339))
	a.printf("MaxMind import complete: %d records (%d skipped)\n", total.Records, total.Skipped)
	return nil
}

func (a *App) initAll(ctx context.Context) error {
	type step struct {
		name string
		fn   func(context.Context) error
	}
	steps := []step{
		{"country codes", a.downloadCountryCodes},
		{"RIR delegations", a.initRIR},
		{"LIR delegations", a.initLIR},
		{"ASN descriptions", a.initASNDescriptions},
		{"BGP announcements", a.initASNAssignments},
	}
	if a.opts.MaxMindLicense != "" || os.Getenv("MAXMIND_LICENSE_KEY") != "" {
		steps = append(steps, step{"MaxMind GeoLite2", a.initMaxMind})
	}
	var errs []error
	for i, step := range steps {
		a.printf("\n[%d/%d] %s\n", i+1, len(steps), step.name)
		if err := step.fn(ctx); err != nil {
			a.warnf("%s failed: %v", step.name, err)
			errs = append(errs, fmt.Errorf("%s: %w", step.name, err))
		}
	}
	if len(errs) == 0 {
		a.printf("\nCache initialization complete.\n")
	}
	return errors.Join(errs...)
}

var cacheImportKeys = []string{
	"last_rir_import",
	"last_lir_import",
	"last_asn_description_import",
	"last_asn_assignment_import",
	"last_maxmind_import",
}

type cacheFileInfo struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Modified   string `json:"modified"`
	AgeSeconds int64  `json:"age_seconds"`
}

type cacheInfoReport struct {
	SchemaVersion string            `json:"schema_version"`
	DatabasePath  string            `json:"database_path"`
	DatabaseSize  int64             `json:"database_size"`
	Rows          []store.Stat      `json:"rows"`
	Imports       map[string]string `json:"imports"`
	Files         []cacheFileInfo   `json:"files"`
	Warnings      []string          `json:"warnings,omitempty"`
}

func (a *App) cacheInfo(ctx context.Context) error {
	dbPath, err := filepath.Abs(a.dbPath())
	if err != nil {
		return err
	}
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	version, ok, err := st.GetMeta(ctx, "schema_version")
	if err != nil {
		return err
	}
	if !ok || version == "" {
		version = store.SchemaVersion
	}
	imports := make(map[string]string, len(cacheImportKeys))
	for _, key := range cacheImportKeys {
		value, ok, err := st.GetMeta(ctx, key)
		if err != nil {
			return err
		}
		if ok {
			imports[key] = value
		}
	}
	rows, err := st.Stats(ctx)
	if err != nil {
		return err
	}
	var dbSize int64
	info, err := os.Stat(dbPath)
	switch {
	case err == nil:
		dbSize = info.Size()
	case errors.Is(err, os.ErrNotExist):
	default:
		return err
	}
	files := make([]cacheFileInfo, 0)
	entries, err := os.ReadDir(a.opts.CacheDir)
	switch {
	case err == nil:
		for _, entry := range entries {
			name := entry.Name()
			if name == "netblocks.db" || strings.HasPrefix(name, "netblocks.db-") {
				continue
			}
			entryInfo, err := entry.Info()
			if err != nil {
				return err
			}
			if entryInfo.IsDir() || !entryInfo.Mode().IsRegular() {
				continue
			}
			files = append(files, cacheFileInfo{
				Name:       name,
				Size:       entryInfo.Size(),
				Modified:   entryInfo.ModTime().UTC().Format(time.RFC3339),
				AgeSeconds: int64(time.Since(entryInfo.ModTime()).Seconds()),
			})
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return err
	}
	var warnings []string
	rirValue, rirPresent, err := st.GetMeta(ctx, "last_rir_import")
	if err != nil {
		return err
	}
	var rirTime time.Time
	rirParsed := false
	if rirPresent {
		parsed, err := time.Parse(time.RFC3339, rirValue)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("last_rir_import has invalid timestamp %q", rirValue))
		} else {
			rirTime = parsed
			rirParsed = true
		}
	}
	rirFileExists := false
	for _, src := range sources.RIRFiles {
		name := sources.Filename(src.URL)
		fileInfo, err := os.Stat(a.cache.Path(name))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		rirFileExists = true
		if rirParsed && fileInfo.ModTime().UTC().Truncate(time.Second).After(rirTime) {
			warnings = append(warnings, fmt.Sprintf("cached RIR file %s is newer than the last RIR import (%s); run `netblocks -d`", name, rirValue))
		}
	}
	if rirFileExists && !rirPresent {
		warnings = append(warnings, "cached RIR files exist but no RIR import is recorded; run `netblocks -d`")
	}
	if a.opts.JSON {
		return a.writeJSON(cacheInfoReport{
			SchemaVersion: version,
			DatabasePath:  dbPath,
			DatabaseSize:  dbSize,
			Rows:          rows,
			Imports:       imports,
			Files:         files,
			Warnings:      warnings,
		})
	}
	a.printf("Cache database: %s\n", dbPath)
	a.printf("Schema version: %s\n", version)
	a.printf("Database size: %d bytes\n", dbSize)
	a.printf("\nRows:\n")
	a.printf("%-20s %-14s %10s\n", "SOURCE", "TYPE", "ROWS")
	for _, row := range rows {
		a.printf("%-20s %-14s %10d\n", row.SourceType, row.NumType, row.Count)
	}
	a.printf("\nImports:\n")
	for _, key := range cacheImportKeys {
		value, ok := imports[key]
		if !ok {
			value = "(none)"
		}
		a.printf("  %-30s  %s\n", key, value)
	}
	a.printf("\nCached files:\n")
	if len(files) == 0 {
		a.printf("  (none)\n")
	} else {
		for _, f := range files {
			age := (time.Duration(f.AgeSeconds) * time.Second).Round(time.Second)
			a.printf("  %-32s  %d bytes  %s (%s ago)\n", f.Name, f.Size, f.Modified, age)
		}
	}
	if len(warnings) > 0 {
		a.printf("\nWarnings:\n")
		for _, warning := range warnings {
			a.warnf("%s", warning)
		}
	}
	return nil
}

func (a *App) stats(ctx context.Context) error {
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	rows, err := st.Stats(ctx)
	if err != nil {
		return err
	}
	if a.opts.JSON {
		return a.writeJSON(map[string]any{"rows": rows})
	}
	a.printf("%-20s %-14s %10s\n", "SOURCE", "TYPE", "ROWS")
	for _, row := range rows {
		a.printf("%-20s %-14s %10d\n", row.SourceType, row.NumType, row.Count)
	}
	return nil
}

func (a *App) reservedCount(ctx context.Context) error {
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	counts, err := st.StatusCounts(ctx)
	if err != nil {
		return err
	}
	reserved, err := st.ReservedCount(ctx)
	if err != nil {
		return err
	}
	if a.opts.JSON {
		return a.writeJSON(map[string]any{"status_counts": counts, "reserved_total": reserved})
	}
	keys := make([]string, 0, len(counts))
	for status := range counts {
		keys = append(keys, status)
	}
	sort.Strings(keys)
	a.printf("RIR status counts:\n")
	for _, status := range keys {
		a.printf("  %-12s %10d\n", status, counts[status])
	}
	a.printf("Reserved/available total: %d\n", reserved)
	return nil
}

func (a *App) listCountryCodes(ctx context.Context) error {
	d, err := a.loadCountries()
	if err != nil {
		return err
	}
	list := d.List()
	if a.opts.JSON {
		return a.writeJSON(list)
	}
	for _, c := range list {
		a.printf("%s\t%s\n", c.Code, c.Name)
	}
	return nil
}

func (a *App) exportGeoIP(ctx context.Context) error {
	st, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	srcs, err := a.assignmentSources([]string{sources.SourceRIR})
	if err != nil {
		return err
	}
	d, err := a.loadCountries()
	if err != nil {
		return err
	}
	v4 := a.opts.GeoIPV4File
	if v4 == "" {
		v4 = "GeoIPCountryWhois.csv"
	}
	v6 := a.opts.GeoIPV6File
	if v6 == "" {
		v6 = "v6.csv"
	}
	asnFile := a.opts.GeoIPASNFile
	if asnFile == "" {
		asnFile = "GeoIPASNum.csv"
	}
	asn6File := a.opts.GeoIPASNv6File
	if asn6File == "" {
		asn6File = "GeoIPASNumv6.csv"
	}
	if err := a.exportFile(v4, func(f *os.File) (export.Stats, error) {
		return export.CountryCSV(ctx, st, f, netutil.IPv4, srcs, d)
	}); err != nil {
		return err
	}
	if err := a.exportFile(v6, func(f *os.File) (export.Stats, error) {
		return export.CountryCSV(ctx, st, f, netutil.IPv6, srcs, d)
	}); err != nil {
		return err
	}
	if err := a.exportFile(asnFile, func(f *os.File) (export.Stats, error) {
		return export.ASNCSV(ctx, st, f, netutil.IPv4)
	}); err != nil {
		return err
	}
	if err := a.exportFile(asn6File, func(f *os.File) (export.Stats, error) {
		return export.ASNCSV(ctx, st, f, netutil.IPv6)
	}); err != nil {
		return err
	}
	return nil
}

func (a *App) exportFile(name string, run func(*os.File) (export.Stats, error)) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	stats, runErr := run(f)
	closeErr := f.Close()
	if runErr != nil {
		_ = os.Remove(name)
		return runErr
	}
	if closeErr != nil {
		_ = os.Remove(name)
		return closeErr
	}
	if stats.Rows == 0 {
		_ = os.Remove(name)
		a.warnf("no rows exported to %s (is the relevant dataset imported?)", name)
		return nil
	}
	a.printf("Exported %d rows to %s\n", stats.Rows, name)
	return nil
}
