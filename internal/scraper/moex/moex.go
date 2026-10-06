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
// (the CSV/smart-lab convention). ISSUESIZE is only the current share count,
// so the count at a past date is rebuilt by undoing the splits since then:
// MOEX's split list (/iss/statistics/engines/stock/splits/{SECID}) plus
// Client.ExtraSplits for splits it misses. Additional issues and buybacks are
// not tracked, so caps across those remain approximate. A renamed security
// (TCSG -> T) keeps its old history under the old secid; Client.Predecessors
// lets CapitalizationAt price those years there, on the old secid's own
// (frozen) ISSUESIZE, which is the share count at the time of the rename.
package moex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

	// ExtraSplits adds splits MOEX's list lacks, keyed by upper-case secid
	// (the fetcher fills it from the bundled share_splits.txt).
	ExtraSplits map[string][]Split
	// Predecessors lists a secid's former secids, newest first (the fetcher
	// fills it from the bundled ticker_renames.txt). CapitalizationAt falls
	// back to them for years the current secid has no December close, each
	// priced on its own share count and splits.
	Predecessors map[string][]string
	// Logger receives warnings (a registry split MOEX now lists, a quote
	// priced without split data); nil discards them.
	Logger *slog.Logger
	// Now is the clock for dropping not-yet-effective splits; nil = time.Now.
	Now func() time.Time

	// Per-secid caches: ISSUESIZE and the split list are requested once per
	// ticker but used for every year. A Client is not safe for concurrent use.
	issueSize map[string]float64
	splits    map[string][]Split
	splitsErr map[string]error
}

// splitDedupWindow is how close an ExtraSplits entry may be to a split MOEX
// lists before both are taken to be the same event (MOEX may date it to the
// suspension start or the resumption, the registry to the other).
const splitDedupWindow = 31 * 24 * time.Hour

func (c *Client) logger() *slog.Logger {
	if c.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return c.Logger
}

func (c *Client) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

// Split is a share split or consolidation of one security: from TradeDate —
// the first session at post-split prices, "YYYY-MM-DD" — every Before old
// shares became After new ones (1:10 split: Before 1, After 10; 5000:1
// consolidation: Before 5000, After 1).
type Split struct {
	TradeDate string
	Before    float64
	After     float64
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
// (e.g. the security was not yet listed) under secid or any of its
// Predecessors.
func (c *Client) CapitalizationAt(ctx context.Context, secid string, year int) (float64, error) {
	cp, err := c.capitalizationAt(ctx, secid, year)
	if err != nil || cp != 0 {
		return cp, err
	}
	for _, old := range c.Predecessors[strings.ToUpper(secid)] {
		if cp, err = c.capitalizationAt(ctx, old, year); err != nil || cp != 0 {
			return cp, err
		}
	}
	return 0, nil
}

// capitalizationAt is CapitalizationAt for exactly secid, without fallbacks.
func (c *Client) capitalizationAt(ctx context.Context, secid string, year int) (float64, error) {
	from := fmt.Sprintf("%04d-12-01", year)
	till := fmt.Sprintf("%04d-12-31", year)
	body, err := c.history(ctx, secid, from, till)
	if err != nil {
		return 0, fmt.Errorf("close %s %d: %w", secid, year, err)
	}
	close, date, err := ParseLastCloseDated(body)
	if err != nil {
		return 0, fmt.Errorf("close %s %d: %w", secid, year, err)
	}
	if close == 0 {
		return 0, nil
	}
	if date == "" {
		// Without the date no split can be placed before or after the close.
		return 0, fmt.Errorf("close %s %d: no trade date", secid, year)
	}
	shares, err := c.SharesAt(ctx, secid, date)
	if err != nil {
		return 0, err
	}
	return close * shares / rubPerBillion, nil
}

const rubPerBillion = 1e9

// SharesAt returns the shares outstanding for a close on date ("YYYY-MM-DD"):
// the current ISSUESIZE with every split from a later trade date undone.
func (c *Client) SharesAt(ctx context.Context, secid, date string) (float64, error) {
	shares, err := c.IssueSize(ctx, secid)
	if err != nil {
		return 0, fmt.Errorf("issue size %s: %w", secid, err)
	}
	if shares == 0 {
		return 0, fmt.Errorf("issue size %s: zero", secid)
	}
	splits, err := c.Splits(ctx, secid)
	if err != nil {
		return 0, fmt.Errorf("splits %s: %w", secid, err)
	}
	return SharesBefore(shares, splits, date), nil
}

// SharesBefore undoes, from current shares, every split whose trade date is
// after date (a close on the trade date itself is already post-split).
func SharesBefore(current float64, splits []Split, date string) float64 {
	shares := current
	for _, sp := range splits {
		if sp.TradeDate > date { // ISO dates compare lexically
			shares = shares * sp.Before / sp.After
		}
	}
	return shares
}

// Splits returns secid's effective splits: MOEX's list merged with
// ExtraSplits. An extra entry within splitDedupWindow of a MOEX split is taken
// to be the same event and skipped (with a warning to remove the registry
// row), so it is never applied twice. Splits dated after today are dropped:
// ISSUESIZE only changes on the trade date, so undoing one early would skew
// every cap. The result — or a failure, except cancellation — is cached per
// client, so an outage costs one retry cycle per ticker.
func (c *Client) Splits(ctx context.Context, secid string) ([]Split, error) {
	secid = strings.ToUpper(secid)
	if v, ok := c.splits[secid]; ok {
		return v, nil
	}
	if err, ok := c.splitsErr[secid]; ok {
		return nil, err
	}
	splits, err := c.fetchSplits(ctx, secid)
	if err != nil {
		if ctx.Err() == nil {
			if c.splitsErr == nil {
				c.splitsErr = make(map[string]error)
			}
			c.splitsErr[secid] = err
		}
		return nil, err
	}
	if c.splits == nil {
		c.splits = make(map[string][]Split)
	}
	c.splits[secid] = splits
	return splits, nil
}

func (c *Client) fetchSplits(ctx context.Context, secid string) ([]Split, error) {
	url := fmt.Sprintf("%s/iss/statistics/engines/stock/splits/%s.json?iss.meta=off&iss.only=splits", c.BaseURL, secid)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	listed, err := ParseSplits(body)
	if err != nil {
		return nil, err
	}
	all := append([]Split(nil), listed...)
	for _, extra := range c.ExtraSplits[secid] {
		if dup, ok := nearSplit(listed, extra.TradeDate); ok {
			c.logger().Warn("share_splits.txt entry duplicates a split MOEX now lists; remove the registry row",
				"secid", secid, "registry", extra.TradeDate, "moex", dup.TradeDate)
			continue
		}
		all = append(all, extra)
	}
	today := c.now().Format(time.DateOnly)
	effective := all[:0]
	for _, sp := range all {
		if sp.TradeDate > today {
			c.logger().Warn("ignoring a split that is not effective yet", "secid", secid, "tradedate", sp.TradeDate)
			continue
		}
		effective = append(effective, sp)
	}
	return effective, nil
}

// nearSplit returns a split in list within splitDedupWindow of date.
func nearSplit(list []Split, date string) (Split, bool) {
	d, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return Split{}, false
	}
	for _, sp := range list {
		t, err := time.Parse(time.DateOnly, sp.TradeDate)
		if err != nil {
			continue
		}
		if diff := t.Sub(d); diff <= splitDedupWindow && diff >= -splitDedupWindow {
			return sp, true
		}
	}
	return Split{}, false
}

// ParseSplits reads a /iss/statistics/engines/stock/splits/{SECID}.json
// "splits" block (columns tradedate, secid, before, after).
func ParseSplits(body []byte) ([]Split, error) {
	b, err := decodeBlock(body, "splits")
	if err != nil {
		return nil, err
	}
	dateCol, beforeCol, afterCol := b.col("tradedate"), b.col("before"), b.col("after")
	if dateCol < 0 || beforeCol < 0 || afterCol < 0 {
		return nil, fmt.Errorf("splits block missing tradedate/before/after columns")
	}
	var out []Split
	for _, row := range b.Data {
		if len(row) <= dateCol || len(row) <= beforeCol || len(row) <= afterCol {
			return nil, fmt.Errorf("malformed split row %v", row)
		}
		before, ok1 := asFloat(row[beforeCol])
		after, ok2 := asFloat(row[afterCol])
		date := asString(row[dateCol])
		if _, err := time.Parse(time.DateOnly, date); err != nil || !ok1 || !ok2 || before <= 0 || after <= 0 {
			return nil, fmt.Errorf("malformed split row %v", row)
		}
		out = append(out, Split{TradeDate: date, Before: before, After: after})
	}
	return out, nil
}

// ParseSplitRegistry parses "SECID YYYY-MM-DD BEFORE AFTER" lines ("#" starts
// a comment) into ExtraSplits form.
func ParseSplitRegistry(text string) (map[string][]Split, error) {
	out := make(map[string][]Split)
	for i, line := range strings.Split(text, "\n") {
		if j := strings.IndexByte(line, '#'); j >= 0 {
			line = line[:j]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 4 {
			return nil, fmt.Errorf("line %d: want SECID DATE BEFORE AFTER, got %q", i+1, line)
		}
		if _, err := time.Parse(time.DateOnly, f[1]); err != nil {
			return nil, fmt.Errorf("line %d: bad date %q", i+1, f[1])
		}
		before, err1 := strconv.ParseFloat(f[2], 64)
		after, err2 := strconv.ParseFloat(f[3], 64)
		if err1 != nil || err2 != nil || before <= 0 || after <= 0 {
			return nil, fmt.Errorf("line %d: bad ratio %s:%s", i+1, f[2], f[3])
		}
		secid := strings.ToUpper(f[0])
		out[secid] = append(out[secid], Split{TradeDate: f[1], Before: before, After: after})
	}
	return out, nil
}

// ParseRenameRegistry parses "SECID OLD_SECID [OLDER_SECID...]" lines ("#"
// starts a comment) into Predecessors form.
func ParseRenameRegistry(text string) (map[string][]string, error) {
	out := make(map[string][]string)
	for i, line := range strings.Split(text, "\n") {
		if j := strings.IndexByte(line, '#'); j >= 0 {
			line = line[:j]
		}
		f := strings.Fields(strings.ToUpper(line))
		if len(f) == 0 {
			continue
		}
		if len(f) < 2 {
			return nil, fmt.Errorf("line %d: want SECID OLD_SECID..., got %q", i+1, line)
		}
		if _, dup := out[f[0]]; dup {
			return nil, fmt.Errorf("line %d: duplicate secid %s", i+1, f[0])
		}
		out[f[0]] = f[1:]
	}
	return out, nil
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

// quoteLookback is how far back LatestQuote searches for a trading day; it
// spans long holidays (the New Year break) and short trading suspensions.
const quoteLookback = 14 * 24 * time.Hour

// Quote is a security's latest close and the market cap it implies.
type Quote struct {
	Price          float64 // RUB per share
	Date           string  // trade date of Price, "YYYY-MM-DD"
	Capitalization float64 // Price x current ISSUESIZE, billions of RUB
}

// LatestQuote returns the last close of secid in the two weeks up to now. It
// uses the same EOD history endpoint as CapitalizationAt, so the price is the
// last completed session's close, not an intraday quote.
func (c *Client) LatestQuote(ctx context.Context, secid string, now time.Time) (Quote, error) {
	body, err := c.history(ctx, secid, now.Add(-quoteLookback).Format(time.DateOnly), now.Format(time.DateOnly))
	if err != nil {
		return Quote{}, err
	}
	price, date, err := ParseLastCloseDated(body)
	if err != nil {
		return Quote{}, err
	}
	if price == 0 || date == "" {
		return Quote{}, fmt.Errorf("no trades for %s in the last %d days", secid, int(quoteLookback.Hours()/24))
	}
	shares, err := c.SharesAt(ctx, secid, date)
	if err != nil {
		// A split between a close from the last two weeks and today is rare;
		// rather than lose the quote, price it on the current share count.
		c.logger().Warn("quote priced without split data", "secid", secid, "error", err)
		if shares, err = c.IssueSize(ctx, secid); err != nil {
			return Quote{}, fmt.Errorf("issue size %s: %w", secid, err)
		}
		if shares == 0 {
			return Quote{}, fmt.Errorf("issue size %s: zero", secid)
		}
	}
	return Quote{Price: price, Date: date, Capitalization: price * shares / rubPerBillion}, nil
}

// history fetches the EOD history block of secid for [from, till].
func (c *Client) history(ctx context.Context, secid, from, till string) ([]byte, error) {
	url := fmt.Sprintf("%s/iss/history/engines/stock/markets/shares/boards/%s/securities/%s.json"+
		"?iss.meta=off&iss.only=history&from=%s&till=%s",
		c.BaseURL, c.Board, strings.ToUpper(secid), from, till)
	return c.get(ctx, url)
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

// ParseLastClose returns the last non-empty CLOSE (or LEGALCLOSEPRICE) value in
// a history block, in chronological (API) order. Returns 0 when the block is
// empty (no trades in the requested window).
func ParseLastClose(body []byte) (float64, error) {
	v, _, err := ParseLastCloseDated(body)
	return v, err
}

// ParseLastCloseDated is ParseLastClose plus the TRADEDATE ("YYYY-MM-DD") of
// the close it returns ("" when the block is empty or has no date column).
func ParseLastCloseDated(body []byte) (float64, string, error) {
	b, err := decodeBlock(body, "history")
	if err != nil {
		return 0, "", err
	}
	closeCol := b.col("CLOSE")
	if closeCol < 0 {
		closeCol = b.col("LEGALCLOSEPRICE")
	}
	if closeCol < 0 {
		return 0, "", fmt.Errorf("history block missing CLOSE column")
	}
	dateCol := b.col("TRADEDATE")
	var (
		last float64
		date string
	)
	for _, row := range b.Data {
		if len(row) <= closeCol {
			continue
		}
		if v, ok := asFloat(row[closeCol]); ok {
			last = v
			if dateCol >= 0 && len(row) > dateCol {
				date = asString(row[dateCol])
			}
		}
	}
	return last, date, nil
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
