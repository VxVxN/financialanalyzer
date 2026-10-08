package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/analytics"
)

func ptr(v float64) *float64 { return &v }

func TestValuationVerdict(t *testing.T) {
	band := analytics.HistoryBand{Metric: "pe", Current: 3, Min: 3, Median: 7, Max: 10, Percentile: 12.5, N: 4, From: "2022-Q4", To: "2025-Q4"}
	row := analytics.ScreenerRow{
		PE: ptr(3), PESector: ptr(9), Bands: map[string]analytics.HistoryBand{"pe": band},
		Basis: map[string]analytics.Point{"pe": {}},
	}

	sentence, pills, ok := valuationVerdict(row, true)
	want := "P/E 3.00 по МСФО группы — 12-й перцентиль собственной истории за 2022-Q4 – 2025-Q4 и на 67% ниже медианы сектора (9.00)."
	if !ok || sentence != want {
		t.Errorf("sentence = %q, want %q", sentence, want)
	}
	joined := strings.Join(pills, "")
	for _, w := range []string{`pill pill-cheap">дёшево относительно истории`, `pill pill-cheap">дешевле сектора (-67%)`} {
		if !strings.Contains(joined, w) {
			t.Errorf("pills %q lack %q", joined, w)
		}
	}

	// Without peers the sector half and its pill disappear.
	sentence, pills, _ = valuationVerdict(row, false)
	if strings.Contains(sentence, "сектора") || len(pills) != 1 {
		t.Errorf("without peers: %q, %d pills", sentence, len(pills))
	}

	// Sector only: no band. Standalone RSBU names the legal entity.
	sentence, _, ok = valuationVerdict(analytics.ScreenerRow{
		PE: ptr(12), PESector: ptr(8),
		Basis: map[string]analytics.Point{"pe": {Standalone: true}},
	}, true)
	if !ok || sentence != "P/E 12.00 по РСБУ юрлица — на 50% выше медианы сектора (8.00)." {
		t.Errorf("sector only: %q", sentence)
	}

	// A bank's multiple rests on the Central Bank forms.
	sentence, _, ok = valuationVerdict(analytics.ScreenerRow{
		Bank: true, PE: ptr(4), PESector: ptr(5),
		Basis: map[string]analytics.Point{"pe": {Standalone: true}},
	}, true)
	if !ok || !strings.Contains(sentence, "по формам ЦБ") {
		t.Errorf("bank basis: %q", sentence)
	}

	// No P/E (a loss): the verdict falls through to P/B.
	pb := analytics.HistoryBand{Metric: "pb", Min: 0.5, Median: 1, Max: 2, Percentile: 80, From: "2016-Q4", To: "2025-Q4"}
	sentence, pills, ok = valuationVerdict(analytics.ScreenerRow{PB: ptr(1.8), Bands: map[string]analytics.HistoryBand{"pb": pb}}, true)
	if !ok || !strings.HasPrefix(sentence, "P/B 1.80 — 80-й перцентиль") || !strings.Contains(strings.Join(pills, ""), "pill-dear") {
		t.Errorf("P/B fallback: %q %v", sentence, pills)
	}

	// A value with neither band nor sector says nothing, until the basis is known.
	if _, _, ok := valuationVerdict(analytics.ScreenerRow{PE: ptr(5)}, true); ok {
		t.Error("a bare value should give no verdict")
	}
	sentence, _, ok = valuationVerdict(analytics.ScreenerRow{
		PE: ptr(5), Basis: map[string]analytics.Point{"pe": {}},
	}, true)
	if !ok || sentence != "P/E 5.00 по МСФО группы." {
		t.Errorf("basis only: %q", sentence)
	}
}

func TestValuationRulers(t *testing.T) {
	band := analytics.HistoryBand{Metric: "pe", Min: 4, Median: 6, Max: 10, Percentile: 10, N: 5, From: "2021-Q4", To: "2025-Q4"}
	row := analytics.ScreenerRow{PE: ptr(4), PESector: ptr(8), DivYield: ptr(5),
		Bands: map[string]analytics.HistoryBand{"pe": band}}

	rec := httptest.NewRecorder()
	renderValuationRulers(rec, row, true)
	body := rec.Body.String()
	for _, want := range []string{
		`class="now pos" style="left:0.0%"`, // the lowest P/E of the range: cheap, at the left edge
		`class="sector" style="left:66.7%"`, // 8 on the 4..10 scale
		`class="median" style="left:33.3%"`,
		"10-й перцентиль", "мало истории", // yield has a value but no band
		"медиана по сектору",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rulers lack %q", want)
		}
	}
	// A lone value (yield 5, no band, no sector) still gets a finite scale.
	if strings.Contains(body, "NaN") || strings.Contains(body, "Inf") {
		t.Error("a degenerate scale produced NaN/Inf positions")
	}

	rec = httptest.NewRecorder()
	renderValuationRulers(rec, row, false)
	if body := rec.Body.String(); strings.Contains(body, `class="sector"`) || strings.Contains(body, "медиана по сектору") {
		t.Error("without peers the sector tick and its legend must be hidden")
	}

	rec = httptest.NewRecorder()
	renderValuationRulers(rec, analytics.ScreenerRow{}, true)
	if !strings.Contains(rec.Body.String(), "Нет оценки") {
		t.Error("no valuation should say so")
	}
}
