package analytics

import (
	"math"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// rsbuCF is an annual RSBU row with the cash-flow lines (billions).
func rsbuCF(year int, cap, revenue, opProfit, debt, cash, ocf, capex float64) models.QuarterData {
	f := models.Float
	return models.QuarterData{Year: year, Quarter: "Q4", Company: "CF", Source: models.SourceRSBU,
		Capitalization: f(cap), Revenue: f(revenue), NetProfit: f(opProfit * 0.8), Debt: f(debt), Cash: f(cash),
		OperatingProfit: f(opProfit), OperatingCashFlow: f(ocf), Capex: f(capex)}
}

func latestValue(t *testing.T, s Series) Point {
	t.Helper()
	p, ok := LatestValid(s)
	if !ok {
		t.Fatalf("series has no value: %+v", s)
	}
	return p
}

func TestCashFlowMetricsAnnualRSBU(t *testing.T) {
	// 2025: cap 1000, revenue 500, EBIT 100, debt 300, cash 50, OCF 150, capex 60.
	h := []models.QuarterData{rsbuCF(2024, 900, 450, 90, 280, 40, 120, 50), rsbuCF(2025, 1000, 500, 100, 300, 50, 150, 60)}
	tests := []struct {
		metric string
		want   float64
	}{
		{"net_debt", 250},         // 300 - 50
		{"ev", 1250},              // 1000 + 250
		{"operating_margin", 20},  // 100 / 500
		{"ev_ebit", 12.5},         // 1250 / 100
		{"fcf", 90},               // 150 - 60
		{"p_fcf", 1000.0 / 90},    // cap / FCF
		{"operating_profit", 100}, // a raw flow
		{"capex", 60},
	}
	for _, period := range []Period{PeriodQuarter, PeriodTTM, PeriodAnnual} {
		for _, tt := range tests {
			p := latestValue(t, SeriesFor(h, tt.metric, period))
			if !approx(p.Value, tt.want, 1e-9) || p.Year != 2025 {
				t.Errorf("%s/%s = %v at %s, want %v at 2025", tt.metric, period, p.Value, p.Label, tt.want)
			}
		}
	}
	for _, m := range []string{"net_debt", "ev", "operating_margin", "ev_ebit", "fcf", "p_fcf", "cash", "operating_profit", "operating_cash_flow", "capex"} {
		if !IsValidMetric(m) {
			t.Errorf("%s is not a valid metric", m)
		}
	}
	if !IsFlow("operating_cash_flow") || IsFlow("cash") || !IsDerived("fcf") || IsDerived("capex") {
		t.Error("flow/derived classification is wrong")
	}
}

// Quarterly CSV figures: flows sum over four quarters, and the valuation
// multiples on a quarter pair the period's cap/EV with TTM flows.
func TestCashFlowMetricsQuarterly(t *testing.T) {
	f := models.Float
	var h []models.QuarterData
	for i, q := range []string{"Q1", "Q2", "Q3", "Q4"} {
		h = append(h, models.QuarterData{Year: 2025, Quarter: q, Company: "Q", Source: models.SourceCSV,
			Revenue: f(100), OperatingProfit: f(10 + float64(i)), OperatingCashFlow: f(20), Capex: f(5)})
	}
	h[3].Capitalization, h[3].Debt, h[3].Cash = f(460), f(100), f(20)

	if p := latestValue(t, SeriesFor(h, "fcf", PeriodTTM)); p.Value != 60 { // 4*20 - 4*5
		t.Errorf("TTM FCF = %v, want 60", p.Value)
	}
	if p := latestValue(t, SeriesFor(h, "fcf", PeriodQuarter)); p.Value != 15 {
		t.Errorf("quarterly FCF = %v, want 15", p.Value)
	}
	// EV 540 over TTM EBIT 10+11+12+13 = 46, not the Q4 quarter's 13.
	if p := latestValue(t, SeriesFor(h, "ev_ebit", PeriodQuarter)); !approx(p.Value, 540.0/46, 1e-9) {
		t.Errorf("EV/EBIT = %v, want %v", p.Value, 540.0/46)
	}
	if p := latestValue(t, SeriesFor(h, "p_fcf", PeriodQuarter)); !approx(p.Value, 460.0/60, 1e-9) {
		t.Errorf("P/FCF = %v, want %v", p.Value, 460.0/60)
	}
}

// A loss or negative FCF gives no multiple; net debt may be negative (net
// cash) and EV still follows; missing cash leaves net debt unknown.
func TestCashFlowMetricsEdgeCases(t *testing.T) {
	h := []models.QuarterData{rsbuCF(2025, 1000, 500, -20, 100, 400, 50, 80)}
	for _, m := range []string{"ev_ebit", "p_fcf"} {
		if _, ok := LatestValid(SeriesFor(h, m, PeriodAnnual)); ok {
			t.Errorf("%s defined for a loss / negative FCF", m)
		}
	}
	if p := latestValue(t, SeriesFor(h, "net_debt", PeriodAnnual)); p.Value != -300 {
		t.Errorf("net debt = %v, want -300 (net cash)", p.Value)
	}
	if p := latestValue(t, SeriesFor(h, "ev", PeriodAnnual)); p.Value != 700 {
		t.Errorf("EV = %v, want 700", p.Value)
	}

	h[0].Cash = nil
	if _, ok := LatestValid(SeriesFor(h, "net_debt", PeriodAnnual)); ok {
		t.Error("net debt defined without cash")
	}
}

func TestSnapshotAndCurrentCashFlow(t *testing.T) {
	h := []models.QuarterData{rsbuCF(2024, 900, 450, 90, 280, 40, 120, 50), rsbuCF(2025, 1000, 500, 100, 300, 50, 150, 60)}
	s := BuildSnapshot(h)
	if s.NetDebt != 250 || s.NetDebtLabel != "2025-Q4" || !approx(s.EVEBIT, 12.5, 1e-9) || s.EVEBITLabel != "2025-Q4" ||
		s.FCF != 90 || !approx(s.PFCF, 1000.0/90, 1e-9) || s.OperatingMargin != 20 {
		t.Errorf("snapshot = net debt %v (%s), EV/EBIT %v (%s), FCF %v, P/FCF %v, op margin %v",
			s.NetDebt, s.NetDebtLabel, s.EVEBIT, s.EVEBITLabel, s.FCF, s.PFCF, s.OperatingMargin)
	}

	quote := models.MarketQuote{Company: "CF", Capitalization: 1200, PriceDate: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
	c := BuildCurrent(h, quote)
	// EV now = 1200 + 250; EBIT 100; FCF 90.
	if !approx(c.EVEBIT, 14.5, 1e-9) || c.EBITLabel != "2025-Q4" || !approx(c.PFCF, 1200.0/90, 1e-9) || c.FCFLabel != "2025-Q4" {
		t.Errorf("current EV/EBIT %v (%s), P/FCF %v (%s)", c.EVEBIT, c.EBITLabel, c.PFCF, c.FCFLabel)
	}

	// Two years later both fundamentals are stale: no multiples.
	quote.PriceDate = time.Date(2027, 9, 29, 0, 0, 0, 0, time.UTC)
	if c := BuildCurrent(h, quote); !math.IsNaN(c.EVEBIT) || !math.IsNaN(c.PFCF) || !c.Stale {
		t.Errorf("stale: EV/EBIT %v, P/FCF %v, stale %v", c.EVEBIT, c.PFCF, c.Stale)
	}

	row := BuildScreenerRow(h, nil, nil)
	if row.EVEBIT == nil || *row.EVEBIT != s.EVEBIT || row.PFCF == nil || row.OperatingMargin == nil {
		t.Errorf("screener row = %+v", row)
	}
}

// Cash above cap + debt makes EV negative; EV/EBIT is then withheld (it would
// sort as the cheapest), while EV itself stays negative on its own chart.
func TestNegativeEV(t *testing.T) {
	h := []models.QuarterData{rsbuCF(2025, 100, 500, 50, 0, 300, 60, 10)}
	if p := latestValue(t, SeriesFor(h, "ev", PeriodAnnual)); p.Value != -200 {
		t.Errorf("EV = %v, want -200", p.Value)
	}
	if _, ok := LatestValid(SeriesFor(h, "ev_ebit", PeriodAnnual)); ok {
		t.Error("EV/EBIT defined for a negative EV")
	}
	quote := models.MarketQuote{Company: "CF", Capitalization: 150, PriceDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	if c := BuildCurrent(h, quote); !math.IsNaN(c.EVEBIT) {
		t.Errorf("current EV/EBIT = %v for a negative EV, want NaN", c.EVEBIT)
	}
	if row := BuildScreenerRow(h, nil, nil); row.EVEBIT != nil {
		t.Errorf("screener EV/EBIT = %v, want null", *row.EVEBIT)
	}
}

// Net debt from CSV years only up to 2023 while profit and EBIT are current:
// P/E is priced and the quote is not stale; EV/EBIT is withheld and its label
// names the old net debt. A loss keeps the EBIT label without a multiple.
func TestCurrentStaleNetDebtDoesNotStaleQuote(t *testing.T) {
	f := models.Float
	h := []models.QuarterData{
		{Year: 2023, Quarter: "Q4", Company: "C", Source: models.SourceCSV, Debt: f(100), Cash: f(20)},
	}
	for _, q := range []string{"Q3", "Q4"} {
		h = append(h, models.QuarterData{Year: 2025, Quarter: q, Company: "C", Source: models.SourceCSV,
			NetProfit: f(10), OperatingProfit: f(15)})
	}
	for _, q := range []string{"Q1", "Q2"} {
		h = append(h, models.QuarterData{Year: 2026, Quarter: q, Company: "C", Source: models.SourceCSV,
			NetProfit: f(10), OperatingProfit: f(15)})
	}
	quote := models.MarketQuote{Company: "C", Capitalization: 400, PriceDate: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
	c := BuildCurrent(h, quote)
	if c.Stale || !approx(c.PE, 10, 1e-9) {
		t.Errorf("stale=%v P/E=%v; want a fresh P/E of 10", c.Stale, c.PE)
	}
	if !math.IsNaN(c.EVEBIT) || c.EBITLabel != "2026-Q2, чистый долг на 2023-Q4 (устарело)" {
		t.Errorf("EV/EBIT = %v (%q)", c.EVEBIT, c.EBITLabel)
	}

	for i := range h[1:] {
		h[i+1].OperatingProfit = f(-5)
	}
	if c := BuildCurrent(h, quote); !math.IsNaN(c.EVEBIT) || c.EBITLabel != "2026-Q2" {
		t.Errorf("loss: EV/EBIT = %v (%q), want NaN with the label kept", c.EVEBIT, c.EBITLabel)
	}
}

// TTM FCF needs capex in all four quarters, and combine never mixes a group
// (CSV) figure with a standalone (RSBU) one.
func TestCashFlowGaps(t *testing.T) {
	f := models.Float
	var h []models.QuarterData
	for _, q := range []string{"Q1", "Q2", "Q3", "Q4"} {
		h = append(h, models.QuarterData{Year: 2025, Quarter: q, Company: "G", Source: models.SourceCSV,
			OperatingCashFlow: f(20), Capex: f(5)})
	}
	h[2].Capex = nil
	if _, ok := LatestValid(SeriesFor(h, "fcf", PeriodTTM)); ok {
		t.Error("TTM FCF defined with a quarter's capex missing")
	}

	// Annual base: debt/cash last reported on a CSV Q3 row, cap on an RSBU Q4
	// row — the year's EV would mix kinds.
	mixed := []models.QuarterData{
		{Year: 2025, Quarter: "Q3", Company: "M", Source: models.SourceCSV, Debt: f(100), Cash: f(10)},
		{Year: 2025, Quarter: "Q4", Company: "M", Source: models.SourceRSBU, Capitalization: f(500), Revenue: f(50)},
	}
	if _, ok := LatestValid(SeriesFor(mixed, "ev", PeriodAnnual)); ok {
		t.Error("annual EV mixes group net debt with a standalone cap")
	}
}

// Debt written by a CSV onto an RSBU row that already holds the parent's cash
// is one row with two kinds. Net debt, EV, EV/EBIT and P/FCF stay empty; FCF
// itself does not use the balance sheet and stays.
func TestMixedColumnSourcesWithholdValuation(t *testing.T) {
	f := models.Float
	h := []models.QuarterData{{
		Year: 2025, Quarter: "Q4", Company: "M", Source: models.SourceRSBU,
		Capitalization: f(500), Revenue: f(200),
		Debt: f(100), DebtSource: models.SourceCSV,
		Cash: f(10), CashSource: models.SourceRSBU,
		OperatingProfit: f(50), OperatingCashFlow: f(40), Capex: f(10),
	}}
	for _, m := range []string{"net_debt", "ev", "ev_ebit", "p_fcf"} {
		for _, period := range []Period{PeriodQuarter, PeriodAnnual} {
			if _, ok := LatestValid(SeriesFor(h, m, period)); ok {
				t.Errorf("%s/%s computed from mixed debt and cash", m, period)
			}
		}
	}
	if p := latestValue(t, SeriesFor(h, "fcf", PeriodAnnual)); p.Value != 30 {
		t.Errorf("FCF = %v, want 30", p.Value)
	}

	// Same pipeline on both columns: net debt is ordinary.
	h[0].DebtSource = models.SourceRSBU
	if p := latestValue(t, SeriesFor(h, "net_debt", PeriodQuarter)); p.Value != 90 {
		t.Errorf("same-source net debt = %v, want 90", p.Value)
	}

	quote := models.MarketQuote{Company: "M", Capitalization: 500, PriceDate: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
	h[0].DebtSource = models.SourceCSV
	cur := BuildCurrent(h, quote)
	if !math.IsNaN(cur.EVEBIT) || !math.IsNaN(cur.PFCF) {
		t.Errorf("current EV/EBIT = %v P/FCF = %v, want both withheld", cur.EVEBIT, cur.PFCF)
	}
}
