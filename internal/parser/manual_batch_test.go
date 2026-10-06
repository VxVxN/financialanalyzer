package parser

import (
	"strings"
	"testing"
)

func TestParseManualBatch(t *testing.T) {
	in := strings.Join([]string{
		"компания;год;выручка;чистая прибыль;капитал;долг;денежные средства;дивиденды;капзатраты",
		"X5;2024;3 500,5;120;800;0;50;40;",
		"MGNT;2023;2000;;;;;",
		";2024;1;;;;;",
		"X5;2024;1;;;;;",
		"BAD;год;1;;;;;",
		"",
	}, "\n")
	batch, err := ParseManualBatch(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Entries) != 2 {
		t.Fatalf("entries = %+v, want 2", batch.Entries)
	}
	x5 := batch.Entries[0]
	if x5.Company != "X5" || x5.Year != 2024 || *x5.Revenue != 3500.5 || *x5.Debt != 0 || *x5.Cash != 50 || x5.Equity == nil || *x5.Equity != 800 {
		t.Errorf("X5 = %+v", x5)
	}
	if x5.Capex != nil || x5.EBITDA != nil {
		t.Errorf("empty cells must stay unset: %+v", x5)
	}
	mgnt := batch.Entries[1]
	if mgnt.Company != "MGNT" || mgnt.Year != 2023 || *mgnt.Revenue != 2000 || mgnt.NetProfit != nil {
		t.Errorf("MGNT = %+v", mgnt)
	}
	if len(batch.RowErrors) != 3 {
		t.Fatalf("row errors = %v, want 3", batch.RowErrors)
	}

	if _, err := ParseManualBatch(strings.NewReader("компания;год;выручка лишняя\nX5;2024;1\n")); err == nil {
		t.Error("unknown column must fail the file")
	}
	if _, err := ParseManualBatch(strings.NewReader("выручка\n1\n")); err == nil {
		t.Error("missing company and year columns must fail the file")
	}
}
