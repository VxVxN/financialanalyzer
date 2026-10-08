package handlers

import (
	"bytes"
	"context"
	"net/http"
	"sort"
	"strconv"

	"github.com/VxVxN/financialanalyzer/internal/analytics"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

var screenerTemplate = parsePage("screener.html")

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
	reviews, err := controller.repo.CapReviews(ctx)
	if err != nil {
		return nil, err
	}
	byCompany := analytics.GroupCapReviews(reviews)

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
		row := analytics.BuildScreenerRow(history, quote, byCompany[c])
		row.Portfolio = inPortfolio(row.Company)
		rows = append(rows, row)
	}
	analytics.ApplySectorMedians(rows, controller.now())
	sort.Slice(rows, func(i, j int) bool { return rows[i].Company < rows[j].Company })
	return rows, nil
}

// CheapList is the Monday operator note. eventIDs are the queued IFRS years
// included in the text; the caller deletes them after the note is delivered.
// bands is the P/E bucket to remember for each portfolio name, so the next
// note mentions a quartile only when the name enters it.
func (controller *Controller) CheapList(ctx context.Context) (msg string, eventIDs []int64, bands map[string]string, err error) {
	rows, err := controller.buildScreener(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	companies := make([]string, len(rows))
	for i, r := range rows {
		companies[i] = r.Company
	}
	histories, err := controller.repo.GetCompaniesHistory(ctx, companies)
	if err != nil {
		return "", nil, nil, err
	}
	quotes, err := controller.repo.GetMarketQuotes(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	events, err := controller.repo.PendingDigestEvents(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	prev, err := controller.repo.PortfolioBands(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	var years []analytics.NoteYear
	for _, ev := range events {
		if ev.Kind != models.DigestIFRSYear {
			continue
		}
		year, convErr := strconv.Atoi(ev.Detail)
		if convErr != nil {
			continue
		}
		years = append(years, analytics.NoteYear{Company: ev.Company, Year: year})
		eventIDs = append(eventIDs, ev.ID)
	}
	reviews, err := controller.repo.CapReviews(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	return analytics.MondayMessage(rows, analytics.StaleQuotes(quotes, controller.now()), analytics.CollectJumps(histories, analytics.GroupCapReviews(reviews)), years, prev), eventIDs, analytics.NextPortfolioBands(rows), nil
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

// ScreenerHandler renders the screener, the home page: every company in one
// sortable, filterable table with a summary drawer and the comparison tray.
// The rows are embedded in the page; sorting and filtering happen client-side.
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
		Meta         pageMeta
		Rows         []analytics.ScreenerRow
		SourceLabels map[string]string
		Categories   []string
	}{pageMeta{Title: "Скринер", Active: "screener"}, rows, sourceLabels, categories}

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

// ScreenerRedirect sends the screener's former address to the home page.
func (controller *Controller) ScreenerRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusMovedPermanently)
}
