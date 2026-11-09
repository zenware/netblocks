package app

import (
	"encoding/csv"
)

// writeTable writes a header and rows as CSV, or as TSV when --tsv is set.
func (a *App) writeTable(header []string, rows [][]string) error {
	w := csv.NewWriter(a.opts.Out)
	if a.opts.TSV {
		w.Comma = '\t'
	}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
