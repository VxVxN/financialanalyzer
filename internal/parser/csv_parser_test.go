package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/models"
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
		if d.Source != models.SourceCSV {
			t.Errorf("source = %q, want %q", d.Source, models.SourceCSV)
		}
		if d.Company != "SBER" || d.Category != "banks" {
			t.Errorf("unexpected company/category: %+v", d)
		}
		if d.Year != 2023 || (d.Quarter != "Q1" && d.Quarter != "Q2") {
			t.Errorf("unexpected period (LTM leaked?): %+v", d)
		}
	}
}

func TestParseTreatsZeroAsNoData(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "ZERO_test.csv")
	// Every spelling of zero is an aggregator placeholder; only Q4 has data.
	content := "Метрика;2023-Q1;2023-Q2;2023-Q3;2023-Q4\n" +
		"Долг;0;0.00;0,0;5\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := NewCSVParser(file).Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("rows = %d, want 1 (zeros skipped): %+v", len(data), data)
	}
	if data[0].Quarter != "Q4" || data[0].Debt == nil || *data[0].Debt != 5 {
		t.Errorf("row = %+v, want Q4 with debt 5", data[0])
	}
}

func TestParseDividendsRow(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "DIV_test.csv")
	// Only the total "Дивиденды" row is read, and only its Q4 columns; zero is
	// a real "no payout" there. Per-share, payout-ratio and yield rows from
	// aggregator exports are ignored.
	content := "Метрика;2023-Q3;2023-Q4;2024-Q4;2025-Q4\n" +
		"Дивиденды, млрд руб;0;50,5;0;-\n" +
		"Дивиденд, руб/акцию;0;33,3;0;0\n" +
		"Дивиденды/прибыль, %;0;50;0;0\n" +
		"Дивиденды на акцию, руб;0;33,3;0;0\n" +
		"Див доход, ао, %;0;11;0;0\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := NewCSVParser(file).Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(data) != 2 {
		t.Fatalf("rows = %d, want 2 (2023 and 2024): %+v", len(data), data)
	}
	got := map[int]float64{}
	for _, d := range data {
		if d.Dividends == nil {
			t.Fatalf("row %d-%s has no dividends: %+v", d.Year, d.Quarter, d)
		}
		got[d.Year] = *d.Dividends
	}
	if got[2023] != 50.5 || got[2024] != 0 {
		t.Errorf("dividends = %v, want 2023:50.5 2024:0", got)
	}
}

func TestParseCashFlowRows(t *testing.T) {
	file := filepath.Join(t.TempDir(), "CF_test.csv")
	// Amount rows are read; ratio rows that share a prefix ("Долг/EBITDA",
	// "CAPEX/Выручка, %") are not, so they never overwrite the amounts.
	content := "Метрика;2024-Q4\n" +
		"Долг, млрд руб;500\n" +
		"Долг/EBITDA;1,2\n" +
		"Операционная прибыль, млрд руб;120\n" +
		"Наличность, млрд руб;80\n" +
		"Операционный денежный поток, млрд руб;150\n" +
		"CAPEX, млрд руб;-60\n" +
		"CAPEX/Выручка, %;12\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := NewCSVParser(file).Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := map[string][]float64{}
	add := func(name string, v *float64) {
		if v != nil {
			got[name] = append(got[name], *v)
		}
	}
	for _, d := range data {
		add("debt", d.Debt)
		add("operating_profit", d.OperatingProfit)
		add("cash", d.Cash)
		add("operating_cash_flow", d.OperatingCashFlow)
		add("capex", d.Capex)
	}
	want := map[string]float64{"debt": 500, "operating_profit": 120, "cash": 80, "operating_cash_flow": 150, "capex": 60}
	for name, w := range want {
		if len(got[name]) != 1 || got[name][0] != w {
			t.Errorf("%s = %v, want exactly [%v]", name, got[name], w)
		}
	}
	if len(data) != len(want) {
		t.Errorf("rows = %d, want %d (ratio rows skipped)", len(data), len(want))
	}
}
