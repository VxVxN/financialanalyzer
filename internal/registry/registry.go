// Package registry builds a candidate ГИР БО ticker registry (the format of
// fetch_tickers.txt) from primary sources, replacing the hand search for
// issuer INNs:
//
//  1. MOEX ISS lists the shares on the main board (TQBR) and gives each one's
//     issuer INN; preferred shares fold into their issuer's common share.
//  2. ГИР БО is checked for each issuer: a recent annual report with a real
//     revenue line makes it a candidate, otherwise the reason it is left out
//     is recorded (closed reporting, a holding with no operating revenue, ...).
//  3. The category comes from MOEX sector index membership.
//
// The output is a proposal for a human to diff against the bundled registry
// and commit; nothing here writes the registry itself.
package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/scraper/girbo"
	"github.com/VxVxN/financialanalyzer/internal/scraper/moex"
)

// Exchange is the MOEX ISS side (implemented by *moex.Client).
type Exchange interface {
	BoardSecurities(ctx context.Context) ([]moex.BoardSecurity, error)
	EmitterByISIN(ctx context.Context, secid, isin string) (moex.Emitter, error)
	IndexConstituents(ctx context.Context, index string) ([]string, error)
}

// Filings is the ГИР БО side (implemented by *girbo.Client).
type Filings interface {
	FetchAnnual(ctx context.Context, inn string, skip func(year int) bool) ([]girbo.AnnualReport, error)
}

// SectorIndexes maps MOEX sector indexes to registry categories, in priority
// order for a share listed in more than one.
var SectorIndexes = []struct{ Index, Category string }{
	{"MOEXOG", "oil"},
	{"MOEXEU", "energy"},
	{"MOEXTL", "telecom"},
	{"MOEXMM", "metals"},
	{"MOEXFN", "finance"},
	{"MOEXCN", "consumer"},
	{"MOEXCH", "chemicals"},
	{"MOEXIT", "it"},
	{"MOEXRE", "realestate"},
	{"MOEXTN", "transport"},
}

// DefaultCategory is given to a share in no sector index.
const DefaultCategory = "other"

// MinRevenue is the parent-entity RSBU revenue (billions of RUB) below which
// an issuer is left out: such a holding's figures say nothing about the
// business (MGNT: the revenue sits in АО "Тандер").
const MinRevenue = 1.0

// recentYears is how many years back the latest report may be: in October
// 2026 a 2025 or 2024 report counts, an older one means reporting stopped.
const recentYears = 2

// Verdict is the decision on one issuer.
type Verdict int

const (
	Include Verdict = iota // a usable candidate
	Exclude                // left out for a stated reason
	Failed                 // a source request failed; rerun to decide
)

// Candidate is one issuer's entry.
type Candidate struct {
	Ticker    string // the common share's secid (a preferred one when only it trades)
	Preferred []string
	INN       string
	Title     string
	Category  string
	Verdict   Verdict
	Reason    string              // why Exclude/Failed; "" for Include
	Holding   bool                // Include whose net profit exceeds revenue
	Latest    *girbo.AnnualReport // the most recent report checked (nil when none)
	Known     bool                // already in the bundled registry
	Previous  string              // the bundled registry's INN when it differs
	secType   string              // Ticker's SECTYPE
}

// KnownEntry is a bundled registry row.
type KnownEntry struct {
	INN      string
	Category string
}

// Options tunes Build.
type Options struct {
	Now     time.Time
	Banks   map[string]struct{}   // tickers of the bank registry: skipped
	Known   map[string]KnownEntry // the bundled registry, by ticker
	Tickers map[string]struct{}   // when non-empty, only these secids are checked
	Logger  *slog.Logger
}

// Build checks every share on the board and returns one candidate per issuer,
// sorted by ticker. Only a failed board listing is an error; per-issuer
// failures become Failed candidates, and a sector index that fails to load
// only costs its members their category (with a warning).
func Build(ctx context.Context, ex Exchange, filings Filings, opts Options) ([]Candidate, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}

	board, err := ex.BoardSecurities(ctx)
	if err != nil {
		return nil, err
	}
	sectors := sectorsOf(ctx, ex, logger)

	byINN := make(map[string]*Candidate)
	seen := make(map[string]struct{})
	var out []*Candidate
	shares := 0
	for _, sec := range board {
		if !sec.IsShare() {
			continue
		}
		ticker := strings.ToUpper(sec.SecID)
		if _, dup := seen[ticker]; dup {
			continue
		}
		seen[ticker] = struct{}{}
		if len(opts.Tickers) > 0 {
			if _, ok := opts.Tickers[ticker]; !ok {
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		shares++
		if _, bank := opts.Banks[ticker]; bank {
			out = append(out, &Candidate{Ticker: ticker, Verdict: Exclude, Reason: "банк: отчётность в ЦБ (bank_tickers.txt)"})
			continue
		}
		e, err := ex.EmitterByISIN(ctx, ticker, sec.ISIN)
		switch {
		case errors.Is(err, moex.ErrNoEmitter):
			out = append(out, &Candidate{Ticker: ticker, Verdict: Exclude, Reason: "ISS не знает ИНН эмитента (иностранный эмитент?)"})
			continue
		case err != nil:
			out = append(out, &Candidate{Ticker: ticker, Verdict: Failed, Reason: "ISS: " + err.Error()})
			continue
		case !isINN(e.INN):
			out = append(out, &Candidate{Ticker: ticker, Verdict: Exclude, Reason: fmt.Sprintf("ИНН эмитента %q не российский", e.INN)})
			continue
		}
		if c, ok := byINN[e.INN]; ok {
			// Another share of the same issuer: the common one names the
			// company, the others are listed.
			if sec.SecType == moex.SecTypeCommon && c.secType != moex.SecTypeCommon {
				c.Preferred = append(c.Preferred, c.Ticker)
				c.Ticker, c.secType = ticker, sec.SecType
			} else {
				c.Preferred = append(c.Preferred, ticker)
			}
			continue
		}
		c := &Candidate{Ticker: ticker, INN: e.INN, Title: e.Title, secType: sec.SecType}
		byINN[e.INN] = c
		out = append(out, c)
	}
	logger.Info("Shares on the board", "checked", shares, "issuers", len(byINN))

	for _, c := range out {
		c.Category = categoryOf(sectors, append([]string{c.Ticker}, c.Preferred...))
		matchKnown(c, opts.Known)
	}
	for i, c := range out {
		if c.INN == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		checkFilings(ctx, filings, c, opts.Now)
		logger.Info("Checked issuer", "n", i+1, "of", len(out), "ticker", c.Ticker, "verdict", c.Verdict.String(), "reason", c.Reason)
	}

	res := make([]Candidate, len(out))
	for i, c := range out {
		sort.Strings(c.Preferred)
		res[i] = *c
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Ticker < res[j].Ticker })
	return res, nil
}

// matchKnown links c to its bundled registry row. The registry's ticker wins
// over the common share's (a stored company is keyed by it, so renaming it
// would orphan the stored history), and its category wins over the sector
// index's (sector medians group by the category string).
func matchKnown(c *Candidate, known map[string]KnownEntry) {
	k, ok := known[c.Ticker]
	if !ok {
		for i, p := range c.Preferred {
			if k, ok = known[p]; ok {
				c.Preferred[i], c.Ticker = c.Ticker, p
				break
			}
		}
	}
	if !ok {
		return
	}
	c.Known = true
	if k.Category != "" {
		c.Category = k.Category
	}
	if c.INN != "" && k.INN != c.INN {
		c.Previous = k.INN
	}
}

// isINN reports whether s looks like a Russian INN (10 digits for an
// organization, 12 for an individual).
func isINN(s string) bool {
	if len(s) != 10 && len(s) != 12 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// checkFilings sets c's verdict from its ГИР БО reports of the recent years.
func checkFilings(ctx context.Context, filings Filings, c *Candidate, now time.Time) {
	oldest := now.Year() - recentYears
	reports, err := filings.FetchAnnual(ctx, c.INN, func(year int) bool { return year < oldest })
	var partial *girbo.PartialError
	switch {
	case errors.Is(err, girbo.ErrNoOrganization):
		c.Verdict, c.Reason = Exclude, "нет в ГИР БО"
		return
	case err != nil && !errors.As(err, &partial):
		c.Verdict, c.Reason = Failed, "ГИР БО: "+err.Error()
		return
	}
	if len(reports) == 0 { // FetchAnnual never pairs a PartialError with no reports
		c.Verdict, c.Reason = Exclude, fmt.Sprintf("нет отчётности РСБУ за %d–%d (публикация закрыта или прекращена)", oldest, now.Year()-1)
		return
	}
	latest := reports[0] // most recent first
	c.Latest = &latest
	if partial != nil && latest.Year < now.Year()-1 {
		// A newer year's details failed: deciding on an older one could
		// exclude an issuer whose latest report would include it.
		c.Verdict, c.Reason = Failed, fmt.Sprintf("ГИР БО: последний полученный отчёт за %d, более новый не загрузился: %s", latest.Year, partial.Error())
		return
	}
	switch {
	case latest.Revenue == nil || *latest.Revenue < MinRevenue:
		c.Verdict, c.Reason = Exclude, fmt.Sprintf("выручка РСБУ %d: %s — холдинг без операционной выручки", latest.Year, fmtBln(latest.Revenue))
	default:
		c.Verdict = Include
		c.Holding = latest.NetProfit != nil && *latest.NetProfit > *latest.Revenue
	}
}

// sectorsOf loads every sector index: secid -> categories in SectorIndexes
// order.
func sectorsOf(ctx context.Context, ex Exchange, logger *slog.Logger) map[string][]string {
	out := make(map[string][]string)
	for _, s := range SectorIndexes {
		members, err := ex.IndexConstituents(ctx, s.Index)
		if err != nil {
			logger.Warn("Sector index unavailable: its members get no category", "index", s.Index, "error", err)
			continue
		}
		for _, m := range members {
			m = strings.ToUpper(m)
			out[m] = append(out[m], s.Category)
		}
	}
	return out
}

// categoryOf is the first sector category of any of the issuer's shares.
func categoryOf(sectors map[string][]string, secids []string) string {
	for _, id := range secids {
		if cats := sectors[id]; len(cats) > 0 {
			return cats[0]
		}
	}
	return DefaultCategory
}

func (v Verdict) String() string {
	switch v {
	case Include:
		return "include"
	case Exclude:
		return "exclude"
	default:
		return "failed"
	}
}

// fmtBln formats billions of RUB for a comment ("н/д" when absent).
func fmtBln(v *float64) string {
	if v == nil {
		return "н/д"
	}
	return fmt.Sprintf("%.1f млрд", *v)
}
