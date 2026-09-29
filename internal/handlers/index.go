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
		Name: "Core",
		Items: []metricItem{
			{ID: "revenue", Label: "Revenue"},
			{ID: "net_profit", Label: "Net Profit"},
			{ID: "ebitda", Label: "EBITDA"},
			{ID: "capitalization", Label: "Market Cap"},
			{ID: "debt", Label: "Debt"},
			{ID: "pe", Label: "P/E"},
			{ID: "roe", Label: "ROE"},
		},
	},
	{
		Name: "Profitability",
		Items: []metricItem{
			{ID: "net_margin", Label: "Net Margin"},
			{ID: "ebitda_margin", Label: "EBITDA Margin"},
			{ID: "debt_ebitda", Label: "Debt / EBITDA"},
		},
	},
	{
		Name: "Growth",
		Items: []metricItem{
			{ID: "revenue_yoy", Label: "Revenue YoY"},
			{ID: "net_profit_yoy", Label: "Net Profit YoY"},
			{ID: "ebitda_yoy", Label: "EBITDA YoY"},
			{ID: "revenue_cagr3", Label: "Revenue CAGR 3Y"},
			{ID: "net_profit_cagr3", Label: "Net Profit CAGR 3Y"},
			{ID: "revenue_cagr5", Label: "Revenue CAGR 5Y"},
			{ID: "net_profit_cagr5", Label: "Net Profit CAGR 5Y"},
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
