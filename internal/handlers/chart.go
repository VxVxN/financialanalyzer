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

	"github.com/VxVxN/financialanalyzer/internal/analytics"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

func (controller *Controller) ChartHandler(w http.ResponseWriter, r *http.Request) {
	metric := chi.URLParam(r, "metric")
	if !analytics.IsValidMetric(metric) {
		http.Error(w, "unknown metric", http.StatusBadRequest)
		return
	}
	// Echoed into the page: only the two known values may pass.
	theme := "light"
	if r.URL.Query().Get("theme") == "dark" {
		theme = "dark"
	}
	companiesParam := r.URL.Query().Get("companies")
	period := analytics.Period(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("period"))))

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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// The page is framed by the compare page and the company card, which pass
	// their theme; it shares their stylesheet (tokens, fonts).
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="ru" data-theme="%s">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <link rel="stylesheet" href="%s">
    <style>
        body { padding: 8px 12px 16px; background: transparent; }
        .chart-container { width: 100%%; }
        .chart-container .container { width: 100%% !important; }
        .chart-container .item { width: 100%% !important; margin: 0 !important; }
    </style>
</head>
<body>
    <div class="chart-container">`, theme, assetURL("app.css"))

	page := components.NewPage()
	page.PageTitle = fmt.Sprintf("%s — Финансовый анализатор", formatMetricName(metric))

	lineChart := buildSeriesChart(seriesByCompany, companies, metric, period, quality, chartPaletteFor(theme))
	page.AddCharts(lineChart)
	_ = page.Render(w)
	// go-echarts sizes the chart once; follow the frame's width afterwards.
	fmt.Fprint(w, `<script>
window.addEventListener('resize', function () {
	document.querySelectorAll('[_echarts_instance_]').forEach(function (el) {
		var chart = echarts.getInstanceByDom(el);
		if (chart) chart.resize();
	});
});
</script>`)

	fmt.Fprintf(w, `</div>`)
	renderSeriesTable(w, seriesByCompany, companies, metric, quality)
	fmt.Fprintf(w, `<script>
function exportTableCSV() {
	const table = document.querySelector('.data-table');
	if (!table) return;
	const rows = Array.from(table.querySelectorAll('tr'));
	const csv = rows.map(function(row) {
		return Array.from(row.querySelectorAll('th,td')).map(function(cell) {
			let text = cell.innerText.trim();
			if (text === '—') text = '';
			if (/[";\n]/.test(text)) text = '"' + text.replace(/"/g, '""') + '"';
			return text;
		}).join(';');
	}).join('\n');
	const blob = new Blob(['\uFEFF' + csv], { type: 'text/csv;charset=utf-8;' });
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
	anoms := append(analytics.CheckHistory(history), analytics.CapJumpAnomalies(history)...)
	return companyQuality{
		Sources: analytics.Sources(history),
		Flags:   analytics.AnomaliesByLabel(anoms, metric, period),
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

// chartPalette is the chart's ink for one theme. The series hues are the
// dataviz reference categorical order, stepped separately for each surface
// (validated: adjacent CVD ΔE ≥ 8, normal-vision ΔE ≥ 15).
type chartPalette struct {
	series                   []string
	text, textMuted          string
	gridLine                 string
	tooltipBg, tooltipBorder string
}

func chartPaletteFor(theme string) chartPalette {
	if theme == "dark" {
		return chartPalette{
			series:    []string{"#3987e5", "#d95926", "#199e70", "#c98500", "#d55181", "#008300", "#9085e9", "#e66767"},
			text:      "#e8ecf2",
			textMuted: "#8d97a5",
			gridLine:  "#232b37",
			tooltipBg: "#161c25", tooltipBorder: "#2f3946",
		}
	}
	return chartPalette{
		series:    []string{"#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300", "#4a3aa7", "#e34948"},
		text:      "#111827",
		textMuted: "#5b6472",
		gridLine:  "#e4e7ec",
		tooltipBg: "#ffffff", tooltipBorder: "#d0d5dd",
	}
}

const chartFont = "Golos Text, system-ui, sans-serif"

func buildSeriesChart(seriesByCompany map[string]analytics.Series, companies []string, metric string, period analytics.Period, quality map[string]companyQuality, pal chartPalette) *charts.Line {
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

	subtitle := "по кварталам"
	switch period {
	case analytics.PeriodTTM:
		subtitle = "LTM (скользящие 12 месяцев)"
	case analytics.PeriodAnnual:
		subtitle = "по годам"
	}

	line.SetGlobalOptions(
		charts.WithTitleOpts(opts.Title{
			Title:    fmt.Sprintf("%s — %s", metricName, subtitle),
			Subtitle: metricDescription(metric),
			Left:     "left",
			TitleStyle: &opts.TextStyle{
				Color: pal.text, FontFamily: chartFont, FontSize: 16, FontWeight: "600",
			},
			SubtitleStyle: &opts.TextStyle{Color: pal.textMuted, FontFamily: chartFont, FontSize: 12},
		}),
		charts.WithInitializationOpts(opts.Initialization{
			Width:           "100%",
			Height:          "560px",
			BackgroundColor: "transparent",
		}),
		charts.WithLegendOpts(opts.Legend{
			Show:      opts.Bool(true),
			Bottom:    "0",
			Orient:    "horizontal",
			TextStyle: &opts.TextStyle{Color: pal.text, FontFamily: chartFont},
		}),
		charts.WithTooltipOpts(opts.Tooltip{
			Show:            opts.Bool(true),
			Trigger:         "axis",
			BackgroundColor: pal.tooltipBg,
			BorderColor:     pal.tooltipBorder,
			AxisPointer: &opts.AxisPointer{
				Type: "shadow",
			},
			Formatter: opts.FuncOpts(tooltipFormatter),
		}),
		charts.WithGridOpts(opts.Grid{
			Show:         opts.Bool(false),
			Left:         "2%",
			Right:        "2%",
			Top:          "70",
			Bottom:       "90",
			ContainLabel: opts.Bool(true),
		}),
		charts.WithXAxisOpts(opts.XAxis{
			Name:         "Период",
			NameLocation: "center",
			NameGap:      30,
			Type:         "category",
			AxisLabel: &opts.AxisLabel{
				Rotate: 30,
				Margin: 10,
				Color:  pal.textMuted,
			},
			SplitLine: &opts.SplitLine{
				Show: opts.Bool(false),
			},
		}),
		charts.WithYAxisOpts(opts.YAxis{
			// No axis name: the title names the metric and the labels carry the unit.
			Type: "value",
			AxisLabel: &opts.AxisLabel{
				Formatter: opts.FuncOpts(fmt.Sprintf("function(value) { return value + '%s'; }", unit)),
				Color:     pal.textMuted,
			},
			SplitLine: &opts.SplitLine{
				Show: opts.Bool(true),
				LineStyle: &opts.LineStyle{
					Color: pal.gridLine,
					Type:  "solid",
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
		// Fixed order; past the eighth series (the all-companies chart) the
		// hues repeat, and the legend and table carry identity.
		color := pal.series[idx%len(pal.series)]
		// Escape the series name: go-echarts emits it verbatim inside a <script>
		// block, so a name containing </script> would otherwise break out (XSS).
		line.AddSeries(html.EscapeString(company), values,
			charts.WithLineChartOpts(opts.LineChart{
				Smooth:       opts.Bool(false),
				ShowSymbol:   opts.Bool(true),
				Symbol:       "circle",
				SymbolSize:   8,
				ConnectNulls: opts.Bool(true),
			}),
			charts.WithLabelOpts(opts.Label{
				Show: opts.Bool(false),
			}),
			charts.WithLineStyleOpts(opts.LineStyle{Color: color, Width: 2}),
			charts.WithItemStyleOpts(opts.ItemStyle{Color: color}),
		)
	}

	return line
}

func renderSeriesTable(w http.ResponseWriter, seriesByCompany map[string]analytics.Series, companies []string, metric string, quality map[string]companyQuality) {
	labels := mergedLabels(seriesByCompany, companies)

	fmt.Fprint(w, `
	<style>
		.table-toolbar { margin-top: 16px; display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; }
		.table-toolbar h2 { font-size: 15px; font-weight: 600; }
		.table-container { margin-top: 10px; overflow-x: auto; border: 1px solid var(--border); border-radius: var(--radius); }
		.data-table td:first-child { font-weight: 600; position: sticky; left: 0; background: var(--surface); }
		.data-table thead th:first-child { left: 0; z-index: 2; }
		.data-table td { font-variant-numeric: tabular-nums; }
		.data-table .no-data { color: var(--text-3); text-align: center; }
		.data-table .pos { color: var(--up); }
		.data-table .neg { color: var(--down); }
		.data-table td.source { text-align: left; font-size: 12px; cursor: help; color: var(--text-2); }
		.data-table td.source.warn::before { content: "⚠ "; color: var(--warn); }
		.data-table td.flag { background: var(--warn-soft); cursor: help; }
		.data-table td.flag::after { content: " ▲"; color: var(--warn); }
		.table-legend { margin-top: 8px; font-size: 12px; color: var(--text-3); }
	</style>
	<div class="table-toolbar">
		<h2>Данные</h2>
		<button type="button" class="btn btn-sm export-btn" onclick="exportTableCSV()">Экспорт в CSV</button>
	</div>
	<div class="table-container">
		<table class="table data-table">
			<thead>
				<tr>
					<th>Компания / период</th>
					<th class="left">Источник</th>`)

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
			} else if metric == "pe" {
				fmt.Fprintf(w, `<td%s>%.2f</td>`, classAttr, v)
			} else {
				fmt.Fprintf(w, `<td%s>%s</td>`, classAttr, humanFormat(v))
			}
		}
		fmt.Fprintf(w, `</tr>`)
	}

	fmt.Fprintf(w, `</tbody></table></div>`)
	flagNote := "значение выглядит подозрительно"
	if metric == "capitalization" {
		flagNote = "значение выглядит подозрительно или капитализация скакнула вдвое и больше к соседнему году"
	}
	fmt.Fprintf(w, `<div class="table-legend">⚠ Источник: данные не по МСФО группы и могут быть несопоставимы между компаниями. `+
		`▲ Ячейка или треугольная точка: %s (причина — во всплывающей подсказке).</div>`, flagNote)
}

// humanFormat renders a money value stored in billions of RUB (the unit of
// every flow/stock column) as "X млрд" or, from 1000 bln up, "X трлн".
func humanFormat(v float64) string {
	if math.Abs(v) >= 1000 {
		return fmt.Sprintf("%.2f трлн", v/1000)
	}
	return fmt.Sprintf("%.2f млрд", v)
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
	if unit == "" && metric != "pe" {
		// money in billions of RUB (revenue, cap, debt, etc.)
		return `
			function(params) {
				let result = params[0].name + '<br/>';
				for(let i = 0; i < params.length; i++) {
					if (params[i].value !== null && params[i].value !== undefined) {
						let value = params[i].value;
						let absV = Math.abs(value);
						let formattedValue;
						if (absV >= 1000) {
							formattedValue = (value / 1000).toFixed(2) + ' трлн';
						} else {
							formattedValue = value.toFixed(2) + ' млрд';
						}
						result += params[i].marker + ' ' +
								params[i].seriesName + srcOf(params[i].seriesName) + ': ' +
								formattedValue + '<br/>';
					} else {
						result += params[i].marker + ' ' +
								params[i].seriesName + ': нет данных<br/>';
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
							params[i].seriesName + ': нет данных<br/>';
				}
			}
			return result;
		}
	`
}

func formatMetricName(metric string) string {
	switch metric {
	case "revenue":
		return "Выручка"
	case "net_profit":
		return "Чистая прибыль"
	case "ebitda":
		return "EBITDA"
	case "pe":
		return "P/E"
	case "roe":
		return "ROE"
	case "capitalization":
		return "Капитализация"
	case "debt":
		return "Долг"
	case "equity":
		return "Капитал"
	case "dividends":
		return "Дивиденды"
	case "pb":
		return "P/B"
	case "div_yield":
		return "Дивидендная доходность"
	case "net_margin":
		return "Чистая маржа"
	case "ebitda_margin":
		return "Маржа EBITDA"
	case "debt_ebitda":
		return "Долг / EBITDA"
	case "revenue_yoy":
		return "Выручка г/г"
	case "net_profit_yoy":
		return "Чистая прибыль г/г"
	case "ebitda_yoy":
		return "EBITDA г/г"
	case "revenue_cagr3":
		return "CAGR выручки (3 года)"
	case "net_profit_cagr3":
		return "CAGR чистой прибыли (3 года)"
	case "revenue_cagr5":
		return "CAGR выручки (5 лет)"
	case "net_profit_cagr5":
		return "CAGR чистой прибыли (5 лет)"
	case "operating_profit":
		return "Операционная прибыль"
	case "cash":
		return "Денежные средства"
	case "net_debt":
		return "Чистый долг"
	case "operating_cash_flow":
		return "Операционный денежный поток"
	case "capex":
		return "Капзатраты"
	case "fcf":
		return "Свободный денежный поток (FCF)"
	case "ev":
		return "EV"
	case "ev_ebit":
		return "EV/EBIT"
	case "p_fcf":
		return "P/FCF"
	case "operating_margin":
		return "Операционная маржа"
	}
	return metric
}

func metricDescription(metric string) string {
	switch metric {
	case "operating_profit":
		return "Прибыль от продаж (РСБУ, стр. 2200) или операционная прибыль из CSV — используется как EBIT"
	case "cash":
		return "Денежные средства и эквиваленты на конец периода (РСБУ, стр. 1250)"
	case "net_debt":
		return "Долг минус денежные средства; отрицательный — у компании больше денег, чем займов"
	case "operating_cash_flow":
		return "Сальдо денежных потоков от текущих операций (РСБУ, стр. 4100)"
	case "capex":
		return "Платежи на приобретение и создание внеоборотных активов (РСБУ, стр. 4221)"
	case "fcf":
		return "Операционный денежный поток минус капзатраты — деньги, доступные акционерам и кредиторам"
	case "ev":
		return "Стоимость бизнеса: капитализация + чистый долг"
	case "ev_ebit":
		return "EV / операционная прибыль LTM — оценка с учётом долга; при убытке не считается"
	case "p_fcf":
		return "Капитализация / свободный денежный поток LTM; при отрицательном FCF не считается"
	case "operating_margin":
		return "Операционная прибыль в % от выручки"
	case "revenue":
		return "Выручка (банки: чистый процентный доход + комиссионные доходы)"
	case "net_margin":
		return "Чистая прибыль в % от выручки — ценовая сила и эффективность"
	case "ebitda_margin":
		return "EBITDA в % от выручки — операционная рентабельность"
	case "debt_ebitda":
		return "Долговая нагрузка — за сколько лет EBITDA можно погасить долг"
	case "revenue_yoy", "net_profit_yoy", "ebitda_yoy":
		return "Рост год к году, %"
	case "revenue_cagr3", "net_profit_cagr3":
		return "Среднегодовой рост за последние 3 года"
	case "revenue_cagr5", "net_profit_cagr5":
		return "Среднегодовой рост за последние 5 лет"
	case "pe":
		return "Цена / прибыль — чем ниже, тем дешевле (отрицательный = убыток)"
	case "roe":
		return "Рентабельность капитала — насколько эффективно используется капитал"
	case "pb":
		return "Цена / балансовая стоимость — капитализация на рубль капитала"
	case "div_yield":
		return "Дивиденды с датой отсечки в этом году / капитализация на конец года, %"
	case "equity":
		return "Собственный капитал (банки: баланс самого банка по РСБУ, форма 101)"
	case "dividends":
		return "Сумма дивидендов за год (по году отсечки), млрд руб."
	}
	return ""
}

func getMetricUnit(metric string) string {
	switch metric {
	case "roe", "div_yield",
		"net_margin", "ebitda_margin", "operating_margin",
		"revenue_yoy", "net_profit_yoy", "ebitda_yoy",
		"revenue_cagr3", "net_profit_cagr3",
		"revenue_cagr5", "net_profit_cagr5":
		return "%"
	case "debt_ebitda", "pb", "ev_ebit", "p_fcf":
		return "x"
	}
	return ""
}
