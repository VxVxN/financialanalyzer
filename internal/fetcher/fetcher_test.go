package fetcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
	"github.com/VxVxN/financialanalyzer/internal/scraper/cbr"
	"github.com/VxVxN/financialanalyzer/internal/scraper/moex"
)

func TestResolveSpec(t *testing.T) {
	registry := map[string]tickerSpec{
		"LKOH": {"LKOH", "7708004767", "oil"},
	}

	tests := []struct {
		name string
		in   string
		want tickerSpec
		ok   bool
	}{
		{"colon full", "LKOH:7708004767:oil", tickerSpec{"LKOH", "7708004767", "oil"}, true},
		{"space full", "lkoh  7708004767  oil", tickerSpec{"LKOH", "7708004767", "oil"}, true},
		{"no category", "MTSS 7740000076", tickerSpec{"MTSS", "7740000076", ""}, true},
		{"multiword category", "AFLT 7712040126 air transport", tickerSpec{"AFLT", "7712040126", "air transport"}, true},
		{"unknown ticker, no inn: left for the lookup", "ZZZZ:metals", tickerSpec{"ZZZZ", "", "metals"}, true},
		{"bare unknown ticker", "ZZZZ", tickerSpec{"ZZZZ", "", ""}, true},
		{"empty entry", " ", tickerSpec{}, false},
		{"registry fills inn+category", "LKOH", tickerSpec{"LKOH", "7708004767", "oil"}, true},
		{"registry inn, overridden category", "LKOH:energy", tickerSpec{"LKOH", "7708004767", "energy"}, true},
		{"explicit inn overrides registry", "LKOH:1234567890", tickerSpec{"LKOH", "1234567890", "oil"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolveSpec(splitFields(tt.in), registry)
			if ok != tt.ok || got != tt.want {
				t.Errorf("resolveSpec(%q) = %+v,%v; want %+v,%v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestLoadRegistry parses the embedded fetch_tickers.txt and checks that inline
// "# ..." comments are stripped and every row is well-formed.
func TestLoadRegistry(t *testing.T) {
	registry, err := loadRegistry()
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(registry) == 0 {
		t.Fatal("expected the bundled registry to have entries, got none")
	}

	for _, s := range registry {
		if strings.ContainsAny(s.Category, "#\"") || strings.Contains(s.Category, "ПАО") {
			t.Errorf("category for %s looks like it absorbed a comment: %q", s.Ticker, s.Category)
		}
		if len(s.INN) < 10 { // RU INN is 10 (orgs) or 12 (individuals) digits
			t.Errorf("%s has implausible INN %q", s.Ticker, s.INN)
		}
	}

	x5, ok := registry["X5"]
	if !ok {
		t.Fatal("X5 not found in bundled registry")
	}
	if x5.INN != "9722079341" || x5.Category != "retail" {
		t.Errorf("X5 = %+v, want INN 9722079341 / retail", x5)
	}
	lkoh, ok := registry["LKOH"]
	if !ok || lkoh.INN != "7708004767" || lkoh.Category != "oil" {
		t.Errorf("LKOH = %+v, want INN 7708004767 / oil", lkoh)
	}
	if _, ok := registry["NVTK"]; ok {
		t.Error("NVTK has no organization in ГИР БО")
	}
	if _, ok := registry["TATNP"]; ok {
		t.Error("TATNP shares TATN's filing and must not be a second company")
	}
}

func TestPERatioAndROE(t *testing.T) {
	f := models.Float
	eq := func(got *float64, want float64) bool {
		return got != nil && math.Abs(*got-want) < 1e-9
	}

	if got := peRatio(f(1000), f(100)); !eq(got, 10) {
		t.Errorf("peRatio(1000,100) = %v, want 10", got)
	}
	for name, got := range map[string]*float64{
		"no cap":      peRatio(nil, f(100)),
		"no profit":   peRatio(f(1000), nil),
		"zero profit": peRatio(f(1000), f(0)),
		"loss":        peRatio(f(1000), f(-5)),
	} {
		if got != nil {
			t.Errorf("peRatio %s = %v, want nil", name, *got)
		}
	}

	if got := roePercent(f(20), f(100)); !eq(got, 20) {
		t.Errorf("roePercent(20,100) = %v, want 20", got)
	}
	if got := roePercent(f(0), f(100)); !eq(got, 0) {
		t.Errorf("roePercent(0,100) = %v, want reported 0", got)
	}
	for name, got := range map[string]*float64{
		"no profit":       roePercent(nil, f(100)),
		"no equity":       roePercent(f(20), nil),
		"zero equity":     roePercent(f(20), f(0)),
		"negative equity": roePercent(f(20), f(-1)),
	} {
		if got != nil {
			t.Errorf("roePercent %s = %v, want nil", name, *got)
		}
	}
}

func TestCapitalization(t *testing.T) {
	if got := capitalization(500, nil); got == nil || *got != 500 {
		t.Errorf("capitalization(500) = %v, want 500", got)
	}
	if got := capitalization(0, errors.New("boom")); got != nil {
		t.Errorf("capitalization on error = %v, want nil", *got)
	}
	if got := capitalization(0, nil); got != nil {
		t.Errorf("capitalization(0) = %v, want nil", *got)
	}
}

type fakeQuoteStore struct {
	periods map[string]int
	prev    map[string]models.MarketQuote
	saved   []models.MarketQuote
}

func (f *fakeQuoteStore) ExistingPeriods(_ context.Context, company string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	for i := 0; i < f.periods[company]; i++ {
		out[fmt.Sprintf("%d-Q4", 2020+i)] = struct{}{}
	}
	return out, nil
}

func (f *fakeQuoteStore) GetMarketQuote(_ context.Context, company string) (models.MarketQuote, bool, error) {
	for i := len(f.saved) - 1; i >= 0; i-- {
		if f.saved[i].Company == company {
			return f.saved[i], true, nil
		}
	}
	if q, ok := f.prev[company]; ok {
		return q, true, nil
	}
	return models.MarketQuote{}, false, nil
}

func (f *fakeQuoteStore) SaveMarketQuote(_ context.Context, q models.MarketQuote) error {
	f.saved = append(f.saved, q)
	return nil
}

type fakeQuoteSource map[string]moex.Quote

func (f fakeQuoteSource) LatestQuote(_ context.Context, secid string, _ time.Time) (moex.Quote, error) {
	q, ok := f[secid]
	if !ok {
		return moex.Quote{}, errors.New("no trades")
	}
	return q, nil
}

func (f fakeQuoteSource) Splits(context.Context, string) ([]moex.Split, error) {
	return nil, nil
}

func TestFetchQuotes(t *testing.T) {
	store := &fakeQuoteStore{periods: map[string]int{"SBER": 3, "LOST": 2}}
	src := fakeQuoteSource{
		"SBER": {Price: 274.65, Date: "2026-09-29", Capitalization: 5929},
		"NEW":  {Price: 1, Date: "2026-09-29", Capitalization: 1},
	}
	logger := slog.New(slog.DiscardHandler)

	saved, failed := fetchQuotes(context.Background(), store, src, []string{"SBER", "NEW", "LOST"}, time.Now(), logger)
	if saved != 1 || len(store.saved) != 1 {
		t.Fatalf("saved = %d (%v), want 1", saved, store.saved)
	}
	got := store.saved[0]
	if got.Company != "SBER" || got.Capitalization != 5929 || got.PriceDate.Format(time.DateOnly) != "2026-09-29" {
		t.Errorf("saved quote = %+v", got)
	}
	// NEW has no financial rows (skipped silently); LOST has rows but no quote.
	if len(failed) != 1 || failed[0] != "LOST" {
		t.Errorf("failed = %v, want [LOST]", failed)
	}
}

func TestUniqueNames(t *testing.T) {
	got := uniqueNames([]string{"SBER", "LKOH", "SBER", "Sber", "T"})
	want := []string{"SBER", "LKOH", "Sber", "T"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("uniqueNames = %v, want %v", got, want)
	}
}

func TestCapJumps(t *testing.T) {
	f := models.Float
	row := func(y int, c float64) models.QuarterData {
		return models.QuarterData{Year: y, Quarter: "Q4", Capitalization: f(c)}
	}
	// BELU before the split fix: 2023 cap 675.9 -> 2024 64.7 (x0.096).
	rows := []models.QuarterData{row(2025, 51.6), row(2023, 675.9), row(2024, 64.7), row(2021, 423), row(2022, 350),
		{Year: 2020, Quarter: "Q4"}}
	got := capJumps(rows)
	if len(got) != 1 || got[0].from != 2023 || got[0].to != 2024 || math.Abs(got[0].ratio-64.7/675.9) > 1e-9 {
		t.Errorf("capJumps = %+v, want one 2023->2024 jump", got)
	}
	// A gap year is not "consecutive".
	if got := capJumps([]models.QuarterData{row(2020, 10), row(2022, 100)}); len(got) != 0 {
		t.Errorf("gap years flagged: %+v", got)
	}
}

func TestUnexplainedShares(t *testing.T) {
	splits := []moex.Split{{TradeDate: "2024-07-15", Before: 1, After: 10}}
	// A recorded 1:10 between the two quotes is the whole change.
	if _, bad := unexplainedShares(100, 1000, "2024-07-01", "2024-07-20", splits); bad {
		t.Error("a known split was reported as unexplained")
	}
	// Doubling with no split in the window (a 1:2 the registry missed, or an issue).
	if ratio, bad := unexplainedShares(100, 200, "2024-01-01", "2024-06-01", splits); !bad || math.Abs(ratio-2) > 1e-9 {
		t.Errorf("unexplained double = %v, %v", ratio, bad)
	}
	// Nothing stored yet.
	if _, bad := unexplainedShares(0, 200, "2024-01-01", "2024-06-01", nil); bad {
		t.Error("missing previous count must not warn")
	}
}

func TestExtraSplitsRegistryLoads(t *testing.T) {
	if sp := extraSplits["BELU"]; len(sp) != 1 || sp[0].After != 8 {
		t.Errorf("bundled BELU split = %+v", sp)
	}
}

func TestRenameRegistryLoads(t *testing.T) {
	if p := predecessors["T"]; len(p) != 1 || p[0] != "TCSG" {
		t.Errorf("bundled T predecessors = %v", p)
	}
}

func TestStoredSpecs(t *testing.T) {
	registry := map[string]tickerSpec{
		"LKOH": {"LKOH", "7708004767", "oil"},
		"MGNT": {"MGNT", "2309085638", "retail"},
	}
	banks := map[string]bankSpec{
		"SBER": {"SBER", 1481, "banks"},
		"VTBR": {"VTBR", 1000, "banks"},
	}
	stored := []database.CompanyWithCategory{
		{Company: "SBER", Category: "финансы"}, // stored category wins
		{Company: "SBER", Category: "banks"},   // duplicate company is ignored
		{Company: "MGNT", Category: ""},        // empty category keeps the registry one
		{Company: "lkoh", Category: "oil"},     // not the stored ticker form
		{Company: "CSVONLY", Category: "misc"}, // in neither registry
	}

	specs, bankSpecs := storedSpecs(stored, registry, banks)

	wantSpecs := []tickerSpec{{"MGNT", "2309085638", "retail"}}
	wantBanks := []bankSpec{{"SBER", 1481, "финансы"}}
	if fmt.Sprint(specs) != fmt.Sprint(wantSpecs) {
		t.Errorf("specs = %+v, want %+v", specs, wantSpecs)
	}
	if fmt.Sprint(bankSpecs) != fmt.Sprint(wantBanks) {
		t.Errorf("banks = %+v, want %+v", bankSpecs, wantBanks)
	}
}

// TestBankRows: cumulative YTD figures are differenced into quarters, each row
// carries its quarter-end equity, and only the Q4 row gets cap, P/E and ROE.
// Stored periods are skipped, and a quarter whose predecessor is missing
// cannot be differenced.
func TestBankRows(t *testing.T) {
	const regn = 1481
	spec := bankSpec{Ticker: "SBER", Regn: regn, Category: "banks"}
	cum := map[string]map[int]cbr.Form102{
		"Q1": {regn: {NetProfit: 100, Revenue: 300}},
		"Q2": {regn: {NetProfit: 250, Revenue: 650}},
		"Q3": {regn: {NetProfit: 380, Revenue: 1000}},
		"Q4": {regn: {NetProfit: 500, Revenue: 1400}, 1000: {NetProfit: 1}},
	}
	equity := map[string]map[int]float64{
		"Q1": {regn: 1000},
		"Q3": {regn: 1100},
		"Q4": {regn: 1200},
	}
	existing := map[string]struct{}{"2023-Q2": {}}
	var capCalls []string
	capAt := func(ticker string, year int) *float64 {
		capCalls = append(capCalls, fmt.Sprintf("%s-%d", ticker, year))
		return models.Float(5000)
	}

	rows := bankRows(spec, 2023, cum, equity, nil, existing, capAt)

	type want struct {
		quarter              string
		profit, revenue, eq  float64
		hasEquity, valuation bool
	}
	wants := []want{
		{quarter: "Q1", profit: 100, revenue: 300, eq: 1000, hasEquity: true},
		// Q2 is already stored; Q3 is still differenced against Q2's cumulative.
		{quarter: "Q3", profit: 130, revenue: 350, eq: 1100, hasEquity: true},
		{quarter: "Q4", profit: 120, revenue: 400, eq: 1200, hasEquity: true, valuation: true},
	}
	if len(rows) != len(wants) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(wants), rows)
	}
	near := func(got *float64, want float64) bool {
		return got != nil && math.Abs(*got-want) < 1e-9
	}
	for i, w := range wants {
		r := rows[i]
		if r.Quarter != w.quarter || r.Year != 2023 || r.Company != "SBER" || r.Category != "banks" || r.Source != models.SourceCBR102 {
			t.Errorf("row %d = %d-%s %s/%s/%s, want 2023-%s SBER/banks/%s", i, r.Year, r.Quarter, r.Company, r.Category, r.Source, w.quarter, models.SourceCBR102)
		}
		if !near(r.NetProfit, w.profit) || !near(r.Revenue, w.revenue) {
			t.Errorf("%s profit/revenue = %v/%v, want %v/%v", w.quarter, deref(r.NetProfit), deref(r.Revenue), w.profit, w.revenue)
		}
		if w.hasEquity != (r.Equity != nil) || (w.hasEquity && !near(r.Equity, w.eq)) {
			t.Errorf("%s equity = %v, want %v", w.quarter, deref(r.Equity), w.eq)
		}
		if !w.valuation {
			if r.Capitalization != nil || r.PE != nil || r.ROE != nil {
				t.Errorf("%s carries valuation fields; only Q4 should", w.quarter)
			}
			continue
		}
		if !near(r.Capitalization, 5000) || !near(r.PE, 10) || !near(r.ROE, 500.0/1200*100) {
			t.Errorf("Q4 cap/PE/ROE = %v/%v/%v, want 5000/10/%v", deref(r.Capitalization), deref(r.PE), deref(r.ROE), 500.0/1200*100)
		}
	}
	if len(capCalls) != 1 || capCalls[0] != "SBER-2023" {
		t.Errorf("capAt calls = %v, want exactly [SBER-2023]", capCalls)
	}

	// A missing Q2 cumulative leaves Q2 and Q3 out; Q4 (no equity) still has
	// cap and P/E but no ROE.
	delete(cum, "Q2")
	rows = bankRows(spec, 2023, cum, map[string]map[int]float64{}, nil, nil, capAt)
	if len(rows) != 2 || rows[0].Quarter != "Q1" || rows[1].Quarter != "Q4" {
		t.Fatalf("with Q2 missing got %+v, want Q1 and Q4", rows)
	}
	if q4 := rows[1]; q4.Equity != nil || q4.ROE != nil || !near(q4.PE, 10) {
		t.Errorf("Q4 without equity: equity=%v ROE=%v PE=%v, want nil/nil/10", deref(q4.Equity), deref(q4.ROE), deref(q4.PE))
	}

	// A bank absent from the archives yields nothing and is never priced.
	capCalls = nil
	if rows := bankRows(bankSpec{Ticker: "VTBR", Regn: 1000}, 2023, map[string]map[int]cbr.Form102{"Q1": {}}, nil, nil, nil, capAt); len(rows) != 0 {
		t.Errorf("absent bank produced rows: %+v", rows)
	}
	if len(capCalls) != 0 {
		t.Errorf("absent bank priced: %v", capCalls)
	}
}

// TestBankRowsLossesAndPending: a quarter below the previous cumulative goes
// negative, a loss year has no P/E but a negative ROE, and a pending quarter is
// held back while its cumulative still differences the next one.
func TestBankRowsLossesAndPending(t *testing.T) {
	const regn = 7
	spec := bankSpec{Ticker: "BANK", Regn: regn}
	cum := map[string]map[int]cbr.Form102{
		"Q1": {regn: {NetProfit: 10, Revenue: 50}},
		"Q2": {regn: {NetProfit: 4, Revenue: 45}}, // Q2 alone: -6 profit, -5 revenue
		"Q3": {regn: {NetProfit: -20, Revenue: 60}},
		"Q4": {regn: {NetProfit: -30, Revenue: 80}},
	}
	equity := map[string]map[int]float64{"Q2": {regn: 100}, "Q4": {regn: 90}}
	capAt := func(string, int) *float64 { return models.Float(500) }

	rows := bankRows(spec, 2024, cum, equity, map[string]bool{"Q3": true}, nil, capAt)
	got := map[string]models.QuarterData{}
	for _, r := range rows {
		got[r.Quarter] = r
	}
	if _, ok := got["Q3"]; ok || len(rows) != 3 {
		t.Fatalf("rows = %+v, want Q1, Q2, Q4 (Q3 pending)", rows)
	}
	near := func(p *float64, want float64) bool { return p != nil && math.Abs(*p-want) < 1e-9 }
	if q2 := got["Q2"]; !near(q2.NetProfit, -6) || !near(q2.Revenue, -5) {
		t.Errorf("Q2 = %v/%v, want -6/-5", deref(q2.NetProfit), deref(q2.Revenue))
	}
	q4 := got["Q4"] // differenced against the pending Q3's cumulative
	if !near(q4.NetProfit, -10) || !near(q4.Revenue, 20) {
		t.Errorf("Q4 = %v/%v, want -10/20", deref(q4.NetProfit), deref(q4.Revenue))
	}
	if q4.PE != nil {
		t.Errorf("loss-year P/E = %v, want nil", *q4.PE)
	}
	if !near(q4.ROE, -30.0/90*100) {
		t.Errorf("loss-year ROE = %v, want %v", deref(q4.ROE), -30.0/90*100)
	}
}

func TestEquityPending(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) // grace = 90 days
	for date, want := range map[string]bool{
		"20261001": true,  // future quarter end
		"20260801": true,  // 60 days ago
		"20260703": true,  // 89 days ago, still within grace
		"20260701": false, // 91 days ago
		"20230101": false,
		"garbage":  false,
	} {
		if got := equityPending(date, now); got != want {
			t.Errorf("equityPending(%s) = %v, want %v", date, got, want)
		}
	}
}

func TestQuarterFullyStored(t *testing.T) {
	specs := []bankSpec{{Ticker: "A"}, {Ticker: "B"}}
	existing := map[string]map[string]struct{}{
		"A": {"2024-Q1": {}, "2024-Q2": {}},
		"B": {"2024-Q1": {}},
	}
	if !quarterFullyStored(specs, existing, 2024, "Q1") {
		t.Error("Q1 stored for both banks, want true")
	}
	if quarterFullyStored(specs, existing, 2024, "Q2") {
		t.Error("Q2 missing for B, want false")
	}
	if yearFullyStored(specs, existing, 2024) {
		t.Error("year incomplete, want false")
	}
}

// deref formats an optional metric for failure messages.
func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestFillMissingINN(t *testing.T) {
	banks := map[string]bankSpec{"SBER": {Ticker: "SBER"}}
	var looked []string
	lookup := func(_ context.Context, secid string) (string, error) {
		looked = append(looked, secid)
		switch secid {
		case "NLMK":
			return "4823006703", nil
		case "BAD":
			return "n/a", nil
		}
		return "", errors.New("not found")
	}
	specs := []tickerSpec{
		{Ticker: "LKOH", INN: "7708004767", Category: "oil"}, // registry: kept as is
		{Ticker: "NLMK", Category: "metals"},                 // looked up
		{Ticker: "SBER"},                                     // a bank: not looked up
		{Ticker: "ZZZZ"},                                     // lookup fails
		{Ticker: "BAD"},                                      // malformed INN
	}
	logger := slog.New(slog.DiscardHandler)
	registry := map[string]tickerSpec{"LKOH": specs[0]}
	got, failed := fillMissingINN(context.Background(), specs, registry, banks, lookup, logger)

	want := []tickerSpec{
		{Ticker: "LKOH", INN: "7708004767", Category: "oil"},
		{Ticker: "NLMK", INN: "4823006703", Category: "metals"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("resolved = %+v, want %+v", got, want)
	}
	if strings.Join(failed, ",") != "SBER,ZZZZ,BAD" {
		t.Errorf("failed = %v, want SBER,ZZZZ,BAD", failed)
	}
	if strings.Join(looked, ",") != "NLMK,ZZZZ,BAD" {
		t.Errorf("looked up %v, want NLMK,ZZZZ,BAD (no registry or bank lookups)", looked)
	}

	// A canceled run looks nothing up and reports the unresolved tickers.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	looked = nil
	got, failed = fillMissingINN(ctx, specs[1:2], registry, banks, lookup, logger)
	if len(got) != 0 || len(failed) != 1 || len(looked) != 0 {
		t.Errorf("canceled: resolved %v, failed %v, looked %v", got, failed, looked)
	}
}
