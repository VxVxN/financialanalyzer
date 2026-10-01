package analytics

import (
	"math"
	"slices"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// Valuation relative to the company's own history and to its sector.

// BandMetrics are the multiples that get a historical band and a sector
// median, in display order.
var BandMetrics = []string{"pe", "pb", "div_yield"}

// BandYears is how far back from the latest period a historical band reaches.
const BandYears = 10

// MinBandPoints is the fewest historical periods a band needs: fewer say
// nothing about what is "usual" for the company.
const MinBandPoints = 3

// MinSectorPeers is the fewest other companies of a category a sector median
// needs.
const MinSectorPeers = 2

// HigherIsCheaper reports whether a larger value of a band metric means a
// cheaper stock (dividend yield), rather than a dearer one (P/E, P/B).
func HigherIsCheaper(metric string) bool { return metric == "div_yield" }

// HistoryBand places a current multiple within the company's own history.
type HistoryBand struct {
	Metric           string
	Current          float64
	Min, Median, Max float64
	// Percentile is the share of historical periods, in %, with a lower value
	// than Current (ties count half).
	Percentile float64
	N          int    // historical periods in the band
	From, To   string // labels of the first and last period
}

// historySeries is a band metric's stored history per period. A loss P/E
// (non-positive) is no valuation and is left out, like a non-positive equity
// is out of P/B; a zero yield (no payout) is a real value.
func historySeries(history []models.QuarterData, metric string) Series {
	switch metric {
	case "pe":
		return positiveOnly(QuarterlySeries(history, "pe"))
	case "pb", "div_yield":
		return DerivedSeries(history, metric, PeriodQuarter)
	}
	return nil
}

// HistoricalBand places current — the multiple at the latest quote, or the
// latest stored one — among the metric's values over the last BandYears.
// Only periods of the same kind as the latest one count (standalone RSBU/CBR
// multiples never mix with group ones). ok is false when current is unknown or
// fewer than MinBandPoints periods qualify.
func HistoricalBand(history []models.QuarterData, metric string, current float64) (HistoryBand, bool) {
	if math.IsNaN(current) || math.IsInf(current, 0) {
		return HistoryBand{}, false
	}
	s := historySeries(history, metric)
	last, ok := LatestValid(s)
	if !ok {
		return HistoryBand{}, false
	}
	var pts []Point
	for _, p := range s {
		if !math.IsNaN(p.Value) && !math.IsInf(p.Value, 0) && p.Standalone == last.Standalone && p.Year > last.Year-BandYears {
			pts = append(pts, p)
		}
	}
	if len(pts) < MinBandPoints {
		return HistoryBand{}, false
	}
	vals := make([]float64, len(pts))
	below := 0.0
	for i, p := range pts {
		vals[i] = p.Value
		switch {
		case p.Value < current:
			below++
		case p.Value == current:
			below += 0.5
		}
	}
	slices.Sort(vals)
	return HistoryBand{
		Metric:     metric,
		Current:    current,
		Min:        vals[0],
		Median:     Median(vals),
		Max:        vals[len(vals)-1],
		Percentile: below / float64(len(vals)) * 100,
		N:          len(vals),
		From:       pts[0].Label,
		To:         pts[len(pts)-1].Label,
	}, true
}

// Median returns the median of vals (NaN for none); vals is not modified.
func Median(vals []float64) float64 {
	if len(vals) == 0 {
		return math.NaN()
	}
	s := slices.Clone(vals)
	slices.Sort(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

// metricValue reads a band metric off a screener row.
func (r ScreenerRow) metricValue(metric string) *float64 {
	switch metric {
	case "pe":
		return r.PE
	case "pb":
		return r.PB
	case "div_yield":
		return r.DivYield
	}
	return nil
}

// ApplySectorMedians fills each row's sector medians: per band metric, the
// median over the other companies of the same category that have it (the
// company itself is left out, so it is compared with its peers), when at
// least MinSectorPeers have. SectorPeers is the number of other companies in
// the category. Rows without a category get none.
func ApplySectorMedians(rows []ScreenerRow) {
	byCat := map[string][]int{}
	for i, r := range rows {
		if r.Category != "" {
			byCat[r.Category] = append(byCat[r.Category], i)
		}
	}
	for _, idx := range byCat {
		for _, i := range idx {
			rows[i].SectorPeers = len(idx) - 1
			for _, m := range BandMetrics {
				var vals []float64
				for _, j := range idx {
					if v := rows[j].metricValue(m); j != i && v != nil {
						vals = append(vals, *v)
					}
				}
				if len(vals) < MinSectorPeers {
					continue
				}
				med := Median(vals)
				switch m {
				case "pe":
					rows[i].PESector = &med
				case "pb":
					rows[i].PBSector = &med
				case "div_yield":
					rows[i].DivYieldSector = &med
				}
			}
		}
	}
}
