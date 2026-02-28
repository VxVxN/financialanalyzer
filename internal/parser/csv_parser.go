package parser

import (
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

type CSVParser struct {
	rootPath string
	logger   *slog.Logger
}

func NewCSVParser(rootPath string, logger *slog.Logger) *CSVParser {
	return &CSVParser{rootPath: rootPath, logger: logger}
}

type MetricHandler func(*models.QuarterData, float64)

type MetricConfig struct {
	Name        string
	Handler     MetricHandler
	IsSpecial   bool
	SpecialType string
}

func (p *CSVParser) Parse() ([]models.QuarterData, error) {
	var allResults []models.QuarterData

	err := filepath.Walk(p.rootPath, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(strings.ToLower(info.Name()), ".csv") {
			return nil
		}

		p.logger.Debug("Parsing file", "path", filePath)

		fileParser := &CSVParser{rootPath: filePath}
		results, err := fileParser.parseFile()
		if err != nil {
			p.logger.Error("Error parsing file", "path", filePath, "err", err)
			return nil
		}

		allResults = append(allResults, results...)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}

	return allResults, nil
}

func (p *CSVParser) parseFile() ([]models.QuarterData, error) {
	file, err := os.Open(p.rootPath)
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

		data, err := p.processMetricRow(metricConfig, quarters, record, companyName, category)
		if err != nil {
			continue
		}

		results = append(results, data...)
	}

	return results, nil
}

func (p *CSVParser) extractCompanyNameAndCategory() (string, string) {
	filename := path.Base(p.rootPath)
	filename = strings.TrimSuffix(filename, filepath.Ext(filename))
	splitedFilename := strings.Split(filename, "_")

	if len(splitedFilename) < 2 {
		return filename, "unknown"
	}

	return splitedFilename[0], splitedFilename[1]
}

func (p *CSVParser) shouldSkipMetric(metricName string) bool {
	return metricName == "Дата отчета" || metricName == "Валюта отчета"
}

func (p *CSVParser) getMetricHandlers() map[string]MetricHandler {
	return map[string]MetricHandler{
		"Капитализация": func(d *models.QuarterData, v float64) { d.Capitalization = v },
		"Выручка":       func(d *models.QuarterData, v float64) { d.Revenue = v },
		"EBITDA":        func(d *models.QuarterData, v float64) { d.EBITDA = v },
		"ROE":           func(d *models.QuarterData, v float64) { d.ROE = v },
	}
}

func (p *CSVParser) detectMetricConfig(metricName string, handlers map[string]MetricHandler) *MetricConfig {
	switch {
	case strings.Contains(metricName, "P/E"):
		return &MetricConfig{IsSpecial: true, SpecialType: "PE"}
	case strings.Contains(metricName, "Долг") && !strings.Contains(metricName, "Чистый"):
		return &MetricConfig{IsSpecial: true, SpecialType: "DEBT"}
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

func (p *CSVParser) processMetricRow(config *MetricConfig, quarters []string, record []string, companyName string,
	category string) ([]models.QuarterData, error) {

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

		value, err := p.parseValue(record[colIdx])
		if err != nil {
			continue
		}

		data := models.QuarterData{
			Year:     year,
			Quarter:  quarter,
			Company:  companyName,
			Category: category,
		}

		p.applyMetricValue(config, &data, value)

		if !data.IsEmpty() {
			results = append(results, data)
		}
	}

	return results, nil
}

func (p *CSVParser) parseValue(valueStr string) (float64, error) {
	valueStr = strings.TrimSpace(valueStr)
	valueStr = strings.ReplaceAll(valueStr, ",", ".")
	valueStr = strings.ReplaceAll(valueStr, " ", "")
	valueStr = strings.ReplaceAll(valueStr, "\"", "")
	valueStr = strings.ReplaceAll(valueStr, "%", "")

	if valueStr == "" || valueStr == "-" || valueStr == "0.00" {
		return 0, fmt.Errorf("empty or invalid value")
	}

	return strconv.ParseFloat(valueStr, 64)
}

func (p *CSVParser) applyMetricValue(config *MetricConfig, data *models.QuarterData, value float64) {
	if config.IsSpecial {
		switch config.SpecialType {
		case "PE":
			data.PE = value
		case "DEBT":
			data.Debt = value
		case "NET_PROFIT":
			data.NetProfit = value
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
