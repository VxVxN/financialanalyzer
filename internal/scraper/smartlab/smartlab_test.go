package smartlab

import (
	"os"
	"testing"
)

func TestParseLKOH(t *testing.T) {
	body, err := os.ReadFile("testdata/LKOH.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	rows, err := Parse(body, "LKOH", "oil")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows parsed")
	}

	// Spot-check known values from /tmp/lkoh.html:
	//   header: 2024Q4, 2025Q1, 2025Q2, 2025Q3, 2025Q4
	//   market_cap row: 5 017 / 4 877 / 4 383 / 4 221 / 4 074
	//   revenue row:    4 288 /       / 3 602 /       / 166.0
	//   roe row:        12.4% /       / 10.1% /       / 2.8%
	got := map[string]map[string]float64{}
	for _, r := range rows {
		key := keyOf(r.Year, r.Quarter)
		got[key] = map[string]float64{
			"cap": r.Capitalization,
			"rev": r.Revenue,
			"roe": r.ROE,
		}
	}

	tests := []struct {
		key   string
		field string
		want  float64
		exact bool // true ⇒ require non-zero match
	}{
		{"2024Q4", "cap", 5017, true},
		{"2025Q1", "cap", 4877, true},
		{"2025Q2", "cap", 4383, true},
		{"2024Q4", "rev", 4288, true},
		{"2025Q2", "rev", 3602, true},
		{"2025Q4", "rev", 166.0, true},
		{"2024Q4", "roe", 12.4, true},
		{"2025Q2", "roe", 10.1, true},
	}

	for _, tc := range tests {
		row, ok := got[tc.key]
		if !ok {
			t.Errorf("missing row for %s", tc.key)
			continue
		}
		if row[tc.field] != tc.want {
			t.Errorf("%s.%s = %v, want %v", tc.key, tc.field, row[tc.field], tc.want)
		}
	}

	// Company / category propagation.
	if rows[0].Company != "LKOH" {
		t.Errorf("company = %q, want LKOH", rows[0].Company)
	}
	if rows[0].Category != "oil" {
		t.Errorf("category = %q, want oil", rows[0].Category)
	}
}

func keyOf(year int, quarter string) string {
	return itoa(year) + quarter
}

func itoa(i int) string {
	// avoid importing strconv just for the test
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var buf [16]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}

func TestParseNumber(t *testing.T) {
	tests := []struct {
		in    string
		want  float64
		valid bool
	}{
		{"4 288", 4288, true},
		{"166.0", 166.0, true},
		{"12.4%", 12.4, true},
		{"-1,5", -1.5, true},
		{"", 0, false},
		{"—", 0, false},
		{"-", 0, false},
		{" 4 877", 4877, true}, // nbsp prefix
	}
	for _, tc := range tests {
		got, ok := parseNumber(tc.in)
		if ok != tc.valid {
			t.Errorf("parseNumber(%q): ok=%v, want %v", tc.in, ok, tc.valid)
		}
		if ok && got != tc.want {
			t.Errorf("parseNumber(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseQuarterLabel(t *testing.T) {
	year, q, err := parseQuarterLabel("2024Q4")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if year != 2024 || q != "Q4" {
		t.Errorf("got (%d, %q), want (2024, Q4)", year, q)
	}
	if _, _, err := parseQuarterLabel("bad"); err == nil {
		t.Error("expected error on invalid label")
	}
}
