package analytics

import (
	"math"
	"testing"
	"time"

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
	b, ok := HistoricalBand(h, "pe", 7, true)
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
	if b, _ := HistoricalBand(h, "pe", 10, true); !approx(b.Percentile, 100*3.5/6, 1e-9) {
		t.Errorf("tie percentile = %v", b.Percentile)
	}

	if _, ok := HistoricalBand(h, "pe", math.NaN(), true); ok {
		t.Error("unknown current value must give no band")
	}
	if _, ok := HistoricalBand(h[:3], "pe", 5, true); ok {
		t.Errorf("%d periods must be too few", 2)
	}
}

// Group (CSV) multiples before standalone RSBU ones: only the kind of the
// current figure counts, and the window ends at that kind's latest period.
// Yield is the company's payout over its cap whatever the row's source, so
// its band takes every period.
func TestHistoricalBandKeepsOneKind(t *testing.T) {
	f := models.Float
	var h []models.QuarterData
	for y := 2016; y <= 2020; y++ {
		h = append(h, models.QuarterData{Year: y, Quarter: "Q4", Company: "R", Source: models.SourceCSV,
			PE: f(30), Capitalization: f(100), Dividends: f(5)})
	}
	for y := 2021; y <= 2025; y++ {
		h = append(h, bandYear(y, f(float64(y-2020)), f(100), nil, f(10)))
	}
	b, ok := HistoricalBand(h, "pe", 3, true)
	if !ok || b.N != 5 || b.Max != 5 || b.From != "2021-Q4" {
		t.Errorf("standalone band = %+v (ok %v), want the 5 RSBU years only", b, ok)
	}
	b, ok = HistoricalBand(h, "pe", 3, false)
	if !ok || b.N != 5 || b.Min != 30 || b.To != "2020-Q4" {
		t.Errorf("group band = %+v (ok %v), want the 5 CSV years only", b, ok)
	}
	for _, standalone := range []bool{true, false} {
		dy, ok := HistoricalBand(h, "div_yield", 7, standalone)
		if !ok || dy.N != 10 || dy.Min != 5 || dy.Max != 10 || dy.Percentile != 50 {
			t.Errorf("yield band (standalone %v) = %+v (ok %v), want all 10 years", standalone, dy, ok)
		}
	}
}

// CSV P/E is quarterly, so each year adds four periods to the band.
func TestHistoricalBandQuarterlyCSV(t *testing.T) {
	var h []models.QuarterData
	for y := 2023; y <= 2025; y++ {
		for q := 1; q <= 4; q++ {
			h = append(h, models.QuarterData{Year: y, Quarter: "Q" + string(rune('0'+q)), Company: "R",
				Source: models.SourceCSV, PE: models.Float(float64(q))})
		}
	}
	b, ok := HistoricalBand(h, "pe", 2.5, false)
	if !ok || b.N != 12 || b.From != "2023-Q1" || b.To != "2025-Q4" || b.Percentile != 50 {
		t.Errorf("band = %+v (ok %v)", b, ok)
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
	pb, ok := HistoricalBand(h, "pb", 2.5, true)
	if !ok || pb.N != 3 || pb.Min != 1 || pb.Max != 3 || pb.Median != 2 {
		t.Errorf("P/B band = %+v (ok %v)", pb, ok)
	}
	dy, ok := HistoricalBand(h, "div_yield", 6, true)
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

// sectorRow is a screener row with stored valuations from period p.
func sectorRow(company, category string, p Point, pe, pb, dy *float64) ScreenerRow {
	r := ScreenerRow{Company: company, Category: category, PE: pe, PB: pb, DivYield: dy}
	r.setBasis(p, p, p)
	return r
}

func TestApplySectorMedians(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	group := Point{Label: "2025-Q4", Year: 2025, Index: 20254}
	solo := Point{Label: "2025-Q4", Year: 2025, Index: 20254, Standalone: true}
	old := Point{Label: "2016-Q4", Year: 2016, Index: 20164}
	quoted := sectorRow("Q", "oil", old, f(50), nil, nil)
	quoted.Current = true // priced at a live quote: current whatever its basis says
	rows := []ScreenerRow{
		sectorRow("A", "oil", group, f(4), f(1), f(5)),
		sectorRow("B", "oil", group, f(6), nil, f(7)),
		sectorRow("C", "oil", group, f(10), f(2), nil),
		sectorRow("D", "oil", group, nil, nil, nil),
		sectorRow("O", "oil", old, f(1), f(9), f(1)),    // stopped reporting: no peer
		sectorRow("S", "oil", solo, f(100), f(9), f(9)), // standalone RSBU
		quoted,
		func() ScreenerRow {
			b := sectorRow("BK", "oil", group, f(1000), nil, f(50))
			b.Bank = true
			return b
		}(),
		sectorRow("E", "retail", group, f(20), nil, nil),
		sectorRow("F", "retail", group, f(30), nil, nil),
		sectorRow("G", "", group, f(5), nil, nil),
	}
	ApplySectorMedians(rows, now)

	byName := map[string]ScreenerRow{}
	for _, r := range rows {
		byName[r.Company] = r
	}
	check := func(name string, got *float64, peers int, want float64, wantPeers int) {
		t.Helper()
		if math.IsNaN(want) {
			if got != nil || peers != 0 {
				t.Errorf("%s: median = %v (%d peers), want none", name, *got, peers)
			}
			return
		}
		if got == nil || *got != want || peers != wantPeers {
			t.Errorf("%s: median = %v (%d peers), want %v (%d)", name, got, peers, want, wantPeers)
		}
	}
	a := byName["A"]
	// A's group peers with a current P/E: B, C, Q (D has none, O is too old,
	// S is standalone, BK is a bank).
	check("A pe", a.PESector, a.PESectorPeers, 10, 3)
	// Only C among A's peers has a current group P/B: too few.
	check("A pb", a.PBSector, a.PBSectorPeers, math.NaN(), 0)
	// Yield does not depend on the kind: B and S count, O is too old.
	check("A yield", a.DivYieldSector, a.DivYieldSectorPeers, 8, 2)
	// D has nothing to compare.
	check("D pe", byName["D"].PESector, byName["D"].PESectorPeers, math.NaN(), 0)
	// O's own figures are too old to compare with today's sector.
	check("O pe", byName["O"].PESector, byName["O"].PESectorPeers, math.NaN(), 0)
	// S has no standalone peers. The bank in the same category is not one.
	check("S pe", byName["S"].PESector, byName["S"].PESectorPeers, math.NaN(), 0)
	check("BK pe", byName["BK"].PESector, byName["BK"].PESectorPeers, math.NaN(), 0)
	check("S yield", byName["S"].DivYieldSector, byName["S"].DivYieldSectorPeers, 6, 2)
	// Retail has one peer each.
	check("E pe", byName["E"].PESector, byName["E"].PESectorPeers, math.NaN(), 0)
	// No category: no sector.
	check("G pe", byName["G"].PESector, byName["G"].PESectorPeers, math.NaN(), 0)
	if !quoted.CurrentAt("pe", now) || byName["O"].CurrentAt("pe", now) || !a.CurrentAt("pb", now) {
		t.Error("CurrentAt misjudges the rows' age")
	}
}

func TestScreenerRowHistoryPercentile(t *testing.T) {
	f := models.Float
	var h []models.QuarterData
	for i, pe := range []float64{4, 6, 8, 10} {
		h = append(h, bandYear(2022+i, f(pe), f(pe*10), f(10), nil))
	}
	row := BuildScreenerRow(h, nil, nil)
	// The stored P/E is the latest year's 10: 4, 6, 8 below, itself a tie.
	if row.PEHistPct == nil || !approx(*row.PEHistPct, 100*3.5/4, 1e-9) {
		t.Errorf("P/E percentile = %v", row.PEHistPct)
	}
	if row.DivYieldHistPct != nil {
		t.Errorf("no dividends: yield percentile = %v, want nil", *row.DivYieldHistPct)
	}
}

// A live quote priced on standalone RSBU earnings (the newer CSV quarters do
// not make four of a kind) must be ranked among the RSBU years, not among the
// CSV quarters that happen to hold the latest stored P/E.
func TestScreenerRowBandFollowsQuoteBasis(t *testing.T) {
	f := models.Float
	var h []models.QuarterData
	for i, pe := range []float64{5, 6, 8, 12, 14} {
		y := 2020 + i
		h = append(h, models.QuarterData{Year: y, Quarter: "Q4", Company: "R", Category: "retail", Source: models.SourceRSBU,
			PE: f(pe), Capitalization: f(pe * 100), NetProfit: f(100), Equity: f(500)})
	}
	for q := 1; q <= 3; q++ {
		h = append(h, models.QuarterData{Year: 2025, Quarter: "Q" + string(rune('0'+q)), Company: "R", Category: "retail",
			Source: models.SourceCSV, PE: f(30), NetProfit: f(30)})
	}
	quote := models.MarketQuote{Company: "R", Capitalization: 1000, PriceDate: time.Date(2025, 11, 3, 0, 0, 0, 0, time.UTC)}
	row := BuildScreenerRow(h, &quote, nil)

	if row.PE == nil || *row.PE != 10 {
		t.Fatalf("current P/E = %v, want 1000/100", row.PE)
	}
	basis := row.Basis["pe"]
	if basis.Label != "2024-Q4" || !basis.Standalone {
		t.Errorf("P/E basis = %+v, want standalone 2024-Q4", basis)
	}
	b, ok := row.Bands["pe"]
	// 5, 6, 8 of the five RSBU years are below 10.
	if !ok || b.N != 5 || b.To != "2024-Q4" || row.PEHistPct == nil || *row.PEHistPct != 60 {
		t.Errorf("P/E band = %+v (ok %v), percentile %v; want the RSBU years at 60%%", b, ok, row.PEHistPct)
	}
	// P/B at the quote rests on RSBU equity: 1000/500 among the RSBU years'
	// 1, 1.2, 1.6, 2.4, 2.8.
	if b, ok := row.Bands["pb"]; !ok || b.N != 5 || b.Percentile != 60 {
		t.Errorf("P/B band = %+v (ok %v)", b, ok)
	}
}

// A bank (CBR): Q1-Q3 rows carry profit and equity but no cap, so its stored
// P/E and P/B come from the last Q4 and the bands hold Q4s only.
func TestScreenerRowBankBands(t *testing.T) {
	f := models.Float
	var h []models.QuarterData
	for y := 2021; y <= 2025; y++ {
		for q := 1; q <= 4; q++ {
			row := models.QuarterData{Year: y, Quarter: "Q" + string(rune('0'+q)), Company: "B", Category: "banks",
				Source: models.SourceCBR102, NetProfit: f(100), Equity: f(1000)}
			if q == 4 {
				// Caps grow slowly: a 2× year-over-year jump would cut the band.
				row.Capitalization, row.PE = f(4000+float64(y-2021)*400), f(float64(y-2020)*2.5)
			}
			h = append(h, row)
		}
	}
	h = append(h, models.QuarterData{Year: 2026, Quarter: "Q1", Company: "B", Category: "banks",
		Source: models.SourceCBR102, NetProfit: f(100), Equity: f(1000)})
	row := BuildScreenerRow(h, nil, nil)

	if row.Score != nil {
		t.Errorf("bank score = %v, want nil so it does not sort with industrials", *row.Score)
	}
	if row.LastPeriod != "2026-Q1" || row.Basis["pe"].Label != "2025-Q4" || row.Basis["pb"].Label != "2025-Q4" {
		t.Errorf("last %s, P/E basis %s, P/B basis %s", row.LastPeriod, row.Basis["pe"].Label, row.Basis["pb"].Label)
	}
	pe, ok := row.Bands["pe"]
	if !ok || pe.N != 5 || pe.From != "2021-Q4" || pe.Current != 12.5 {
		t.Errorf("P/E band = %+v (ok %v)", pe, ok)
	}
	if pb, ok := row.Bands["pb"]; !ok || pb.N != 5 || math.Abs(pb.Max-5.6) > 1e-9 {
		t.Errorf("P/B band = %+v (ok %v)", pb, ok)
	}
}

func TestScreenerBandStopsAtUnreviewedCapJump(t *testing.T) {
	f := models.Float
	var h []models.QuarterData
	for y := 2016; y <= 2023; y++ {
		h = append(h, models.QuarterData{Year: y, Quarter: "Q4", Company: "R", Source: models.SourceRSBU,
			PE: f(10), Capitalization: f(100), NetProfit: f(10), Equity: f(50)})
	}
	h = append(h, models.QuarterData{Year: 2024, Quarter: "Q4", Company: "R", Source: models.SourceRSBU,
		PE: f(4), Capitalization: f(300), NetProfit: f(75), Equity: f(50)})
	if row := BuildScreenerRow(h, nil, nil); row.PEHistPct != nil {
		t.Errorf("unreviewed 3× jump still has a P/E percentile %v", *row.PEHistPct)
	}
	row := BuildScreenerRow(h, nil, []CapReview{{From: 2023, To: 2024, Kind: CapReviewPrice}})
	if row.PEHistPct == nil || row.Bands["pe"].N < MinBandPoints {
		t.Errorf("a price review must keep the decade in the band, got %+v", row.Bands["pe"])
	}
}
