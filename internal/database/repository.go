package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// ErrCompanyNotFound is returned by DeleteCompany when no rows match. Callers
// match it with errors.Is rather than string-matching the error text.
var ErrCompanyNotFound = errors.New("company not found")

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Ping verifies the database is reachable. Used by the readiness probe.
func (r *Repository) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

func (r *Repository) SaveQuarterData(ctx context.Context, data models.QuarterData) error {
	query := `
    INSERT INTO company_financials (year, quarter, company, category, capitalization, revenue, net_profit, ebitda, debt, pe, roe, source, equity, dividends)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
    ON CONFLICT (year, quarter, company) 
    DO UPDATE SET
        source = COALESCE(EXCLUDED.source, company_financials.source),
        capitalization = COALESCE(EXCLUDED.capitalization, company_financials.capitalization),
        revenue = COALESCE(EXCLUDED.revenue, company_financials.revenue),
        net_profit = COALESCE(EXCLUDED.net_profit, company_financials.net_profit),
        ebitda = COALESCE(EXCLUDED.ebitda, company_financials.ebitda),
        debt = COALESCE(EXCLUDED.debt, company_financials.debt),
        pe = COALESCE(EXCLUDED.pe, company_financials.pe),
        roe = COALESCE(EXCLUDED.roe, company_financials.roe),
        equity = COALESCE(EXCLUDED.equity, company_financials.equity),
        dividends = COALESCE(EXCLUDED.dividends, company_financials.dividends)`

	_, err := r.db.ExecContext(ctx, query,
		data.Year,
		data.Quarter,
		data.Company,
		data.Category,
		data.Capitalization,
		data.Revenue,
		data.NetProfit,
		data.EBITDA,
		data.Debt,
		data.PE,
		data.ROE,
		nullIfEmpty(data.Source),
		data.Equity,
		data.Dividends,
	)

	return err
}

func nullIfEmpty(val string) interface{} {
	if val == "" {
		return nil
	}
	return val
}

// ExistingPeriods returns the set of "YEAR-QUARTER" keys already stored for a
// company (e.g. "2023-Q4"), letting importers skip periods they have already
// ingested instead of re-fetching them. The set is empty for an unknown company.
func (r *Repository) ExistingPeriods(ctx context.Context, company string) (map[string]struct{}, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT year, quarter FROM company_financials WHERE company = $1`, company)
	if err != nil {
		return nil, fmt.Errorf("query existing periods for %s: %w", company, err)
	}
	defer rows.Close()

	out := make(map[string]struct{})
	for rows.Next() {
		var (
			year    int
			quarter string
		)
		if err := rows.Scan(&year, &quarter); err != nil {
			return nil, fmt.Errorf("scan existing period: %w", err)
		}
		out[fmt.Sprintf("%d-%s", year, quarter)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return out, nil
}

func (r *Repository) GetCompanyHistory(ctx context.Context, company string) ([]models.QuarterData, error) {
	query := `
		SELECT year, quarter, company, COALESCE(category, ''),
			capitalization, revenue, net_profit, ebitda, debt, pe, roe,
			COALESCE(source, ''), equity, dividends
		FROM company_financials
		WHERE company = $1
		ORDER BY year,
			CASE quarter
				WHEN 'Q1' THEN 1
				WHEN 'Q2' THEN 2
				WHEN 'Q3' THEN 3
				WHEN 'Q4' THEN 4
			END
	`

	rows, err := r.db.QueryContext(ctx, query, company)
	if err != nil {
		return nil, fmt.Errorf("failed to query company history: %w", err)
	}
	defer rows.Close()

	var out []models.QuarterData
	for rows.Next() {
		var q models.QuarterData
		if err := rows.Scan(
			&q.Year, &q.Quarter, &q.Company, &q.Category,
			&q.Capitalization, &q.Revenue, &q.NetProfit,
			&q.EBITDA, &q.Debt, &q.PE, &q.ROE, &q.Source,
			&q.Equity, &q.Dividends,
		); err != nil {
			return nil, fmt.Errorf("failed to scan history row: %w", err)
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return out, nil
}

func (r *Repository) GetCompaniesHistory(ctx context.Context, companies []string) (map[string][]models.QuarterData, error) {
	if len(companies) == 0 {
		return map[string][]models.QuarterData{}, nil
	}
	placeholders := make([]string, len(companies))
	args := make([]interface{}, len(companies))
	for i, c := range companies {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = c
	}
	query := fmt.Sprintf(`
		SELECT year, quarter, company, COALESCE(category, ''),
			capitalization, revenue, net_profit, ebitda, debt, pe, roe,
			COALESCE(source, ''), equity, dividends
		FROM company_financials
		WHERE company IN (%s)
		ORDER BY company, year,
			CASE quarter
				WHEN 'Q1' THEN 1
				WHEN 'Q2' THEN 2
				WHEN 'Q3' THEN 3
				WHEN 'Q4' THEN 4
			END
	`, strings.Join(placeholders, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query companies history: %w", err)
	}
	defer rows.Close()

	out := make(map[string][]models.QuarterData, len(companies))
	for rows.Next() {
		var q models.QuarterData
		if err := rows.Scan(
			&q.Year, &q.Quarter, &q.Company, &q.Category,
			&q.Capitalization, &q.Revenue, &q.NetProfit,
			&q.EBITDA, &q.Debt, &q.PE, &q.ROE, &q.Source,
			&q.Equity, &q.Dividends,
		); err != nil {
			return nil, fmt.Errorf("failed to scan history row: %w", err)
		}
		out[q.Company] = append(out[q.Company], q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return out, nil
}

func (r *Repository) GetAllCompanies(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
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

func (r *Repository) GetAllCategories(ctx context.Context) ([]string, error) {
	query := `SELECT DISTINCT category FROM company_financials WHERE category IS NOT NULL AND category != '' ORDER BY category`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("error getting categories: %w", err)
	}
	defer rows.Close()

	var categories []string
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			return nil, fmt.Errorf("error scanning category: %w", err)
		}
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating categories: %w", err)
	}

	return categories, nil
}

type CompanyWithCategory struct {
	Company  string `json:"company"`
	Category string `json:"category"`
}

func (r *Repository) GetAllCompaniesWithCategories(ctx context.Context) ([]CompanyWithCategory, error) {
	query := `SELECT DISTINCT company, category FROM company_financials ORDER BY company`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("error getting companies with categories: %w", err)
	}
	defer rows.Close()

	var companies []CompanyWithCategory
	for rows.Next() {
		var c CompanyWithCategory
		if err := rows.Scan(&c.Company, &c.Category); err != nil {
			return nil, fmt.Errorf("error scanning company with category: %w", err)
		}
		companies = append(companies, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating companies with categories: %w", err)
	}

	return companies, nil
}

func (r *Repository) DeleteCompany(ctx context.Context, company string) error {
	query := `DELETE FROM company_financials WHERE company = $1`

	result, err := r.db.ExecContext(ctx, query, company)
	if err != nil {
		return fmt.Errorf("error deleting company %s: %w", company, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("%w: %s", ErrCompanyNotFound, company)
	}

	return nil
}

type CompanyNote struct {
	Company   string    `json:"company"`
	Note      string    `json:"note"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`
}

func (r *Repository) GetCompanyNote(ctx context.Context, company string) (string, error) {
	query := `SELECT note FROM company_notes WHERE company = $1`

	var note sql.NullString
	err := r.db.QueryRowContext(ctx, query, company).Scan(&note)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("error getting company note: %w", err)
	}

	if note.Valid {
		return note.String, nil
	}
	return "", nil
}

func (r *Repository) SaveCompanyNote(ctx context.Context, company, note string) error {
	query := `
        INSERT INTO company_notes (company, note, updated_at)
        VALUES ($1, $2, CURRENT_TIMESTAMP)
        ON CONFLICT (company) 
        DO UPDATE SET
            note = EXCLUDED.note,
            updated_at = CURRENT_TIMESTAMP
    `

	_, err := r.db.ExecContext(ctx, query, company, note)
	if err != nil {
		return fmt.Errorf("error saving company note: %w", err)
	}

	return nil
}

func (r *Repository) DeleteCompanyNote(ctx context.Context, company string) error {
	query := `DELETE FROM company_notes WHERE company = $1`

	_, err := r.db.ExecContext(ctx, query, company)
	if err != nil {
		return fmt.Errorf("error deleting company note: %w", err)
	}

	return nil
}
