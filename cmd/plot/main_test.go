package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/config"
	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/handlers"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

// stubRepo satisfies handlers.Repository with empty results; the router tests
// only care whether a request reaches a handler, not what it returns.
type stubRepo struct{ writes int }

func (s *stubRepo) Ping(context.Context) error                        { return nil }
func (s *stubRepo) GetAllCompanies(context.Context) ([]string, error) { return nil, nil }
func (s *stubRepo) GetAllCategories(context.Context) ([]string, error) {
	return nil, nil
}
func (s *stubRepo) GetAllCompaniesWithCategories(context.Context) ([]database.CompanyWithCategory, error) {
	return nil, nil
}
func (s *stubRepo) GetCompaniesHistory(context.Context, []string) (map[string][]models.QuarterData, error) {
	return nil, nil
}
func (s *stubRepo) GetCompanyHistory(context.Context, string) ([]models.QuarterData, error) {
	return nil, nil
}
func (s *stubRepo) DeleteCompany(context.Context, string) error { s.writes++; return nil }
func (s *stubRepo) GetCompanyNote(context.Context, string) (string, error) {
	return "", nil
}
func (s *stubRepo) SaveCompanyNote(context.Context, string, string) error { s.writes++; return nil }
func (s *stubRepo) DeleteCompanyNote(context.Context, string) error       { s.writes++; return nil }

type routeCase struct {
	method, target, body string
}

var writeRoutes = []routeCase{
	{http.MethodDelete, "/api/companies", `{"company":"SBER"}`},
	{http.MethodPost, "/api/company-note", `{"company":"SBER","note":"x"}`},
	{http.MethodDelete, "/api/company-note?company=SBER", ""},
}

func serve(h http.Handler, rc routeCase, auth bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(rc.method, rc.target, strings.NewReader(rc.body))
	if rc.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.SetBasicAuth("admin", "s3cret")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newTestRouter(cfg *config.Config) (http.Handler, *stubRepo) {
	repo := &stubRepo{}
	return newRouter(cfg, handlers.NewController(repo, slog.New(slog.DiscardHandler))), repo
}

func TestRouterWriteEndpointsRequireAuth(t *testing.T) {
	h, repo := newTestRouter(&config.Config{AuthUser: "admin", AuthPassword: "s3cret"})

	for _, rc := range writeRoutes {
		if rec := serve(h, rc, false); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without auth: status = %d, want 401", rc.method, rc.target, rec.Code)
		}
	}
	if repo.writes != 0 {
		t.Fatalf("unauthenticated requests reached the repository %d times", repo.writes)
	}

	for _, rc := range writeRoutes {
		if rec := serve(h, rc, true); rec.Code != http.StatusOK {
			t.Errorf("%s %s with auth: status = %d, want 200 (%s)", rc.method, rc.target, rec.Code, rec.Body)
		}
	}
	if repo.writes != len(writeRoutes) {
		t.Errorf("writes = %d, want %d", repo.writes, len(writeRoutes))
	}
}

func TestRouterReadEndpointsStayOpen(t *testing.T) {
	h, _ := newTestRouter(&config.Config{AuthUser: "admin", AuthPassword: "s3cret"})
	for _, target := range []string{"/healthz", "/api/companies", "/api/categories", "/api/company-note?company=SBER", "/updates", "/api/fetch-runs"} {
		rec := serve(h, routeCase{method: http.MethodGet, target: target}, false)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", target, rec.Code)
		}
	}
}

func TestRouterAuthDisabled(t *testing.T) {
	h, repo := newTestRouter(&config.Config{})
	for _, rc := range writeRoutes {
		if rec := serve(h, rc, false); rec.Code != http.StatusOK {
			t.Errorf("%s %s: status = %d, want 200 when auth is disabled", rc.method, rc.target, rec.Code)
		}
	}
	if repo.writes != len(writeRoutes) {
		t.Errorf("writes = %d, want %d", repo.writes, len(writeRoutes))
	}

	// The CSRF guard applies even without auth.
	req := httptest.NewRequest(http.MethodPost, "/api/company-note", strings.NewReader(`{"company":"X","note":"y"}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain POST: status = %d, want 415", rec.Code)
	}
}

func (s *stubRepo) GetMarketQuote(context.Context, string) (models.MarketQuote, bool, error) {
	return models.MarketQuote{}, false, nil
}
func (s *stubRepo) GetMarketQuotes(context.Context) (map[string]models.MarketQuote, error) {
	return nil, nil
}
func (s *stubRepo) RecentFetchRuns(context.Context, int) ([]models.FetchRun, error) {
	return nil, nil
}

func TestSchedulerJobs(t *testing.T) {
	jobs, err := schedulerJobs(&config.Config{ScheduleQuotes: "07:30", ScheduleFinancials: "sat 04:00"})
	if err != nil || len(jobs) != 2 || jobs[0].Name() != "quotes" || jobs[0].Spec.String() != "07:30" ||
		jobs[1].Name() != "financials" || jobs[1].Spec.String() != "sat 04:00" {
		t.Errorf("jobs = %+v, %v", jobs, err)
	}
	jobs, err = schedulerJobs(&config.Config{ScheduleQuotes: "OFF", ScheduleFinancials: "sun 05:00"})
	if err != nil || len(jobs) != 1 || jobs[0].Name() != "financials" {
		t.Errorf("quotes off: jobs = %+v, %v", jobs, err)
	}
	if _, err := schedulerJobs(&config.Config{ScheduleQuotes: "7am", ScheduleFinancials: "off"}); err == nil ||
		!strings.Contains(err.Error(), "SCHEDULE_QUOTES") {
		t.Errorf("bad spec: err = %v, want one naming SCHEDULE_QUOTES", err)
	}
}
