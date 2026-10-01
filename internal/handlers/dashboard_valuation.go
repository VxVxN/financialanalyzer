package handlers

import (
	"fmt"
	"html"
	"math"
	"net/http"

	"github.com/VxVxN/financialanalyzer/internal/analytics"
	"github.com/VxVxN/financialanalyzer/internal/models"
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

// renderRelativeValuation shows the company's valuation against its own
// history and its sector's median. row is the company's screener row (the same
// valuation basis as the screener); peersKnown is false when the other
// companies could not be loaded, so the sector column is left out.
func renderRelativeValuation(w http.ResponseWriter, history []models.QuarterData, row analytics.ScreenerRow, peersKnown bool) {
	basis := "по последним сохранённым данным за " + orDash(row.LastPeriod)
	if row.Current {
		basis = "по цене закрытия " + row.PriceDate
	}
	fmt.Fprintf(w, `<div class="section-title">Оценка относительно истории и сектора · %s</div>
<div class="score-breakdown rel-val"><div class="table-scroll"><table>
<thead><tr><th>Показатель</th><th class="num">Сейчас</th><th class="num">Медиана истории</th><th class="num">Диапазон</th><th class="num">Перцентиль</th><th>Относительно истории</th>`,
		html.EscapeString(basis))
	if peersKnown {
		fmt.Fprintf(w, `<th class="num">Медиана сектора</th><th>Относительно сектора</th>`)
	}
	fmt.Fprintf(w, `</tr></thead><tbody>`)

	values := map[string]*float64{"pe": row.PE, "pb": row.PB, "div_yield": row.DivYield}
	sector := map[string]*float64{"pe": row.PESector, "pb": row.PBSector, "div_yield": row.DivYieldSector}
	muted := func(s string) string { return `<span class="muted">` + html.EscapeString(s) + `</span>` }
	verdict := func(text, cls string) string {
		return `<span class="` + cls + `">` + html.EscapeString(text) + `</span>`
	}
	for _, m := range analytics.BandMetrics {
		v := values[m]
		cur, med, rng, pct, hist := "—", "—", "—", "—", muted("нет данных")
		if v != nil {
			cur = fmtBandValue(m, *v)
			hist = muted("мало истории")
			if b, ok := analytics.HistoricalBand(history, m, *v); ok {
				med = fmtBandValue(m, b.Median)
				rng = fmt.Sprintf("%s – %s (%d пер., %s – %s)", fmtBandValue(m, b.Min), fmtBandValue(m, b.Max), b.N, b.From, b.To)
				pct = fmt.Sprintf("%.0f%%", b.Percentile)
				hist = verdict(historyVerdict(m, b.Percentile))
			}
		}
		fmt.Fprintf(w, `<tr><td>%s</td><td class="num">%s</td><td class="num">%s</td><td class="num">%s</td><td class="num">%s</td><td>%s</td>`,
			html.EscapeString(bandMetricNames[m]), html.EscapeString(cur), html.EscapeString(med),
			html.EscapeString(rng), html.EscapeString(pct), hist)
		if peersKnown {
			smed, sv := "—", muted("мало аналогов")
			if s := sector[m]; s != nil {
				smed = fmtBandValue(m, *s)
				sv = muted("нет данных")
				if v != nil {
					if text, cls := sectorVerdict(m, *v, *s); text != "" {
						sv = verdict(text, cls)
					}
				}
			}
			fmt.Fprintf(w, `<td class="num">%s</td><td>%s</td>`, html.EscapeString(smed), sv)
		}
		fmt.Fprintf(w, `</tr>`)
	}
	fmt.Fprintf(w, `</tbody></table></div>
<div class="rel-val-note">Перцентиль — доля периодов за последние %d лет (только того же типа отчётности: РСБУ/ЦБ или МСФО), где показатель был ниже текущего; убыточные периоды в P/E не входят.`, analytics.BandYears)
	if peersKnown {
		fmt.Fprintf(w, ` Медиана сектора — по остальным компаниям категории «%s» (%d), на той же основе, что в скринере; нужно не меньше %d аналогов. РСБУ материнской компании и данные ЦБ между компаниями сопоставимы плохо.`,
			html.EscapeString(orDash(row.Category)), row.SectorPeers, analytics.MinSectorPeers)
	}
	fmt.Fprintf(w, `</div>
</div>`)
}
