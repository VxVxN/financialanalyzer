package database

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) SaveQuarterData(data models.QuarterData) error {
	query := `
    INSERT INTO company_financials (year, quarter, company, category, capitalization, revenue, net_profit, ebitda, debt, pe, roe)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
    ON CONFLICT (year, quarter, company) 
    DO UPDATE SET
        capitalization = COALESCE(EXCLUDED.capitalization, company_financials.capitalization),
        revenue = COALESCE(EXCLUDED.revenue, company_financials.revenue),
        net_profit = COALESCE(EXCLUDED.net_profit, company_financials.net_profit),
        ebitda = COALESCE(EXCLUDED.ebitda, company_financials.ebitda),
        debt = COALESCE(EXCLUDED.debt, company_financials.debt),
        pe = COALESCE(EXCLUDED.pe, company_financials.pe),
        roe = COALESCE(EXCLUDED.roe, company_financials.roe)`

	_, err := r.db.Exec(query,
		data.Year,
		data.Quarter,
		data.Company,
		data.Category,
		nullIfZero(data.Capitalization),
		nullIfZero(data.Revenue),
		nullIfZero(data.NetProfit),
		nullIfZero(data.EBITDA),
		nullIfZero(data.Debt),
		nullIfZero(data.PE),
		nullIfZero(data.ROE),
	)

	return err
}

func nullIfZero(val float64) interface{} {
	if val == 0 {
		return nil
	}
	return val
}

type CompanyMetric struct {
	Year    int
	Quarter string
	Company string
	Value   float64
}

func (r *Repository) GetCompaniesMetric(companies []string, metric string) ([]CompanyMetric, error) {
	placeholders := make([]string, len(companies))
	args := make([]interface{}, len(companies))
	for i, company := range companies {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = company
	}

	query := fmt.Sprintf(`
		SELECT year, quarter, company, %s as value
		FROM company_financials
		WHERE company IN (%s)
		ORDER BY year, 
			CASE quarter
				WHEN 'Q1' THEN 1
				WHEN 'Q2' THEN 2
				WHEN 'Q3' THEN 3
				WHEN 'Q4' THEN 4
			END
	`, metric, strings.Join(placeholders, ","))

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query metric %s: %w", metric, err)
	}
	defer rows.Close()

	var result []CompanyMetric
	for rows.Next() {
		var item CompanyMetric
		var value sql.NullFloat64

		if err := rows.Scan(&item.Year, &item.Quarter, &item.Company, &value); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		if value.Valid {
			item.Value = value.Float64
		} else {
			item.Value = 0
		}

		result = append(result, item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return result, nil
}

func (r *Repository) GetAllCompanies() ([]string, error) {
	rows, err := r.db.Query(`
		SELECT DISTINCT company 
		FROM company_financials 
		WHERE company IS NOT NULL AND company != ''
		ORDER BY company
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var companies []string
	for rows.Next() {
		var company string
		if err := rows.Scan(&company); err != nil {
			return nil, err
		}
		companies = append(companies, company)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return companies, nil
}
