package ifrs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// Store is the manual-entry table. Save replaces a company-year, so the run
// reads the current row and writes back the fields it did not itself find.
type Store interface {
	GetAllCompanies(ctx context.Context) ([]string, error)
	GetManualFinancials(ctx context.Context, company string) ([]models.ManualFinancials, error)
	SaveManualFinancials(ctx context.Context, m models.ManualFinancials) error
}

// Source fetches the text of one company-year report.
type Source interface {
	Text(ctx context.Context, ticker string, year int) (string, error)
}

// Result is what one button press changed. Counts are company-years.
type Result struct {
	Saved     int
	Unchanged int
	Missing   int
	Failed    int
}

// Status is the poll payload for the updates page. Message is Russian;
// it is shown as-is.
type Status struct {
	Running   bool   `json:"running"`
	Saved     int    `json:"saved"`
	Unchanged int    `json:"unchanged"`
	Missing   int    `json:"missing"`
	Failed    int    `json:"failed"`
	Message   string `json:"message"`
}

// Status renders Result for the page.
func (r Result) Status() Status {
	s := Status{Saved: r.Saved, Unchanged: r.Unchanged, Missing: r.Missing, Failed: r.Failed}
	if r.Saved == 0 && r.Unchanged == 0 && r.Missing == 0 && r.Failed == 0 {
		s.Message = "В базе нет компаний."
		return s
	}
	s.Message = fmt.Sprintf("Дописано лет: %d. Без новых цифр: %d. Не найдено отчётов: %d.", r.Saved, r.Unchanged, r.Missing)
	if r.Failed > 0 {
		s.Message += fmt.Sprintf(" Ошибок: %d.", r.Failed)
	}
	return s
}

// Years is the annual-report window the button asks for: the five years
// ending with the last completed calendar year.
func Years(now time.Time) []int {
	end := now.Year() - 1
	start := end - 4
	out := make([]int, 0, 5)
	for y := start; y <= end; y++ {
		out = append(out, y)
	}
	return out
}

// Run downloads annual IFRS for each company and fills empty manual fields.
// A figure that is already stored is left as it was. companies nil means
// every company in the store.
func Run(ctx context.Context, store Store, src Source, companies []string, years []int, logger *slog.Logger) Result {
	if logger == nil {
		logger = slog.Default()
	}
	var res Result
	if companies == nil {
		var err error
		companies, err = store.GetAllCompanies(ctx)
		if err != nil {
			logger.Error("ifrs: list companies", "error", err)
			res.Failed++
			return res
		}
	}
	if len(companies) == 0 || len(years) == 0 {
		return res
	}
	for _, company := range companies {
		if ctx.Err() != nil {
			return res
		}
		existing, err := store.GetManualFinancials(ctx, company)
		if err != nil {
			logger.Error("ifrs: read manual entries", "company", company, "error", err)
			res.Failed += len(years)
			continue
		}
		byYear := make(map[int]models.ManualFinancials, len(existing))
		for _, m := range existing {
			byYear[m.Year] = m
		}
		for _, year := range years {
			if ctx.Err() != nil {
				return res
			}
			text, err := src.Text(ctx, company, year)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					res.Missing++
					continue
				}
				logger.Warn("ifrs: download failed", "company", company, "year", year, "error", err)
				res.Failed++
				continue
			}
			parsed, ok := Parse(text)
			if !ok {
				logger.Info("ifrs: no figures in report", "company", company, "year", year)
				res.Missing++
				continue
			}
			merged, n := fillGaps(byYear[year], parsed)
			if n == 0 {
				res.Unchanged++
				continue
			}
			merged.Company = company
			merged.Year = year
			if err := store.SaveManualFinancials(ctx, merged); err != nil {
				logger.Warn("ifrs: save failed", "company", company, "year", year, "error", err)
				res.Failed++
				continue
			}
			byYear[year] = merged
			res.Saved++
			logger.Info("ifrs: filled manual entry", "company", company, "year", year, "fields", n)
		}
	}
	return res
}

// fillGaps copies parsed figures into the fields the stored row left empty.
// n is how many fields changed.
func fillGaps(dst, src models.ManualFinancials) (models.ManualFinancials, int) {
	n := 0
	take := func(d **float64, v *float64) {
		if *d == nil && v != nil {
			*d = v
			n++
		}
	}
	take(&dst.Revenue, src.Revenue)
	take(&dst.NetProfit, src.NetProfit)
	take(&dst.EBITDA, src.EBITDA)
	take(&dst.OperatingProfit, src.OperatingProfit)
	take(&dst.OperatingCashFlow, src.OperatingCashFlow)
	take(&dst.Capex, src.Capex)
	take(&dst.Debt, src.Debt)
	take(&dst.Cash, src.Cash)
	take(&dst.Equity, src.Equity)
	take(&dst.Dividends, src.Dividends)
	return dst, n
}

// MatchCompanies keeps the stored spelling of tickers named in a comma
// separated list. An empty list means no filter (the caller passes nil).
func MatchCompanies(stored []string, list string) []string {
	want := map[string]bool{}
	for _, p := range strings.Split(list, ",") {
		p = strings.ToUpper(strings.TrimSpace(p))
		if p != "" {
			want[p] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	var out []string
	for _, c := range stored {
		if want[strings.ToUpper(c)] {
			out = append(out, c)
		}
	}
	return out
}
