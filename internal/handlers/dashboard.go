package handlers

import (
	"fmt"
	"html"
	"math"
	"net/http"
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
	theme := r.URL.Query().Get("theme")
	if theme == "" {
		theme = "light"
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

	pal := paletteFor(theme)

	w.Header().Set("Content-Type", "text/html")

	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en"%s>
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s — Dashboard</title>
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
</style>
</head>
<body>
<div class="container">
  <div class="toolbar">
    <div class="left">
      <a href="/?theme=%s">← Back to comparison</a>
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
		fmt.Fprintf(w, `<div class="empty-state">No data for company <b>%s</b>.</div></div></body></html>`,
			html.EscapeString(company))
		return
	}

	sources := analytics.Sources(history)
	renderDashboardHeader(w, snap, sources)
	renderDataQuality(w, sources, analytics.CheckHistory(history))
	renderKPIs(w, snap)
	renderSparklines(w, history)
	renderDashboardChart(w, history, theme)
	renderScoreBreakdown(w, snap)
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
  const company = %q;
  const note = document.getElementById('noteText').value;
  const status = document.getElementById('noteStatus');
  status.textContent = 'Saving…';
  try {
    const resp = await fetch('/api/company-note', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ company, note })
    });
    if (!resp.ok) throw new Error('Failed to save note');
    status.textContent = 'Saved';
    setTimeout(() => status.textContent = '', 2000);
  } catch (err) {
    status.textContent = 'Error: ' + err.message;
  }
}
</script>
</body></html>`, company)
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
		return "☀ Light"
	}
	return "🌙 Dark"
}

func renderDashboardHeader(w http.ResponseWriter, s analytics.Snapshot, sources []string) {
	stars := scoreStars(s.Score)
	srcLabel, srcNotes, _ := sourcesLabel(sources)
	fmt.Fprintf(w, `<div class="header">
  <div>
    <h1>%s</h1>
    <div class="meta">
      <span class="pill">%s</span>
      <span class="pill" title="%s">Source: %s</span>
      <span>Latest: %s</span>
    </div>
  </div>
  <div class="score-box">
    <div class="value">%d</div>
    <div class="stars">%s</div>
    <div class="label">Quality score</div>
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
	fmt.Fprint(w, `<div class="quality"><div class="title">⚠ Data quality</div>`)
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
			fmt.Fprintf(w, `<p>…and %d more.</p>`, extra)
		}
	}
	fmt.Fprint(w, `</div>`)
}

func scoreStars(score int) string {
	full := score / 20
	empty := 5 - full
	return strings.Repeat("★", full) + strings.Repeat("☆", empty)
}

func renderKPIs(w http.ResponseWriter, s analytics.Snapshot) {
	cards := []kpiCard{
		{name: "Market Cap", value: fmtMoney(s.Capitalization)},
		{name: "Revenue (TTM)", value: fmtMoney(s.Revenue), sub: fmtSignedPctSub("YoY", s.RevenueYoY)},
		{name: "Net Profit (TTM)", value: fmtMoney(s.NetProfit), sub: fmtSignedPctSub("YoY", s.NetProfitYoY)},
		{name: "EBITDA (TTM)", value: fmtMoney(s.EBITDA)},
		{name: "Net Margin", value: fmtPct(s.NetMargin)},
		{name: "EBITDA Margin", value: fmtPct(s.EBITDAMargin)},
		{name: "ROE", value: fmtPct(s.ROE)},
		{name: "P/E", value: fmtRatio(s.PE), sub: peComment(s.PE)},
		{name: "Debt", value: fmtMoney(s.Debt)},
		{name: "Debt / EBITDA", value: fmtMultiple(s.DebtEBITDA), sub: leverageComment(s.DebtEBITDA)},
		{name: "Revenue CAGR (3Y)", value: fmtPct(s.RevenueCAGR3Y)},
		{name: "Net Profit CAGR (3Y)", value: fmtPct(s.NetProfitCAGR3)},
		{name: "Revenue CAGR (5Y)", value: fmtPct(s.RevenueCAGR5Y)},
		{name: "Net Profit CAGR (5Y)", value: fmtPct(s.NetProfitCAGR5)},
	}

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
		{"revenue", "Revenue"},
		{"net_profit", "Net Profit"},
		{"ebitda", "EBITDA"},
		{"debt", "Debt"},
	}

	fmt.Fprintf(w, `<div class="sparkline-grid">`)
	for _, sp := range specs {
		series := analytics.TTMSeries(history, sp.metric)
		latest, latestOK := analytics.LatestValid(series)
		var deltaText, deltaClass string
		if latestOK {
			if prev, ok := findValuePastLag(series, 4); ok && prev != 0 {
				delta := (latest.Value/prev - 1) * 100
				deltaClass = "pos"
				sign := "+"
				if delta < 0 {
					deltaClass = "neg"
					sign = ""
				}
				deltaText = fmt.Sprintf("%s%.1f%% YoY", sign, delta)
			}
		}
		valueStr := "—"
		if latestOK {
			valueStr = humanFormat(latest.Value)
		}
		svg := buildSparklineSVG(series)
		fmt.Fprintf(w, `<div class="spark">
  <div class="name">%s (TTM)</div>
  <div class="value">%s</div>
  <div class="delta %s">%s</div>
  %s
</div>`, html.EscapeString(sp.name), html.EscapeString(valueStr), deltaClass, html.EscapeString(deltaText), svg)
	}
	fmt.Fprintf(w, `</div>`)
}

// findValuePastLag returns the most recent valid value that is `lag` points
// before the most recent valid value in the series.
func findValuePastLag(s analytics.Series, lag int) (float64, bool) {
	last := -1
	for i := len(s) - 1; i >= 0; i-- {
		if !math.IsNaN(s[i].Value) {
			last = i
			break
		}
	}
	if last < 0 || last-lag < 0 {
		return 0, false
	}
	v := s[last-lag].Value
	if math.IsNaN(v) {
		return 0, false
	}
	return v, true
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

	fmt.Fprintf(w, `<div class="section-title">Trend explorer</div>
<div class="metric-row">
  <span class="label">Metric</span>
  <button class="btn metric-btn active" onclick="selectMetric('revenue', this)">Revenue</button>
  <button class="btn metric-btn" onclick="selectMetric('net_profit', this)">Net Profit</button>
  <button class="btn metric-btn" onclick="selectMetric('ebitda', this)">EBITDA</button>
  <button class="btn metric-btn" onclick="selectMetric('capitalization', this)">Market Cap</button>
  <button class="btn metric-btn" onclick="selectMetric('debt', this)">Debt</button>
  <button class="btn metric-btn" onclick="selectMetric('pe', this)">P/E</button>
  <button class="btn metric-btn" onclick="selectMetric('roe', this)">ROE</button>
  <button class="btn metric-btn" onclick="selectMetric('net_margin', this)">Net Margin</button>
  <button class="btn metric-btn" onclick="selectMetric('ebitda_margin', this)">EBITDA Margin</button>
  <button class="btn metric-btn" onclick="selectMetric('debt_ebitda', this)">Debt/EBITDA</button>
  <button class="btn metric-btn" onclick="selectMetric('revenue_yoy', this)">Revenue YoY</button>
  <button class="btn metric-btn" onclick="selectMetric('net_profit_yoy', this)">Net Profit YoY</button>
</div>
<div class="metric-row">
  <span class="label">Period</span>
  <button class="btn period-btn" onclick="selectPeriod('quarter', this)">Quarterly</button>
  <button class="btn period-btn active" onclick="selectPeriod('ttm', this)">TTM</button>
  <button class="btn period-btn" onclick="selectPeriod('annual', this)">Annual</button>
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
		{"Growth (Revenue CAGR 3Y)", fmtPct(s.RevenueCAGR3Y), "30%", !math.IsNaN(s.RevenueCAGR3Y), s.RevenueCAGR3Y >= 10},
		{"Profitability (ROE)", fmtPct(s.ROE), "25%", !math.IsNaN(s.ROE), s.ROE >= 15},
		{"Margins (Net Margin)", fmtPct(s.NetMargin), "15%", !math.IsNaN(s.NetMargin), s.NetMargin >= 10},
		{"Leverage (Debt/EBITDA)", fmtMultiple(s.DebtEBITDA), "15%", !math.IsNaN(s.DebtEBITDA), s.DebtEBITDA <= 2},
		{"Valuation (P/E)", fmtRatio(s.PE), "15%", !math.IsNaN(s.PE) && s.PE > 0, s.PE > 0 && s.PE <= 12},
	}
	fmt.Fprintf(w, `<div class="score-breakdown">
  <table>
    <thead><tr><th>Component</th><th>Weight</th><th class="num">Value</th><th>Verdict</th></tr></thead>
    <tbody>`)
	for _, r := range rows {
		var verdict string
		switch {
		case !r.ok:
			verdict = `<span style="color:var(--text-muted)">no data</span>`
		case r.good:
			verdict = `<span style="color:var(--pos)">good</span>`
		default:
			verdict = `<span style="color:var(--warn)">weak</span>`
		}
		fmt.Fprintf(w, `<tr><td>%s</td><td>%s</td><td class="num">%s</td><td>%s</td></tr>`,
			html.EscapeString(r.name), r.weight, html.EscapeString(r.value), verdict)
	}
	fmt.Fprintf(w, `</tbody></table>
  <div style="font-size:11px; color:var(--text-muted); margin-top:8px;">
    Heuristic score, not a recommendation. Read each component and form your own judgment.
  </div>
</div>`)
}

func renderNotes(w http.ResponseWriter, company, note string) {
	fmt.Fprintf(w, `<div class="note-card">
  <div class="section-title" style="margin-top:0">Your notes</div>
  <textarea id="noteText" placeholder="Investment thesis, watchlist reminders, due-diligence findings…">%s</textarea>
  <div class="note-actions">
    <span class="status" id="noteStatus"></span>
    <button class="save-btn" onclick="saveNote()">Save</button>
  </div>
</div>`, html.EscapeString(note))
}

func urlEscapeCSV(s string) string {
	return strings.ReplaceAll(s, ",", "%2C")
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
		return "loss-making"
	case v <= 8:
		return "cheap"
	case v <= 15:
		return "fair"
	case v <= 25:
		return "expensive"
	}
	return "very expensive"
}

func leverageComment(v float64) string {
	if math.IsNaN(v) {
		return ""
	}
	switch {
	case v < 0:
		return "net cash"
	case v <= 1:
		return "low leverage"
	case v <= 3:
		return "moderate"
	case v <= 5:
		return "high"
	}
	return "very high"
}
