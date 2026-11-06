# Roadmap

`netblocks` is feature-complete relative to
[blockfinder](https://github.com/ioerror/blockfinder): every original cache
and lookup mode exists, and the original's dead MaxMind/export paths now work.
This file tracks additive work that is worth doing next. It is written so a
future session can pick up an item without re-deriving the design.

## Completed baseline

- All five RIR delegation files, md5 verification, ARIN extended records.
- RIPE LIR `inetnum`/`inet6num` import, including placeholder filtering.
- MaxMind GeoLite2 Country CSV and legacy GeoIP CSV import.
- RouteViews BGP snapshots, cidr-report ASN descriptions.
- SQLite range store with point, country, overlap, and compare queries.
- Country/name, reverse IP, ASN, org, compare, stats, and export modes.
- Text and JSON output, offline operation, merge/expansion controls.

## High priority

### 1. IPv6 BGP source and IPv6 ASN export

**Current state**

- `internal/parse/bgp.go` already parses IPv6 prefixes.
- `internal/sources/sources.go` has one hard-coded BGP URL,
  `ASNAssignmentURL`, pointing at the IPv4-only
  `oix-full-snapshot-latest.dat.bz2` snapshot.
- Live import produced no IPv6 announcements, so
  `--lookup-org-by-ip` on an IPv6 address returns nothing.
- `--export-geoip` writes ASN data only for IPv4 because `internal/app/app.go`
  calls `export.ASNCSV(..., netutil.IPv4)` once.

**Work**

- Add a list of BGP sources instead of one URL. Evaluate the current
  RouteViews `route-views6` RIB and RIPE RIS. RouteViews text snapshots can
  reuse `parse.BGP`; RIPE RIS `bview` data is MRT/binary and needs a new
  parser or an MRT dependency.
- Verify the chosen IPv6 URL before hard-coding it. Keep the existing IPv4
  URL as the default so current behavior does not change.
- Make source selection explicit (`--asn-source routeviews,routeviews6`, or a
  `sources.BGPFiles` slice). `asn_assignments` is already keyed by
  `(num_type, start_hex, next_start_hex)`, so IPv4 and IPv6 coexist.
- Add `--geoip-asn-v6-file` and call `export.ASNCSV(ctx, st, f, netutil.IPv6)`.
- Make `--stats` show separate IPv4/IPv6 BGP counts, or add per-type counts to
  `Store.Stats`.

**Acceptance**

- A live `--lookup-org-by-ip 2001:4860:4860::8888` returns an ASN and
  description.
- An IPv6 ASN export contains prefixes instead of being empty.
- Existing IPv4 org lookups and exports are unchanged.

### 2. ASN → announced prefixes

**Current state**

- `Store.ASNAnnouncementCount` returns only a count.
- `app.lookupASN` prints `AS announced prefixes: N` but not the prefixes
  themselves.
- `Store.OrgForIP`/`OrgForRange` already expose individual announcements.

**Work**

- Add `Store.AnnouncementsForASN(ctx, asn uint32, numType netutil.NumType)`
  returning `[]ASNAnnouncement`, ordered by start. Reuse the existing
  `asn_assignments` indexes (`idx_asn_assignments_asn`).
- Print the prefixes in `-a` only when `--prefixes` is set; a large ASN can
  have thousands of prefixes and the current one-line summary is intentional.
- Include an `announcements` array in `--json -a` output. Each entry should
  include the CIDR/range and source. Consider a `--limit` for JSON.
- Add tests for an ASN with multiple prefixes and for an ASN absent from BGP
  data.

**Acceptance**

- `netblocks -a AS15169 --prefixes` lists IPv4 prefixes.
- `netblocks --json -a AS15169` contains the same announcements in a
  machine-readable form.
- ASN lookup still works when BGP data has not been imported.

### 3. CI and live-data checks

**Current state**

- All checks are run locally; there is no `.github/` directory.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, and
  `CGO_ENABLED=0 go build .` are the expected commands.

**Work**

- Add `.github/workflows/ci.yml`:
  - Linux and macOS, Go 1.26.x.
  - `go test -count=1 ./...`, `go test -race ./...`, `go vet ./...`.
  - `CGO_ENABLED=0 go build .`.
  - Optional Windows build-only job (`go build ./...`).
- Add a separate scheduled or `workflow_dispatch` live-import job:
  - `-i` (RIR) and `-j` (ASN names) are enough and need no secrets.
  - Keep LIR/BGP out of the default job: they are large and slow.
  - MaxMind requires `MAXMIND_LICENSE_KEY`; guard it with a secret check and
    skip cleanly when absent.
- Cache the Go module cache; do not cache downloaded RIR files between runs.

**Acceptance**

- Pull requests fail on test, vet, race, or build regressions.
- The live job proves the RIR and ASN endpoints and parsers still work.
- No required workflow needs a paid secret.

### 4. Cache provenance and `--info`

**Current state**

- Import timestamps are written with `Store.SetMeta` under:
  `last_rir_import`, `last_lir_import`, `last_asn_description_import`,
  `last_asn_assignment_import`, `last_maxmind_import`.
- There is no way to read them back in the CLI. `Store.GetMeta` was removed
  during dead-code cleanup and needs to be re-added.
- `Store.Stats` gives row counts; `cache.Client.Age` gives file ages.

**Work**

- Re-add `Store.GetMeta(ctx, key)`.
- Extend `--stats` or add `--info` with: schema version, database path and
  size, row counts, import timestamps, cached source files and ages.
- Include the same data in `--json`.
- Warn when the database is older than the cached RIR files, which indicates
  a stale import.

**Acceptance**

- `netblocks --info` explains exactly what data is present and when it was
  imported.
- A fresh cache and a partially populated cache are distinguishable without
  querying.

## Medium priority

### 5. Compare aggregation and streaming

- Default conflict mode can still print hundreds of rows for one RIR block
  when a country has granular LIR assignments (e.g. `-p DE`).
- Group conflicts per first range by `(second source, second country)`, with a
  count and a few example ranges. Keep `--compare-all` for full detail.
- Stream text comparisons instead of buffering all pairs; keep JSON collected.
- Consider a `--compare-limit` for pathological cases.

### 6. MaxMind ASN database

- Add GeoLite2-ASN CSV support as an alternative to the 49 MB BGP snapshot.
- Blocks files contain `network,autonomous_system_number,
  autonomous_system_organization`.
- Decide whether to reuse `asn_assignments` with a new `source_name` or add a
  dedicated table. If reused, ensure `-a` prefers BGP when both exist and
  falls back to MaxMind.
- Add `--maxmind-asn` and import the `GeoLite2-ASN-CSV` edition in the same
  license flow as Country.

### 7. Bulk input and NDJSON

- Accept multiple IPs, CIDRs, ASNs, and country codes from a file or stdin.
- Keep mode semantics explicit: `--ipv4 @file` or a new `--input --type ip`.
- Add `--jsonl`/NDJSON for one result object per line.
- Ensure partial failures return a non-zero exit code but do not discard
  successful results.

### 8. RIR status and special-use filtering

- `assignments.status` and `country_code='*'` are already stored.
- Add status filters (`--status allocated,assigned`) and
  `--exclude-reserved` to country listings.
- Consider integrating the IANA special-purpose registry so private,
  loopback, multicast, and documentation ranges can be excluded from
  country results.

## Low priority

- **Packaging**: goreleaser, release checksums, shell completions, man page,
  Docker image, version/commit/date via `-ldflags`.
- **Checksum hardening**: prefer SHA-256 where a registry publishes it;
  `.md5` remains only for compatibility.
- **ASN search**: `--find-asn NAME` over `asn_descriptions`, ideally with
  SQLite FTS5 instead of `LIKE` scans.
- **Output formats**: CSV/TSV for country listings and reverse lookups.
- **Schema migrations**: currently a schema-version mismatch recreates the
  database. Acceptable for a cache, but a migration path would avoid a
  re-import for users with large LIR/BGP caches.

## Known limitations

- MaxMind's licensed download path is covered by synthetic GeoLite2 zips, not
  a live licensed fetch.
- The default RouteViews snapshot contains no IPv6 announcements.
- `--compare-all` buffers all comparisons in memory; conflict mode is the
  default for that reason.
- LIR data is RIPE-only, as in blockfinder.
- `--source` in compare mode selects the baseline; the second side is every
  other assignment source.

## Suggested order

1. CI (cheap, protects everything after it).
2. `--info` (small and immediately useful).
3. ASN prefixes (small, high user value).
4. IPv6 BGP + IPv6 ASN export (largest functional gap).
5. MaxMind ASN and bulk input (workflow improvements).
