// Package countries provides ISO 3166-1 alpha-2 country codes and names.
//
// The embedded list is the canonical fallback so lookups by name or code work
// offline. A list downloaded with --download-cc is merged on top at runtime.
package countries

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

//go:embed countries.csv
var embeddedCSV []byte

// Country is a single ISO 3166-1 entry.
type Country struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Data is a loaded country table.
type Data struct {
	byCode map[string]string
	byName map[string]string
	names  map[string]string
}

// Load parses the embedded list and, when present, the cached country list at
// cachePath. A missing cache file is not an error.
func Load(cachePath string) (*Data, error) {
	d := &Data{
		byCode: make(map[string]string),
		byName: make(map[string]string),
		names:  make(map[string]string),
	}
	if err := d.mergeCSV(embeddedCSV, ','); err != nil {
		return nil, fmt.Errorf("embedded country list: %w", err)
	}
	if cachePath != "" {
		b, err := os.ReadFile(cachePath)
		if err == nil {
			formatErr := d.mergeCSV(b, ',')
			if formatErr != nil {
				formatErr = d.mergeCSV(b, ';')
			}
			if formatErr != nil {
				return nil, fmt.Errorf("cached country list: %w", formatErr)
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	d.addAliases()
	return d, nil
}

func (d *Data) mergeCSV(b []byte, comma rune) error {
	r := csv.NewReader(strings.NewReader(string(b)))
	r.Comma = comma
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	rows, err := r.ReadAll()
	if err != nil {
		return err
	}
	added := 0
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		name := strings.TrimSpace(row[0])
		code := normalizeCode(row[1])
		if !validCode(code) {
			continue
		}
		if strings.EqualFold(name, "name") || strings.Contains(strings.ToLower(name), "country name") {
			continue
		}
		d.byCode[code] = name
		d.names[normalizeName(name)] = code
		added++
	}
	if added == 0 {
		return fmt.Errorf("no valid country rows")
	}
	return nil
}

func (d *Data) addAliases() {
	for alias, code := range aliasCodes {
		if _, ok := d.byCode[code]; !ok {
			if _, pseudo := pseudoNames[code]; !pseudo {
				continue
			}
		}
		d.names[normalizeName(alias)] = code
	}
}

var pseudoNames = map[string]string{
	"EU": "Europe",
	"AP": "Asia-Pacific",
	"ZZ": "Unknown",
	"XK": "Kosovo",
	"UK": "United Kingdom",
}

var aliasCodes = map[string]string{
	"america":                          "US",
	"britain":                          "GB",
	"burma":                            "MM",
	"cape verde":                       "CV",
	"congo brazzaville":                "CG",
	"congo kinshasa":                   "CD",
	"czech republic":                   "CZ",
	"democratic republic of congo":     "CD",
	"democratic republic of the congo": "CD",
	"dr congo":                         "CD",
	"east timor":                       "TL",
	"europe":                           "EU",
	"great britain":                    "GB",
	"holland":                          "NL",
	"cote d ivoire":                    "CI",
	"ivory coast":                      "CI",
	"kosovo":                           "XK",
	"laos":                             "LA",
	"macau":                            "MO",
	"macedonia":                        "MK",
	"micronesia":                       "FM",
	"north korea":                      "KP",
	"republic of congo":                "CG",
	"republic of ireland":              "IE",
	"russia":                           "RU",
	"south korea":                      "KR",
	"swaziland":                        "SZ",
	"turkey":                           "TR",
	"turkiye":                          "TR",
	"uae":                              "AE",
	"uk":                               "GB",
	"united states of america":         "US",
	"usa":                              "US",
	"vatican":                          "VA",
	"vatican city":                     "VA",
	"vietnam":                          "VN",
	"west bank":                        "PS",
	"bolivia":                          "BO",
	"iran":                             "IR",
	"moldova":                          "MD",
	"palestine":                        "PS",
	"syria":                            "SY",
	"taiwan":                           "TW",
	"tanzania":                         "TZ",
	"venezuela":                        "VE",
	"brunei":                           "BN",
}

// Name returns the canonical name for a country code. RIR pseudo-codes such
// as EU and AP are included even though they are not ISO 3166-1 codes.
func (d *Data) Name(code string) (string, bool) {
	code = normalizeCode(code)
	if name, ok := d.byCode[code]; ok {
		return name, true
	}
	name, ok := pseudoNames[code]
	return name, ok
}

// Code resolves a country name to a code. Exact matches win, then unique name
// prefixes, then unique substrings.
func (d *Data) Code(name string) (string, bool) {
	key := normalizeName(name)
	if key == "" {
		return "", false
	}
	if code, ok := d.names[key]; ok {
		return code, true
	}
	var matches []string
	for candidate, code := range d.names {
		if strings.HasPrefix(candidate, key) {
			matches = appendUnique(matches, code)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	if len(matches) > 1 {
		return "", false
	}
	for candidate, code := range d.names {
		if strings.Contains(candidate, key) {
			matches = appendUnique(matches, code)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

// Find returns all countries whose name contains name.
func (d *Data) Find(name string) []Country {
	key := normalizeName(name)
	if key == "" {
		return nil
	}
	seen := make(map[string]bool)
	var out []Country
	for candidate, code := range d.names {
		if strings.Contains(candidate, key) && !seen[code] {
			seen[code] = true
			out = append(out, Country{Code: code, Name: d.byCode[code]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// List returns all known countries sorted by code.
func (d *Data) List() []Country {
	out := make([]Country, 0, len(d.byCode))
	for code, name := range d.byCode {
		out = append(out, Country{Code: code, Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

func appendUnique(values []string, v string) []string {
	for _, existing := range values {
		if existing == v {
			return values
		}
	}
	return append(values, v)
}

// NormalizeCode canonicalizes a country code from a delegation file, which
// may contain a trailing comment such as "EU # Country is really world wide".
func NormalizeCode(s string) string { return normalizeCode(s) }

func normalizeCode(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "# \t"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if s == "*" {
		return s
	}
	return strings.ToUpper(s)
}

func validCode(s string) bool {
	if len(s) != 2 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}
