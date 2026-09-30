// Package moex pulls per-security market capitalization from the Moscow
// Exchange ISS API (https://iss.moex.com), which is free and needs no auth.
//
// ISS has no "historical market cap" endpoint, so capitalization at a date is
// reconstructed as  close_price * shares_outstanding:
//
//   - shares outstanding (ISSUESIZE) comes from the security description
//     /iss/securities/{SECID}.json
//   - the closing price comes from the EOD history
//     /iss/history/engines/stock/markets/shares/boards/{board}/securities/{SECID}.json
//
// Values are returned in billions of RUB to match the rest of the codebase
// (the CSV/smart-lab convention). Shares count is the current ISSUESIZE, so
// caps for years with since-changed share counts are approximate.
//
// Dividends come from /iss/securities/{SECID}/dividends.json (per-share value
// by registry close date). A year's total is the sum of its record-date
// payouts times ISSUESIZE — the same current-share-count approximation.
package moex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/scraper/httpx"
)

const (
	BaseURL          = "https://iss.moex.com"
	DefaultBoard     = "TQBR"
	defaultUserAgent = "financialanalyzer-bot/1.0 (+github.com/VxVxN/financialanalyzer)"
	defaultDelay     = 500 * time.Millisecond
	defaultTimeout   = 20 * time.Second
)

type Client struct {
	BaseURL   string // ISS root; NewClient sets the package BaseURL (tests override it)
	HTTP      *http.Client
	UserAgent string
	Board     string
	Delay     time.Duration
	Retry     httpx.Policy // retries for transient failures (network, 429, 5xx)
	lastReq   time.Time

	// Per-secid caches: both values are requested once per ticker but used for
	// every year. A failed dividends lookup is cached too, so an outage costs
	// one retry cycle per ticker, not one per year. A Client is not safe for
	// concurrent use.
	issueSize    map[string]float64
	dividends    map[string]DividendHistory
	dividendsErr map[string]error
}

func NewClient() *Client {
	return &Client{
		BaseURL:   BaseURL,
		HTTP:      &http.Client{Timeout: defaultTimeout},
		UserAgent: defaultUserAgent,
		Board:     DefaultBoard,
		Delay:     defaultDelay,
		Retry:     httpx.DefaultPolicy,
	}
}

// CapitalizationAt returns the market capitalization of secid at the end of the
// given year, in billions of RUB. It takes the last available closing price in
// December of that year and multiplies by the current shares outstanding.
// Returns (0, nil) when the exchange has no December trades for that year
// (e.g. the security was not yet listed).
func (c *Client) CapitalizationAt(ctx context.Context, secid string, year int) (float64, error) {
	shares, err := c.IssueSize(ctx, secid)
	if err != nil {
		return 0, fmt.Errorf("issue size %s: %w", secid, err)
	}
	if shares == 0 {
		return 0, fmt.Errorf("issue size %s: zero", secid)
	}

	from := fmt.Sprintf("%04d-12-01", year)
	till := fmt.Sprintf("%04d-12-31", year)
	close, err := c.LastClose(ctx, secid, from, till)
	if err != nil {
		return 0, fmt.Errorf("close %s %d: %w", secid, year, err)
	}
	if close == 0 {
		return 0, nil
	}
	const rubPerBillion = 1e9
	return close * shares / rubPerBillion, nil
}

// IssueSize returns the number of shares outstanding for secid.
func (c *Client) IssueSize(ctx context.Context, secid string) (float64, error) {
	secid = strings.ToUpper(secid)
	if v, ok := c.issueSize[secid]; ok {
		return v, nil
	}
	url := fmt.Sprintf("%s/iss/securities/%s.json?iss.meta=off&iss.only=description", c.BaseURL, secid)
	body, err := c.get(ctx, url)
	if err != nil {
		return 0, err
	}
	v, err := ParseIssueSize(body)
	if err != nil {
		return 0, err
	}
	if c.issueSize == nil {
		c.issueSize = make(map[string]float64)
	}
	c.issueSize[secid] = v
	return v, nil
}

// DividendHistory is a security's per-share dividend record, bucketed by the
// calendar year of the registry close (record) date.
type DividendHistory struct {
	perShare  map[int]float64 // RUB per share, summed per year
	unknown   map[int]bool    // years with a non-RUB or unparseable payout
	firstYear int             // earliest record-date year; 0 = no records
}

// PerShare returns the RUB paid per share for record dates in year. ok is false
// when the figure is unknown: no history at all, a year before the history
// starts (MOEX coverage, not proof of no payout), or a payout that is non-RUB or
// has no parseable amount. A year the history covers without payouts returns
// (0, true) — a real zero, as trustworthy as ISS's completeness.
func (h DividendHistory) PerShare(year int) (float64, bool) {
	if h.firstYear == 0 || year < h.firstYear || h.unknown[year] {
		return 0, false
	}
	return h.perShare[year], true
}

// Dividends returns the dividend history of secid (cached per client).
func (c *Client) Dividends(ctx context.Context, secid string) (DividendHistory, error) {
	secid = strings.ToUpper(secid)
	if h, ok := c.dividends[secid]; ok {
		return h, nil
	}
	if err, ok := c.dividendsErr[secid]; ok {
		return DividendHistory{}, err
	}
	url := fmt.Sprintf("%s/iss/securities/%s/dividends.json?iss.meta=off", c.BaseURL, secid)
	body, err := c.get(ctx, url)
	if err == nil {
		var h DividendHistory
		if h, err = ParseDividends(body); err == nil {
			if c.dividends == nil {
				c.dividends = make(map[string]DividendHistory)
			}
			c.dividends[secid] = h
			return h, nil
		}
	}
	// A canceled context is not a property of the ticker; don't cache it.
	if ctx.Err() == nil {
		if c.dividendsErr == nil {
			c.dividendsErr = make(map[string]error)
		}
		c.dividendsErr[secid] = err
	}
	return DividendHistory{}, err
}

// DividendsTotal returns the dividends of secid with record dates in year, in
// billions of RUB (per-share sum x current ISSUESIZE). It returns nil when the
// year's figure is unknown (see DividendHistory.PerShare) and a pointer to 0
// for a year the history covers without payouts.
func (c *Client) DividendsTotal(ctx context.Context, secid string, year int) (*float64, error) {
	h, err := c.Dividends(ctx, secid)
	if err != nil {
		return nil, fmt.Errorf("dividends %s: %w", secid, err)
	}
	perShare, ok := h.PerShare(year)
	if !ok {
		return nil, nil
	}
	shares, err := c.IssueSize(ctx, secid)
	if err != nil {
		return nil, fmt.Errorf("issue size %s: %w", secid, err)
	}
	if shares == 0 {
		return nil, fmt.Errorf("issue size %s: zero", secid)
	}
	const rubPerBillion = 1e9
	total := perShare * shares / rubPerBillion
	return &total, nil
}

// LastClose returns the most recent closing price for secid in [from, till]
// (dates as "YYYY-MM-DD"). Returns 0 when there were no trades in the window.
func (c *Client) LastClose(ctx context.Context, secid, from, till string) (float64, error) {
	url := fmt.Sprintf("%s/iss/history/engines/stock/markets/shares/boards/%s/securities/%s.json"+
		"?iss.meta=off&iss.only=history&from=%s&till=%s",
		c.BaseURL, c.Board, strings.ToUpper(secid), from, till)
	body, err := c.get(ctx, url)
	if err != nil {
		return 0, err
	}
	return ParseLastClose(body)
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
		"User-Agent": {c.UserAgent},
		"Accept":     {"application/json"},
	}, c.Retry)
}

// ---- Parsing ----------------------------------------------------------------
//
// ISS JSON blocks share the shape {"<block>": {"columns": [...], "data": [[...]]}}.

type issBlock struct {
	Columns []string        `json:"columns"`
	Data    [][]interface{} `json:"data"`
}

func decodeBlock(body []byte, name string) (issBlock, error) {
	var wrap map[string]issBlock
	if err := json.Unmarshal(body, &wrap); err != nil {
		return issBlock{}, fmt.Errorf("decode ISS json: %w", err)
	}
	b, ok := wrap[name]
	if !ok {
		return issBlock{}, fmt.Errorf("ISS block %q not found", name)
	}
	return b, nil
}

func (b issBlock) col(name string) int {
	for i, c := range b.Columns {
		if c == name {
			return i
		}
	}
	return -1
}

// ParseIssueSize extracts ISSUESIZE from a /iss/securities/{SECID}.json
// description block. The description is a vertical name/value table.
func ParseIssueSize(body []byte) (float64, error) {
	b, err := decodeBlock(body, "description")
	if err != nil {
		return 0, err
	}
	nameCol, valCol := b.col("name"), b.col("value")
	if nameCol < 0 || valCol < 0 {
		return 0, fmt.Errorf("description block missing name/value columns")
	}
	for _, row := range b.Data {
		if len(row) <= nameCol || len(row) <= valCol {
			continue
		}
		if asString(row[nameCol]) == "ISSUESIZE" {
			v, ok := asFloat(row[valCol])
			if !ok {
				return 0, fmt.Errorf("ISSUESIZE not numeric: %v", row[valCol])
			}
			return v, nil
		}
	}
	return 0, fmt.Errorf("ISSUESIZE not found in description")
}

// ParseDividends reads a /iss/securities/{SECID}/dividends.json "dividends"
// block (columns include registryclosedate "YYYY-MM-DD", value, currencyid).
func ParseDividends(body []byte) (DividendHistory, error) {
	b, err := decodeBlock(body, "dividends")
	if err != nil {
		return DividendHistory{}, err
	}
	dateCol, valCol, curCol := b.col("registryclosedate"), b.col("value"), b.col("currencyid")
	if dateCol < 0 || valCol < 0 {
		return DividendHistory{}, fmt.Errorf("dividends block missing registryclosedate/value columns")
	}
	if err := checkComplete(body, "dividends", len(b.Data)); err != nil {
		return DividendHistory{}, err
	}
	h := DividendHistory{perShare: map[int]float64{}, unknown: map[int]bool{}}
	for _, row := range b.Data {
		if len(row) <= dateCol || len(row) <= valCol {
			continue
		}
		date := asString(row[dateCol])
		if len(date) < 4 {
			continue
		}
		year, err := strconv.Atoi(date[:4])
		if err != nil {
			continue
		}
		if h.firstYear == 0 || year < h.firstYear {
			h.firstYear = year
		}
		// A payout without an amount (declared, not yet filled in) makes the
		// year unknown rather than silently zero.
		v, ok := asFloat(row[valCol])
		if !ok {
			h.unknown[year] = true
			continue
		}
		if curCol >= 0 && len(row) > curCol {
			if cur := asString(row[curCol]); cur != "" && cur != "RUB" && cur != "SUR" {
				h.unknown[year] = true
				continue
			}
		}
		h.perShare[year] += v
	}
	return h, nil
}

// checkComplete fails when an ISS "<block>.cursor" block reports more rows
// than were returned — a paginated response read as if it were complete would
// turn every later year into a false zero.
func checkComplete(body []byte, block string, got int) error {
	cur, err := decodeBlock(body, block+".cursor")
	if err != nil {
		return nil // no cursor block: the response is the whole list
	}
	col := cur.col("TOTAL")
	if col < 0 || len(cur.Data) == 0 || len(cur.Data[0]) <= col {
		return nil
	}
	if total, ok := asFloat(cur.Data[0][col]); ok && int(total) > got {
		return fmt.Errorf("%s: paginated response (%d of %d rows)", block, got, int(total))
	}
	return nil
}

// ParseLastClose returns the last non-empty CLOSE (or LEGALCLOSEPRICE) value in
// a history block, in chronological (API) order. Returns 0 when the block is
// empty (no trades in the requested window).
func ParseLastClose(body []byte) (float64, error) {
	b, err := decodeBlock(body, "history")
	if err != nil {
		return 0, err
	}
	closeCol := b.col("CLOSE")
	if closeCol < 0 {
		closeCol = b.col("LEGALCLOSEPRICE")
	}
	if closeCol < 0 {
		return 0, fmt.Errorf("history block missing CLOSE column")
	}
	var last float64
	for _, row := range b.Data {
		if len(row) <= closeCol {
			continue
		}
		if v, ok := asFloat(row[closeCol]); ok {
			last = v
		}
	}
	return last, nil
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func asFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		// Description-block values arrive as strings (e.g. ISSUESIZE).
		if n == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}
