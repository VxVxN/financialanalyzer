package database_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

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
	if _, err := db.Exec(`TRUNCATE company_financials, company_notes, market_quotes, fetch_runs, manual_financials, cheap_list_sends`); err != nil {
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
		Revenue: f(100), NetProfit: f(10), Debt: f(0), Equity: f(250.5), Dividends: f(0),
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	got := history(t, repo, "ZERO")
	assertMetric(t, "debt", got.Debt, f(0))
	assertMetric(t, "ebitda", got.EBITDA, nil)
	assertMetric(t, "revenue", got.Revenue, f(100))
	assertMetric(t, "equity", got.Equity, f(250.5))
	assertMetric(t, "dividends", got.Dividends, f(0))
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

func TestMarketQuotes(t *testing.T) {
	repo := openTestDB(t)
	ctx := context.Background()

	if _, ok, err := repo.GetMarketQuote(ctx, "SBER"); err != nil || ok {
		t.Fatalf("missing quote: ok=%v err=%v", ok, err)
	}
	day := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	for _, q := range []models.MarketQuote{
		{Company: "SBER", Price: 270, Capitalization: 5800, PriceDate: day(28)},
		{Company: "SBER", Price: 274.65, Capitalization: 5928.85, Shares: 21586948000, PriceDate: day(29)}, // replaces
	} {
		if err := repo.SaveMarketQuote(ctx, q); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	q, ok, err := repo.GetMarketQuote(ctx, "SBER")
	if err != nil || !ok || q.Price != 274.65 || q.Capitalization != 5928.85 || q.Shares != 21586948000 || !q.PriceDate.Equal(day(29)) {
		t.Fatalf("quote = %+v ok=%v err=%v", q, ok, err)
	}
	all, err := repo.GetMarketQuotes(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("quotes = %v err=%v", all, err)
	}

	// Deleting the company drops its quote too.
	if err := repo.SaveQuarterData(ctx, models.QuarterData{Year: 2025, Quarter: "Q4", Company: "SBER",
		Category: "banks", NetProfit: models.Float(1)}); err != nil {
		t.Fatalf("save row: %v", err)
	}
	if err := repo.DeleteCompany(ctx, "SBER"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := repo.GetMarketQuote(ctx, "SBER"); ok {
		t.Error("quote survived DeleteCompany")
	}
	// An orphaned quote (no financial rows) can still be deleted.
	_ = repo.SaveMarketQuote(ctx, models.MarketQuote{Company: "GAZP", Price: 1, Capitalization: 1, PriceDate: day(29)})
	if err := repo.DeleteCompany(ctx, "GAZP"); err != nil {
		t.Errorf("delete orphan quote: %v", err)
	}
	if _, ok, _ := repo.GetMarketQuote(ctx, "GAZP"); ok {
		t.Error("orphaned quote survived DeleteCompany")
	}
	// A company with neither rows nor quote is not found.
	if err := repo.DeleteCompany(ctx, "NOPE"); !errors.Is(err, database.ErrCompanyNotFound) {
		t.Errorf("delete unknown: err = %v, want ErrCompanyNotFound", err)
	}
}

func TestSaveQuarterDataSourceSemantics(t *testing.T) {
	repo := openTestDB(t)
	ctx := context.Background()
	f := models.Float
	save := func(q models.QuarterData) {
		t.Helper()
		q.Year, q.Quarter, q.Category = 2024, "Q4", "test"
		if err := repo.SaveQuarterData(ctx, q); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	// Annual RSBU figures, then a CSV row carrying only dividends: the row
	// must stay "rsbu" so its flows keep meaning "the whole year".
	save(models.QuarterData{Company: "ANNUAL", Source: models.SourceRSBU, Revenue: f(100), NetProfit: f(10)})
	save(models.QuarterData{Company: "ANNUAL", Source: models.SourceCSV, Dividends: f(4)})
	got := history(t, repo, "ANNUAL")
	if got.Source != models.SourceRSBU {
		t.Errorf("source = %q, want rsbu (a dividends-only row must not relabel flows)", got.Source)
	}
	assertMetric(t, "dividends", got.Dividends, f(4))
	assertMetric(t, "revenue", got.Revenue, f(100))

	// Quarterly CSV flows, then RSBU annual flows without EBITDA: the source
	// switches and the CSV single-quarter flows are replaced, not merged.
	save(models.QuarterData{Company: "SWITCH", Source: models.SourceCSV, Revenue: f(30), NetProfit: f(3), EBITDA: f(6), Debt: f(50)})
	save(models.QuarterData{Company: "SWITCH", Source: models.SourceRSBU, Revenue: f(120), NetProfit: f(12)})
	got = history(t, repo, "SWITCH")
	if got.Source != models.SourceRSBU {
		t.Errorf("source = %q, want rsbu", got.Source)
	}
	assertMetric(t, "revenue", got.Revenue, f(120))
	assertMetric(t, "ebitda", got.EBITDA, nil) // quarterly EBITDA dropped
	assertMetric(t, "debt", got.Debt, f(50))   // stock metrics still merge

	// Quarterly-to-quarterly source changes (smartlab -> csv, legacy NULL ->
	// csv) merge: a partial CSV must not wipe the other flows.
	save(models.QuarterData{Company: "MERGEQ", Source: models.SourceSmartLab, Revenue: f(30), NetProfit: f(3), EBITDA: f(6)})
	save(models.QuarterData{Company: "MERGEQ", Source: models.SourceCSV, Revenue: f(31)})
	got = history(t, repo, "MERGEQ")
	assertMetric(t, "revenue", got.Revenue, f(31))
	assertMetric(t, "net_profit", got.NetProfit, f(3))
	assertMetric(t, "ebitda", got.EBITDA, f(6))
	save(models.QuarterData{Company: "LEGACY", Revenue: f(30), NetProfit: f(3)}) // NULL source
	save(models.QuarterData{Company: "LEGACY", Source: models.SourceCSV, Revenue: f(32)})
	got = history(t, repo, "LEGACY")
	assertMetric(t, "net_profit", got.NetProfit, f(3))

	// Annual RSBU -> quarterly CSV flows: annual flows and the P/E/ROE built
	// on them go; point-in-time figures stay.
	save(models.QuarterData{Company: "FLIP", Source: models.SourceRSBU, Revenue: f(120), NetProfit: f(12),
		PE: f(10), ROE: f(20), Capitalization: f(120)})
	save(models.QuarterData{Company: "FLIP", Source: models.SourceCSV, Revenue: f(33)})
	got = history(t, repo, "FLIP")
	assertMetric(t, "revenue", got.Revenue, f(33))
	assertMetric(t, "net_profit", got.NetProfit, nil)
	assertMetric(t, "pe", got.PE, nil)
	assertMetric(t, "roe", got.ROE, nil)
	assertMetric(t, "capitalization", got.Capitalization, f(120))

	// Same source again (FETCH_FORCE re-fetch): flows merge as before.
	save(models.QuarterData{Company: "SWITCH", Source: models.SourceRSBU, NetProfit: f(13)})
	got = history(t, repo, "SWITCH")
	assertMetric(t, "revenue", got.Revenue, f(120))
	assertMetric(t, "net_profit", got.NetProfit, f(13))
}

func TestFetchRuns(t *testing.T) {
	repo := openTestDB(t)
	ctx := context.Background()
	base := time.Now().Add(-48 * time.Hour).Truncate(time.Second)

	start := func(kind string, full bool, at time.Time) models.FetchRun {
		t.Helper()
		run := models.FetchRun{Kind: kind, Trigger: "cli", Scope: "stored", FullScope: full, Status: models.RunRunning, StartedAt: at}
		id, err := repo.StartFetchRun(ctx, run)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		run.ID = id
		return run
	}
	finish := func(run models.FetchRun, status string) {
		t.Helper()
		end := run.StartedAt.Add(time.Minute)
		run.Status, run.FinishedAt = status, &end
		run.Updated, run.Rows, run.QuotesSaved = 2, 5, 7
		run.Failed, run.QuotesFailed = []string{"LKOH", "MGNT"}, nil
		if err := repo.FinishFetchRun(ctx, run); err != nil {
			t.Fatalf("finish: %v", err)
		}
	}

	if _, ok, err := repo.LastCompletedRun(ctx, []string{"quotes"}); err != nil || ok {
		t.Fatalf("empty table: ok=%v err=%v", ok, err)
	}

	finish(start("quotes", true, base), models.RunOK)
	finish(start("financials", true, base.Add(time.Hour)), models.RunPartial)
	finish(start("quotes", false, base.Add(2*time.Hour)), models.RunOK)    // subset: not full scope
	finish(start("quotes", true, base.Add(3*time.Hour)), models.RunFailed) // not completed
	stale := start("financials", true, base.Add(4*time.Hour))              // process died
	fresh := start("quotes", true, time.Now().Add(-time.Minute))           // still running

	got, ok, err := repo.LastCompletedRun(ctx, []string{"quotes"})
	if err != nil || !ok || !got.Equal(base) {
		t.Errorf("last quotes run = %v,%v,%v; want %v", got, ok, err, base)
	}
	got, _, _ = repo.LastCompletedRun(ctx, []string{"quotes", "financials"})
	if !got.Equal(base.Add(time.Hour)) {
		t.Errorf("last quotes|financials run = %v, want %v", got, base.Add(time.Hour))
	}

	if n, err := repo.AbandonStaleRuns(ctx, time.Hour, nil); err != nil || n != 1 {
		t.Errorf("abandoned = %d, %v; want 1", n, err)
	}
	// A recent run of one of the given triggers goes too; others stay.
	if n, err := repo.AbandonStaleRuns(ctx, time.Hour, []string{"schedule"}); err != nil || n != 0 {
		t.Errorf("abandoned by trigger = %d, %v; want 0 (fresh run is cli)", n, err)
	}

	runs, err := repo.RecentFetchRuns(ctx, 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(runs) != 6 || runs[0].ID != fresh.ID || runs[0].Status != models.RunRunning || runs[0].FinishedAt != nil {
		t.Fatalf("recent = %+v", runs)
	}
	if runs[1].ID != stale.ID || runs[1].Status != models.RunAbandoned {
		t.Errorf("stale run = %+v", runs[1])
	}
	last := runs[5]
	if last.Kind != "quotes" || !last.FullScope || last.Duration() != time.Minute || last.Rows != 5 || last.QuotesSaved != 7 ||
		strings.Join(last.Failed, ",") != "LKOH,MGNT" || last.QuotesFailed != nil {
		t.Errorf("oldest run = %+v", last)
	}
	if runs, _ := repo.RecentFetchRuns(ctx, 2); len(runs) != 2 {
		t.Errorf("limit ignored: %d runs", len(runs))
	}
}

// The cash-flow columns round-trip; operating profit / cash flow / capex are
// flows (a CSV row carrying only one of them flips an annual RSBU row and
// replaces the others), cash is a stock that merges.
func TestSaveQuarterDataCashFlowColumns(t *testing.T) {
	repo := openTestDB(t)
	ctx := context.Background()
	f := models.Float
	save := func(q models.QuarterData) {
		t.Helper()
		q.Year, q.Quarter, q.Category = 2025, "Q4", "test"
		if err := repo.SaveQuarterData(ctx, q); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	save(models.QuarterData{Company: "CF", Source: models.SourceRSBU, Revenue: f(100), NetProfit: f(10), Debt: f(0),
		Cash: f(7.5), OperatingProfit: f(15), OperatingCashFlow: f(20), Capex: f(0)})
	got := history(t, repo, "CF")
	assertMetric(t, "cash", got.Cash, f(7.5))
	assertMetric(t, "operating_profit", got.OperatingProfit, f(15))
	assertMetric(t, "operating_cash_flow", got.OperatingCashFlow, f(20))
	assertMetric(t, "capex", got.Capex, f(0)) // a reported zero, not NULL

	// A CSV row with only quarterly operating cash flow: the row turns
	// quarterly, so the annual flows go, while cash (a stock) stays.
	save(models.QuarterData{Company: "CF", Source: models.SourceCSV, OperatingCashFlow: f(6)})
	got = history(t, repo, "CF")
	if got.Source != models.SourceCSV {
		t.Errorf("source = %q, want csv", got.Source)
	}
	assertMetric(t, "operating_cash_flow", got.OperatingCashFlow, f(6))
	assertMetric(t, "operating_profit", got.OperatingProfit, nil)
	assertMetric(t, "capex", got.Capex, nil)
	assertMetric(t, "revenue", got.Revenue, nil)
	assertMetric(t, "cash", got.Cash, f(7.5))

	// A CSV row with only cash (a stock) neither flips nor relabels an annual
	// RSBU row.
	save(models.QuarterData{Company: "STOCK", Source: models.SourceRSBU, Revenue: f(100), OperatingCashFlow: f(20)})
	save(models.QuarterData{Company: "STOCK", Source: models.SourceCSV, Cash: f(9)})
	got = history(t, repo, "STOCK")
	if got.Source != models.SourceRSBU {
		t.Errorf("source = %q, want rsbu (a cash-only row carries no flows)", got.Source)
	}
	assertMetric(t, "operating_cash_flow", got.OperatingCashFlow, f(20))
	assertMetric(t, "cash", got.Cash, f(9))
	if got.CashSource != models.SourceCSV || got.DebtSource != "" {
		t.Errorf("cash source = %q, debt source = %q; want csv and empty", got.CashSource, got.DebtSource)
	}

	// CSV debt merged into an RSBU row keeps the parent's cash source, so the
	// two columns no longer claim to be one balance sheet.
	save(models.QuarterData{Company: "MIX", Source: models.SourceRSBU, Revenue: f(100), Debt: f(40), Cash: f(5)})
	save(models.QuarterData{Company: "MIX", Source: models.SourceCSV, Debt: f(80)})
	got = history(t, repo, "MIX")
	if got.Source != models.SourceRSBU || got.DebtSource != models.SourceCSV || got.CashSource != models.SourceRSBU {
		t.Errorf("MIX source = %q debt_source = %q cash_source = %q", got.Source, got.DebtSource, got.CashSource)
	}
	assertMetric(t, "debt", got.Debt, f(80))
	assertMetric(t, "cash", got.Cash, f(5))

	// Quarterly CSV flows, then annual RSBU ones: the CSV quarter's capex and
	// operating profit go (the RSBU write did not carry them).
	save(models.QuarterData{Company: "UP", Source: models.SourceCSV, Revenue: f(25), OperatingProfit: f(4), Capex: f(2)})
	save(models.QuarterData{Company: "UP", Source: models.SourceRSBU, Revenue: f(100), OperatingCashFlow: f(30)})
	got = history(t, repo, "UP")
	assertMetric(t, "operating_cash_flow", got.OperatingCashFlow, f(30))
	assertMetric(t, "operating_profit", got.OperatingProfit, nil)
	assertMetric(t, "capex", got.Capex, nil)
}

func TestManualFinancials(t *testing.T) {
	repo := openTestDB(t)
	ctx := context.Background()
	f := models.Float

	if err := repo.SaveQuarterData(ctx, models.QuarterData{Year: 2025, Quarter: "Q4", Company: "X5", Category: "retail",
		Source: models.SourceRSBU, Capitalization: f(800), Revenue: f(85), NetProfit: f(124), Debt: f(119)}); err != nil {
		t.Fatalf("save fetched: %v", err)
	}
	if err := repo.SaveManualFinancials(ctx, models.ManualFinancials{Company: "NOPE", Year: 2025, Dividends: f(1)}); !errors.Is(err, database.ErrCompanyNotFound) {
		t.Errorf("entry for an unknown company: %v, want ErrCompanyNotFound", err)
	}

	entry := models.ManualFinancials{Company: "X5", Year: 2025, Revenue: f(4000), NetProfit: f(100), Dividends: f(0)}
	if err := repo.SaveManualFinancials(ctx, entry); err != nil {
		t.Fatalf("save manual: %v", err)
	}
	got := history(t, repo, "X5")
	if got.Source != models.SourceManual {
		t.Errorf("source = %q, want manual", got.Source)
	}
	assertMetric(t, "revenue", got.Revenue, f(4000))
	assertMetric(t, "debt", got.Debt, nil) // the fetched RSBU debt is replaced
	assertMetric(t, "dividends", got.Dividends, f(0))
	assertMetric(t, "pe", got.PE, f(8))
	all, err := repo.GetCompaniesHistory(ctx, []string{"X5"})
	if err != nil || len(all["X5"]) != 1 || all["X5"][0].Source != models.SourceManual {
		t.Errorf("GetCompaniesHistory = %+v, %v", all, err)
	}

	// A later save replaces the entry entirely; a fetch run never touches it.
	entry.Revenue, entry.Dividends = nil, nil
	entry.NetProfit = f(200)
	if err := repo.SaveManualFinancials(ctx, entry); err != nil {
		t.Fatalf("resave manual: %v", err)
	}
	if err := repo.SaveQuarterData(ctx, models.QuarterData{Year: 2025, Quarter: "Q4", Company: "X5",
		Source: models.SourceRSBU, Revenue: f(90)}); err != nil {
		t.Fatal(err)
	}
	list, err := repo.GetManualFinancials(ctx, "X5")
	if err != nil || len(list) != 1 || list[0].Revenue != nil || *list[0].NetProfit != 200 || list[0].UpdatedAt.IsZero() {
		t.Fatalf("manual list = %+v, %v", list, err)
	}
	assertMetric(t, "manual revenue survives the fetch", history(t, repo, "X5").Revenue, nil)

	// Deleting the entry brings the fetched figures back.
	if err := repo.DeleteManualFinancials(ctx, "X5", 2025); err != nil {
		t.Fatalf("delete manual: %v", err)
	}
	got = history(t, repo, "X5")
	if got.Source != models.SourceRSBU {
		t.Errorf("source after delete = %q, want rsbu", got.Source)
	}
	assertMetric(t, "revenue", got.Revenue, f(90))
	if err := repo.DeleteManualFinancials(ctx, "X5", 2025); !errors.Is(err, database.ErrManualNotFound) {
		t.Errorf("second delete: %v, want ErrManualNotFound", err)
	}

	// Deleting the company removes its manual entries too.
	if err := repo.SaveManualFinancials(ctx, models.ManualFinancials{Company: "X5", Year: 2024, Dividends: f(5)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteCompany(ctx, "X5"); err != nil {
		t.Fatalf("delete company: %v", err)
	}
	if list, _ := repo.GetManualFinancials(ctx, "X5"); len(list) != 0 {
		t.Errorf("manual entries left after DeleteCompany: %+v", list)
	}
}

// TestLockFetch checks the cross-process fetch lock with two pools standing in
// for the fetch pipelines and cmd/plot.
func TestLockFetch(t *testing.T) {
	a := openTestDB(t)
	b := openTestDB(t)
	ctx := context.Background()

	releaseA, err := a.LockFetch(ctx, func() { t.Error("first lock waited") })
	if err != nil {
		t.Fatalf("lock A: %v", err)
	}

	// A canceled wait gives up and leaves no lock behind.
	tctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	_, err = b.LockFetch(tctx, nil)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock B while held = %v, want the deadline", err)
	}

	waiting := make(chan struct{})
	got := make(chan func(), 1)
	go func() {
		release, err := b.LockFetch(ctx, func() { close(waiting) })
		if err != nil {
			t.Errorf("lock B: %v", err)
		}
		got <- release
	}()
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("B did not wait for the lock A holds")
	}
	select {
	case <-got:
		t.Fatal("B took the lock while A held it")
	case <-time.After(200 * time.Millisecond):
	}

	releaseA()
	var releaseB func()
	select {
	case releaseB = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("B did not get the lock after A released it")
	}
	if releaseB == nil {
		t.FailNow()
	}
	releaseB()

	// Released for good, the canceled wait included: no advisory lock is
	// left in the database, and A takes it again without waiting.
	raw, err := sql.Open("postgres", os.Getenv("TEST_DATABASE_DSN"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer raw.Close()
	var held int
	if err := raw.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND granted`).Scan(&held); err != nil || held != 0 {
		t.Errorf("advisory locks held after release = %d (err %v), want 0", held, err)
	}
	release, err := a.LockFetch(ctx, func() { t.Error("lock waited after both releases") })
	if err != nil {
		t.Fatalf("relock: %v", err)
	}
	release()
}

func TestCheapListSent(t *testing.T) {
	repo := openTestDB(t)
	ctx := context.Background()
	monday := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	sent, err := repo.CheapListSent(ctx, monday)
	if err != nil || sent {
		t.Fatalf("before send: sent=%v err=%v", sent, err)
	}
	if err := repo.MarkCheapListSent(ctx, monday); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := repo.MarkCheapListSent(ctx, monday); err != nil {
		t.Fatalf("mark again: %v", err)
	}
	sent, err = repo.CheapListSent(ctx, monday)
	if err != nil || !sent {
		t.Fatalf("after send: sent=%v err=%v", sent, err)
	}
	other := time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)
	if sent, err = repo.CheapListSent(ctx, other); err != nil || sent {
		t.Fatalf("other Monday: sent=%v err=%v", sent, err)
	}
}
