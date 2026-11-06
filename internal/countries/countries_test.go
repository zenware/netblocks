package countries

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeCode(t *testing.T) {
	tests := map[string]string{
		"EU":                                "EU",
		"EU # Country is really world wide": "EU",
		"DE #AT # IT":                       "DE",
		"FR # GF # GP # MQ # RE":            "FR",
		"SE# RU UA":                         "SE",
		"ro":                                "RO",
		"RO":                                "RO",
		"":                                  "",
		"*":                                 "*",
	}
	for in, want := range tests {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNameAndCode(t *testing.T) {
	d, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		code string
	}{
		{"South Korea", "KR"},
		{"Korea, Republic of", "KR"},
		{"North Korea", "KP"},
		{"Myanmar", "MM"},
		{"USA", "US"},
		{"United States of America", "US"},
		{"Vietnam", "VN"},
		{"Russia", "RU"},
		{"Côte d'Ivoire", "CI"},
		{"Cote d Ivoire", "CI"},
	}
	for _, tc := range tests {
		got, ok := d.Code(tc.name)
		if !ok || got != tc.code {
			t.Errorf("Code(%q) = %q, %v; want %q", tc.name, got, ok, tc.code)
		}
	}
	if _, ok := d.Code("Korea"); ok {
		t.Error("ambiguous bare 'Korea' should not resolve")
	}
	if name, ok := d.Name("MM"); !ok || name != "Myanmar" {
		t.Errorf("Name(MM) = %q, %v", name, ok)
	}
	if name, ok := d.Name("EU"); !ok || name != "Europe" {
		t.Errorf("Name(EU) = %q, %v", name, ok)
	}
}

func TestLoadCachedSemicolonList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "countries.csv")
	content := "Country Name;ISO 3166-1-alpha-2 code\nTestland;TL\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if name, ok := d.Name("TL"); !ok || name != "Testland" {
		t.Fatalf("cached override failed: %q, %v", name, ok)
	}
	if _, ok := d.Name("US"); !ok {
		t.Fatal("embedded names should survive a cache merge")
	}
}
