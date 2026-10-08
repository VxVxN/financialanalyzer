package analytics

import (
	"fmt"
	"sort"
	"strings"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// CapJumpRatio is the year-over-year market-cap change at which a split
// missing from MOEX's list (and share_splits.txt), an extra issue or a
// buyback is the likely cause. A clean 1:2 is included; smaller moves are not.
const CapJumpRatio = 2.0

// CapJump is one pair of consecutive years whose year-end (latest-quarter)
// market caps differ by at least CapJumpRatio. Quarter is the later year's
// period the cap was taken from, so a chart can mark that point.
type CapJump struct {
	From, To int
	Quarter  string
	Ratio    float64 // later cap / earlier cap
}

type yearCap struct {
	value   float64
	quarter string
}

// CapJumps finds consecutive years whose market caps differ by at least
// CapJumpRatio in either direction. A gap year is not a pair. Within a year
// the latest quarter that reports a positive cap is the year-end figure.
func CapJumps(rows []models.QuarterData) []CapJump {
	caps := map[int]yearCap{}
	var years []int
	for _, r := range rows {
		if r.Capitalization == nil || *r.Capitalization <= 0 {
			continue
		}
		prev, seen := caps[r.Year]
		if !seen {
			years = append(years, r.Year)
		}
		if !seen || r.Quarter >= prev.quarter {
			caps[r.Year] = yearCap{value: *r.Capitalization, quarter: r.Quarter}
		}
	}
	sort.Ints(years)
	var out []CapJump
	for i := 1; i < len(years); i++ {
		if years[i] != years[i-1]+1 {
			continue
		}
		ratio := caps[years[i]].value / caps[years[i-1]].value
		if ratio >= CapJumpRatio || ratio <= 1/CapJumpRatio {
			out = append(out, CapJump{
				From: years[i-1], To: years[i], Quarter: caps[years[i]].quarter, Ratio: ratio,
			})
		}
	}
	return out
}

// Kinds a stored review of one cap jump may take. Price means the move is the
// market's, so both years stay in the valuation band. Error drops the later
// year as a bad point. Split, issue and buyback are a new share-count regime
// the stored caps do not adjust for, so the band starts at the later year —
// the same cut an unreviewed jump gets, until someone says the move is a price.
const (
	CapReviewSplit   = "split"
	CapReviewIssue   = "issue"
	CapReviewBuyback = "buyback"
	CapReviewError   = "error"
	CapReviewPrice   = "price"
)

// CapReview is one operator classification of a year-over-year cap jump.
type CapReview struct {
	Company  string
	From, To int
	Kind     string
}

// ValidCapReview reports whether kind is one of the stored classifications.
func ValidCapReview(kind string) bool {
	switch kind {
	case CapReviewSplit, CapReviewIssue, CapReviewBuyback, CapReviewError, CapReviewPrice:
		return true
	}
	return false
}

// CapReviewLabel is the Russian name of a review kind, empty when unknown.
func CapReviewLabel(kind string) string {
	switch kind {
	case CapReviewSplit:
		return "сплит"
	case CapReviewIssue:
		return "допэмиссия"
	case CapReviewBuyback:
		return "байбек"
	case CapReviewError:
		return "ошибка"
	case CapReviewPrice:
		return "это цена"
	}
	return ""
}

// ReviewKind is the stored kind for the from→to jump, or "" when none matches.
func ReviewKind(reviews []CapReview, from, to int) string {
	for _, r := range reviews {
		if r.From == from && r.To == to {
			return r.Kind
		}
	}
	return ""
}

// GroupCapReviews indexes reviews by company.
func GroupCapReviews(all []CapReview) map[string][]CapReview {
	out := map[string][]CapReview{}
	for _, r := range all {
		out[r.Company] = append(out[r.Company], r)
	}
	return out
}

// CapBandWindow is which years of a cap-based multiple may share one
// historical band. FromYear 0 means the series has no floor. Drop years are
// points thrown out as bad data.
type CapBandWindow struct {
	FromYear int
	Drop     map[int]bool
}

// Allows reports whether a period of year belongs in the band.
func (w CapBandWindow) Allows(year int) bool {
	if len(w.Drop) > 0 && w.Drop[year] {
		return false
	}
	return w.FromYear == 0 || year >= w.FromYear
}

// BandWindow decides which years stay comparable after cap jumps.
// A jump reviewed as a price keeps both years. A jump reviewed as an error
// drops the later year and keeps the rest. Every other jump — unreviewed, or
// a split, an issue or a buyback whose share count was not adjusted — starts
// the band at the later year, so a percentile is not built across the break.
func BandWindow(rows []models.QuarterData, reviews []CapReview) CapBandWindow {
	var w CapBandWindow
	for _, j := range CapJumps(rows) {
		switch ReviewKind(reviews, j.From, j.To) {
		case CapReviewPrice:
			continue
		case CapReviewError:
			if w.Drop == nil {
				w.Drop = map[int]bool{}
			}
			w.Drop[j.To] = true
		default:
			if j.To > w.FromYear {
				w.FromYear = j.To
			}
		}
	}
	return w
}

// CapJumpAnomalies marks the later year of each cap jump the way CheckRow
// marks a suspicious figure, so the capitalization chart can draw the same
// triangle. A jump reviewed as a price is not marked. reviews may be nil.
func CapJumpAnomalies(rows []models.QuarterData, reviews []CapReview) []Anomaly {
	var out []Anomaly
	for _, j := range CapJumps(rows) {
		kind := ReviewKind(reviews, j.From, j.To)
		if kind == CapReviewPrice {
			continue
		}
		out = append(out, Anomaly{
			Year: j.To, Quarter: j.Quarter, Label: qLabel(j.To, j.Quarter),
			Metrics: []string{"capitalization"},
			Message: capJumpMessage(j, kind),
		})
	}
	return out
}

func capJumpMessage(j CapJump, kind string) string {
	times := j.Ratio
	dir := "выросла"
	if j.Ratio < 1 {
		times = 1 / j.Ratio
		dir = "упала"
	}
	n := strings.ReplaceAll(fmt.Sprintf("%.1f", times), ".", ",")
	head := fmt.Sprintf("капитализация %s в %s раза к %d", dir, n, j.From)
	switch kind {
	case CapReviewSplit, CapReviewIssue, CapReviewBuyback:
		return fmt.Sprintf("%s — %s; в полосе оценки годы до %d не участвуют", head, CapReviewLabel(kind), j.To)
	case CapReviewError:
		return fmt.Sprintf("%s — ошибка данных, %d в полосе оценки не участвует", head, j.To)
	default:
		return head + " — возможны неучтённый сплит, допэмиссия или байбек"
	}
}
