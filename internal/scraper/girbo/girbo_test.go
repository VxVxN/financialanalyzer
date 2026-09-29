package girbo

import (
	"math"
	"os"
	"testing"
)

func TestParseSearch(t *testing.T) {
	body, err := os.ReadFile("testdata/lukoil_search.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// The fixture's inn field is highlighted: "<strong>7708004767</strong>".
	id, err := parseSearch(body, "7708004767")
	if err != nil {
		t.Fatalf("parseSearch: %v", err)
	}
	if id != 6681478 {
		t.Errorf("org id = %d, want 6681478", id)
	}

	if _, err := parseSearch(body, "0000000000"); err == nil {
		t.Error("expected error for non-matching inn")
	}
}

func TestParseReportList(t *testing.T) {
	body, err := os.ReadFile("testdata/lukoil_bfo_list.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	entries, err := parseReportList(body)
	if err != nil {
		t.Fatalf("parseReportList: %v", err)
	}
	byYear := map[int]int{}
	for _, e := range entries {
		byYear[e.Year] = e.ID
	}
	if byYear[2025] != 27306568 {
		t.Errorf("2025 report id = %d, want 27306568", byYear[2025])
	}
	if byYear[2021] != 13682663 {
		t.Errorf("2021 report id = %d, want 13682663", byYear[2021])
	}
}

func TestParseDetails(t *testing.T) {
	body, err := os.ReadFile("testdata/lukoil_details.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	rep, ok, err := parseDetails(body, 2025)
	if err != nil {
		t.Fatalf("parseDetails: %v", err)
	}
	if !ok {
		t.Fatal("expected usable report")
	}

	// Source figures are in thousands of RUB; we store billions.
	//   revenue   2110 = 3 453 224 535  -> 3453.224535
	//   net prof  2400 =   403 734 771  ->  403.734771
	//   equity    1300 = 1 110 183 683  -> 1110.183683
	//   debt 1410+1510 = 62 480 604 + 708 222 745 -> 770.703349
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"revenue", rep.Revenue, 3453.224535},
		{"net profit", rep.NetProfit, 403.734771},
		{"equity", rep.Equity, 1110.183683},
		{"debt", rep.Debt, 770.703349},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > 1e-6 {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestParseDetailsEmpty(t *testing.T) {
	rep, ok, err := parseDetails([]byte(`[]`), 2024)
	if err != nil {
		t.Fatalf("parseDetails: %v", err)
	}
	if ok {
		t.Errorf("expected empty payload to be unusable, got %+v", rep)
	}
}
