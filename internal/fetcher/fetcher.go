// Package fetcher builds financials from free primary sources, as a
// replacement for the (now paywalled) smart-lab scraper:
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
// number (REGN). See banks.go and the cbr package docs.
//
// Ticker resolution. The legal entity's INN is required (neither MOEX nor ГИР БО
// bridges ticker<->INN), but it does not have to be retyped: the bundled
// registry (fetch_tickers.txt, embedded via financialanalyzer.TickerRegistry)
// maps every known ticker to its INN and default category. A request may give
// just the ticker, a ticker+category, or the full ticker+INN+category; missing
// fields are filled from the registry.
//
// Every run also refreshes the latest exchange close (market_quotes) of the
// requested companies. Run is shared by POST /api/fetch and the scheduler;
// RunRecorded additionally logs the run in the fetch_runs table.
package fetcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

const (
	defaultConcurrency  = 3
	defaultBankFromYear = 2020

	// equityGrace is how long after a quarter's archive date a missing form
	// 101 counts as "not published yet": such a quarter is not written, so the
	// next run retries it rather than storing it without equity for good.
	equityGrace = 90 * 24 * time.Hour
)

// Run kinds, as stored in fetch_runs.kind.
const (
	KindQuotes     = models.RunKindQuotes
	KindFinancials = models.RunKindFinancials
)

// ErrNoCompanies is returned by a run over the stored companies when the DB
// has none yet: there is nothing to refresh until companies are added with an
// explicit list (tickers / banks on POST /api/fetch, or all=true).
var ErrNoCompanies = errors.New("no companies in the DB yet: add some from /updates (tickers/banks) or all=true for the whole registry")

// Request says what one run fetches. The zero value refreshes every company
// already stored (financials and quotes).
type Request struct {
	// Tickers is a comma-separated list of "TICKER", "TICKER:CATEGORY" or
	// "TICKER:INN:CATEGORY" entries (ГИР БО pipeline).
	Tickers string
	// TickersFile names a file with one "TICKER [INN] [CATEGORY]" per line.
	TickersFile string
	// Banks is a comma-separated list of bank tickers (CBR pipeline).
	Banks string
	// All, with no list set, fetches every ticker in the bundled registries
	// instead of only the companies already stored.
	All bool
	// Force re-fetches periods already stored instead of skipping them.
	Force bool
	// Backfill re-fetches a stored period only when a column added after the
	// row was written is still NULL: equity, debt, cash, operating profit,
	// operating cash flow and capex for an RSBU year, equity for a bank
	// quarter. Complete periods and CSV rows are left alone, and years that
	// are not stored yet are still fetched. Force wins over Backfill.
	Backfill bool
	// QuotesOnly refreshes only the latest exchange closes.
	QuotesOnly bool
	// Concurrency is the number of ГИР БО tickers fetched in parallel
	// (<= 0: default 3).
	Concurrency int
	// BankFromYear is the earliest bank reporting year (<= 0: default 2020).
	BankFromYear int
}

// FullScope reports whether the run covers every stored company (no explicit
// ticker or bank list), which is what the scheduler's catch-up relies on.
func (r Request) FullScope() bool {
	return r.Tickers == "" && r.TickersFile == "" && r.Banks == ""
}

// Kind is the run kind recorded in fetch_runs.
func (r Request) Kind() string {
	if r.QuotesOnly {
		return KindQuotes
	}
	return KindFinancials
}

// Scope is a short human-readable description of what the run covered.
func (r Request) Scope() string {
	var parts []string
	if r.Tickers != "" {
		parts = append(parts, "tickers="+r.Tickers)
	}
	if r.TickersFile != "" {
		// The base name only: the scope is shown on an open web page.
		parts = append(parts, "file="+filepath.Base(r.TickersFile))
	}
	if r.Banks != "" {
		parts = append(parts, "banks="+r.Banks)
	}
	if len(parts) == 0 {
		if r.All {
			parts = append(parts, "registry")
		} else {
			parts = append(parts, "stored")
		}
	}
	if r.Force {
		parts = append(parts, "force")
	} else if r.Backfill {
		parts = append(parts, "backfill")
	}
	return strings.Join(parts, " ")
}

// Summary is the outcome of one run.
type Summary struct {
	Updated      int      // companies that got new rows
	UpToDate     int      // companies with nothing new to write
	Rows         int      // rows written
	Failed       []string // companies whose financials could not be fetched
	QuotesSaved  int
	QuotesFailed []string // companies with rows but no quote (CSV-only names included)
}

// Run executes one fetch. Per-company failures are reported in the Summary;
// an error means the run as a whole could not proceed.
func Run(ctx context.Context, repo *database.Repository, req Request, logger *slog.Logger) (Summary, error) {
	var sum Summary

	registry, err := loadRegistry()
	if err != nil {
		return sum, fmt.Errorf("load ticker registry: %w", err)
	}
	bankRegistry, err := loadBankRegistry()
	if err != nil {
		return sum, fmt.Errorf("load bank registry: %w", err)
	}

	// Resolve which pipelines to run. An explicit request for one kind
	// suppresses the other; with no list at all, refresh the companies already
	// in the DB (All: every registry entry instead).
	explicitNonBank := req.Tickers != "" || req.TickersFile != ""
	explicitBank := req.Banks != ""
	fetchEverything := !explicitNonBank && !explicitBank
	fetchAllRegistry := fetchEverything && req.All

	var specs []tickerSpec
	var bankSpecs []bankSpec
	switch {
	case fetchAllRegistry:
		specs = registrySpecs(registry)
		bankSpecs = loadBankSpecs(bankRegistry, "")
	case fetchEverything:
		stored, err := repo.GetAllCompaniesWithCategories(ctx)
		if err != nil {
			return sum, fmt.Errorf("list stored companies: %w", err)
		}
		if len(stored) == 0 {
			return sum, ErrNoCompanies
		}
		specs, bankSpecs = storedSpecs(stored, registry, bankRegistry)
	default:
		if explicitNonBank {
			if specs, err = loadTickerSpecs(req, registry); err != nil {
				return sum, err
			}
			// Quotes need only the ticker: a quotes-only run keeps the
			// specs without an INN and makes no lookups.
			if !req.QuotesOnly {
				var unresolved []string
				specs, unresolved = fillMissingINN(ctx, specs, registry, bankRegistry, issINNLookup(logger), logger)
				sum.Failed = append(sum.Failed, unresolved...)
			}
		}
		if explicitBank {
			bankSpecs = loadBankSpecs(bankRegistry, req.Banks)
		}
	}
	if len(specs) == 0 && len(bankSpecs) == 0 && !fetchEverything {
		if len(sum.Failed) > 0 {
			return sum, fmt.Errorf("no tickers could be resolved: %s", strings.Join(sum.Failed, ","))
		}
		return sum, fmt.Errorf("no tickers: give tickers or banks on /updates, or add registry entries")
	}
	if fetchEverything && !fetchAllRegistry && !req.QuotesOnly {
		logger.Info("Refreshing companies stored in the DB (name tickers/banks on /updates to add new ones, all=true for the whole registry)",
			"companies", len(specs), "banks", len(bankSpecs))
	}

	start := time.Now()

	if len(specs) > 0 && !req.QuotesOnly {
		concurrency := req.Concurrency
		if concurrency <= 0 {
			concurrency = defaultConcurrency
		}
		if concurrency > len(specs) {
			concurrency = len(specs)
		}
		logger.Info("Fetching companies (ГИР БО)", "tickers", len(specs), "concurrency", concurrency, "force", req.Force, "backfill", req.Backfill && !req.Force)
		sum.add(fetchAll(ctx, repo, specs, concurrency, req.Force, req.Backfill, logger))
	}

	if len(bankSpecs) > 0 && !req.QuotesOnly && ctx.Err() == nil {
		fromYear := req.BankFromYear
		if fromYear <= 0 {
			fromYear = defaultBankFromYear
		}
		toYear := time.Now().Year()
		logger.Info("Fetching banks (ЦБ формы 102/101)", "tickers", len(bankSpecs), "years", fmt.Sprintf("%d-%d", fromYear, toYear), "force", req.Force, "backfill", req.Backfill && !req.Force)
		sum.add(fetchBanks(ctx, repo, bankSpecs, fromYear, toYear, req.Force, req.Backfill, logger))
	}

	// Latest prices for current valuation: cheap (two ISS calls per ticker),
	// so they are refreshed on every run; QuotesOnly does just this.
	if ctx.Err() == nil {
		companies := make([]string, 0, len(specs)+len(bankSpecs))
		for _, sp := range specs {
			companies = append(companies, strings.ToUpper(sp.Ticker))
		}
		for _, b := range bankSpecs {
			companies = append(companies, strings.ToUpper(b.Ticker))
		}
		// A full run also prices companies that only came from CSV; a name
		// that is not a MOEX secid just fails its quote. Stored names are
		// kept as-is: quotes are keyed by the exact company name.
		if fetchEverything {
			if dbCompanies, err := repo.GetAllCompanies(ctx); err != nil {
				logger.Warn("Quotes: cannot list stored companies", "error", err)
			} else {
				companies = uniqueNames(append(companies, dbCompanies...))
			}
		}
		sum.QuotesSaved, sum.QuotesFailed = fetchQuotes(ctx, repo, newMoexClient(logger), companies, time.Now(), logger)
		sort.Strings(sum.QuotesFailed)
		logger.Info("Quotes updated", "saved", sum.QuotesSaved, "failed", len(sum.QuotesFailed))
		if len(sum.QuotesFailed) > 0 {
			logger.Info("Failed quotes", "list", strings.Join(sum.QuotesFailed, ","))
		}
	}

	if !req.QuotesOnly && ctx.Err() == nil {
		checked := make([]string, 0, len(specs)+len(bankSpecs))
		for _, sp := range specs {
			checked = append(checked, strings.ToUpper(sp.Ticker))
		}
		for _, b := range bankSpecs {
			checked = append(checked, strings.ToUpper(b.Ticker))
		}
		warnCapJumps(ctx, repo, checked, logger)
	}

	sort.Strings(sum.Failed)
	logger.Info("Fetch done",
		"updated", sum.Updated,
		"up_to_date", sum.UpToDate,
		"failed", len(sum.Failed),
		"rows", sum.Rows,
		"duration", time.Since(start).Round(time.Second))
	if len(sum.Failed) > 0 {
		logger.Info("Failed tickers", "list", strings.Join(sum.Failed, ","))
	}
	return sum, ctx.Err()
}

// pipelineResult is one pipeline's per-company tally.
type pipelineResult struct {
	updated, upToDate, rows int
	failed                  []string
}

func (s *Summary) add(p pipelineResult) {
	s.Updated += p.updated
	s.UpToDate += p.upToDate
	s.Rows += p.rows
	s.Failed = append(s.Failed, p.failed...)
}

// outcome is one company's result in a pipeline.
type outcome int

const (
	outcomeFailed   outcome = iota
	outcomeUpdated          // new rows written
	outcomeUpToDate         // nothing new, nothing wrong
)

// classify decides a company's outcome. Any source or save error makes it
// failed — even when some rows were written — so an incomplete fetch shows up
// on the updates page instead of passing as "up to date". Otherwise written
// rows mean updated, and a company with no new rows is up to date only when it
// is known to the source or the DB (known); else nothing was found at all.
func classify(saved, saveErrs int, sourceErr, known bool) outcome {
	switch {
	case sourceErr || saveErrs > 0:
		return outcomeFailed
	case saved > 0:
		return outcomeUpdated
	case known:
		return outcomeUpToDate
	default:
		return outcomeFailed
	}
}
