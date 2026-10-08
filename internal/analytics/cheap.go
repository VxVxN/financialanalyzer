package analytics

import (
	"fmt"
	"sort"
	"strings"
)

// cheapHistPct and cheapVsSector are the screener presets "дешевле истории"
// and "дешевле сектора": P/E in the bottom quartile of the company's own
// 10-year band, and at least 20% below the sector median.
const (
	cheapHistPct  = 25.0
	cheapVsSector = -20.0
	cheapListMax  = 25
)

// CheapListMessage is the weekly operator note: comparable companies that are
// cheap against both their own history and their sector. An empty list still
// produces a sentence, so a quiet week is visible.
func CheapListMessage(rows []ScreenerRow) string {
	type pick struct {
		row ScreenerRow
		vs  float64
	}
	var picks []pick
	for _, r := range rows {
		if !r.Comparable || r.Bank || !r.Liquid || r.PE == nil || r.PEHistPct == nil || r.PESector == nil || *r.PESector <= 0 {
			continue
		}
		if *r.PEHistPct > cheapHistPct {
			continue
		}
		vs := (*r.PE / *r.PESector - 1) * 100
		if vs > cheapVsSector {
			continue
		}
		picks = append(picks, pick{r, vs})
	}
	sort.Slice(picks, func(i, j int) bool {
		if picks[i].row.PEHistPct == picks[j].row.PEHistPct {
			return picks[i].row.Company < picks[j].row.Company
		}
		return *picks[i].row.PEHistPct < *picks[j].row.PEHistPct
	})

	var b strings.Builder
	b.WriteString("Дешевле своей истории и сектора\n")
	b.WriteString("P/E в нижней четверти 10 лет и минимум на 20% ниже медианы сектора. " +
		"Только сопоставимая отчётность, без банков, средний дневной оборот от 10 млн ₽ (если он уже известен).\n")
	if len(picks) == 0 {
		b.WriteString("Таких компаний нет.")
		return b.String()
	}
	n := len(picks)
	if n > cheapListMax {
		n = cheapListMax
	}
	for _, p := range picks[:n] {
		fmt.Fprintf(&b, "%s — P/E %.1f, история %.0f%%, к сектору %+.0f%%\n",
			p.row.Company, *p.row.PE, *p.row.PEHistPct, p.vs)
	}
	if len(picks) > cheapListMax {
		fmt.Fprintf(&b, "… и ещё %d\n", len(picks)-cheapListMax)
	}
	return strings.TrimRight(b.String(), "\n")
}
