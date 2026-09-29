package database_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"

	financialanalyzer "github.com/VxVxN/financialanalyzer"
	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

// openTestDB connects to the Postgres named by TEST_DATABASE_DSN, applies the
// migrations and empties the tables. The test is skipped when the variable is
// unset, so `go test ./...` needs no database. The DSN must point at a
// throwaway database: its tables are truncated.
func openTestDB(t *testing.T) *database.Repository {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set; skipping Postgres integration test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.RunMigrations(db, financialanalyzer.MigrationsFS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec(`TRUNCATE company_financials, company_notes`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return database.NewRepository(db)
}

func history(t *testing.T, repo *database.Repository, company string) models.QuarterData {
	t.Helper()
	h, err := repo.GetCompanyHistory(context.Background(), company)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(h) != 1 {
		t.Fatalf("history rows = %d, want 1", len(h))
	}
	return h[0]
}

func assertMetric(t *testing.T, name string, got *float64, want *float64) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s = %v, want %v", name, deref(got), deref(want))
	case *got != *want:
		t.Errorf("%s = %v, want %v", name, *got, *want)
	}
}

func deref(p *float64) any {
	if p == nil {
		return "nil"
	}
	return *p
}

func TestSaveQuarterDataZeroVersusNull(t *testing.T) {
	repo := openTestDB(t)
	ctx := context.Background()
	f := models.Float

	// Debt is a reported zero; EBITDA is not reported at all.
	err := repo.SaveQuarterData(ctx, models.QuarterData{
		Year: 2024, Quarter: "Q4", Company: "ZERO", Category: "test", Source: models.SourceRSBU,
		Revenue: f(100), NetProfit: f(10), Debt: f(0),
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	got := history(t, repo, "ZERO")
	assertMetric(t, "debt", got.Debt, f(0))
	assertMetric(t, "ebitda", got.EBITDA, nil)
	assertMetric(t, "revenue", got.Revenue, f(100))
	if got.Source != models.SourceRSBU {
		t.Errorf("source = %q, want %q", got.Source, models.SourceRSBU)
	}
}

func TestSaveQuarterDataMergesColumns(t *testing.T) {
	repo := openTestDB(t)
	ctx := context.Background()
	f := models.Float
	base := models.QuarterData{Year: 2024, Quarter: "Q4", Company: "MERGE", Category: "test"}

	first := base
	first.Source = models.SourceCSV
	first.Revenue, first.EBITDA, first.Debt = f(100), f(30), f(50)
	if err := repo.SaveQuarterData(ctx, first); err != nil {
		t.Fatalf("save first: %v", err)
	}

	// A later partial write: unset metrics must keep their stored values, a
	// non-nil zero must overwrite, and an empty source keeps the old label.
	second := base
	second.NetProfit, second.Debt = f(12), f(0)
	if err := repo.SaveQuarterData(ctx, second); err != nil {
		t.Fatalf("save second: %v", err)
	}

	got := history(t, repo, "MERGE")
	assertMetric(t, "revenue", got.Revenue, f(100))
	assertMetric(t, "ebitda", got.EBITDA, f(30))
	assertMetric(t, "net_profit", got.NetProfit, f(12))
	assertMetric(t, "debt", got.Debt, f(0))
	assertMetric(t, "pe", got.PE, nil)
	if got.Source != models.SourceCSV {
		t.Errorf("source = %q, want %q (kept)", got.Source, models.SourceCSV)
	}
}

func TestRepositoryHonoursCanceledContext(t *testing.T) {
	repo := openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repo.GetAllCompanies(ctx); err == nil {
		t.Fatal("GetAllCompanies with a canceled context: want error, got nil")
	}
}
