package models

// Data sources, stored in company_financials.source. They tell the UI how far
// a row's figures can be trusted and compared with other companies.
const (
	SourceRSBU     = "rsbu"     // ГИР БО annual RSBU of the issuer (unconsolidated)
	SourceCBR102   = "cbr_102"  // CBR form 102/123 of the bank legal entity (RSBU)
	SourceCSV      = "csv"      // manual CSV import (usually group IFRS figures)
	SourceSmartLab = "smartlab" // smart-lab.ru aggregator (group IFRS figures)
)

// QuarterData is one company-period row. Source is row-level and "last writer
// wins": the upsert merges metrics column by column, so after two pipelines
// write the same period the label names the latest one while untouched columns
// may still hold the earlier source's values.
type QuarterData struct {
	Year           int
	Quarter        string
	Company        string
	Category       string
	Source         string // one of the Source* constants; "" = unknown (legacy row)
	Capitalization float64
	Revenue        float64
	NetProfit      float64
	EBITDA         float64
	Debt           float64
	PE             float64
	ROE            float64
}

func (q *QuarterData) IsEmpty() bool {
	return q.Capitalization == 0 && q.Revenue == 0 &&
		q.NetProfit == 0 && q.EBITDA == 0 &&
		q.Debt == 0 && q.PE == 0 && q.ROE == 0
}
