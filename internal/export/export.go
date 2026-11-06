// Package export writes GeoIP-compatible CSV datasets from the cache.
package export

import (
	"bufio"
	"context"
	"encoding/csv"
	"io"
	"math/big"

	"github.com/zenware/netblocks/internal/countries"
	"github.com/zenware/netblocks/internal/netutil"
	"github.com/zenware/netblocks/internal/store"
)

// Stats summarizes an export.
type Stats struct {
	Rows int64
}

type countryRow struct {
	start, end *big.Int
	country    string
	numType    netutil.NumType
}

// CountryCSV writes the MaxMind GeoIPCountryWhois-compatible six-column CSV.
func CountryCSV(ctx context.Context, st *store.Store, out io.Writer, numType netutil.NumType, sources []string, countryData *countries.Data) (Stats, error) {
	var stats Stats
	bw := bufio.NewWriterSize(out, 1<<20)
	cw := csv.NewWriter(bw)

	var pending *countryRow
	flush := func() error {
		if pending == nil {
			return nil
		}
		if err := writeCountryRow(cw, pending, countryData); err != nil {
			return err
		}
		stats.Rows++
		pending = nil
		return nil
	}

	err := st.ExportCountryRows(ctx, numType, sources, func(a store.Assignment) error {
		if pending != nil && pending.country == a.CountryCode {
			next := new(big.Int).Add(pending.end, big.NewInt(1))
			if a.Start.Cmp(next) <= 0 {
				if a.End.Cmp(pending.end) > 0 {
					pending.end = new(big.Int).Set(a.End)
				}
				return nil
			}
		}
		if err := flush(); err != nil {
			return err
		}
		pending = &countryRow{start: new(big.Int).Set(a.Start), end: new(big.Int).Set(a.End), country: a.CountryCode, numType: a.NumType}
		return nil
	})
	if err != nil {
		return stats, err
	}
	if err := flush(); err != nil {
		return stats, err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return stats, err
	}
	return stats, bw.Flush()
}

func writeCountryRow(cw *csv.Writer, row *countryRow, countryData *countries.Data) error {
	bits := row.numType.Bits()
	start, err := netutil.IntToAddr(row.start, bits)
	if err != nil {
		return err
	}
	end, err := netutil.IntToAddr(row.end, bits)
	if err != nil {
		return err
	}
	name := ""
	if countryData != nil {
		name, _ = countryData.Name(row.country)
	}
	return cw.Write([]string{
		start.String(),
		end.String(),
		row.start.String(),
		row.end.String(),
		row.country,
		name,
	})
}

type asnRow struct {
	start, end *big.Int
	asn        uint32
	numType    netutil.NumType
}

// ASNCSV writes a MaxMind GeoIPASNum-compatible five-column CSV.
func ASNCSV(ctx context.Context, st *store.Store, out io.Writer, numType netutil.NumType) (Stats, error) {
	var stats Stats
	bw := bufio.NewWriterSize(out, 1<<20)
	cw := csv.NewWriter(bw)

	var pending *asnRow
	flush := func() error {
		if pending == nil {
			return nil
		}
		bits := pending.numType.Bits()
		start, err := netutil.IntToAddr(pending.start, bits)
		if err != nil {
			return err
		}
		end, err := netutil.IntToAddr(pending.end, bits)
		if err != nil {
			return err
		}
		if err := cw.Write([]string{
			start.String(), end.String(), pending.start.String(), pending.end.String(), netutil.FormatASN(pending.asn),
		}); err != nil {
			return err
		}
		stats.Rows++
		pending = nil
		return nil
	}

	err := st.ExportASNRows(ctx, numType, func(a store.ASNAnnouncement) error {
		if pending != nil && pending.asn == a.ASN {
			next := new(big.Int).Add(pending.end, big.NewInt(1))
			if a.Start.Cmp(next) <= 0 {
				if a.End.Cmp(pending.end) > 0 {
					pending.end = new(big.Int).Set(a.End)
				}
				return nil
			}
		}
		if err := flush(); err != nil {
			return err
		}
		pending = &asnRow{start: new(big.Int).Set(a.Start), end: new(big.Int).Set(a.End), asn: a.ASN, numType: a.NumType}
		return nil
	})
	if err != nil {
		return stats, err
	}
	if err := flush(); err != nil {
		return stats, err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return stats, err
	}
	return stats, bw.Flush()
}
