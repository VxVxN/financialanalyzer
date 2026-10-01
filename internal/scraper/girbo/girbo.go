// Package girbo pulls annual RSBU (РСБУ) financial statements from the Russian
// Federal Tax Service "ГИР БО" resource (https://bo.nalog.gov.ru).
//
// Important caveats about this source:
//
//   - It holds ANNUAL statements only — there is no quarterly breakdown. Each
//     report covers a full calendar year, so the caller maps a report to that
//     year's Q4.
//   - It is RSBU of the single legal entity, NOT consolidated IFRS/МСФО. For
//     holdings the parent-company revenue/profit differ substantially from the
//     group figures investors usually quote, so derived P/E and ROE will not
//     match smart-lab-style numbers. This is inherent to the source.
//   - Credit institutions (banks, e.g. SBER) report to the Central Bank, not
//     ГИР БО, so they are absent here.
//
// Request flow (all JSON, no auth):
//
//	GET /advanced-search/organizations/search?query={inn}   -> content[0].id (orgID)
//	GET /nbo/organizations/{orgID}/bfo                       -> [{period, id}, ...]
//	GET /nbo/bfo/{reportID}/details                          -> [{balance, financialResult}]
//
// All monetary values in the source are in thousands of RUB; this package
// converts them to billions of RUB to match the rest of the codebase.
package girbo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/scraper/httpx"
)

const (
	DefaultBaseURL   = "https://bo.nalog.gov.ru"
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/120.0 Safari/537.36"
	defaultDelay   = 700 * time.Millisecond
	defaultTimeout = 30 * time.Second

	// thousands of RUB -> billions of RUB.
	thousandToBillion = 1e6
)

type Client struct {
	BaseURL   string // DefaultBaseURL; overridable for tests
	HTTP      *http.Client
	UserAgent string
	Delay     time.Duration
	Retry     httpx.Policy // retries for transient failures (network, 429, 5xx)
	lastReq   time.Time
}

func NewClient() *Client {
	return &Client{
		BaseURL:   DefaultBaseURL,
		HTTP:      &http.Client{Timeout: defaultTimeout},
		UserAgent: defaultUserAgent,
		Delay:     defaultDelay,
		Retry:     httpx.DefaultPolicy,
	}
}

// AnnualReport is one year of parsed RSBU figures, in billions of RUB
// (Equity is also billions; it backs the ROE calculation done by the caller).
// A nil field means the line is absent from the filing; zero is a reported zero.
type AnnualReport struct {
	Year      int
	Revenue   *float64 // line 2110
	NetProfit *float64 // line 2400
	Equity    *float64 // line 1300 (capital and reserves)
	Debt      *float64 // lines 1410 + 1510 (long- + short-term borrowings)

	Cash              *float64 // line 1250 (cash and cash equivalents), year-end
	OperatingProfit   *float64 // line 2200 (profit from sales: revenue - costs), the EBIT proxy
	OperatingCashFlow *float64 // line 4100 (net cash flow from operations)
	Capex             *float64 // line 4221 (payments for non-current assets), positive
}

// PartialError is returned by FetchAnnual, together with the reports that did
// parse, when some years' detail calls failed: the caller keeps what it got but
// should not treat the company as complete.
type PartialError struct {
	Failed int   // years whose details could not be fetched or parsed
	Err    error // the last such failure
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("%d report(s) failed, last: %v", e.Failed, e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

// FetchAnnual resolves an INN to its organization, then returns one
// AnnualReport per published year, most recent first. Years whose detail
// payload is empty (e.g. simplified filings) are skipped, as are years for
// which skip (may be nil) returns true — their detail call is not made. When
// some years fail and others parse, the parsed ones come with a *PartialError;
// when every fetched year fails, the last error comes alone.
func (c *Client) FetchAnnual(ctx context.Context, inn string, skip func(year int) bool) ([]AnnualReport, error) {
	orgID, err := c.SearchByINN(ctx, inn)
	if err != nil {
		return nil, fmt.Errorf("search inn %s: %w", inn, err)
	}

	entries, err := c.ListReports(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("list reports org %d: %w", orgID, err)
	}

	out := make([]AnnualReport, 0, len(entries))
	var lastErr error
	failed := 0
	for _, e := range entries {
		if skip != nil && skip(e.Year) {
			continue
		}
		rep, ok, err := c.fetchReport(ctx, e)
		if err != nil {
			// One year's detail call failing (a transient HTTP error or an odd
			// payload) must not throw away the years that did parse. Honour
			// cancellation immediately, otherwise remember the error and move
			// on.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("report %d (%d): %w", e.ID, e.Year, err)
			failed++
			continue
		}
		if ok {
			out = append(out, rep)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Year > out[j].Year })
	switch {
	case lastErr == nil:
		return out, nil
	case len(out) == 0:
		return nil, lastErr
	default:
		return out, &PartialError{Failed: failed, Err: lastErr}
	}
}

func (c *Client) fetchReport(ctx context.Context, e BfoEntry) (AnnualReport, bool, error) {
	body, err := c.get(ctx, fmt.Sprintf("%s/nbo/bfo/%d/details", c.BaseURL, e.ID))
	if err != nil {
		return AnnualReport{}, false, err
	}
	rep, ok, err := parseDetails(body, e.Year)
	if err != nil {
		return AnnualReport{}, false, err
	}
	return rep, ok, nil
}

// SearchByINN returns the ГИР БО organization id for an exact INN match.
func (c *Client) SearchByINN(ctx context.Context, inn string) (int, error) {
	url := fmt.Sprintf("%s/advanced-search/organizations/search?query=%s&page=0", c.BaseURL, inn)
	body, err := c.get(ctx, url)
	if err != nil {
		return 0, err
	}
	return parseSearch(body, inn)
}

// ListReports returns the per-year report entries for an organization.
func (c *Client) ListReports(ctx context.Context, orgID int) ([]BfoEntry, error) {
	url := fmt.Sprintf("%s/nbo/organizations/%d/bfo", c.BaseURL, orgID)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	return parseReportList(body)
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	if d := time.Until(c.lastReq.Add(c.Delay)); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c.lastReq = time.Now()

	defer func() { c.lastReq = time.Now() }() // pace from the last attempt, retries included
	return httpx.Get(ctx, c.HTTP, url, http.Header{
		"User-Agent":      {c.UserAgent},
		"Accept":          {"application/json, text/plain, */*"},
		"Accept-Language": {"ru,en;q=0.8"},
		"Referer":         {c.BaseURL + "/"},
	}, c.Retry)
}

// ---- Parsing ----------------------------------------------------------------

// BfoEntry identifies one annual report of an organization.
type BfoEntry struct {
	Year int
	ID   int
}

// parseSearch returns the id of the content entry whose INN matches exactly.
func parseSearch(body []byte, inn string) (int, error) {
	var resp struct {
		Content []struct {
			ID  int    `json:"id"`
			INN string `json:"inn"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("decode search: %w", err)
	}
	for _, c := range resp.Content {
		// The API highlights the matched query, so an INN search returns the
		// inn field wrapped in <strong>…</strong>; strip tags before comparing.
		if stripTags(c.INN) == inn {
			return c.ID, nil
		}
	}
	if len(resp.Content) == 0 {
		return 0, fmt.Errorf("no organization for inn %s (banks file with the Central Bank, not ГИР БО)", inn)
	}
	return 0, fmt.Errorf("no exact inn match for %s among %d results", inn, len(resp.Content))
}

// parseReportList reads the [{period, id}, ...] array. Period is a year string.
func parseReportList(body []byte) ([]BfoEntry, error) {
	var raw []struct {
		Period string `json:"period"`
		ID     int    `json:"id"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode report list: %w", err)
	}
	out := make([]BfoEntry, 0, len(raw))
	for _, r := range raw {
		year, err := strconv.Atoi(r.Period)
		if err != nil || r.ID == 0 {
			continue
		}
		out = append(out, BfoEntry{Year: year, ID: r.ID})
	}
	return out, nil
}

// detailsItem mirrors the relevant parts of one /nbo/bfo/{id}/details element.
// All current* fields are in thousands of RUB.
type detailsItem struct {
	Balance struct {
		Current1250 *float64 `json:"current1250"` // cash and cash equivalents
		Current1300 *float64 `json:"current1300"` // capital and reserves (equity)
		Current1410 *float64 `json:"current1410"` // long-term borrowings
		Current1510 *float64 `json:"current1510"` // short-term borrowings
		Current1600 *float64 `json:"current1600"` // total assets: the balance was filed
	} `json:"balance"`
	FinancialResult struct {
		Current2110 *float64 `json:"current2110"` // revenue
		Current2200 *float64 `json:"current2200"` // profit (loss) from sales
		Current2400 *float64 `json:"current2400"` // net profit
	} `json:"financialResult"`
	FundsMovement struct {
		Current4100 *float64 `json:"current4100"` // net operating cash flow (signed)
		Current4221 *float64 `json:"current4221"` // capex outflow (filed as a positive amount)
	} `json:"fundsMovement"`
}

// parseDetails extracts the figures for one year. ok is false when the payload
// carries no usable financial result (revenue and profit both absent), which
// happens for simplified or empty filings.
func parseDetails(body []byte, year int) (AnnualReport, bool, error) {
	var items []detailsItem
	if err := json.Unmarshal(body, &items); err != nil {
		return AnnualReport{}, false, fmt.Errorf("decode details: %w", err)
	}
	if len(items) == 0 {
		return AnnualReport{}, false, nil
	}
	it := items[0]

	// An RSBU form omits the lines that are zero, so within a section that
	// was filed (its total is present) a missing line is a real zero: a
	// company without borrowings has debt 0, not "unknown". Without the
	// total the section is treated as absent and the lines stay nil.
	balance, funds := it.Balance.Current1600 != nil, it.FundsMovement.Current4100 != nil
	rep := AnnualReport{
		Year:              year,
		Revenue:           bln(it.FinancialResult.Current2110),
		NetProfit:         bln(it.FinancialResult.Current2400),
		Equity:            bln(it.Balance.Current1300),
		Debt:              zeroIf(balance, sumBln(it.Balance.Current1410, it.Balance.Current1510)),
		Cash:              zeroIf(balance, bln(it.Balance.Current1250)),
		OperatingProfit:   bln(it.FinancialResult.Current2200),
		OperatingCashFlow: bln(it.FundsMovement.Current4100),
		Capex:             zeroIf(funds, absBln(it.FundsMovement.Current4221)),
	}
	if nilOrZero(rep.Revenue) && nilOrZero(rep.NetProfit) {
		return AnnualReport{}, false, nil
	}
	return rep, true, nil
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

// stripTags removes the <strong> highlight tags the search API wraps around
// matched query text.
func stripTags(s string) string {
	return tagRe.ReplaceAllString(s, "")
}

// bln converts a thousands-of-RUB pointer field to billions of RUB, keeping
// nil (line absent) distinct from a reported zero.
func bln(v *float64) *float64 {
	if v == nil {
		return nil
	}
	b := *v / thousandToBillion
	return &b
}

// sumBln adds thousands-of-RUB lines into billions; nil only if all are absent.
func sumBln(vs ...*float64) *float64 {
	var sum *float64
	for _, v := range vs {
		if b := bln(v); b != nil {
			if sum == nil {
				sum = new(float64)
			}
			*sum += *b
		}
	}
	return sum
}

// absBln is bln of the magnitude: outflow lines are filed as positive amounts,
// but a filer that typed the sign must not turn capex into an inflow.
func absBln(v *float64) *float64 {
	b := bln(v)
	if b != nil && *b < 0 {
		*b = -*b
	}
	return b
}

// zeroIf turns an absent line into a reported zero when its section was filed.
func zeroIf(filed bool, v *float64) *float64 {
	if v == nil && filed {
		return new(float64)
	}
	return v
}

func nilOrZero(v *float64) bool { return v == nil || *v == 0 }
