package analytics

import (
	"math"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// ScreenerRow is one company's line in the cross-company screener. Numeric
// fields are nil when unknown (JSON null) so the table can sort them last.
//
// Valuation (Capitalization, PE, PB, DivYield, EVEBIT, PFCF) is taken at the latest exchange
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

	Capitalization  *float64 `json:"capitalization"`
	PE              *float64 `json:"pe"`
	PB              *float64 `json:"pb"`
	DivYield        *float64 `json:"div_yield"`
	EVEBIT          *float64 `json:"ev_ebit"`
	PFCF            *float64 `json:"p_fcf"`
	ROE             *float64 `json:"roe"`
	NetMargin       *float64 `json:"net_margin"`
	OperatingMargin *float64 `json:"operating_margin"`
	DebtEBITDA      *float64 `json:"debt_ebitda"`
	RevenueYoY      *float64 `json:"revenue_yoy"`
	NetProfitYoY    *float64 `json:"net_profit_yoy"`
	// Payout is the year's dividends over that year's profit, percent, for an
	// annual RSBU or IFRS row. Nil for a bank, a quarterly row, or a loss.
	Payout *float64 `json:"payout"`
	// Dividend growth, percent per year, over the annual dividend series.
	DividendsCAGR3 *float64 `json:"dividends_cagr3"`
	DividendsCAGR5 *float64 `json:"dividends_cagr5"`
	// Score is nil for a bank: its scale is not the industrial one, so the
	// screener column does not sort the two together. The card still shows it.
	Score      *int `json:"score"`
	ScoreParts int  `json:"score_parts"` // components that had data
	ScoreScale int  `json:"score_scale"` // 5, or 3 on the bank scale
	Bank       bool `json:"bank"`
	Anomalies  int  `json:"anomalies"` // data-quality flags over the history
	// Turnover is the average daily exchange turnover, billions of RUB, from
	// the fresh quote. Nil when it was not measured.
	Turnover *float64 `json:"turnover,omitempty"`
	// Liquid is false only when Turnover is known and below MinDailyTurnover.
	// Unknown turnover does not hide the company.
	Liquid bool `json:"liquid"`
	// Reporting names what P/E rests on, or P/B when there is no P/E:
	// "МСФО группы", "РСБУ юрлица" or "формы ЦБ". Empty when neither has a basis.
	Reporting string `json:"reporting,omitempty"`
	// Portfolio marks a holding from the bundled portfolio list. BuildScreenerRow
	// leaves it false; the HTTP layer sets it.
	Portfolio bool `json:"portfolio,omitempty"`

	// *HistPct: the valuation's percentile within the company's own history
	// (HistoricalBand; low = cheap for P/E and P/B, high = cheap for yield).
	PEHistPct       *float64 `json:"pe_hist_pct"`
	PBHistPct       *float64 `json:"pb_hist_pct"`
	DivYieldHistPct *float64 `json:"div_yield_hist_pct"`
	// *Sector: median over the comparable other companies of the category
	// (ApplySectorMedians); *SectorPeers counts the companies it rests on.
	PESector            *float64 `json:"pe_sector"`
	PBSector            *float64 `json:"pb_sector"`
	DivYieldSector      *float64 `json:"div_yield_sector"`
	PESectorPeers       int      `json:"pe_sector_peers"`
	PBSectorPeers       int      `json:"pb_sector_peers"`
	DivYieldSectorPeers int      `json:"div_yield_sector_peers"`

	// *Current is CurrentAt for that metric: the footer median of the screener
	// uses the same freshness rule as the sector median. *Standalone is the
	// kind that median keeps P/E and P/B inside (yield ignores kind).
	PECurrent       bool `json:"pe_current"`
	PBCurrent       bool `json:"pb_current"`
	DivYieldCurrent bool `json:"div_yield_current"`
	PEStandalone    bool `json:"pe_standalone"`
	PBStandalone    bool `json:"pb_standalone"`

	// Basis holds, per band metric with a value, the period it rests on: the
	// fundamental priced at the quote, or the stored multiple's period. Its
	// Standalone picks the kind of the history band and of the sector peers.
	Basis map[string]Point `json:"-"`
	// Bands holds the HistoricalBand of each band metric that has one.
	Bands map[string]HistoryBand `json:"-"`
}

// setBands places the row's valuation within the company's history.
// reviews classify cap jumps; a nil list still cuts the band at every jump,
// because an unclassified 2× move is not a comparable cap series.
func (r *ScreenerRow) setBands(history []models.QuarterData, reviews []CapReview) {
	r.Bands = map[string]HistoryBand{}
	window := BandWindow(history, reviews)
	pct := func(metric string, v *float64) *float64 {
		basis, ok := r.Basis[metric]
		if v == nil || !ok {
			return nil
		}
		b, ok := historicalBand(history, metric, *v, basis.Standalone, window)
		if !ok {
			return nil
		}
		r.Bands[metric] = b
		return &b.Percentile
	}
	r.PEHistPct = pct("pe", r.PE)
	r.PBHistPct = pct("pb", r.PB)
	r.DivYieldHistPct = pct("div_yield", r.DivYield)
}

// setBasis records the period behind each band metric that has a value.
func (r *ScreenerRow) setBasis(pe, pb, divYield Point) {
	r.Basis = map[string]Point{}
	for m, p := range map[string]Point{"pe": pe, "pb": pb, "div_yield": divYield} {
		if v, _, _ := r.sectorFields(m); v != nil {
			r.Basis[m] = p
		}
	}
}

// BuildScreenerRow summarizes one company; quote is nil when none is stored
// (callers drop quotes that fail QuoteIsFresh). A P/E of a loss-making period
// is null on both paths, so the column sorts and filters one way. Sector
// medians need every row: ApplySectorMedians fills them afterwards.
// reviews may be nil; unreviewed cap jumps still limit the history band.
func BuildScreenerRow(history []models.QuarterData, quote *models.MarketQuote, reviews []CapReview) ScreenerRow {
	row := buildScreenerRow(history, quote)
	row.setBands(history, reviews)
	return row
}

func buildScreenerRow(history []models.QuarterData, quote *models.MarketQuote) ScreenerRow {
	snap := BuildSnapshot(history)
	sources := Sources(history)
	row := ScreenerRow{
		Company:         snap.Company,
		Category:        snap.Category,
		Sources:         sources,
		Comparable:      true,
		LastPeriod:      snap.LastLabel,
		ROE:             finite(snap.ROE),
		NetMargin:       finite(snap.NetMargin),
		OperatingMargin: finite(snap.OperatingMargin),
		DebtEBITDA:      finite(snap.DebtEBITDA),
		RevenueYoY:      finite(snap.RevenueYoY),
		NetProfitYoY:    finite(snap.NetProfitYoY),
		Payout:          finite(snap.Payout),
		DividendsCAGR3:  finite(snap.DividendsCAGR3),
		DividendsCAGR5:  finite(snap.DividendsCAGR5),
		Score:           industrialScore(snap),
		ScoreParts:      snap.ScoreParts,
		ScoreScale:      snap.ScoreScale(),
		Bank:            snap.Bank,
		Anomalies:       len(CheckHistory(history)),
		Liquid:          true,
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
		row.EVEBIT, row.PFCF = finite(cur.EVEBIT), finite(cur.PFCF)
		row.setBasis(cur.EarningsPoint, cur.EquityPoint, cur.DividendsPoint)
		row.setReporting()
		row.setLiquidity(quote.Turnover)
		return row
	}
	row.EarningsPeriod = snap.PELabel
	row.Capitalization = finite(snap.Capitalization)
	row.PE, row.PB, row.DivYield = positive(snap.PE), finite(snap.PB), finite(snap.DivYield)
	row.EVEBIT, row.PFCF = finite(snap.EVEBIT), finite(snap.PFCF)
	row.setBasis(snap.PEPoint, snap.PBPoint, snap.DivYieldPoint)
	row.setReporting()
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

// MinDailyTurnover is the floor for the cheap screens, billions of RUB:
// 10 million RUB of average daily exchange turnover. Below that a position
// is hard to buy without moving the price.
const MinDailyTurnover = 0.01

// ReportingLabel is the Russian name of the figures a multiple rests on,
// in the nominative ("МСФО группы", "РСБУ юрлица", "формы ЦБ").
// standalone is a legal-entity source (RSBU or CBR); bank selects the CBR wording.
func ReportingLabel(bank, standalone bool) string {
	if !standalone {
		return "МСФО группы"
	}
	if bank {
		return "формы ЦБ"
	}
	return "РСБУ юрлица"
}

// ReportingPhrase is ReportingLabel in the phrase "по …" (the CBR name takes
// the prepositional case).
func ReportingPhrase(bank, standalone bool) string {
	if bank && standalone {
		return "по формам ЦБ"
	}
	return "по " + ReportingLabel(bank, standalone)
}

// setReporting names the entity behind P/E, falling back to P/B.
func (r *ScreenerRow) setReporting() {
	for _, m := range []string{"pe", "pb"} {
		p, ok := r.Basis[m]
		if !ok {
			continue
		}
		r.Reporting = ReportingLabel(r.Bank, p.Standalone)
		return
	}
}

// setLiquidity copies a measured turnover and marks a thin book illiquid.
// No measurement leaves Liquid true.
func (r *ScreenerRow) setLiquidity(turnover *float64) {
	r.Liquid = true
	if turnover == nil {
		return
	}
	r.Turnover = turnover
	r.Liquid = *turnover >= MinDailyTurnover
}

// industrialScore is the screener column. A bank's score uses another scale,
// so it stays off this column (nil sorts apart from the industrial numbers).
func industrialScore(s Snapshot) *int {
	if s.Bank {
		return nil
	}
	score := s.Score
	return &score
}

// finite converts analytics' NaN/Inf "no data" into nil.
func finite(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}
