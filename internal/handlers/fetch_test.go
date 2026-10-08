package handlers

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/fetcher"
	"github.com/VxVxN/financialanalyzer/internal/ifrs"
	"github.com/VxVxN/financialanalyzer/internal/ops"
	"github.com/go-chi/chi/v5"
)

type fakeJobs struct {
	mu            sync.Mutex
	fetches       []fetcher.Request
	fetchErr      error
	ifrsCompanies []string
	ifrsYears     []int
}

func (f *fakeJobs) StartFetch(req fetcher.Request) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fetchErr != nil {
		return f.fetchErr
	}
	f.fetches = append(f.fetches, req)
	return nil
}
func (f *fakeJobs) FetchRunning() bool { return false }

func (f *fakeJobs) StartIFRS(companies []string, years []int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fetchErr != nil {
		return f.fetchErr
	}
	f.ifrsCompanies = append([]string(nil), companies...)
	f.ifrsYears = append([]int(nil), years...)
	return nil
}

func (f *fakeJobs) IFRSStatus() ifrs.Status { return ifrs.Status{} }

func newJobsServer(j Jobs) *chi.Mux {
	c := NewController(&fakeRepo{}, slog.New(slog.DiscardHandler))
	c.SetJobs(j)
	r := chi.NewRouter()
	r.Post("/api/fetch", c.StartFetch)
	return r
}

func TestStartFetch(t *testing.T) {
	j := &fakeJobs{}
	rec := do(t, newJobsServer(j), http.MethodPost, "/api/fetch", `{"tickers":"NLMK\nOZON:retail","quotes_only":true,"force":true}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if len(j.fetches) != 1 || j.fetches[0].Tickers != "NLMK,OZON:retail" || !j.fetches[0].QuotesOnly || !j.fetches[0].Force {
		t.Errorf("req = %+v", j.fetches)
	}
	rec = do(t, newJobsServer(j), http.MethodPost, "/api/fetch", `{"backfill":true}`)
	if rec.Code != http.StatusAccepted || len(j.fetches) != 2 || !j.fetches[1].Backfill || j.fetches[1].Force {
		t.Errorf("backfill: %d %+v", rec.Code, j.fetches)
	}

	rec = do(t, newJobsServer(j), http.MethodPost, "/api/fetch", `{"tickers_file":"/etc/passwd"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: status = %d", rec.Code)
	}

	j.fetchErr = ops.ErrBusy
	rec = do(t, newJobsServer(j), http.MethodPost, "/api/fetch", `{}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already running") {
		t.Errorf("busy: %d %s", rec.Code, rec.Body)
	}

	c := NewController(&fakeRepo{}, slog.New(slog.DiscardHandler))
	r := chi.NewRouter()
	r.Post("/api/fetch", c.StartFetch)
	rec = do(t, r, http.MethodPost, "/api/fetch", `{}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no jobs: %d", rec.Code)
	}
}
