package main

import (
	"bytes"
	"context"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/config"
	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/fetcher"
	"github.com/VxVxN/financialanalyzer/internal/handlers"
	"github.com/VxVxN/financialanalyzer/internal/models"
	"github.com/VxVxN/financialanalyzer/internal/ops"
)

// stubRepo satisfies handlers.Repository with empty results; the router tests
// only care whether a request reaches a handler, not what it returns.
type stubRepo struct{ writes int }

type stubJobs struct{}

func (stubJobs) StartFetch(fetcher.Request) error     { return nil }
func (stubJobs) FetchRunning() bool                   { return false }
func (stubJobs) StartRegistry(string) error           { return nil }
func (stubJobs) RegistrySnapshot() ops.RegistryStatus { return ops.RegistryStatus{} }

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
	{http.MethodPut, "/api/manual-financials", `{"company":"SBER","year":2025,"dividends":800}`},
	{http.MethodDelete, "/api/manual-financials?company=SBER&year=2025", ""},
}

var startRoutes = []routeCase{
	{http.MethodPost, "/api/fetch", `{}`},
	{http.MethodPost, "/api/registry", `{}`},
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
	c := handlers.NewController(repo, slog.New(slog.DiscardHandler))
	c.SetJobs(stubJobs{})
	return newRouter(cfg, c), repo
}

func TestRouterWriteEndpointsRequireAuth(t *testing.T) {
	h, repo := newTestRouter(&config.Config{AuthUser: "admin", AuthPassword: "s3cret"})

	for _, rc := range append(writeRoutes, startRoutes...) {
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
	for _, rc := range startRoutes {
		if rec := serve(h, rc, true); rec.Code != http.StatusAccepted {
			t.Errorf("%s %s with auth: status = %d, want 202 (%s)", rc.method, rc.target, rec.Code, rec.Body)
		}
	}
	if repo.writes != len(writeRoutes) {
		t.Errorf("writes = %d, want %d", repo.writes, len(writeRoutes))
	}
}

func TestRouterReadEndpointsStayOpen(t *testing.T) {
	h, _ := newTestRouter(&config.Config{AuthUser: "admin", AuthPassword: "s3cret"})
	for _, target := range []string{"/", "/compare", "/static/app.css", "/static/app.js", "/healthz", "/api/companies", "/api/categories", "/api/company-note?company=SBER", "/updates", "/api/fetch-runs", "/api/registry", "/api/manual-financials?company=SBER"} {
		rec := serve(h, routeCase{method: http.MethodGet, target: target}, false)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", target, rec.Code)
		}
	}
}

func TestRouterScreenerMovedHome(t *testing.T) {
	h, _ := newTestRouter(&config.Config{})
	rec := serve(h, routeCase{method: http.MethodGet, target: "/screener"}, false)
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/" {
		t.Errorf("GET /screener: status %d, location %q; want 301 to /", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRouterAuthDisabled(t *testing.T) {
	h, repo := newTestRouter(&config.Config{})
	for _, rc := range writeRoutes {
		if rec := serve(h, rc, false); rec.Code != http.StatusOK {
			t.Errorf("%s %s: status = %d, want 200 when auth is disabled", rc.method, rc.target, rec.Code)
		}
	}
	for _, rc := range startRoutes {
		if rec := serve(h, rc, false); rec.Code != http.StatusAccepted {
			t.Errorf("%s %s: status = %d, want 202 when auth is disabled", rc.method, rc.target, rec.Code)
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

func TestRouterImportRequiresAuth(t *testing.T) {
	h, repo := newTestRouter(&config.Config{AuthUser: "admin", AuthPassword: "s3cret"})
	if rec := serveImport(h, false); rec.Code != http.StatusUnauthorized {
		t.Errorf("without auth: %d", rec.Code)
	}
	if repo.writes != 0 {
		t.Fatalf("unauthenticated import wrote %d times", repo.writes)
	}
	if rec := serveImport(h, true); rec.Code != http.StatusOK {
		t.Errorf("with auth: %d %s", rec.Code, rec.Body)
	}
	if repo.writes == 0 {
		t.Error("authenticated import did not write")
	}

	h, repo = newTestRouter(&config.Config{})
	if rec := serveImport(h, false); rec.Code != http.StatusOK {
		t.Errorf("auth off: %d %s", rec.Code, rec.Body)
	}
}

func TestRouterImportManualRequiresAuth(t *testing.T) {
	h, repo := newTestRouter(&config.Config{AuthUser: "admin", AuthPassword: "s3cret"})
	if rec := serveManualImport(h, false); rec.Code != http.StatusUnauthorized {
		t.Errorf("without auth: %d", rec.Code)
	}
	if repo.writes != 0 {
		t.Fatalf("unauthenticated import wrote %d times", repo.writes)
	}
	if rec := serveManualImport(h, true); rec.Code != http.StatusOK {
		t.Errorf("with auth: %d %s", rec.Code, rec.Body)
	}
	if repo.writes == 0 {
		t.Error("authenticated import did not write")
	}
}

func serveManualImport(h http.Handler, auth bool) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "ifrs.csv")
	_, _ = fw.Write([]byte("company;year;revenue\nX5;2024;100\n"))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/import-manual", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if auth {
		req.SetBasicAuth("admin", "s3cret")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func serveImport(h http.Handler, auth bool) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "SBER_banks.csv")
	_, _ = fw.Write([]byte("Метрика;2023-Q1\nКапитализация;1 000\n"))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/import", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if auth {
		req.SetBasicAuth("admin", "s3cret")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
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

func (s *stubRepo) GetManualFinancials(context.Context, string) ([]models.ManualFinancials, error) {
	return nil, nil
}
func (s *stubRepo) SaveManualFinancials(context.Context, models.ManualFinancials) error {
	s.writes++
	return nil
}
func (s *stubRepo) DeleteManualFinancials(context.Context, string, int) error { s.writes++; return nil }
func (s *stubRepo) SaveQuarterData(context.Context, models.QuarterData) error {
	s.writes++
	return nil
}
