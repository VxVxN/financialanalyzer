package handlers

import (
	"html/template"
	"net/http"

	financialanalyzer "github.com/VxVxN/financialanalyzer"
)

// indexTemplate is parsed once from the embedded FS at startup. A parse failure
// is a programmer error (the template ships inside the binary), so we panic.
var indexTemplate = template.Must(
	template.ParseFS(financialanalyzer.TemplatesFS, "templates/index.html"),
)

type metricItem struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type metricGroup struct {
	Name  string       `json:"name"`
	Items []metricItem `json:"items"`
}

var indexMetricGroups = []metricGroup{
	{
		Name: "Основные",
		Items: []metricItem{
			{ID: "revenue", Label: "Выручка"},
			{ID: "net_profit", Label: "Чистая прибыль"},
			{ID: "ebitda", Label: "EBITDA"},
			{ID: "capitalization", Label: "Капитализация"},
			{ID: "debt", Label: "Долг"},
			{ID: "equity", Label: "Капитал"},
			{ID: "dividends", Label: "Дивиденды"},
			{ID: "pe", Label: "P/E"},
			{ID: "roe", Label: "ROE"},
		},
	},
	{
		Name: "Оценка",
		Items: []metricItem{
			{ID: "pb", Label: "P/B"},
			{ID: "div_yield", Label: "Див. доходность"},
		},
	},
	{
		Name: "Рентабельность",
		Items: []metricItem{
			{ID: "net_margin", Label: "Чистая маржа"},
			{ID: "ebitda_margin", Label: "Маржа EBITDA"},
			{ID: "debt_ebitda", Label: "Долг / EBITDA"},
		},
	},
	{
		Name: "Рост",
		Items: []metricItem{
			{ID: "revenue_yoy", Label: "Выручка г/г"},
			{ID: "net_profit_yoy", Label: "Чистая прибыль г/г"},
			{ID: "ebitda_yoy", Label: "EBITDA г/г"},
			{ID: "revenue_cagr3", Label: "CAGR выручки 3 г."},
			{ID: "net_profit_cagr3", Label: "CAGR прибыли 3 г."},
			{ID: "revenue_cagr5", Label: "CAGR выручки 5 л."},
			{ID: "net_profit_cagr5", Label: "CAGR прибыли 5 л."},
		},
	},
}

func (controller *Controller) IndexHandler(w http.ResponseWriter, r *http.Request) {
	data := struct {
		MetricGroups []metricGroup
	}{
		MetricGroups: indexMetricGroups,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := indexTemplate.Execute(w, data); err != nil {
		controller.htmlServerError(w, "failed to render index", err)
	}
}
