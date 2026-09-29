package analytics

import (
	"math"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// makeHistory builds a contiguous quarterly history starting at (startYear, Q1)
// where each quarter's slice is the corresponding row in `values`. Each row
// is {capitalization, revenue, net_profit, ebitda, debt, pe, roe}. Use 0 for
// "no data" — IsEmpty filters out rows that are entirely zero.
func makeHistory(company string, startYear int, values [][7]float64) []models.QuarterData {
	quarters := []string{"Q1", "Q2", "Q3", "Q4"}
	var out []models.QuarterData
	for i, v := range values {
		out = append(out, models.QuarterData{
			Year:           startYear + i/4,
			Quarter:        quarters[i%4],
			Company:        company,
			Category:       "test",
			Capitalization: v[0],
			Revenue:        v[1],
			NetProfit:      v[2],
			EBITDA:         v[3],
			Debt:           v[4],
			PE:             v[5],
			ROE:            v[6],
		})
	}
	return out
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
