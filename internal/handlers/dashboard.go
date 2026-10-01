package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/VxVxN/financialanalyzer/internal/analytics"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

// DashboardHandler renders /company/{name}: a single-company long-term-investor
// dashboard. Everything is server-rendered so the page works without a build
// step and stays consistent with the rest of the app.
func (controller *Controller) DashboardHandler(w http.ResponseWriter, r *http.Request) {
	company := strings.TrimSpace(chi.URLParam(r, "name"))
	if company == "" {
		http.Error(w, "company is required", http.StatusBadRequest)
		return
	}
	// The theme is echoed into links and attributes: only the two known
	// values may pass, never raw query text.
	theme := "light"
	if r.URL.Query().Get("theme") == "dark" {
		theme = "dark"
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

	pal := paletteFor(theme)

	w.Header().Set("Content-Type", "text/html")

	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="ru"%s>
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s — дашборд</title>
<style>
:root {
  --bg-primary: %s;
  --bg-secondary: %s;
  --bg-card: %s;
  --bg-hover: %s;
  --text-primary: %s;
  --text-secondary: %s;
  --text-muted: %s;
  --border: %s;
  --accent: #5470c6;
  --pos: #3ba272;
  --neg: #ee6666;
  --warn: #fac858;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  background: var(--bg-primary);
  color: var(--text-primary);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Arial, sans-serif;
}
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }

.container { max-width: 1400px; margin: 0 auto; padding: 24px; }

.toolbar {
  display: flex; justify-content: space-between; align-items: center;
  margin-bottom: 20px; flex-wrap: wrap; gap: 12px;
}
.toolbar .left a { font-size: 14px; color: var(--text-secondary); }
.theme-toggle {
  padding: 8px 16px; background: var(--bg-card); border: 1px solid var(--border);
  color: var(--text-primary); border-radius: 6px; cursor: pointer; font-size: 13px;
}
.theme-toggle:hover { background: var(--bg-hover); }

.header {
  background: var(--bg-secondary);
  border-radius: 12px;
  padding: 24px;
  display: grid; grid-template-columns: 1fr auto; gap: 24px; align-items: center;
  margin-bottom: 20px;
}
.header h1 { margin: 0; font-size: 28px; }
.header .meta {
  color: var(--text-secondary); font-size: 14px; margin-top: 4px;
}
.header .meta .pill {
  display: inline-block; padding: 2px 10px; background: var(--bg-card);
  border-radius: 12px; margin-right: 8px;
}
.score-box {
  text-align: center; min-width: 160px;
  padding: 16px 20px;
  background: var(--bg-card); border-radius: 12px;
  border: 1px solid var(--border);
}
.score-box .value { font-size: 40px; font-weight: 700; line-height: 1; }
.score-box .label { font-size: 12px; color: var(--text-muted); margin-top: 4px; text-transform: uppercase; letter-spacing: 0.5px; }
.score-box .stars { font-size: 18px; margin-top: 6px; color: var(--warn); }

.kpi-grid {
  display: grid; gap: 14px;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  margin-bottom: 24px;
}
.kpi {
  background: var(--bg-secondary); border-radius: 10px; padding: 16px;
  border: 1px solid var(--border);
}
.kpi .name {
  font-size: 12px; color: var(--text-muted); text-transform: uppercase;
  letter-spacing: 0.5px; margin-bottom: 6px;
}
.kpi .value { font-size: 22px; font-weight: 600; }
.kpi .sub { font-size: 12px; color: var(--text-secondary); margin-top: 4px; }
.kpi .sub.pos { color: var(--pos); }
.kpi .sub.neg { color: var(--neg); }
.kpi.empty .value { color: var(--text-muted); font-weight: 400; font-size: 16px; }

.sparkline-grid {
  display: grid; gap: 14px;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  margin-bottom: 24px;
}
.spark {
  background: var(--bg-secondary); border-radius: 10px; padding: 16px;
  border: 1px solid var(--border);
}
.spark .name { font-size: 13px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; }
.spark .value { font-size: 20px; font-weight: 600; margin-top: 4px; }
.spark .delta { font-size: 12px; }
.spark .delta.pos { color: var(--pos); }
.spark .delta.neg { color: var(--neg); }
.spark svg { display: block; width: 100%%; height: 60px; margin-top: 8px; }

.section-title {
  font-size: 18px; margin: 32px 0 12px; color: var(--text-primary);
}
.stale-note { color: var(--text-secondary); font-size: 14px; margin: 0 0 20px; }

.quality {
  background: var(--bg-secondary); border: 1px solid var(--warn); border-left-width: 4px;
  border-radius: 10px; padding: 14px 18px; margin-bottom: 20px; font-size: 14px;
}
.quality .title { font-weight: 600; margin-bottom: 6px; }
.quality p { margin: 4px 0; color: var(--text-secondary); }
.quality ul { margin: 6px 0 0; padding-left: 20px; }
.quality li { margin: 2px 0; }
.quality .period { font-variant-numeric: tabular-nums; color: var(--text-muted); margin-right: 6px; }

.controls {
  display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 12px;
}
.btn {
  padding: 8px 14px; background: var(--bg-card); border: 1px solid var(--border);
  border-radius: 6px; cursor: pointer; color: var(--text-primary); font-size: 13px;
}
.btn:hover { background: var(--bg-hover); }
.btn.active { background: var(--accent); color: white; border-color: var(--accent); }

.metric-row { margin-bottom: 8px; }
.metric-row .label {
  font-size: 12px; color: var(--text-muted); margin-right: 8px;
  text-transform: uppercase; letter-spacing: 0.5px;
}

.chart-card {
  background: var(--bg-secondary); border-radius: 12px; padding: 16px;
  border: 1px solid var(--border);
}

.note-card {
  background: var(--bg-secondary); border-radius: 12px; padding: 20px;
  border: 1px solid var(--border); margin-top: 24px;
}
.note-card textarea {
  width: 100%%; min-height: 120px; padding: 12px;
  background: var(--bg-card); color: var(--text-primary);
  border: 1px solid var(--border); border-radius: 8px;
  font-family: inherit; font-size: 14px; resize: vertical;
}
.note-card .note-actions {
  display: flex; justify-content: space-between; align-items: center; margin-top: 10px;
}
.note-card .save-btn {
  background: var(--accent); color: white; border: none;
  padding: 8px 18px; border-radius: 6px; cursor: pointer; font-size: 13px;
}
.note-card .save-btn:hover { background: #4060b0; }
.note-card .status { font-size: 12px; color: var(--text-muted); }

.empty-state {
  text-align: center; padding: 60px 20px; color: var(--text-muted);
}

.score-breakdown {
  margin-top: 14px; font-size: 13px; color: var(--text-secondary);
  background: var(--bg-secondary); border: 1px solid var(--border);
  border-radius: 10px; padding: 14px;
}
.score-breakdown table { width: 100%%; border-collapse: collapse; }
.score-breakdown th, .score-breakdown td { padding: 6px 8px; text-align: left; border-bottom: 1px solid var(--border); }
.score-breakdown th { color: var(--text-muted); font-weight: 500; font-size: 12px; text-transform: uppercase; letter-spacing: 0.5px; }
.score-breakdown td.num { text-align: right; font-variant-numeric: tabular-nums; }
.rel-val { margin: 0 0 20px; }
.rel-val .table-scroll { overflow-x: auto; }
.rel-val th.num { text-align: right; }
.rel-val td { white-space: nowrap; }
.rel-val .pos { color: var(--pos); }
.rel-val .neg { color: var(--neg); }
.rel-val .muted { color: var(--text-muted); }
.rel-val-note { font-size: 11px; color: var(--text-muted); margin-top: 8px; }
</style>
</head>
<body>
<div class="container">
  <div class="toolbar">
    <div class="left">
      <a href="/?theme=%s">← К сравнению</a>
    </div>
    <button class="theme-toggle" onclick="toggleTheme()">%s</button>
  </div>
`,
		themeAttr(theme),
		html.EscapeString(company),
		pal.bgPrimary, pal.bgSecondary, pal.bgCard, pal.bgHover,
		pal.textPrimary, pal.textSecondary, pal.textMuted, pal.border,
		theme,
		themeToggleLabel(theme),
	)

	if len(history) == 0 {
		fmt.Fprintf(w, `<div class="empty-state">Нет данных по компании <b>%s</b>.</div></div></body></html>`,
			html.EscapeString(company))
		return
	}

	sources := analytics.Sources(history)
	renderDashboardHeader(w, snap, sources)
	renderDataQuality(w, sources, analytics.CheckHistory(history))
	switch {
	case hasQuote && analytics.QuoteIsFresh(quote, controller.now()):
		renderCurrent(w, analytics.BuildCurrent(history, quote))
	case hasQuote && quote.Capitalization > 0:
		fmt.Fprintf(w, `<div class="section-title">Текущая оценка</div><p class="stale-note">Последняя сохранённая цена закрытия от %s — слишком старая для оценки (обновите: <code>FETCH_QUOTES_ONLY=1 go run ./cmd/fetch</code>).</p>`,
			html.EscapeString(quote.PriceDate.Format("2006-01-02")))
	}
	renderRelativeValuation(w, ownRow, peersKnown, controller.now())
	renderKPIs(w, snap)
	renderSparklines(w, history)
	renderDashboardChart(w, history, theme)
	renderScoreBreakdown(w, snap)
	renderManual(w, company, manual, controller.now().Year()-1)
	renderNotes(w, company, note)

	fmt.Fprintf(w, `</div>
<script>
function toggleTheme() {
  const url = new URL(window.location.href);
  const next = url.searchParams.get('theme') === 'dark' ? 'light' : 'dark';
  url.searchParams.set('theme', next);
  window.location.href = url.toString();
}

let currentMetric = 'revenue';
let currentPeriod = 'ttm';
function selectMetric(metric, btn) {
  currentMetric = metric;
  document.querySelectorAll('.metric-btn').forEach(b => b.classList.remove('active'));
  btn.classList.add('active');
  reloadChart();
}
function selectPeriod(period, btn) {
  currentPeriod = period;
  document.querySelectorAll('.period-btn').forEach(b => b.classList.remove('active'));
  btn.classList.add('active');
  reloadChart();
}
function reloadChart() {
  const ifr = document.getElementById('dash-chart');
  const url = new URL(ifr.src, window.location.href);
  const params = new URLSearchParams(url.search);
  params.set('period', currentPeriod);
  ifr.src = '/chart/' + currentMetric + '?' + params.toString() + '&t=' + Date.now();
}

async function saveNote() {
  const company = %s;
  const note = document.getElementById('noteText').value;
  const status = document.getElementById('noteStatus');
  status.textContent = 'Сохранение…';
  try {
    const resp = await fetch('/api/company-note', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ company, note })
    });
    if (!resp.ok) throw new Error('не удалось сохранить заметку');
    status.textContent = 'Сохранено';
    setTimeout(() => status.textContent = '', 2000);
  } catch (err) {
    status.textContent = 'Ошибка: ' + err.message;
  }
}
</script>
</body></html>`, jsonForScript(company))
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
	own := analytics.BuildScreenerRow(history, q)
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

type palette struct {
	bgPrimary, bgSecondary, bgCard, bgHover string
	textPrimary, textSecondary, textMuted   string
	border                                  string
}

func paletteFor(theme string) palette {
	if theme == "dark" {
		return palette{
			bgPrimary: "#1a1a1a", bgSecondary: "#242424", bgCard: "#2d2d2d", bgHover: "#3a3a3a",
			textPrimary: "#ffffff", textSecondary: "#cfcfcf", textMuted: "#888888",
			border: "#3a3a3a",
		}
	}
	return palette{
		bgPrimary: "#fafafa", bgSecondary: "#ffffff", bgCard: "#f0f2f5", bgHover: "#e6e9ee",
		textPrimary: "#1a1a1a", textSecondary: "#444444", textMuted: "#888888",
		border: "#e0e0e0",
	}
}

func themeAttr(theme string) string {
	if theme == "dark" {
		return ` data-theme="dark"`
	}
	return ""
}
func themeToggleLabel(theme string) string {
	if theme == "dark" {
		return "☀ Светлая"
	}
	return "🌙 Тёмная"
}

func renderDashboardHeader(w http.ResponseWriter, s analytics.Snapshot, sources []string) {
	stars := scoreStars(s.Score)
	srcLabel, srcNotes, _ := sourcesLabel(sources)
	fmt.Fprintf(w, `<div class="header">
  <div>
    <h1>%s</h1>
    <div class="meta">
      <span class="pill">%s</span>
      <span class="pill" title="%s">Источник: %s</span>
      <span>Последний период: %s</span>
    </div>
  </div>
  <div class="score-box">
    <div class="value">%d</div>
    <div class="stars">%s</div>
    <div class="label">Итоговый балл</div>
  </div>
</div>`,
		html.EscapeString(s.Company),
		html.EscapeString(orDash(s.Category)),
		html.EscapeString(srcNotes), html.EscapeString(srcLabel),
		html.EscapeString(orDash(s.LastLabel)),
		s.Score, stars,
	)
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
	fmt.Fprint(w, `<div class="quality"><div class="title">⚠ Качество данных</div>`)
	for _, c := range caveats {
		fmt.Fprintf(w, `<p>%s</p>`, c)
	}
	if len(anomalies) > 0 {
		// Newest first: the latest periods matter most for the KPIs above.
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
	fmt.Fprint(w, `</div>`)
}

func scoreStars(score int) string {
	full := score / 20
	empty := 5 - full
	return strings.Repeat("★", full) + strings.Repeat("☆", empty)
}

// renderCurrent shows valuation at the latest exchange close, each multiple
// annotated with the period its fundamental comes from.
func renderCurrent(w http.ResponseWriter, c analytics.Current) {
	fmt.Fprintf(w, `<div class="section-title">Текущая оценка · закрытие %s</div>`, html.EscapeString(c.PriceDate))
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
		{name: "Див. доходность (сейчас)", value: fmtPct(c.DivYield), sub: sub("дивиденды за", c.DividendsLabel)},
		{name: "EV/EBIT (сейчас)", value: fmtRatio(c.EVEBIT), sub: sub("EBIT LTM на", c.EBITLabel)},
		{name: "P/FCF (сейчас)", value: fmtRatio(c.PFCF), sub: sub("FCF LTM на", c.FCFLabel)},
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

// joinSub joins non-empty KPI sub-lines.
func joinSub(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

func renderKPIs(w http.ResponseWriter, s analytics.Snapshot) {
	cards := []kpiCard{
		{name: "Капитализация", value: fmtMoney(s.Capitalization), sub: periodNote(s, s.CapLabel)},
		{name: "Выручка (LTM)", value: fmtMoney(s.Revenue), sub: fmtSignedPctSub("г/г", s.RevenueYoY)},
		{name: "Чистая прибыль (LTM)", value: fmtMoney(s.NetProfit), sub: fmtSignedPctSub("г/г", s.NetProfitYoY)},
		{name: "EBITDA (LTM)", value: fmtMoney(s.EBITDA)},
		{name: "Чистая маржа", value: fmtPct(s.NetMargin)},
		{name: "Маржа EBITDA", value: fmtPct(s.EBITDAMargin)},
		{name: "Операционная маржа", value: fmtPct(s.OperatingMargin)},
		{name: "FCF (LTM)", value: fmtMoney(s.FCF), sub: periodNote(s, s.FCFLabel)},
		{name: "ROE", value: fmtPct(s.ROE), sub: periodNote(s, s.ROELabel)},
		{name: "P/E", value: fmtRatio(s.PE), sub: joinSub(peComment(s.PE), periodNote(s, s.PELabel))},
		{name: "P/B", value: fmtRatio(s.PB), sub: s.PBLabel},
		{name: "Див. доходность", value: fmtPct(s.DivYield), sub: s.DivYieldLabel},
		{name: "EV/EBIT", value: fmtRatio(s.EVEBIT), sub: s.EVEBITLabel},
		{name: "P/FCF", value: fmtRatio(s.PFCF), sub: s.PFCFLabel},
		{name: "Долг", value: fmtMoney(s.Debt), sub: periodNote(s, s.DebtLabel)},
		{name: "Чистый долг", value: fmtMoney(s.NetDebt), sub: periodNote(s, s.NetDebtLabel)},
		{name: "Долг / EBITDA", value: fmtMultiple(s.DebtEBITDA), sub: leverageComment(s.DebtEBITDA)},
		{name: "CAGR выручки (3 года)", value: fmtPct(s.RevenueCAGR3Y)},
		{name: "CAGR прибыли (3 года)", value: fmtPct(s.NetProfitCAGR3)},
		{name: "CAGR выручки (5 лет)", value: fmtPct(s.RevenueCAGR5Y)},
		{name: "CAGR прибыли (5 лет)", value: fmtPct(s.NetProfitCAGR5)},
	}
	renderKPICards(w, cards)
}

func renderKPICards(w http.ResponseWriter, cards []kpiCard) {
	fmt.Fprintf(w, `<div class="kpi-grid">`)
	for _, c := range cards {
		emptyCls := ""
		if c.value == "—" {
			emptyCls = " empty"
		}
		subHTML := ""
		if c.sub != "" {
			cls := ""
			switch {
			case strings.HasPrefix(c.sub, "+"):
				cls = " pos"
			case strings.HasPrefix(c.sub, "-") || strings.HasPrefix(c.sub, "−"):
				cls = " neg"
			}
			subHTML = fmt.Sprintf(`<div class="sub%s">%s</div>`, cls, html.EscapeString(c.sub))
		}
		fmt.Fprintf(w, `<div class="kpi%s">
  <div class="name">%s</div>
  <div class="value">%s</div>
  %s
</div>`, emptyCls, html.EscapeString(c.name), html.EscapeString(c.value), subHTML)
	}
	fmt.Fprintf(w, `</div>`)
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

	fmt.Fprintf(w, `<div class="sparkline-grid">`)
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
		svg := buildSparklineSVG(series)
		fmt.Fprintf(w, `<div class="spark">
  <div class="name">%s (LTM)</div>
  <div class="value">%s</div>
  <div class="delta %s">%s</div>
  %s
</div>`, html.EscapeString(sp.name), html.EscapeString(valueStr), deltaClass, html.EscapeString(deltaText), svg)
	}
	fmt.Fprintf(w, `</div>`)
}

func buildSparklineSVG(s analytics.Series) string {
	// Filter to valid points
	type xy struct{ x, y float64 }
	var points []xy
	for i, p := range s {
		if !math.IsNaN(p.Value) {
			points = append(points, xy{x: float64(i), y: p.Value})
		}
	}
	if len(points) < 2 {
		return `<svg viewBox="0 0 100 30"></svg>`
	}

	minY, maxY := points[0].y, points[0].y
	minX, maxX := points[0].x, points[len(points)-1].x
	for _, p := range points {
		if p.y < minY {
			minY = p.y
		}
		if p.y > maxY {
			maxY = p.y
		}
	}
	if maxY == minY {
		maxY = minY + 1
	}
	if maxX == minX {
		maxX = minX + 1
	}

	const W, H = 240.0, 56.0
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

	last := points[len(points)-1]
	first := points[0]
	color := "#5470c6"
	if last.y < first.y {
		color = "#ee6666"
	} else if last.y > first.y {
		color = "#3ba272"
	}

	// area under curve
	areaPath := b.String() + fmt.Sprintf(" %.1f,%.1f %.1f,%.1f",
		pad+(last.x-minX)/(maxX-minX)*(W-2*pad), H-pad,
		pad, H-pad)

	return fmt.Sprintf(
		`<svg viewBox="0 0 %.0f %.0f" preserveAspectRatio="none">
  <polyline points="%s" fill="%s33" stroke="none"/>
  <polyline points="%s" fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round"/>
</svg>`,
		W, H, areaPath, color, b.String(), color)
}

func renderDashboardChart(w http.ResponseWriter, history []models.QuarterData, theme string) {
	company := ""
	if len(history) > 0 {
		company = history[0].Company
	}

	fmt.Fprintf(w, `<div class="section-title">Динамика показателей</div>
<div class="metric-row">
  <span class="label">Показатель</span>
  <button class="btn metric-btn active" onclick="selectMetric('revenue', this)">Выручка</button>
  <button class="btn metric-btn" onclick="selectMetric('net_profit', this)">Чистая прибыль</button>
  <button class="btn metric-btn" onclick="selectMetric('ebitda', this)">EBITDA</button>
  <button class="btn metric-btn" onclick="selectMetric('capitalization', this)">Капитализация</button>
  <button class="btn metric-btn" onclick="selectMetric('debt', this)">Долг</button>
  <button class="btn metric-btn" onclick="selectMetric('pe', this)">P/E</button>
  <button class="btn metric-btn" onclick="selectMetric('roe', this)">ROE</button>
  <button class="btn metric-btn" onclick="selectMetric('pb', this)">P/B</button>
  <button class="btn metric-btn" onclick="selectMetric('div_yield', this)">Див. доходность</button>
  <button class="btn metric-btn" onclick="selectMetric('ev_ebit', this)">EV/EBIT</button>
  <button class="btn metric-btn" onclick="selectMetric('p_fcf', this)">P/FCF</button>
  <button class="btn metric-btn" onclick="selectMetric('fcf', this)">FCF</button>
  <button class="btn metric-btn" onclick="selectMetric('net_debt', this)">Чистый долг</button>
  <button class="btn metric-btn" onclick="selectMetric('net_margin', this)">Чистая маржа</button>
  <button class="btn metric-btn" onclick="selectMetric('ebitda_margin', this)">Маржа EBITDA</button>
  <button class="btn metric-btn" onclick="selectMetric('operating_margin', this)">Опер. маржа</button>
  <button class="btn metric-btn" onclick="selectMetric('debt_ebitda', this)">Долг/EBITDA</button>
  <button class="btn metric-btn" onclick="selectMetric('revenue_yoy', this)">Выручка г/г</button>
  <button class="btn metric-btn" onclick="selectMetric('net_profit_yoy', this)">Прибыль г/г</button>
</div>
<div class="metric-row">
  <span class="label">Период</span>
  <button class="btn period-btn" onclick="selectPeriod('quarter', this)">Квартал</button>
  <button class="btn period-btn active" onclick="selectPeriod('ttm', this)">LTM</button>
  <button class="btn period-btn" onclick="selectPeriod('annual', this)">Год</button>
</div>
<div class="chart-card">
  <iframe id="dash-chart" src="/chart/revenue?period=ttm&theme=%s&companies=%s"
    style="width:100%%; height:780px; border:0; background:transparent;"></iframe>
</div>`, theme, urlEscapeCSV(company))
}

func renderScoreBreakdown(w http.ResponseWriter, s analytics.Snapshot) {
	rows := []struct {
		name   string
		value  string
		weight string
		ok     bool
		good   bool
	}{
		{"Рост (CAGR выручки, 3 года)", fmtPct(s.RevenueCAGR3Y), "30%", !math.IsNaN(s.RevenueCAGR3Y), s.RevenueCAGR3Y >= 10},
		{"Рентабельность (ROE)", fmtPct(s.ROE), "25%", !math.IsNaN(s.ROE), s.ROE >= 15},
		{"Маржинальность (чистая маржа)", fmtPct(s.NetMargin), "15%", !math.IsNaN(s.NetMargin), s.NetMargin >= 10},
		{"Долговая нагрузка (Долг/EBITDA)", fmtMultiple(s.DebtEBITDA), "15%", !math.IsNaN(s.DebtEBITDA), s.DebtEBITDA <= 2},
		{"Оценка (P/E)", fmtRatio(s.PE), "15%", !math.IsNaN(s.PE) && s.PE > 0, s.PE > 0 && s.PE <= 12},
	}
	fmt.Fprintf(w, `<div class="score-breakdown">
  <table>
    <thead><tr><th>Компонент</th><th>Вес</th><th class="num">Значение</th><th>Вывод</th></tr></thead>
    <tbody>`)
	for _, r := range rows {
		var verdict string
		switch {
		case !r.ok:
			verdict = `<span style="color:var(--text-muted)">нет данных</span>`
		case r.good:
			verdict = `<span style="color:var(--pos)">хорошо</span>`
		default:
			verdict = `<span style="color:var(--warn)">слабо</span>`
		}
		fmt.Fprintf(w, `<tr><td>%s</td><td>%s</td><td class="num">%s</td><td>%s</td></tr>`,
			html.EscapeString(r.name), r.weight, html.EscapeString(r.value), verdict)
	}
	fmt.Fprintf(w, `</tbody></table>
  <div style="font-size:11px; color:var(--text-muted); margin-top:8px;">
    Эвристический балл, а не рекомендация. Изучите каждый компонент и составьте собственное мнение.
  </div>
</div>`)
}

func renderNotes(w http.ResponseWriter, company, note string) {
	fmt.Fprintf(w, `<div class="note-card">
  <div class="section-title" style="margin-top:0">Ваши заметки</div>
  <textarea id="noteText" placeholder="Инвестиционная идея, напоминания, результаты анализа…">%s</textarea>
  <div class="note-actions">
    <span class="status" id="noteStatus"></span>
    <button class="save-btn" onclick="saveNote()">Сохранить</button>
  </div>
</div>`, html.EscapeString(note))
}

// urlEscapeCSV escapes a company name for the chart's ?companies= list: a
// query-escaped value cannot break out of the attribute it is written into.
func urlEscapeCSV(s string) string {
	return url.QueryEscape(s)
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
