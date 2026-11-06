# netblocks

`netblocks` returns the IPv4, IPv6, and ASN allocations registered to a
country, and performs reverse IP/ASN lookups. It is a Go implementation of
[blockfinder](https://github.com/ioerror/blockfinder) with the same cache and
lookup modes, plus a few things the original could not do:

- downloads all five RIR delegation files (ARIN, RIPE NCC, AFRINIC, APNIC,
  LACNIC) over HTTPS, verifies each `.md5` checksum when published, and
  understands ARIN's extended status/opaque-id columns;
- imports RIPE LIR `inetnum`/`inet6num` data, MaxMind GeoLite2 Country and ASN
  CSV, and RouteViews BGP snapshots;
- verifies RIR downloads against each registry's `.md5` checksum;
- uses a single pure-Go binary with SQLite built in — no Python, no CGO;
- works offline after the first import;
- emits JSON for every lookup, plus NDJSON for batch runs;
- runs batch lookups from a file or stdin (`--input`/`--input-type`);
- merges adjacent/overlapping blocks by default (`--no-merge` reproduces the
  original per-record output);
- expands multi-ASN allocations by default (`--no-expand-asns` reproduces the
  original first-ASN-only output);
- exports real GeoIP-compatible IPv4/IPv6/ASN CSV files. The original's export
  mode exited with "Export needs to be refactored."

## Build

```sh
go build -o netblocks .
```

Requires Go 1.26 or newer. The SQLite driver is pure Go, so `CGO_ENABLED=0`
works and cross-compilation is straightforward.

## Quick start

```sh
# Download and import RIR delegation data (~1 minute, ~30 MB)
./netblocks -i

# All IPv4, IPv6, and ASN allocations for Myanmar
./netblocks -t MM

# Only IPv4 allocations
./netblocks -t MM:ipv4

# Look up a country name instead of a code
./netblocks -n "South Korea":ipv6

# Reverse lookups
./netblocks -4 8.8.8.8
./netblocks -6 2001:4860:4860::8888
./netblocks -a AS15169

# Compare overlapping assignments across data sources
./netblocks -p MM

# Only assigned IPv4 allocations
./netblocks -t MM:ipv4 --status assigned

# Batch lookups, one value per line
./netblocks --input ips.txt --input-type ip
./netblocks --input ips.txt --input-type ip --jsonl

# Machine-readable output
./netblocks --json -4 8.8.8.8
```

The default cache directory is `~/.netblocks`. Override it with `-c DIR`,
`--cache-dir DIR`, or `$NETBLOCKS_CACHE`.

## Cache modes

Pick at most one mode. Most modes download the source first and then import it;
the matching `reload` mode imports an already-downloaded file.

| Mode | Long form | What it does |
|---|---|---|
| `-i` | `--init-rir` | Download all five RIR delegation files, verify `.md5`, import them |
| `-d` | `--reload-rir` | Import already-downloaded RIR files |
| `-l` | `--init-lir` | Download RIPE `inetnum`/`inet6num` and import them (large) |
| `-z` | `--reload-lir` | Import cached RIPE LIR files |
| `-m` | `--init-maxmind` | Download and import MaxMind GeoLite2 Country CSV (license required) |
| `-g` | `--reload-maxmind` | Import cached MaxMind data |
| `-r FILE` | `--import-maxmind FILE` | Import a GeoLite2 zip, a directory of blocks/locations CSVs, or a legacy GeoIP CSV |
| `-o` | `--download-cc` | Download the ISO 3166-1 country list (an embedded list is always available) |
| `-j` | `--init-asn-descriptions` | Download and import ASN descriptions from cidr-report.org |
| `-k` | `--reload-asn-descriptions` | Import cached ASN descriptions |
| `-y` | `--init-asn-assignments` | Download and import BGP snapshots (large; IPv4 by default) |
| `-u` | `--reload-asn-assignments` | Import cached BGP snapshots |
| `-e` | `--erase-cache` | Delete the local SQLite database |
| | `--init` | Download and import every available dataset |

Notes:

- `-i` is enough for country lookups. LIR, MaxMind, and BGP data are needed for
  richer reverse lookups and ASN organization names.
- MaxMind GeoLite2 requires a free license key. Set
  `MAXMIND_LICENSE_KEY` or pass `--maxmind-license-key`. Alternatively,
  download the CSV zip yourself and use `-r`.
- `--maxmind-asn` switches `-m`, `-g`, and `-r` to the GeoLite2-ASN edition
  instead of the Country edition. ASN data is kept separately from BGP
  announcements and is used by `-a` only when BGP has nothing for that ASN.
- `--offline` makes every download fail if the file is not already cached,
  which is useful for reproducible queries and air-gapped systems.
- Downloads are atomic (temporary file + rename) and retried on transient
  errors.

## Lookup modes

| Mode | Long form | What it does |
|---|---|---|
| `-4 ADDR` | `--ipv4 ADDR` | Reverse lookup an IPv4 address |
| `-6 ADDR` | `--ipv6 ADDR` | Reverse lookup an IPv6 address |
| `-a ASN` | `--asn ASN` | Country and description for an ASN (`AS15169` or `15169`) |
| `-t CC[:type]` | `--code CC[:type]` | List allocations for a country code |
| `-n NAME[:type]` | `--name NAME[:type]` | List allocations for a country name |
| `-p CC` | `--compare CC` | Compare assignments across sources; conflicts only by default |
| `-w CC` | `--what-country CC` | Print the country name for a code |
| | `--lookup-org-by-ip IP` | ASN and organization for an IP |
| | `--lookup-org-by-range` | Announcements contained in `--range-start`/`--range-end` |

`-a` prints a one-line summary by design; a large ASN can originate thousands
of prefixes. Add `--prefixes` to list the announcements per family, and
`--prefix-limit N` to cap the rows listed per family (`0`, the default, means
all). `--json -a` always includes the full `announcements` array:

```sh
./netblocks -a AS15169 --prefixes
./netblocks -a AS15169 --prefixes --prefix-limit 5
./netblocks --json -a AS15169
```

`type` is `ipv4`, `ipv6`, or `asn`. Without it, all three are listed.

`-t` and `-n` print one CIDR or ASN per line, which is convenient for shell
pipelines:

```sh
./netblocks -t MM:ipv4
./netblocks -t MM:asn
```

`--status` keeps only the named RIR statuses and `--exclude-reserved` drops
`reserved`/`available` records and records with no country code (`''` or
`*`). Both apply to `-t` and `-n` only:

```sh
./netblocks -t MM:ipv4 --status assigned
./netblocks -t MM:ipv4 --exclude-reserved
```

`--input FILE --input-type ip|asn|country` runs one lookup per line (`-` reads
stdin). Blank lines and lines starting with `#` are ignored, per-line failures
are reported on stderr, and the process exits non-zero after writing every
successful result. Use `--json` for one array or `--jsonl` for one object per
line:

```sh
./netblocks --input ips.txt --input-type ip
./netblocks --input ips.txt --input-type ip --json
./netblocks --input ips.txt --input-type ip --jsonl
```

Reverse lookups consult whatever sources are cached. Use
`--source rir,lir,maxmind` to restrict them.

`-p CC` uses RIR assignments as the baseline and compares them with LIR and
MaxMind assignments. It reports country-code conflicts only, grouped per
baseline range by second source and country, with a count and up to three
example ranges, which keeps the output useful even when a country has
hundreds of thousands of LIR customer assignments. `--compare-limit N` caps
the groups shown per baseline range. Use `--compare-all` to print every
overlap (same-country results included), and `--source` to choose the baseline
sources:

```sh
./netblocks -p IT
./netblocks -p IT --compare-limit 5
./netblocks -p IT --compare-all
./netblocks -p IT --source lir --compare-all
```

`--json -p` always contains one object per overlapping pair, unaffected by
grouping or `--compare-limit`.

### Merging and ASN expansion

By default, adjacent or overlapping ranges with the same country are merged
before CIDR summarization. RIR delegation records that are adjacent therefore
appear as one smaller set of prefixes:

```sh
$ ./netblocks -t JP:ipv6
2001:200::/32

$ ./netblocks -t JP:ipv6 --no-merge
2001:200::/35
2001:200:2000::/35
2001:200:4000::/34
2001:200:8000::/33
```

Use `--no-merge` when you need the original delegation boundaries.

ASN records with a count greater than one are expanded to every ASN by
default. `--no-expand-asns` prints only the first ASN of each record, matching
blockfinder.

## JSON output

`--json` applies to lookup, compare, stats, info, country-list, and
reserved-count modes. `--jsonl` applies to batch runs and writes one JSON
object per input line. Example:

```sh
$ ./netblocks --json -t MM
{"query":"country","country_code":"MM","country_name":"Myanmar","ipv4":["203.81.64.0/19","203.81.160.0/20"]}
```

Reverse IP lookups include each source's result and any BGP announcement:

```json
{
  "query": "ip",
  "ip": "8.8.8.8",
  "family": "ipv4",
  "sources": [
    {"source": "rir", "country_code": "US", "country_name": "United States", "network": "8.8.8.0/24"}
  ],
  "asn": [
    {"asn": 15169, "description": "GOOGLE, US", "network": "8.8.8.0/24"}
  ]
}
```

## Export

```sh
./netblocks --export-geoip
```

Writes:

- `GeoIPCountryWhois.csv` — IPv4 country ranges
- `v6.csv` — IPv6 country ranges
- `GeoIPASNum.csv` — IPv4 ASN ranges (from BGP snapshots)
- `GeoIPASNumv6.csv` — IPv6 ASN ranges (from BGP snapshots)

Override the names with `--geoip-v4-file`, `--geoip-v6-file`,
`--geoip-asn-file`, and `--geoip-asn-v6-file`. Country exports default to RIR
data; use `--source` to export LIR or MaxMind data instead. Rows are
adjacent-merged per country/ASN and written in the standard six/five-column
MaxMind format. Empty datasets produce a warning and no file.

## Options

```
-c, --cache-dir DIR          cache directory (default ~/.netblocks)
-v, --verbose                print download and parse progress
    --user-agent VALUE       User-Agent used when fetching
    --json                   machine-readable output
    --jsonl                  one JSON object per --input line
    --input FILE             batch lookup values, one per line (- = stdin)
    --input-type TYPE        batch input type: ip, asn, or country
    --source LIST            rir,lir,maxmind (or all)
    --asn-source LIST        routeviews,routeviews6 (or all; default routeviews)
    --status LIST            keep these RIR statuses with -t/-n
    --exclude-reserved       drop reserved/available records with -t/-n
    --compare-all            show all overlaps in -p, not just conflicts
    --compare-limit N        max conflict groups per baseline range in -p
    --offline                never download
    --retries N              download retry attempts (default 3)
    --timeout DURATION       overall timeout (default none)
    --merge / --no-merge     merge adjacent blocks (default merge)
    --expand-asns            expand multi-ASN allocations (default true)
    --no-expand-asns         print only the first ASN of each allocation
    --maxmind-license-key K  MaxMind license key
    --list-cc                list known ISO country codes
    --reserved-count         count reserved/available RIR records
    --stats                  show cache row counts
    --info                   show cache provenance, sizes, and file ages
    --version                print version
```

`--info` reports the schema version, absolute database path and size, row
counts, import timestamps, and every cached source file with its age. It also
warns when a cached RIR file is newer than the recorded RIR import, which
usually means `-d` is due. `--json --info` emits the same data as one object
(`schema_version`, `database_path`, `database_size`, `rows`, `imports`,
`files`, and optional `warnings`).

## Data sources

| Source | URL |
|---|---|
| ARIN | `https://ftp.arin.net/pub/stats/arin/delegated-arin-extended-latest` |
| RIPE NCC | `https://ftp.ripe.net/pub/stats/ripencc/delegated-ripencc-latest` |
| AFRINIC | `https://ftp.afrinic.net/pub/stats/afrinic/delegated-afrinic-latest` |
| APNIC | `https://ftp.apnic.net/stats/apnic/delegated-apnic-latest` |
| LACNIC | `https://ftp.lacnic.net/pub/stats/lacnic/delegated-lacnic-latest` |
| RIPE LIR | `https://ftp.ripe.net/ripe/dbase/split/ripe.db.inetnum.gz`, `.../ripe.db.inet6num.gz` |
| ASN names | `https://www.cidr-report.org/as2.0/autnums.html` |
| BGP RIB | `https://archive.routeviews.org/oix-route-views/oix-full-snapshot-latest.dat.bz2` (IPv4 text), `https://archive.routeviews.org/route-views6/bgpdata/YYYY.MM/RIBS/rib.YYYYMMDD.HHMM.bz2` (IPv6 MRT) |
| Country list | `https://raw.githubusercontent.com/datasets/country-list/master/data.csv` |
| MaxMind | `https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-Country-CSV&license_key=...` (and `GeoLite2-ASN-CSV` with `--maxmind-asn`) |

RIR downloads are verified against the corresponding `.md5` file when the
registry publishes one. The country list is embedded in the binary, so
`-w`, `-n`, and exports work without a network connection or `-o`.

RIPE's `IANA-BLK` and `NON-RIPE-NCC-MANAGED-ADDRESS-BLOCK` placeholder
objects are skipped: they cover the whole or unmanaged address space and would
otherwise label unrelated addresses with `EU`.

## Compatibility with blockfinder

The command-line surface is intentionally compatible: every original cache
and lookup mode has a short flag, and the long names are available too. The
original `-x/--hack-the-internet` easter egg is preserved.

Differences:

- The cache is `~/.netblocks/netblocks.db` rather than
  `~/.blockfinder/sqlitedb`. Use `-c` to point at a different directory.
- `-i` uses HTTPS URLs, verifies `.md5` checksums, and keeps reserved and
  available ranges (normalized to country `*`) instead of dropping them.
- `-m` works when a MaxMind license key is available; it no longer prints a
  "no longer freely available" message and exits.
- `--export-geoip` actually writes the files.
- Default output merges adjacent blocks; use `--no-merge` for the original
  behavior.
- Country names use the ISO 3166-1 common list, with aliases such as
  "South Korea", "USA", "Russia", "Vietnam", and "Ivory Coast". RIR
  pseudo-codes such as `EU` and `AP` are understood.
- Errors go to stderr and return a non-zero exit status.

## How it works

The cache is a SQLite database with four tables:

- `assignments` — country assignments from RIR, LIR, and MaxMind data.
- `asn_assignments` — BGP prefix-to-origin-ASN announcements.
- `maxmind_asn_assignments` — MaxMind GeoLite2-ASN prefix-to-origin-ASN rows.
- `asn_descriptions` — ASN-to-organization names.

Ranges are stored as fixed-width hexadecimal keys, `[start_hex,
next_start_hex)`. Fixed width keeps SQLite's text indexes in numeric order and
allows IPv6 values larger than a signed 64-bit integer. The extra nibble of
width makes `end+1` representable even for `255.255.255.255/32` and
`ffff:...:ffff/128`.

All bulk imports run in a single transaction and rebuild indexes afterward, so
an interrupted import leaves the previous cache intact.

## Roadmap

Post-parity work — IPv6 BGP data, ASN prefix listings, CI, cache provenance,
MaxMind ASN, and packaging — is tracked with implementation notes and
acceptance criteria in [ROADMAP.md](ROADMAP.md).

## Development

```sh
go test ./...
go vet ./...
```

Parity fixtures from the original blockfinder test suite are in `testdata/`.
The tests exercise the RIR, LIR, BGP, ASN-description, and MaxMind parsers,
the SQLite range queries, app-level lookups, JSON output, and exports.

## License

MIT. See `LICENSE`.
