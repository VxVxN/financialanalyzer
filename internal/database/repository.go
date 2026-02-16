package database

import (
	"database/sql"

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
    INSERT INTO company_financials (year, quarter, company, capitalization, revenue, net_profit, ebitda, debt, pe, roe)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
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
