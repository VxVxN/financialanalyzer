package main

import (
	"strings"
	"testing"
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
