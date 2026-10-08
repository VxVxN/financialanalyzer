package analytics

import (
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }

func TestCheapListMessage(t *testing.T) {
	rows := []ScreenerRow{
		{Company: "DEAR", Comparable: true, Liquid: true, PE: f64(12), PEHistPct: f64(80), PESector: f64(10)},
		{Company: "RSBU", Comparable: false, Liquid: true, PE: f64(3), PEHistPct: f64(5), PESector: f64(10)},
		{Company: "CHEAP", Comparable: true, Liquid: true, PE: f64(4), PEHistPct: f64(10), PESector: f64(8)},
		{Company: "HIST", Comparable: true, Liquid: true, PE: f64(9), PEHistPct: f64(10), PESector: f64(10)}, // only 10% below sector
		{Company: "BANK", Comparable: true, Liquid: true, Bank: true, PE: f64(3), PEHistPct: f64(5), PESector: f64(10)},
		{Company: "THIN", Comparable: true, Liquid: false, PE: f64(3), PEHistPct: f64(5), PESector: f64(10)},
	}
	msg := CheapListMessage(rows)
	if !strings.Contains(msg, "CHEAP — P/E 4.0") || strings.Contains(msg, "DEAR") ||
		strings.Contains(msg, "RSBU") || strings.Contains(msg, "HIST") ||
		strings.Contains(msg, "BANK") || strings.Contains(msg, "THIN") {
		t.Fatalf("message = %q", msg)
	}
	if !strings.Contains(msg, "без банков") || !strings.Contains(msg, "10 млн") {
		t.Fatalf("header = %q", msg)
	}

	if empty := CheapListMessage(nil); !strings.Contains(empty, "Таких компаний нет.") {
		t.Fatalf("empty list = %q", empty)
	}
}

func TestMondayMessage(t *testing.T) {
	rows := []ScreenerRow{
		{Company: "X5", Portfolio: true, PE: f64(4), PEHistPct: f64(8)},
		{Company: "T", Portfolio: true, PE: f64(20), PEHistPct: f64(90)},
		{Company: "MID", Portfolio: true, PE: f64(10), PEHistPct: f64(40)},
		{Company: "OUT", PE: f64(3), PEHistPct: f64(5)},
	}
	msg := MondayMessage(rows,
		[]NoteQuote{{Company: "OLD", Date: "01.06.2026"}},
		[]NoteJump{{Company: "BELU", From: 2023, To: 2024, Ratio: 0.1}},
		[]NoteYear{{Company: "X5", Year: 2024}},
		nil,
	)
	for _, want := range []string{
		"Дешевле своей истории",
		"X5 — P/E 4.0 вошёл в нижнюю четверть",
		"T — P/E 20.0 вошёл в верхнюю четверть",
		"OLD — 01.06.2026",
		"BELU — 2023→2024, в 10,0 раза меньше",
		"X5 — 2024",
		"МСФО: дописан год",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "MID —") || strings.Contains(msg, "OUT —") {
		t.Errorf("message names a name it should not:\n%s", msg)
	}

	again := MondayMessage(rows, nil, nil, nil, map[string]string{"X5": BandLow, "T": BandHigh, "MID": BandMid})
	if strings.Contains(again, "X5 —") || strings.Contains(again, "T —") {
		t.Errorf("a name that stayed in its quartile must not be repeated:\n%s", again)
	}
	moved := MondayMessage(rows, nil, nil, nil, map[string]string{"X5": BandLow, "T": BandMid})
	if !strings.Contains(moved, "T — P/E 20.0 вошёл в верхнюю четверть") || strings.Contains(moved, "X5 —") {
		t.Errorf("only a new entry should be named:\n%s", moved)
	}

	quiet := MondayMessage(nil, nil, nil, nil, nil)
	if !strings.Contains(quiet, "Таких бумаг нет.") || !strings.Contains(quiet, "Таких котировок нет.") ||
		!strings.Contains(quiet, "Таких скачков нет.") || strings.Contains(quiet, "МСФО") {
		t.Fatalf("quiet week = %q", quiet)
	}
}
