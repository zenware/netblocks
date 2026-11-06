package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// batch runs one lookup per input line, continuing past per-line failures.
// It returns an error naming the failure count after writing all successful
// results so callers can exit non-zero.
func (a *App) batch(ctx context.Context) error {
	var in io.Reader
	if a.opts.Input == "-" {
		in = os.Stdin
	} else {
		f, err := os.Open(a.opts.Input)
		if err != nil {
			return fmt.Errorf("open %s: %w", a.opts.Input, err)
		}
		defer f.Close()
		in = f
	}

	origOut := a.opts.Out
	outJSON := a.opts.JSON || a.opts.JSONL
	var items []json.RawMessage
	if a.opts.JSON {
		items = make([]json.RawMessage, 0)
	}
	failures, total := 0, 0
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		total++
		payload, err := a.batchOne(ctx, line, outJSON)
		if err != nil {
			failures++
			a.warnf("%s: %v", line, err)
			continue
		}
		switch {
		case a.opts.JSON:
			items = append(items, json.RawMessage(payload))
		case a.opts.JSONL:
			fmt.Fprintf(origOut, "%s\n", payload)
		default:
			fmt.Fprintf(origOut, "> %s\n", line)
			fmt.Fprintf(origOut, "%s\n\n", payload)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if a.opts.JSON {
		if err := a.writeJSON(items); err != nil {
			return err
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d of %d inputs failed", failures, total)
	}
	return nil
}

// batchOne runs one lookup with output captured in memory. It swaps the app's
// output state for the duration of the call so existing lookup code is reused
// unchanged.
func (a *App) batchOne(ctx context.Context, line string, outJSON bool) ([]byte, error) {
	var buf bytes.Buffer
	prevJSON, prevOut := a.opts.JSON, a.opts.Out
	a.opts.JSON = outJSON
	a.opts.Out = &buf
	defer func() { a.opts.JSON, a.opts.Out = prevJSON, prevOut }()
	var err error
	switch a.opts.InputType {
	case "ip":
		a.opts.IP = line
		err = a.lookupIP(ctx)
	case "asn":
		a.opts.ASN = line
		err = a.lookupASN(ctx)
	case "country":
		a.opts.Code, a.opts.TypeFilter = splitCountrySpec(line)
		err = a.lookupCode(ctx)
	default:
		err = fmt.Errorf("invalid input type %q", a.opts.InputType)
	}
	return bytes.TrimSpace(buf.Bytes()), err
}

// splitCountrySpec splits a CC[:type] input line.
func splitCountrySpec(s string) (value, filter string) {
	value = strings.TrimSpace(s)
	if i := strings.IndexByte(value, ':'); i >= 0 {
		filter = strings.TrimSpace(value[i+1:])
		value = strings.TrimSpace(value[:i])
	}
	return value, filter
}
