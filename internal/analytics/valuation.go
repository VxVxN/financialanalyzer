package analytics

import (
	"math"
	"slices"
	"time"

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

// kindMatters reports whether a band metric depends on the reporting entity:
// P/E and P/B rest on the profit or equity of a legal entity (standalone
// RSBU/CBR) or of the group, while a dividend yield is the company's payout
// over its market cap whatever the source of the row.
func kindMatters(metric string) bool { return metric != "div_yield" }

// HistoricalBand places current — the multiple at the latest quote, or the
// latest stored one — among the metric's values over the BandYears up to the
// latest qualifying period. For P/E and P/B only periods of the kind of the
// figure current rests on count (standalone says which: standalone RSBU/CBR
// multiples never mix with group ones); the yield band takes every period.
// ok is false when current is unknown or fewer than MinBandPoints periods
// qualify.
func HistoricalBand(history []models.QuarterData, metric string, current float64, standalone bool) (HistoryBand, bool) {
	if math.IsNaN(current) || math.IsInf(current, 0) {
		return HistoryBand{}, false
	}
	var s Series
	for _, p := range historySeries(history, metric) {
		if !math.IsNaN(p.Value) && !math.IsInf(p.Value, 0) && (!kindMatters(metric) || p.Standalone == standalone) {
			s = append(s, p)
		}
	}
	if len(s) == 0 {
		return HistoryBand{}, false
	}
	last := s[len(s)-1]
	var pts []Point
	for _, p := range s {
		if p.Year > last.Year-BandYears {
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

// sectorFields returns a row's value and its sector median and peer
// count fields for a band metric.
func (r *ScreenerRow) sectorFields(metric string) (v *float64, median **float64, peers *int) {
	switch metric {
	case "pe":
		return r.PE, &r.PESector, &r.PESectorPeers
	case "pb":
		return r.PB, &r.PBSector, &r.PBSectorPeers
	case "div_yield":
		return r.DivYield, &r.DivYieldSector, &r.DivYieldSectorPeers
	}
	return nil, nil, nil
}

// CurrentAt reports whether the row's value of metric can stand for today's
// valuation at now: priced at a live quote (BuildCurrent already withholds
// too-old fundamentals), or a stored figure whose period ended at most
// MaxFundamentalAge before now. A stored valuation has no age bound
// elsewhere, but a 2016 P/E says nothing about what a sector costs today.
func (r ScreenerRow) CurrentAt(metric string, now time.Time) bool {
	v, _, _ := r.sectorFields(metric)
	basis, ok := r.Basis[metric]
	if v == nil || !ok {
		return false
	}
	return r.Current || monthsBetween(basis, now) <= MaxFundamentalAge
}

// ApplySectorMedians fills each row's sector medians: per band metric, the
// median over the other companies of the same category (the company itself is
// left out, so it is compared with its peers), when at least MinSectorPeers
// qualify; *SectorPeers counts them. A peer qualifies when its value is
// current at now (CurrentAt) and, for P/E and P/B, rests on figures of the
// same kind (standalone or group) as the row's own. A row without a current
// value of the metric, or without a category, gets no median: there is
// nothing to compare.
func ApplySectorMedians(rows []ScreenerRow, now time.Time) {
	byCat := map[string][]int{}
	for i, r := range rows {
		if r.Category != "" {
			byCat[r.Category] = append(byCat[r.Category], i)
		}
	}
	for _, idx := range byCat {
		for _, i := range idx {
			for _, m := range BandMetrics {
				_, median, peers := rows[i].sectorFields(m)
				*median, *peers = nil, 0
				if !rows[i].CurrentAt(m, now) {
					continue
				}
				own := rows[i].Basis[m].Standalone
				var vals []float64
				for _, j := range idx {
					if j == i || !rows[j].CurrentAt(m, now) {
						continue
					}
					if kindMatters(m) && rows[j].Basis[m].Standalone != own {
						continue
					}
					v, _, _ := rows[j].sectorFields(m)
					vals = append(vals, *v)
				}
				if len(vals) < MinSectorPeers {
					continue
				}
				med := Median(vals)
				*median, *peers = &med, len(vals)
			}
		}
	}
}
