package fetcher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer"
	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
	"github.com/VxVxN/financialanalyzer/internal/scraper/cbr"
)

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
// even downloaded, unless force is set. Outcomes (see classify): a download
// error other than "not published yet" fails every bank (all of them miss that
// period), a save error fails its bank, and a bank with no new rows is up to
// date when it appears in this run's form 102 archives or nothing needed
// downloading — otherwise it is failed (e.g. a wrong REGN or a revoked
// license).
func fetchBanks(ctx context.Context, repo *database.Repository, specs []bankSpec, fromYear, toYear int, force bool, logger *slog.Logger) (out pipelineResult) {
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
	saveErrs := make(map[string]int, len(specs))
	seen := make(map[string]bool, len(specs)) // present in a downloaded form 102
	downloaded, sourceErr := false, false
	now := time.Now()

	for year := fromYear; year <= toYear; year++ {
		if ctx.Err() != nil {
			break
		}
		if !force && yearFullyStored(specs, existing, year) {
			continue
		}

		cum, failed, canceled := fetchYearCumulatives(ctx, cb, year, logger)
		sourceErr = sourceErr || failed
		if canceled {
			break
		}
		downloaded = downloaded || len(cum) > 0
		if len(cum) == 0 {
			continue
		}

		// Quarter-end balance-sheet equity (form 101) for the quarters that
		// will be written.
		needed := func(q string) bool { return force || !quarterFullyStored(specs, existing, year, q) }
		equity, pending, failed, canceled := fetchYearEquity(ctx, cb, year, cum, needed, now, logger)
		sourceErr = sourceErr || failed
		if canceled {
			break
		}

		for _, s := range specs {
			for _, m := range cum {
				if _, ok := m[s.Regn]; ok {
					seen[s.Ticker] = true
					break
				}
			}
			for _, r := range bankRows(s, year, cum, equity, pending, existing[s.Ticker], capAt) {
				if err := repo.SaveQuarterData(ctx, r); err != nil {
					logger.Warn("Save failed", "ticker", s.Ticker, "year", r.Year, "quarter", r.Quarter, "error", err)
					saveErrs[s.Ticker]++
					continue
				}
				saved[s.Ticker]++
				out.rows++
			}
		}
	}

	for _, s := range specs {
		n := saved[s.Ticker]
		known := seen[s.Ticker] || (!downloaded && ctx.Err() == nil)
		switch classify(n, saveErrs[s.Ticker], sourceErr, known) {
		case outcomeUpdated:
			out.updated++
			logger.Info("Fetched bank", "ticker", s.Ticker, "category", s.Category, "rows", n)
		case outcomeUpToDate:
			out.upToDate++
		default:
			logger.Warn("Bank fetch incomplete", "ticker", s.Ticker, "rows", n, "save_errors", saveErrs[s.Ticker], "source_errors", sourceErr, "in_archives", seen[s.Ticker])
			out.failed = append(out.failed, s.Ticker)
		}
	}
	return out
}

// isCanceled reports whether err comes from a canceled or expired context.
func isCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// fetchYearCumulatives downloads the four cumulative YTD form 102 archives for a
// year, returning quarter -> (regn -> cumulative figures, billions). Missing
// (unpublished) quarters are simply absent; failed reports any other download
// error. canceled is true if the context was canceled mid-download, telling
// the caller to stop.
func fetchYearCumulatives(ctx context.Context, cb *cbr.Client, year int, logger *slog.Logger) (cum map[string]map[int]cbr.Form102, failed, canceled bool) {
	cum = make(map[string]map[int]cbr.Form102, len(bankQuarters))
	for _, q := range bankQuarters {
		date, _ := cbr.ArchiveDate(year, q)
		m, err := cb.FetchPeriod(ctx, date)
		switch {
		case errors.Is(err, cbr.ErrNotPublished):
			continue
		case isCanceled(err):
			return cum, failed, true
		case err != nil:
			logger.Warn("Period fetch failed", "archive", date, "error", err)
			failed = true
			continue
		}
		cum[q] = m
	}
	return cum, failed, false
}

// fetchYearEquity downloads the form 101 archive at the end of each quarter that
// has form 102 figures and is needed (some requested bank still lacks it), and
// returns quarter -> (regn -> balance-sheet equity, billions). A quarter whose
// archive is missing or fails is absent from equity; if it is recent (see
// equityPending) it is also reported in pending, and its rows are held back
// until a later run finds the archive. Older quarters are written without
// equity (and a Q4 row without ROE). failed reports a download error other
// than "not published yet"; canceled is true if the context was canceled
// mid-download.
func fetchYearEquity(ctx context.Context, cb *cbr.Client, year int, cum map[string]map[int]cbr.Form102, needed func(quarter string) bool, now time.Time, logger *slog.Logger) (equity map[string]map[int]float64, pending map[string]bool, failed, canceled bool) {
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
			return equity, pending, failed, true
		case err != nil:
			if !errors.Is(err, cbr.ErrNotPublished) {
				logger.Warn("Equity fetch failed", "archive", date, "error", err)
				failed = true
			}
			if equityPending(date, now) {
				pending[q] = true
				logger.Info("Form 101 not available yet; quarter deferred to a later run", "archive", date)
			}
			continue
		}
		equity[q] = m
	}
	return equity, pending, failed, false
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
