package moex

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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

// dividends_synthetic.json is hand-written in the ISS dividends.json shape
// (block "dividends", columns secid/isin/registryclosedate/value/currencyid),
// not a captured response: iss.moex.com was unreachable when it was added.
// Replace it with a real capture (curl .../iss/securities/SBER/dividends.json).
func TestParseDividends(t *testing.T) {
	body, err := os.ReadFile("testdata/dividends_synthetic.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	h, err := ParseDividends(body)
	if err != nil {
		t.Fatalf("ParseDividends: %v", err)
	}

	tests := []struct {
		year   int
		want   float64
		wantOK bool
	}{
		{2019, 0, false}, // before the history starts: unknown, not zero
		{2020, 10, true},
		{2021, 0, true},  // covered by the history, no payout: a real zero
		{2023, 10, true}, // two record dates summed
		{2024, 0, false}, // non-RUB payout: unknown
		{2025, 0, true},
	}
	for _, tt := range tests {
		got, ok := h.PerShare(tt.year)
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("PerShare(%d) = (%v, %v), want (%v, %v)", tt.year, got, ok, tt.want, tt.wantOK)
		}
	}

	empty, err := ParseDividends([]byte(`{"dividends":{"columns":["secid","isin","registryclosedate","value","currencyid"],"data":[]}}`))
	if err != nil {
		t.Fatalf("ParseDividends(empty): %v", err)
	}
	if _, ok := empty.PerShare(2023); ok {
		t.Error("empty history: PerShare should be unknown")
	}
}

func TestDividendsTotal(t *testing.T) {
	divs, err := os.ReadFile("testdata/dividends_synthetic.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	desc, err := os.ReadFile("testdata/lkoh_desc.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		switch r.URL.Path {
		case "/iss/securities/TEST/dividends.json":
			_, _ = w.Write(divs)
		case "/iss/securities/TEST.json":
			_, _ = w.Write(desc)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL = srv.URL
	c.Delay = 0
	ctx := context.Background()

	got, err := c.DividendsTotal(ctx, "test", 2023)
	if err != nil {
		t.Fatalf("DividendsTotal: %v", err)
	}
	const shares = 692865762
	want := 10.0 * shares / 1e9
	if got == nil || math.Abs(*got-want) > 1e-9 {
		t.Fatalf("DividendsTotal(2023) = %v, want %v", got, want)
	}
	if got, err := c.DividendsTotal(ctx, "TEST", 2021); err != nil || got == nil || *got != 0 {
		t.Errorf("DividendsTotal(2021) = %v, %v; want 0", got, err)
	}
	if got, err := c.DividendsTotal(ctx, "TEST", 2019); err != nil || got != nil {
		t.Errorf("DividendsTotal(2019) = %v, %v; want nil", got, err)
	}

	// Both endpoints are fetched once and then served from the client cache.
	for path, n := range hits {
		if n != 1 {
			t.Errorf("%s fetched %d times, want 1", path, n)
		}
	}
}

func TestParseDividendsEdgeCases(t *testing.T) {
	const cols = `"columns":["secid","isin","registryclosedate","value","currencyid"]`

	// A payout without an amount makes its year unknown, not zero; a year
	// after the last record is covered (a stopped payer shows 0).
	h, err := ParseDividends([]byte(`{"dividends":{` + cols + `,"data":[
		["T","X","2021-05-01",5,"RUB"],
		["T","X","2022-05-01",null,"RUB"]]}}`))
	if err != nil {
		t.Fatalf("ParseDividends: %v", err)
	}
	if _, ok := h.PerShare(2022); ok {
		t.Error("PerShare(2022) with a null amount should be unknown")
	}
	if v, ok := h.PerShare(2023); !ok || v != 0 {
		t.Errorf("PerShare(2023) = (%v, %v), want (0, true)", v, ok)
	}

	// A paginated response must fail instead of being read as complete.
	_, err = ParseDividends([]byte(`{"dividends":{` + cols + `,"data":[["T","X","2021-05-01",5,"RUB"]]},
		"dividends.cursor":{"columns":["INDEX","TOTAL","PAGESIZE"],"data":[[0,3,1]]}}`))
	if err == nil {
		t.Error("expected an error for a truncated (paginated) response")
	}
	_, err = ParseDividends([]byte(`{"dividends":{` + cols + `,"data":[["T","X","2021-05-01",5,"RUB"]]},
		"dividends.cursor":{"columns":["INDEX","TOTAL","PAGESIZE"],"data":[[0,1,100]]}}`))
	if err != nil {
		t.Errorf("complete response with cursor: unexpected error %v", err)
	}
}

func TestDividendsErrorIsCached(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL, c.Delay = srv.URL, 0
	for year := 2020; year < 2024; year++ {
		if _, err := c.DividendsTotal(context.Background(), "TEST", year); err == nil {
			t.Fatalf("year %d: expected error", year)
		}
	}
	if hits != 1 {
		t.Errorf("dividends endpoint hit %d times, want 1 (error cached)", hits)
	}
}
