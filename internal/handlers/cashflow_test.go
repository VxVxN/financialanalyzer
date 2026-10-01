package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// cfRow is an annual RSBU row with the cash-flow lines: EV 1250, EBIT 100,
// FCF 90.
func cfRow() models.QuarterData {
	f := models.Float
	return models.QuarterData{Year: 2025, Quarter: "Q4", Company: "CF", Category: "test", Source: models.SourceRSBU,
		Capitalization: f(1000), Revenue: f(500), NetProfit: f(80), Debt: f(300), Cash: f(50),
		OperatingProfit: f(100), OperatingCashFlow: f(150), Capex: f(60)}
}

func TestCashFlowMetricsInUI(t *testing.T) {
	repo := &fakeRepo{
		companies:   []string{"CF"},
		companyHist: []models.QuarterData{cfRow()},
		history:     map[string][]models.QuarterData{"CF": {cfRow()}},
		quotes: map[string]models.MarketQuote{
			"CF": {Company: "CF", Capitalization: 1200, PriceDate: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)},
		},
	}
	srv := newTestServer(repo)

	for metric, want := range map[string]string{"ev_ebit": "12.50x", "p_fcf": "11.11x", "net_debt": "250.00 млрд", "fcf": "90.00 млрд", "operating_margin": "20.00%"} {
		rec := do(t, srv, http.MethodGet, "/chart/"+metric+"?companies=CF&period=annual", "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("/chart/%s: status %d, want a table cell %q", metric, rec.Code, want)
		}
	}

	body := do(t, srv, http.MethodGet, "/company/CF", "").Body.String()
	for _, want := range []string{"EV/EBIT", "12.50", "P/FCF", "11.11", "Чистый долг", "250.00 млрд", "FCF (LTM)",
		"EV/EBIT (сейчас)", "14.50", "EBIT LTM на 2025-Q4", "selectMetric('ev_ebit'"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard lacks %q", want)
		}
	}

	var rows []map[string]any
	if err := json.Unmarshal(do(t, srv, http.MethodGet, "/api/screener", "").Body.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("screener: %v %v", rows, err)
	}
	if rows[0]["ev_ebit"] != 14.5 || rows[0]["operating_margin"] != 20.0 {
		t.Errorf("screener row = %v, want current EV/EBIT 14.5 and operating margin 20", rows[0])
	}

	index := do(t, srv, http.MethodGet, "/", "").Body.String()
	for _, id := range []string{`"ev_ebit"`, `"p_fcf"`, `"operating_cash_flow"`, `"cash"`} {
		if !strings.Contains(index, id) {
			t.Errorf("index metric list lacks %s", id)
		}
	}
}
