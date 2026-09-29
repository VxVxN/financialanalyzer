package handlers

import (
	"fmt"
	"html"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/components"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-echarts/go-echarts/v2/types"

	"github.com/VxVxN/financialanalyzer/internal/analytics"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

func (controller *Controller) ChartHandler(w http.ResponseWriter, r *http.Request) {
	metric := chi.URLParam(r, "metric")
	if !analytics.IsValidMetric(metric) {
		http.Error(w, "unknown metric", http.StatusBadRequest)
		return
	}
	theme := r.URL.Query().Get("theme")
	companiesParam := r.URL.Query().Get("companies")
	period := analytics.Period(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("period"))))

	if theme == "" {
		theme = "light"
	}
	switch period {
	case analytics.PeriodQuarter, analytics.PeriodTTM, analytics.PeriodAnnual:
	default:
		period = analytics.PeriodQuarter
	}

	var companies []string
	if companiesParam != "" {
		companies = strings.Split(companiesParam, ",")
	} else {
		var err error
		companies, err = controller.repo.GetAllCompanies(r.Context())
		if err != nil {
			controller.htmlServerError(w, "failed to list companies for chart", err)
			return
		}
	}

	history, err := controller.repo.GetCompaniesHistory(r.Context(), companies)
	if err != nil {
		controller.htmlServerError(w, "failed to load chart history", err)
		return
	}

	seriesByCompany := make(map[string]analytics.Series, len(companies))
	quality := make(map[string]companyQuality, len(companies))
	for _, c := range companies {
		seriesByCompany[c] = analytics.SeriesFor(history[c], metric, period)
		quality[c] = qualityFor(history[c], metric, period)
	}

	w.Header().Set("Content-Type", "text/html")

	bgColor := "#ffffff"
	textColor := "#000000"
	if theme == "dark" {
		bgColor = "#1a1a1a"
		textColor = "#ffffff"
	}

	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body {
            margin: 0;
            padding: 20px;
            background-color: %s;
            color: %s;
            font-family: Arial, sans-serif;
        }
        .chart-container {
            background-color: %s;
            border-radius: 8px;
            padding: 20px;
        }
    </style>
</head>
<body>
    <div class="chart-container">`, bgColor, textColor, bgColor)

	page := components.NewPage()
	page.PageTitle = fmt.Sprintf("%s — Financial Analyzer", formatMetricName(metric))

	lineChart := buildSeriesChart(seriesByCompany, companies, metric, period, quality)
	page.AddCharts(lineChart)
	_ = page.Render(w)

	fmt.Fprintf(w, `</div>`)
	renderSeriesTable(w, seriesByCompany, companies, metric, theme, quality)
	fmt.Fprintf(w, `<script>
function exportTableCSV() {
	const table = document.querySelector('.data-table');
	if (!table) return;
	const rows = Array.from(table.querySelectorAll('tr'));
	const csv = rows.map(function(row) {
		return Array.from(row.querySelectorAll('th,td')).map(function(cell) {
			let text = cell.innerText.trim();
			if (text === '—') text = '';
			if (/[",\n]/.test(text)) text = '"' + text.replace(/"/g, '""') + '"';
			return text;
		}).join(',');
	}).join('\n');
	const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
	const link = document.createElement('a');
	link.href = URL.createObjectURL(blob);
	link.download = %q;
	document.body.appendChild(link);
	link.click();
	document.body.removeChild(link);
	URL.revokeObjectURL(link.href);
}
</script>`, metric+"_export.csv")
	fmt.Fprintf(w, `</body></html>`)
}

// companyQuality is what the chart page shows about how trustworthy a
// company's figures are: where they came from and which points look suspicious.
type companyQuality struct {
	Sources []string                       // distinct models.Source* values
	Flags   map[string][]analytics.Anomaly // Point label -> anomalies for this metric
}

func qualityFor(history []models.QuarterData, metric string, period analytics.Period) companyQuality {
	return companyQuality{
		Sources: analytics.Sources(history),
		Flags:   analytics.AnomaliesByLabel(analytics.CheckHistory(history), metric, period),
	}
}

// sourcesLabel renders sources as "RSBU (issuer), CSV import"; notes is the
// matching explanation for a title attribute; comparable is false when any
// source is not group-level IFRS.
func sourcesLabel(sources []string) (label, notes string, comparable bool) {
	if len(sources) == 0 {
		sources = []string{""}
	}
	labels := make([]string, len(sources))
	noteList := make([]string, len(sources))
	comparable = true
	for i, s := range sources {
		labels[i] = analytics.SourceLabel(s)
		noteList[i] = analytics.SourceLabel(s) + ": " + analytics.SourceNote(s)
		if !analytics.IsComparable(s) {
			comparable = false
		}
	}
	return strings.Join(labels, ", "), strings.Join(noteList, "\n"), comparable
}

func anomalyMessages(anoms []analytics.Anomaly) string {
	msgs := make([]string, len(anoms))
	for i, a := range anoms {
		msgs[i] = a.Message
	}
	return strings.Join(msgs, "\n")
}

func mergedLabels(seriesByCompany map[string]analytics.Series, companies []string) []string {
	order := map[string]int{}
	for _, c := range companies {
		for _, p := range seriesByCompany[c] {
			if _, ok := order[p.Label]; !ok {
				order[p.Label] = p.Index
			}
		}
	}
	labels := make([]string, 0, len(order))
	for label := range order {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		return order[labels[i]] < order[labels[j]]
	})
	return labels
}

func buildSeriesChart(seriesByCompany map[string]analytics.Series, companies []string, metric string, period analytics.Period, quality map[string]companyQuality) *charts.Line {
	line := charts.NewLine()

	metricName := formatMetricName(metric)
	unit := getMetricUnit(metric)
	// Keyed by the escaped series name, which is what the tooltip receives.
	sourceBySeries := make(map[string]string, len(companies))
	for _, c := range companies {
		label, _, _ := sourcesLabel(quality[c].Sources)
		sourceBySeries[html.EscapeString(c)] = html.EscapeString(label) // tooltip renders HTML
	}
	tooltipFormatter := getTooltipFormatter(metric, unit, sourceBySeries)

	subtitle := "Quarterly"
	switch period {
	case analytics.PeriodTTM:
		subtitle = "Trailing 12 months"
	case analytics.PeriodAnnual:
		subtitle = "Annual"
	}

	line.SetGlobalOptions(
		charts.WithTitleOpts(opts.Title{
			Title:    fmt.Sprintf("%s — %s", metricName, subtitle),
			Subtitle: metricDescription(metric),
			Left:     "center",
		}),
		charts.WithInitializationOpts(opts.Initialization{
			Theme:  types.ThemeInfographic,
			Width:  "1200px",
			Height: "600px",
		}),
		charts.WithLegendOpts(opts.Legend{
			Show:   opts.Bool(true),
			Bottom: "0",
			Orient: "horizontal",
		}),
		charts.WithTooltipOpts(opts.Tooltip{
			Show:    opts.Bool(true),
			Trigger: "axis",
			AxisPointer: &opts.AxisPointer{
				Type: "shadow",
			},
			Formatter: opts.FuncOpts(tooltipFormatter),
		}),
		charts.WithGridOpts(opts.Grid{
			Show:         opts.Bool(true),
			Left:         "10%",
			Right:        "8%",
			Bottom:       "15%",
			ContainLabel: opts.Bool(true),
		}),
		charts.WithXAxisOpts(opts.XAxis{
			Name:         "Period",
			NameLocation: "center",
			NameGap:      30,
			Type:         "category",
			AxisLabel: &opts.AxisLabel{
				Rotate: 30,
				Margin: 10,
			},
			SplitLine: &opts.SplitLine{
				Show: opts.Bool(true),
				LineStyle: &opts.LineStyle{
					Type: "dashed",
				},
			},
		}),
		charts.WithYAxisOpts(opts.YAxis{
			Name:         metricName,
			NameLocation: "center",
			NameGap:      50,
			Type:         "value",
			AxisLabel: &opts.AxisLabel{
				Formatter: opts.FuncOpts(fmt.Sprintf("function(value) { return value + '%s'; }", unit)),
			},
			SplitLine: &opts.SplitLine{
				Show: opts.Bool(true),
				LineStyle: &opts.LineStyle{
					Type: "dashed",
				},
			},
		}),
		charts.WithDataZoomOpts(opts.DataZoom{
			Type:       "slider",
			Start:      0,
			End:        100,
			XAxisIndex: []int{0},
		}),
	)

	labels := mergedLabels(seriesByCompany, companies)
	line.SetXAxis(labels)

	colors := []string{
		"#5470c6", "#fac858", "#ee6666", "#73c0de",
		"#3ba272", "#fc8452", "#9a60b4", "#ea7ccc",
	}

	for idx, company := range companies {
		s := seriesByCompany[company]
		if len(s) == 0 {
			continue
		}
		byLabel := make(map[string]float64, len(s))
		for _, p := range s {
			byLabel[p.Label] = p.Value
		}
		values := make([]opts.LineData, len(labels))
		for i, label := range labels {
			v, ok := byLabel[label]
			if !ok || math.IsNaN(v) {
				values[i] = opts.LineData{Value: nil, Symbol: "none"}
				continue
			}
			if len(quality[company].Flags[label]) > 0 {
				// Suspicious point: a larger triangle, explained in the table below.
				values[i] = opts.LineData{Value: v, Symbol: "triangle", SymbolSize: 14}
				continue
			}
			values[i] = opts.LineData{Value: v, Symbol: "circle", SymbolSize: 8}
		}
		color := colors[idx%len(colors)]
		// Escape the series name: go-echarts emits it verbatim inside a <script>
		// block, so a name containing </script> would otherwise break out (XSS).
		line.AddSeries(html.EscapeString(company), values,
			charts.WithLineChartOpts(opts.LineChart{
				Smooth:       opts.Bool(true),
				ShowSymbol:   opts.Bool(true),
				Symbol:       "circle",
				SymbolSize:   8,
				ConnectNulls: opts.Bool(true),
			}),
			charts.WithLabelOpts(opts.Label{
				Show: opts.Bool(false),
			}),
			charts.WithAreaStyleOpts(opts.AreaStyle{
				Color: color + "20",
			}),
		)
	}

	return line
}

func renderSeriesTable(w http.ResponseWriter, seriesByCompany map[string]analytics.Series, companies []string, metric, theme string, quality map[string]companyQuality) {
	labels := mergedLabels(seriesByCompany, companies)

	bgPrimary := "#ffffff"
	bgSecondary := "#f0f0f0"
	bgButton := "#f0f0f0"
	bgButtonHover := "#e0e0e0"
	textPrimary := "#000000"
	borderColor := "#ccc"
	shadowColor := "rgba(0,0,0,0.1)"

	if theme == "dark" {
		bgPrimary = "#1a1a1a"
		bgSecondary = "#2d2d2d"
		bgButton = "#3d3d3d"
		bgButtonHover = "#4d4d4d"
		textPrimary = "#ffffff"
		borderColor = "#555"
		shadowColor = "rgba(255,255,255,0.1)"
	}

	fmt.Fprintf(w, `
	<style>
		.data-table {
			width: 100%%;
			border-collapse: collapse;
			margin-top: 30px;
			background-color: %s;
			color: %s;
			font-family: Arial, sans-serif;
			font-size: 14px;
			border-radius: 8px;
			overflow: hidden;
			box-shadow: 0 2px 10px %s;
		}
		.data-table th {
			background-color: %s;
			padding: 12px;
			text-align: center;
			border: 1px solid %s;
			font-weight: 600;
			color: %s;
		}
		.data-table td {
			padding: 10px;
			text-align: right;
			border: 1px solid %s;
			color: %s;
		}
		.data-table td:first-child {
			text-align: left;
			font-weight: 500;
			background-color: %s;
		}
		.data-table tr:hover td {
			background-color: %s;
		}
		.data-table .no-data {
			color: #999;
			font-style: italic;
			text-align: center;
		}
		.data-table .pos { color: #3ba272; }
		.data-table .neg { color: #ee6666; }
		.data-table td.source { text-align: left; font-size: 12px; white-space: nowrap; cursor: help; }
		.data-table td.source.warn::before { content: "⚠ "; color: #e6a23c; }
		.data-table td.flag { background-color: rgba(230,162,60,0.18); cursor: help; }
		.data-table td.flag::after { content: " ⚠"; color: #e6a23c; }
		.table-legend { margin-top: 8px; font-size: 12px; opacity: 0.75; font-family: Arial, sans-serif; }
		.table-container {
			margin-top: 20px;
			overflow-x: auto;
			border-radius: 8px;
		}
		.table-toolbar {
			margin-top: 24px;
			display: flex;
			justify-content: flex-end;
		}
		.export-btn {
			padding: 8px 16px;
			background-color: %s;
			color: %s;
			border: 1px solid %s;
			border-radius: 6px;
			cursor: pointer;
			font-size: 13px;
			font-family: Arial, sans-serif;
		}
		.export-btn:hover { background-color: %s; }
	</style>
	<div class="table-toolbar">
		<button class="export-btn" onclick="exportTableCSV()">⬇ Export CSV</button>
	</div>
	<div class="table-container">
		<table class="data-table">
			<thead>
				<tr>
					<th>Company / Period</th>
					<th>Source</th>`,
		bgPrimary, textPrimary, shadowColor, bgButton, borderColor, textPrimary, borderColor, textPrimary, bgSecondary, bgButtonHover,
		bgButton, textPrimary, borderColor, bgButtonHover)

	for _, label := range labels {
		fmt.Fprintf(w, `<th>%s</th>`, label)
	}
	fmt.Fprintf(w, `</tr></thead><tbody>`)

	unit := getMetricUnit(metric)
	isPct := unit == "%"

	for _, company := range companies {
		s := seriesByCompany[company]
		byLabel := make(map[string]float64, len(s))
		for _, p := range s {
			byLabel[p.Label] = p.Value
		}
		if len(byLabel) == 0 {
			continue
		}
		fmt.Fprintf(w, `<tr><td>%s</td>`, html.EscapeString(company))
		srcLabel, srcNotes, comparable := sourcesLabel(quality[company].Sources)
		srcClass := "source"
		if !comparable {
			srcClass += " warn"
		}
		fmt.Fprintf(w, `<td class="%s" title="%s">%s</td>`, srcClass, html.EscapeString(srcNotes), html.EscapeString(srcLabel))
		flags := quality[company].Flags
		for _, label := range labels {
			v, ok := byLabel[label]
			if !ok || math.IsNaN(v) {
				fmt.Fprintf(w, `<td class="no-data">—</td>`)
				continue
			}
			var classes []string
			if isPct {
				if v > 0 {
					classes = append(classes, "pos")
				} else if v < 0 {
					classes = append(classes, "neg")
				}
			}
			classAttr := ""
			if anoms := flags[label]; len(anoms) > 0 {
				classes = append(classes, "flag")
				classAttr = ` title="` + html.EscapeString(anomalyMessages(anoms)) + `"`
			}
			if len(classes) > 0 {
				classAttr = ` class="` + strings.Join(classes, " ") + `"` + classAttr
			}
			if unit == "x" {
				fmt.Fprintf(w, `<td%s>%.2fx</td>`, classAttr, v)
			} else if unit != "" {
				fmt.Fprintf(w, `<td%s>%.2f%s</td>`, classAttr, v, unit)
			} else {
				fmt.Fprintf(w, `<td%s>%s</td>`, classAttr, humanFormat(v))
			}
		}
		fmt.Fprintf(w, `</tr>`)
	}

	fmt.Fprintf(w, `</tbody></table></div>`)
	fmt.Fprintf(w, `<div class="table-legend">⚠ Source: figures are not group IFRS and may not be comparable across companies. `+
		`⚠ Cell / ▲ point: the value looks suspicious (hover for the reason).</div>`)
}

// humanFormat collapses large numbers to T/B/M/K. Used by the table for raw
// flow/stock values where 7-trillion-with-no-grouping is unreadable.
func humanFormat(v float64) string {
	abs := math.Abs(v)
	switch {
	case abs >= 1e12:
		return fmt.Sprintf("%.2fT", v/1e12)
	case abs >= 1e9:
		return fmt.Sprintf("%.2fB", v/1e9)
	case abs >= 1e6:
		return fmt.Sprintf("%.2fM", v/1e6)
	case abs >= 1e3:
		return fmt.Sprintf("%.2fK", v/1e3)
	}
	return fmt.Sprintf("%.2f", v)
}

// getTooltipFormatter returns the ECharts tooltip function. sources maps each
// (escaped) series name to its data-source label, appended after the name.
func getTooltipFormatter(metric, unit string, sources map[string]string) string {
	return strings.Replace(tooltipFormatterBody(metric, unit), "function(params) {",
		"function(params) {\n\t\t\t\tconst src = "+jsStringMap(sources)+";\n"+
			"\t\t\t\tconst srcOf = function(n) { return src[n] ? ' <span style=opacity:0.6>[' + src[n] + ']</span>' : ''; };", 1)
}

// jsStringMap renders m as a JS object literal for use inside a FuncOpts body.
// go-echarts JSON-encodes FuncOpts and injects the result verbatim, so any
// double quote or backslash in the code would come out escaped/doubled; the
// literal therefore uses only single quotes and no escape sequences (see
// jsString). Keys are sorted for deterministic output.
func jsStringMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = "[" + jsString(k) + "]: " + jsString(m[k]) // computed key: jsString may be a call
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// jsString renders s as a JS string expression free of quotes, backslashes and
// '<'. Plain text becomes '...'; anything else is percent-encoded and wrapped
// in decodeURIComponent('...'), which also keeps it safe inside <script>.
func jsString(s string) string {
	const safe = " -_.,:()/+"
	s = strings.ToValidUTF8(s, "\uFFFD") // decodeURIComponent throws on invalid UTF-8
	var b strings.Builder
	encoded := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(safe, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
		encoded = true
	}
	if encoded {
		return "decodeURIComponent('" + b.String() + "')"
	}
	return "'" + b.String() + "'"
}

func tooltipFormatterBody(metric, unit string) string {
	if unit == "" {
		// large absolute numbers (revenue, cap, debt, etc.)
		return `
			function(params) {
				let result = params[0].name + '<br/>';
				for(let i = 0; i < params.length; i++) {
					if (params[i].value !== null && params[i].value !== undefined) {
						let value = params[i].value;
						let absV = Math.abs(value);
						let formattedValue;
						if (absV >= 1000000000000) {
							formattedValue = (value / 1000000000000).toFixed(2) + 'T';
						} else if (absV >= 1000000000) {
							formattedValue = (value / 1000000000).toFixed(2) + 'B';
						} else if (absV >= 1000000) {
							formattedValue = (value / 1000000).toFixed(2) + 'M';
						} else if (absV >= 1000) {
							formattedValue = (value / 1000).toFixed(2) + 'K';
						} else {
							formattedValue = value.toFixed(2);
						}
						result += params[i].marker + ' ' +
								params[i].seriesName + srcOf(params[i].seriesName) + ': ' +
								formattedValue + '<br/>';
					} else {
						result += params[i].marker + ' ' +
								params[i].seriesName + ': No data<br/>';
					}
				}
				return result;
			}
		`
	}
	return `
		function(params) {
			let result = params[0].name + '<br/>';
			for(let i = 0; i < params.length; i++) {
				if (params[i].value !== null && params[i].value !== undefined) {
					result += params[i].marker + ' ' +
							params[i].seriesName + srcOf(params[i].seriesName) + ': ' +
							params[i].value.toFixed(2) + '` + unit + `' + '<br/>';
				} else {
					result += params[i].marker + ' ' +
							params[i].seriesName + ': No data<br/>';
				}
			}
			return result;
		}
	`
}

func formatMetricName(metric string) string {
	switch metric {
	case "revenue":
		return "Revenue"
	case "net_profit":
		return "Net Profit"
	case "ebitda":
		return "EBITDA"
	case "pe":
		return "P/E Ratio"
	case "roe":
		return "ROE"
	case "capitalization":
		return "Market Cap"
	case "debt":
		return "Debt"
	case "net_margin":
		return "Net Margin"
	case "ebitda_margin":
		return "EBITDA Margin"
	case "debt_ebitda":
		return "Debt / EBITDA"
	case "revenue_yoy":
		return "Revenue YoY"
	case "net_profit_yoy":
		return "Net Profit YoY"
	case "ebitda_yoy":
		return "EBITDA YoY"
	case "revenue_cagr3":
		return "Revenue CAGR (3Y)"
	case "net_profit_cagr3":
		return "Net Profit CAGR (3Y)"
	case "revenue_cagr5":
		return "Revenue CAGR (5Y)"
	case "net_profit_cagr5":
		return "Net Profit CAGR (5Y)"
	}
	return metric
}

func metricDescription(metric string) string {
	switch metric {
	case "net_margin":
		return "Net profit as % of revenue — pricing power & efficiency"
	case "ebitda_margin":
		return "EBITDA as % of revenue — operating profitability"
	case "debt_ebitda":
		return "Leverage — how many years of EBITDA to repay debt"
	case "revenue_yoy", "net_profit_yoy", "ebitda_yoy":
		return "Year-over-year growth, %"
	case "revenue_cagr3", "net_profit_cagr3":
		return "Compound annual growth, last 3 years"
	case "revenue_cagr5", "net_profit_cagr5":
		return "Compound annual growth, last 5 years"
	case "pe":
		return "Price / Earnings — lower is cheaper (negative = losses)"
	case "roe":
		return "Return on equity — how efficiently capital is used"
	}
	return ""
}

func getMetricUnit(metric string) string {
	switch metric {
	case "roe",
		"net_margin", "ebitda_margin",
		"revenue_yoy", "net_profit_yoy", "ebitda_yoy",
		"revenue_cagr3", "net_profit_cagr3",
		"revenue_cagr5", "net_profit_cagr5":
		return "%"
	case "debt_ebitda":
		return "x"
	}
	return ""
}
