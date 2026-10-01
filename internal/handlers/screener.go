package handlers

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"sort"

	financialanalyzer "github.com/VxVxN/financialanalyzer"
	"github.com/VxVxN/financialanalyzer/internal/analytics"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

var screenerTemplate = template.Must(
	template.ParseFS(financialanalyzer.TemplatesFS, "templates/screener.html"),
)

// buildScreener assembles one row per company, sorted by name.
func (controller *Controller) buildScreener(ctx context.Context) ([]analytics.ScreenerRow, error) {
	companies, err := controller.repo.GetAllCompanies(ctx)
	if err != nil {
		return nil, err
	}
	histories, err := controller.repo.GetCompaniesHistory(ctx, companies)
	if err != nil {
		return nil, err
	}
	quotes, err := controller.repo.GetMarketQuotes(ctx)
	if err != nil {
		return nil, err
	}

	rows := make([]analytics.ScreenerRow, 0, len(companies))
	for _, c := range companies {
		history := histories[c]
		if len(history) == 0 {
			continue
		}
		var quote *models.MarketQuote
		if q, ok := quotes[c]; ok && analytics.QuoteIsFresh(q, controller.now()) {
			quote = &q
		}
		rows = append(rows, analytics.BuildScreenerRow(history, quote))
	}
	analytics.ApplySectorMedians(rows, controller.now())
	sort.Slice(rows, func(i, j int) bool { return rows[i].Company < rows[j].Company })
	return rows, nil
}

// ScreenerAPI returns the screener rows as JSON (null = no data).
func (controller *Controller) ScreenerAPI(w http.ResponseWriter, r *http.Request) {
	rows, err := controller.buildScreener(r.Context())
	if err != nil {
		controller.serverError(w, "failed to build screener", err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// ScreenerHandler renders the sortable, filterable screener page. The rows are
// embedded in the page; sorting and filtering happen client-side.
func (controller *Controller) ScreenerHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := controller.buildScreener(r.Context())
	if err != nil {
		controller.htmlServerError(w, "failed to build screener", err)
		return
	}

	sourceLabels := map[string]string{"": analytics.SourceLabel("")}
	categorySet := map[string]struct{}{}
	for _, row := range rows {
		for _, s := range row.Sources {
			sourceLabels[s] = analytics.SourceLabel(s)
		}
		if row.Category != "" {
			categorySet[row.Category] = struct{}{}
		}
	}
	categories := make([]string, 0, len(categorySet))
	for c := range categorySet {
		categories = append(categories, c)
	}
	sort.Strings(categories)

	data := struct {
		Rows         []analytics.ScreenerRow
		SourceLabels map[string]string
		Categories   []string
	}{rows, sourceLabels, categories}

	// Render into a buffer so a template error becomes a clean 500 rather
	// than a truncated 200.
	var buf bytes.Buffer
	if err := screenerTemplate.Execute(&buf, data); err != nil {
		controller.htmlServerError(w, "failed to render screener", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
