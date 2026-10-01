package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

func TestSaveManualFinancials(t *testing.T) {
	repo := &fakeRepo{companies: []string{"X5"}}
	srv := newTestServer(repo)

	rec := do(t, srv, http.MethodPut, "/api/manual-financials",
		`{"company":" X5 ","year":2025,"revenue":4000.5,"net_profit":-12,"dividends":0,"updated_at":"2001-01-01T00:00:00Z"}`)
	if rec.Code != http.StatusOK || len(repo.savedManual) != 1 {
		t.Fatalf("status %d (%s), saved %d", rec.Code, rec.Body, len(repo.savedManual))
	}
	got := repo.savedManual[0]
	if got.Company != "X5" || got.Year != 2025 || *got.Revenue != 4000.5 || *got.NetProfit != -12 ||
		*got.Dividends != 0 || got.EBITDA != nil || !got.UpdatedAt.IsZero() {
		t.Errorf("saved = %+v", got)
	}

	for _, tt := range []struct {
		body   string
		status int
		msg    string
	}{
		{`{"company":"X5","year":2025}`, 400, "at least one figure"},
		{`{"company":"X5","year":1989,"revenue":1}`, 400, "year must be in 1990..2026"},
		{`{"company":"X5","year":2027,"revenue":1}`, 400, "year must be"},
		{`{"company":"X5","year":2025,"capex":-5}`, 400, "capex must not be negative"},
		{`{"company":"","year":2025,"revenue":1}`, 400, "company name is required"},
		{`{"company":"X5","year":2025,"revenu":1}`, 400, "invalid request body"}, // typo
		{`{"company":"NOPE","year":2025,"revenue":1}`, 404, "company not found"},
		{`{"company":"X5","year":2025,"revenue":12345678901234}`, 400, "revenue is too large"},
		{`{"company":"X5","year":2025,"revenue":1}{"company":"X5"}`, 400, "invalid request body"},
	} {
		rec := do(t, srv, http.MethodPut, "/api/manual-financials", tt.body)
		if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.msg) {
			t.Errorf("%s: %d %s, want %d %q", tt.body, rec.Code, rec.Body, tt.status, tt.msg)
		}
	}
	if len(repo.savedManual) != 1 {
		t.Errorf("rejected entries were saved: %d saves", len(repo.savedManual))
	}
}

func TestGetAndDeleteManualFinancials(t *testing.T) {
	repo := &fakeRepo{manual: []models.ManualFinancials{{Company: "X5", Year: 2025, Revenue: models.Float(4000)}}}
	srv := newTestServer(repo)

	var resp ManualFinancialsResponse
	rec := do(t, srv, http.MethodGet, "/api/manual-financials?company=X5", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Entries) != 1 || *resp.Entries[0].Revenue != 4000 {
		t.Errorf("GET = %s (%v)", rec.Body, err)
	}
	empty := do(t, newTestServer(&fakeRepo{}), http.MethodGet, "/api/manual-financials?company=X5", "")
	if !strings.Contains(empty.Body.String(), `"entries":[]`) {
		t.Errorf("no entries: %s, want an empty array", empty.Body)
	}

	if rec := do(t, srv, http.MethodDelete, "/api/manual-financials?company=X5&year=2025", ""); rec.Code != 200 || repo.deletedYear != 2025 {
		t.Errorf("DELETE: %d, year %d", rec.Code, repo.deletedYear)
	}
	if rec := do(t, srv, http.MethodDelete, "/api/manual-financials?company=X5&year=abc", ""); rec.Code != 400 {
		t.Errorf("bad year: %d, want 400", rec.Code)
	}
	repo.manualErr = fmt.Errorf("%w: X5 2024", database.ErrManualNotFound)
	if rec := do(t, srv, http.MethodDelete, "/api/manual-financials?company=X5&year=2024", ""); rec.Code != 404 {
		t.Errorf("missing entry: %d, want 404", rec.Code)
	}
}

func TestDashboardManualBlock(t *testing.T) {
	hist := []models.QuarterData{{Year: 2025, Quarter: "Q4", Company: "X5", Source: models.SourceManual, Revenue: models.Float(4000)}}
	repo := &fakeRepo{companyHist: hist, manual: []models.ManualFinancials{
		{Company: "X5", Year: 2025, Revenue: models.Float(4000.5), Dividends: models.Float(0)},
	}}
	body := do(t, newTestServer(repo), http.MethodGet, "/company/X5", "").Body.String()
	for _, want := range []string{
		"Ручные данные</h2>",
		"<td>2025</td><td>4000.5</td>", // entered value
		"<td>0</td>",                   // a reported zero dividend, not "—"
		`onclick="editManual(2025)"`, `onclick="deleteManual(2025)"`,
		`id="manual-operating_cash_flow"`, `value="2025"`, // default year: last year (clock pinned to 2026)
		"Источник: ввод вручную",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard lacks %q", want)
		}
	}

	// A hostile company name stays inside its JSON string in the script.
	evil := `X</script><script>alert(1)</script>`
	repo.companyHist[0].Company = evil
	body = do(t, newTestServer(repo), http.MethodGet, "/company/"+strings.ReplaceAll(evil, "/", "%2F"), "").Body.String()
	if strings.Contains(body, "<script>alert(1)") {
		t.Error("company name breaks out of the manual-entry script")
	}
}

// Query text never reaches the dashboard's HTML raw: the theme is one of two
// values and the company is query-escaped in the chart URL.
func TestDashboardEscapesReflectedInput(t *testing.T) {
	repo := &fakeRepo{companyHist: []models.QuarterData{{Year: 2025, Quarter: "Q4", Company: "X", Revenue: models.Float(1)}}}
	body := do(t, newTestServer(repo), http.MethodGet, `/company/X?theme=%22%3E%3Cimg%20src=x%20onerror=alert(1)%3E`, "").Body.String()
	if strings.Contains(body, "onerror=alert(1)") {
		t.Error("theme parameter is reflected unescaped")
	}
	// The company name reaches the page only escaped (HTML) or as a JSON
	// literal (the inline script).
	xss := &fakeRepo{companyHist: []models.QuarterData{{Year: 2025, Quarter: "Q4", Company: `<b>"X`, Revenue: models.Float(1)}}}
	body = do(t, newTestServer(xss), http.MethodGet, "/company/%3Cb%3E%22X", "").Body.String()
	if strings.Contains(body, `<b>"X`) || !strings.Contains(body, `&lt;b&gt;&#34;X`) {
		t.Error("company name reached the card unescaped")
	}
}
