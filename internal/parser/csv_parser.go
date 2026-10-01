package parser

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

type CSVParser struct {
	filePath string
}

func NewCSVParser(filePath string) *CSVParser {
	return &CSVParser{filePath: filePath}
}

type MetricHandler func(*models.QuarterData, float64)

type MetricConfig struct {
	Name        string
	Handler     MetricHandler
	IsSpecial   bool
	SpecialType string
}

func (p *CSVParser) Parse() ([]models.QuarterData, error) {
	file, err := os.Open(p.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	records, err := p.readCSV(file)
	if err != nil {
		return nil, err
	}

	return p.processRecords(records)
}

func (p *CSVParser) readCSV(file io.Reader) ([][]string, error) {
	reader := csv.NewReader(file)
	reader.Comma = ';'
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV: %w", err)
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("insufficient rows: %d", len(records))
	}

	return records, nil
}

func (p *CSVParser) processRecords(records [][]string) ([]models.QuarterData, error) {
	quarters := records[0]
	companyName, category := p.extractCompanyNameAndCategory()

	var results []models.QuarterData

	metricHandlers := p.getMetricHandlers()

	for rowIdx := 1; rowIdx < len(records); rowIdx++ {
		record := records[rowIdx]
		if len(record) == 0 {
			continue
		}

		metricName := strings.TrimSpace(record[0])
		if p.shouldSkipMetric(metricName) {
			continue
		}

		metricConfig := p.detectMetricConfig(metricName, metricHandlers)
		if metricConfig == nil {
			continue
		}

		data, err := p.processMetricRow(metricConfig, metricName, quarters, record, companyName, category)
		if err != nil {
			continue
		}

		results = append(results, data...)
	}

	return results, nil
}

// extractCompanyNameAndCategory derives the company and category from the file
// name, which is expected to be "<COMPANY>_<CATEGORY>.csv". A missing category
// yields an empty string rather than panicking on a malformed name.
func (p *CSVParser) extractCompanyNameAndCategory() (string, string) {
	filename := path.Base(p.filePath)
	filename = strings.TrimSuffix(filename, filepath.Ext(filename))
	company, category, found := strings.Cut(filename, "_")
	if !found {
		return filename, ""
	}
	return company, category
}

func (p *CSVParser) shouldSkipMetric(metricName string) bool {
	return metricName == "Дата отчета" || metricName == "Валюта отчета"
}

func (p *CSVParser) getMetricHandlers() map[string]MetricHandler {
	return map[string]MetricHandler{
		"Капитализация": func(d *models.QuarterData, v float64) { d.Capitalization = &v },
		"Выручка":       func(d *models.QuarterData, v float64) { d.Revenue = &v },
		"EBITDA":        func(d *models.QuarterData, v float64) { d.EBITDA = &v },
		"ROE":           func(d *models.QuarterData, v float64) { d.ROE = &v },
	}
}

func (p *CSVParser) detectMetricConfig(metricName string, handlers map[string]MetricHandler) *MetricConfig {
	switch {
	// Total dividends for the year, billions of RUB, entered in the Q4 column.
	// A prefix match keeps per-share ("Дивиденд, руб/акцию") and yield ("Див
	// доход") rows of aggregator exports out; rejecting "/" and "%" keeps out
	// ratios such as "Дивиденды/прибыль, %", and "акци" per-share variants
	// such as "Дивиденды на акцию, руб".
	case strings.HasPrefix(metricName, "Дивиденды") &&
		!strings.ContainsAny(metricName, "/%") && !strings.Contains(metricName, "акци"):
		return &MetricConfig{IsSpecial: true, SpecialType: "DIVIDENDS"}
	case strings.Contains(metricName, "P/E"):
		return &MetricConfig{IsSpecial: true, SpecialType: "PE"}
	// "/" and "%" keep ratios such as "Долг/EBITDA" out: their value is a
	// multiple, not billions.
	case strings.Contains(metricName, "Долг") && !strings.Contains(metricName, "Чистый") && !isRatioRow(metricName):
		return &MetricConfig{IsSpecial: true, SpecialType: "DEBT"}
	// Cash-flow rows of aggregator exports (amounts only, not ratios such as
	// "CAPEX/Выручка, %"). Capex is stored as a positive outflow.
	case strings.HasPrefix(metricName, "CAPEX") && !isRatioRow(metricName):
		return &MetricConfig{IsSpecial: true, SpecialType: "CAPEX"}
	case strings.HasPrefix(metricName, "Операционный денежный поток") && !isRatioRow(metricName):
		return &MetricConfig{IsSpecial: true, SpecialType: "OPERATING_CASH_FLOW"}
	case strings.HasPrefix(metricName, "Операционная прибыль") && !isRatioRow(metricName):
		return &MetricConfig{IsSpecial: true, SpecialType: "OPERATING_PROFIT"}
	case strings.HasPrefix(metricName, "Наличность") && !isRatioRow(metricName):
		return &MetricConfig{IsSpecial: true, SpecialType: "CASH"}
	case strings.Contains(metricName, "Чистая прибыль") && !strings.Contains(metricName, "н/с"):
		return &MetricConfig{IsSpecial: true, SpecialType: "NET_PROFIT"}
	}

	for key, handler := range handlers {
		if strings.HasPrefix(metricName, key) {
			return &MetricConfig{
				Name:    key,
				Handler: handler,
			}
		}
	}

	return nil
}

// isRatioRow reports whether a row label names a ratio or percentage rather
// than an amount.
func isRatioRow(metricName string) bool {
	return strings.ContainsAny(metricName, "/%")
}

func (p *CSVParser) processMetricRow(
	config *MetricConfig,
	metricName string,
	quarters []string,
	record []string,
	companyName string,
	category string,
) ([]models.QuarterData, error) {

	var results []models.QuarterData

	for colIdx := 1; colIdx < len(record) && colIdx < len(quarters); colIdx++ {
		quarterStr := strings.TrimSpace(quarters[colIdx])
		if quarterStr == "" || quarterStr == "LTM" {
			continue
		}

		year, quarter, err := ParseQuarter(quarterStr)
		if err != nil {
			continue
		}

		parse := p.parseValue
		if config.IsSpecial && config.SpecialType == "DIVIDENDS" {
			// Dividends are the year's total on Q4; other columns (often
			// zero-filled in quarterly exports) would create bogus rows.
			if quarter != "Q4" {
				continue
			}
			// A hand-entered dividends row means what it says: 0 = no payout.
			parse = p.parseNumber
		}
		value, err := parse(record[colIdx])
		if err != nil {
			continue
		}

		data := models.QuarterData{
			Year:     year,
			Quarter:  quarter,
			Company:  companyName,
			Category: category,
			Source:   models.SourceCSV,
		}

		p.applyMetricValue(config, &data, value)

		if !data.IsEmpty() {
			results = append(results, data)
		}
	}

	return results, nil
}

// parseValue parses a cell, treating zero as "no data" (see below).
func (p *CSVParser) parseValue(valueStr string) (float64, error) {
	v, err := p.parseNumber(valueStr)
	if err != nil {
		return 0, err
	}
	// Aggregator exports (smart-lab style) fill cells they have no figure for
	// with 0 — a bank's revenue, the P/E of a loss-making quarter — so in CSV a
	// zero, however it is formatted, means "no data" and is not stored. Honest
	// zeros come from the primary sources, which distinguish absent from zero.
	if v == 0 {
		return 0, fmt.Errorf("zero is a placeholder for no data")
	}
	return v, nil
}

// parseNumber normalizes and parses a cell; empty and "-" are errors, zero is
// a value.
func (p *CSVParser) parseNumber(valueStr string) (float64, error) {
	valueStr = strings.TrimSpace(valueStr)
	valueStr = strings.ReplaceAll(valueStr, ",", ".")
	valueStr = strings.ReplaceAll(valueStr, " ", "")
	valueStr = strings.ReplaceAll(valueStr, "\"", "")
	valueStr = strings.ReplaceAll(valueStr, "%", "")

	if valueStr == "" || valueStr == "-" {
		return 0, fmt.Errorf("empty or invalid value")
	}

	return strconv.ParseFloat(valueStr, 64)
}

func (p *CSVParser) applyMetricValue(config *MetricConfig, data *models.QuarterData, value float64) {
	if config.IsSpecial {
		switch config.SpecialType {
		case "PE":
			data.PE = &value
		case "DEBT":
			data.Debt = &value
		case "NET_PROFIT":
			data.NetProfit = &value
		case "DIVIDENDS":
			data.Dividends = &value
		case "CASH":
			data.Cash = &value
		case "OPERATING_PROFIT":
			data.OperatingProfit = &value
		case "OPERATING_CASH_FLOW":
			data.OperatingCashFlow = &value
		case "CAPEX":
			v := math.Abs(value)
			data.Capex = &v
		}
	} else if config.Handler != nil {
		config.Handler(data, value)
	}
}

func ParseQuarter(q string) (int, string, error) {
	if len(q) < 6 {
		return 0, "", fmt.Errorf("invalid quarter format: %s", q)
	}

	year, err := strconv.Atoi(q[:4])
	if err != nil {
		return 0, "", fmt.Errorf("invalid year: %w", err)
	}

	return year, q[5:], nil
}
