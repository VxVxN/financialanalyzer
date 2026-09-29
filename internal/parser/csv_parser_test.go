package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseQuarter(t *testing.T) {
	tests := []struct {
		in      string
		year    int
		quarter string
		wantErr bool
	}{
		{"2023-Q1", 2023, "Q1", false},
		{"2024-Q4", 2024, "Q4", false},
		{"LTM", 0, "", true},
		{"abcd-Q1", 0, "", true},
		{"", 0, "", true},
	}
	for _, tt := range tests {
		y, q, err := ParseQuarter(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseQuarter(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseQuarter(%q): unexpected error %v", tt.in, err)
			continue
		}
		if y != tt.year || q != tt.quarter {
			t.Errorf("ParseQuarter(%q) = (%d,%q), want (%d,%q)", tt.in, y, q, tt.year, tt.quarter)
		}
	}
}

func TestParseValue(t *testing.T) {
	p := NewCSVParser("")
	tests := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{"1 234,56", 1234.56, false},
		{"12,5%", 12.5, false},
		{`"100.00"`, 100.0, false},
		{"-", 0, true},
		{"0.00", 0, true},
		{"", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := p.parseValue(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseValue(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseValue(%q): unexpected error %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseValue(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestExtractCompanyNameAndCategory(t *testing.T) {
	tests := []struct {
		path     string
		company  string
		category string
	}{
		{"/data/SBER_banks.csv", "SBER", "banks"},
		{"LKOH_oil.csv", "LKOH", "oil"},
		{"noseparator.csv", "noseparator", ""},
	}
	for _, tt := range tests {
		p := NewCSVParser(tt.path)
		company, category := p.extractCompanyNameAndCategory()
		if company != tt.company || category != tt.category {
			t.Errorf("extract(%q) = (%q,%q), want (%q,%q)",
				tt.path, company, category, tt.company, tt.category)
		}
	}
}

func TestParseFullFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "SBER_banks.csv")
	content := "Метрика;2023-Q1;2023-Q2;LTM\n" +
		"Капитализация;1 000;2 000;9 999\n" +
		"ROE;15,5;16,0;0\n" +
		"P/E;5,5;6,0;0\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := NewCSVParser(file).Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("no data parsed")
	}

	// The LTM column must be skipped: every record must be Q1 or Q2 of 2023.
	for _, d := range data {
		if d.Company != "SBER" || d.Category != "banks" {
			t.Errorf("unexpected company/category: %+v", d)
		}
		if d.Year != 2023 || (d.Quarter != "Q1" && d.Quarter != "Q2") {
			t.Errorf("unexpected period (LTM leaked?): %+v", d)
		}
	}
}
