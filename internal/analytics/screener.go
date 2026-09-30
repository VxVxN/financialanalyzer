package analytics

import (
	"math"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// ScreenerRow is one company's line in the cross-company screener. Numeric
// fields are nil when unknown (JSON null) so the table can sort them last.
//
// Valuation (Capitalization, PE, PB, DivYield) is taken at the latest exchange
// close when a quote is stored (Current = true), otherwise from the latest
// stored period — the same figures the dashboard shows.
type ScreenerRow struct {
	Company    string   `json:"company"`
	Category   string   `json:"category"`
	Sources    []string `json:"sources"`
	Comparable bool     `json:"comparable"` // false when figures are standalone RSBU/CBR
	LastPeriod string   `json:"last_period"`
	Current    bool     `json:"current"`    // valuation uses a live quote
	PriceDate  string   `json:"price_date"` // quote date when Current
	// EarningsPeriod is the period P/E's earnings come from (TTM end for a
	// live quote, the stored P/E's period otherwise).
	EarningsPeriod string `json:"earnings_period"`
	// Stale: a live quote exists but the fundamentals are too old to price
	// (see MaxFundamentalAge), so the live multiples are null.
	Stale bool `json:"stale"`

	Capitalization *float64 `json:"capitalization"`
	PE             *float64 `json:"pe"`
	PB             *float64 `json:"pb"`
	DivYield       *float64 `json:"div_yield"`
	ROE            *float64 `json:"roe"`
	NetMargin      *float64 `json:"net_margin"`
	DebtEBITDA     *float64 `json:"debt_ebitda"`
	RevenueYoY     *float64 `json:"revenue_yoy"`
	NetProfitYoY   *float64 `json:"net_profit_yoy"`
	Score          int      `json:"score"`
	Anomalies      int      `json:"anomalies"` // data-quality flags over the history
}

// BuildScreenerRow summarizes one company; quote is nil when none is stored
// (callers drop quotes that fail QuoteIsFresh). A P/E of a loss-making period
// is null on both paths, so the column sorts and filters one way.
func BuildScreenerRow(history []models.QuarterData, quote *models.MarketQuote) ScreenerRow {
	snap := BuildSnapshot(history)
	sources := Sources(history)
	row := ScreenerRow{
		Company:      snap.Company,
		Category:     snap.Category,
		Sources:      sources,
		Comparable:   true,
		LastPeriod:   snap.LastLabel,
		ROE:          finite(snap.ROE),
		NetMargin:    finite(snap.NetMargin),
		DebtEBITDA:   finite(snap.DebtEBITDA),
		RevenueYoY:   finite(snap.RevenueYoY),
		NetProfitYoY: finite(snap.NetProfitYoY),
		Score:        snap.Score,
		Anomalies:    len(CheckHistory(history)),
	}
	for _, s := range sources {
		if !IsComparable(s) {
			row.Comparable = false
		}
	}

	if quote != nil && quote.Capitalization > 0 {
		cur := BuildCurrent(history, *quote)
		row.Current, row.PriceDate, row.Stale = true, cur.PriceDate, cur.Stale
		row.EarningsPeriod = cur.EarningsLabel
		row.Capitalization = finite(cur.Capitalization)
		row.PE, row.PB, row.DivYield = finite(cur.PE), finite(cur.PB), finite(cur.DivYield)
		return row
	}
	row.EarningsPeriod = snap.PELabel
	row.Capitalization = finite(snap.Capitalization)
	row.PE, row.PB, row.DivYield = positive(snap.PE), finite(snap.PB), finite(snap.DivYield)
	return row
}

// positive is finite for values that are only meaningful above zero (a
// negative stored P/E is a loss).
func positive(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	return finite(v)
}

// finite converts analytics' NaN/Inf "no data" into nil.
func finite(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}
