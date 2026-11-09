# Roadmap

`netblocks` is feature-complete relative to
[blockfinder](https://github.com/ioerror/blockfinder): every original cache
and lookup mode exists, and the original's dead MaxMind/export paths now work.
This file tracks additive work that is worth doing next. It is written so a
future session can pick up an item without re-deriving the design. Items are
listed in intended implementation order; packaging is deliberately last.

## Completed baseline

- All five RIR delegation files, md5 verification, ARIN extended records.
- RIPE LIR `inetnum`/`inet6num` import, including placeholder filtering.
- MaxMind GeoLite2 Country CSV and legacy GeoIP CSV import.
- RouteViews BGP snapshots, cidr-report ASN descriptions.
- SQLite range store with point, country, overlap, and compare queries.
- Country/name, reverse IP, ASN, org, compare, stats, and export modes.
- Text and JSON output, offline operation, merge/expansion controls.

## Low priority

1. **Checksum hardening** — blocked on upstream availability. As of
   2026-11-09 the RIR and RIPE LIR hosts publish only MD5: the
   `delegated-*-latest.md5` and `ripe.db.*.gz.md5` files return 200, while
   `.sha256`, `.sha256sum`, `.sha256.txt`, and `.sha512` return 404 for
   APNIC, RIPE NCC, ARIN, AFRINIC, and LACNIC. Revisit if a registry starts
   publishing a stronger digest. A local hardening remains possible without
   upstream support: re-verify cached payloads against the cached `.md5`
   before `-d`/`-z` imports.

2. **ASN search**: `--find-asn NAME` over `asn_descriptions`, ideally with
   SQLite FTS5 instead of `LIKE` scans.

3. **Schema migrations**: currently a schema-version mismatch recreates the
   database. Acceptable for a cache, but a migration path would avoid a
   re-import for users with large LIR/BGP caches.

4. **Packaging**: goreleaser, release checksums, shell completions, man page,
   Docker image, version/commit/date via `-ldflags`.

## Known limitations

- MaxMind's licensed download path is covered by synthetic GeoLite2 zips, not
  a live licensed fetch.
- The default RouteViews snapshot contains no IPv6 announcements.
- Integrity verification is MD5-only because upstream hosts publish no
  stronger digest.
- Text comparisons stream in store order and are bounded by `--compare-limit`;
  `--json` still collects every pair for the `comparisons` array.
- MaxMind ASN data is consulted only by `-a` when BGP has no announcement for
  the ASN. Org lookups and ASN exports remain BGP-only.
- `--exclude-reserved` uses stored status and country columns; the IANA
  special-purpose registry is not integrated.
- LIR data is RIPE-only, as in blockfinder.
- `--source` in compare mode selects the baseline; the second side is every
  other assignment source.
