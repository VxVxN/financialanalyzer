package handlers

import (
	"bytes"
	"net/http"
)

// compareTemplate is parsed once from the embedded FS at startup.
var compareTemplate = parsePage("compare.html")

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
			{ID: "operating_profit", Label: "Операционная прибыль"},
			{ID: "capitalization", Label: "Капитализация"},
			{ID: "debt", Label: "Долг"},
			{ID: "cash", Label: "Денежные средства"},
			{ID: "net_debt", Label: "Чистый долг"},
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
			{ID: "ev", Label: "EV"},
			{ID: "ev_ebit", Label: "EV/EBIT"},
			{ID: "p_fcf", Label: "P/FCF"},
		},
	},
	{
		Name: "Денежный поток",
		Items: []metricItem{
			{ID: "operating_cash_flow", Label: "Операционный поток"},
			{ID: "capex", Label: "Капзатраты"},
			{ID: "fcf", Label: "FCF"},
		},
	},
	{
		Name: "Рентабельность",
		Items: []metricItem{
			{ID: "net_margin", Label: "Чистая маржа"},
			{ID: "ebitda_margin", Label: "Маржа EBITDA"},
			{ID: "operating_margin", Label: "Операционная маржа"},
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

// CompareHandler renders /compare: pick companies (or arrive with
// ?companies=A,B from the comparison tray) and chart their metrics side by side.
func (controller *Controller) CompareHandler(w http.ResponseWriter, r *http.Request) {
	data := struct {
		Meta         pageMeta
		MetricGroups []metricGroup
	}{
		Meta:         pageMeta{Title: "Сравнение", Active: "compare"},
		MetricGroups: indexMetricGroups,
	}

	var buf bytes.Buffer
	if err := compareTemplate.Execute(&buf, data); err != nil {
		controller.htmlServerError(w, "failed to render compare page", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
