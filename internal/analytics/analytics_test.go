package analytics

import (
	"math"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// makeHistory builds a contiguous quarterly history starting at (startYear, Q1)
// where each quarter's slice is the corresponding row in `values`. Each row
// is {capitalization, revenue, net_profit, ebitda, debt, pe, roe}. Use 0 for
// "no data": zeros are stored as unreported (nil) metrics, which keeps the
// fixtures terse. Use makeRow directly to test a reported zero.
func makeHistory(company string, startYear int, values [][7]float64) []models.QuarterData {
	quarters := []string{"Q1", "Q2", "Q3", "Q4"}
	var out []models.QuarterData
	for i, v := range values {
		out = append(out, models.QuarterData{
			Year:           startYear + i/4,
			Quarter:        quarters[i%4],
			Company:        company,
			Category:       "test",
			Capitalization: zeroAsNil(v[0]),
			Revenue:        zeroAsNil(v[1]),
			NetProfit:      zeroAsNil(v[2]),
			EBITDA:         zeroAsNil(v[3]),
			Debt:           zeroAsNil(v[4]),
			PE:             zeroAsNil(v[5]),
			ROE:            zeroAsNil(v[6]),
		})
	}
	return out
}

func zeroAsNil(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return models.Float(v)
}

func approx(a, b, eps float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Abs(a-b) <= eps
}

func TestTTMSeries_FlowSumsLast4Quarters(t *testing.T) {
	// 8 quarters of revenue: 100, 110, 120, 130, 140, 150, 160, 170
	rev := [][7]float64{
		{0, 100, 0, 0, 0, 0, 0},
		{0, 110, 0, 0, 0, 0, 0},
		{0, 120, 0, 0, 0, 0, 0},
		{0, 130, 0, 0, 0, 0, 0},
		{0, 140, 0, 0, 0, 0, 0},
		{0, 150, 0, 0, 0, 0, 0},
		{0, 160, 0, 0, 0, 0, 0},
		{0, 170, 0, 0, 0, 0, 0},
	}
	h := makeHistory("X", 2022, rev)
	s := TTMSeries(h, "revenue")

	// First 3 quarters: not enough data
	for i := 0; i < 3; i++ {
		if !math.IsNaN(s[i].Value) {
			t.Fatalf("TTM[%d] should be NaN, got %v", i, s[i].Value)
		}
	}
	// At i=3: sum 100+110+120+130 = 460
	if !approx(s[3].Value, 460, 1e-9) {
		t.Fatalf("TTM[3] expected 460, got %v", s[3].Value)
	}
	// At i=7: sum 140+150+160+170 = 620
	if !approx(s[7].Value, 620, 1e-9) {
		t.Fatalf("TTM[7] expected 620, got %v", s[7].Value)
	}
}

func TestTTMSeries_StockUsesLastValue(t *testing.T) {
	// Debt is a stock; TTM should carry the latest non-zero value.
	rows := [][7]float64{
		{0, 0, 0, 0, 100, 0, 0},
		{0, 0, 0, 0, 110, 0, 0},
		{0, 0, 0, 0, 0, 0, 0}, // gap
		{0, 0, 0, 0, 130, 0, 0},
	}
	h := makeHistory("X", 2024, rows)
	s := TTMSeries(h, "debt")

	if !approx(s[0].Value, 100, 1e-9) || !approx(s[1].Value, 110, 1e-9) ||
		!approx(s[3].Value, 130, 1e-9) {
		t.Fatalf("stock TTM mismatch: %v", s)
	}
	if !math.IsNaN(s[2].Value) {
		t.Fatalf("stock TTM[2] should be NaN (zero), got %v", s[2].Value)
	}
}

func TestAnnualSeries_FlowSumsAcrossYear(t *testing.T) {
	rows := [][7]float64{
		{0, 100, 0, 0, 0, 0, 0},
		{0, 100, 0, 0, 0, 0, 0},
		{0, 100, 0, 0, 0, 0, 0},
		{0, 100, 0, 0, 0, 0, 0},
		{0, 200, 0, 0, 0, 0, 0},
		{0, 200, 0, 0, 0, 0, 0},
		{0, 200, 0, 0, 0, 0, 0},
		{0, 200, 0, 0, 0, 0, 0},
	}
	h := makeHistory("X", 2023, rows)
	s := AnnualSeries(h, "revenue")
	if len(s) != 2 {
		t.Fatalf("expected 2 yearly points, got %d", len(s))
	}
	if !approx(s[0].Value, 400, 1e-9) || !approx(s[1].Value, 800, 1e-9) {
		t.Fatalf("annual revenue mismatch: %v", s)
	}
	if s[0].Label != "2023" || s[1].Label != "2024" {
		t.Fatalf("annual labels mismatch: %v", s)
	}
}

func TestDerivedSeries_NetMarginTTM(t *testing.T) {
	// Each quarter: revenue=100, net_profit=20 → TTM revenue=400, TTM np=80 → margin 20%
	rows := [][7]float64{
		{0, 100, 20, 0, 0, 0, 0},
		{0, 100, 20, 0, 0, 0, 0},
		{0, 100, 20, 0, 0, 0, 0},
		{0, 100, 20, 0, 0, 0, 0},
	}
	h := makeHistory("X", 2024, rows)
	s := DerivedSeries(h, "net_margin", PeriodTTM)
	if !approx(s[3].Value, 20, 1e-9) {
		t.Fatalf("net margin expected 20%%, got %v", s[3].Value)
	}
}

func TestDerivedSeries_RevenueYoYAnnual(t *testing.T) {
	// year 1: 4×100 = 400, year 2: 4×130 = 520 → YoY = 30%
	rows := [][7]float64{
		{0, 100, 0, 0, 0, 0, 0},
		{0, 100, 0, 0, 0, 0, 0},
		{0, 100, 0, 0, 0, 0, 0},
		{0, 100, 0, 0, 0, 0, 0},
		{0, 130, 0, 0, 0, 0, 0},
		{0, 130, 0, 0, 0, 0, 0},
		{0, 130, 0, 0, 0, 0, 0},
		{0, 130, 0, 0, 0, 0, 0},
	}
	h := makeHistory("X", 2023, rows)
	s := DerivedSeries(h, "revenue_yoy", PeriodAnnual)
	if len(s) != 2 {
		t.Fatalf("expected 2 annual points, got %d", len(s))
	}
	if !math.IsNaN(s[0].Value) {
		t.Fatalf("YoY[0] should be NaN (no prior year), got %v", s[0].Value)
	}
	if !approx(s[1].Value, 30, 1e-9) {
		t.Fatalf("YoY annual expected 30, got %v", s[1].Value)
	}
}

func TestDerivedSeries_CAGR3Y(t *testing.T) {
	// Build 16 quarters where TTM revenue grows from 400 to 691.2 (20% CAGR over 3 years).
	// We just pick constant quarterly revenue per year so TTM jumps cleanly.
	years := []float64{100, 120, 144, 172.8} // each year's quarterly revenue
	var rows [][7]float64
	for _, y := range years {
		for j := 0; j < 4; j++ {
			rows = append(rows, [7]float64{0, y, 0, 0, 0, 0, 0})
		}
	}
	h := makeHistory("X", 2022, rows)
	s := DerivedSeries(h, "revenue_cagr3", PeriodTTM)
	// last point: TTM = 4*172.8 = 691.2; 3y ago TTM = 4*100 = 400; (691.2/400)^(1/3) - 1 = 20%
	last := s[len(s)-1]
	if !approx(last.Value, 20, 1e-6) {
		t.Fatalf("revenue_cagr3 expected ~20%%, got %v", last.Value)
	}
}

func TestBuildSnapshot_FillsAllFields(t *testing.T) {
	// 16 quarters with revenue 100/q growing to 130/q, npm 20%, EBITDA 30%,
	// debt 200, P/E 10, ROE 18.
	var rows [][7]float64
	for y := 0; y < 4; y++ {
		rev := 100.0 + 10*float64(y)
		for q := 0; q < 4; q++ {
			rows = append(rows, [7]float64{
				/*cap*/ 5000,
				/*rev*/ rev,
				/*np*/ rev * 0.20,
				/*ebitda*/ rev * 0.30,
				/*debt*/ 200,
				/*pe*/ 10,
				/*roe*/ 18,
			})
		}
	}
	h := makeHistory("X", 2022, rows)
	snap := BuildSnapshot(h)

	if snap.Company != "X" || snap.LastLabel != "2025-Q4" {
		t.Fatalf("snapshot identity wrong: %+v", snap)
	}
	// TTM revenue at last: 4*130 = 520
	if !approx(snap.Revenue, 520, 1e-9) {
		t.Fatalf("snapshot TTM revenue expected 520, got %v", snap.Revenue)
	}
	if !approx(snap.NetMargin, 20, 1e-6) {
		t.Fatalf("snapshot net margin expected 20, got %v", snap.NetMargin)
	}
	if !approx(snap.EBITDAMargin, 30, 1e-6) {
		t.Fatalf("snapshot EBITDA margin expected 30, got %v", snap.EBITDAMargin)
	}
	// Debt/EBITDA at last: 200 / (4*130*0.3) = 200/156 ≈ 1.282
	if !approx(snap.DebtEBITDA, 200.0/156.0, 1e-6) {
		t.Fatalf("snapshot D/EBITDA expected ~1.28, got %v", snap.DebtEBITDA)
	}
	if snap.Score < 50 {
		t.Fatalf("snapshot score expected > 50 for a healthy profile, got %d", snap.Score)
	}
}

func TestBuildSnapshot_EmptyHistory(t *testing.T) {
	snap := BuildSnapshot(nil)
	if snap.Score != 0 {
		t.Fatalf("empty snapshot score should be 0, got %d", snap.Score)
	}
	if !math.IsNaN(snap.Revenue) || !math.IsNaN(snap.PE) {
		t.Fatalf("empty snapshot should have NaN fields")
	}
}

func TestComputeScore_AllGood(t *testing.T) {
	s := Snapshot{
		RevenueCAGR3Y: 25,
		ROE:           22,
		NetMargin:     25,
		DebtEBITDA:    0.5,
		PE:            7,
	}
	if got := computeScore(s); got != 100 {
		t.Fatalf("all-good score should be 100, got %d", got)
	}
}

func TestComputeScore_AllBad(t *testing.T) {
	s := Snapshot{
		RevenueCAGR3Y: -10,
		ROE:           -5,
		NetMargin:     -5,
		DebtEBITDA:    10,
		PE:            100,
	}
	if got := computeScore(s); got != 0 {
		t.Fatalf("all-bad score should be 0, got %d", got)
	}
}

func TestReportedZeroIsNotMissing(t *testing.T) {
	zero, missing := models.Float(0), (*float64)(nil)
	h := []models.QuarterData{
		{Year: 2024, Quarter: "Q3", Company: "X", Debt: models.Float(50), Revenue: models.Float(10)},
		{Year: 2024, Quarter: "Q4", Company: "X", Debt: zero, Revenue: missing},
	}

	q := QuarterlySeries(h, "debt")
	if q[1].Value != 0 {
		t.Errorf("quarterly debt Q4 = %v, want reported 0", q[1].Value)
	}
	if r := QuarterlySeries(h, "revenue"); !math.IsNaN(r[1].Value) {
		t.Errorf("quarterly revenue Q4 = %v, want NaN (not reported)", r[1].Value)
	}
	// Stock metric TTM carries the latest reported value — the zero, not 50.
	if ttm := TTMSeries(h, "debt"); ttm[1].Value != 0 {
		t.Errorf("TTM debt Q4 = %v, want 0", ttm[1].Value)
	}
	if a := AnnualSeries(h, "debt"); a[0].Value != 0 {
		t.Errorf("annual debt 2024 = %v, want 0", a[0].Value)
	}
	if snap := BuildSnapshot(h); snap.Debt != 0 {
		t.Errorf("snapshot debt = %v, want 0 (debt-free company)", snap.Debt)
	}
}

func TestValuationMetrics(t *testing.T) {
	f := models.Float
	// Annual-style rows (figures on Q4) plus a bank-style Q1 row carrying only
	// net profit, which must not break the trailing P/B / yield.
	h := []models.QuarterData{
		{Year: 2023, Quarter: "Q4", Company: "X", Capitalization: f(1000), Equity: f(500), Dividends: f(50)},
		{Year: 2024, Quarter: "Q4", Company: "X", Capitalization: f(1200), Equity: f(400), Dividends: f(0)},
		{Year: 2025, Quarter: "Q1", Company: "X", NetProfit: f(10)},
	}

	pb := DerivedSeries(h, "pb", PeriodQuarter)
	if !approx(pb[0].Value, 2, 1e-9) || !approx(pb[1].Value, 3, 1e-9) || !math.IsNaN(pb[2].Value) {
		t.Errorf("quarterly P/B = %v, want [2 3 NaN]", pb)
	}
	dy := DerivedSeries(h, "div_yield", PeriodAnnual)
	if !approx(dy[0].Value, 5, 1e-9) || dy[1].Value != 0 {
		t.Errorf("annual dividend yield = %v, want [5 0]", dy)
	}

	snap := BuildSnapshot(h)
	if !approx(snap.PB, 3, 1e-9) {
		t.Errorf("snapshot P/B = %v, want 3 (carried from 2024-Q4)", snap.PB)
	}
	if snap.DivYield != 0 {
		t.Errorf("snapshot dividend yield = %v, want 0 (no payout in 2024 is a real zero)", snap.DivYield)
	}

	noEquity := []models.QuarterData{{Year: 2024, Quarter: "Q4", Company: "Y", Capitalization: f(100), Equity: f(0)}}
	if v := DerivedSeries(noEquity, "pb", PeriodQuarter)[0].Value; !math.IsNaN(v) {
		t.Errorf("P/B with zero equity = %v, want NaN", v)
	}
	for _, m := range []string{"pb", "div_yield", "equity", "dividends"} {
		if !IsValidMetric(m) {
			t.Errorf("IsValidMetric(%q) = false", m)
		}
	}
}

func TestValuationEdgeCases(t *testing.T) {
	f := models.Float
	snap := BuildSnapshot(nil)
	if !math.IsNaN(snap.PB) || !math.IsNaN(snap.DivYield) || !math.IsNaN(snap.PE) {
		t.Errorf("empty history: PB=%v DivYield=%v PE=%v, want NaN", snap.PB, snap.DivYield, snap.PE)
	}

	// Bank-style: latest row is Q1 with profit only; valuation comes from Q4
	// and carries its label.
	h := []models.QuarterData{
		{Year: 2024, Quarter: "Q4", Company: "B", Capitalization: f(900), Equity: f(300), Dividends: f(45)},
		{Year: 2025, Quarter: "Q1", Company: "B", NetProfit: f(50)},
	}
	snap = BuildSnapshot(h)
	if !approx(snap.PB, 3, 1e-9) || snap.PBLabel != "2024-Q4" {
		t.Errorf("PB = %v (%q), want 3 (2024-Q4)", snap.PB, snap.PBLabel)
	}
	if !approx(snap.DivYield, 5, 1e-9) || snap.DivYieldLabel != "2024-Q4" {
		t.Errorf("DivYield = %v (%q), want 5 (2024-Q4)", snap.DivYield, snap.DivYieldLabel)
	}

	neg := []models.QuarterData{{Year: 2024, Quarter: "Q4", Company: "N", Capitalization: f(100), Equity: f(-20)}}
	if v := DerivedSeries(neg, "pb", PeriodQuarter)[0].Value; !math.IsNaN(v) {
		t.Errorf("P/B with negative equity = %v, want NaN", v)
	}
	an := CheckRow(neg[0])
	if len(an) != 1 || !an[0].Concerns("pb") || !an[0].Concerns("roe") {
		t.Errorf("negative equity anomalies = %+v, want one concerning pb and roe", an)
	}
}

func rsbuYear(year int, revenue, netProfit float64) models.QuarterData {
	return models.QuarterData{Year: year, Quarter: "Q4", Company: "R", Source: models.SourceRSBU,
		Revenue: models.Float(revenue), NetProfit: models.Float(netProfit)}
}

func TestTTMAnnualRSBURows(t *testing.T) {
	// Annual-only RSBU history: every row is Q4 of a different year.
	h := []models.QuarterData{
		rsbuYear(2021, 100, 10), rsbuYear(2022, 110, 11), rsbuYear(2023, 120, 12), rsbuYear(2024, 150, 15),
	}
	ttm := TTMSeries(h, "net_profit")
	// Before the fix the last point summed four different years (48).
	if ttm[3].Value != 15 {
		t.Errorf("TTM net profit 2024 = %v, want 15 (the annual figure itself)", ttm[3].Value)
	}
	if a := AnnualSeries(h, "revenue"); a[3].Value != 150 {
		t.Errorf("annual revenue 2024 = %v, want 150", a[3].Value)
	}
	// YoY with a TTM base compares with the previous calendar year, not the
	// row four positions back.
	y := DerivedSeries(h, "revenue_yoy", PeriodTTM)
	if !approx(y[3].Value, (150.0/120-1)*100, 1e-9) {
		t.Errorf("TTM revenue YoY 2024 = %v, want 25", y[3].Value)
	}
	if !math.IsNaN(y[0].Value) {
		t.Errorf("TTM revenue YoY 2021 = %v, want NaN (no 2020)", y[0].Value)
	}
	c := DerivedSeries(h, "revenue_cagr3", PeriodAnnual)
	if want := (math.Pow(150.0/100, 1.0/3) - 1) * 100; !approx(c[3].Value, want, 1e-9) {
		t.Errorf("3y CAGR 2024 = %v, want %v", c[3].Value, want)
	}
	if snap := BuildSnapshot(h); !approx(snap.NetMargin, 10, 1e-9) || snap.NetProfit != 15 {
		t.Errorf("snapshot net profit/margin = %v/%v, want 15/10", snap.NetProfit, snap.NetMargin)
	}
}

func TestTTMRequiresConsecutiveQuarters(t *testing.T) {
	f := models.Float
	q := func(y int, qq string, np float64) models.QuarterData {
		return models.QuarterData{Year: y, Quarter: qq, Company: "Q", Source: models.SourceCBR102, NetProfit: f(np)}
	}
	// 2024-Q2 is missing: the window ending 2025-Q1 spans five quarters.
	h := []models.QuarterData{q(2024, "Q1", 1), q(2024, "Q3", 3), q(2024, "Q4", 4), q(2025, "Q1", 5), q(2025, "Q2", 6)}
	ttm := TTMSeries(h, "net_profit")
	if !math.IsNaN(ttm[3].Value) {
		t.Errorf("TTM at 2025-Q1 across a gap = %v, want NaN", ttm[3].Value)
	}
	if ttm[4].Value != 18 {
		t.Errorf("TTM at 2025-Q2 = %v, want 18 (Q3..Q2)", ttm[4].Value)
	}
}

func TestBuildCurrent(t *testing.T) {
	f := models.Float
	h := []models.QuarterData{
		{Year: 2024, Quarter: "Q4", Company: "R", Source: models.SourceRSBU, NetProfit: f(90), Equity: f(400), Dividends: f(30)},
		{Year: 2025, Quarter: "Q4", Company: "R", Source: models.SourceRSBU, NetProfit: f(100), Equity: f(500), Dividends: f(40)},
	}
	quote := models.MarketQuote{Company: "R", Capitalization: 800, PriceDate: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
	c := BuildCurrent(h, quote)
	if !approx(c.PE, 8, 1e-9) || c.EarningsLabel != "2025-Q4" {
		t.Errorf("current P/E = %v (%s), want 8 (2025-Q4)", c.PE, c.EarningsLabel)
	}
	if !approx(c.PB, 1.6, 1e-9) || !approx(c.DivYield, 5, 1e-9) || c.PriceDate != "2026-09-29" {
		t.Errorf("current = %+v, want P/B 1.6, yield 5%%, date 2026-09-29", c)
	}

	loss := []models.QuarterData{{Year: 2025, Quarter: "Q4", Company: "L", Source: models.SourceRSBU, NetProfit: f(-5)}}
	if c := BuildCurrent(loss, quote); !math.IsNaN(c.PE) || c.EarningsLabel != "2025-Q4" {
		t.Errorf("loss-making: P/E = %v (%s), want NaN with label", c.PE, c.EarningsLabel)
	}
	if c := BuildCurrent(h, models.MarketQuote{}); !math.IsNaN(c.PE) || !math.IsNaN(c.Capitalization) {
		t.Errorf("no quote: %+v, want NaN", c)
	}
}

func TestBuildCurrentStale(t *testing.T) {
	f := models.Float
	// Data stopped in 2023; pricing it in September 2026 would be meaningless.
	h := []models.QuarterData{{Year: 2023, Quarter: "Q4", Company: "S", Source: models.SourceRSBU,
		NetProfit: f(100), Equity: f(500), Dividends: f(10)}}
	quote := models.MarketQuote{Company: "S", Capitalization: 800, PriceDate: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
	c := BuildCurrent(h, quote)
	if !c.Stale || !math.IsNaN(c.PE) || !math.IsNaN(c.PB) || !math.IsNaN(c.DivYield) {
		t.Errorf("stale fundamentals: %+v, want NaN multiples and Stale", c)
	}
	if c.EarningsLabel != "2023-Q4 (устарело)" {
		t.Errorf("earnings label = %q", c.EarningsLabel)
	}
	// 2025-Q4 ends Dec 2025: 9 months before the quote, still fresh.
	h[0].Year = 2025
	if c := BuildCurrent(h, quote); c.Stale || !approx(c.PE, 8, 1e-9) {
		t.Errorf("fresh fundamentals: %+v", c)
	}
}

func TestQuoteIsFresh(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	q := models.MarketQuote{Capitalization: 1, PriceDate: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
	if !QuoteIsFresh(q, now) {
		t.Error("yesterday's close should be fresh")
	}
	q.PriceDate = now.Add(-31 * 24 * time.Hour)
	if QuoteIsFresh(q, now) {
		t.Error("a month-old close should not be fresh")
	}
}

func TestGrowthNeverMixesGroupAndStandalone(t *testing.T) {
	f := models.Float
	csvYear := func(y int, np float64) []models.QuarterData {
		var out []models.QuarterData
		for _, q := range []string{"Q1", "Q2", "Q3", "Q4"} {
			out = append(out, models.QuarterData{Year: y, Quarter: q, Company: "M", Source: models.SourceCSV, NetProfit: f(np / 4)})
		}
		return out
	}
	// Group IFRS quarters for 2022-2023, then parent-only RSBU annual rows.
	h := append(csvYear(2022, 400), csvYear(2023, 480)...)
	h = append(h,
		models.QuarterData{Year: 2024, Quarter: "Q4", Company: "M", Source: models.SourceRSBU, NetProfit: f(90)},
		models.QuarterData{Year: 2025, Quarter: "Q4", Company: "M", Source: models.SourceRSBU, NetProfit: f(99)},
	)
	byLabel := map[string]float64{}
	for _, p := range DerivedSeries(h, "net_profit_yoy", PeriodTTM) {
		byLabel[p.Label] = p.Value
	}
	if !approx(byLabel["2023-Q4"], 20, 1e-9) {
		t.Errorf("group YoY 2023-Q4 = %v, want 20", byLabel["2023-Q4"])
	}
	if !math.IsNaN(byLabel["2024-Q4"]) {
		t.Errorf("YoY 2024-Q4 across group->standalone = %v, want NaN", byLabel["2024-Q4"])
	}
	if !approx(byLabel["2025-Q4"], 10, 1e-9) {
		t.Errorf("standalone YoY 2025-Q4 = %v, want 10", byLabel["2025-Q4"])
	}

	ttm := TTMSeries(h, "net_profit")
	latest, _ := LatestValid(ttm)
	if prev, ok := YearAgo(ttm, latest, PeriodTTM, 1); !ok || prev.Value != 90 {
		t.Errorf("YearAgo(2025-Q4) = %+v, %v; want 2024-Q4 = 90", prev, ok)
	}
	if _, ok := YearAgo(ttm, ttm[len(ttm)-2], PeriodTTM, 1); ok {
		t.Error("YearAgo from standalone 2024-Q4 to group 2023-Q4 should not match")
	}
}

func TestSnapshotStockFiguresFromLatestPeriod(t *testing.T) {
	f := models.Float
	// Bank: Q4 carries cap/P/E/ROE, the newer Q1 only profit.
	h := []models.QuarterData{
		{Year: 2025, Quarter: "Q4", Company: "B", Source: models.SourceCBR102, NetProfit: f(400), Capitalization: f(6000), PE: f(4), ROE: f(22)},
		{Year: 2026, Quarter: "Q1", Company: "B", Source: models.SourceCBR102, NetProfit: f(420)},
	}
	s := BuildSnapshot(h)
	if s.LastLabel != "2026-Q1" || s.PE != 4 || s.ROE != 22 || s.Capitalization != 6000 {
		t.Errorf("snapshot = last %s, P/E %v, ROE %v, cap %v", s.LastLabel, s.PE, s.ROE, s.Capitalization)
	}
	if s.PELabel != "2025-Q4" || s.ROELabel != "2025-Q4" || s.CapLabel != "2025-Q4" {
		t.Errorf("labels = %q %q %q, want 2025-Q4", s.PELabel, s.ROELabel, s.CapLabel)
	}
}

// A manual entry is an annual, comparable figure: it is its own TTM value,
// and growth compares it with other group figures but not with RSBU years.
func TestManualRowsAreAnnualGroupFigures(t *testing.T) {
	f := models.Float
	h := models.ApplyManual([]models.QuarterData{
		{Year: 2023, Quarter: "Q4", Company: "M", Source: models.SourceRSBU, Revenue: f(50)},
		{Year: 2024, Quarter: "Q4", Company: "M", Source: models.SourceRSBU, Revenue: f(60)},
	}, []models.ManualFinancials{
		{Company: "M", Year: 2024, Revenue: f(1000)},
		{Company: "M", Year: 2025, Revenue: f(1200)},
	})
	if p, _ := LatestValid(TTMSeries(h, "revenue")); p.Value != 1200 {
		t.Errorf("TTM revenue = %v, want 1200 (a manual Q4 row is annual)", p.Value)
	}
	yoy := DerivedSeries(h, "revenue_yoy", PeriodAnnual)
	byYear := map[int]float64{}
	for _, p := range yoy {
		byYear[p.Year] = p.Value
	}
	if !approx(byYear[2025], 20, 1e-9) || !math.IsNaN(byYear[2024]) {
		t.Errorf("YoY = %v; want 2025 +20%% and none for 2024 (RSBU -> manual)", byYear)
	}
	if !IsComparable(models.SourceManual) || SourceLabel(models.SourceManual) != "ввод вручную" {
		t.Error("manual source must be comparable and labelled")
	}
}

// A manual year among CSV quarters (the reviewer's case): the quarterly YoY
// never compares the annual Q4 figure with a single quarter, and the TTM of
// the next quarters still sums four quarters through the replaced Q4.
func TestManualYearAmongCSVQuarters(t *testing.T) {
	f := models.Float
	var fetched []models.QuarterData
	for year := 2024; year <= 2026; year++ {
		for _, q := range []string{"Q1", "Q2", "Q3", "Q4"} {
			if year == 2026 && (q == "Q3" || q == "Q4") {
				continue
			}
			fetched = append(fetched, models.QuarterData{Year: year, Quarter: q, Company: "C", Source: models.SourceCSV,
				Revenue: f(100), NetProfit: f(10)})
		}
	}
	h := models.ApplyManual(fetched, []models.ManualFinancials{{Company: "C", Year: 2025, Revenue: f(420), NetProfit: f(44)}})

	for _, p := range DerivedSeries(h, "revenue_yoy", PeriodQuarter) {
		if p.Label == "2025-Q4" && !math.IsNaN(p.Value) {
			t.Errorf("quarterly YoY at the manual year = %v, want NaN (a year vs a quarter)", p.Value)
		}
	}
	ttm := map[string]float64{}
	for _, p := range TTMSeries(h, "net_profit") {
		ttm[p.Label] = p.Value
	}
	if ttm["2025-Q4"] != 44 || ttm["2026-Q1"] != 40 || ttm["2026-Q2"] != 40 {
		t.Errorf("TTM = %v; want 44 at the manual year and 40 after it", ttm)
	}
	if p, _ := LatestValid(TTMSeries(h, "net_profit")); p.Label != "2026-Q2" {
		t.Errorf("latest TTM at %s, want 2026-Q2 (newer quarters are not lost)", p.Label)
	}

	// A dividends-only entry for a year with no row does not claim to be a
	// manual statement.
	h = models.ApplyManual(fetched, []models.ManualFinancials{{Company: "C", Year: 2023, Dividends: f(5)}})
	if h[0].Year != 2023 || h[0].Source != models.SourceCSV {
		t.Errorf("dividends-only row = %+v, want source csv", h[0])
	}
}
