package cbr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/scraper/httpx"
)

// testClient points a Client at srv with no rate limit and near-instant retries.
func testClient(srv *httptest.Server) *Client {
	c := NewClient()
	c.HTTP = srv.Client()
	c.BaseURL = srv.URL
	c.Delay = 0
	c.Retry = httpx.Policy{Attempts: 3, BaseDelay: time.Millisecond}
	return c
}

// TestFetchPeriodRetriesThenParses: a transient 503 from cbr.ru is retried and
// the archive served on the next attempt is parsed as usual.
func TestFetchPeriodRetriesThenParses(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "102-20240101.rar"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vfs/credit/forms/102-20240101.rar" {
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	figures, err := testClient(srv).FetchPeriod(context.Background(), "20240101")
	if err != nil {
		t.Fatalf("FetchPeriod: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", calls.Load())
	}
	if _, ok := figures[1481]; !ok {
		t.Error("Sberbank (REGN 1481) missing from parsed archive")
	}
}

// TestFetchPeriodNotPublished: a 404 maps to ErrNotPublished and is not retried.
func TestFetchPeriodNotPublished(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := testClient(srv).FetchPeriod(context.Background(), "20990101")
	if !errors.Is(err, ErrNotPublished) {
		t.Fatalf("err = %v, want ErrNotPublished", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (404 must not be retried)", calls.Load())
	}
}

// TestFetchEquityServesForm101: FetchEquity requests the form 101 archive for
// the date and parses its balance table.
func TestFetchEquityServesForm101(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "101-20240101.rar"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vfs/credit/forms/101-20240101.rar" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	c := testClient(srv)
	equity, err := c.FetchEquity(context.Background(), "20240101")
	if err != nil {
		t.Fatalf("FetchEquity: %v", err)
	}
	if _, ok := equity[1481]; !ok {
		t.Error("Sberbank (REGN 1481) missing from parsed equity")
	}
	if _, err := c.FetchEquity(context.Background(), "20990101"); !errors.Is(err, ErrNotPublished) {
		t.Errorf("unpublished date: err = %v, want ErrNotPublished", err)
	}
}
