package parser

import (
	"strings"
	"testing"
)

func TestParseDividendBatch(t *testing.T) {
	batch, err := ParseDividendBatch(strings.NewReader("компания;год;дивиденды\nX5;2024;12,5\nBELU;2024;0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Entries) != 2 || len(batch.RowErrors) != 0 {
		t.Fatalf("batch = %+v", batch)
	}
	if batch.Entries[0].Company != "X5" || *batch.Entries[0].Dividends != 12.5 || batch.Entries[0].Revenue != nil {
		t.Errorf("first = %+v", batch.Entries[0])
	}
	if *batch.Entries[1].Dividends != 0 {
		t.Errorf("zero payout = %v", batch.Entries[1].Dividends)
	}

	batch, err = ParseDividendBatch(strings.NewReader("company;year;dividends\nX5;2024;\nX5;2024;1\n"))
	if err != nil || len(batch.Entries) != 1 || len(batch.RowErrors) != 1 {
		t.Fatalf("blank cell: %+v %v", batch, err)
	}
	if _, err := ParseDividendBatch(strings.NewReader("компания;год;выручка;дивиденды\nX5;2024;1;1\n")); err == nil {
		t.Error("an IFRS column must not be accepted on the dividends file")
	}
	if _, err := ParseDividendBatch(strings.NewReader("компания;год\nX5;2024\n")); err == nil {
		t.Error("missing dividends column")
	}
}
