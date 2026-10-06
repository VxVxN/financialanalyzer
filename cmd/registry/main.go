// Command registry proposes a ГИР БО ticker registry (fetch_tickers.txt) from
// primary sources: every share on MOEX's main board (TQBR) with its issuer INN
// from MOEX ISS, checked against ГИР БО for a recent annual report with real
// revenue, categorized by MOEX sector index (see internal/registry). It prints
// the proposal to stdout — logs go to stderr — and never touches the bundled
// file: review the diff, then commit.
//
//	go run ./cmd/registry > /tmp/fetch_tickers.txt && diff fetch_tickers.txt /tmp/fetch_tickers.txt
//
//	REGISTRY_TICKERS  comma-separated secids to check instead of the whole
//	                  board (e.g. to rerun the ones whose check failed)
//
// A full run makes a few ISS calls and three or four ГИР БО calls per issuer,
// paced by the clients' rate limits: expect 10-15 minutes. It needs no
// database.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/fetcher"
	"github.com/VxVxN/financialanalyzer/internal/registry"
	"github.com/VxVxN/financialanalyzer/internal/scraper/girbo"
	"github.com/VxVxN/financialanalyzer/internal/scraper/moex"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("Interrupted")
		} else {
			logger.Error("Registry build failed", "error", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	banks, err := fetcher.BankTickers()
	if err != nil {
		return fmt.Errorf("load bank registry: %w", err)
	}
	entries, err := fetcher.RegistryEntries()
	if err != nil {
		return fmt.Errorf("load ticker registry: %w", err)
	}
	known := make(map[string]registry.KnownEntry, len(entries))
	for t, e := range entries {
		known[t] = registry.KnownEntry{INN: e.INN, Category: e.Category}
	}

	mx := moex.NewClient()
	mx.Logger = logger
	now := time.Now()
	cands, err := registry.Build(ctx, mx, girbo.NewClient(), registry.Options{
		Now:     now,
		Banks:   banks,
		Known:   known,
		Tickers: tickerSet(os.Getenv("REGISTRY_TICKERS")),
		Logger:  logger,
	})
	if err != nil {
		return err
	}

	counts := map[registry.Verdict]int{}
	for _, c := range cands {
		counts[c.Verdict]++
	}
	logger.Info("Registry proposal ready",
		"include", counts[registry.Include], "exclude", counts[registry.Exclude], "failed", counts[registry.Failed])
	return registry.Write(os.Stdout, cands, now)
}

// tickerSet parses a comma-separated secid list (nil when empty).
func tickerSet(raw string) map[string]struct{} {
	var out map[string]struct{}
	for _, t := range strings.Split(raw, ",") {
		if t = strings.ToUpper(strings.TrimSpace(t)); t != "" {
			if out == nil {
				out = make(map[string]struct{})
			}
			out[t] = struct{}{}
		}
	}
	return out
}
