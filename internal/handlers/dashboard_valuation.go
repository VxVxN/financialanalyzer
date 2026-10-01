package handlers

import (
	"fmt"
	"html"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/analytics"
)

// Thresholds of the relative-valuation verdicts: a percentile within the
// company's history at or beyond 25/75, or a gap to the sector median of 20%.
const (
	cheapPercentile  = 25
	dearPercentile   = 75
	sectorGapPercent = 20
)

var bandMetricNames = map[string]string{
	"pe":        "P/E",
	"pb":        "P/B",
	"div_yield": "Див. доходность",
}

// fmtBandValue formats a band metric (a ratio, or a yield in %).
func fmtBandValue(metric string, v float64) string {
	if metric == "div_yield" {
		return fmtPct(v)
	}
	return fmtRatio(v)
}

// historyVerdict reads a percentile within the company's history.
func historyVerdict(metric string, pct float64) (text, cls string) {
	if analytics.HigherIsCheaper(metric) {
		pct = 100 - pct
	}
	switch {
	case pct <= cheapPercentile:
		return "дёшево относительно истории", "pos"
	case pct >= dearPercentile:
		return "дорого относительно истории", "neg"
	}
	return "около обычного уровня", ""
}

// sectorVerdict compares a value with the sector median.
func sectorVerdict(metric string, v, median float64) (text, cls string) {
	if median <= 0 {
		return "", ""
	}
	gap := (v/median - 1) * 100
	cheaper := gap < 0
	if analytics.HigherIsCheaper(metric) {
		cheaper = !cheaper
	}
	switch {
	case math.Abs(gap) < sectorGapPercent:
		return "на уровне сектора", ""
	case cheaper:
		return fmt.Sprintf("дешевле сектора (%+.0f%%)", gap), "pos"
	default:
		return fmt.Sprintf("дороже сектора (%+.0f%%)", gap), "neg"
	}
}

// periodsWord agrees "период" with n.
func periodsWord(n int) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return "период"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return "периода"
	}
	return "периодов"
}

// renderRelativeValuation shows the company's valuation against its own
// history and its sector's median. row is the company's screener row (the same
// valuation basis and bands as the screener); peersKnown is false when the
// other companies could not be loaded, so the sector column is left out; now
// tells whether a stored valuation is too old to compare with the sector.
func renderRelativeValuation(w http.ResponseWriter, row analytics.ScreenerRow, peersKnown bool, now time.Time) {
	// Each value names its own period: a bank's P/E and P/B come from the
	// last Q4 while its latest row is usually a later quarter.
	basis := "по последним сохранённым данным"
	if row.Current {
		basis = "по цене закрытия " + row.PriceDate
	}
	fmt.Fprintf(w, `<div class="sub-title">Оценка относительно истории и сектора · %s</div>`, html.EscapeString(basis))
	renderValuationRulers(w, row, peersKnown)
	fmt.Fprint(w, `<details class="more"><summary>Подробная таблица</summary>
<div class="card rel-val"><div class="table-wrap"><table class="table">
<thead><tr><th>Показатель</th><th class="num">Сейчас</th><th class="num">Медиана истории</th><th class="num">Диапазон</th><th class="num">Перцентиль</th><th class="left">Относительно истории</th>`)
	if peersKnown {
		fmt.Fprintf(w, `<th class="num">Медиана сектора (аналогов)</th><th class="left">Относительно сектора</th>`)
	}
	fmt.Fprintf(w, `</tr></thead><tbody>`)

	values := map[string]*float64{"pe": row.PE, "pb": row.PB, "div_yield": row.DivYield}
	sector := map[string]*float64{"pe": row.PESector, "pb": row.PBSector, "div_yield": row.DivYieldSector}
	peers := map[string]int{"pe": row.PESectorPeers, "pb": row.PBSectorPeers, "div_yield": row.DivYieldSectorPeers}
	muted := func(s string) string { return `<span class="muted">` + html.EscapeString(s) + `</span>` }
	verdict := func(text, cls string) string {
		return `<span class="` + cls + `">` + html.EscapeString(text) + `</span>`
	}
	for _, m := range analytics.BandMetrics {
		v := values[m]
		cur, med, rng, pct, hist := "—", "—", "—", "—", muted("нет данных")
		if v != nil {
			cur = html.EscapeString(fmtBandValue(m, *v))
			if p, ok := row.Basis[m]; ok {
				cur += " " + muted("("+p.Label+")")
			}
			hist = muted("мало истории")
			if b, ok := row.Bands[m]; ok {
				med = fmtBandValue(m, b.Median)
				rng = fmt.Sprintf("%s – %s (%d %s, %s – %s)", fmtBandValue(m, b.Min), fmtBandValue(m, b.Max), b.N, periodsWord(b.N), b.From, b.To)
				pct = fmt.Sprintf("%.0f%%", b.Percentile)
				hist = verdict(historyVerdict(m, b.Percentile))
			}
		}
		fmt.Fprintf(w, `<tr><td>%s</td><td class="num">%s</td><td class="num">%s</td><td class="num">%s</td><td class="num">%s</td><td class="left">%s</td>`,
			html.EscapeString(bandMetricNames[m]), cur, html.EscapeString(med),
			html.EscapeString(rng), html.EscapeString(pct), hist)
		if peersKnown {
			smed, sv := "—", muted("мало сопоставимых аналогов")
			switch {
			case v == nil:
				sv = muted("нет данных")
			case !row.CurrentAt(m, now):
				sv = muted("оценка устарела")
			}
			if s := sector[m]; s != nil {
				smed = fmt.Sprintf("%s (%d)", fmtBandValue(m, *s), peers[m])
				sv = muted("нет данных")
				if v != nil {
					if text, cls := sectorVerdict(m, *v, *s); text != "" {
						sv = verdict(text, cls)
					}
				}
			}
			fmt.Fprintf(w, `<td class="num">%s</td><td class="left">%s</td>`, html.EscapeString(smed), sv)
		}
		fmt.Fprintf(w, `</tr>`)
	}
	fmt.Fprintf(w, `</tbody></table></div>
<div class="rel-val-note">В скобках у текущего значения — период отчётности, на котором оно основано. Перцентиль — доля периодов за последние %d лет, где показатель был ниже текущего; для P/E и P/B берутся только периоды того же типа отчётности (РСБУ/ЦБ юрлица или отчётность группы), убыточные периоды в P/E не входят.`, analytics.BandYears)
	if peersKnown {
		fmt.Fprintf(w, ` Медиана сектора — по остальным компаниям категории «%s» с оценкой по рыночной цене или по данным не старше %d месяцев, для P/E и P/B — того же типа отчётности; в скобках — число таких компаний, нужно не меньше %d. РСБУ материнской компании и данные ЦБ между компаниями сопоставимы плохо.`,
			html.EscapeString(orDash(row.Category)), analytics.MaxFundamentalAge, analytics.MinSectorPeers)
	}
	fmt.Fprint(w, `</div></div></details>`)
}

// renderValuationRulers draws each band metric on one scale: the company's
// historical range (bar), its historical median (short tick), the sector
// median (tall tick) and the current value (ring, blue when cheap against its
// history, orange when dear). The table below carries the exact figures.
func renderValuationRulers(w http.ResponseWriter, row analytics.ScreenerRow, peersKnown bool) {
	values := map[string]*float64{"pe": row.PE, "pb": row.PB, "div_yield": row.DivYield}
	sector := map[string]*float64{"pe": row.PESector, "pb": row.PBSector, "div_yield": row.DivYieldSector}
	drawn := 0
	fmt.Fprint(w, `<div class="rulers">`)
	for _, m := range analytics.BandMetrics {
		v := values[m]
		if v == nil {
			continue
		}
		hint := "ниже — дешевле"
		if analytics.HigherIsCheaper(m) {
			hint = "выше — дешевле"
		}
		b, hasBand := row.Bands[m]
		var sec *float64
		if peersKnown {
			sec = sector[m]
		}
		lo, hi := *v, *v
		if hasBand {
			lo, hi = math.Min(lo, b.Min), math.Max(hi, b.Max)
		}
		if sec != nil {
			lo, hi = math.Min(lo, *sec), math.Max(hi, *sec)
		}
		if hi == lo {
			lo, hi = lo-1, hi+1
		}
		pos := func(x float64) float64 { return (x - lo) / (hi - lo) * 100 }

		var marks strings.Builder
		marks.WriteString(`<div class="track"></div>`)
		cls, pct, label := "", "мало истории", bandMetricNames[m]+" "+fmtBandValue(m, *v)
		if hasBand {
			fmt.Fprintf(&marks, `<div class="range" style="left:%.1f%%;width:%.1f%%"></div><div class="median" style="left:%.1f%%"></div>`,
				pos(b.Min), pos(b.Max)-pos(b.Min), pos(b.Median))
			text, c := historyVerdict(m, b.Percentile)
			cls, pct = c, fmt.Sprintf("%.0f-й перцентиль", b.Percentile)
			label += fmt.Sprintf(", %s, история %s – %s, медиана %s", text,
				fmtBandValue(m, b.Min), fmtBandValue(m, b.Max), fmtBandValue(m, b.Median))
		}
		if sec != nil {
			fmt.Fprintf(&marks, `<div class="sector" style="left:%.1f%%"></div>`, pos(*sec))
			label += ", медиана сектора " + fmtBandValue(m, *sec)
		}
		fmt.Fprintf(&marks, `<div class="now %s" style="left:%.1f%%"></div><span class="lo">%s</span><span class="hi">%s</span>`,
			cls, pos(*v), html.EscapeString(fmtBandValue(m, lo)), html.EscapeString(fmtBandValue(m, hi)))

		fmt.Fprintf(w, `<div class="ruler-row">
  <div><div class="name">%s</div><div class="hint">%s</div></div>
  <div class="scale" role="img" aria-label="%s">%s</div>
  <div class="val"><div class="v %s">%s</div><div class="small muted">%s</div></div>
</div>`, html.EscapeString(bandMetricNames[m]), hint, html.EscapeString(label), marks.String(),
			cls, html.EscapeString(fmtBandValue(m, *v)), html.EscapeString(pct))
		drawn++
	}
	fmt.Fprint(w, `</div>`)
	if drawn == 0 {
		fmt.Fprint(w, `<p class="muted">Нет оценки: не хватает капитализации или прибыли.</p>`)
		return
	}
	fmt.Fprint(w, `<div class="ruler-legend"><span><i style="width:24px;height:8px;border-radius:4px;background:var(--border-strong)"></i>диапазон за 10 лет</span>`+
		`<span><i style="width:2px;height:12px;background:var(--text-3)"></i>медиана истории</span>`)
	if peersKnown {
		fmt.Fprint(w, `<span><i style="width:2px;height:16px;background:var(--text-1)"></i>медиана по сектору</span>`)
	}
	fmt.Fprint(w, `<span><i style="width:12px;height:12px;border-radius:50%;border:3px solid var(--cheap)"></i>сейчас</span></div>`)
}
