// Command scrape pulls quarterly financials for a list of tickers from
// smart-lab.ru and writes them to Postgres via the same repository the
// CSV importer uses.
//
// Ticker list comes from one of:
//
//	SCRAPE_TICKERS      comma-separated "TICKER:CATEGORY" pairs, e.g.
//	                    "SBER:banks,LKOH:oil,GAZP:oil"
//	SCRAPE_TICKERS_FILE path to a text file, one "TICKER CATEGORY" per
//	                    line ("#" comments allowed)
//
// If neither is set, the tickers already present in the database are
// re-scraped (their existing category is preserved).
package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/application"
	"github.com/VxVxN/financialanalyzer/internal/config"
	"github.com/VxVxN/financialanalyzer/internal/scraper/smartlab"
)

type tickerSpec struct {
	Ticker   string
	Category string
}

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
		logger.Error("Scrape failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	app, err := application.Init(cfg)
	if err != nil {
		return fmt.Errorf("init app: %w", err)
	}
	defer app.Close()

	if err := app.MigrateDB(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	specs, err := loadTickerSpecs(app)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return fmt.Errorf("no tickers to scrape: set SCRAPE_TICKERS or SCRAPE_TICKERS_FILE, or import some companies first")
	}

	client := smartlab.NewClient()
	logger.Info("Starting scrape", "tickers", len(specs), "delay", client.Delay)

	var (
		okTickers int
		failed    []string
		totalRows int
	)
	start := time.Now()

	for _, spec := range specs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rows, err := client.FetchTicker(ctx, spec.Ticker, spec.Category)
		if err != nil {
			logger.Warn("Fetch failed", "ticker", spec.Ticker, "error", err)
			failed = append(failed, spec.Ticker)
			continue
		}
		if len(rows) == 0 {
			logger.Warn("No data parsed", "ticker", spec.Ticker)
			failed = append(failed, spec.Ticker)
			continue
		}

		saved := 0
		for _, r := range rows {
			if err := app.Repo.SaveQuarterData(r); err != nil {
				logger.Warn("Save failed", "ticker", spec.Ticker,
					"year", r.Year, "quarter", r.Quarter, "error", err)
				continue
			}
			saved++
		}
		totalRows += saved
		okTickers++
		logger.Info("Scraped", "ticker", spec.Ticker, "category", spec.Category, "rows", saved)
	}

	logger.Info("Scrape done",
		"ok", okTickers,
		"failed", len(failed),
		"rows", totalRows,
		"duration", time.Since(start).Round(time.Second))
	if len(failed) > 0 {
		logger.Info("Failed tickers", "list", strings.Join(failed, ","))
	}
	return nil
}

// loadTickerSpecs resolves the ticker list from env vars or, as a fallback,
// from the set of companies already in the database.
func loadTickerSpecs(app *application.Application) ([]tickerSpec, error) {
	if raw := os.Getenv("SCRAPE_TICKERS"); raw != "" {
		return parseTickerSpecList(raw), nil
	}
	if path := os.Getenv("SCRAPE_TICKERS_FILE"); path != "" {
		return readTickerSpecFile(path)
	}

	existing, err := app.Repo.GetAllCompaniesWithCategories()
	if err != nil {
		return nil, fmt.Errorf("list existing companies: %w", err)
	}
	seen := make(map[string]bool, len(existing))
	specs := make([]tickerSpec, 0, len(existing))
	for _, c := range existing {
		t := strings.ToUpper(strings.TrimSpace(c.Company))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		specs = append(specs, tickerSpec{Ticker: t, Category: c.Category})
	}
	return specs, nil
}

// parseTickerSpecList parses "SBER:banks,LKOH:oil,GAZP".
// A spec without ":category" gets an empty category.
func parseTickerSpecList(raw string) []tickerSpec {
	parts := strings.Split(raw, ",")
	specs := make([]tickerSpec, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		ticker, category, _ := strings.Cut(p, ":")
		specs = append(specs, tickerSpec{
			Ticker:   strings.ToUpper(strings.TrimSpace(ticker)),
			Category: strings.TrimSpace(category),
		})
	}
	return specs
}

// readTickerSpecFile reads "TICKER CATEGORY" per line; "#" starts a comment.
func readTickerSpecFile(path string) ([]tickerSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var specs []tickerSpec
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Allow either whitespace-separated or "TICKER:CATEGORY".
		ticker, category := splitTickerLine(line)
		if ticker == "" {
			continue
		}
		specs = append(specs, tickerSpec{
			Ticker:   strings.ToUpper(ticker),
			Category: category,
		})
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return specs, nil
}

func splitTickerLine(line string) (ticker, category string) {
	if strings.Contains(line, ":") {
		t, c, _ := strings.Cut(line, ":")
		return strings.TrimSpace(t), strings.TrimSpace(c)
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", ""
	}
	if len(fields) == 1 {
		return fields[0], ""
	}
	return fields[0], strings.Join(fields[1:], " ")
}
