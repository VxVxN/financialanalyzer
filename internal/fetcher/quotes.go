package fetcher

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/VxVxN/financialanalyzer"
	"github.com/VxVxN/financialanalyzer/internal/analytics"
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
	var reviewed map[string][]analytics.CapReview
	if all, err := repo.CapReviews(ctx); err != nil {
		logger.Warn("Cap-jump reviews unavailable", "error", err)
	} else {
		reviewed = analytics.GroupCapReviews(all)
	}
	for _, company := range companies {
		if ctx.Err() != nil {
			return
		}
		history, err := repo.GetCompanyHistory(ctx, company)
		if err != nil {
			logger.Warn("Cap-jump check skipped", "ticker", company, "error", err)
			continue
		}
		for _, j := range analytics.CapJumps(history) {
			if analytics.ReviewKind(reviewed[company], j.From, j.To) != "" {
				continue
			}
			logger.Warn("Market cap jumps between years — possibly an unrecorded split, extra issue or buyback; add a split to share_splits.txt",
				"ticker", company, "from", j.From, "to", j.To, "ratio", fmt.Sprintf("%.1fx", j.Ratio))
		}
	}
}

// quoteStore is the slice of the repository fetchQuotes needs (a fake in tests).
type quoteStore interface {
	ExistingPeriods(ctx context.Context, company string) (map[string]struct{}, error)
	GetMarketQuote(ctx context.Context, company string) (models.MarketQuote, bool, error)
	SaveMarketQuote(ctx context.Context, q models.MarketQuote) error
}

// quoteSource is the MOEX call fetchQuotes needs (a fake in tests).
type quoteSource interface {
	LatestQuote(ctx context.Context, secid string, now time.Time) (moex.Quote, error)
	Splits(ctx context.Context, secid string) ([]moex.Split, error)
}

// shareChangeRatio is the smallest relative move in shares outstanding, after
// known splits, that is worth a warning. Smaller moves are rounding.
const shareChangeRatio = 0.01

// unexplainedShares reports whether next shares differ from prev by more than
// shareChangeRatio once every split whose first post-split session falls
// strictly after prevDate and on or before nextDate has been applied.
// Dates are YYYY-MM-DD. ratio is next/expected (1 when the change is a known
// split). unexplained is false when there is nothing to compare.
func unexplainedShares(prevShares, nextShares float64, prevDate, nextDate string, splits []moex.Split) (ratio float64, unexplained bool) {
	if prevShares <= 0 || nextShares <= 0 || prevDate == "" || nextDate == "" || nextDate < prevDate {
		return 0, false
	}
	expected := prevShares
	for _, sp := range splits {
		if sp.TradeDate > prevDate && sp.TradeDate <= nextDate && sp.Before > 0 && sp.After > 0 {
			expected *= sp.After / sp.Before
		}
	}
	if expected <= 0 {
		return 0, false
	}
	ratio = nextShares / expected
	if ratio > 1+shareChangeRatio || ratio < 1/(1+shareChangeRatio) {
		return ratio, true
	}
	return ratio, false
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
		if prev, ok, err := repo.GetMarketQuote(ctx, company); err != nil {
			logger.Warn("Share-count check skipped", "ticker", company, "error", err)
		} else if ok && q.Shares > 0 {
			splits, serr := mx.Splits(ctx, company)
			if serr != nil {
				logger.Warn("Share-count check skipped: split list unavailable", "ticker", company, "error", serr)
			} else if ratio, bad := unexplainedShares(prev.Shares, q.Shares, prev.PriceDate.Format(time.DateOnly), q.Date, splits); bad {
				logger.Warn("Share count changed without a matching split — possible extra issue, buyback, or a missing share_splits.txt row",
					"ticker", company, "from", prev.Shares, "to", q.Shares, "residual", fmt.Sprintf("%.2fx", ratio))
			}
		}
		mq := models.MarketQuote{
			Company: company, Price: q.Price, Capitalization: q.Capitalization,
			Shares: q.Shares, Turnover: q.Turnover, PriceDate: date,
		}
		if err := repo.SaveMarketQuote(ctx, mq); err != nil {
			logger.Warn("Quote not saved", "ticker", company, "error", err)
			failed = append(failed, company)
			continue
		}
		saved++
	}
	return saved, failed
}
