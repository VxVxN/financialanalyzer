package models

import (
	"sort"
	"time"
)

// ManualFinancials is one company-year entered by hand, in billions of RUB;
// nil = not entered. It lives in its own table and is overlaid on the fetched
// history when read (ApplyManual), so no loader can overwrite it and deleting
// it brings the fetched figures back. Market data (capitalization) is never
// entered: it keeps coming from MOEX.
type ManualFinancials struct {
	Company string `json:"company"`
	Year    int    `json:"year"`

	// Annual flows.
	Revenue           *float64 `json:"revenue"`
	NetProfit         *float64 `json:"net_profit"`
	EBITDA            *float64 `json:"ebitda"`
	OperatingProfit   *float64 `json:"operating_profit"`
	OperatingCashFlow *float64 `json:"operating_cash_flow"`
	Capex             *float64 `json:"capex"` // positive outflow
	// Year-end stocks.
	Debt   *float64 `json:"debt"`
	Cash   *float64 `json:"cash"`
	Equity *float64 `json:"equity"`
	// The year's total dividends (record dates in the year), as in CSV.
	Dividends *float64 `json:"dividends"`

	UpdatedAt time.Time `json:"updated_at"`
}

// HasFundamentals reports whether the entry carries any reported figure
// besides dividends, i.e. whether it replaces the year's statements.
func (m ManualFinancials) HasFundamentals() bool {
	for _, v := range []*float64{m.Revenue, m.NetProfit, m.EBITDA, m.OperatingProfit, m.OperatingCashFlow,
		m.Capex, m.Debt, m.Cash, m.Equity} {
		if v != nil {
			return true
		}
	}
	return false
}

// IsEmpty reports whether nothing was entered.
func (m ManualFinancials) IsEmpty() bool {
	return !m.HasFundamentals() && m.Dividends == nil
}

// ApplyManual overlays one company's manual entries on its fetched history.
// Each entry is a full year and lands on that year's Q4 row:
//
//   - With any reported figure besides dividends, the entry replaces the
//     year's statements wholesale: the fetched flows and balance-sheet figures
//     are dropped (a parent's RSBU must not mix with the group's IFRS, nor a
//     single CSV quarter with an annual figure), capitalization stays (market
//     data), dividends stay unless entered, P/E and ROE are recomputed from
//     the entered profit and equity, and the row's source becomes "manual"
//     (an annual figure, comparable across companies).
//   - With dividends only, it just sets the row's dividends.
//
// A year with no fetched Q4 row gets a new one. The result is ordered by
// period; the input is not modified.
func ApplyManual(history []QuarterData, manual []ManualFinancials) []QuarterData {
	if len(manual) == 0 {
		return history
	}
	out := make([]QuarterData, len(history))
	copy(out, history)

	q4 := make(map[int]int, len(out)) // year -> index of its Q4 row
	category, company, source := "", "", ""
	for i, q := range out {
		if q.Quarter == "Q4" {
			q4[q.Year] = i
		}
		if category == "" {
			category = q.Category
		}
		company = q.Company
		if q.Source != "" {
			source = q.Source
		}
	}

	for _, m := range manual {
		if m.IsEmpty() {
			continue
		}
		i, ok := q4[m.Year]
		if !ok {
			name := company
			if name == "" {
				name = m.Company
			}
			// A row holding only entered dividends is not a manual statement:
			// it takes the company's usual source instead of claiming one.
			rowSource := SourceManual
			if !m.HasFundamentals() {
				rowSource = source
			}
			out = append(out, QuarterData{Year: m.Year, Quarter: "Q4", Company: name, Category: category, Source: rowSource})
			i = len(out) - 1
			q4[m.Year] = i
		}
		fetched := out[i]
		if !m.HasFundamentals() {
			out[i].Dividends = m.Dividends
			continue
		}
		row := QuarterData{
			Year: fetched.Year, Quarter: "Q4", Company: fetched.Company, Category: fetched.Category,
			Source:         SourceManual,
			Capitalization: fetched.Capitalization,
			Dividends:      fetched.Dividends,

			Revenue:           m.Revenue,
			NetProfit:         m.NetProfit,
			EBITDA:            m.EBITDA,
			OperatingProfit:   m.OperatingProfit,
			OperatingCashFlow: m.OperatingCashFlow,
			Capex:             m.Capex,
			Debt:              m.Debt,
			Cash:              m.Cash,
			Equity:            m.Equity,
		}
		if m.Dividends != nil {
			row.Dividends = m.Dividends
		}
		// A fetched single quarter stays reachable for trailing windows;
		// an annual RSBU row is not a quarter and is simply replaced.
		if ok && fetched.Source != SourceRSBU && fetched.Quarterly == nil {
			q := fetched
			row.Quarterly = &q
		}
		row.PE = PERatio(row.Capitalization, row.NetProfit)
		row.ROE = ROEPercent(row.NetProfit, row.Equity)
		out[i] = row
	}

	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Year != out[b].Year {
			return out[a].Year < out[b].Year
		}
		return out[a].Quarter < out[b].Quarter // "Q1" < "Q2" < "Q3" < "Q4"
	})
	return out
}

// PERatio returns capitalization / net profit, or nil when it is undefined
// (no cap, no profit figure, or non-positive earnings).
func PERatio(capitalization, netProfit *float64) *float64 {
	if capitalization == nil || netProfit == nil || *netProfit <= 0 {
		return nil
	}
	return Float(*capitalization / *netProfit)
}

// ROEPercent returns net profit / equity as a percentage, or nil when it is
// undefined (either figure missing, or non-positive equity).
func ROEPercent(netProfit, equity *float64) *float64 {
	if netProfit == nil || equity == nil || *equity <= 0 {
		return nil
	}
	return Float(*netProfit / *equity * 100)
}
