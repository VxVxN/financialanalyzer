package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
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
		{"unknown ticker, no inn", "ZZZZ", tickerSpec{}, false},
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
	saved   []models.MarketQuote
}

func (f *fakeQuoteStore) ExistingPeriods(_ context.Context, company string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	for i := 0; i < f.periods[company]; i++ {
		out[fmt.Sprintf("%d-Q4", 2020+i)] = struct{}{}
	}
	return out, nil
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
