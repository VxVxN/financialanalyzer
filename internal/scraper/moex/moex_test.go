package moex

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseIssueSize(t *testing.T) {
	body, err := os.ReadFile("testdata/lkoh_desc.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := ParseIssueSize(body)
	if err != nil {
		t.Fatalf("ParseIssueSize: %v", err)
	}
	const want = 692865762
	if got != want {
		t.Errorf("ISSUESIZE = %v, want %v", got, want)
	}
}

func TestParseLastClose(t *testing.T) {
	body, err := os.ReadFile("testdata/lkoh_history.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := ParseLastClose(body)
	if err != nil {
		t.Fatalf("ParseLastClose: %v", err)
	}
	// Last (chronologically latest) CLOSE in the Dec-2024 window fixture.
	const want = 7235
	if got != want {
		t.Errorf("last close = %v, want %v", got, want)
	}
}

func TestParseLastCloseEmpty(t *testing.T) {
	// No history rows -> 0, no error (security not traded in the window).
	got, err := ParseLastClose([]byte(`{"history":{"columns":["CLOSE"],"data":[]}}`))
	if err != nil {
		t.Fatalf("ParseLastClose: %v", err)
	}
	if got != 0 {
		t.Errorf("empty history close = %v, want 0", got)
	}
}

// sber_history_recent.json / sber_desc.json are real ISS captures (2026-09-30):
// last close 274.65 on 2026-09-29, ISSUESIZE 21 586 948 000.
func TestLatestQuote(t *testing.T) {
	hist, err := os.ReadFile("testdata/sber_history_recent.json")
	if err != nil {
		t.Fatal(err)
	}
	desc, err := os.ReadFile("testdata/sber_desc.json")
	if err != nil {
		t.Fatal(err)
	}
	var gotFrom, gotTill string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/iss/securities/SBER.json":
			_, _ = w.Write(desc)
		case "/iss/history/engines/stock/markets/shares/boards/TQBR/securities/SBER.json":
			gotFrom, gotTill = r.URL.Query().Get("from"), r.URL.Query().Get("till")
			_, _ = w.Write(hist)
		case "/iss/statistics/engines/stock/splits/SBER.json":
			_, _ = w.Write([]byte(noSplits))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL, c.Delay = srv.URL, 0
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	q, err := c.LatestQuote(context.Background(), "sber", now)
	if err != nil {
		t.Fatalf("LatestQuote: %v", err)
	}
	if gotFrom != "2026-09-16" || gotTill != "2026-09-30" {
		t.Errorf("window = %s..%s, want 2026-09-16..2026-09-30", gotFrom, gotTill)
	}
	if q.Price != 274.65 || q.Date != "2026-09-29" {
		t.Errorf("quote = %+v, want 274.65 on 2026-09-29", q)
	}
	if want := 274.65 * 21586948000 / 1e9; math.Abs(q.Capitalization-want) > 1e-6 {
		t.Errorf("capitalization = %v, want %v", q.Capitalization, want)
	}
}

func TestLatestQuoteNoTrades(t *testing.T) {
	desc, err := os.ReadFile("testdata/sber_desc.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/iss/securities/SBER.json" {
			_, _ = w.Write(desc)
			return
		}
		_, _ = w.Write([]byte(`{"history":{"columns":["TRADEDATE","CLOSE"],"data":[]}}`))
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL, c.Delay = srv.URL, 0
	if _, err := c.LatestQuote(context.Background(), "SBER", time.Now()); err == nil {
		t.Error("expected an error when the window has no trades")
	}
}

const noSplits = `{"splits":{"columns":["tradedate","secid","before","after"],"data":[]}}`

// splits_t.json / splits_belu_empty.json are real ISS captures (2026-09-30):
// T split 1:10 with first post-split session 2026-04-17; MOEX lists nothing
// for BELU although it split 1:8 in August 2024.
func TestParseSplits(t *testing.T) {
	body, err := os.ReadFile("testdata/splits_t.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseSplits(body)
	if err != nil {
		t.Fatalf("ParseSplits: %v", err)
	}
	if len(got) != 1 || got[0] != (Split{TradeDate: "2026-04-17", Before: 1, After: 10}) {
		t.Errorf("splits = %+v", got)
	}
	empty, err := os.ReadFile("testdata/splits_belu_empty.json")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ParseSplits(empty); err != nil || len(got) != 0 {
		t.Errorf("empty splits = %v, %v", got, err)
	}
	if _, err := ParseSplits([]byte(`{"splits":{"columns":["tradedate","secid","before","after"],"data":[["2024-01-01","X",0,10]]}}`)); err == nil {
		t.Error("a zero ratio must be rejected")
	}
}

func TestSharesBefore(t *testing.T) {
	splits := []Split{
		{TradeDate: "2024-07-15", Before: 5000, After: 1}, // VTBR-style consolidation
		{TradeDate: "2026-04-17", Before: 1, After: 10},   // T-style split
	}
	const now = 1000.0
	tests := []struct {
		date string
		want float64
	}{
		{"2026-09-29", 1000},       // after both
		{"2026-04-17", 1000},       // first post-split session: already new shares
		{"2026-04-16", 100},        // day before the split
		{"2024-12-30", 100},        // between the two
		{"2023-12-29", 100 * 5000}, // before the consolidation too
	}
	for _, tt := range tests {
		if got := SharesBefore(now, splits, tt.date); got != tt.want {
			t.Errorf("SharesBefore(%s) = %v, want %v", tt.date, got, tt.want)
		}
	}
}

func TestParseSplitRegistry(t *testing.T) {
	reg, err := ParseSplitRegistry("# comment\nbelu 2024-08-22 1 8  # NovaBev 1:8\n\n")
	if err != nil {
		t.Fatalf("ParseSplitRegistry: %v", err)
	}
	if got := reg["BELU"]; len(got) != 1 || got[0] != (Split{TradeDate: "2024-08-22", Before: 1, After: 8}) {
		t.Errorf("BELU = %+v", got)
	}
	for _, bad := range []string{"X 2024-08-22 1", "X 22.08.2024 1 8", "X 2024-08-22 1 0"} {
		if _, err := ParseSplitRegistry(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

// TestCapitalizationAtUndoesSplits prices 2023 BELU (pre-split close 5347) on
// the pre-split share count: current ISSUESIZE 126.4M / 8 = 15.8M.
func TestCapitalizationAtUndoesSplits(t *testing.T) {
	desc := `{"description":{"columns":["name","title","value"],"data":[["ISSUESIZE","","126400000"]]}}`
	hist := `{"history":{"columns":["TRADEDATE","CLOSE"],"data":[["2023-12-28",5300],["2023-12-29",5347]]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/iss/securities/BELU.json":
			_, _ = w.Write([]byte(desc))
		case "/iss/statistics/engines/stock/splits/BELU.json":
			_, _ = w.Write([]byte(noSplits)) // MOEX misses this split
		default:
			_, _ = w.Write([]byte(hist))
		}
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL, c.Delay = srv.URL, 0
	c.ExtraSplits = map[string][]Split{"BELU": {{TradeDate: "2024-08-22", Before: 1, After: 8}}}
	got, err := c.CapitalizationAt(context.Background(), "BELU", 2023)
	if err != nil {
		t.Fatalf("CapitalizationAt: %v", err)
	}
	if want := 5347 * 15.8e6 / 1e9; math.Abs(got-want) > 1e-9 {
		t.Errorf("cap = %v, want %v (without the split: %v)", got, want, 5347*126.4e6/1e9)
	}
}

func splitsServer(t *testing.T, splitsJSON string, splitsStatus int, hits *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/iss/statistics/engines/stock/splits/"):
			*hits++
			if splitsStatus != http.StatusOK {
				http.Error(w, "boom", splitsStatus)
				return
			}
			_, _ = w.Write([]byte(splitsJSON))
		case strings.HasPrefix(r.URL.Path, "/iss/securities/"):
			_, _ = w.Write([]byte(`{"description":{"columns":["name","title","value"],"data":[["ISSUESIZE","","1000"]]}}`))
		default:
			_, _ = w.Write([]byte(`{"history":{"columns":["TRADEDATE","CLOSE"],"data":[["2026-09-29",10]]}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testClient(srv *httptest.Server) *Client {
	c := NewClient()
	c.BaseURL, c.Delay = srv.URL, 0
	c.Retry.Attempts = 1
	c.Now = func() time.Time { return time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC) }
	return c
}

func TestSplitsMergeRules(t *testing.T) {
	listed := `{"splits":{"columns":["tradedate","secid","before","after"],"data":[
		["2024-08-20","X",1,8],
		["2027-01-15","X",1,2]]}}`
	hits := 0
	c := testClient(splitsServer(t, listed, http.StatusOK, &hits))
	c.ExtraSplits = map[string][]Split{"X": {
		{TradeDate: "2024-08-22", Before: 1, After: 8}, // same event, 2 days apart: skipped
		{TradeDate: "2020-03-02", Before: 1, After: 5}, // unknown to MOEX: added
	}}
	got, err := c.Splits(context.Background(), "x")
	if err != nil {
		t.Fatalf("Splits: %v", err)
	}
	want := []Split{{"2024-08-20", 1, 8}, {"2020-03-02", 1, 5}} // 2027 split not effective yet
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("splits = %+v, want %+v", got, want)
	}
	if _, err := c.Splits(context.Background(), "X"); err != nil || hits != 1 {
		t.Errorf("second call: err=%v hits=%d, want cached", err, hits)
	}
}

func TestSplitsFailureCachedAndQuoteFallsBack(t *testing.T) {
	hits := 0
	c := testClient(splitsServer(t, "", http.StatusInternalServerError, &hits))
	ctx := context.Background()

	// Historical caps fail closed: an unadjusted cap could be off by the ratio.
	for year := 2021; year <= 2023; year++ {
		if _, err := c.CapitalizationAt(ctx, "X", year); err == nil {
			t.Fatalf("CapitalizationAt(%d): expected error without split data", year)
		}
	}
	if hits != 1 {
		t.Errorf("splits endpoint hit %d times, want 1 (failure cached)", hits)
	}
	// A current quote falls back to ISSUESIZE instead of being lost.
	q, err := c.LatestQuote(ctx, "X", c.Now())
	if err != nil || q.Capitalization != 10*1000/1e9 {
		t.Errorf("LatestQuote = %+v, %v; want cap from ISSUESIZE", q, err)
	}
}

func TestCapitalizationAtNeedsTradeDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"history":{"columns":["CLOSE"],"data":[[10]]}}`))
	}))
	defer srv.Close()
	c := testClient(srv)
	if _, err := c.CapitalizationAt(context.Background(), "X", 2023); err == nil {
		t.Error("a close without a trade date must be an error, not an unadjusted cap")
	}
}
