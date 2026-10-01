package fetcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
	"github.com/VxVxN/financialanalyzer/internal/scraper/girbo"
	"github.com/VxVxN/financialanalyzer/internal/scraper/moex"
)

// fetchResult is one ticker's outcome, sent back from a worker to the collector.
type fetchResult struct {
	ticker   string
	category string
	rows     int
	outcome  outcome
	err      error // why it failed (logged); nil for a failure with no rows at all
}

// fetchAll runs the ticker specs through a pool of workers, each with its own
// rate-limited clients, and returns the per-ticker tally.
func fetchAll(ctx context.Context, repo *database.Repository, specs []tickerSpec, concurrency int, force bool, logger *slog.Logger) (out pipelineResult) {
	jobs := make(chan tickerSpec)
	results := make(chan fetchResult)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each worker owns its clients: girbo/moex clients are not safe for
			// concurrent use (they carry a lastReq timestamp for rate limiting).
			bo := girbo.NewClient()
			mx := newMoexClient(logger)
			for spec := range jobs {
				results <- fetchOne(ctx, repo, bo, mx, spec, force, logger)
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, spec := range specs {
			select {
			case jobs <- spec:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	for res := range results {
		out.rows += res.rows
		switch res.outcome {
		case outcomeFailed:
			if res.err != nil {
				logger.Warn("Fetch failed", "ticker", res.ticker, "rows", res.rows, "error", res.err)
			} else {
				logger.Warn("No data", "ticker", res.ticker)
			}
			out.failed = append(out.failed, res.ticker)
		case outcomeUpToDate:
			out.upToDate++
		case outcomeUpdated:
			out.updated++
			logger.Info("Fetched", "ticker", res.ticker, "category", res.category, "rows", res.rows)
		}
	}
	return out
}

// fetchOne fetches and saves one ticker, returning its outcome.
func fetchOne(ctx context.Context, repo *database.Repository, bo *girbo.Client, mx *moex.Client, spec tickerSpec, force bool, logger *slog.Logger) fetchResult {
	res := fetchResult{ticker: spec.Ticker, category: spec.Category}

	rows, listed, err := fetchTicker(ctx, repo, bo, mx, spec, force, logger)
	var partial *girbo.PartialError
	if err != nil && !errors.As(err, &partial) {
		res.outcome, res.err = outcomeFailed, err
		return res
	}
	res.err = err // a partial failure: the rows that did parse are still saved

	saveErrs := 0
	for _, r := range rows {
		if err := repo.SaveQuarterData(ctx, r); err != nil {
			logger.Warn("Save failed", "ticker", spec.Ticker, "year", r.Year, "error", err)
			saveErrs++
			if res.err == nil {
				res.err = fmt.Errorf("save %d: %w", r.Year, err)
			}
			continue
		}
		res.rows++
	}
	res.outcome = classify(res.rows, saveErrs, partial != nil, listed > 0)
	return res
}

// fetchTicker combines one company's annual RSBU reports with year-end market
// caps into Q4 QuarterData rows, computing P/E and ROE. Years already stored
// for the company are not even downloaded unless force; listed counts the
// years ГИР БО has for the company (new or already stored), so the caller can
// tell "nothing new" from "no data at all" — stored CSV rows alone do not make
// a company known to ГИР БО. A *girbo.PartialError comes with the rows that
// did parse.
func fetchTicker(ctx context.Context, repo *database.Repository, bo *girbo.Client, mx *moex.Client, spec tickerSpec, force bool, logger *slog.Logger) (rows []models.QuarterData, listed int, err error) {
	company := strings.ToUpper(spec.Ticker)

	var existing map[string]struct{}
	if !force {
		existing, err = repo.ExistingPeriods(ctx, company)
		if err != nil {
			return nil, 0, fmt.Errorf("existing periods: %w", err)
		}
	}
	skipped := 0
	have := func(year int) bool {
		_, ok := existing[fmt.Sprintf("%d-Q4", year)]
		if ok {
			skipped++
		}
		return ok
	}

	reports, fetchErr := bo.FetchAnnual(ctx, spec.INN, have)
	var partial *girbo.PartialError
	if fetchErr != nil && !errors.As(fetchErr, &partial) {
		return nil, 0, fetchErr
	}

	out := make([]models.QuarterData, 0, len(reports))
	for _, r := range reports {
		marketCap, err := mx.CapitalizationAt(ctx, spec.Ticker, r.Year)
		if err != nil {
			// Missing market cap is non-fatal: keep the RSBU figures, skip P/E.
			logger.Warn("Capitalization unavailable", "ticker", spec.Ticker, "year", r.Year, "error", err)
		}
		capPtr := capitalization(marketCap, err)

		out = append(out, models.QuarterData{
			Year:           r.Year,
			Quarter:        "Q4",
			Company:        company,
			Category:       spec.Category,
			Source:         models.SourceRSBU,
			Capitalization: capPtr,
			Revenue:        r.Revenue,
			NetProfit:      r.NetProfit,
			Debt:           r.Debt,
			PE:             peRatio(capPtr, r.NetProfit),
			ROE:            roePercent(r.NetProfit, r.Equity),
			Equity:         r.Equity,

			Cash:              r.Cash,
			OperatingProfit:   r.OperatingProfit,
			OperatingCashFlow: r.OperatingCashFlow,
			Capex:             r.Capex,
		})
	}
	return out, skipped + len(out), fetchErr
}

// capitalization turns a MOEX market-cap lookup into a metric: nil when the
// lookup failed or returned nothing, so the row stores NULL rather than 0.
func capitalization(v float64, err error) *float64 {
	if err != nil || v <= 0 {
		return nil
	}
	return &v
}

// peRatio returns capitalization / net profit, or nil when it is undefined
// (no cap, no profit figure, or non-positive earnings).
func peRatio(capitalization, netProfit *float64) *float64 {
	if capitalization == nil || netProfit == nil || *netProfit <= 0 {
		return nil
	}
	return models.Float(*capitalization / *netProfit)
}

// roePercent returns net profit / equity as a percentage, or nil when undefined
// (either figure missing, or non-positive equity).
func roePercent(netProfit, equity *float64) *float64 {
	if netProfit == nil || equity == nil || *equity <= 0 {
		return nil
	}
	return models.Float(*netProfit / *equity * 100)
}
