// Command fetch builds quarterly financials from free primary sources, as a
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
// number (REGN). See "Banks (CBR forms 102/101)" below and the cbr package docs.
//
// Ticker resolution. The legal entity's INN is required (neither MOEX nor ГИР БО
// bridges ticker<->INN), but it no longer has to be retyped: the bundled
// registry (fetch_tickers.txt, embedded via financialanalyzer.TickerRegistry)
// maps every known ticker to its INN and default category. A request may give
// just the ticker, a ticker+category, or the full ticker+INN+category; missing
// fields are filled from the registry.
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
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	financialanalyzer "github.com/VxVxN/financialanalyzer"
	"github.com/VxVxN/financialanalyzer/internal/application"
	"github.com/VxVxN/financialanalyzer/internal/config"
	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
	"github.com/VxVxN/financialanalyzer/internal/scraper/cbr"
	"github.com/VxVxN/financialanalyzer/internal/scraper/girbo"
	"github.com/VxVxN/financialanalyzer/internal/scraper/moex"
)

const (
	defaultConcurrency  = 3
	defaultBankFromYear = 2020

	// equityGrace is how long after a quarter's archive date a missing form
	// 101 counts as "not published yet": such a quarter is not written, so the
	// next run retries it rather than storing it without equity for good.
	equityGrace = 90 * 24 * time.Hour
)

type tickerSpec struct {
	Ticker   string
	INN      string
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

	registry, err := loadRegistry()
	if err != nil {
		return fmt.Errorf("load ticker registry: %w", err)
	}
	bankRegistry, err := loadBankRegistry()
	if err != nil {
		return fmt.Errorf("load bank registry: %w", err)
	}

	// Resolve which pipelines to run. An explicit request for one kind
	// suppresses the other; with no list at all, refresh the companies already
	// in the DB (FETCH_ALL: every registry entry instead).
	bankRaw := os.Getenv("FETCH_BANKS")
	explicitNonBank := os.Getenv("FETCH_TICKERS") != "" || os.Getenv("FETCH_TICKERS_FILE") != ""
	explicitBank := bankRaw != ""
	fetchEverything := !explicitNonBank && !explicitBank
	fetchAllRegistry := fetchEverything && os.Getenv("FETCH_ALL") != ""

	var specs []tickerSpec
	var bankSpecs []bankSpec
	switch {
	case fetchAllRegistry:
		specs = registrySpecs(registry)
		bankSpecs = loadBankSpecs(bankRegistry, "")
	case fetchEverything:
		stored, err := app.Repo.GetAllCompaniesWithCategories(ctx)
		if err != nil {
			return fmt.Errorf("list stored companies: %w", err)
		}
		if len(stored) == 0 {
			return fmt.Errorf("DB has no companies yet: set FETCH_TICKERS / FETCH_TICKERS_FILE / FETCH_BANKS, or FETCH_ALL=1 for the whole registry")
		}
		specs, bankSpecs = storedSpecs(stored, registry, bankRegistry)
	default:
		if explicitNonBank {
			if specs, err = loadTickerSpecs(registry); err != nil {
				return err
			}
		}
		if explicitBank {
			bankSpecs = loadBankSpecs(bankRegistry, bankRaw)
		}
	}
	if len(specs) == 0 && len(bankSpecs) == 0 && !fetchEverything {
		return fmt.Errorf("no tickers: set FETCH_TICKERS / FETCH_TICKERS_FILE / FETCH_BANKS, or add registry entries")
	}
	if fetchEverything && !fetchAllRegistry {
		logger.Info("Refreshing companies stored in the DB (set FETCH_TICKERS / FETCH_BANKS to add new ones, FETCH_ALL=1 for the whole registry)",
			"companies", len(specs), "banks", len(bankSpecs))
	}

	force := os.Getenv("FETCH_FORCE") != ""
	quotesOnly := os.Getenv("FETCH_QUOTES_ONLY") != ""
	start := time.Now()
	var okTotal, rowTotal int
	var failed []string

	if len(specs) > 0 && !quotesOnly {
		concurrency := envInt("FETCH_CONCURRENCY", defaultConcurrency)
		if concurrency > len(specs) {
			concurrency = len(specs)
		}
		logger.Info("Fetching companies (ГИР БО)", "tickers", len(specs), "concurrency", concurrency, "force", force)
		ok, rows, fail := fetchAll(ctx, app.Repo, specs, concurrency, force, logger)
		okTotal += ok
		rowTotal += rows
		failed = append(failed, fail...)
	}

	if len(bankSpecs) > 0 && !quotesOnly && ctx.Err() == nil {
		fromYear := envInt("FETCH_BANK_FROM_YEAR", defaultBankFromYear)
		toYear := time.Now().Year()
		logger.Info("Fetching banks (ЦБ формы 102/101)", "tickers", len(bankSpecs), "years", fmt.Sprintf("%d-%d", fromYear, toYear), "force", force)
		ok, rows, fail := fetchBanks(ctx, app.Repo, bankSpecs, fromYear, toYear, force, logger)
		okTotal += ok
		rowTotal += rows
		failed = append(failed, fail...)
	}

	// Latest prices for current valuation: cheap (two ISS calls per ticker),
	// so they are refreshed on every run; FETCH_QUOTES_ONLY does just this.
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
			if dbCompanies, err := app.Repo.GetAllCompanies(ctx); err != nil {
				logger.Warn("Quotes: cannot list stored companies", "error", err)
			} else {
				companies = uniqueNames(append(companies, dbCompanies...))
			}
		}
		saved, qFailed := fetchQuotes(ctx, app.Repo, newMoexClient(logger), companies, time.Now(), logger)
		logger.Info("Quotes updated", "saved", saved, "failed", len(qFailed))
		if len(qFailed) > 0 {
			sort.Strings(qFailed)
			logger.Info("Failed quotes", "list", strings.Join(qFailed, ","))
		}
	}

	if !quotesOnly && ctx.Err() == nil {
		checked := make([]string, 0, len(specs)+len(bankSpecs))
		for _, sp := range specs {
			checked = append(checked, strings.ToUpper(sp.Ticker))
		}
		for _, b := range bankSpecs {
			checked = append(checked, strings.ToUpper(b.Ticker))
		}
		warnCapJumps(ctx, app.Repo, checked, logger)
	}

	logger.Info("Fetch done",
		"ok", okTotal,
		"failed", len(failed),
		"rows", rowTotal,
		"duration", time.Since(start).Round(time.Second))
	if len(failed) > 0 {
		sort.Strings(failed)
		logger.Info("Failed tickers", "list", strings.Join(failed, ","))
	}
	return nil
}

// uniqueNames drops duplicate names, keeping first order.
func uniqueNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// capJumpRatio is the year-over-year market-cap change beyond which a split
// missing from MOEX's list (and share_splits.txt) is the likely cause.
const capJumpRatio = 4.0

type capJump struct {
	from, to int
	ratio    float64 // later cap / earlier cap
}

// capJumps finds consecutive years whose year-end (latest-quarter) market caps
// differ by more than capJumpRatio in either direction. Splits of 1:2-1:4 and
// ones masked by a same-year price move go unnoticed.
func capJumps(rows []models.QuarterData) []capJump {
	caps := map[int]float64{}
	var years []int
	for _, r := range rows {
		if r.Capitalization != nil && *r.Capitalization > 0 {
			if _, seen := caps[r.Year]; !seen {
				years = append(years, r.Year)
			}
			caps[r.Year] = *r.Capitalization
		}
	}
	sort.Ints(years)
	var out []capJump
	for i := 1; i < len(years); i++ {
		if years[i] != years[i-1]+1 {
			continue
		}
		ratio := caps[years[i]] / caps[years[i-1]]
		if ratio > capJumpRatio || ratio < 1/capJumpRatio {
			out = append(out, capJump{from: years[i-1], to: years[i], ratio: ratio})
		}
	}
	return out
}

// extraSplits are the bundled splits MOEX's list lacks (share_splits.txt). A
// malformed embedded file is a build-time mistake, so it panics at startup.
var extraSplits = func() map[string][]moex.Split {
	m, err := moex.ParseSplitRegistry(financialanalyzer.SplitRegistry)
	if err != nil {
		panic(fmt.Sprintf("share_splits.txt: %v", err))
	}
	return m
}()

// predecessors are the bundled former secids of renamed securities
// (ticker_renames.txt). A malformed embedded file panics at startup.
var predecessors = func() map[string][]string {
	m, err := moex.ParseRenameRegistry(financialanalyzer.RenameRegistry)
	if err != nil {
		panic(fmt.Sprintf("ticker_renames.txt: %v", err))
	}
	return m
}()

// newMoexClient returns a MOEX client that knows the bundled extra splits and
// renamed secids.
func newMoexClient(logger *slog.Logger) *moex.Client {
	c := moex.NewClient()
	c.ExtraSplits = extraSplits
	c.Predecessors = predecessors
	c.Logger = logger
	return c
}

// warnCapJumps checks each company's stored history (after this run's writes,
// so an incremental run that adds one year still sees the pair) for year-end
// caps that jump like an unrecorded split.
func warnCapJumps(ctx context.Context, repo *database.Repository, companies []string, logger *slog.Logger) {
	for _, company := range companies {
		if ctx.Err() != nil {
			return
		}
		history, err := repo.GetCompanyHistory(ctx, company)
		if err != nil {
			logger.Warn("Cap-jump check skipped", "ticker", company, "error", err)
			continue
		}
		for _, j := range capJumps(history) {
			logger.Warn("Market cap jumps between years — possibly an unrecorded split; add it to share_splits.txt",
				"ticker", company, "from", j.from, "to", j.to, "ratio", fmt.Sprintf("%.1fx", j.ratio))
		}
	}
}

// quoteStore is the slice of the repository fetchQuotes needs (a fake in tests).
type quoteStore interface {
	ExistingPeriods(ctx context.Context, company string) (map[string]struct{}, error)
	SaveMarketQuote(ctx context.Context, q models.MarketQuote) error
}

// quoteSource is the MOEX call fetchQuotes needs (a fake in tests).
type quoteSource interface {
	LatestQuote(ctx context.Context, secid string, now time.Time) (moex.Quote, error)
}

// fetchQuotes stores the latest close for each company (the ticker doubles as
// the MOEX secid). Companies without financial rows are skipped — a quote
// alone has nothing to value. Failures are per ticker and non-fatal.
func fetchQuotes(ctx context.Context, repo quoteStore, mx quoteSource, companies []string, now time.Time, logger *slog.Logger) (saved int, failed []string) {
	for _, company := range companies {
		if ctx.Err() != nil {
			break
		}
		periods, err := repo.ExistingPeriods(ctx, company)
		if err != nil {
			logger.Warn("Quote skipped: existing periods", "ticker", company, "error", err)
			failed = append(failed, company)
			continue
		}
		if len(periods) == 0 {
			continue
		}
		q, err := mx.LatestQuote(ctx, company, now)
		if err != nil {
			logger.Warn("Quote unavailable", "ticker", company, "error", err)
			failed = append(failed, company)
			continue
		}
		date, err := time.Parse(time.DateOnly, q.Date)
		if err != nil {
			logger.Warn("Quote has a bad trade date", "ticker", company, "date", q.Date)
			failed = append(failed, company)
			continue
		}
		mq := models.MarketQuote{Company: company, Price: q.Price, Capitalization: q.Capitalization, PriceDate: date}
		if err := repo.SaveMarketQuote(ctx, mq); err != nil {
			logger.Warn("Quote not saved", "ticker", company, "error", err)
			failed = append(failed, company)
			continue
		}
		saved++
	}
	return saved, failed
}

// fetchResult is one ticker's outcome, sent back from a worker to the collector.
type fetchResult struct {
	ticker   string
	category string
	rows     int
	noData   bool
	err      error
}

// fetchAll runs the ticker specs through a pool of workers, each with its own
// rate-limited clients, and returns aggregate counts plus the failed tickers.
func fetchAll(ctx context.Context, repo *database.Repository, specs []tickerSpec, concurrency int, force bool, logger *slog.Logger) (okTickers, totalRows int, failed []string) {
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
		switch {
		case res.err != nil:
			logger.Warn("Fetch failed", "ticker", res.ticker, "error", res.err)
			failed = append(failed, res.ticker)
		case res.noData:
			logger.Warn("No data", "ticker", res.ticker)
			failed = append(failed, res.ticker)
		default:
			okTickers++
			totalRows += res.rows
			logger.Info("Fetched", "ticker", res.ticker, "category", res.category, "rows", res.rows)
		}
	}
	return okTickers, totalRows, failed
}

// fetchOne fetches and saves one ticker, returning its outcome.
func fetchOne(ctx context.Context, repo *database.Repository, bo *girbo.Client, mx *moex.Client, spec tickerSpec, force bool, logger *slog.Logger) fetchResult {
	res := fetchResult{ticker: spec.Ticker, category: spec.Category}

	rows, err := fetchTicker(ctx, repo, bo, mx, spec, force, logger)
	if err != nil {
		res.err = err
		return res
	}
	if len(rows) == 0 {
		res.noData = true
		return res
	}

	for _, r := range rows {
		if err := repo.SaveQuarterData(ctx, r); err != nil {
			logger.Warn("Save failed", "ticker", spec.Ticker, "year", r.Year, "error", err)
			continue
		}
		res.rows++
	}
	if res.rows == 0 {
		res.noData = true
	}
	return res
}

// fetchTicker combines one company's annual RSBU reports with year-end market
// caps into Q4 QuarterData rows, computing P/E and ROE. Periods already stored
// for the company are skipped (along with their market-cap call) unless force.
func fetchTicker(ctx context.Context, repo *database.Repository, bo *girbo.Client, mx *moex.Client, spec tickerSpec, force bool, logger *slog.Logger) ([]models.QuarterData, error) {
	reports, err := bo.FetchAnnual(ctx, spec.INN)
	if err != nil {
		return nil, err
	}

	company := strings.ToUpper(spec.Ticker)

	var existing map[string]struct{}
	if !force {
		existing, err = repo.ExistingPeriods(ctx, company)
		if err != nil {
			return nil, fmt.Errorf("existing periods: %w", err)
		}
	}

	out := make([]models.QuarterData, 0, len(reports))
	for _, r := range reports {
		if _, ok := existing[fmt.Sprintf("%d-Q4", r.Year)]; ok {
			continue // already ingested; skip the network call too
		}

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
		})
	}
	return out, nil
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

// ---- Ticker resolution ------------------------------------------------------

// loadRegistry parses the embedded fetch_tickers.txt into a ticker->spec map.
func loadRegistry() (map[string]tickerSpec, error) {
	registry := make(map[string]tickerSpec)
	scan := bufio.NewScanner(strings.NewReader(financialanalyzer.TickerRegistry))
	for scan.Scan() {
		if spec, ok := parseRegistryLine(scan.Text()); ok {
			registry[spec.Ticker] = spec
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return registry, nil
}

// parseRegistryLine reads one "TICKER INN CATEGORY" registry line; the INN is
// mandatory there (registry entries that lack it are skipped as malformed).
func parseRegistryLine(line string) (tickerSpec, bool) {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return tickerSpec{}, false
	}
	spec := tickerSpec{
		Ticker: strings.ToUpper(fields[0]),
		INN:    fields[1],
	}
	if len(fields) > 2 {
		spec.Category = strings.Join(fields[2:], " ")
	}
	if !isAllDigits(spec.INN) {
		return tickerSpec{}, false
	}
	return spec, true
}

// loadTickerSpecs resolves the ticker list from FETCH_TICKERS or
// FETCH_TICKERS_FILE (the caller checks that one is set).
func loadTickerSpecs(registry map[string]tickerSpec) ([]tickerSpec, error) {
	if raw := os.Getenv("FETCH_TICKERS"); raw != "" {
		return parseTickerSpecList(raw, registry), nil
	}
	if path := os.Getenv("FETCH_TICKERS_FILE"); path != "" {
		return readTickerSpecFile(path, registry)
	}
	return nil, nil
}

// storedSpecs picks the registry entries for companies already in the DB, so a
// default run refreshes what is there instead of pulling the whole registry.
// A stored name must equal the ticker exactly (fetch stores tickers
// upper-cased; anything else would create a second company), and its stored
// category is kept. Companies in neither registry (CSV-only) are skipped here;
// they still get quotes.
func storedSpecs(stored []database.CompanyWithCategory, registry map[string]tickerSpec, bankRegistry map[string]bankSpec) ([]tickerSpec, []bankSpec) {
	var specs []tickerSpec
	var banks []bankSpec
	seen := make(map[string]struct{}, len(stored))
	for _, c := range stored {
		if _, dup := seen[c.Company]; dup {
			continue
		}
		seen[c.Company] = struct{}{}
		if b, ok := bankRegistry[c.Company]; ok {
			if c.Category != "" {
				b.Category = c.Category
			}
			banks = append(banks, b)
			continue
		}
		if s, ok := registry[c.Company]; ok {
			if c.Category != "" {
				s.Category = c.Category
			}
			specs = append(specs, s)
		}
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Ticker < specs[j].Ticker })
	sort.Slice(banks, func(i, j int) bool { return banks[i].Ticker < banks[j].Ticker })
	return specs, banks
}

// registrySpecs returns every registry entry, sorted by ticker for stable runs.
func registrySpecs(registry map[string]tickerSpec) []tickerSpec {
	out := make([]tickerSpec, 0, len(registry))
	for _, spec := range registry {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ticker < out[j].Ticker })
	return out
}

// parseTickerSpecList parses a comma-separated request list, e.g.
// "LKOH,GAZP:oil,MGNT:2309085638:retail".
func parseTickerSpecList(raw string, registry map[string]tickerSpec) []tickerSpec {
	parts := strings.Split(raw, ",")
	specs := make([]tickerSpec, 0, len(parts))
	for _, p := range parts {
		if spec, ok := resolveSpec(splitFields(p), registry); ok {
			specs = append(specs, spec)
		}
	}
	return specs
}

// readTickerSpecFile reads "TICKER [INN] [CATEGORY]" per line; "#" starts a
// comment. Missing INN/category are filled from the registry.
func readTickerSpecFile(path string, registry map[string]tickerSpec) ([]tickerSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var specs []tickerSpec
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if spec, ok := resolveSpec(splitFields(line), registry); ok {
			specs = append(specs, spec)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return specs, nil
}

// splitFields splits a request entry on ":" or, failing that, on whitespace.
func splitFields(s string) []string {
	if strings.Contains(s, ":") {
		return strings.Split(s, ":")
	}
	return strings.Fields(s)
}

// resolveSpec turns request fields ("TICKER", "TICKER CATEGORY", or
// "TICKER INN CATEGORY") into a full spec, filling INN/category from the
// registry when omitted. An all-digit field is treated as the INN, any other
// trailing field as the category. Returns false when the INN cannot be
// resolved (unknown ticker and none supplied).
func resolveSpec(fields []string, registry map[string]tickerSpec) (tickerSpec, bool) {
	if len(fields) == 0 {
		return tickerSpec{}, false
	}
	ticker := strings.ToUpper(strings.TrimSpace(fields[0]))
	if ticker == "" {
		return tickerSpec{}, false
	}

	// Start from the registry default (zero value if the ticker is unknown).
	base := registry[ticker]
	spec := tickerSpec{Ticker: ticker, INN: base.INN, Category: base.Category}

	var catTokens []string
	for _, f := range fields[1:] {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if isAllDigits(f) {
			spec.INN = f
			continue
		}
		catTokens = append(catTokens, f)
	}
	if len(catTokens) > 0 {
		spec.Category = strings.Join(catTokens, " ")
	}

	if spec.INN == "" {
		return tickerSpec{}, false
	}
	return spec, true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// ---- Banks (CBR form 102) ---------------------------------------------------

type bankSpec struct {
	Ticker   string
	Regn     int
	Category string
}

// loadBankRegistry parses the embedded bank_tickers.txt into a ticker->spec map.
func loadBankRegistry() (map[string]bankSpec, error) {
	registry := make(map[string]bankSpec)
	scan := bufio.NewScanner(strings.NewReader(financialanalyzer.BankRegistry))
	for scan.Scan() {
		line := scan.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		regn, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		spec := bankSpec{Ticker: strings.ToUpper(fields[0]), Regn: regn}
		if len(fields) > 2 {
			spec.Category = strings.Join(fields[2:], " ")
		}
		registry[spec.Ticker] = spec
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return registry, nil
}

// loadBankSpecs returns the requested banks (comma-separated tickers) resolved
// from the registry, or every registry entry when raw is empty.
func loadBankSpecs(registry map[string]bankSpec, raw string) []bankSpec {
	if raw == "" {
		out := make([]bankSpec, 0, len(registry))
		for _, s := range registry {
			out = append(out, s)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Ticker < out[j].Ticker })
		return out
	}
	var out []bankSpec
	for _, t := range strings.Split(raw, ",") {
		if spec, ok := registry[strings.ToUpper(strings.TrimSpace(t))]; ok {
			out = append(out, spec)
		}
	}
	return out
}

var bankQuarters = []string{"Q1", "Q2", "Q3", "Q4"}

// fetchBanks ingests banks' quarterly figures from the CBR. For each year it
// downloads the four cumulative YTD form 102 archives once (shared across all
// banks), differences them into single-quarter net profit and revenue per
// bank, adds each quarter-end's balance-sheet equity from form 101, and
// attaches year-end market cap + annual P/E + ROE to the Q4 (full-year) row.
// Periods already stored are skipped, and whole years already complete are not
// even downloaded, unless force is set.
func fetchBanks(ctx context.Context, repo *database.Repository, specs []bankSpec, fromYear, toYear int, force bool, logger *slog.Logger) (okBanks, totalRows int, failed []string) {
	cb := cbr.NewClient()
	mx := newMoexClient(logger)
	capAt := func(ticker string, year int) *float64 {
		marketCap, err := mx.CapitalizationAt(ctx, ticker, year)
		if err != nil {
			logger.Warn("Capitalization unavailable", "ticker", ticker, "year", year, "error", err)
		}
		return capitalization(marketCap, err)
	}

	existing := make(map[string]map[string]struct{}, len(specs))
	if !force {
		for _, s := range specs {
			set, err := repo.ExistingPeriods(ctx, s.Ticker)
			if err != nil {
				logger.Warn("Existing periods lookup failed", "ticker", s.Ticker, "error", err)
			}
			existing[s.Ticker] = set
		}
	}

	saved := make(map[string]int, len(specs))
	now := time.Now()

	for year := fromYear; year <= toYear; year++ {
		if ctx.Err() != nil {
			break
		}
		if !force && yearFullyStored(specs, existing, year) {
			continue
		}

		cum, canceled := fetchYearCumulatives(ctx, cb, year, logger)
		if canceled {
			break
		}
		if len(cum) == 0 {
			continue
		}

		// Quarter-end balance-sheet equity (form 101) for the quarters that
		// will be written.
		needed := func(q string) bool { return force || !quarterFullyStored(specs, existing, year, q) }
		equity, pending, canceled := fetchYearEquity(ctx, cb, year, cum, needed, now, logger)
		if canceled {
			break
		}

		for _, s := range specs {
			for _, r := range bankRows(s, year, cum, equity, pending, existing[s.Ticker], capAt) {
				if err := repo.SaveQuarterData(ctx, r); err != nil {
					logger.Warn("Save failed", "ticker", s.Ticker, "year", r.Year, "quarter", r.Quarter, "error", err)
					continue
				}
				saved[s.Ticker]++
				totalRows++
			}
		}
	}

	for _, s := range specs {
		if n := saved[s.Ticker]; n > 0 {
			okBanks++
			logger.Info("Fetched bank", "ticker", s.Ticker, "category", s.Category, "rows", n)
		} else {
			failed = append(failed, s.Ticker)
		}
	}
	return okBanks, totalRows, failed
}

// isCanceled reports whether err comes from a canceled or expired context.
func isCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// fetchYearCumulatives downloads the four cumulative YTD form 102 archives for a
// year, returning quarter -> (regn -> cumulative figures, billions). Missing
// (unpublished) quarters are simply absent. canceled is true if the context was
// canceled mid-download, telling the caller to stop.
func fetchYearCumulatives(ctx context.Context, cb *cbr.Client, year int, logger *slog.Logger) (cum map[string]map[int]cbr.Form102, canceled bool) {
	cum = make(map[string]map[int]cbr.Form102, len(bankQuarters))
	for _, q := range bankQuarters {
		date, _ := cbr.ArchiveDate(year, q)
		m, err := cb.FetchPeriod(ctx, date)
		switch {
		case errors.Is(err, cbr.ErrNotPublished):
			continue
		case isCanceled(err):
			return cum, true
		case err != nil:
			logger.Warn("Period fetch failed", "archive", date, "error", err)
			continue
		}
		cum[q] = m
	}
	return cum, false
}

// fetchYearEquity downloads the form 101 archive at the end of each quarter that
// has form 102 figures and is needed (some requested bank still lacks it), and
// returns quarter -> (regn -> balance-sheet equity, billions). A quarter whose
// archive is missing or fails is absent from equity; if it is recent (see
// equityPending) it is also reported in pending, and its rows are held back
// until a later run finds the archive. Older quarters are written without
// equity (and a Q4 row without ROE). canceled is true if the context was
// canceled mid-download.
func fetchYearEquity(ctx context.Context, cb *cbr.Client, year int, cum map[string]map[int]cbr.Form102, needed func(quarter string) bool, now time.Time, logger *slog.Logger) (equity map[string]map[int]float64, pending map[string]bool, canceled bool) {
	equity = make(map[string]map[int]float64, len(cum))
	pending = make(map[string]bool)
	for _, q := range bankQuarters {
		if len(cum[q]) == 0 || !needed(q) {
			continue
		}
		date, _ := cbr.ArchiveDate(year, q) // balance at the quarter end
		m, err := cb.FetchEquity(ctx, date)
		switch {
		case isCanceled(err):
			return equity, pending, true
		case err != nil:
			if !errors.Is(err, cbr.ErrNotPublished) {
				logger.Warn("Equity fetch failed", "archive", date, "error", err)
			}
			if equityPending(date, now) {
				pending[q] = true
				logger.Info("Form 101 not available yet; quarter deferred to a later run", "archive", date)
			}
			continue
		}
		equity[q] = m
	}
	return equity, pending, false
}

// equityPending reports whether a form 101 archive date ("YYYYMMDD") is recent
// enough (within equityGrace of now) that a missing archive is probably just
// not published yet.
func equityPending(archiveDate string, now time.Time) bool {
	d, err := time.Parse("20060102", archiveDate)
	if err != nil {
		return false
	}
	return now.Before(d.Add(equityGrace))
}

// bankRows differences the cumulative YTD net profit and revenue into
// single-quarter rows for one bank and year, each with its quarter-end
// balance-sheet equity. Quarters in pending (form 101 not published yet) are
// held back, though their cumulatives still difference the next quarter. The
// Q4 row additionally carries year-end market cap
// (capAt, called only for a Q4 row that will be written), the annual P/E (cap /
// full-year net profit) and ROE (full-year profit / year-end equity). Quarters
// whose cumulative — or the preceding one needed to difference it — is
// unavailable are skipped, as are periods already stored and rows that would
// carry no value.
func bankRows(s bankSpec, year int, cum map[string]map[int]cbr.Form102, equity map[string]map[int]float64, pending map[string]bool, existing map[string]struct{}, capAt func(ticker string, year int) *float64) []models.QuarterData {
	// Cumulative figures for this bank at each quarter, with a presence flag.
	cumAt := func(q string) (cbr.Form102, bool) {
		v, ok := cum[q][s.Regn]
		return v, ok
	}
	prevQuarter := map[string]string{"Q2": "Q1", "Q3": "Q2", "Q4": "Q3"}

	var out []models.QuarterData
	for _, q := range bankQuarters {
		cur, ok := cumAt(q)
		if !ok {
			continue
		}

		quarter := cur
		if q != "Q1" {
			prev, ok := cumAt(prevQuarter[q])
			if !ok {
				continue // cannot difference without the previous cumulative
			}
			quarter = cbr.Form102{
				NetProfit: cur.NetProfit - prev.NetProfit,
				Revenue:   cur.Revenue - prev.Revenue,
			}
		}

		if _, done := existing[fmt.Sprintf("%d-%s", year, q)]; done || pending[q] {
			continue
		}

		row := models.QuarterData{
			Year:      year,
			Quarter:   q,
			Company:   s.Ticker,
			Category:  s.Category,
			Source:    models.SourceCBR102,
			NetProfit: models.Float(quarter.NetProfit),
			Revenue:   models.Float(quarter.Revenue),
		}
		if e, ok := equity[q][s.Regn]; ok {
			row.Equity = models.Float(e)
		}
		if q == "Q4" {
			row.Capitalization = capAt(s.Ticker, year)
			annual := models.Float(cur.NetProfit)
			row.PE = peRatio(row.Capitalization, annual) // annual P/E uses full-year profit
			row.ROE = roePercent(annual, row.Equity)     // full-year profit / year-end equity
		}

		if row.IsEmpty() {
			continue
		}
		out = append(out, row)
	}
	return out
}

// yearFullyStored reports whether every bank already has all four quarters of a
// year in the DB, so the year's archives need not be downloaded again.
func yearFullyStored(specs []bankSpec, existing map[string]map[string]struct{}, year int) bool {
	for _, q := range bankQuarters {
		if !quarterFullyStored(specs, existing, year, q) {
			return false
		}
	}
	return true
}

// quarterFullyStored reports whether every bank already has the given quarter
// in the DB.
func quarterFullyStored(specs []bankSpec, existing map[string]map[string]struct{}, year int, quarter string) bool {
	key := fmt.Sprintf("%d-%s", year, quarter)
	for _, s := range specs {
		if _, ok := existing[s.Ticker][key]; !ok {
			return false
		}
	}
	return true
}
