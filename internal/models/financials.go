package models

import "math"

// Data sources, stored in company_financials.source. They tell the UI how far
// a row's figures can be trusted and compared with other companies.
const (
	SourceRSBU     = "rsbu"     // ГИР БО annual RSBU of the issuer (unconsolidated)
	SourceCBR102   = "cbr_102"  // CBR form 102/123 of the bank legal entity (RSBU)
	SourceCSV      = "csv"      // manual CSV import (usually group IFRS figures)
	SourceSmartLab = "smartlab" // smart-lab.ru aggregator (legacy rows; scraper removed)
)

// QuarterData is one company-period row. Source is row-level and "last writer
// wins": the upsert merges metrics column by column, so after two pipelines
// write the same period the label names the latest one while untouched columns
// may still hold the earlier source's values.
//
// Metric fields are pointers: nil means "not reported" (stored as NULL, and the
// upsert keeps whatever the column held), while a non-nil zero is a real zero
// (e.g. a company with no debt). The zero value is therefore safe — a loader
// that leaves a metric unset never overwrites existing data. Only the primary
// sources (ГИР БО, CBR) emit real zeros; the CSV parser treats 0 as "no data".
// A real zero does overwrite, under the same last-writer-wins rule as any
// other value (e.g. a holding's RSBU revenue of 0 replaces CSV group revenue).
type QuarterData struct {
	Year           int
	Quarter        string
	Company        string
	Category       string
	Source         string // one of the Source* constants; "" = unknown (legacy row)
	Capitalization *float64
	Revenue        *float64
	NetProfit      *float64
	EBITDA         *float64
	Debt           *float64
	PE             *float64
	ROE            *float64
}

// IsEmpty reports whether the row carries no metric at all.
func (q *QuarterData) IsEmpty() bool {
	return q.Capitalization == nil && q.Revenue == nil &&
		q.NetProfit == nil && q.EBITDA == nil &&
		q.Debt == nil && q.PE == nil && q.ROE == nil
}

// Float returns a pointer to v, for filling QuarterData metric fields.
func Float(v float64) *float64 { return &v }

// ValueOrNaN dereferences a metric, mapping "not reported" to NaN — the
// missing-value convention of the analytics package.
func ValueOrNaN(p *float64) float64 {
	if p == nil {
		return math.NaN()
	}
	return *p
}
