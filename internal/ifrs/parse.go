// Package ifrs reads annual IFRS figures from issuer PDFs published on a
// Russian CDN (cdn.financemarker.ru) and turns them into manual entries.
// The parser keeps a figure only when a statement table yields it; a partial
// read leaves the other fields empty so the caller can fill gaps without
// clearing numbers that were typed in by hand.
package ifrs

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// numToken matches one Russian-formatted number, optionally in parentheses
// (a negative) and optionally a percent. Grouped thousands ("1 237 965") stay
// one token; a PDF extractor that drops the spaces ("149266") does too.
var numToken = regexp.MustCompile(`\(?[+-]?(?:\d{1,3}(?:[ \x{00a0}\x{202f}]\d{3})+|\d+)(?:[.,]\d+)?\)?%?`)

const maxSaneBillion = 100_000 // above this the unit was misread

// Parse extracts annual figures from the plain text of one company-year
// report. Money is in billions of RUB. ok is false when neither revenue nor
// net profit could be read — the statement is then not saved.
func Parse(text string) (models.ManualFinancials, bool) {
	doc := compact(text)
	skipCash := strings.Contains(doc, "средстваклиентов") ||
		strings.Contains(doc, "эквивалентыпринадлежащие") ||
		strings.Contains(doc, "принадлежащиекредит")
	skipDebt := strings.Contains(doc, "средстваклиентов")

	var best map[string]cand
	best = map[string]cand{}
	scale := 0.0
	pos := 0
	for _, page := range strings.Split(text, "\f") {
		mode := pageMode(page)
		lines := glueSplitNumbers(pageLines(page))
		var label string
		var nums []token
		flush := func() {
			if label != "" && len(nums) > 0 {
				if metric := metricOf(label); metric != "" {
					if v, cols, ok := annual(nums, scale); ok {
						c := cand{metric: metric, val: v, mode: mode, cols: cols, pos: pos}
						if prev, seen := best[metric]; !seen || better(c, prev) {
							best[metric] = c
						}
					}
				}
			}
			pos++
			label = ""
			nums = nil
		}
		for _, line := range lines {
			if isNumberLine(line) {
				if label == "" {
					continue
				}
				nums = append(nums, scanTokens(line)...)
				continue
			}
			if s := unitScale(line); s != 0 {
				// The unit on this heading applies to the rows that follow,
				// not to the row being closed.
				flush()
				scale = s
			}
			// A text line ends the current row. An adjusted-EBITDA heading is
			// split across lines ("СКОРР." / "EBITDA"); keep the marker so the
			// following figures are not stored as IFRS EBITDA.
			if label != "" && len(nums) == 0 && len(label) < 80 &&
				strings.Contains(compact(label), "скорр") && !strings.Contains(compact(line), "скорр") {
				label = label + " " + line
				continue
			}
			if label != "" && len(nums) == 0 && len(label) < 100 && !metricStart(line) {
				label = label + " " + line
				continue
			}
			flush()
			label = line
		}
		flush()
	}

	var m models.ManualFinancials
	set := func(metric string, dst **float64, allowNeg bool) {
		c, ok := best[metric]
		if !ok {
			return
		}
		if !allowNeg && c.val < 0 {
			return
		}
		*dst = models.Float(c.val)
	}
	set("revenue", &m.Revenue, false)
	set("net_profit", &m.NetProfit, true)
	set("ebitda", &m.EBITDA, true)
	set("operating_profit", &m.OperatingProfit, true)
	set("ocf", &m.OperatingCashFlow, true)
	set("equity", &m.Equity, true)
	if !skipDebt {
		set("debt", &m.Debt, false)
		if m.Debt == nil {
			if lt, lok := best["debt_lt"]; lok && lt.val > 0 {
				if st, sok := best["debt_st"]; sok && st.val >= 0 {
					sum := math.Round((lt.val+st.val)*100) / 100
					if sum < maxSaneBillion {
						m.Debt = models.Float(sum)
					}
				}
			}
		}
	}
	if !skipCash {
		set("cash", &m.Cash, false)
	}
	if c, ok := best["dividends"]; ok {
		v := math.Abs(c.val)
		if v > 0 && v < maxSaneBillion {
			m.Dividends = models.Float(v)
		}
	}
	if m.Revenue == nil && m.NetProfit == nil {
		return models.ManualFinancials{}, false
	}
	return m, true
}

type cand struct {
	metric string
	val    float64
	mode   int // 2 IFRS section, 1 unlabeled, 0 pre-IFRS 16
	cols   int
	pos    int
}

func better(a, b cand) bool {
	if a.mode != b.mode {
		return a.mode > b.mode
	}
	stock := a.metric == "equity" || a.metric == "cash" || a.metric == "debt" ||
		a.metric == "debt_lt" || a.metric == "debt_st"
	if stock {
		return a.pos < b.pos
	}
	if a.cols != b.cols {
		return a.cols > b.cols
	}
	return a.pos < b.pos
}

// pageMode scores a page by its heading. Figures published "before IFRS 16"
// are the management view (rent still inside EBITDA); the appendix that
// applies IFRS 16 is the one that ties to the audited statements.
func pageMode(page string) int {
	var head []string
	for _, line := range pageLines(page) {
		head = append(head, line)
		if len(head) >= 14 {
			break
		}
	}
	h := strings.ToLower(strings.Join(head, " "))
	pre := strings.Contains(h, "до применения")
	ifrs := strings.Contains(h, "мсфо") || strings.Contains(h, "ifrs")
	switch {
	case ifrs && !pre:
		return 2
	case pre:
		return 0
	default:
		return 1
	}
}

func pageLines(page string) []string {
	raw := strings.Split(page, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\u00a0", " "))
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// glueSplitNumbers joins a thousands-group the extractor broke in two
// ("503 6" / "29" is 503 629).
func glueSplitNumbers(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(out) > 0 && shortDigits(line) && incompleteGroup(out[len(out)-1]) {
			out[len(out)-1] = strings.TrimSpace(out[len(out)-1]) + line
			continue
		}
		out = append(out, line)
	}
	return out
}

func shortDigits(s string) bool {
	if s == "" || len(s) > 2 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func incompleteGroup(s string) bool {
	s = strings.TrimSpace(s)
	if !isNumberLine(s) {
		return false
	}
	i := strings.LastIndexAny(s, " \u00a0\u202f")
	if i < 0 {
		return false
	}
	g := strings.Trim(s[i+1:], " %)")
	return g != "" && len(g) < 3 && shortDigits(g)
}

type token struct {
	v        float64
	percent  bool
	footnote bool
}

func scanTokens(line string) []token {
	found := numToken.FindAllString(line, -1)
	if len(found) == 0 {
		return nil
	}
	toks := make([]token, 0, len(found))
	for _, raw := range found {
		v, pct, ok := parseNum(raw)
		if !ok {
			continue
		}
		toks = append(toks, token{v: v, percent: pct})
	}
	return toks
}

func parseNum(raw string) (float64, bool, bool) {
	pct := strings.Contains(raw, "%")
	neg := strings.Contains(raw, "(") || strings.HasPrefix(strings.TrimLeft(raw, "("), "-")
	clean := strings.Trim(raw, "()+-% ")
	clean = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\u00a0' || r == '\u202f' {
			return -1
		}
		if r == ',' {
			return '.'
		}
		return r
	}, clean)
	v, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0, false, false
	}
	if neg {
		v = -math.Abs(v)
	}
	return v, pct, true
}

func isNumberLine(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "-", "—", "–", "н/д", "н/п", "n/a", "n/m":
		return true
	}
	toks := numToken.FindAllString(s, -1)
	if len(toks) == 0 {
		return false
	}
	rest := s
	for _, t := range toks {
		rest = strings.Replace(rest, t, "", 1)
	}
	rest = strings.Trim(rest, " \t()[]+.%")
	return rest == ""
}

// annual picks the current year's column out of a statement row and scales
// it to billions. Rows of four or more figures are a quarterly-and-annual
// table (the current year is the second-to-last figure); two or three
// figures are a year comparison with the current year first.
func annual(toks []token, scale float64) (float64, int, bool) {
	hasLarge := false
	for _, t := range toks {
		if math.Abs(t.v) >= 1000 {
			hasLarge = true
			break
		}
	}
	var data []float64
	for _, t := range toks {
		if t.percent {
			continue
		}
		if hasLarge && math.Abs(t.v) < 100 && t.v != math.Trunc(t.v) {
			continue // year-on-year rate sitting next to figures in millions
		}
		if hasLarge && t.v == math.Trunc(t.v) && math.Abs(t.v) < 100 {
			continue // footnote marker
		}
		data = append(data, t.v)
	}
	if len(data) == 0 {
		return 0, 0, false
	}
	var v float64
	if len(data) >= 4 {
		v = data[len(data)-2]
	} else {
		v = data[0]
	}
	sc := scale
	if sc == 0 {
		if math.Abs(v) >= 10000 {
			sc = 0.001
		} else {
			sc = 1
		}
	}
	out := math.Round(v*sc*100) / 100
	if math.IsNaN(out) || math.IsInf(out, 0) || math.Abs(out) >= maxSaneBillion {
		return 0, 0, false
	}
	return out, len(data), true
}

func unitScale(s string) float64 {
	l := strings.ToLower(s)
	mln := strings.LastIndex(l, "млн")
	if i := strings.LastIndex(l, "миллион"); i > mln {
		mln = i
	}
	mld := strings.LastIndex(l, "млрд")
	// The extractor sometimes drops the leading "М" ("Млрд" → "лрд").
	if i := strings.LastIndex(l, "лрд"); i > mld {
		mld = i
	}
	if mln < 0 && mld < 0 {
		return 0
	}
	if mld > mln {
		return 1
	}
	return 0.001
}

func metricStart(line string) bool {
	return metricOf(line) != ""
}

func metricOf(label string) string {
	if strings.Contains(label, "%") {
		return ""
	}
	c := compact(label)
	if c == "" || len(c) > 120 {
		return ""
	}
	if strings.Contains(c, "рентабельн") || strings.Contains(c, "скорр") || strings.Contains(c, "скоррект") {
		return ""
	}
	switch {
	case strings.HasPrefix(c, "итоговыручка") || strings.HasPrefix(c, "выручка"):
		if containsAny(c, "рознич", "сегмент", "цифров", "межсегмент", "оказан", "продаж", "процентн", "финтех", "вырос", "увеличил", "составил", "достиг", "снизил") {
			return ""
		}
		return "revenue"
	case strings.HasPrefix(c, "чистаяприбыль"):
		if containsAny(c, "приходящ", "акционер") {
			return ""
		}
		return "net_profit"
	case strings.Contains(c, "прибыль") && strings.Contains(c, "убыток") && strings.Contains(c, "запериод"):
		if containsAny(c, "доналого", "доналог") {
			return ""
		}
		return "net_profit"
	case c == "ebitda" || strings.HasPrefix(c, "ebitda") || strings.HasPrefix(c, "показательebitda"):
		if containsAny(c, "доприменения", "чистыйдолг", "сегмент", "групп", "составил", "вырос", "прогноз", "прибыл") {
			return ""
		}
		// Keep a bare EBITDA line (plus a unit). "показатель EBITDA сегментов"
		// and prose ("EBITDA группы составила") are not the statement total.
		rest := strings.TrimPrefix(c, "показатель")
		rest = strings.TrimPrefix(rest, "ebitda")
		rest = strings.Trim(rest, "0123456789")
		rest = strings.TrimPrefix(rest, "млнруб")
		rest = strings.TrimPrefix(rest, "млрдруб")
		if rest != "" {
			return ""
		}
		return "ebitda"
	case strings.HasPrefix(c, "операционнаяприбыль"):
		return "operating_profit"
	case strings.Contains(c, "операцион") && strings.Contains(c, "денеж") &&
		(strings.Contains(c, "поток") || strings.Contains(c, "средств")):
		if containsAny(c, "корректиров", "доизменений", "наначало", "наконец") {
			return ""
		}
		return "ocf"
	case strings.HasPrefix(c, "итогокапитал") || strings.HasPrefix(c, "всегокапитал") || c == "капитал":
		if containsAny(c, "приходящ", "операцион", "достаточ", "оборотн", "акционерн", "обязатель") {
			return ""
		}
		return "equity"
	case strings.HasPrefix(c, "общийдолг"):
		if strings.Contains(c, "чистый") {
			return ""
		}
		return "debt"
	case strings.HasPrefix(c, "долгосрочныекредитыиоблигации"):
		return "debt_lt"
	case strings.HasPrefix(c, "краткосрочныекредитыиоблигации"):
		return "debt_st"
	case strings.HasPrefix(c, "денежныесредстваиихэквиваленты"):
		if containsAny(c, "увеличен", "эффект", "наначало", "наконец", "принадлежащ") {
			return ""
		}
		return "cash"
	case strings.HasPrefix(c, "дивиденды"):
		// Cash-flow "paid" and equity-statement "declared" are the year's
		// total. Per-share amounts, dividend income and minority distributions
		// are not the figure the yield is built on.
		if containsAny(c, "получен", "наакцию", "доходн", "политик", "рекоменд", "прогноз", "неконтрол", "неконтролир", "миноритар") {
			return ""
		}
		if strings.Contains(c, "выплач") || strings.Contains(c, "уплач") || strings.Contains(c, "объявлен") {
			return "dividends"
		}
		return ""
	default:
		return ""
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func compact(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// firstLetter is the folder the CDN uses (the security's first character).
func firstLetter(name string) string {
	r, _ := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		return ""
	}
	return strings.ToUpper(string(r))
}
