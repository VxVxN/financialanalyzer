package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/config"
	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/components"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-echarts/go-echarts/v2/types"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg := config.LoadConfig()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	db, err := database.NewConnection(cfg)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	repo := database.NewRepository(db)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		companies, err := repo.GetAllCompanies()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		page := components.NewPage()
		page.PageTitle = "Financial Analyzer"

		metrics := []string{"revenue", "net_profit", "ebitda", "pe", "roe", "capitalization", "debt"}

		for _, metric := range metrics {
			data, err := repo.GetCompaniesMetric(companies, metric)
			if err != nil {
				logger.Error("Failed to get metric", "metric", metric, "error", err)
				continue
			}

			if len(data) == 0 {
				continue
			}

			lineChart := createNormalizedLineChart(data, metric, companies)
			page.AddCharts(lineChart)
		}

		page.Render(w)
	})

	port := ":8080"
	logger.Info("Starting server", "port", port)

	srv := &http.Server{
		Addr:    port,
		Handler: r,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	return srv.ListenAndServe()
}

func createNormalizedLineChart(data []database.CompanyMetric, metric string, companies []string) *charts.Line {
	line := charts.NewLine()

	metricName := formatMetricName(metric)

	line.SetGlobalOptions(
		charts.WithTitleOpts(opts.Title{
			Title:    fmt.Sprintf("%s Comparison", metricName),
			Subtitle: "Normalized to show relative performance (first value = 100%)",
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
			Formatter: opts.FuncOpts(fmt.Sprintf(`
				function(params) {
					let result = params[0].name + '<br/>';
					for(let i = 0; i < params.length; i++) {
						result += params[i].marker + ' ' + 
								params[i].seriesName + ': ' + 
								params[i].value.toFixed(2) + '%%<br/>';
					}
					return result;
				}
			`)),
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
					Type:  "dashed",
					Color: "#e9ecef",
				},
			},
		}),
		charts.WithYAxisOpts(opts.YAxis{
			Name:         "Normalized Value (%)",
			NameLocation: "center",
			NameGap:      50,
			Type:         "value",
			AxisLabel: &opts.AxisLabel{
				Formatter: "{value}%",
			},
			SplitLine: &opts.SplitLine{
				Show: opts.Bool(true),
				LineStyle: &opts.LineStyle{
					Type:  "dashed",
					Color: "#e9ecef",
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
		return quarters[i] < quarters[j]
	})

	line.SetXAxis(quarters)

	colors := []string{
		"#5470c6", "#fac858", "#ee6666", "#73c0de",
		"#3ba272", "#fc8452", "#9a60b4", "#ea7ccc",
	}

	for idx, company := range companies {
		if companyValues, ok := companyData[company]; ok {
			values := make([]opts.LineData, len(quarters))

			var baseValue float64
			for _, q := range quarters {
				if val, ok := companyValues[q]; ok && val != 0 {
					baseValue = val
					break
				}
			}

			if baseValue == 0 {
				for _, q := range quarters {
					if val, ok := companyValues[q]; ok {
						baseValue = val
						break
					}
				}
				if baseValue == 0 {
					baseValue = 1
				}
			}

			for i, q := range quarters {
				if val, ok := companyValues[q]; ok && val != 0 {
					values[i] = opts.LineData{
						Value:      (val / baseValue) * 100,
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
					Smooth:     opts.Bool(true),
					ShowSymbol: opts.Bool(true),
					Symbol:     "circle",
					SymbolSize: 8,
				}),
				charts.WithLabelOpts(opts.Label{
					Show: opts.Bool(false),
				}),
				charts.WithAreaStyleOpts(opts.AreaStyle{
					Color:   color + "20",
					Opacity: opts.Float(0.3),
				}),
			)
		}
	}

	return line
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
