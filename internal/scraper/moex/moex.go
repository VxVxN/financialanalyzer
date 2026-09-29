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
package moex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	BaseURL          = "https://iss.moex.com"
	DefaultBoard     = "TQBR"
	defaultUserAgent = "financialanalyzer-bot/1.0 (+github.com/VxVxN/financialanalyzer)"
	defaultDelay     = 500 * time.Millisecond
	defaultTimeout   = 20 * time.Second
)

type Client struct {
	HTTP      *http.Client
	UserAgent string
	Board     string
	Delay     time.Duration
	lastReq   time.Time
}

func NewClient() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: defaultTimeout},
		UserAgent: defaultUserAgent,
		Board:     DefaultBoard,
		Delay:     defaultDelay,
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
	url := fmt.Sprintf("%s/iss/securities/%s.json?iss.meta=off&iss.only=description",
		BaseURL, strings.ToUpper(secid))
	body, err := c.get(ctx, url)
	if err != nil {
		return 0, err
	}
	return ParseIssueSize(body)
}

// LastClose returns the most recent closing price for secid in [from, till]
// (dates as "YYYY-MM-DD"). Returns 0 when there were no trades in the window.
func (c *Client) LastClose(ctx context.Context, secid, from, till string) (float64, error) {
	url := fmt.Sprintf("%s/iss/history/engines/stock/markets/shares/boards/%s/securities/%s.json"+
		"?iss.meta=off&iss.only=history&from=%s&till=%s",
		BaseURL, c.Board, strings.ToUpper(secid), from, till)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")

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
