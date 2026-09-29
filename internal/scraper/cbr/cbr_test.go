package cbr

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestFetchPeriodFromArchive exercises the real extraction + parsing path
// against a saved form 102 archive (102-20240101.rar = full year 2023). The
// expected values are the published RSBU net profits for those banks.
func TestExtractAndParseArchive(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "102-20240101.rar"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	dbf, err := extractDBF(raw, "_P1.DBF")
	if err != nil {
		t.Fatalf("extract dbf: %v", err)
	}

	profits, err := parseForm102(dbf)
	if err != nil {
		t.Fatalf("parse form 102: %v", err)
	}

	// REGN -> approximate 2023 RSBU net profit, billions of RUB.
	want := map[int]float64{
		1481: 1493.1, // Сбербанк
		1000: 241.5,  // ВТБ
		2673: 46.3,   // Тинькофф Банк
	}
	for regn, exp := range want {
		got, ok := profits[regn]
		if !ok {
			t.Errorf("REGN %d missing from parsed profits", regn)
			continue
		}
		if math.Abs(got-exp) > 0.5 {
			t.Errorf("REGN %d net profit = %.1f bln; want ~%.1f", regn, got, exp)
		}
	}

	if len(profits) < 100 {
		t.Errorf("expected 100+ banks with a financial result, got %d", len(profits))
	}
}

// TestExtractAndParseCapital exercises the form 123 path against a saved archive
// (123-20240101.rar = regulatory capital as of year-end 2023).
func TestExtractAndParseCapital(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "123-20240101.rar"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	dbf, err := extractDBF(raw, "_123D.DBF")
	if err != nil {
		t.Fatalf("extract dbf: %v", err)
	}

	capital, err := parseForm123(dbf)
	if err != nil {
		t.Fatalf("parse form 123: %v", err)
	}

	// REGN -> total regulatory capital, billions of RUB (year-end 2023).
	want := map[int]float64{
		1481: 6265.2, // Сбербанк
		1000: 1781.4, // ВТБ
		436:  173.4,  // Банк "Санкт-Петербург"
	}
	for regn, exp := range want {
		got, ok := capital[regn]
		if !ok {
			t.Errorf("REGN %d missing from parsed capital", regn)
			continue
		}
		if math.Abs(got-exp) > 0.5 {
			t.Errorf("REGN %d capital = %.1f bln; want ~%.1f", regn, got, exp)
		}
	}

	if len(capital) < 100 {
		t.Errorf("expected 100+ banks with capital, got %d", len(capital))
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
	if len(tbl.records) != 3 {
		t.Fatalf("expected 3 non-deleted records, got %d", len(tbl.records))
	}

	profits, err := parseForm102(dbf)
	if err != nil {
		t.Fatalf("parseForm102: %v", err)
	}
	if math.Abs(profits[1481]-1493.148578) > 1e-6 {
		t.Errorf("REGN 1481 = %v, want 1493.148578", profits[1481])
	}
	if math.Abs(profits[963]-(-5.0)) > 1e-6 { // loss code 61102 -> negative
		t.Errorf("REGN 963 = %v, want -5.0", profits[963])
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
