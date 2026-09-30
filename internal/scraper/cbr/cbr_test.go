package cbr

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestExtractAndParseArchive exercises the real extraction + parsing path
// against a saved form 102 archive (102-20240101.rar = full year 2023). The
// expected net profits are the published RSBU figures for those banks; the
// revenues were computed independently from the same archive's section totals
// (Сбербанк: net interest income 2414.7 + fee income 999.8).
func TestExtractAndParseArchive(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "102-20240101.rar"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	dbf, err := extractDBF(raw, "_P1.DBF")
	if err != nil {
		t.Fatalf("extract dbf: %v", err)
	}

	figures, err := parseForm102(dbf)
	if err != nil {
		t.Fatalf("parse form 102: %v", err)
	}

	// REGN -> 2023 RSBU figures, billions of RUB.
	want := map[int]Form102{
		1481: {NetProfit: 1493.149, Revenue: 3414.518}, // Сбербанк
		1000: {NetProfit: 241.482, Revenue: 703.959},   // ВТБ
		2673: {NetProfit: 46.263, Revenue: 420.914},    // Тинькофф Банк
	}
	for regn, exp := range want {
		got, ok := figures[regn]
		if !ok {
			t.Errorf("REGN %d missing from parsed figures", regn)
			continue
		}
		if math.Abs(got.NetProfit-exp.NetProfit) > 0.001 {
			t.Errorf("REGN %d net profit = %.3f bln; want %.3f", regn, got.NetProfit, exp.NetProfit)
		}
		if math.Abs(got.Revenue-exp.Revenue) > 0.001 {
			t.Errorf("REGN %d revenue = %.3f bln; want %.3f", regn, got.Revenue, exp.Revenue)
		}
	}

	if len(figures) < 100 {
		t.Errorf("expected 100+ banks with a financial result, got %d", len(figures))
	}
}

// TestExtractAndParseEquity exercises the form 101 path against a saved archive
// (101-20240101.rar = balances as of year-end 2023, first-order accounts).
// Сбербанк: 102 (67.8) + 106 (-12.3) + 107 (3.5) + 108 (4798.3) + 114 (-9.8) +
// 706 (the year's profit, 1493.1) = 6340.7.
func TestExtractAndParseEquity(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "101-20240101.rar"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	dbf, err := extractDBF(raw, "B1.DBF")
	if err != nil {
		t.Fatalf("extract dbf: %v", err)
	}

	equity, err := parseForm101(dbf)
	if err != nil {
		t.Fatalf("parse form 101: %v", err)
	}

	// REGN -> balance-sheet equity, billions of RUB (year-end 2023).
	want := map[int]float64{
		1481: 6340.667, // Сбербанк
		1000: 1204.094, // ВТБ
		2673: 206.955,  // Тинькофф Банк
	}
	for regn, exp := range want {
		got, ok := equity[regn]
		if !ok {
			t.Errorf("REGN %d missing from parsed equity", regn)
			continue
		}
		if math.Abs(got-exp) > 0.001 {
			t.Errorf("REGN %d equity = %.3f bln; want %.3f", regn, got, exp)
		}
	}

	if len(equity) < 100 {
		t.Errorf("expected 100+ banks with equity, got %d", len(equity))
	}
}

// TestParseForm101 covers the account selection and sign rules on a synthetic
// table: plan filter, passive/active sides, 5-digit rows used only when no
// first-order row exists for the account, junk after a NUL (pre-2022 archives)
// and non-equity accounts ignored.
func TestParseForm101(t *testing.T) {
	const planA, planB = "\x80", "\x81" // cp866 "А", "Б"
	dbf := buildDBF(
		[]dbfFieldDef{{"REGN", 4}, {"PLAN", 1}, {"NUM_SC", 5}, {"A_P", 1}, {"IITG", 20}},
		[][]string{
			// Bank 1: old archive, second-order accounts only.
			{"1", planA, "10207", "2", "1000000"},      // +1.0 charter capital
			{"1", planA, "10605", "1", "200000"},       // -0.2 negative revaluation
			{"1", planA, "10801", "2", "3000000"},      // +3.0 retained earnings
			{"1", planA, "70601", "2", "5000000"},      // +5.0 current-year income
			{"1", planA, "70606", "1", "4500000"},      // -4.5 current-year expense
			{"1", planA, "20202", "1", "9000000"},      // cash: not equity
			{"1", planA, "ITGAP", "2", "99000000"},     // total line: not an account
			{"1", planB, "10207", "2", "7000000"},      // off-plan: ignored
			{"1", planA, "109\x00\xd1", "1", "100000"}, // -0.1, junk after NUL
			// Bank 2: both orders for 108 -> the first-order row wins.
			{"2", planA, "108", "2", "4000000"},   // +4.0
			{"2", planA, "10801", "2", "4000000"}, // same money, second order
			{"2", planA, "111", "1", "500000"},    // -0.5 dividends
			{"2", planA, "707", "2", "250000"},    // +0.25 last year's result
			{"2", planA, "708", "2", "0"},
			{"2", planA, "102", "9", "123"}, // unknown side: skipped
		},
		nil,
	)

	equity, err := parseForm101(dbf)
	if err != nil {
		t.Fatalf("parseForm101: %v", err)
	}
	want := map[int]float64{1: 1.0 - 0.2 + 3.0 + 5.0 - 4.5 - 0.1, 2: 4.0 - 0.5 + 0.25}
	for regn, exp := range want {
		if math.Abs(equity[regn]-exp) > 1e-9 {
			t.Errorf("REGN %d equity = %v, want %v", regn, equity[regn], exp)
		}
	}
	if len(equity) != len(want) {
		t.Errorf("parsed %d banks, want %d: %v", len(equity), len(want), equity)
	}
}

func TestArchiveDate(t *testing.T) {
	tests := []struct {
		year    int
		quarter string
		want    string
		ok      bool
	}{
		{2023, "Q1", "20230401", true},
		{2023, "Q2", "20230701", true},
		{2023, "Q3", "20231001", true},
		{2023, "Q4", "20240101", true}, // full year reported the next 1 Jan
		{2023, "Q5", "", false},
	}
	for _, tt := range tests {
		got, err := ArchiveDate(tt.year, tt.quarter)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("ArchiveDate(%d,%q) = %q,%v; want %q,ok=%v",
				tt.year, tt.quarter, got, err, tt.want, tt.ok)
		}
	}
}

// TestParseDBF builds a tiny dBASE III table in memory and checks the header,
// field, deleted-record and cell parsing without relying on a fixture.
func TestParseDBF(t *testing.T) {
	dbf := buildDBF(
		[]dbfFieldDef{{"REGN", 4}, {"CODE", 5}, {"SIM_ITOGO", 16}},
		[][]string{
			{"1481", "61101", "1493148578"},
			{"1000", "61101", "241481674"},
			{"DELETED", "x", "x"}, // marked deleted below
			{"963", "61102", "5000000"},
			{"963", "11000", "9000000"}, // interest income
			{"963", "12000", "1000000"}, // commission booked as interest
			{"963", "15000", "7000000"}, // provision release: excluded
			{"963", "27000", "2000000"}, // fee income
			{"963", "31000", "4000000"}, // interest expense
			{"963", "35000", "500000"},  // adjustment reducing interest income
			{"963", "37000", "3000000"}, // provision charge: excluded
			{"42", "11000", "1000000"},  // no financial result: omitted
		},
		map[int]bool{2: true},
	)

	tbl, err := parseDBF(dbf)
	if err != nil {
		t.Fatalf("parseDBF: %v", err)
	}
	if tbl.col("REGN") != 0 || tbl.col("CODE") != 1 || tbl.col("SIM_ITOGO") != 2 {
		t.Fatalf("unexpected columns: %+v", tbl.fields)
	}
	if len(tbl.records) != 11 {
		t.Fatalf("expected 11 non-deleted records, got %d", len(tbl.records))
	}

	figures, err := parseForm102(dbf)
	if err != nil {
		t.Fatalf("parseForm102: %v", err)
	}
	if math.Abs(figures[1481].NetProfit-1493.148578) > 1e-6 {
		t.Errorf("REGN 1481 = %v, want 1493.148578", figures[1481].NetProfit)
	}
	if math.Abs(figures[963].NetProfit-(-5.0)) > 1e-6 { // loss code 61102 -> negative
		t.Errorf("REGN 963 net profit = %v, want -5.0", figures[963].NetProfit)
	}
	if math.Abs(figures[963].Revenue-7.5) > 1e-6 { // 9 + 1 - 4 - 0.5 + 2
		t.Errorf("REGN 963 revenue = %v, want 7.5", figures[963].Revenue)
	}
	if _, ok := figures[42]; ok {
		t.Error("REGN 42 has no financial result and must be omitted")
	}
}

// TestParseDBFJunkAfterFieldName covers the pre-2022 archives, whose field
// descriptors leave garbage after the name's NUL terminator.
func TestParseDBFJunkAfterFieldName(t *testing.T) {
	dbf := buildDBF(
		[]dbfFieldDef{{"REGN\x00\xd0\x31", 4}, {"CODE\x00\xe8\xa7", 5}, {"SIM_ITOGO\x00\x5c", 16}},
		[][]string{{"2673", "61101", "2673066"}},
		nil,
	)

	figures, err := parseForm102(dbf)
	if err != nil {
		t.Fatalf("parseForm102: %v", err)
	}
	if math.Abs(figures[2673].NetProfit-2.673066) > 1e-9 {
		t.Errorf("REGN 2673 = %v, want 2.673066", figures[2673].NetProfit)
	}
}

// ---- DBF builder for tests --------------------------------------------------

type dbfFieldDef struct {
	name   string
	length int
}

// buildDBF assembles a minimal dBASE III file from string cells. Cells are
// left-justified into their field width (good enough for the parser's
// whitespace trimming); records whose index is in deleted are flagged '*'.
func buildDBF(fields []dbfFieldDef, rows [][]string, deleted map[int]bool) []byte {
	const fieldDesc = 32
	headerSize := 32 + len(fields)*fieldDesc + 1
	recordSize := 1
	for _, f := range fields {
		recordSize += f.length
	}

	buf := make([]byte, 32)
	buf[0] = 0x03 // dBASE III
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(rows)))
	binary.LittleEndian.PutUint16(buf[8:10], uint16(headerSize))
	binary.LittleEndian.PutUint16(buf[10:12], uint16(recordSize))

	for _, f := range fields {
		desc := make([]byte, fieldDesc)
		copy(desc, f.name)
		desc[11] = 'C'
		desc[16] = byte(f.length)
		buf = append(buf, desc...)
	}
	buf = append(buf, 0x0D) // header terminator

	for i, row := range rows {
		rec := make([]byte, recordSize)
		for j := range rec {
			rec[j] = ' '
		}
		if deleted[i] {
			rec[0] = '*'
		}
		pos := 1
		for j, f := range fields {
			cell := ""
			if j < len(row) {
				cell = row[j]
			}
			copy(rec[pos:pos+f.length], cell)
			pos += f.length
		}
		buf = append(buf, rec...)
	}
	return append(buf, 0x1A) // EOF marker
}
