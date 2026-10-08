package analytics

import (
	"sort"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// RSBUGap is a registry company whose newest stored year is still parent
// RSBU, with no group figures (manual IFRS or CSV) in that year.
type RSBUGap struct {
	Company string
	Year    int
}

// RSBUOnlyLatest lists names whose latest year in histories is RSBU and
// nothing else comparable. A name with no rows is skipped: there is no year
// to call RSBU. names is the ticker registry, not every company in the DB.
func RSBUOnlyLatest(names []string, histories map[string][]models.QuarterData) []RSBUGap {
	var out []RSBUGap
	for _, name := range names {
		hist := histories[name]
		if len(hist) == 0 {
			continue
		}
		latest := hist[0].Year
		for _, q := range hist {
			if q.Year > latest {
				latest = q.Year
			}
		}
		rsbu, group := false, false
		for _, q := range hist {
			if q.Year != latest {
				continue
			}
			switch q.Source {
			case models.SourceRSBU:
				rsbu = true
			case models.SourceManual, models.SourceCSV, models.SourceSmartLab:
				group = true
			}
		}
		if rsbu && !group {
			out = append(out, RSBUGap{Company: name, Year: latest})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Company < out[j].Company })
	return out
}
