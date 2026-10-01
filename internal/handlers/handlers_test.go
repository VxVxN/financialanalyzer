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
	"time"

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

	quotes map[string]models.MarketQuote
	runs   []models.FetchRun

	savedCompany string
	savedNote    string
	deleted      []string
}

func (f *fakeRepo) Ping(ctx context.Context) error { return f.pingErr }
func (f *fakeRepo) GetAllCompanies(_ context.Context) ([]string, error) {
	return f.companies, f.companiesErr
}
func (f *fakeRepo) GetAllCategories(_ context.Context) ([]string, error) { return f.categories, nil }
func (f *fakeRepo) GetAllCompaniesWithCategories(_ context.Context) ([]database.CompanyWithCategory, error) {
	return f.withCats, nil
}
func (f *fakeRepo) GetCompaniesHistory(_ context.Context, companies []string) (map[string][]models.QuarterData, error) {
	return f.history, f.historyErr
}
func (f *fakeRepo) GetCompanyHistory(_ context.Context, company string) ([]models.QuarterData, error) {
	return f.companyHist, nil
}
func (f *fakeRepo) DeleteCompany(_ context.Context, company string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, company)
	return nil
}
func (f *fakeRepo) GetCompanyNote(_ context.Context, company string) (string, error) {
	return f.note, f.noteErr
}
func (f *fakeRepo) SaveCompanyNote(_ context.Context, company, note string) error {
	f.savedCompany, f.savedNote = company, note
	return nil
}
func (f *fakeRepo) DeleteCompanyNote(_ context.Context, company string) error { return nil }
func (f *fakeRepo) GetMarketQuote(_ context.Context, company string) (models.MarketQuote, bool, error) {
	q, ok := f.quotes[company]
	return q, ok, nil
}
func (f *fakeRepo) GetMarketQuotes(_ context.Context) (map[string]models.MarketQuote, error) {
	return f.quotes, nil
}

func (f *fakeRepo) RecentFetchRuns(_ context.Context, limit int) ([]models.FetchRun, error) {
	if len(f.runs) > limit {
		return f.runs[:limit], nil
	}
	return f.runs, nil
}

// testNow pins the controller clock so quote freshness is deterministic.
var testNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func newTestServer(repo Repository) *chi.Mux {
	c := NewController(repo, slog.New(slog.DiscardHandler))
	c.now = func() time.Time { return testNow }
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
	r.Get("/company/{name}", c.DashboardHandler)
	r.Get("/screener", c.ScreenerHandler)
	r.Get("/api/screener", c.ScreenerAPI)
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
			xss: {{Year: 2023, Quarter: "Q1", Company: xss, Revenue: models.Float(100)}},
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
			"SBER": {{Year: 2023, Quarter: "Q1", Company: "SBER", Revenue: models.Float(100)}},
		},
	}
	r := newTestServer(repo)
	rec := do(t, r, http.MethodGet, "/chart/revenue?companies=SBER", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "exportTableCSV") || !strings.Contains(body, "Экспорт в CSV") {
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

// holdingRow mimics ПАО «КЦ ИКС 5» 2025 RSBU: net profit above revenue.
var holdingRow = models.QuarterData{Year: 2025, Quarter: "Q4", Company: "X5", Category: "retail",
	Source: models.SourceRSBU, Capitalization: models.Float(700), Revenue: models.Float(85.7), NetProfit: models.Float(124.5), PE: models.Float(5.6), ROE: models.Float(29.2)}

func TestChartShowsSourceAndFlags(t *testing.T) {
	repo := &fakeRepo{history: map[string][]models.QuarterData{
		"X5":   {holdingRow},
		"SBER": {{Year: 2025, Quarter: "Q4", Company: "SBER", Source: models.SourceSmartLab, Revenue: models.Float(100), NetProfit: models.Float(30), PE: models.Float(4)}},
	}}
	rec := do(t, newTestServer(repo), http.MethodGet, "/chart/pe?companies=X5,SBER", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<th>Источник</th>",
		`class="source warn"`, // X5: RSBU is not comparable
		"РСБУ (эмитент)",
		`<td class="source" `, // SBER: smart-lab is comparable, no warning
		`class="flag"`,        // X5 2025-Q4 P/E cell flagged
		"чистая прибыль больше выручки",
		`"triangle"`, // flagged point on the chart
		`const src = {['SBER']: 'smart-lab', ['X5']: decodeURIComponent('%D0%A0%D0%A1%D0%91%D0%A3 (%D1%8D%D0%BC%D0%B8%D1%82%D0%B5%D0%BD%D1%82)')}`, // tooltip source map
	} {
		if !strings.Contains(body, want) {
			t.Errorf("chart page missing %q", want)
		}
	}
	// The flag only concerns metrics derived from the anomaly's inputs.
	rec = do(t, newTestServer(repo), http.MethodGet, "/chart/debt?companies=X5", "")
	if strings.Contains(rec.Body.String(), `class="flag"`) {
		t.Error("debt chart must not flag the profit>revenue anomaly")
	}
}

func TestDashboardDataQuality(t *testing.T) {
	repo := &fakeRepo{companyHist: []models.QuarterData{holdingRow}}
	rec := do(t, newTestServer(repo), http.MethodGet, "/company/X5", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Источник: РСБУ (эмитент)", "Качество данных", "2025-Q4", "чистая прибыль больше выручки"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}

	clean := &fakeRepo{companyHist: []models.QuarterData{{Year: 2025, Quarter: "Q4", Company: "SBER",
		Source: models.SourceSmartLab, Revenue: models.Float(100), NetProfit: models.Float(30), PE: models.Float(4)}}}
	rec = do(t, newTestServer(clean), http.MethodGet, "/company/SBER", "")
	if strings.Contains(rec.Body.String(), "Качество данных") {
		t.Error("clean IFRS data must not show the data-quality warning")
	}
}

func TestJSStringMap(t *testing.T) {
	got := jsStringMap(map[string]string{`a'b\c"</script>`: "RSBU (issuer)", "B": "ГИР"})
	want := `{['B']: decodeURIComponent('%D0%93%D0%98%D0%A0'), ` +
		`[decodeURIComponent('a%27b%5Cc%22%3C/script%3E')]: 'RSBU (issuer)'}`
	if got != want {
		t.Errorf("jsStringMap = %s, want %s", got, want)
	}
	if strings.ContainsAny(got, "\"\\<") {
		t.Error("output must not contain double quotes, backslashes or '<'")
	}
}

func TestScreener(t *testing.T) {
	xss := `<script>alert(1)</script>`
	repo := &fakeRepo{
		companies: []string{"SBER", xss},
		history: map[string][]models.QuarterData{
			"SBER": {{Year: 2025, Quarter: "Q4", Company: "SBER", Category: "banks", Source: models.SourceCBR102,
				NetProfit: models.Float(1500), Capitalization: models.Float(6000), Equity: models.Float(7000)}},
			xss: {{Year: 2025, Quarter: "Q4", Company: xss, Revenue: models.Float(1)}},
		},
		quotes: map[string]models.MarketQuote{
			"SBER": {Company: "SBER", Capitalization: 5929, PriceDate: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)},
		},
	}
	r := newTestServer(repo)

	rec := do(t, r, http.MethodGet, "/api/screener", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("api status = %d", rec.Code)
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	var sber map[string]any
	for _, row := range rows {
		if row["company"] == "SBER" {
			sber = row
		}
	}
	if sber == nil || sber["current"] != true || sber["price_date"] != "2026-09-29" {
		t.Fatalf("SBER row = %v", sber)
	}
	if sber["ebitda"] != nil || sber["debt_ebitda"] != nil {
		t.Errorf("unknown metric should be null: %v", sber["debt_ebitda"])
	}

	page := do(t, r, http.MethodGet, "/screener", "")
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d", page.Code)
	}
	body := page.Body.String()
	if strings.Contains(body, xss) {
		t.Error("company name reached the page unescaped")
	}
	if !strings.Contains(body, "SBER") || !strings.Contains(body, `<option value="banks">`) {
		t.Error("page is missing rows or the category filter")
	}
}

func TestDashboardCurrentValuation(t *testing.T) {
	hist := []models.QuarterData{{Year: 2025, Quarter: "Q4", Company: "SBER", Source: models.SourceCBR102,
		NetProfit: models.Float(1500), Capitalization: models.Float(6000)}}
	withQuote := &fakeRepo{companyHist: hist, quotes: map[string]models.MarketQuote{
		"SBER": {Company: "SBER", Capitalization: 5929, PriceDate: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)},
	}}
	body := do(t, newTestServer(withQuote), http.MethodGet, "/company/SBER", "").Body.String()
	if !strings.Contains(body, "Текущая оценка · закрытие 2026-09-29") || !strings.Contains(body, "P/E (сейчас)") {
		t.Error("dashboard with a quote should show the current-valuation block")
	}

	noQuote := &fakeRepo{companyHist: hist}
	body = do(t, newTestServer(noQuote), http.MethodGet, "/company/SBER", "").Body.String()
	if strings.Contains(body, "Текущая оценка") {
		t.Error("dashboard without a quote must not show the current-valuation block")
	}
}

func TestStaleQuoteIsIgnored(t *testing.T) {
	hist := []models.QuarterData{{Year: 2025, Quarter: "Q4", Company: "SBER", Source: models.SourceCBR102,
		NetProfit: models.Float(1500), Capitalization: models.Float(6000), PE: models.Float(4)}}
	old := testNow.Add(-45 * 24 * time.Hour)
	repo := &fakeRepo{
		companies:   []string{"SBER"},
		companyHist: hist,
		history:     map[string][]models.QuarterData{"SBER": hist},
		quotes:      map[string]models.MarketQuote{"SBER": {Company: "SBER", Capitalization: 1, PriceDate: old}},
	}
	r := newTestServer(repo)

	body := do(t, r, http.MethodGet, "/company/SBER", "").Body.String()
	if strings.Contains(body, "P/E (сейчас)") || !strings.Contains(body, "слишком старая для оценки") {
		t.Error("dashboard should replace a stale quote's valuation with a note")
	}

	var rows []map[string]any
	if err := json.Unmarshal(do(t, r, http.MethodGet, "/api/screener", "").Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["current"] != false || rows[0]["pe"] != 4.0 || rows[0]["capitalization"] != 6000.0 {
		t.Errorf("screener row with stale quote = %v, want stored valuation", rows)
	}
}
