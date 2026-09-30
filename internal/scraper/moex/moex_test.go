package moex

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
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
