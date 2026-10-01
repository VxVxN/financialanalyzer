package models

import "testing"

func val(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestApplyManualReplacesTheYearsStatements(t *testing.T) {
	history := []QuarterData{
		{Year: 2024, Quarter: "Q4", Company: "X5", Category: "retail", Source: SourceRSBU, Revenue: Float(60), NetProfit: Float(95)},
		{Year: 2025, Quarter: "Q4", Company: "X5", Category: "retail", Source: SourceRSBU,
			Capitalization: Float(800), Revenue: Float(85), NetProfit: Float(124), Debt: Float(119), Cash: Float(0.1),
			Equity: Float(300), PE: Float(6.5), ROE: Float(41), Dividends: Float(70), OperatingCashFlow: Float(9)},
	}
	manual := []ManualFinancials{{Company: "X5", Year: 2025, Revenue: Float(4000), NetProfit: Float(100), Equity: Float(250), EBITDA: Float(300)}}

	got := ApplyManual(history, manual)
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	if got[0].Source != SourceRSBU || *got[0].Revenue != 60 {
		t.Errorf("other years must stay untouched: %+v", got[0])
	}
	row := got[1]
	checks := map[string]struct{ got, want any }{
		"source":         {row.Source, SourceManual},
		"category":       {row.Category, "retail"},
		"revenue":        {val(row.Revenue), 4000.0},
		"ebitda":         {val(row.EBITDA), 300.0},
		"capitalization": {val(row.Capitalization), 800.0}, // market data stays
		"dividends":      {val(row.Dividends), 70.0},       // not entered: kept
		"pe":             {val(row.PE), 8.0},               // 800 / 100, recomputed
		"roe":            {val(row.ROE), 40.0},             // 100 / 250
		"debt":           {val(row.Debt), nil},             // the parent's RSBU debt goes
		"cash":           {val(row.Cash), nil},
		"ocf":            {val(row.OperatingCashFlow), nil},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", name, c.got, c.want)
		}
	}
	if *history[1].Revenue != 85 {
		t.Error("ApplyManual modified its input")
	}
}

func TestApplyManualDividendsOnlyAndNewYears(t *testing.T) {
	history := []QuarterData{
		{Year: 2025, Quarter: "Q3", Company: "SBER", Category: "banks", Source: SourceCBR102, NetProfit: Float(400)},
		{Year: 2025, Quarter: "Q4", Company: "SBER", Category: "banks", Source: SourceCBR102, NetProfit: Float(380), Equity: Float(7000)},
	}
	manual := []ManualFinancials{
		{Company: "SBER", Year: 2025, Dividends: Float(800)},
		{Company: "SBER", Year: 2026, NetProfit: Float(1700), Debt: Float(0)},
		{Company: "SBER", Year: 2023}, // empty: ignored
	}
	got := ApplyManual(history, manual)
	if len(got) != 3 {
		t.Fatalf("rows = %+v, want 3", got)
	}
	q4 := got[1]
	if q4.Source != SourceCBR102 || val(q4.Dividends) != 800.0 || val(q4.NetProfit) != 380.0 || val(q4.Equity) != 7000.0 {
		t.Errorf("dividends-only entry must only set dividends: %+v", q4)
	}
	added := got[2]
	if added.Year != 2026 || added.Quarter != "Q4" || added.Company != "SBER" || added.Category != "banks" ||
		added.Source != SourceManual || val(added.NetProfit) != 1700.0 || val(added.Debt) != 0.0 || added.PE != nil {
		t.Errorf("new year row = %+v", added)
	}

	if got := ApplyManual(nil, []ManualFinancials{{Company: "NEW", Year: 2025, Revenue: Float(1)}}); len(got) != 1 || got[0].Company != "NEW" {
		t.Errorf("manual-only history = %+v", got)
	}
}
