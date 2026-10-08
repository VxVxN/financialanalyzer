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
