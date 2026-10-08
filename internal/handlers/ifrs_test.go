package handlers

import (
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/ops"
	"github.com/go-chi/chi/v5"
)

func TestStartIFRS(t *testing.T) {
	j := &fakeJobs{}
	repo := &fakeRepo{companies: []string{"X5", "BELU"}}
	c := NewController(repo, slog.New(slog.DiscardHandler))
	c.now = func() time.Time { return testNow }
	c.SetJobs(j)
	r := chi.NewRouter()
	r.Post("/api/fetch-ifrs", c.StartIFRS)
	r.Get("/api/fetch-ifrs", c.IFRSStatus)

	rec := do(t, r, http.MethodPost, "/api/fetch-ifrs", `{"tickers":"belu"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if len(j.ifrsCompanies) != 1 || j.ifrsCompanies[0] != "BELU" {
		t.Fatalf("companies = %v", j.ifrsCompanies)
	}
	if len(j.ifrsYears) != 5 || j.ifrsYears[0] != 2021 || j.ifrsYears[4] != 2025 {
		t.Fatalf("years = %v", j.ifrsYears)
	}

	rec = do(t, r, http.MethodPost, "/api/fetch-ifrs", `{}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("all companies: %d %s", rec.Code, rec.Body)
	}
	if j.ifrsCompanies != nil {
		t.Fatalf("empty ticker list must mean every company, got %v", j.ifrsCompanies)
	}

	rec = do(t, r, http.MethodPost, "/api/fetch-ifrs", `{"tickers":"NOPE"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown ticker: %d", rec.Code)
	}

	j.fetchErr = ops.ErrBusy
	rec = do(t, r, http.MethodPost, "/api/fetch-ifrs", `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("busy: %d %s", rec.Code, rec.Body)
	}
}
