package fetcher

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/VxVxN/financialanalyzer"
	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
	"github.com/VxVxN/financialanalyzer/internal/scraper/moex"
)

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
