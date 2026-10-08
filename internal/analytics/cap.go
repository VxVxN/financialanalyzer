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

// CapJumpAnomalies marks the later year of each cap jump the way CheckRow
// marks a suspicious figure, so the capitalization chart can draw the same
// triangle. The message names both years.
func CapJumpAnomalies(rows []models.QuarterData) []Anomaly {
	var out []Anomaly
	for _, j := range CapJumps(rows) {
		out = append(out, Anomaly{
			Year: j.To, Quarter: j.Quarter, Label: qLabel(j.To, j.Quarter),
			Metrics: []string{"capitalization"},
			Message: capJumpMessage(j),
		})
	}
	return out
}

func capJumpMessage(j CapJump) string {
	times := j.Ratio
	dir := "выросла"
	if j.Ratio < 1 {
		times = 1 / j.Ratio
		dir = "упала"
	}
	n := strings.ReplaceAll(fmt.Sprintf("%.1f", times), ".", ",")
	return fmt.Sprintf("капитализация %s в %s раза к %d — возможны неучтённый сплит, допэмиссия или байбэк",
		dir, n, j.From)
}
