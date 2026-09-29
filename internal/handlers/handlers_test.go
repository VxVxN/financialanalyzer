package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

// fakeRepo is a configurable in-memory implementation of the Repository
// interface for handler tests.
type fakeRepo struct {
	pingErr      error
	companies    []string
	companiesErr error
	categories   []string
	withCats     []database.CompanyWithCategory
	history      map[string][]models.QuarterData
	historyErr   error
	companyHist  []models.QuarterData
	note         string
	noteErr      error
	deleteErr    error

	savedCompany string
	savedNote    string
	deleted      []string
}

func (f *fakeRepo) Ping(ctx context.Context) error { return f.pingErr }
func (f *fakeRepo) GetAllCompanies() ([]string, error) {
	return f.companies, f.companiesErr
}
func (f *fakeRepo) GetAllCategories() ([]string, error) { return f.categories, nil }
func (f *fakeRepo) GetAllCompaniesWithCategories() ([]database.CompanyWithCategory, error) {
	return f.withCats, nil
}
func (f *fakeRepo) GetCompaniesHistory(companies []string) (map[string][]models.QuarterData, error) {
	return f.history, f.historyErr
}
func (f *fakeRepo) GetCompanyHistory(company string) ([]models.QuarterData, error) {
	return f.companyHist, nil
}
func (f *fakeRepo) DeleteCompany(company string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, company)
	return nil
}
func (f *fakeRepo) GetCompanyNote(company string) (string, error) { return f.note, f.noteErr }
func (f *fakeRepo) SaveCompanyNote(company, note string) error {
	f.savedCompany, f.savedNote = company, note
	return nil
}
func (f *fakeRepo) DeleteCompanyNote(company string) error { return nil }

func newTestServer(repo Repository) *chi.Mux {
	c := NewController(repo, slog.New(slog.DiscardHandler))
	r := chi.NewRouter()
	r.Get("/healthz", c.Health)
	r.Get("/readyz", c.Ready)
	r.Get("/version", c.Version)
	r.Get("/", c.IndexHandler)
	r.Get("/api/companies", c.GetCompanies)
	r.Delete("/api/companies", c.DeleteCompany)
	r.Get("/api/categories", c.GetCategories)
	r.Get("/api/companies-with-categories", c.GetCompaniesWithCategories)
	r.Get("/chart/{metric}", c.ChartHandler)
	r.Get("/api/company-note", c.GetCompanyNote)
	r.Post("/api/company-note", c.SaveCompanyNote)
	r.Delete("/api/company-note", c.DeleteCompanyNote)
	return r
}

func do(t *testing.T, r http.Handler, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestHealth(t *testing.T) {
	r := newTestServer(&fakeRepo{})
	rec := do(t, r, http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if out["status"] != "ok" {
		t.Errorf("status = %q, want ok", out["status"])
	}
}

func TestReady(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		r := newTestServer(&fakeRepo{})
		rec := do(t, r, http.MethodGet, "/readyz", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
	t.Run("db down", func(t *testing.T) {
		r := newTestServer(&fakeRepo{pingErr: context.DeadlineExceeded})
		rec := do(t, r, http.MethodGet, "/readyz", "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
	})
}

func TestVersion(t *testing.T) {
	r := newTestServer(&fakeRepo{})
	rec := do(t, r, http.MethodGet, "/version", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := out["version"]; !ok {
		t.Errorf("missing version field in %v", out)
	}
}

func TestGetCompanies(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		r := newTestServer(&fakeRepo{companies: []string{"SBER", "LKOH"}})
		rec := do(t, r, http.MethodGet, "/api/companies", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var out []string
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if len(out) != 2 {
			t.Errorf("len = %d, want 2", len(out))
		}
	})
	t.Run("error", func(t *testing.T) {
		r := newTestServer(&fakeRepo{companiesErr: context.DeadlineExceeded})
		rec := do(t, r, http.MethodGet, "/api/companies", "")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"error"`) {
			t.Errorf("expected JSON error envelope, got %q", body)
		}
		// The internal cause must not leak to the client.
		if strings.Contains(body, context.DeadlineExceeded.Error()) {
			t.Errorf("internal error text leaked to client: %q", body)
		}
	})
}

func TestDeleteCompany(t *testing.T) {
	t.Run("empty name", func(t *testing.T) {
		r := newTestServer(&fakeRepo{})
		rec := do(t, r, http.MethodDelete, "/api/companies", `{"company":"  "}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("not found", func(t *testing.T) {
		r := newTestServer(&fakeRepo{deleteErr: fmt.Errorf("%w: X", database.ErrCompanyNotFound)})
		rec := do(t, r, http.MethodDelete, "/api/companies", `{"company":"X"}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
	t.Run("ok", func(t *testing.T) {
		repo := &fakeRepo{}
		r := newTestServer(repo)
		rec := do(t, r, http.MethodDelete, "/api/companies", `{"company":"SBER"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if len(repo.deleted) != 1 || repo.deleted[0] != "SBER" {
			t.Errorf("deleted = %v, want [SBER]", repo.deleted)
		}
	})
}

func TestSaveNote(t *testing.T) {
	t.Run("bad body", func(t *testing.T) {
		r := newTestServer(&fakeRepo{})
		rec := do(t, r, http.MethodPost, "/api/company-note", `not json`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("ok", func(t *testing.T) {
		repo := &fakeRepo{}
		r := newTestServer(repo)
		rec := do(t, r, http.MethodPost, "/api/company-note", `{"company":"SBER","note":"buy"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if repo.savedCompany != "SBER" || repo.savedNote != "buy" {
			t.Errorf("saved (%q,%q), want (SBER,buy)", repo.savedCompany, repo.savedNote)
		}
	})
}

func TestChartInvalidMetric(t *testing.T) {
	r := newTestServer(&fakeRepo{})
	rec := do(t, r, http.MethodGet, "/chart/not_a_metric", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestChartEscapesCompanyName(t *testing.T) {
	xss := `<script>alert(1)</script>`
	repo := &fakeRepo{
		history: map[string][]models.QuarterData{
			xss: {{Year: 2023, Quarter: "Q1", Company: xss, Revenue: 100}},
		},
	}
	r := newTestServer(repo)
	rec := do(t, r, http.MethodGet, "/chart/revenue?companies="+xss, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<script>alert(1)</script>") {
		t.Errorf("unescaped company name (XSS) found in response")
	}
}

func TestChartHasCSVExport(t *testing.T) {
	repo := &fakeRepo{
		history: map[string][]models.QuarterData{
			"SBER": {{Year: 2023, Quarter: "Q1", Company: "SBER", Revenue: 100}},
		},
	}
	r := newTestServer(repo)
	rec := do(t, r, http.MethodGet, "/chart/revenue?companies=SBER", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "exportTableCSV") || !strings.Contains(body, "Export CSV") {
		t.Errorf("chart page is missing the CSV export control")
	}
	if !strings.Contains(body, "revenue_export.csv") {
		t.Errorf("export filename not set from metric")
	}
}

func TestIndex(t *testing.T) {
	r := newTestServer(&fakeRepo{})
	rec := do(t, r, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Errorf("empty index body")
	}
}
