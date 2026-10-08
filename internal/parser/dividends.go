package parser

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// ParseDividendBatch reads a semicolon CSV of annual dividends in billions of
// RUB: company, year, dividends. An empty dividends cell is a row error —
// a blank must not clear a figure this import is not allowed to touch.
// A numeric zero is a real zero (the company paid nothing that year).
func ParseDividendBatch(r io.Reader) (ManualBatch, error) {
	reader := csv.NewReader(r)
	reader.Comma = ';'
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return ManualBatch{}, fmt.Errorf("read CSV: %w", err)
	}
	if len(records) == 0 {
		return ManualBatch{}, fmt.Errorf("empty file")
	}
	cols, err := dividendColumns(records[0])
	if err != nil {
		return ManualBatch{}, err
	}

	var batch ManualBatch
	seen := map[string]int{}
	for i := 1; i < len(records); i++ {
		rowNum := i + 1
		if manualRowEmpty(records[i]) {
			continue
		}
		entry, msg := dividendRow(records[i], cols)
		if msg != "" {
			batch.RowErrors = append(batch.RowErrors, fmt.Sprintf("row %d: %s", rowNum, msg))
			continue
		}
		key := entry.Company + "\x00" + strconv.Itoa(entry.Year)
		if prev, ok := seen[key]; ok {
			batch.RowErrors = append(batch.RowErrors, fmt.Sprintf("row %d: duplicate of row %d (%s %d)", rowNum, prev, entry.Company, entry.Year))
			continue
		}
		seen[key] = rowNum
		batch.Entries = append(batch.Entries, entry)
	}
	return batch, nil
}

func dividendColumns(header []string) (map[string]int, error) {
	cols := map[string]int{}
	for i, raw := range header {
		name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, "\uFEFF")))
		if name == "" {
			continue
		}
		field, ok := manualHeader[name]
		if !ok || (field != "company" && field != "year" && field != "dividends") {
			return nil, fmt.Errorf("unknown column %q", strings.TrimSpace(raw))
		}
		if _, dup := cols[field]; dup {
			return nil, fmt.Errorf("duplicate column %q", strings.TrimSpace(raw))
		}
		cols[field] = i
	}
	for _, req := range []string{"company", "year", "dividends"} {
		if _, ok := cols[req]; !ok {
			return nil, fmt.Errorf("%s column is required", req)
		}
	}
	return cols, nil
}

func dividendRow(row []string, cols map[string]int) (models.ManualFinancials, string) {
	cell := func(field string) string {
		i := cols[field]
		if i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}
	company := cell("company")
	if company == "" || !utf8.ValidString(company) {
		return models.ManualFinancials{}, "company name is required"
	}
	year, err := strconv.Atoi(cell("year"))
	if err != nil {
		return models.ManualFinancials{}, "year must be an integer"
	}
	raw := cell("dividends")
	if raw == "" {
		return models.ManualFinancials{}, "dividends are required"
	}
	v, err := parseAmount(raw)
	if err != nil {
		return models.ManualFinancials{}, "dividends is not a number"
	}
	return models.ManualFinancials{Company: company, Year: year, Dividends: models.Float(v)}, ""
}
