// Package analytics derives long-term investing metrics from raw quarterly
// QuarterData: margins, leverage, growth rates (YoY/QoQ/CAGR), TTM and annual
// aggregates, and a composite "quality" score.
package analytics

import (
	"math"
	"sort"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// Period is the reporting horizon used when rendering charts/tables.
type Period string

const (
	PeriodQuarter Period = "quarter"
	PeriodTTM     Period = "ttm"
	PeriodAnnual  Period = "annual"
)

// Point is one (label, value) sample on a time series. Label is "YYYY-Qn"
// for quarterly/TTM data and "YYYY" for annual data. NaN means "no data".
type Point struct {
	Label string
	Year  int
	Index int // monotonic index for ordering (year*10 + quarter for quarterly, year for annual)
	Value float64
}

// Series is a chronologically ordered sequence of Points for one metric.
type Series []Point

// IsFlow reports whether a metric is a flow (sums across quarters in TTM/annual)
// rather than a stock or ratio (last-observed value wins).
func IsFlow(metric string) bool {
	switch metric {
	case "revenue", "net_profit", "ebitda":
		return true
	}
	return false
}

// IsDerived reports whether a metric must be computed rather than read from
// a raw column.
func IsDerived(metric string) bool {
	switch metric {
	case "net_margin", "ebitda_margin", "debt_ebitda",
		"pb", "div_yield",
		"revenue_yoy", "net_profit_yoy", "ebitda_yoy",
		"revenue_cagr3", "net_profit_cagr3",
		"revenue_cagr5", "net_profit_cagr5":
		return true
	}
	return false
}

// AllMetrics is the canonical order of metrics shown to the user.
var AllMetrics = []string{
	"revenue", "net_profit", "ebitda",
	"capitalization", "debt", "equity", "dividends",
	"pe", "roe", "pb", "div_yield",
	"net_margin", "ebitda_margin", "debt_ebitda",
	"revenue_yoy", "net_profit_yoy", "ebitda_yoy",
	"revenue_cagr3", "net_profit_cagr3",
	"revenue_cagr5", "net_profit_cagr5",
}

var metricSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(AllMetrics))
	for _, name := range AllMetrics {
		m[name] = struct{}{}
	}
	return m
}()

// IsValidMetric reports whether name is a metric the application knows how to
// render. Handlers use it to reject unknown user input before doing any work.
func IsValidMetric(name string) bool {
	_, ok := metricSet[name]
	return ok
}

func quarterIdx(q string) int {
	switch q {
	case "Q1":
		return 1
	case "Q2":
		return 2
	case "Q3":
		return 3
	case "Q4":
		return 4
	}
	return 0
}

func qLabel(year int, quarter string) string {
	return formatYear(year) + "-" + quarter
}

func formatYear(y int) string {
	// avoid strconv import bloat in tooling diffs
	if y <= 0 {
		return "0000"
	}
	out := []byte("0000")
	for i := 3; i >= 0 && y > 0; i-- {
		out[i] = byte('0' + y%10)
		y /= 10
	}
	return string(out)
}

// rawValue returns a stored metric, or NaN when it was not reported.
func rawValue(q models.QuarterData, metric string) float64 {
	switch metric {
	case "capitalization":
		return models.ValueOrNaN(q.Capitalization)
	case "revenue":
		return models.ValueOrNaN(q.Revenue)
	case "net_profit":
		return models.ValueOrNaN(q.NetProfit)
	case "ebitda":
		return models.ValueOrNaN(q.EBITDA)
	case "debt":
		return models.ValueOrNaN(q.Debt)
	case "pe":
		return models.ValueOrNaN(q.PE)
	case "roe":
		return models.ValueOrNaN(q.ROE)
	case "equity":
		return models.ValueOrNaN(q.Equity)
	case "dividends":
		return models.ValueOrNaN(q.Dividends)
	}
	return math.NaN()
}

// SortHistory orders history chronologically (year, quarter).
func SortHistory(h []models.QuarterData) []models.QuarterData {
	out := make([]models.QuarterData, len(h))
	copy(out, h)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Year != out[j].Year {
			return out[i].Year < out[j].Year
		}
		return quarterIdx(out[i].Quarter) < quarterIdx(out[j].Quarter)
	})
	return out
}

// QuarterlySeries returns the raw metric values per quarter (unreported
// values are NaN so charts can skip them; a reported zero stays zero).
func QuarterlySeries(history []models.QuarterData, metric string) Series {
	hist := SortHistory(history)
	out := make(Series, 0, len(hist))
	for _, q := range hist {
		out = append(out, Point{
			Label: qLabel(q.Year, q.Quarter),
			Year:  q.Year,
			Index: q.Year*10 + quarterIdx(q.Quarter),
			Value: rawValue(q, metric),
		})
	}
	return out
}

// TTMSeries returns a trailing-twelve-months series for `metric`. For flow
// metrics (revenue/net_profit/ebitda) it sums the last 4 quarters at each
// point; stock/ratio metrics keep each period's own value (NaN where the row
// lacks it — nothing is carried forward).
func TTMSeries(history []models.QuarterData, metric string) Series {
	hist := SortHistory(history)
	out := make(Series, 0, len(hist))
	flow := IsFlow(metric)
	for i, q := range hist {
		var v float64
		var ok bool
		if flow {
			if i < 3 {
				out = append(out, Point{
					Label: qLabel(q.Year, q.Quarter),
					Year:  q.Year,
					Index: q.Year*10 + quarterIdx(q.Quarter),
					Value: math.NaN(),
				})
				continue
			}
			sum := 0.0
			present := 0
			for j := i - 3; j <= i; j++ {
				val := rawValue(hist[j], metric)
				if !math.IsNaN(val) {
					sum += val
					present++
				}
			}
			if present == 4 {
				v = sum
				ok = true
			}
		} else {
			val := rawValue(q, metric)
			if !math.IsNaN(val) {
				v = val
				ok = true
			}
		}
		val := v
		if !ok {
			val = math.NaN()
		}
		out = append(out, Point{
			Label: qLabel(q.Year, q.Quarter),
			Year:  q.Year,
			Index: q.Year*10 + quarterIdx(q.Quarter),
			Value: val,
		})
	}
	return out
}

// AnnualSeries aggregates one point per year. Flow metrics are summed across
// the available quarters of the year (only if all 4 are present); stock/ratio
// metrics take the Q4 value (or last available quarter).
func AnnualSeries(history []models.QuarterData, metric string) Series {
	hist := SortHistory(history)
	byYear := map[int][]models.QuarterData{}
	years := []int{}
	for _, q := range hist {
		if _, seen := byYear[q.Year]; !seen {
			years = append(years, q.Year)
		}
		byYear[q.Year] = append(byYear[q.Year], q)
	}
	sort.Ints(years)
	flow := IsFlow(metric)

	out := make(Series, 0, len(years))
	for _, y := range years {
		quarters := byYear[y]
		val := math.NaN()
		if flow {
			if len(quarters) == 4 {
				sum := 0.0
				present := 0
				for _, q := range quarters {
					v := rawValue(q, metric)
					if !math.IsNaN(v) {
						sum += v
						present++
					}
				}
				if present == 4 {
					val = sum
				}
			}
		} else {
			// take the latest reported quarter of the year
			for i := len(quarters) - 1; i >= 0; i-- {
				v := rawValue(quarters[i], metric)
				if !math.IsNaN(v) {
					val = v
					break
				}
			}
		}
		out = append(out, Point{
			Label: formatYear(y),
			Year:  y,
			Index: y,
			Value: val,
		})
	}
	return out
}

// DerivedSeries returns Series for derived metrics. `base` selects whether the
// underlying numbers are quarterly, TTM or annual.
func DerivedSeries(history []models.QuarterData, metric string, base Period) Series {
	pick := func(m string) Series {
		switch base {
		case PeriodTTM:
			return TTMSeries(history, m)
		case PeriodAnnual:
			return AnnualSeries(history, m)
		default:
			return QuarterlySeries(history, m)
		}
	}

	switch metric {
	case "net_margin":
		return ratio(pick("net_profit"), pick("revenue"), 100)
	case "ebitda_margin":
		return ratio(pick("ebitda"), pick("revenue"), 100)
	case "debt_ebitda":
		// stock/flow: debt is point-in-time; pair with TTM ebitda when base==quarter
		debt := pick("debt")
		ebitdaBase := base
		if base == PeriodQuarter {
			ebitdaBase = PeriodTTM
		}
		var eb Series
		switch ebitdaBase {
		case PeriodTTM:
			eb = TTMSeries(history, "ebitda")
		case PeriodAnnual:
			eb = AnnualSeries(history, "ebitda")
		default:
			eb = QuarterlySeries(history, "ebitda")
		}
		return ratio(debt, eb, 1)
	case "pb":
		// Both are point-in-time values. P/B is undefined for non-positive
		// equity (a negative ratio would read as "cheap"); CheckRow flags
		// negative equity separately.
		return ratio(pick("capitalization"), positiveOnly(pick("equity")), 1)
	case "div_yield":
		// Dividends sit on the Q4 row (year's record dates) and cap is the
		// year-end value, so the quarterly/annual yield is a trailing one.
		return ratio(pick("dividends"), pick("capitalization"), 100)
	case "revenue_yoy":
		return yoy(pick("revenue"), base)
	case "net_profit_yoy":
		return yoy(pick("net_profit"), base)
	case "ebitda_yoy":
		return yoy(pick("ebitda"), base)
	case "revenue_cagr3":
		return rollingCAGR(pick("revenue"), base, 3)
	case "net_profit_cagr3":
		return rollingCAGR(pick("net_profit"), base, 3)
	case "revenue_cagr5":
		return rollingCAGR(pick("revenue"), base, 5)
	case "net_profit_cagr5":
		return rollingCAGR(pick("net_profit"), base, 5)
	}
	return nil
}

// SeriesFor is the universal dispatcher used by the chart handler.
func SeriesFor(history []models.QuarterData, metric string, period Period) Series {
	if IsDerived(metric) {
		return DerivedSeries(history, metric, period)
	}
	switch period {
	case PeriodTTM:
		return TTMSeries(history, metric)
	case PeriodAnnual:
		return AnnualSeries(history, metric)
	default:
		return QuarterlySeries(history, metric)
	}
}

func ratio(num, den Series, scale float64) Series {
	if len(num) == 0 || len(den) == 0 {
		return nil
	}
	denByLabel := make(map[string]float64, len(den))
	for _, p := range den {
		denByLabel[p.Label] = p.Value
	}
	out := make(Series, 0, len(num))
	for _, p := range num {
		d, ok := denByLabel[p.Label]
		val := math.NaN()
		if ok && !math.IsNaN(p.Value) && !math.IsNaN(d) && d != 0 {
			val = p.Value / d * scale
		}
		out = append(out, Point{Label: p.Label, Year: p.Year, Index: p.Index, Value: val})
	}
	return out
}

// positiveOnly maps non-positive values to NaN.
func positiveOnly(s Series) Series {
	out := make(Series, len(s))
	for i, p := range s {
		if p.Value <= 0 {
			p.Value = math.NaN()
		}
		out[i] = p
	}
	return out
}

// yoy computes year-over-year percentage change. The "previous year" window is
// 4 points back for quarterly/TTM data and 1 point back for annual data.
func yoy(s Series, base Period) Series {
	if len(s) == 0 {
		return nil
	}
	lag := 4
	if base == PeriodAnnual {
		lag = 1
	}
	out := make(Series, 0, len(s))
	for i, p := range s {
		val := math.NaN()
		if i >= lag {
			prev := s[i-lag].Value
			if !math.IsNaN(prev) && !math.IsNaN(p.Value) && prev != 0 {
				val = (p.Value/prev - 1) * 100
			}
		}
		out = append(out, Point{Label: p.Label, Year: p.Year, Index: p.Index, Value: val})
	}
	return out
}

// rollingCAGR computes the compound annual growth rate over the last
// `years` years at each point.
func rollingCAGR(s Series, base Period, years int) Series {
	if len(s) == 0 {
		return nil
	}
	lag := years * 4
	if base == PeriodAnnual {
		lag = years
	}
	out := make(Series, 0, len(s))
	for i, p := range s {
		val := math.NaN()
		if i >= lag {
			prev := s[i-lag].Value
			cur := p.Value
			if !math.IsNaN(prev) && !math.IsNaN(cur) && prev > 0 && cur > 0 {
				val = (math.Pow(cur/prev, 1.0/float64(years)) - 1) * 100
			}
		}
		out = append(out, Point{Label: p.Label, Year: p.Year, Index: p.Index, Value: val})
	}
	return out
}

// LatestValid returns the most recent point in s whose value is a real number.
// Returns (point, true) on success, zero-value Point and false otherwise.
func LatestValid(s Series) (Point, bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if !math.IsNaN(s[i].Value) {
			return s[i], true
		}
	}
	return Point{}, false
}

// Snapshot is a one-shot summary of a company built from its full history.
// Every field uses math.NaN() to mean "no data".
type Snapshot struct {
	Company        string
	Category       string
	LastLabel      string
	Capitalization float64
	Revenue        float64 // TTM
	NetProfit      float64 // TTM
	EBITDA         float64 // TTM
	Debt           float64
	PE             float64
	ROE            float64
	PB             float64 // market cap / equity, from the latest period that has both
	PBLabel        string  // period PB was taken from ("" when PB is NaN)
	DivYield       float64 // dividends / market cap, %, latest period that has both
	DivYieldLabel  string  // period DivYield was taken from
	NetMargin      float64 // TTM net_profit / TTM revenue
	EBITDAMargin   float64 // TTM
	DebtEBITDA     float64 // last debt / TTM ebitda
	RevenueYoY     float64
	NetProfitYoY   float64
	RevenueCAGR3Y  float64
	NetProfitCAGR3 float64
	RevenueCAGR5Y  float64
	NetProfitCAGR5 float64
	Score          int // 0-100 composite long-term-investor score
}

func lastNonNaN(s Series) float64 {
	if p, ok := LatestValid(s); ok {
		return p.Value
	}
	return math.NaN()
}

// BuildSnapshot computes a current-state Snapshot for one company.
func BuildSnapshot(history []models.QuarterData) Snapshot {
	hist := SortHistory(history)
	snap := Snapshot{
		Capitalization: math.NaN(),
		Revenue:        math.NaN(),
		NetProfit:      math.NaN(),
		EBITDA:         math.NaN(),
		Debt:           math.NaN(),
		PE:             math.NaN(),
		ROE:            math.NaN(),
		NetMargin:      math.NaN(),
		EBITDAMargin:   math.NaN(),
		DebtEBITDA:     math.NaN(),
		RevenueYoY:     math.NaN(),
		NetProfitYoY:   math.NaN(),
		RevenueCAGR3Y:  math.NaN(),
		NetProfitCAGR3: math.NaN(),
		RevenueCAGR5Y:  math.NaN(),
		NetProfitCAGR5: math.NaN(),
		PB:             math.NaN(),
		DivYield:       math.NaN(),
	}
	if len(hist) == 0 {
		return snap
	}
	last := hist[len(hist)-1]
	snap.Company = last.Company
	snap.Category = last.Category
	snap.LastLabel = qLabel(last.Year, last.Quarter)

	snap.Capitalization = models.ValueOrNaN(last.Capitalization)
	snap.Debt = models.ValueOrNaN(last.Debt)
	snap.PE = models.ValueOrNaN(last.PE)
	snap.ROE = models.ValueOrNaN(last.ROE)

	snap.Revenue = lastNonNaN(TTMSeries(hist, "revenue"))
	snap.NetProfit = lastNonNaN(TTMSeries(hist, "net_profit"))
	snap.EBITDA = lastNonNaN(TTMSeries(hist, "ebitda"))

	snap.NetMargin = lastNonNaN(DerivedSeries(hist, "net_margin", PeriodTTM))
	snap.EBITDAMargin = lastNonNaN(DerivedSeries(hist, "ebitda_margin", PeriodTTM))
	snap.DebtEBITDA = lastNonNaN(DerivedSeries(hist, "debt_ebitda", PeriodTTM))
	// Valuation inputs live on Q4 rows, while a bank's latest row is often a
	// Q1-Q3 with profit only; take the latest period that has them and keep
	// its label so the dashboard can say how old the figure is.
	if p, ok := LatestValid(DerivedSeries(hist, "pb", PeriodQuarter)); ok {
		snap.PB, snap.PBLabel = p.Value, p.Label
	}
	if p, ok := LatestValid(DerivedSeries(hist, "div_yield", PeriodQuarter)); ok {
		snap.DivYield, snap.DivYieldLabel = p.Value, p.Label
	}

	snap.RevenueYoY = lastNonNaN(DerivedSeries(hist, "revenue_yoy", PeriodTTM))
	snap.NetProfitYoY = lastNonNaN(DerivedSeries(hist, "net_profit_yoy", PeriodTTM))

	snap.RevenueCAGR3Y = lastNonNaN(DerivedSeries(hist, "revenue_cagr3", PeriodTTM))
	snap.NetProfitCAGR3 = lastNonNaN(DerivedSeries(hist, "net_profit_cagr3", PeriodTTM))
	snap.RevenueCAGR5Y = lastNonNaN(DerivedSeries(hist, "revenue_cagr5", PeriodTTM))
	snap.NetProfitCAGR5 = lastNonNaN(DerivedSeries(hist, "net_profit_cagr5", PeriodTTM))

	snap.Score = computeScore(snap)
	return snap
}

// computeScore returns a 0-100 composite quality score for long-term investing.
// The thresholds are intentionally simple and explained on the dashboard.
func computeScore(s Snapshot) int {
	score := 0.0
	components := 0.0

	// Growth (30 pts): revenue 3y CAGR
	if !math.IsNaN(s.RevenueCAGR3Y) {
		components += 30
		switch {
		case s.RevenueCAGR3Y >= 20:
			score += 30
		case s.RevenueCAGR3Y >= 10:
			score += 22
		case s.RevenueCAGR3Y >= 5:
			score += 15
		case s.RevenueCAGR3Y >= 0:
			score += 8
		}
	}

	// Profitability (25 pts): ROE
	if !math.IsNaN(s.ROE) {
		components += 25
		switch {
		case s.ROE >= 20:
			score += 25
		case s.ROE >= 15:
			score += 19
		case s.ROE >= 10:
			score += 13
		case s.ROE >= 5:
			score += 7
		case s.ROE >= 0:
			score += 2
		}
	}

	// Margins (15 pts): net margin
	if !math.IsNaN(s.NetMargin) {
		components += 15
		switch {
		case s.NetMargin >= 20:
			score += 15
		case s.NetMargin >= 10:
			score += 11
		case s.NetMargin >= 5:
			score += 7
		case s.NetMargin >= 0:
			score += 3
		}
	}

	// Leverage (15 pts): Debt/EBITDA (lower is better)
	if !math.IsNaN(s.DebtEBITDA) {
		components += 15
		switch {
		case s.DebtEBITDA <= 1:
			score += 15
		case s.DebtEBITDA <= 2:
			score += 11
		case s.DebtEBITDA <= 3:
			score += 7
		case s.DebtEBITDA <= 4:
			score += 3
		}
	}

	// Valuation (15 pts): P/E (lower is better, very low is suspicious)
	if !math.IsNaN(s.PE) {
		components += 15
		switch {
		case s.PE > 0 && s.PE <= 8:
			score += 15
		case s.PE > 0 && s.PE <= 12:
			score += 12
		case s.PE > 0 && s.PE <= 18:
			score += 8
		case s.PE > 0 && s.PE <= 25:
			score += 4
		}
	}

	if components == 0 {
		return 0
	}
	return int(math.Round(score * 100 / components))
}
