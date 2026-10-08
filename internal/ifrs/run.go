package ifrs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
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

// Fill is one company-year the run wrote, with the figures it added.
// Text is Russian and lists only the fields that were empty before.
type Fill struct {
	Company string `json:"company"`
	Year    int    `json:"year"`
	Text    string `json:"text"`
}

// Result is what one button press changed. Counts are company-years.
type Result struct {
	Saved     int
	Unchanged int
	Missing   int
	Failed    int
	Fills     []Fill
}

// Progress is one step of a run, reported before each company-year is
// downloaded. Done counts company-years already finished, so a slow report
// still names the company the run is on.
type Progress struct {
	Done      int
	Total     int
	Company   string
	Year      int
	Saved     int
	Unchanged int
	Missing   int
	Failed    int
	Fills     []Fill
}

// Status is the poll payload for the updates page. Message is Russian;
// it is shown as-is.
type Status struct {
	Running   bool   `json:"running"`
	Done      int    `json:"done"`
	Total     int    `json:"total"`
	Company   string `json:"company,omitempty"`
	Year      int    `json:"year,omitempty"`
	Saved     int    `json:"saved"`
	Unchanged int    `json:"unchanged"`
	Missing   int    `json:"missing"`
	Failed    int    `json:"failed"`
	Message   string `json:"message"`
	Fills     []Fill `json:"fills,omitempty"`
}

// Status renders Progress for the page while the run is still going.
func (p Progress) Status() Status {
	cur := p.Done + 1
	if p.Total > 0 && cur > p.Total {
		cur = p.Total
	}
	s := Status{
		Running:   true,
		Done:      p.Done,
		Total:     p.Total,
		Company:   p.Company,
		Year:      p.Year,
		Saved:     p.Saved,
		Unchanged: p.Unchanged,
		Missing:   p.Missing,
		Failed:    p.Failed,
		Fills:     p.Fills,
		Message:   fmt.Sprintf("%s, %d — %d из %d", p.Company, p.Year, cur, p.Total),
	}
	if p.Done > 0 {
		s.Message += fmt.Sprintf(". Дописано: %d, без новых: %d, не найдено: %d", p.Saved, p.Unchanged, p.Missing)
		if p.Failed > 0 {
			s.Message += fmt.Sprintf(", ошибок: %d", p.Failed)
		}
	}
	return s
}

// Status renders Result for the page.
func (r Result) Status() Status {
	s := Status{Saved: r.Saved, Unchanged: r.Unchanged, Missing: r.Missing, Failed: r.Failed, Fills: r.Fills}
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
// every company in the store. progress may be nil; it is called before each
// company-year download.
func Run(ctx context.Context, store Store, src Source, companies []string, years []int, logger *slog.Logger, progress func(Progress)) Result {
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
	total := len(companies) * len(years)
	done := 0
	for _, company := range companies {
		if ctx.Err() != nil {
			return res
		}
		existing, err := store.GetManualFinancials(ctx, company)
		if err != nil {
			logger.Error("ifrs: read manual entries", "company", company, "error", err)
			res.Failed += len(years)
			done += len(years)
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
			if progress != nil {
				progress(Progress{
					Done: done, Total: total, Company: company, Year: year,
					Saved: res.Saved, Unchanged: res.Unchanged,
					Missing: res.Missing, Failed: res.Failed,
					Fills: append([]Fill(nil), res.Fills...),
				})
			}
			done++
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
			merged, fields := fillGaps(byYear[year], parsed)
			if len(fields) == 0 {
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
			line := strings.Join(fields, ", ")
			res.Fills = append(res.Fills, Fill{Company: company, Year: year, Text: line})
			res.Saved++
			logger.Info("ifrs: filled manual entry", "company", company, "year", year, "fields", line)
		}
	}
	return res
}

// fillGaps copies parsed figures into the fields the stored row left empty.
// The returned names are the Russian labels of the fields that changed,
// each with the value in billions of RUB.
func fillGaps(dst, src models.ManualFinancials) (models.ManualFinancials, []string) {
	var fields []string
	take := func(name string, d **float64, v *float64) {
		if *d == nil && v != nil {
			*d = v
			fields = append(fields, name+" "+formatBln(*v))
		}
	}
	take("выручка", &dst.Revenue, src.Revenue)
	take("чистая прибыль", &dst.NetProfit, src.NetProfit)
	take("EBITDA", &dst.EBITDA, src.EBITDA)
	take("операционная прибыль", &dst.OperatingProfit, src.OperatingProfit)
	take("опер. денежный поток", &dst.OperatingCashFlow, src.OperatingCashFlow)
	take("капзатраты", &dst.Capex, src.Capex)
	take("долг", &dst.Debt, src.Debt)
	take("денежные средства", &dst.Cash, src.Cash)
	take("капитал", &dst.Equity, src.Equity)
	take("дивиденды", &dst.Dividends, src.Dividends)
	return dst, fields
}

// formatBln prints a billions-of-RUB figure with a comma decimal mark and
// trailing zeros dropped, so a unit mistake (millions stored as billions)
// is visible next to the other lines.
func formatBln(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return strings.ReplaceAll(s, ".", ",")
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
