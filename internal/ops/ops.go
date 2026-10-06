// Package ops runs the fetch pipelines and the ticker-registry proposal
// inside the HTTP server (replacing the old cmd/fetch and cmd/registry
// binaries). Both jobs are started from /updates and run in the background
// against the server's lifetime context, so an HTTP timeout cannot cancel them.
package ops

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/fetcher"
	"github.com/VxVxN/financialanalyzer/internal/notify"
	"github.com/VxVxN/financialanalyzer/internal/registry"
	"github.com/VxVxN/financialanalyzer/internal/scraper/girbo"
	"github.com/VxVxN/financialanalyzer/internal/scraper/moex"
)

// ErrBusy means this process is already running that job. A second HTTP
// click is rejected rather than queued; the scheduler still waits on the
// fetch lock as before.
var ErrBusy = errors.New("already running")

// RegistryStatus is the last registry job, for GET /api/registry.
type RegistryStatus struct {
	Running     bool       `json:"running"`
	Tickers     string     `json:"tickers,omitempty"`
	Failed      bool       `json:"failed"`
	GeneratedAt *time.Time `json:"generated_at,omitempty"`
	Include     int        `json:"include"`
	Exclude     int        `json:"exclude"`
	FailedN     int        `json:"failed_count"`
	Text        string     `json:"text,omitempty"`
}

// Ops is the in-process job runner wired into cmd/plot.
type Ops struct {
	ctx    context.Context
	logger *slog.Logger

	runFetch      func(context.Context, fetcher.Request) error
	buildRegistry func(context.Context, string) (RegistryStatus, error)

	mu            sync.Mutex
	fetchRunning  bool
	registryState RegistryStatus
}

// New binds jobs to ctx (canceled on server shutdown).
func New(ctx context.Context, repo *database.Repository, n notify.Notifier, logger *slog.Logger) *Ops {
	if logger == nil {
		logger = slog.Default()
	}
	o := &Ops{ctx: ctx, logger: logger}
	o.runFetch = func(ctx context.Context, req fetcher.Request) error {
		_, err := fetcher.RunRecorded(ctx, repo, req, fetcher.TriggerCLI, fetcher.RunOptions{Notifier: n}, logger)
		return err
	}
	o.buildRegistry = o.runRegistry
	return o
}

// StartFetch begins a recorded fetch. req.TickersFile is ignored (no
// filesystem paths from the API).
func (o *Ops) StartFetch(req fetcher.Request) error {
	req.TickersFile = ""
	o.mu.Lock()
	if o.fetchRunning {
		o.mu.Unlock()
		return ErrBusy
	}
	o.fetchRunning = true
	o.mu.Unlock()

	go func() {
		defer func() {
			o.mu.Lock()
			o.fetchRunning = false
			o.mu.Unlock()
		}()
		if err := o.runFetch(o.ctx, req); err != nil && !errors.Is(err, context.Canceled) {
			o.logger.Error("manual fetch failed", "error", err)
		}
	}()
	return nil
}

// FetchRunning reports whether a manual fetch started here is still in flight
// (a scheduled run holding the lock does not count).
func (o *Ops) FetchRunning() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.fetchRunning
}

// StartRegistry begins a registry proposal. A full board run takes minutes.
func (o *Ops) StartRegistry(tickers string) error {
	o.mu.Lock()
	if o.registryState.Running {
		o.mu.Unlock()
		return ErrBusy
	}
	prev := o.registryState
	o.registryState = RegistryStatus{Running: true, Tickers: tickers, Text: prev.Text, GeneratedAt: prev.GeneratedAt, Include: prev.Include, Exclude: prev.Exclude, FailedN: prev.FailedN}
	o.mu.Unlock()

	go func() {
		st, err := o.buildRegistry(o.ctx, tickers)
		o.mu.Lock()
		defer o.mu.Unlock()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				o.logger.Error("registry build failed", "error", err)
			}
			st.Failed = true
			st.Running = false
			st.Tickers = tickers
			st.Text = prev.Text
			st.GeneratedAt = prev.GeneratedAt
			st.Include, st.Exclude, st.FailedN = prev.Include, prev.Exclude, prev.FailedN
		}
		o.registryState = st
	}()
	return nil
}

// RegistrySnapshot copies the last job's status.
func (o *Ops) RegistrySnapshot() RegistryStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.registryState
}

func (o *Ops) runRegistry(ctx context.Context, tickers string) (RegistryStatus, error) {
	banks, err := fetcher.BankTickers()
	if err != nil {
		return RegistryStatus{}, err
	}
	entries, err := fetcher.RegistryEntries()
	if err != nil {
		return RegistryStatus{}, err
	}
	known := make(map[string]registry.KnownEntry, len(entries))
	for t, e := range entries {
		known[t] = registry.KnownEntry{INN: e.INN, Category: e.Category}
	}

	mx := moex.NewClient()
	mx.Logger = o.logger
	now := time.Now()
	cands, err := registry.Build(ctx, mx, girbo.NewClient(), registry.Options{
		Now:     now,
		Banks:   banks,
		Known:   known,
		Tickers: registry.ParseTickers(tickers),
		Logger:  o.logger,
	})
	if err != nil {
		return RegistryStatus{}, err
	}

	var buf bytes.Buffer
	if err := registry.Write(&buf, cands, now); err != nil {
		return RegistryStatus{}, err
	}
	st := RegistryStatus{Tickers: tickers, GeneratedAt: &now, Text: buf.String()}
	for _, c := range cands {
		switch c.Verdict {
		case registry.Include:
			st.Include++
		case registry.Exclude:
			st.Exclude++
		default:
			st.FailedN++
		}
	}
	o.logger.Info("Registry proposal ready",
		"include", st.Include, "exclude", st.Exclude, "failed", st.FailedN)
	return st, nil
}
