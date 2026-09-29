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
	BaseURL          = "https://bo.nalog.gov.ru"
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/120.0 Safari/537.36"
	defaultDelay   = 700 * time.Millisecond
	defaultTimeout = 30 * time.Second

	// thousands of RUB -> billions of RUB.
	thousandToBillion = 1e6
)

type Client struct {
	HTTP      *http.Client
	UserAgent string
	Delay     time.Duration
	Retry     httpx.Policy // retries for transient failures (network, 429, 5xx)
	lastReq   time.Time
}

func NewClient() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: defaultTimeout},
		UserAgent: defaultUserAgent,
		Delay:     defaultDelay,
		Retry:     httpx.DefaultPolicy,
	}
}

// AnnualReport is one year of parsed RSBU figures, in billions of RUB
// (Equity is also billions; it backs the ROE calculation done by the caller).
type AnnualReport struct {
	Year      int
	Revenue   float64 // line 2110
	NetProfit float64 // line 2400
	Equity    float64 // line 1300 (capital and reserves)
	Debt      float64 // lines 1410 + 1510 (long- + short-term borrowings)
}

// FetchAnnual resolves an INN to its organization, then returns one
// AnnualReport per published year, most recent first. Years whose detail
// payload is empty (e.g. simplified filings) are skipped.
func (c *Client) FetchAnnual(ctx context.Context, inn string) ([]AnnualReport, error) {
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
	for _, e := range entries {
		rep, ok, err := c.fetchReport(ctx, e)
		if err != nil {
			// One year's detail call failing (a transient HTTP error or an odd
			// payload) must not throw away the years that did parse. Honour
			// cancellation immediately, otherwise remember the error and move
			// on; it is surfaced only if every report fails.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("report %d (%d): %w", e.ID, e.Year, err)
			continue
		}
		if ok {
			out = append(out, rep)
		}
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Year > out[j].Year })
	return out, nil
}

func (c *Client) fetchReport(ctx context.Context, e BfoEntry) (AnnualReport, bool, error) {
	body, err := c.get(ctx, fmt.Sprintf("%s/nbo/bfo/%d/details", BaseURL, e.ID))
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
	url := fmt.Sprintf("%s/advanced-search/organizations/search?query=%s&page=0", BaseURL, inn)
	body, err := c.get(ctx, url)
	if err != nil {
		return 0, err
	}
	return parseSearch(body, inn)
}

// ListReports returns the per-year report entries for an organization.
func (c *Client) ListReports(ctx context.Context, orgID int) ([]BfoEntry, error) {
	url := fmt.Sprintf("%s/nbo/organizations/%d/bfo", BaseURL, orgID)
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
		"Referer":         {BaseURL + "/"},
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
		Current1300 *float64 `json:"current1300"` // capital and reserves (equity)
		Current1410 *float64 `json:"current1410"` // long-term borrowings
		Current1510 *float64 `json:"current1510"` // short-term borrowings
	} `json:"balance"`
	FinancialResult struct {
		Current2110 *float64 `json:"current2110"` // revenue
		Current2400 *float64 `json:"current2400"` // net profit
	} `json:"financialResult"`
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

	rep := AnnualReport{
		Year:      year,
		Revenue:   bln(it.FinancialResult.Current2110),
		NetProfit: bln(it.FinancialResult.Current2400),
		Equity:    bln(it.Balance.Current1300),
		Debt:      bln(it.Balance.Current1410) + bln(it.Balance.Current1510),
	}
	if rep.Revenue == 0 && rep.NetProfit == 0 {
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

// bln converts a thousands-of-RUB pointer field to billions of RUB; nil -> 0.
func bln(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v / thousandToBillion
}
