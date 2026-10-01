package analytics

import (
	"math"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

func bandYear(year int, pe, cap, equity, dividends *float64) models.QuarterData {
	return models.QuarterData{Year: year, Quarter: "Q4", Company: "R", Category: "retail", Source: models.SourceRSBU,
		PE: pe, Capitalization: cap, Equity: equity, Dividends: dividends}
}

func TestHistoricalBand(t *testing.T) {
	f := models.Float
	h := []models.QuarterData{
		bandYear(2012, f(1), nil, nil, nil), // older than BandYears before 2025
		bandYear(2019, f(4), nil, nil, nil),
		bandYear(2020, f(-3), nil, nil, nil), // a loss: no valuation
		bandYear(2021, f(6), nil, nil, nil),
		bandYear(2022, f(8), nil, nil, nil),
		bandYear(2023, f(10), nil, nil, nil),
		bandYear(2024, f(12), nil, nil, nil),
		bandYear(2025, f(14), nil, nil, nil),
	}
	b, ok := HistoricalBand(h, "pe", 7)
	if !ok {
		t.Fatal("no band")
	}
	if b.N != 6 || b.Min != 4 || b.Max != 14 || b.Median != 9 || b.From != "2019-Q4" || b.To != "2025-Q4" {
		t.Errorf("band = %+v", b)
	}
	// 4 and 6 are below 7: 2 of 6.
	if !approx(b.Percentile, 100.0*2/6, 1e-9) {
		t.Errorf("percentile = %v", b.Percentile)
	}
	// A tie counts half: 10 has 4,6,8 below and itself equal -> 3.5 of 6.
	if b, _ := HistoricalBand(h, "pe", 10); !approx(b.Percentile, 100*3.5/6, 1e-9) {
		t.Errorf("tie percentile = %v", b.Percentile)
	}

	if _, ok := HistoricalBand(h, "pe", math.NaN()); ok {
		t.Error("unknown current value must give no band")
	}
	if _, ok := HistoricalBand(h[:3], "pe", 5); ok {
		t.Errorf("%d periods must be too few", 2)
	}
}

// Group (CSV) multiples before standalone RSBU ones: only the latest kind
// counts.
func TestHistoricalBandKeepsOneKind(t *testing.T) {
	f := models.Float
	var h []models.QuarterData
	for y := 2016; y <= 2020; y++ {
		h = append(h, models.QuarterData{Year: y, Quarter: "Q4", Company: "R", Source: models.SourceCSV, PE: f(30)})
	}
	for y := 2021; y <= 2025; y++ {
		h = append(h, bandYear(y, f(float64(y-2020)), nil, nil, nil))
	}
	b, ok := HistoricalBand(h, "pe", 3)
	if !ok || b.N != 5 || b.Max != 5 || b.From != "2021-Q4" {
		t.Errorf("band = %+v (ok %v), want the 5 RSBU years only", b, ok)
	}
}

func TestHistoricalBandDerived(t *testing.T) {
	f := models.Float
	h := []models.QuarterData{
		bandYear(2022, nil, f(100), f(100), f(10)),
		bandYear(2023, nil, f(200), f(100), f(0)), // no payout: a real 0% yield
		bandYear(2024, nil, f(300), f(100), f(15)),
		bandYear(2025, nil, f(400), f(-50), f(20)), // negative equity: no P/B
	}
	pb, ok := HistoricalBand(h, "pb", 2.5)
	if !ok || pb.N != 3 || pb.Min != 1 || pb.Max != 3 || pb.Median != 2 {
		t.Errorf("P/B band = %+v (ok %v)", pb, ok)
	}
	dy, ok := HistoricalBand(h, "div_yield", 6)
	if !ok || dy.N != 4 || dy.Min != 0 || dy.Max != 10 || dy.Median != 5 {
		t.Errorf("yield band = %+v (ok %v)", dy, ok)
	}
}

func TestMedian(t *testing.T) {
	vals := []float64{5, 1, 3}
	if got := Median(vals); got != 3 {
		t.Errorf("odd median = %v", got)
	}
	if vals[0] != 5 {
		t.Error("Median sorted its input")
	}
	if got := Median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Errorf("even median = %v", got)
	}
	if !math.IsNaN(Median(nil)) {
		t.Error("median of none must be NaN")
	}
}

func TestApplySectorMedians(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	rows := []ScreenerRow{
		{Company: "A", Category: "oil", PE: f(4), PB: f(1)},
		{Company: "B", Category: "oil", PE: f(6)},
		{Company: "C", Category: "oil", PE: f(10), PB: f(2)},
		{Company: "D", Category: "oil"},
		{Company: "E", Category: "retail", PE: f(20)},
		{Company: "F", Category: "retail", PE: f(30)},
		{Company: "G", PE: f(5)},
	}
	ApplySectorMedians(rows)

	byName := map[string]ScreenerRow{}
	for _, r := range rows {
		byName[r.Company] = r
	}
	check := func(name string, got *float64, want float64) {
		t.Helper()
		if math.IsNaN(want) {
			if got != nil {
				t.Errorf("%s: median = %v, want none", name, *got)
			}
			return
		}
		if got == nil || *got != want {
			t.Errorf("%s: median = %v, want %v", name, got, want)
		}
	}
	// A's peers B, C (D has no P/E): median of 6 and 10.
	check("A pe", byName["A"].PESector, 8)
	// D's peers: 4, 6, 10.
	check("D pe", byName["D"].PESector, 6)
	// Only one other oil company has P/B: too few peers.
	check("A pb", byName["A"].PBSector, math.NaN())
	check("D pb", byName["D"].PBSector, 1.5)
	// Retail has one peer each.
	check("E pe", byName["E"].PESector, math.NaN())
	// No category: no sector.
	check("G pe", byName["G"].PESector, math.NaN())
	if byName["A"].SectorPeers != 3 || byName["E"].SectorPeers != 1 || byName["G"].SectorPeers != 0 {
		t.Errorf("peers = %d, %d, %d", byName["A"].SectorPeers, byName["E"].SectorPeers, byName["G"].SectorPeers)
	}
}

func TestScreenerRowHistoryPercentile(t *testing.T) {
	f := models.Float
	var h []models.QuarterData
	for i, pe := range []float64{4, 6, 8, 10} {
		h = append(h, bandYear(2022+i, f(pe), f(pe*10), f(10), nil))
	}
	row := BuildScreenerRow(h, nil)
	// The stored P/E is the latest year's 10: 4, 6, 8 below, itself a tie.
	if row.PEHistPct == nil || !approx(*row.PEHistPct, 100*3.5/4, 1e-9) {
		t.Errorf("P/E percentile = %v", row.PEHistPct)
	}
	if row.DivYieldHistPct != nil {
		t.Errorf("no dividends: yield percentile = %v, want nil", *row.DivYieldHistPct)
	}
}
