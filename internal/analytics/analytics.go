// Package analytics derives long-term investing metrics from raw quarterly
// QuarterData: margins, leverage, growth rates (YoY/QoQ/CAGR), TTM and annual
// aggregates, and a composite "quality" score.
package analytics

import (
	"math"
	"sort"
	"time"

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
	// Standalone marks figures from a standalone source (RSBU/CBR legal
	// entity) rather than group ones; growth rates never compare the two.
	Standalone bool
	// Annual marks, on a quarterly series, a Q4 point holding a whole year's
	// flows (an RSBU or manual row); growth rates never compare it with a
	// single quarter.
	Annual bool
}

// Series is a chronologically ordered sequence of Points for one metric.
type Series []Point

// IsFlow reports whether a metric is a flow (sums across quarters in TTM/annual)
// rather than a stock or ratio (last-observed value wins).
func IsFlow(metric string) bool {
	switch metric {
	case "revenue", "net_profit", "ebitda",
		"operating_profit", "operating_cash_flow", "capex":
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
		"net_debt", "ev", "operating_margin", "ev_ebit", "fcf", "p_fcf",
		"revenue_yoy", "net_profit_yoy", "ebitda_yoy",
		"revenue_cagr3", "net_profit_cagr3",
		"revenue_cagr5", "net_profit_cagr5":
		return true
	}
	return false
}

// AllMetrics is the canonical order of metrics shown to the user.
var AllMetrics = []string{
	"revenue", "net_profit", "ebitda", "operating_profit",
	"capitalization", "debt", "cash", "net_debt", "equity", "dividends",
	"operating_cash_flow", "capex", "fcf",
	"pe", "roe", "pb", "div_yield", "ev", "ev_ebit", "p_fcf",
	"net_margin", "ebitda_margin", "operating_margin", "debt_ebitda",
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

// quarterSeq numbers quarters consecutively across years (2024-Q4 -> 2025-Q1
// differ by 1).
func quarterSeq(q models.QuarterData) int {
	idx := quarterIdx(q.Quarter)
	if idx == 0 {
		return math.MinInt / 2 // unknown quarter: never consecutive with anything
	}
	return q.Year*4 + idx - 1
}

// standalone reports whether a row's figures are a single legal entity's
// (RSBU/CBR) rather than group figures.
func standalone(q models.QuarterData) bool {
	return !IsComparable(q.Source)
}

// isAnnualFigure reports whether a row's flow metrics cover the whole year:
// ГИР БО RSBU is annual-only and stored on Q4, and so is a manual entry (a
// year typed in from the annual report). CBR bank rows are true single
// quarters, as are CSV rows.
func isAnnualFigure(q models.QuarterData) bool {
	return (q.Source == models.SourceRSBU || q.Source == models.SourceManual) && q.Quarter == "Q4"
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
	case "cash":
		return models.ValueOrNaN(q.Cash)
	case "operating_profit":
		return models.ValueOrNaN(q.OperatingProfit)
	case "operating_cash_flow":
		return models.ValueOrNaN(q.OperatingCashFlow)
	case "capex":
		return models.ValueOrNaN(q.Capex)
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

			Standalone: standalone(q),
			Annual:     isAnnualFigure(q),
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
			switch {
			case isAnnualFigure(q):
				// An annual RSBU figure already is a trailing-12-months value.
				v = rawValue(q, metric)
				ok = !math.IsNaN(v)
			case i >= 3 && quarterSeq(hist[i])-quarterSeq(hist[i-3]) == 3:
				// Only four consecutive quarters make a TTM; rows are unique
				// per quarter, so a span of 3 means no gaps.
				// The four quarters must also be of one kind: a sum of group and
				// standalone quarters is neither.
				sum := 0.0
				present := 0
				for j := i - 3; j <= i; j++ {
					row := hist[j]
					if isAnnualFigure(row) && row.Quarterly != nil {
						// A manual year replaced this Q4 quarter; the
						// window still needs the quarter itself.
						row = *row.Quarterly
					}
					val := rawValue(row, metric)
					if !math.IsNaN(val) && !isAnnualFigure(row) && standalone(row) == standalone(q) {
						sum += val
						present++
					}
				}
				if present == 4 {
					v = sum
					ok = true
				}
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

			Standalone: standalone(q),
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
		sa := standalone(quarters[len(quarters)-1])
		if flow {
			if last := quarters[len(quarters)-1]; isAnnualFigure(last) {
				val = rawValue(last, metric)
			} else if len(quarters) == 4 {
				sum := 0.0
				present := 0
				for _, q := range quarters {
					v := rawValue(q, metric)
					if !math.IsNaN(v) && standalone(q) == sa {
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
					val, sa = v, standalone(quarters[i])
					break
				}
			}
		}
		out = append(out, Point{
			Label: formatYear(y),
			Year:  y,
			Index: y,
			Value: val,

			Standalone: sa,
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

	// pickFlow pairs a flow with point-in-time figures: a single quarter's
	// flow against a balance-sheet value would understate it fourfold, so the
	// quarterly base uses the trailing twelve months.
	pickFlow := func(m string) Series {
		if base == PeriodQuarter {
			return SeriesFor(history, m, PeriodTTM)
		}
		return SeriesFor(history, m, base)
	}

	switch metric {
	case "net_debt":
		// Both are year-end (or quarter-end) balances.
		return combine(pick("debt"), pick("cash"), -1)
	case "ev":
		return combine(pick("capitalization"), DerivedSeries(history, "net_debt", base), 1)
	case "operating_margin":
		return ratio(pick("operating_profit"), pick("revenue"), 100)
	case "ev_ebit":
		// EBIT is profit from sales (RSBU line 2200) or a CSV's operating
		// profit. Like P/E, a loss gives no multiple.
		// A non-positive EV (cash above cap + debt) would read as "cheapest".
		return ratio(positiveOnly(DerivedSeries(history, "ev", base)), positiveOnly(pickFlow("operating_profit")), 1)
	case "fcf":
		return combine(SeriesFor(history, "operating_cash_flow", base), SeriesFor(history, "capex", base), -1)
	case "p_fcf":
		return ratio(pick("capitalization"), positiveOnly(pickFlow("fcf")), 1)
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
		out = append(out, Point{Label: p.Label, Year: p.Year, Index: p.Index, Value: val, Standalone: p.Standalone})
	}
	return out
}

// combine returns a + sign*b per label (NaN when either side is missing),
// keeping a's labels and kind.
func combine(a, b Series, sign float64) Series {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	bByLabel := make(map[string]Point, len(b))
	for _, p := range b {
		bByLabel[p.Label] = p
	}
	out := make(Series, 0, len(a))
	for _, p := range a {
		val := math.NaN()
		if q, ok := bByLabel[p.Label]; ok && !math.IsNaN(p.Value) && !math.IsNaN(q.Value) && q.Standalone == p.Standalone {
			val = p.Value + sign*q.Value
		}
		out = append(out, Point{Label: p.Label, Year: p.Year, Index: p.Index, Value: val, Standalone: p.Standalone})
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

// indexPoints maps each point's Index to the point.
func indexPoints(s Series) map[int]Point {
	m := make(map[int]Point, len(s))
	for _, p := range s {
		m[p.Index] = p
	}
	return m
}

// YearAgo returns the point of s for the same period n calendar years before
// p, if present and of the same kind (group vs standalone figures).
func YearAgo(s Series, p Point, base Period, n int) (Point, bool) {
	for _, q := range s {
		if q.Index == p.Index-yearsBack(base, n) {
			return q, comparablePoints(p, q) && !math.IsNaN(q.Value)
		}
	}
	return Point{}, false
}

// yearsBack is the Index offset of the same period n years earlier: Index is
// year*10+quarter for quarterly/TTM points and the year for annual ones.
// Looking points up by Index (not by position) keeps gaps in the history —
// e.g. annual-only RSBU rows — from pairing the wrong periods.
func yearsBack(base Period, n int) int {
	if base == PeriodAnnual {
		return n
	}
	return n * 10
}

// comparablePoints reports whether two points of one series may be compared
// for growth: same kind of entity and same span (a year vs a quarter).
func comparablePoints(a, b Point) bool {
	return a.Standalone == b.Standalone && a.Annual == b.Annual
}

// yoy computes year-over-year percentage change against the same period one
// calendar year earlier (NaN when that period is absent).
func yoy(s Series, base Period) Series {
	if len(s) == 0 {
		return nil
	}
	byIndex := indexPoints(s)
	out := make(Series, 0, len(s))
	for _, p := range s {
		val := math.NaN()
		if q, ok := byIndex[p.Index-yearsBack(base, 1)]; ok && comparablePoints(p, q) {
			prev := q.Value
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
	byIndex := indexPoints(s)
	out := make(Series, 0, len(s))
	for _, p := range s {
		val := math.NaN()
		if q, ok := byIndex[p.Index-yearsBack(base, years)]; ok && comparablePoints(p, q) {
			prev, cur := q.Value, p.Value
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
	// Stock figures come from the latest period that reports them (a bank's
	// latest row is usually a Q1-Q3 without market cap); these name that period.
	CapLabel, DebtLabel, PELabel, ROELabel string
	PB                                     float64 // market cap / equity, from the latest period that has both
	PBLabel                                string  // period PB was taken from ("" when PB is NaN)
	DivYield                               float64 // dividends / market cap, %, latest period that has both
	DivYieldLabel                          string  // period DivYield was taken from
	NetDebt                                float64 // debt - cash, latest period that has both
	NetDebtLabel                           string
	EVEBIT                                 float64 // (cap + net debt) / TTM operating profit, latest period that has it
	EVEBITLabel                            string
	FCF                                    float64 // TTM operating cash flow - TTM capex
	FCFLabel                               string  // period FCF was taken from
	PFCF                                   float64 // cap / TTM FCF, latest period that has it
	PFCFLabel                              string
	OperatingMargin                        float64 // TTM operating profit / TTM revenue
	NetMargin                              float64 // TTM net_profit / TTM revenue
	EBITDAMargin                           float64 // TTM
	DebtEBITDA                             float64 // last debt / TTM ebitda
	RevenueYoY                             float64
	NetProfitYoY                           float64
	RevenueCAGR3Y                          float64
	NetProfitCAGR3                         float64
	RevenueCAGR5Y                          float64
	NetProfitCAGR5                         float64
	Score                                  int // 0-100 composite long-term-investor score
	// PEPoint, PBPoint, DivYieldPoint are the periods PE, PB and DivYield
	// were taken from (zero when NaN); Standalone tells their reporting kind.
	PEPoint, PBPoint, DivYieldPoint Point
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

		NetDebt:         math.NaN(),
		EVEBIT:          math.NaN(),
		FCF:             math.NaN(),
		PFCF:            math.NaN(),
		OperatingMargin: math.NaN(),
	}
	if len(hist) == 0 {
		return snap
	}
	last := hist[len(hist)-1]
	snap.Company = last.Company
	snap.Category = last.Category
	snap.LastLabel = qLabel(last.Year, last.Quarter)

	latest := func(metric string, v *float64, label *string) {
		if p, ok := LatestValid(QuarterlySeries(hist, metric)); ok {
			*v, *label = p.Value, p.Label
		}
	}
	latest("capitalization", &snap.Capitalization, &snap.CapLabel)
	latest("debt", &snap.Debt, &snap.DebtLabel)
	if p, ok := LatestValid(QuarterlySeries(hist, "pe")); ok {
		snap.PE, snap.PELabel, snap.PEPoint = p.Value, p.Label, p
	}
	latest("roe", &snap.ROE, &snap.ROELabel)

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
		snap.PB, snap.PBLabel, snap.PBPoint = p.Value, p.Label, p
	}
	if p, ok := LatestValid(DerivedSeries(hist, "div_yield", PeriodQuarter)); ok {
		snap.DivYield, snap.DivYieldLabel, snap.DivYieldPoint = p.Value, p.Label, p
	}
	if p, ok := LatestValid(DerivedSeries(hist, "net_debt", PeriodQuarter)); ok {
		snap.NetDebt, snap.NetDebtLabel = p.Value, p.Label
	}
	// The quarterly base pairs each period's EV / cap with TTM flows.
	if p, ok := LatestValid(DerivedSeries(hist, "ev_ebit", PeriodQuarter)); ok {
		snap.EVEBIT, snap.EVEBITLabel = p.Value, p.Label
	}
	if p, ok := LatestValid(DerivedSeries(hist, "p_fcf", PeriodQuarter)); ok {
		snap.PFCF, snap.PFCFLabel = p.Value, p.Label
	}
	if p, ok := LatestValid(DerivedSeries(hist, "fcf", PeriodTTM)); ok {
		snap.FCF, snap.FCFLabel = p.Value, p.Label
	}
	snap.OperatingMargin = lastNonNaN(DerivedSeries(hist, "operating_margin", PeriodTTM))

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

// Current is a company's valuation at its latest exchange close: the stored
// quote's market cap over the most recent fundamentals. Every float is NaN
// when unknown; each *Label names the period the fundamental was taken from.
type Current struct {
	PriceDate      string // trade date of the quote, "YYYY-MM-DD"
	Capitalization float64
	PE             float64 // cap / trailing-12-months net profit (NaN for a loss)
	EarningsLabel  string
	PB             float64 // cap / latest positive equity
	EquityLabel    string
	DivYield       float64 // latest year's dividends / cap, %
	DividendsLabel string
	EVEBIT         float64 // (cap + latest net debt) / TTM operating profit (NaN for a loss)
	EBITLabel      string
	PFCF           float64 // cap / TTM free cash flow (NaN when FCF is not positive)
	FCFLabel       string
	// Stale is set when a fundamental exists but ended more than
	// MaxFundamentalAge before the quote; its multiple is then NaN and its
	// label says "too old" rather than silently pairing today's price with
	// figures from years ago.
	Stale bool
	// EarningsPoint, EquityPoint, DividendsPoint are the fundamentals PE, PB
	// and DivYield rest on (zero when none); Standalone tells their kind.
	EarningsPoint, EquityPoint, DividendsPoint Point
}

// MaxFundamentalAge is how far a fundamental's period end may lag the quote
// date before a "current" multiple built on it is withheld. Annual reports
// land months after year-end, so it spans a full reporting cycle.
const MaxFundamentalAge = 18 // months

// QuoteMaxAge is how old a stored quote may be and still count as current; an
// older one (delisting, long suspension, fetch not run) is ignored.
const QuoteMaxAge = 30 * 24 * time.Hour

// QuoteIsFresh reports whether q can be used as the current price at now.
func QuoteIsFresh(q models.MarketQuote, now time.Time) bool {
	return q.Capitalization > 0 && now.Sub(q.PriceDate) <= QuoteMaxAge
}

// monthsBetween returns how many months the period of p (quarterly Index)
// ended before date.
func monthsBetween(p Point, date time.Time) int {
	endMonth := (p.Index % 10) * 3
	return (date.Year()*12 + int(date.Month())) - (p.Year*12 + endMonth)
}

// BuildCurrent values a company at quote q. Trailing earnings are the latest
// TTM net profit: four consecutive quarters, or an annual RSBU figure.
func BuildCurrent(history []models.QuarterData, q models.MarketQuote) Current {
	cur := Current{
		PriceDate:      q.PriceDate.Format("2006-01-02"),
		Capitalization: q.Capitalization,
		PE:             math.NaN(),
		PB:             math.NaN(),
		DivYield:       math.NaN(),
		EVEBIT:         math.NaN(),
		PFCF:           math.NaN(),
	}
	if q.Capitalization <= 0 {
		cur.Capitalization = math.NaN()
		return cur
	}
	// fresh reports whether the fundamental at p is recent enough to price;
	// otherwise it marks the result stale and relabels the period.
	fresh := func(p Point, label *string) bool {
		*label = p.Label
		if monthsBetween(p, q.PriceDate) > MaxFundamentalAge {
			*label = p.Label + " (устарело)"
			cur.Stale = true
			return false
		}
		return true
	}
	if p, ok := LatestValid(TTMSeries(history, "net_profit")); ok {
		if fresh(p, &cur.EarningsLabel) && p.Value > 0 {
			cur.PE, cur.EarningsPoint = q.Capitalization/p.Value, p
		}
	}
	if p, ok := LatestValid(positiveOnly(QuarterlySeries(history, "equity"))); ok {
		if fresh(p, &cur.EquityLabel) {
			cur.PB, cur.EquityPoint = q.Capitalization/p.Value, p
		}
	}
	if p, ok := LatestValid(QuarterlySeries(history, "dividends")); ok {
		if fresh(p, &cur.DividendsLabel) {
			cur.DivYield, cur.DividendsPoint = p.Value/q.Capitalization*100, p
		}
	}
	// EV/EBIT and P/FCF report a too-old input in their own label rather than
	// through Stale, which keeps meaning "P/E, P/B or yield withheld" — cash
	// or capex missing from recent CSV years must not mark a company whose
	// P/E is current as stale.
	recent := func(p Point, label *string) bool {
		*label = p.Label
		if monthsBetween(p, q.PriceDate) > MaxFundamentalAge {
			*label = p.Label + " (устарело)"
			return false
		}
		return true
	}
	if p, ok := LatestValid(TTMSeries(history, "operating_profit")); ok {
		if recent(p, &cur.EBITLabel) && p.Value > 0 {
			// EV needs a current net debt as well.
			if nd, ok := LatestValid(DerivedSeries(history, "net_debt", PeriodQuarter)); ok {
				var ndLabel string
				switch {
				case !recent(nd, &ndLabel):
					cur.EBITLabel += ", чистый долг на " + ndLabel
				case q.Capitalization+nd.Value > 0:
					cur.EVEBIT = (q.Capitalization + nd.Value) / p.Value
				}
			}
		}
	}
	if p, ok := LatestValid(DerivedSeries(history, "fcf", PeriodTTM)); ok {
		if recent(p, &cur.FCFLabel) && p.Value > 0 {
			cur.PFCF = q.Capitalization / p.Value
		}
	}
	return cur
}
