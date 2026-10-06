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

// ManualBatch is a parsed annual-IFRS file: one company-year per row, the
// same figures as a manual entry. RowErrors are data rows that were skipped;
// a non-nil error from ParseManualBatch means the file itself is unusable.
type ManualBatch struct {
	Entries   []models.ManualFinancials
	RowErrors []string
}

// manualHeader maps a column title (English JSON name or the Russian label
// from the manual form) to a field. Unknown columns are rejected: a typo
// must not silently drop a figure.
var manualHeader = map[string]string{
	"company": "company", "компания": "company",
	"year": "year", "год": "year",
	"revenue": "revenue", "выручка": "revenue",
	"net_profit": "net_profit", "чистая прибыль": "net_profit", "прибыль": "net_profit",
	"ebitda":           "ebitda",
	"operating_profit": "operating_profit",
	"операционная прибыль":        "operating_profit",
	"operating_cash_flow":         "operating_cash_flow",
	"операционный денежный поток": "operating_cash_flow",
	"capex": "capex", "капзатраты": "capex",
	"debt": "debt", "долг": "debt",
	"cash": "cash", "денежные средства": "cash", "наличность": "cash",
	"equity": "equity", "капитал": "equity",
	"dividends": "dividends", "дивиденды": "dividends", "дивиденды за год": "dividends",
}

// ParseManualBatch reads a semicolon CSV of annual figures in billions of RUB.
// An empty cell is "not entered". A numeric zero is a real zero (unlike the
// quarterly aggregator import, where zero is a placeholder).
func ParseManualBatch(r io.Reader) (ManualBatch, error) {
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
	cols, err := manualColumns(records[0])
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
		entry, msg := manualRow(records[i], cols)
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

func manualColumns(header []string) (map[string]int, error) {
	cols := map[string]int{}
	for i, raw := range header {
		name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, "\uFEFF")))
		if name == "" {
			continue
		}
		field, ok := manualHeader[name]
		if !ok {
			return nil, fmt.Errorf("unknown column %q", strings.TrimSpace(raw))
		}
		if _, dup := cols[field]; dup {
			return nil, fmt.Errorf("duplicate column %q", strings.TrimSpace(raw))
		}
		cols[field] = i
	}
	if _, ok := cols["company"]; !ok {
		return nil, fmt.Errorf("company column is required")
	}
	if _, ok := cols["year"]; !ok {
		return nil, fmt.Errorf("year column is required")
	}
	return cols, nil
}

func manualRowEmpty(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func manualRow(row []string, cols map[string]int) (models.ManualFinancials, string) {
	cell := func(field string) string {
		i, ok := cols[field]
		if !ok || i >= len(row) {
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
	m := models.ManualFinancials{Company: company, Year: year}
	set := func(field string, dst **float64) string {
		raw := cell(field)
		if raw == "" {
			return ""
		}
		v, err := parseAmount(raw)
		if err != nil {
			return field + " is not a number"
		}
		*dst = models.Float(v)
		return ""
	}
	for _, f := range []struct {
		field string
		dst   **float64
	}{
		{"revenue", &m.Revenue},
		{"net_profit", &m.NetProfit},
		{"ebitda", &m.EBITDA},
		{"operating_profit", &m.OperatingProfit},
		{"operating_cash_flow", &m.OperatingCashFlow},
		{"capex", &m.Capex},
		{"debt", &m.Debt},
		{"cash", &m.Cash},
		{"equity", &m.Equity},
		{"dividends", &m.Dividends},
	} {
		if msg := set(f.field, f.dst); msg != "" {
			return models.ManualFinancials{}, msg
		}
	}
	return m, ""
}

// parseAmount parses a billions figure. Empty is the caller's concern; zero
// is a value. Spaces and a decimal comma are accepted, as on the manual form.
func parseAmount(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, "\u00a0", "")
	raw = strings.ReplaceAll(raw, " ", "")
	raw = strings.ReplaceAll(raw, ",", ".")
	raw = strings.ReplaceAll(raw, "\"", "")
	if raw == "" || raw == "-" {
		return 0, fmt.Errorf("empty")
	}
	return strconv.ParseFloat(raw, 64)
}
