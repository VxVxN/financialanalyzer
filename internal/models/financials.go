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
	SourceCSV      = "csv"      // legacy quarterly CSV import (usually group IFRS; the import was removed)
	SourceSmartLab = "smartlab" // smart-lab.ru aggregator (legacy rows; scraper removed)
	// SourceManual marks a row overlaid from manual_financials (figures typed
	// in on the dashboard, usually the group's IFRS annual report). It is
	// never stored in company_financials.
	SourceManual = "manual"
)

// QuarterData is one company-period row. Source is row-level and follows the
// flows ("last writer wins" only when the write carries flows). Debt and cash
// merge on their own and remember who wrote them (DebtSource, CashSource), so
// a CSV debt figure on an RSBU row does not pretend to share the parent's cash.
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

	// Cash is cash and equivalents at period end (RSBU line 1250), a stock
	// like Debt; net debt = Debt - Cash.
	Cash *float64
	// DebtSource and CashSource name the pipeline that wrote that column
	// (a Source* constant). Empty means the column was not written, or was
	// stored before sources were tracked: analytics then uses Source. When
	// the two disagree in kind (group IFRS versus standalone RSBU/CBR), net
	// debt, EV, EV/EBIT and P/FCF for that period are withheld.
	DebtSource string
	CashSource string
	// OperatingProfit (RSBU line 2200, profit from sales; the operating
	// profit row of a CSV), OperatingCashFlow (line 4100) and Capex (line
	// 4221, stored as a positive outflow) are flows like Revenue.
	OperatingProfit   *float64
	OperatingCashFlow *float64
	Capex             *float64

	// Quarterly is set only on a manual annual row that replaced a fetched
	// single-quarter Q4 row (CSV, CBR): that row, kept so trailing-twelve-
	// month windows of the next three quarters can still sum four quarters
	// across the year end. Never stored.
	Quarterly *QuarterData
}

// IsEmpty reports whether the row carries no metric at all.
func (q *QuarterData) IsEmpty() bool {
	return q.Capitalization == nil && q.Revenue == nil &&
		q.NetProfit == nil && q.EBITDA == nil &&
		q.Debt == nil && q.PE == nil && q.ROE == nil &&
		q.Equity == nil && q.Dividends == nil &&
		q.Cash == nil && q.OperatingProfit == nil &&
		q.OperatingCashFlow == nil && q.Capex == nil
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
// implies (price x shares outstanding), refreshed by the fetcher.
// Shares is the count behind Capitalization; 0 means a row stored before
// that count was kept.
type MarketQuote struct {
	Company        string
	Price          float64   // RUB per share
	Capitalization float64   // billions of RUB
	Shares         float64   // shares outstanding; 0 = not stored
	PriceDate      time.Time // trade date of Price
}

// Fetch run kinds (fetch_runs.kind).
const (
	RunKindQuotes     = "quotes"     // only the latest exchange closes
	RunKindFinancials = "financials" // both pipelines, then quotes
)

// Fetch run triggers (fetch_runs.trigger).
const (
	TriggerCLI      = "cli"      // /updates POST /api/fetch
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

// Russian labels of run kinds, triggers and statuses, shared by the updates
// page and the failure notifications.
var (
	runKindLabels = map[string]string{
		RunKindQuotes:     "Котировки",
		RunKindFinancials: "Отчётность и котировки",
	}
	runTriggerLabels = map[string]string{
		TriggerCLI:      "вручную",
		TriggerSchedule: "по расписанию",
		TriggerCatchUp:  "пропущенный запуск",
	}
	runStatusLabels = map[string]string{
		RunRunning:   "выполняется",
		RunOK:        "успешно",
		RunPartial:   "частично",
		RunFailed:    "ошибка",
		RunCanceled:  "прерван",
		RunAbandoned: "оборван",
	}
)

// RunKindLabel names a run kind in Russian (an unknown kind as is).
func RunKindLabel(kind string) string { return labelOr(runKindLabels, kind) }

// RunTriggerLabel names a run trigger in Russian (an unknown one as is).
func RunTriggerLabel(trigger string) string { return labelOr(runTriggerLabels, trigger) }

// RunStatusLabel names a run status in Russian (an unknown one as is).
func RunStatusLabel(status string) string { return labelOr(runStatusLabels, status) }

func labelOr(labels map[string]string, key string) string {
	if l, ok := labels[key]; ok {
		return l
	}
	return key
}

// FetchRun is one data refresh (a manual /updates run or a scheduled one in
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
