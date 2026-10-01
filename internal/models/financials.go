package models

import (
	"math"
	"time"
)

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
	// Equity is balance-sheet equity: RSBU line 1300 for companies, the capital
	// and financial-result accounts of CBR form 101 for banks.
	Equity *float64
	// Dividends is the year's total in billions of RUB, on the Q4 row only,
	// entered by hand through CSV (no free exchange API exists). Like
	// Capitalization it is a point-in-time value, not a quarterly flow.
	Dividends *float64
}

// IsEmpty reports whether the row carries no metric at all.
func (q *QuarterData) IsEmpty() bool {
	return q.Capitalization == nil && q.Revenue == nil &&
		q.NetProfit == nil && q.EBITDA == nil &&
		q.Debt == nil && q.PE == nil && q.ROE == nil &&
		q.Equity == nil && q.Dividends == nil
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

// MarketQuote is a company's latest exchange close and the market cap it
// implies (price x current shares outstanding), refreshed by cmd/fetch.
type MarketQuote struct {
	Company        string
	Price          float64   // RUB per share
	Capitalization float64   // billions of RUB
	PriceDate      time.Time // trade date of Price
}

// Fetch run kinds (fetch_runs.kind).
const (
	RunKindQuotes     = "quotes"     // only the latest exchange closes
	RunKindFinancials = "financials" // both pipelines, then quotes
)

// Fetch run triggers (fetch_runs.trigger).
const (
	TriggerCLI      = "cli"      // cmd/fetch
	TriggerSchedule = "schedule" // cmd/plot scheduler, at its slot
	TriggerCatchUp  = "catchup"  // cmd/plot scheduler, a missed slot on startup
)

// Moscow is the data-refresh schedule's time zone. Moscow has had no DST since
// 2014, so a fixed offset avoids depending on the tzdata database.
var Moscow = time.FixedZone("MSK", 3*60*60)

// ScheduledJob is one scheduler job as shown on the updates page. The slot is
// Moscow wall-clock time: daily, or weekly on Weekday.
type ScheduledJob struct {
	Name     string       `json:"name"`     // a RunKind*
	Schedule string       `json:"schedule"` // e.g. "07:00" or "sun 05:00"
	Weekly   bool         `json:"-"`
	Weekday  time.Weekday `json:"-"`
	Hour     int          `json:"-"`
	Minute   int          `json:"-"`
	Next     time.Time    `json:"next"`
	Running  bool         `json:"running"`
}

// Fetch run statuses (fetch_runs.status).
const (
	RunRunning   = "running"
	RunOK        = "ok"        // every company fetched or already up to date
	RunPartial   = "partial"   // finished, but some companies or quotes failed
	RunFailed    = "failed"    // the run as a whole could not proceed
	RunCanceled  = "canceled"  // stopped by shutdown
	RunAbandoned = "abandoned" // left "running" by a process that died
)

// FetchRun is one data refresh (a cmd/fetch run or a scheduled one in
// cmd/plot), as logged in fetch_runs.
type FetchRun struct {
	ID           int64      `json:"id"`
	Kind         string     `json:"kind"`    // fetcher.KindQuotes / KindFinancials
	Trigger      string     `json:"trigger"` // cli | schedule | catchup
	Scope        string     `json:"scope"`
	FullScope    bool       `json:"full_scope"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	Updated      int        `json:"updated"`
	UpToDate     int        `json:"up_to_date"`
	Rows         int        `json:"rows"`
	QuotesSaved  int        `json:"quotes_saved"`
	Failed       []string   `json:"failed"`
	QuotesFailed []string   `json:"quotes_failed"`
	// Error is the raw error chain of a failed run. It is kept for operators
	// (psql, logs) and never served over HTTP: it may hold database details.
	Error string `json:"-"`
}

// Duration is how long a finished run took (0 while running).
func (r FetchRun) Duration() time.Duration {
	if r.FinishedAt == nil {
		return 0
	}
	return r.FinishedAt.Sub(r.StartedAt)
}
