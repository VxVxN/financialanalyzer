package analytics

import (
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

func TestBuildScreenerRow(t *testing.T) {
	f := models.Float
	h := []models.QuarterData{
		{Year: 2024, Quarter: "Q4", Company: "R", Category: "retail", Source: models.SourceRSBU,
			Revenue: f(1000), NetProfit: f(50), Capitalization: f(400), Equity: f(200), PE: f(8), ROE: f(25)},
		{Year: 2025, Quarter: "Q4", Company: "R", Category: "retail", Source: models.SourceRSBU,
			Revenue: f(1200), NetProfit: f(100), Capitalization: f(500), Equity: f(250), PE: f(5), ROE: f(40)},
	}

	stored := BuildScreenerRow(h, nil)
	if stored.Current || stored.Company != "R" || stored.Category != "retail" || stored.LastPeriod != "2025-Q4" {
		t.Errorf("stored row header = %+v", stored)
	}
	if stored.PE == nil || *stored.PE != 5 || stored.PB == nil || *stored.PB != 2 {
		t.Errorf("stored P/E, P/B = %v, %v; want 5, 2", stored.PE, stored.PB)
	}
	if stored.Comparable {
		t.Error("RSBU-only company must not be comparable")
	}
	if stored.Reporting != "РСБУ юрлица" || !stored.Liquid {
		t.Errorf("reporting = %q, liquid = %v", stored.Reporting, stored.Liquid)
	}
	if stored.RevenueYoY == nil || !approx(*stored.RevenueYoY, 20, 1e-9) {
		t.Errorf("revenue YoY = %v, want 20", stored.RevenueYoY)
	}
	if stored.DivYield != nil || stored.DebtEBITDA != nil {
		t.Errorf("unknown metrics must be nil: div %v, debt/ebitda %v", stored.DivYield, stored.DebtEBITDA)
	}

	thin := 0.001 // 1 млн ₽ a day, under the 10 млн floor
	quote := &models.MarketQuote{Company: "R", Capitalization: 800, Turnover: &thin, PriceDate: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}
	live := BuildScreenerRow(h, quote)
	if !live.Current || live.PriceDate != "2026-09-29" {
		t.Errorf("live row = %+v", live)
	}
	if live.PE == nil || *live.PE != 8 || live.PB == nil || !approx(*live.PB, 3.2, 1e-9) || *live.Capitalization != 800 {
		t.Errorf("live P/E, P/B, cap = %v, %v, %v; want 8, 3.2, 800", live.PE, live.PB, live.Capitalization)
	}
	// ROE is a historical ratio, not re-priced.
	if live.ROE == nil || *live.ROE != 40 {
		t.Errorf("ROE = %v, want 40", live.ROE)
	}
	if live.Liquid || live.Turnover == nil || *live.Turnover != thin {
		t.Errorf("turnover = %v, liquid = %v", live.Turnover, live.Liquid)
	}
}
