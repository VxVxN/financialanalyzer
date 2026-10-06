// Command fetch builds quarterly financials from free primary sources (the
// pipelines live in internal/fetcher; cmd/plot can run them on a schedule), as
// a replacement for the (now paywalled) smart-lab scraper:
//
//   - annual RSBU statements from ГИР БО (bo.nalog.gov.ru) -> revenue, net
//     profit, equity, borrowings;
//   - year-end market capitalization from the MOEX ISS API;
//   - P/E and ROE are computed here from those inputs.
//
// Because ГИР БО is annual-only, each company year is stored as one row at Q4.
// Because it is unconsolidated RSBU (not IFRS), figures — and therefore the
// derived P/E / ROE — differ from group-level numbers, especially for holdings.
// EBITDA is left empty.
//
// Banks are absent from ГИР БО (they report to the Central Bank), so listed
// banks are fetched separately from the CBR's form 102 and 101 archives via the
// internal/scraper/cbr package: quarterly net profit and revenue (net interest
// + fee income, differenced from the cumulative YTD reports) and quarter-end
// balance-sheet equity, with market cap + P/E + ROE on the Q4 row. The bundled
// bank_tickers.txt registry maps each bank ticker to its CBR registration
// number (REGN). See "Banks (CBR forms 102/101)" below and the cbr package docs.
//
// Every run is logged in the fetch_runs table (shown on the web UI's
// "Обновление данных" page). Runs never overlap with each other or with the
// cmd/plot scheduler: a Postgres advisory lock makes a second run wait. With
// TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID set, a failed or partial run is
// reported to that Telegram chat.
//
// Ticker resolution. The legal entity's INN is required (neither MOEX nor ГИР БО
// bridges ticker<->INN), but it no longer has to be retyped: the bundled
// registry (fetch_tickers.txt, embedded via financialanalyzer.TickerRegistry)
// maps every known ticker to its INN and default category. A request may give
// just the ticker, a ticker+category, or the full ticker+INN+category; missing
// fields are filled from the registry, and the INN of a ticker the registry
// lacks is looked up in MOEX ISS (cmd/registry proposes the registry itself).
//
//	FETCH_TICKERS       comma-separated entries, each "TICKER", "TICKER:CATEGORY"
//	                    or "TICKER:INN:CATEGORY", e.g. "LKOH,GAZP:oil"
//	FETCH_TICKERS_FILE  path to a file with one "TICKER [INN] [CATEGORY]" per
//	                    line ("#" comments allowed)
//	(no FETCH_* list)   refresh the companies already in the DB (those
//	                    found in the bundled registries)
//	FETCH_ALL           with no list set: fetch every ticker in the bundled
//	                    registries instead
//
// Other knobs:
//
//	FETCH_CONCURRENCY   number of tickers fetched in parallel (default 3); each
//	                    worker keeps its own polite, rate-limited HTTP clients
//	FETCH_FORCE         when set, re-fetch periods already present in the DB
//	                    instead of skipping them
//	FETCH_QUOTES_ONLY   when set, only refresh the latest exchange closes
//
// Banks (CBR forms 102/101), independent of the ГИР БО list above:
//
//	FETCH_BANKS         comma-separated bank tickers, e.g. "SBER,VTBR"
//	FETCH_BANK_FROM_YEAR  earliest reporting year to fetch (default 2020); the
//	                    latest is the current year
//
// With no FETCH_* list set, fetch runs both pipelines over the companies
// already stored (FETCH_ALL: over the full bundled registries); requesting one
// kind explicitly suppresses the other.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/VxVxN/financialanalyzer/internal/application"
	"github.com/VxVxN/financialanalyzer/internal/config"
	"github.com/VxVxN/financialanalyzer/internal/fetcher"
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
		if errors.Is(err, context.Canceled) {
			logger.Info("Fetch interrupted")
			return
		}
		logger.Error("Fetch failed", "error", err)
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

	_, err = fetcher.RunRecorded(ctx, app.Repo, requestFromEnv(), fetcher.TriggerCLI, fetcher.RunOptions{Notifier: app.Notifier}, logger)
	return err
}

// requestFromEnv reads the FETCH_* variables documented above.
func requestFromEnv() fetcher.Request {
	return fetcher.Request{
		Tickers:      os.Getenv("FETCH_TICKERS"),
		TickersFile:  os.Getenv("FETCH_TICKERS_FILE"),
		Banks:        os.Getenv("FETCH_BANKS"),
		All:          os.Getenv("FETCH_ALL") != "",
		Force:        os.Getenv("FETCH_FORCE") != "",
		QuotesOnly:   os.Getenv("FETCH_QUOTES_ONLY") != "",
		Concurrency:  envInt("FETCH_CONCURRENCY"),
		BankFromYear: envInt("FETCH_BANK_FROM_YEAR"),
	}
}

// envInt returns a positive integer variable, or 0 (the fetcher's "default")
// when it is unset or invalid.
func envInt(key string) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return 0
}
