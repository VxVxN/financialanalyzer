package analytics

import (
	"reflect"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

var f = models.Float

func withoutRevenue(q models.QuarterData) models.QuarterData { q.Revenue = nil; return q }
func withoutCap(q models.QuarterData) models.QuarterData     { q.Capitalization = nil; return q }

func TestCheckRow(t *testing.T) {
	row := func(capz, rev, np, pe, roe float64) models.QuarterData {
		return models.QuarterData{Year: 2025, Quarter: "Q4", Company: "X",
			Capitalization: f(capz), Revenue: f(rev), NetProfit: f(np), PE: f(pe), ROE: f(roe)}
	}
	tests := []struct {
		name string
		row  models.QuarterData
		want [][]string // Metrics of each expected anomaly, in order
	}{
		{"healthy operating company", row(1000, 500, 50, 20, 15), nil},
		{"empty row", models.QuarterData{Year: 2025, Quarter: "Q4"}, nil},
		{"loss-making: negative P/E is not an anomaly", row(1000, 500, -10, -100, -5), nil},
		{"P/E above 200", row(1000, 500, 4, 250, 1), [][]string{{"pe"}}},
		{"P/E exactly 200 is fine", row(1000, 500, 5, 200, 1), nil},
		{"ROE above 100%", row(1000, 500, 50, 20, 150), [][]string{{"roe"}}},
		{"ROE below -100%", row(1000, 500, -50, 0, -120), [][]string{{"roe"}}},
		// МКПАО «Озон» 2025: revenue 0.19 bln, profit 30.3 bln (dividends), cap ~900.
		{"holding (Ozon-like)", row(900, 0.19, 30.3, 29.7, 19.8), [][]string{
			{"revenue", "capitalization"},
			{"revenue", "net_profit", "pe", "roe"},
		}},
		// ПАО «КЦ ИКС 5» 2025: revenue 85.7, profit 124.5 — only the profit>revenue rule fires.
		{"holding (X5-like)", row(700, 85.7, 124.5, 5.6, 29.2), [][]string{{"revenue", "net_profit", "pe", "roe"}}},
		{"bank: revenue not reported", withoutRevenue(row(7000, 0, 1500, 4.7, 24)), nil},
		// A bank quarter with a big provision release out-earns the NII + fee
		// revenue proxy; that is not the holding signature.
		{"bank: profit above revenue proxy", withSource(row(7000, 300, 400, 4.7, 24), models.SourceCBR102), nil},
		{"non-bank: profit above revenue still flagged", withSource(row(7000, 300, 400, 4.7, 24), models.SourceCSV),
			[][]string{{"revenue", "net_profit", "pe", "roe"}}},
		{"no cap: revenue/cap rule skipped", withoutCap(row(0, 0.1, 0.05, 0, 5)), nil},
		// A reported zero revenue is a real figure: a pure holding with no sales.
		{"holding with zero revenue", row(900, 0, 30, 30, 20), [][]string{
			{"revenue", "capitalization"},
			{"revenue", "net_profit", "pe", "roe"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckRow(tt.row)
			var metrics [][]string
			for _, a := range got {
				metrics = append(metrics, a.Metrics)
				if a.Label != "2025-Q4" || a.Message == "" {
					t.Errorf("anomaly %+v: want label 2025-Q4 and a message", a)
				}
			}
			if !reflect.DeepEqual(metrics, tt.want) {
				t.Errorf("anomaly metrics = %v, want %v", metrics, tt.want)
			}
		})
	}
}

func TestAnomaliesByLabel(t *testing.T) {
	hist := []models.QuarterData{
		{Year: 2024, Quarter: "Q4", Revenue: f(60), NetProfit: f(95)},            // holding rule
		{Year: 2023, Quarter: "Q2", PE: f(300), Revenue: f(10), NetProfit: f(1)}, // P/E only
	}
	anoms := CheckHistory(hist)
	if len(anoms) != 2 || anoms[0].Label != "2023-Q2" {
		t.Fatalf("CheckHistory = %+v, want 2 anomalies, chronological", anoms)
	}

	pe := AnomaliesByLabel(anoms, "pe", PeriodQuarter)
	if len(pe["2023-Q2"]) != 1 || len(pe["2024-Q4"]) != 1 {
		t.Errorf("pe quarterly = %v, want one on each of 2023-Q2, 2024-Q4", pe)
	}
	// net_margin is derived from revenue and net_profit -> only the holding rule.
	nm := AnomaliesByLabel(anoms, "net_margin", PeriodAnnual)
	if len(nm) != 1 || len(nm["2024"]) != 1 {
		t.Errorf("net_margin annual = %v, want only 2024", nm)
	}
	if got := AnomaliesByLabel(anoms, "debt", PeriodQuarter); len(got) != 0 {
		t.Errorf("debt = %v, want none", got)
	}
}

func TestSources(t *testing.T) {
	hist := []models.QuarterData{
		{Source: models.SourceRSBU}, {Source: ""}, {Source: models.SourceCSV}, {Source: models.SourceRSBU},
	}
	want := []string{models.SourceCSV, models.SourceRSBU}
	if got := Sources(hist); !reflect.DeepEqual(got, want) {
		t.Errorf("Sources = %v, want %v", got, want)
	}
	for _, s := range []string{models.SourceRSBU, models.SourceCBR102, models.SourceCSV, models.SourceSmartLab, ""} {
		if SourceLabel(s) == "" || SourceNote(s) == "" {
			t.Errorf("source %q lacks a label or note", s)
		}
	}
	if IsComparable(models.SourceRSBU) || IsComparable(models.SourceCBR102) ||
		!IsComparable(models.SourceSmartLab) || !IsComparable("") {
		t.Error("IsComparable: RSBU/CBR must be non-comparable; smart-lab and legacy (\"\") comparable")
	}
	if got := SourceLabel("<img onerror=x>"); got != "other" {
		t.Errorf("SourceLabel(unknown value) = %q, want \"other\"", got)
	}
}

func withSource(q models.QuarterData, source string) models.QuarterData {
	q.Source = source
	return q
}
