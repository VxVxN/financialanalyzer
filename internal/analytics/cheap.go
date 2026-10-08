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

// dearHistPct is the top quartile of a company's own 10-year P/E band.
const dearHistPct = 100 - cheapHistPct

// NoteQuote is a stored close older than QuoteMaxAge.
type NoteQuote struct {
	Company string
	Date    string // DD.MM.YYYY
}

// NoteJump is one capitalization jump to mention in the Monday note.
type NoteJump struct {
	Company  string
	From, To int
	Ratio    float64
}

// NoteYear is an IFRS year the pull wrote that had no manual row before.
type NoteYear struct {
	Company string
	Year    int
}

// BandLow, BandMid, BandHigh and BandNone are the P/E quartile buckets stored
// between Monday notes. Low is the bottom quartile, high the top one.
const (
	BandLow  = "low"
	BandMid  = "mid"
	BandHigh = "high"
	BandNone = "none"
)

// PEBand is the quartile bucket of a screener row's own 10-year P/E.
// BandNone means the row has no band (too few years, or a loss).
func PEBand(r ScreenerRow) string {
	if !r.Portfolio || r.PEHistPct == nil {
		return BandNone
	}
	switch {
	case *r.PEHistPct <= cheapHistPct:
		return BandLow
	case *r.PEHistPct >= dearHistPct:
		return BandHigh
	default:
		return BandMid
	}
}

// NextPortfolioBands is the bucket to remember for each portfolio row, whether
// or not it is mentioned in this week's note.
func NextPortfolioBands(rows []ScreenerRow) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		if r.Portfolio {
			out[r.Company] = PEBand(r)
		}
	}
	return out
}

// MondayMessage is the weekly operator note: the cheap list, then the events
// the pipelines already see. prev is the P/E bucket stored after the previous
// note (nil on the first one). A portfolio name is listed when its P/E is in
// an outer quartile and was not there last time. An empty section still says
// so, except IFRS years, which are listed only when one was written since the
// previous note.
func MondayMessage(rows []ScreenerRow, stale []NoteQuote, jumps []NoteJump, years []NoteYear, prev map[string]string) string {
	var b strings.Builder
	b.WriteString(CheapListMessage(rows))
	b.WriteString("\n\n")
	writePortfolioBand(&b, rows, prev)
	b.WriteString("\n\n")
	writeCapped(&b, "Котировки старше 30 дней", "Таких котировок нет.", len(stale), func(i int) string {
		return fmt.Sprintf("%s — %s", stale[i].Company, stale[i].Date)
	})
	b.WriteString("\n\n")
	writeCapped(&b, "Скачок капитализации\nГод к году в два раза и больше — возможны неучтённый сплит, допэмиссия или байбэк.",
		"Таких скачков нет.", len(jumps), func(i int) string {
			return fmt.Sprintf("%s — %d→%d, %s", jumps[i].Company, jumps[i].From, jumps[i].To, ratioText(jumps[i].Ratio))
		})
	if len(years) > 0 {
		b.WriteString("\n\n")
		writeCapped(&b, "МСФО: дописан год, которого не было", "", len(years), func(i int) string {
			return fmt.Sprintf("%s — %d", years[i].Company, years[i].Year)
		})
	}
	return strings.TrimRight(b.String(), "\n")
}

func writePortfolioBand(b *strings.Builder, rows []ScreenerRow, prev map[string]string) {
	type pick struct {
		row ScreenerRow
	}
	var picks []pick
	for _, r := range rows {
		band := PEBand(r)
		if band != BandLow && band != BandHigh {
			continue
		}
		if prev[r.Company] == band {
			continue
		}
		if r.PE == nil {
			continue
		}
		picks = append(picks, pick{r})
	}
	sort.Slice(picks, func(i, j int) bool { return picks[i].row.Company < picks[j].row.Company })
	b.WriteString("Портфель: P/E у края десятилетки\n")
	b.WriteString("С прошлой заметки P/E вошёл в нижнюю или верхнюю четверть собственных 10 лет.\n")
	if len(picks) == 0 {
		b.WriteString("Таких бумаг нет.")
		return
	}
	n := len(picks)
	if n > cheapListMax {
		n = cheapListMax
	}
	for _, p := range picks[:n] {
		side := "нижнюю"
		if *p.row.PEHistPct >= dearHistPct {
			side = "верхнюю"
		}
		fmt.Fprintf(b, "%s — P/E %.1f вошёл в %s четверть (история %.0f%%)\n",
			p.row.Company, *p.row.PE, side, *p.row.PEHistPct)
	}
	if len(picks) > cheapListMax {
		fmt.Fprintf(b, "… и ещё %d\n", len(picks)-cheapListMax)
	}
}

func writeCapped(b *strings.Builder, title, empty string, n int, line func(i int) string) {
	b.WriteString(title)
	b.WriteByte('\n')
	if n == 0 {
		b.WriteString(empty)
		return
	}
	show := n
	if show > cheapListMax {
		show = cheapListMax
	}
	for i := 0; i < show; i++ {
		b.WriteString(line(i))
		b.WriteByte('\n')
	}
	if n > cheapListMax {
		fmt.Fprintf(b, "… и ещё %d\n", n-cheapListMax)
	}
}

func ratioText(ratio float64) string {
	times := ratio
	dir := "больше"
	if ratio < 1 && ratio > 0 {
		times = 1 / ratio
		dir = "меньше"
	}
	n := strings.ReplaceAll(fmt.Sprintf("%.1f", times), ".", ",")
	return "в " + n + " раза " + dir
}
