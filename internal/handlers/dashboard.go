package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/VxVxN/financialanalyzer/internal/analytics"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

// dashboardCSS is the company card's own layout; tokens and components come
// from the shared stylesheet.
const dashboardCSS = `
.report { display: grid; grid-template-columns: minmax(0, 1fr) 300px; gap: 48px; align-items: start; }
.report-main { display: flex; flex-direction: column; gap: 48px; min-width: 0; }
.report-side { position: sticky; top: 84px; display: flex; flex-direction: column; gap: 24px; font-size: 14px; }
.crumbs { font-size: 13px; color: var(--text-3); }
.hero { display: flex; flex-direction: column; gap: 14px; }
.hero h1 { font-family: var(--font-serif); font-size: 52px; font-weight: 600; letter-spacing: -.02em; line-height: 1.05; }
.verdict { font-family: var(--font-serif); font-size: 22px; line-height: 1.4; color: var(--text-2); margin: 0; max-width: 760px; }
.tags { display: flex; gap: 8px; flex-wrap: wrap; }
.strip { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); border-top: 2px solid var(--text-1); border-bottom: 1px solid var(--border); }
.strip > div { padding: 14px 16px 16px 0; display: flex; flex-direction: column; gap: 2px; }
.strip .l { font-size: 12px; color: var(--text-3); text-transform: uppercase; letter-spacing: .06em; }
.strip .v { font-family: var(--font-serif); font-size: 28px; font-weight: 500; font-variant-numeric: tabular-nums lining-nums; }
.strip .s { font-size: 13px; color: var(--text-3); }
.sub-title { font-size: 15px; font-weight: 600; margin: 24px 0 10px; }
.stale-note { color: var(--text-2); margin: 0; }
.kpi-grid { display: grid; gap: 10px; grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); }
.kpi { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 12px 14px; }
.kpi .name { font-size: 12px; color: var(--text-3); margin-bottom: 4px; }
.kpi .value { font-size: 20px; font-weight: 600; font-variant-numeric: tabular-nums; }
.kpi .sub { font-size: 12px; color: var(--text-3); margin-top: 2px; }
.kpi .sub.pos { color: var(--up); }
.kpi .sub.neg { color: var(--down); }
.kpi.empty .value { color: var(--text-3); font-weight: 400; }
.rulers { display: flex; flex-direction: column; }
.ruler-row { display: grid; grid-template-columns: 170px minmax(0, 1fr) 120px; gap: 20px; align-items: center; padding: 14px 0; border-top: 1px solid var(--border); }
.ruler-row .name { font-weight: 600; }
.ruler-row .hint { font-size: 12px; color: var(--text-3); }
.scale { position: relative; height: 44px; }
.scale .track { position: absolute; left: 0; right: 0; top: 20px; height: 2px; background: var(--border); }
.scale .range { position: absolute; top: 17px; height: 8px; border-radius: 4px; background: var(--border-strong); }
.scale .median { position: absolute; top: 14px; width: 2px; height: 14px; margin-left: -1px; background: var(--text-3); }
.scale .sector { position: absolute; top: 6px; width: 2px; height: 30px; margin-left: -1px; background: var(--text-1); }
.scale .now { position: absolute; top: 11px; width: 20px; height: 20px; margin-left: -10px; border-radius: 50%; border: 3px solid var(--text-2); background: var(--surface); }
.scale .now.pos { border-color: var(--cheap); }
.scale .now.neg { border-color: var(--dear); }
.scale .lo, .scale .hi { position: absolute; top: 30px; font-size: 11px; color: var(--text-3); font-variant-numeric: tabular-nums; }
.scale .lo { left: 0; } .scale .hi { right: 0; }
.ruler-row .val { text-align: right; }
.ruler-row .val .v { font-family: var(--font-serif); font-size: 22px; font-variant-numeric: tabular-nums; }
.ruler-row .val .v.pos { color: var(--cheap); } .ruler-row .val .v.neg { color: var(--dear); }
.ruler-legend { display: flex; gap: 18px; flex-wrap: wrap; font-size: 12px; color: var(--text-3); margin-top: 8px; }
.ruler-legend i { display: inline-block; vertical-align: middle; margin-right: 6px; }
.rel-val .pos { color: var(--cheap); }
.rel-val .neg { color: var(--dear); }
.rel-val td { white-space: nowrap; }
.rel-val-note { font-size: 12px; color: var(--text-3); padding: 10px 12px; line-height: 1.5; }
.sparkline-grid { display: grid; gap: 10px; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); margin-bottom: 16px; }
.spark { border: 1px solid var(--border); border-radius: var(--radius); padding: 12px 14px; background: var(--surface); }
.spark .name { font-size: 12px; color: var(--text-3); }
.spark .value { font-size: 20px; font-weight: 600; font-variant-numeric: tabular-nums; }
.spark .delta { font-size: 12px; color: var(--text-3); }
.spark .delta.pos { color: var(--up); }
.spark .delta.neg { color: var(--down); }
.spark svg { display: block; width: 100%; height: 48px; margin-top: 6px; color: var(--accent); }
.spark svg.pos { color: var(--up); } .spark svg.neg { color: var(--down); }
.chart-controls { display: flex; gap: 12px; align-items: flex-start; flex-wrap: wrap; margin-bottom: 8px; }
.metric-chips { display: flex; gap: 6px; flex-wrap: wrap; flex: 1; }
.metric-chips .chip-btn { min-height: 30px; font-size: 12px; }
.chart-card { padding: 4px; overflow: hidden; }
.chart-card iframe { display: block; width: 100%; min-height: 640px; border: 0; background: transparent; }
.groups { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 16px; }
.group { padding: 4px 16px 8px; }
.group h3 { font-size: 13px; font-weight: 600; color: var(--text-3); text-transform: uppercase; letter-spacing: .06em; padding: 12px 0 6px; }
.group dl { margin: 0; }
.group .row { display: flex; justify-content: space-between; gap: 12px; padding: 8px 0; border-top: 1px solid var(--border); }
.group dt { color: var(--text-2); }
.group dd { margin: 0; text-align: right; font-variant-numeric: tabular-nums; font-weight: 500; }
.group dd .sub { display: block; font-size: 12px; font-weight: 400; color: var(--text-3); }
.group dd .sub.pos { color: var(--up); } .group dd .sub.neg { color: var(--down); }
.score-head { display: flex; align-items: baseline; gap: 12px; padding: 16px 16px 0; }
.score-head .big { font-family: var(--font-serif); font-size: 40px; font-weight: 600; }
.score-breakdown td.num { text-align: right; }
.score-breakdown .good { color: var(--cheap); } .score-breakdown .weak { color: var(--warn); } .score-breakdown .none { color: var(--text-3); }
.score-note { font-size: 12px; color: var(--text-3); padding: 10px 16px 14px; }
.side-block { display: flex; flex-direction: column; gap: 10px; padding-top: 16px; border-top: 2px solid var(--text-1); }
.side-block h2 { font-family: var(--font-serif); font-size: 18px; font-weight: 600; }
.side-actions { display: flex; flex-direction: column; gap: 8px; }
.side-actions .btn { width: 100%; min-height: 44px; }
.source-row { display: flex; justify-content: space-between; gap: 12px; }
.source-row span:first-child { color: var(--text-2); }
.quality { display: flex; flex-direction: column; gap: 8px; padding: 12px 14px; border-radius: var(--radius); background: var(--warn-soft); color: var(--warn-ink); font-size: 13px; }
.quality .title { font-weight: 600; }
.quality p { margin: 0; }
.quality ul { margin: 0; padding-left: 18px; }
.quality .period { font-variant-numeric: tabular-nums; margin-right: 6px; opacity: .8; }
.note-actions { display: flex; justify-content: space-between; align-items: center; gap: 8px; }
.note-actions .status { font-size: 12px; color: var(--text-3); }
.toc { display: flex; flex-direction: column; gap: 6px; padding-top: 14px; border-top: 1px solid var(--border); font-size: 13px; }
section[id] { scroll-margin-top: 80px; }
@media (max-width: 1080px) {
  .report { grid-template-columns: 1fr; gap: 32px; }
  .report-side { position: static; }
  .toc { display: none; }
}
@media (max-width: 640px) {
  .hero h1 { font-size: 38px; }
  .verdict { font-size: 18px; }
  .ruler-row { grid-template-columns: 1fr; gap: 6px; }
  .ruler-row .val { text-align: left; }
}
`

// DashboardHandler renders /company/{name}: the company card, laid out like an
// analyst's note — a verdict on valuation, then numbered sections (valuation,
// dynamics, all figures, score, manual entries) beside a column of sources,
// data quality and notes. Everything is server-rendered; the theme is applied
// client-side by the shared layout.
func (controller *Controller) DashboardHandler(w http.ResponseWriter, r *http.Request) {
	company := strings.TrimSpace(chi.URLParam(r, "name"))
	if company == "" {
		http.Error(w, "company is required", http.StatusBadRequest)
		return
	}

	history, err := controller.repo.GetCompanyHistory(r.Context(), company)
	if err != nil {
		controller.htmlServerError(w, "failed to load company history", err)
		return
	}
	// A missing note must not break the dashboard; log and render without it.
	note, err := controller.repo.GetCompanyNote(r.Context(), company)
	if err != nil {
		controller.logger.Warn("failed to load company note", "company", company, "error", err)
	}
	snap := analytics.BuildSnapshot(history)
	// Like the note, failing to load manual entries only hides them.
	manual, err := controller.repo.GetManualFinancials(r.Context(), company)
	if err != nil {
		controller.logger.Warn("failed to load manual financials", "company", company, "error", err)
	}
	// Like the note, a missing quote only hides the current-valuation block.
	quote, hasQuote, err := controller.repo.GetMarketQuote(r.Context(), company)
	if err != nil {
		controller.logger.Warn("failed to load market quote", "company", company, "error", err)
	}

	ownRow, peersKnown := controller.relativeValuationRow(r.Context(), company, history, quote, hasQuote)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, "<!DOCTYPE html>\n<html lang=\"ru\">\n<head>\n")
	if err := writeLayout(w, "head", pageMeta{Title: company}); err != nil {
		controller.logger.Error("failed to render layout", "error", err)
		return
	}
	fmt.Fprintf(w, "<style>%s%s</style>\n</head>\n<body>\n", dashboardCSS, manualCSS)
	_ = writeLayout(w, "topbar", pageMeta{})
	fmt.Fprint(w, `<main id="main" class="page page-narrow">`)

	if len(history) == 0 {
		fmt.Fprintf(w, `<div class="card empty">Нет данных по компании <b>%s</b>. <a href="/">К скринеру</a></div></main>`,
			html.EscapeString(company))
		_ = writeLayout(w, "footer", pageMeta{})
		fmt.Fprint(w, `</body></html>`)
		return
	}

	sources := analytics.Sources(history)
	freshQuote := hasQuote && analytics.QuoteIsFresh(quote, controller.now())

	fmt.Fprint(w, `<div class="report"><article class="report-main">`)
	renderDashboardHeader(w, snap, sources, ownRow, peersKnown, inPortfolio(company))
	var current *analytics.Current
	if freshQuote {
		c := analytics.BuildCurrent(history, quote)
		current = &c
	}
	renderHeadlineStrip(w, snap, current)

	fmt.Fprint(w, `<section id="valuation" aria-labelledby="h-valuation">`)
	sectionHead(w, "01", "h-valuation", "Оценка", "")
	switch {
	case current != nil:
		renderCurrent(w, *current)
	case hasQuote && quote.Capitalization > 0:
		fmt.Fprintf(w, `<div class="sub-title">Текущая оценка</div><p class="stale-note">Последняя сохранённая цена закрытия от %s — слишком старая для оценки (обновите котировки на <a href="/updates">странице обновления данных</a>).</p>`,
			html.EscapeString(quote.PriceDate.Format("2006-01-02")))
	}
	renderRelativeValuation(w, ownRow, peersKnown, controller.now())
	fmt.Fprint(w, `</section>`)

	fmt.Fprint(w, `<section id="dynamics" aria-labelledby="h-dynamics">`)
	sectionHead(w, "02", "h-dynamics", "Динамика", "")
	renderSparklines(w, history)
	renderDashboardChart(w, company)
	fmt.Fprint(w, `</section>`)

	fmt.Fprint(w, `<section id="figures" aria-labelledby="h-figures">`)
	sectionHead(w, "03", "h-figures", "Все показатели", "")
	renderKPIs(w, snap)
	fmt.Fprint(w, `</section>`)

	fmt.Fprint(w, `<section id="score" aria-labelledby="h-score">`)
	sectionHead(w, "04", "h-score", "Балл", "")
	renderScoreBreakdown(w, snap)
	fmt.Fprint(w, `</section>`)

	fmt.Fprint(w, `<section id="manual" aria-labelledby="h-manual">`)
	sectionHead(w, "05", "h-manual", "Ручные данные", "годовой отчёт МСФО")
	renderManual(w, company, manual, controller.now().Year()-1)
	fmt.Fprint(w, `</section>`)
	fmt.Fprint(w, `</article>`)

	fmt.Fprint(w, `<aside class="report-side" aria-label="О данных">`)
	renderSideActions(w)
	renderSources(w, history, sources, quote, freshQuote)
	renderDataQuality(w, sources, analytics.CheckHistory(history))
	renderNotes(w, note)
	fmt.Fprint(w, `<nav class="toc" aria-label="Разделы карточки">
  <a href="#valuation">01 · Оценка</a>
  <a href="#dynamics">02 · Динамика</a>
  <a href="#figures">03 · Все показатели</a>
  <a href="#score">04 · Балл</a>
  <a href="#manual">05 · Ручные данные</a>
</nav>`)
	fmt.Fprint(w, `</aside></div></main>`)

	_ = writeLayout(w, "footer", pageMeta{})
	fmt.Fprintf(w, `<script>
(function () {
  const company = %s;
  const FA = window.FA;
  const $ = (id) => document.getElementById(id);

  // Comparison toggle.
  const cmp = $('compareToggle');
  function syncCompare() {
    const on = FA.compare.has(company);
    cmp.setAttribute('aria-pressed', String(on));
    cmp.textContent = on ? '✓ В сравнении' : 'Добавить в сравнение';
  }
  cmp.addEventListener('click', () => {
    if (FA.compare.has(company)) FA.compare.remove(company);
    else if (!FA.compare.add(company)) cmp.textContent = 'В сравнении уже ' + FA.compare.max + ' компаний';
    syncCompare();
  });
  FA.compare.onChange(syncCompare);
  syncCompare();

  // Chart: metric and period pick the framed chart page; it follows the theme.
  let metric = 'revenue', period = 'ttm';
  const frame = $('dash-chart');
  function chartSrc() {
    return '/chart/' + metric + '?period=' + period + '&theme=' + FA.theme() + '&companies=' + encodeURIComponent(company);
  }
  frame.addEventListener('load', () => {
    try { frame.style.height = frame.contentDocument.documentElement.scrollHeight + 'px'; } catch (e) {}
  });
  function reload() { frame.src = chartSrc(); }
  const controls = document.querySelector('.chart-controls');
  controls.querySelectorAll('[data-metric]').forEach((b) => b.addEventListener('click', () => {
    metric = b.dataset.metric;
    controls.querySelectorAll('[data-metric]').forEach((x) => x.setAttribute('aria-pressed', String(x === b)));
    reload();
  }));
  controls.querySelectorAll('[data-period]').forEach((b) => b.addEventListener('click', () => {
    period = b.dataset.period;
    controls.querySelectorAll('[data-period]').forEach((x) => x.setAttribute('aria-pressed', String(x === b)));
    reload();
  }));
  FA.onThemeChange(reload);
  reload();

  // Notes.
  $('noteSave').addEventListener('click', async () => {
    const status = $('noteStatus');
    status.textContent = 'Сохранение…';
    try {
      const resp = await fetch('/api/company-note', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ company: company, note: $('noteText').value })
      });
      if (!resp.ok) throw new Error('не удалось сохранить заметку');
      status.textContent = 'Сохранено';
      setTimeout(() => { status.textContent = ''; }, 2000);
    } catch (err) {
      status.textContent = 'Ошибка: ' + err.message;
    }
  });
})();
</script>
</body></html>`, jsonForScript(company))
}

// sectionHead renders a numbered section heading of the card.
func sectionHead(w http.ResponseWriter, num, id, title, sub string) {
	subHTML := ""
	if sub != "" {
		subHTML = `<span class="muted">` + html.EscapeString(sub) + `</span>`
	}
	fmt.Fprintf(w, `<div class="section-head"><span class="section-num">%s</span><h2 class="section-title" id="%s">%s</h2>%s</div>`,
		num, id, html.EscapeString(title), subHTML)
}

// relativeValuationRow returns the company's screener row with its sector
// medians, the same figures the screener shows. The medians need every
// company's row; failing to build them only hides the sector column
// (peersKnown false).
func (controller *Controller) relativeValuationRow(ctx context.Context, company string, history []models.QuarterData, quote models.MarketQuote, hasQuote bool) (row analytics.ScreenerRow, peersKnown bool) {
	if len(history) == 0 {
		return analytics.ScreenerRow{}, false
	}
	rows, err := controller.buildScreener(ctx)
	if err != nil {
		controller.logger.Warn("failed to build peer valuations", "company", company, "error", err)
	}
	for _, r := range rows {
		if r.Company == company {
			return r, err == nil
		}
	}
	var q *models.MarketQuote
	if hasQuote && analytics.QuoteIsFresh(quote, controller.now()) {
		q = &quote
	}
	revs, revErr := controller.repo.CapReviews(ctx)
	if revErr != nil {
		controller.logger.Warn("failed to load cap reviews", "company", company, "error", revErr)
	}
	own := analytics.BuildScreenerRow(history, q, analytics.GroupCapReviews(revs)[company])
	if err != nil {
		return own, false
	}
	rows = append(rows, own)
	analytics.ApplySectorMedians(rows, controller.now())
	return rows[len(rows)-1], true
}

// jsonForScript encodes v as a JavaScript literal for an inline <script>:
// json.Marshal escapes <, > and &, so the value cannot close the script.
func jsonForScript(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// verdictPill turns a verdict class ("pos" cheap, "neg" dear) into a pill.
func verdictPill(text, cls string) string {
	pill := "pill"
	switch cls {
	case "pos":
		pill += " pill-cheap"
	case "neg":
		pill += " pill-dear"
	}
	return `<span class="` + pill + `">` + html.EscapeString(text) + `</span>`
}

// valuationVerdict is the card's headline: P/E (else P/B, else yield) against
// the company's own history and its sector, in one sentence, plus the verdict
// pills. ok is false when there is nothing to say.
func valuationVerdict(row analytics.ScreenerRow, peersKnown bool) (sentence string, pills []string, ok bool) {
	values := map[string]*float64{"pe": row.PE, "pb": row.PB, "div_yield": row.DivYield}
	sector := map[string]*float64{"pe": row.PESector, "pb": row.PBSector, "div_yield": row.DivYieldSector}
	for _, m := range analytics.BandMetrics {
		v := values[m]
		if v == nil {
			continue
		}
		var parts []string
		if b, has := row.Bands[m]; has {
			parts = append(parts, fmt.Sprintf("%.0f-й перцентиль собственной истории за %s – %s", b.Percentile, b.From, b.To))
			text, cls := historyVerdict(m, b.Percentile)
			pills = append(pills, verdictPill(text, cls))
		}
		if s := sector[m]; peersKnown && s != nil && *s > 0 {
			gap := (*v / *s - 1) * 100
			dir := "выше"
			if gap < 0 {
				dir = "ниже"
			}
			parts = append(parts, fmt.Sprintf("на %.0f%% %s медианы сектора (%s)", math.Abs(gap), dir, fmtBandValue(m, *s)))
			if text, cls := sectorVerdict(m, *v, *s); text != "" {
				pills = append(pills, verdictPill(text, cls))
			}
		}
		basis := ""
		if m != "div_yield" {
			if p, ok := row.Basis[m]; ok {
				basis = " " + analytics.ReportingPhrase(row.Bank, p.Standalone)
			}
		}
		// A multiple with no history band and no sector peers still names
		// what it rests on: that is the sentence when nothing else compares.
		if len(parts) == 0 {
			if basis == "" {
				continue
			}
			return fmt.Sprintf("%s %s%s.", bandMetricNames[m], fmtBandValue(m, *v), basis), pills, true
		}
		return fmt.Sprintf("%s %s%s — %s.", bandMetricNames[m], fmtBandValue(m, *v), basis, strings.Join(parts, " и ")), pills, true
	}
	return "", nil, false
}

func renderDashboardHeader(w http.ResponseWriter, s analytics.Snapshot, sources []string, row analytics.ScreenerRow, peersKnown, held bool) {
	srcLabel, srcNotes, comparable := sourcesLabel(sources)
	sentence, pills, ok := valuationVerdict(row, peersKnown)
	if !ok {
		sentence = "Последний отчётный период: " + orDash(s.LastLabel) + "."
	}
	srcPill := "pill"
	if !comparable {
		srcPill += " pill-warn"
	}
	if held {
		pills = append([]string{`<span class="pill pill-accent">В портфеле</span>`}, pills...)
	}
	pills = append(pills, fmt.Sprintf(`<span class="%s" title="%s">Источник: %s</span>`,
		srcPill, html.EscapeString(srcNotes), html.EscapeString(srcLabel)))
	category := orDash(s.Category)
	fmt.Fprintf(w, `<header class="hero">
  <div class="crumbs"><a href="/">Скринер</a> / %s</div>
  <h1>%s</h1>
  <p class="verdict">%s</p>
  <div class="tags">%s</div>
</header>`,
		html.EscapeString(category), html.EscapeString(s.Company),
		html.EscapeString(sentence), strings.Join(pills, ""))
}

// renderHeadlineStrip shows the few figures a reader wants first: size (at
// the fresh quote when there is one), earnings, returns and the score.
func renderHeadlineStrip(w http.ResponseWriter, s analytics.Snapshot, current *analytics.Current) {
	capCell := kpiCard{name: "Капитализация", value: fmtMoney(s.Capitalization), sub: orDash(s.CapLabel)}
	if current != nil {
		capCell = kpiCard{name: "Капитализация", value: fmtMoney(current.Capitalization), sub: "закрытие " + current.PriceDate}
	}
	cells := []kpiCard{
		capCell,
		{name: "Выручка LTM", value: fmtMoney(s.Revenue), sub: fmtSignedPctSub("г/г", s.RevenueYoY)},
		{name: "Прибыль LTM", value: fmtMoney(s.NetProfit), sub: fmtSignedPctSub("г/г", s.NetProfitYoY)},
		{name: "ROE", value: fmtPct(s.ROE), sub: s.ROELabel},
		{name: "Балл", value: fmt.Sprintf("%d", s.Score), sub: scoreSub(s)},
	}
	fmt.Fprint(w, `<div class="strip">`)
	for _, c := range cells {
		fmt.Fprintf(w, `<div><span class="l">%s</span><span class="v">%s</span><span class="s">%s</span></div>`,
			html.EscapeString(c.name), html.EscapeString(c.value), html.EscapeString(c.sub))
	}
	fmt.Fprint(w, `</div>`)
}

func renderSideActions(w http.ResponseWriter) {
	fmt.Fprint(w, `<div class="side-actions">
  <button type="button" class="btn btn-primary" id="compareToggle" aria-pressed="false">Добавить в сравнение</button>
  <a class="btn" href="#manual">Ввести годовой МСФО</a>
</div>`)
}

// renderSources lists where the figures come from and the span they cover.
func renderSources(w http.ResponseWriter, history []models.QuarterData, sources []string, quote models.MarketQuote, freshQuote bool) {
	first, last := history[0], history[len(history)-1]
	fmt.Fprint(w, `<div class="side-block"><h2>Источники</h2>`)
	for _, src := range sources {
		fmt.Fprintf(w, `<div class="source-row" title="%s"><span>%s</span></div>`,
			html.EscapeString(analytics.SourceNote(src)), html.EscapeString(analytics.SourceLabel(src)))
	}
	fmt.Fprintf(w, `<div class="source-row"><span>Периоды</span><span class="num muted">%d-%s – %d-%s</span></div>`,
		first.Year, html.EscapeString(first.Quarter), last.Year, html.EscapeString(last.Quarter))
	if freshQuote {
		fmt.Fprintf(w, `<div class="source-row"><span>Котировка MOEX</span><span class="num muted">%s</span></div>`,
			html.EscapeString(quote.PriceDate.Format("02.01.2006")))
	}
	fmt.Fprint(w, `<a class="small" href="/updates">Журнал обновлений →</a></div>`)
}

// maxQualityItems caps the anomaly list so a long bad history stays readable.
const maxQualityItems = 8

// renderDataQuality warns when the figures come from a non-IFRS source or look
// suspicious. It renders nothing for clean, comparable data.
func renderDataQuality(w http.ResponseWriter, sources []string, anomalies []analytics.Anomaly) {
	var caveats []string
	for _, src := range sources {
		if !analytics.IsComparable(src) {
			caveats = append(caveats, "<b>"+html.EscapeString(analytics.SourceLabel(src))+"</b> — "+
				html.EscapeString(analytics.SourceNote(src)))
		}
	}
	if len(caveats) == 0 && len(anomalies) == 0 {
		return
	}
	fmt.Fprint(w, `<div class="side-block"><h2>Качество данных</h2><div class="quality">`)
	for _, c := range caveats {
		fmt.Fprintf(w, `<p>%s</p>`, c)
	}
	if len(anomalies) > 0 {
		// Newest first: the latest periods matter most for the figures above.
		fmt.Fprint(w, `<ul>`)
		for i := len(anomalies) - 1; i >= 0 && len(anomalies)-i <= maxQualityItems; i-- {
			a := anomalies[i]
			fmt.Fprintf(w, `<li><span class="period">%s</span>%s</li>`,
				html.EscapeString(a.Label), html.EscapeString(a.Message))
		}
		fmt.Fprint(w, `</ul>`)
		if extra := len(anomalies) - maxQualityItems; extra > 0 {
			fmt.Fprintf(w, `<p>…и ещё %d.</p>`, extra)
		}
	}
	fmt.Fprint(w, `</div></div>`)
}

// renderCurrent shows valuation at the latest exchange close, each multiple
// annotated with the period its fundamental comes from.
func renderCurrent(w http.ResponseWriter, c analytics.Current) {
	fmt.Fprintf(w, `<div class="sub-title">Текущая оценка · закрытие %s</div>`, html.EscapeString(c.PriceDate))
	sub := func(prefix, label string) string {
		if label == "" {
			return ""
		}
		return prefix + " " + label
	}
	renderKPICards(w, []kpiCard{
		{name: "Капитализация (сейчас)", value: fmtMoney(c.Capitalization)},
		{name: "P/E (сейчас)", value: fmtRatio(c.PE), sub: sub("прибыль LTM на", c.EarningsLabel)},
		{name: "P/B (сейчас)", value: fmtRatio(c.PB), sub: sub("капитал на", c.EquityLabel)},
		{name: "Див. доходность (сейчас)", value: fmtPct(c.DivYield), sub: yieldSub("дивиденды за", c.DividendsLabel, c.DividendsMissing)},
		{name: "EV/EBIT (сейчас)", value: fmtRatio(c.EVEBIT), sub: balanceSub(sub("EBIT LTM на", c.EBITLabel), c.EVEBIT, c.BalanceNote)},
		{name: "P/FCF (сейчас)", value: fmtRatio(c.PFCF), sub: balanceSub(sub("FCF LTM на", c.FCFLabel), c.PFCF, c.BalanceNote)},
	})
}

// periodNote names the period a stock figure came from when it is not the
// company's latest period ("" otherwise, to keep the common case quiet).
func periodNote(s analytics.Snapshot, label string) string {
	if label == "" || label == s.LastLabel {
		return ""
	}
	return "на " + label
}

// yieldSub is the line under a dividend-yield figure. A yield with no
// dividends anywhere in the history says they were not entered.
func yieldSub(prefix, label string, missing bool) string {
	base := label
	if prefix != "" && label != "" {
		base = prefix + " " + label
	}
	if !missing {
		return base
	}
	return joinSub(base, "дивиденды не введены")
}

// joinSub joins non-empty KPI sub-lines.
// payoutCard is the year's dividends over that year's profit. Banks are left
// out: their Q4 profit is one quarter, so the ratio would not be a payout.
func payoutCard(s analytics.Snapshot) []kpiCard {
	if s.Bank {
		return nil
	}
	return []kpiCard{{name: "Коэффициент выплат", value: fmtPct(s.Payout), sub: s.PayoutLabel}}
}

// netDebtSub names the period, or the mixed-source sentence when the figure is blank for that reason.
func netDebtSub(s analytics.Snapshot) string {
	if math.IsNaN(s.NetDebt) && s.BalanceNote != "" {
		return s.BalanceNote
	}
	return periodNote(s, s.NetDebtLabel)
}

// scoreSub is the line under the headline score. A bank's number stays on
// the card and says it is not the industrial column.
func scoreSub(s analytics.Snapshot) string {
	sub := fmt.Sprintf("по %d из %d", s.ScoreParts, s.ScoreScale())
	if s.Bank {
		return sub + " · шкала банка, в скринере не сортируется"
	}
	return sub
}

// balanceSub adds the mixed-source sentence when the multiple itself is blank.
func balanceSub(base string, value float64, note string) string {
	if !math.IsNaN(value) || note == "" {
		return base
	}
	return joinSub(base, note)
}

func joinSub(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// renderKPIs lists every snapshot figure, grouped the way an analyst reads
// them: valuation, earnings and margins, debt, growth.
func renderKPIs(w http.ResponseWriter, s analytics.Snapshot) {
	groups := []struct {
		name  string
		cards []kpiCard
	}{
		{"Оценка по отчётности", append([]kpiCard{
			{name: "Капитализация", value: fmtMoney(s.Capitalization), sub: periodNote(s, s.CapLabel)},
			{name: "P/E", value: fmtRatio(s.PE), sub: joinSub(peComment(s.PE), periodNote(s, s.PELabel))},
			{name: "P/B", value: fmtRatio(s.PB), sub: s.PBLabel},
			{name: "Див. доходность", value: fmtPct(s.DivYield), sub: yieldSub("", s.DivYieldLabel, s.DividendsMissing)},
			{name: "EV/EBIT", value: fmtRatio(s.EVEBIT), sub: balanceSub(s.EVEBITLabel, s.EVEBIT, s.BalanceNote)},
			{name: "P/FCF", value: fmtRatio(s.PFCF), sub: balanceSub(s.PFCFLabel, s.PFCF, s.BalanceNote)},
		}, payoutCard(s)...)},
		{"Прибыль и рентабельность", []kpiCard{
			{name: "Выручка (LTM)", value: fmtMoney(s.Revenue), sub: fmtSignedPctSub("г/г", s.RevenueYoY)},
			{name: "Чистая прибыль (LTM)", value: fmtMoney(s.NetProfit), sub: fmtSignedPctSub("г/г", s.NetProfitYoY)},
			{name: "EBITDA (LTM)", value: fmtMoney(s.EBITDA)},
			{name: "FCF (LTM)", value: fmtMoney(s.FCF), sub: periodNote(s, s.FCFLabel)},
			{name: "Чистая маржа", value: fmtPct(s.NetMargin)},
			{name: "Маржа EBITDA", value: fmtPct(s.EBITDAMargin)},
			{name: "Операционная маржа", value: fmtPct(s.OperatingMargin)},
			{name: "ROE", value: fmtPct(s.ROE), sub: periodNote(s, s.ROELabel)},
		}},
		{"Долг", []kpiCard{
			{name: "Долг", value: fmtMoney(s.Debt), sub: periodNote(s, s.DebtLabel)},
			{name: "Чистый долг", value: fmtMoney(s.NetDebt), sub: netDebtSub(s)},
			{name: "Долг / EBITDA", value: fmtMultiple(s.DebtEBITDA), sub: leverageComment(s.DebtEBITDA)},
		}},
		{"Рост", []kpiCard{
			{name: "CAGR выручки (3 года)", value: fmtPct(s.RevenueCAGR3Y)},
			{name: "CAGR прибыли (3 года)", value: fmtPct(s.NetProfitCAGR3)},
			{name: "CAGR выручки (5 лет)", value: fmtPct(s.RevenueCAGR5Y)},
			{name: "CAGR прибыли (5 лет)", value: fmtPct(s.NetProfitCAGR5)},
			{name: "CAGR дивидендов (3 года)", value: fmtPct(s.DividendsCAGR3)},
			{name: "CAGR дивидендов (5 лет)", value: fmtPct(s.DividendsCAGR5)},
		}},
	}
	fmt.Fprint(w, `<div class="groups">`)
	for _, g := range groups {
		fmt.Fprintf(w, `<div class="card group"><h3>%s</h3><dl>`, html.EscapeString(g.name))
		for _, c := range g.cards {
			fmt.Fprintf(w, `<div class="row"><dt>%s</dt><dd>%s%s</dd></div>`,
				html.EscapeString(c.name), html.EscapeString(c.value), subHTML(c.sub, "sub"))
		}
		fmt.Fprint(w, `</dl></div>`)
	}
	fmt.Fprint(w, `</div>`)
}

// subHTML renders a KPI sub-line, coloured by the sign of a leading change.
func subHTML(sub, class string) string {
	if sub == "" {
		return ""
	}
	cls := ""
	switch {
	case strings.HasPrefix(sub, "+"):
		cls = " pos"
	case strings.HasPrefix(sub, "-") || strings.HasPrefix(sub, "−"):
		cls = " neg"
	}
	return fmt.Sprintf(`<span class="%s%s">%s</span>`, class, cls, html.EscapeString(sub))
}

func renderKPICards(w http.ResponseWriter, cards []kpiCard) {
	fmt.Fprint(w, `<div class="kpi-grid">`)
	for _, c := range cards {
		emptyCls := ""
		if c.value == "—" {
			emptyCls = " empty"
		}
		sub := ""
		if c.sub != "" {
			sub = strings.Replace(subHTML(c.sub, "sub"), "<span", "<div", 1)
			sub = strings.TrimSuffix(sub, "</span>") + "</div>"
		}
		fmt.Fprintf(w, `<div class="kpi%s"><div class="name">%s</div><div class="value">%s</div>%s</div>`,
			emptyCls, html.EscapeString(c.name), html.EscapeString(c.value), sub)
	}
	fmt.Fprint(w, `</div>`)
}

type kpiCard struct {
	name, value, sub string
}

func renderSparklines(w http.ResponseWriter, history []models.QuarterData) {
	specs := []struct {
		metric, name string
	}{
		{"revenue", "Выручка"},
		{"net_profit", "Чистая прибыль"},
		{"ebitda", "EBITDA"},
		{"debt", "Долг"},
	}

	fmt.Fprint(w, `<div class="sparkline-grid">`)
	for _, sp := range specs {
		series := analytics.TTMSeries(history, sp.metric)
		latest, latestOK := analytics.LatestValid(series)
		var deltaText, deltaClass string
		if latestOK {
			// Same quarter a calendar year earlier, found by date rather
			// than position so gaps and annual-only rows pair correctly.
			if prev, ok := analytics.YearAgo(series, latest, analytics.PeriodTTM, 1); ok && prev.Value != 0 {
				delta := (latest.Value/prev.Value - 1) * 100
				deltaClass = "pos"
				sign := "+"
				if delta < 0 {
					deltaClass = "neg"
					sign = ""
				}
				deltaText = fmt.Sprintf("%s%.1f%% г/г", sign, delta)
			}
		}
		valueStr := "—"
		if latestOK {
			valueStr = humanFormat(latest.Value)
		}
		fmt.Fprintf(w, `<div class="spark">
  <div class="name">%s (LTM)</div>
  <div class="value">%s</div>
  <div class="delta %s">%s</div>
  %s
</div>`, html.EscapeString(sp.name), html.EscapeString(valueStr), deltaClass, html.EscapeString(deltaText), buildSparklineSVG(series, sp.name))
	}
	fmt.Fprint(w, `</div>`)
}

// buildSparklineSVG draws a series as a small line, coloured by the
// direction from its first to its last valid point (via currentColor).
func buildSparklineSVG(s analytics.Series, name string) string {
	type xy struct{ x, y float64 }
	var points []xy
	for i, p := range s {
		if !math.IsNaN(p.Value) {
			points = append(points, xy{x: float64(i), y: p.Value})
		}
	}
	if len(points) < 2 {
		return `<svg viewBox="0 0 100 30" aria-hidden="true"></svg>`
	}

	minY, maxY := points[0].y, points[0].y
	minX, maxX := points[0].x, points[len(points)-1].x
	for _, p := range points {
		minY = math.Min(minY, p.y)
		maxY = math.Max(maxY, p.y)
	}
	if maxY == minY {
		maxY = minY + 1
	}
	if maxX == minX {
		maxX = minX + 1
	}

	const W, H = 240.0, 48.0
	pad := 2.0
	var b strings.Builder
	for i, p := range points {
		x := pad + (p.x-minX)/(maxX-minX)*(W-2*pad)
		y := H - pad - (p.y-minY)/(maxY-minY)*(H-2*pad)
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", x, y)
	}

	last, first := points[len(points)-1], points[0]
	cls := ""
	if last.y < first.y {
		cls = "neg"
	} else if last.y > first.y {
		cls = "pos"
	}
	areaPath := b.String() + fmt.Sprintf(" %.1f,%.1f %.1f,%.1f",
		pad+(last.x-minX)/(maxX-minX)*(W-2*pad), H-pad, pad, H-pad)

	return fmt.Sprintf(
		`<svg class="%s" viewBox="0 0 %.0f %.0f" preserveAspectRatio="none" role="img" aria-label="%s: динамика LTM">
  <polyline points="%s" fill="currentColor" fill-opacity="0.12" stroke="none"/>
  <polyline points="%s" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" vector-effect="non-scaling-stroke"/>
</svg>`,
		cls, W, H, html.EscapeString(name), areaPath, b.String())
}

// dashboardChartMetrics are the metrics the card's chart can switch between.
var dashboardChartMetrics = []metricItem{
	{"revenue", "Выручка"}, {"net_profit", "Чистая прибыль"}, {"ebitda", "EBITDA"},
	{"capitalization", "Капитализация"}, {"debt", "Долг"}, {"net_debt", "Чистый долг"},
	{"pe", "P/E"}, {"pb", "P/B"}, {"div_yield", "Див. доходность"}, {"payout", "Выплаты"}, {"roe", "ROE"},
	{"ev_ebit", "EV/EBIT"}, {"p_fcf", "P/FCF"}, {"fcf", "FCF"},
	{"net_margin", "Чистая маржа"}, {"ebitda_margin", "Маржа EBITDA"}, {"operating_margin", "Опер. маржа"},
	{"debt_ebitda", "Долг/EBITDA"}, {"revenue_yoy", "Выручка г/г"}, {"net_profit_yoy", "Прибыль г/г"},
	{"dividends_cagr3", "Дивиденды 3г"}, {"dividends_cagr5", "Дивиденды 5л"},
}

// renderDashboardChart renders the chart frame and its controls; the script
// at the end of the page points the frame at /chart/{metric}.
func renderDashboardChart(w http.ResponseWriter, company string) {
	fmt.Fprint(w, `<div class="chart-controls"><div class="metric-chips" role="group" aria-label="Показатель">`)
	for i, m := range dashboardChartMetrics {
		fmt.Fprintf(w, `<button type="button" class="chip-btn" data-metric="%s" aria-pressed="%t">%s</button>`,
			m.ID, i == 0, html.EscapeString(m.Label))
	}
	fmt.Fprint(w, `</div>
<div class="segmented" role="group" aria-label="Период">
  <button type="button" data-period="quarter" aria-pressed="false">Квартал</button>
  <button type="button" data-period="ttm" aria-pressed="true">LTM</button>
  <button type="button" data-period="annual" aria-pressed="false">Год</button>
</div></div>`)
	fmt.Fprintf(w, `<div class="card chart-card"><iframe id="dash-chart" title="График показателя %s"></iframe></div>`,
		html.EscapeString(company))
}

func renderScoreBreakdown(w http.ResponseWriter, s analytics.Snapshot) {
	type scoreRow struct {
		name   string
		value  string
		weight string
		ok     bool
		good   bool
	}
	var rows []scoreRow
	note := "Эвристический балл, а не рекомендация. Компонент без данных не входит в итог, и оставшиеся пересчитываются на 100 — поэтому балл по двум компонентам нельзя сравнивать с баллом по всем."
	if s.Bank {
		note = "Шкала для банков: рост дохода (чистый процентный плюс комиссии), ROE и P/B. " + note
		rows = []scoreRow{
			{"Рост дохода (CAGR, 3 года)", fmtPct(s.RevenueCAGR3Y), "35%", !math.IsNaN(s.RevenueCAGR3Y), s.RevenueCAGR3Y >= 10},
			{"Рентабельность (ROE)", fmtPct(s.ROE), "40%", !math.IsNaN(s.ROE), s.ROE >= 15},
			{"Оценка (P/B)", fmtRatio(s.PB), "25%", !math.IsNaN(s.PB) && s.PB > 0, s.PB > 0 && s.PB <= 1},
		}
	} else {
		rows = []scoreRow{
			{"Рост (CAGR выручки, 3 года)", fmtPct(s.RevenueCAGR3Y), "30%", !math.IsNaN(s.RevenueCAGR3Y), s.RevenueCAGR3Y >= 10},
			{"Рентабельность (ROE)", fmtPct(s.ROE), "25%", !math.IsNaN(s.ROE), s.ROE >= 15},
			{"Маржинальность (чистая маржа)", fmtPct(s.NetMargin), "15%", !math.IsNaN(s.NetMargin), s.NetMargin >= 10},
			{"Долговая нагрузка (Долг/EBITDA)", fmtMultiple(s.DebtEBITDA), "15%", !math.IsNaN(s.DebtEBITDA), s.DebtEBITDA <= 2},
			{"Оценка (P/E)", fmtRatio(s.PE), "15%", !math.IsNaN(s.PE) && s.PE > 0, s.PE > 0 && s.PE <= 12},
		}
	}
	fmt.Fprintf(w, `<div class="card score-breakdown">
  <div class="score-head"><span class="big num">%d</span><span class="muted">из 100 · по %d из %d</span></div>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Компонент</th><th class="left">Вес</th><th>Значение</th><th class="left">Вывод</th></tr></thead>
    <tbody>`, s.Score, s.ScoreParts, s.ScoreScale())
	for _, r := range rows {
		var verdict string
		switch {
		case !r.ok:
			verdict = `<span class="none">нет данных</span>`
		case r.good:
			verdict = `<span class="good">хорошо</span>`
		default:
			verdict = `<span class="weak">слабо</span>`
		}
		fmt.Fprintf(w, `<tr><td>%s</td><td class="left">%s</td><td class="num">%s</td><td class="left">%s</td></tr>`,
			html.EscapeString(r.name), r.weight, html.EscapeString(r.value), verdict)
	}
	fmt.Fprintf(w, `</tbody></table></div>
  <div class="score-note">%s</div>
</div>`, html.EscapeString(note))
}

func renderNotes(w http.ResponseWriter, note string) {
	fmt.Fprintf(w, `<div class="side-block">
  <h2><label for="noteText">Заметки</label></h2>
  <textarea class="textarea" id="noteText" placeholder="Инвестиционная идея, напоминания, результаты анализа…">%s</textarea>
  <div class="note-actions">
    <span class="status" id="noteStatus" role="status"></span>
    <button type="button" class="btn btn-sm" id="noteSave">Сохранить</button>
  </div>
</div>`, html.EscapeString(note))
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func fmtMoney(v float64) string {
	if math.IsNaN(v) {
		return "—"
	}
	return humanFormat(v)
}

func fmtPct(v float64) string {
	if math.IsNaN(v) {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", v)
}

func fmtRatio(v float64) string {
	if math.IsNaN(v) {
		return "—"
	}
	return fmt.Sprintf("%.2f", v)
}

func fmtMultiple(v float64) string {
	if math.IsNaN(v) {
		return "—"
	}
	return fmt.Sprintf("%.2fx", v)
}

func fmtSignedPctSub(label string, v float64) string {
	if math.IsNaN(v) {
		return ""
	}
	sign := "+"
	if v < 0 {
		sign = ""
	}
	return fmt.Sprintf("%s%.1f%% %s", sign, v, label)
}

func peComment(v float64) string {
	if math.IsNaN(v) {
		return ""
	}
	switch {
	case v <= 0:
		return "убыток"
	case v <= 8:
		return "дёшево"
	case v <= 15:
		return "справедливо"
	case v <= 25:
		return "дорого"
	}
	return "очень дорого"
}

func leverageComment(v float64) string {
	if math.IsNaN(v) {
		return ""
	}
	switch {
	case v < 0:
		return "чистая денежная позиция"
	case v <= 1:
		return "низкая нагрузка"
	case v <= 3:
		return "умеренная"
	case v <= 5:
		return "высокая"
	}
	return "очень высокая"
}
