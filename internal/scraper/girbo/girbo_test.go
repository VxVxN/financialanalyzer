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
		got  *float64
		want float64
	}{
		{"revenue", rep.Revenue, 3453.224535},
		{"net profit", rep.NetProfit, 403.734771},
		{"equity", rep.Equity, 1110.183683},
		{"debt", rep.Debt, 770.703349},
		//   cash 1250 = 529 000 696, sales profit 2200 = 1 384 144 385,
		//   operating cash flow 4100 = 2 262 748 444, capex 4221 = 440 565
		{"cash", rep.Cash, 529.000696},
		{"operating profit", rep.OperatingProfit, 1384.144385},
		{"operating cash flow", rep.OperatingCashFlow, 2262.748444},
		{"capex", rep.Capex, 0.440565},
	}
	for _, c := range checks {
		if c.got == nil {
			t.Errorf("%s = nil, want %v", c.name, c.want)
			continue
		}
		if math.Abs(*c.got-c.want) > 1e-6 {
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

func TestParseDetailsAbsentVersusZero(t *testing.T) {
	// Revenue reported, equity an explicit zero, 1410 absent and 1510 present:
	// absent lines stay nil, zeros stay zero, and debt sums what is present.
	body := []byte(`[{"balance":{"current1300":0,"current1510":2000000},
		"financialResult":{"current2110":5000000,"current2400":null}}]`)
	rep, ok, err := parseDetails(body, 2024)
	if err != nil || !ok {
		t.Fatalf("parseDetails: ok=%v err=%v", ok, err)
	}
	if rep.Revenue == nil || *rep.Revenue != 5 {
		t.Errorf("revenue = %v, want 5", rep.Revenue)
	}
	if rep.NetProfit != nil {
		t.Errorf("net profit = %v, want nil (null in payload)", *rep.NetProfit)
	}
	if rep.Equity == nil || *rep.Equity != 0 {
		t.Errorf("equity = %v, want reported zero", rep.Equity)
	}
	if rep.Debt == nil || *rep.Debt != 2 {
		t.Errorf("debt = %v, want 2 (1510 only)", rep.Debt)
	}

	noDebt := []byte(`[{"balance":{},"financialResult":{"current2110":1000000}}]`)
	rep, _, _ = parseDetails(noDebt, 2024)
	if rep.Debt != nil || rep.Cash != nil || rep.Capex != nil || rep.OperatingCashFlow != nil {
		t.Errorf("unfiled sections: debt=%v cash=%v capex=%v ocf=%v, want all nil", rep.Debt, rep.Cash, rep.Capex, rep.OperatingCashFlow)
	}
}

// Within a filed section (its total present) a line the form omits is a real
// zero: a holding with no borrowings and no capex (like OZON's 2025 report).
// A capex filed with a minus sign still counts as an outflow.
func TestParseDetailsOmittedLinesInFiledSections(t *testing.T) {
	body := []byte(`[{"balance":{"current1600":90000000,"current1250":100000,"current1300":80000000},
		"financialResult":{"current2110":200000,"current2200":100000,"current2400":30300000},
		"fundsMovement":{"current4100":25000000}}]`)
	rep, ok, err := parseDetails(body, 2025)
	if err != nil || !ok {
		t.Fatalf("parseDetails: ok=%v err=%v", ok, err)
	}
	for name, got := range map[string]*float64{"debt": rep.Debt, "capex": rep.Capex} {
		if got == nil || *got != 0 {
			t.Errorf("%s = %v, want reported zero", name, got)
		}
	}
	if rep.Cash == nil || *rep.Cash != 0.1 || rep.OperatingCashFlow == nil || *rep.OperatingCashFlow != 25 {
		t.Errorf("cash = %v, ocf = %v", rep.Cash, rep.OperatingCashFlow)
	}

	signed := []byte(`[{"financialResult":{"current2110":1000000},
		"fundsMovement":{"current4100":-5000000,"current4221":-2000000}}]`)
	rep, _, _ = parseDetails(signed, 2025)
	if rep.Capex == nil || *rep.Capex != 2 || *rep.OperatingCashFlow != -5 {
		t.Errorf("signed filing: capex = %v, ocf = %v; want 2 and -5", rep.Capex, rep.OperatingCashFlow)
	}
	if rep.OperatingProfit != nil {
		t.Errorf("operating profit = %v, want nil (line absent)", *rep.OperatingProfit)
	}
}
