package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/fetcher"
	"github.com/VxVxN/financialanalyzer/internal/ops"
	"github.com/go-chi/chi/v5"
)

type fakeJobs struct {
	mu       sync.Mutex
	fetches  []fetcher.Request
	fetchErr error
	regs     []string
	regErr   error
	status   ops.RegistryStatus
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
func (f *fakeJobs) StartRegistry(tickers string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.regErr != nil {
		return f.regErr
	}
	f.regs = append(f.regs, tickers)
	return nil
}
func (f *fakeJobs) RegistrySnapshot() ops.RegistryStatus { return f.status }

func newJobsServer(j Jobs) *chi.Mux {
	c := NewController(&fakeRepo{}, slog.New(slog.DiscardHandler))
	c.SetJobs(j)
	r := chi.NewRouter()
	r.Post("/api/fetch", c.StartFetch)
	r.Post("/api/registry", c.StartRegistry)
	r.Get("/api/registry", c.RegistryAPI)
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

func TestStartRegistryAndStatus(t *testing.T) {
	now := testNow
	j := &fakeJobs{status: ops.RegistryStatus{Include: 2, Text: "NLMK 1 metals", GeneratedAt: &now}}
	rec := do(t, newJobsServer(j), http.MethodPost, "/api/registry", `{"tickers":"nlmk, phor"}`)
	if rec.Code != http.StatusAccepted || len(j.regs) != 1 || j.regs[0] != "nlmk,phor" {
		t.Errorf("start: %d %s regs=%v", rec.Code, rec.Body, j.regs)
	}
	rec = do(t, newJobsServer(j), http.MethodGet, "/api/registry", "")
	var st ops.RegistryStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Include != 2 || st.Text != "NLMK 1 metals" {
		t.Errorf("status = %+v", st)
	}

	j.regErr = ops.ErrBusy
	rec = do(t, newJobsServer(j), http.MethodPost, "/api/registry", `{}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("busy: %d", rec.Code)
	}
}
