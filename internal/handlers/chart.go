package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/components"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-echarts/go-echarts/v2/types"

	"github.com/VxVxN/financialanalyzer/internal/database"
)

func (controller *Controller) ChartHandler(w http.ResponseWriter, r *http.Request) {
	metric := chi.URLParam(r, "metric")
	theme := r.URL.Query().Get("theme")
	companiesParam := r.URL.Query().Get("companies")

	if theme == "" {
		theme = "light"
	}

	var companies []string
	if companiesParam != "" {
		companies = strings.Split(companiesParam, ",")
	} else {
		var err error
		companies, err = controller.repo.GetAllCompanies()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	data, err := controller.repo.GetCompaniesMetric(companies, metric)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
	page.PageTitle = fmt.Sprintf("%s - Financial Analyzer", formatMetricName(metric))

	if len(data) > 0 {
		lineChart := createNormalizedLineChart(data, metric, companies)
		page.AddCharts(lineChart)
	}

	page.Render(w)

	fmt.Fprintf(w, `</div>`)
	renderDataTable(w, data, companies, metric, theme)

	fmt.Fprintf(w, `</body></html>`)
}

func renderDataTable(w http.ResponseWriter, data []database.CompanyMetric, companies []string, metric, theme string) {
	companyData := make(map[string]map[string]float64)
	allQuarters := make(map[string]bool)

	for _, item := range data {
		key := fmt.Sprintf("%d-%s", item.Year, item.Quarter)
		if companyData[item.Company] == nil {
			companyData[item.Company] = make(map[string]float64)
		}
		companyData[item.Company][key] = item.Value
		allQuarters[key] = true
	}

	quarters := make([]string, 0, len(allQuarters))
	for q := range allQuarters {
		quarters = append(quarters, q)
	}
	sort.Slice(quarters, func(i, j int) bool {
		yearI, _ := strconv.Atoi(quarters[i][:4])
		yearJ, _ := strconv.Atoi(quarters[j][:4])
		if yearI == yearJ {
			return quarters[i][5:] < quarters[j][5:]
		}
		return yearI < yearJ
	})

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
		.table-container {
			margin-top: 20px;
			overflow-x: auto;
			border-radius: 8px;
		}
	</style>
	<div class="table-container">
		<table class="data-table">
			<thead>
				<tr>
					<th>Company / Quarter</th>`,
		bgPrimary, textPrimary, shadowColor, bgButton, borderColor, textPrimary, borderColor, textPrimary, bgSecondary, bgButtonHover)

	for _, quarter := range quarters {
		fmt.Fprintf(w, `<th>%s</th>`, quarter)
	}

	fmt.Fprintf(w, `</tr></thead><tbody>`)

	for _, company := range companies {
		if _, ok := companyData[company]; !ok {
			continue
		}

		fmt.Fprintf(w, `<tr><td>%s</td>`, company)

		for _, quarter := range quarters {
			if val, ok := companyData[company][quarter]; ok && val != 0 {
				unit := getMetricUnit(metric)
				if unit != "" {
					fmt.Fprintf(w, `<td>%.2f%s</td>`, val, unit)
				} else {
					fmt.Fprintf(w, `<td>%.2f</td>`, val)
				}
			} else {
				fmt.Fprintf(w, `<td class="no-data">—</td>`)
			}
		}
		fmt.Fprintf(w, `</tr>`)
	}

	fmt.Fprintf(w, `</tbody></table></div>`)
}

func createNormalizedLineChart(data []database.CompanyMetric, metric string, companies []string) *charts.Line {
	line := charts.NewLine()

	metricName := formatMetricName(metric)

	yAxisName := metricName
	unit := getMetricUnit(metric)

	tooltipFormatter := getTooltipFormatter(metric, unit)

	line.SetGlobalOptions(
		charts.WithTitleOpts(opts.Title{
			Title:    fmt.Sprintf("%s Comparison", metricName),
			Subtitle: "Absolute values",
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
			Name:         "Quarter",
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
			Name:         yAxisName,
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

	companyData := make(map[string]map[string]float64)
	allQuarters := make(map[string]bool)

	for _, item := range data {
		key := fmt.Sprintf("%d-%s", item.Year, item.Quarter)
		if companyData[item.Company] == nil {
			companyData[item.Company] = make(map[string]float64)
		}
		companyData[item.Company][key] = item.Value
		allQuarters[key] = true
	}

	quarters := make([]string, 0, len(allQuarters))
	for q := range allQuarters {
		quarters = append(quarters, q)
	}
	sort.Slice(quarters, func(i, j int) bool {
		yearI, _ := strconv.Atoi(quarters[i][:4])
		yearJ, _ := strconv.Atoi(quarters[j][:4])
		if yearI == yearJ {
			return quarters[i][5:] < quarters[j][5:]
		}
		return yearI < yearJ
	})

	line.SetXAxis(quarters)

	colors := []string{
		"#5470c6", "#fac858", "#ee6666", "#73c0de",
		"#3ba272", "#fc8452", "#9a60b4", "#ea7ccc",
	}

	for idx, company := range companies {
		if companyValues, ok := companyData[company]; ok {
			values := make([]opts.LineData, len(quarters))

			for i, q := range quarters {
				if val, ok := companyValues[q]; ok && val != 0 {
					values[i] = opts.LineData{
						Value:      val,
						Symbol:     "circle",
						SymbolSize: 8,
					}
				} else {
					values[i] = opts.LineData{
						Value:  nil,
						Symbol: "none",
					}
				}
			}

			color := colors[idx%len(colors)]
			line.AddSeries(company, values,
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
	}

	return line
}

func getTooltipFormatter(metric, unit string) string {
	switch metric {
	case "capitalization", "revenue", "net_profit", "ebitda", "debt":
		return `
			function(params) {
				let result = params[0].name + '<br/>';
				for(let i = 0; i < params.length; i++) {
					if (params[i].value !== null && params[i].value !== undefined) {
						let value = params[i].value;
						let formattedValue;
						if (value >= 1000000000000) {
							formattedValue = (value / 1000000000000).toFixed(2) + 'T';
						} else if (value >= 1000000000) {
							formattedValue = (value / 1000000000).toFixed(2) + 'B';
						} else if (value >= 1000000) {
							formattedValue = (value / 1000000).toFixed(2) + 'M';
						} else if (value >= 1000) {
							formattedValue = (value / 1000).toFixed(2) + 'K';
						} else {
							formattedValue = value.toFixed(2);
						}
						result += params[i].marker + ' ' + 
								params[i].seriesName + ': ' + 
								formattedValue + '` + unit + `' + '<br/>';
					} else {
						result += params[i].marker + ' ' + 
								params[i].seriesName + ': No data<br/>';
					}
				}
				return result;
			}
		`
	default:
		return `
			function(params) {
				let result = params[0].name + '<br/>';
				for(let i = 0; i < params.length; i++) {
					if (params[i].value !== null && params[i].value !== undefined) {
						result += params[i].marker + ' ' + 
								params[i].seriesName + ': ' + 
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
		return "ROE (%)"
	case "capitalization":
		return "Market Cap"
	case "debt":
		return "Debt"
	default:
		return metric
	}
}

func getMetricUnit(metric string) string {
	switch metric {
	case "roe":
		return "%"
	default:
		return ""
	}
}
