// Package smartlab scrapes quarterly financials from smart-lab.ru.
//
// Target page: https://smart-lab.ru/q/{TICKER}/f/q/  (defaults to MSFO).
//
// The page contains a single <table class="simple-little-table financials">.
// Each metric row is <tr field="X">, where X is a stable identifier like
// "revenue", "ebitda", "p_e", "roe", "market_cap", "debt", "net_income".
// Column order matches the header row (tr.header_row) with quarter labels
// like "2024Q4", "2025Q1"; an LTM column is always last and is ignored.
package smartlab

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

const (
	BaseURL          = "https://smart-lab.ru"
	defaultUserAgent = "financialanalyzer-bot/1.0 (+github.com/VxVxN/financialanalyzer)"
	defaultDelay     = 1200 * time.Millisecond
	defaultTimeout   = 20 * time.Second
)

type Client struct {
	HTTP      *http.Client
	UserAgent string
	Delay     time.Duration
	lastReq   time.Time
}

func NewClient() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: defaultTimeout},
		UserAgent: defaultUserAgent,
		Delay:     defaultDelay,
	}
}

// FetchTicker downloads /q/{ticker}/f/q/ and returns one QuarterData per
// (year, quarter) row, with company set to ticker and category passed through.
// Values are stored verbatim as displayed on the page (e.g. revenue in
// billions of RUB), matching the existing CSV-import convention.
func (c *Client) FetchTicker(ctx context.Context, ticker, category string) ([]models.QuarterData, error) {
	url := fmt.Sprintf("%s/q/%s/f/q/", BaseURL, strings.ToUpper(ticker))
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	return Parse(body, ticker, category)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "ru,en;q=0.8")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// ---- Parsing ----------------------------------------------------------------

// fieldMap binds the smart-lab `field="..."` attribute to a setter on
// QuarterData. Only the fields the database tracks are mapped.
var fieldMap = map[string]func(*models.QuarterData, float64){
	"market_cap": func(d *models.QuarterData, v float64) { d.Capitalization = v },
	"revenue":    func(d *models.QuarterData, v float64) { d.Revenue = v },
	"net_income": func(d *models.QuarterData, v float64) { d.NetProfit = v },
	"ebitda":     func(d *models.QuarterData, v float64) { d.EBITDA = v },
	"debt":       func(d *models.QuarterData, v float64) { d.Debt = v },
	"p_e":        func(d *models.QuarterData, v float64) { d.PE = v },
	"roe":        func(d *models.QuarterData, v float64) { d.ROE = v },
}

var (
	headerRowRe = regexp.MustCompile(`(?s)<tr class="header_row">(.*?)</tr>`)
	quarterRe   = regexp.MustCompile(`<strong>(\d{4}Q[1-4])</strong>`)
	tdRe        = regexp.MustCompile(`(?s)<td([^>]*)>(.*?)</td>`)
	classAttrRe = regexp.MustCompile(`class="([^"]+)"`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

// Parse extracts QuarterData rows from a smart-lab quarterly-financials page.
// Exported so it can be tested against a saved HTML fixture.
func Parse(htmlBody []byte, ticker, category string) ([]models.QuarterData, error) {
	quarters, err := parseHeader(htmlBody)
	if err != nil {
		return nil, err
	}
	if len(quarters) == 0 {
		return nil, fmt.Errorf("no quarter columns found in header")
	}

	// One accumulator per quarter so multiple fields stack into the same row.
	rows := make(map[string]*models.QuarterData, len(quarters))
	for _, q := range quarters {
		year, qq, perr := parseQuarterLabel(q)
		if perr != nil {
			continue
		}
		rows[q] = &models.QuarterData{
			Year:     year,
			Quarter:  qq,
			Company:  strings.ToUpper(ticker),
			Category: category,
		}
	}

	for field, setter := range fieldMap {
		values := extractFieldValues(htmlBody, field)
		for i, v := range values {
			if i >= len(quarters) {
				break
			}
			if v == nil {
				continue
			}
			if r, ok := rows[quarters[i]]; ok {
				setter(r, *v)
			}
		}
	}

	out := make([]models.QuarterData, 0, len(rows))
	for _, q := range quarters {
		r, ok := rows[q]
		if !ok || r.IsEmpty() {
			continue
		}
		out = append(out, *r)
	}
	return out, nil
}

func parseHeader(body []byte) ([]string, error) {
	m := headerRowRe.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("header row not found")
	}
	matches := quarterRe.FindAllSubmatch(m[1], -1)
	out := make([]string, 0, len(matches))
	for _, mm := range matches {
		out = append(out, string(mm[1]))
	}
	return out, nil
}

// extractFieldValues returns the per-quarter values for a given smart-lab
// field, in header order. Length equals len(quarters); nil means the cell
// was empty / "—". The trailing LTM column is dropped.
func extractFieldValues(body []byte, field string) []*float64 {
	rowRe := regexp.MustCompile(`(?s)<tr field="` + regexp.QuoteMeta(field) + `">(.*?)</tr>`)
	m := rowRe.FindSubmatch(body)
	if m == nil {
		return nil
	}
	tds := tdRe.FindAllSubmatch(m[1], -1)

	var values []*float64
	for _, td := range tds {
		attrs := string(td[1])
		// Skip layout cells: the chart-icon column and the LTM spacer.
		if classMatch := classAttrRe.FindStringSubmatch(attrs); classMatch != nil {
			cls := classMatch[1]
			if strings.Contains(cls, "chartrow") || strings.Contains(cls, "ltm_spc") {
				continue
			}
		}
		raw := stripTags(string(td[2]))
		v, ok := parseNumber(raw)
		if !ok {
			values = append(values, nil)
			continue
		}
		values = append(values, &v)
	}
	// Drop the trailing LTM column.
	if len(values) > 0 {
		values = values[:len(values)-1]
	}
	return values
}

func stripTags(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(s)
}

// parseNumber accepts smart-lab cell text and returns the numeric value.
// "4 288"   -> 4288
// "166.0"   -> 166.0
// "12.4%"   -> 12.4
// "-1,5"    -> -1.5
// ""        -> false
// "—" / "-" -> false
func parseNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "—" || s == "-" || s == "&nbsp;" {
		return 0, false
	}
	// Russian non-breaking space U+00A0 sometimes used as thousands sep.
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "%", "")
	s = strings.ReplaceAll(s, ",", ".")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func parseQuarterLabel(label string) (int, string, error) {
	// Format: "2024Q4"
	if len(label) != 6 || label[4] != 'Q' {
		return 0, "", fmt.Errorf("invalid quarter label: %q", label)
	}
	year, err := strconv.Atoi(label[:4])
	if err != nil {
		return 0, "", fmt.Errorf("invalid year in %q: %w", label, err)
	}
	return year, label[4:], nil
}
