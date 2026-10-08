package analytics

import (
	"math"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

func TestPayoutAndDividendGrowth(t *testing.T) {
	f := models.Float
	annual := func(y int, profit, div float64, source string) models.QuarterData {
		q := models.QuarterData{Year: y, Quarter: "Q4", Company: "X", Source: source, NetProfit: f(profit)}
		if div >= 0 {
			q.Dividends = f(div)
		}
		return q
	}
	h := []models.QuarterData{
		annual(2021, 100, 10, models.SourceRSBU),
		annual(2022, 100, 20, models.SourceManual),
		annual(2023, 0, 5, models.SourceRSBU), // loss: no payout
		annual(2024, 80, 0, models.SourceRSBU),
		{Year: 2024, Quarter: "Q4", Company: "B", Source: models.SourceCBR102, NetProfit: f(50), Dividends: f(25)},
		{Year: 2024, Quarter: "Q4", Company: "C", Source: models.SourceCSV, NetProfit: f(50), Dividends: f(25)},
	}

	byLabel := map[string]float64{}
	for _, p := range payoutSeries(h) {
		if !math.IsNaN(p.Value) {
			byLabel[p.Label] = p.Value
		}
	}
	if byLabel["2021-Q4"] != 10 || byLabel["2022-Q4"] != 20 {
		t.Errorf("payouts = %v", byLabel)
	}
	if v, ok := byLabel["2024-Q4"]; !ok || v != 0 {
		t.Errorf("zero dividend payout = %v, ok %v", v, ok)
	}
	if _, ok := byLabel["2023-Q4"]; ok {
		t.Error("a loss must not have a payout")
	}
	for _, p := range payoutSeries([]models.QuarterData{h[4], h[5]}) {
		if !math.IsNaN(p.Value) {
			t.Errorf("bank or quarterly CSV payout = %v, want none", p.Value)
		}
	}

	growth := []models.QuarterData{
		annual(2019, 10, 10, models.SourceRSBU),
		annual(2022, 10, 20, models.SourceRSBU),
	}
	cagr := lastNonNaN(DerivedSeries(growth, "dividends_cagr3", PeriodAnnual))
	// 10 → 20 over 3 years is 2^(1/3) - 1 ≈ 26.0%.
	if math.Abs(cagr-25.992) > 0.01 {
		t.Errorf("dividend CAGR = %v, want about 26", cagr)
	}
}

func TestMixedBalanceNote(t *testing.T) {
	f := models.Float
	h := []models.QuarterData{{
		Year: 2024, Quarter: "Q4", Company: "X", Source: models.SourceRSBU,
		Debt: f(10), DebtSource: models.SourceCSV, Cash: f(4), CashSource: models.SourceRSBU,
		Revenue: f(100), NetProfit: f(20),
	}}
	snap := BuildSnapshot(h)
	if !math.IsNaN(snap.NetDebt) || snap.BalanceNote != MixedBalanceNote {
		t.Errorf("net debt = %v, note = %q", snap.NetDebt, snap.BalanceNote)
	}
	cur := BuildCurrent(h, models.MarketQuote{
		Capitalization: 100,
		PriceDate:      time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	})
	if cur.BalanceNote != MixedBalanceNote {
		t.Errorf("current note = %q", cur.BalanceNote)
	}
}
