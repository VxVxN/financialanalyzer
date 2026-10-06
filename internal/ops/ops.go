// Package ops runs the fetch pipelines inside the HTTP server (replacing the
// old cmd/fetch binary). A job is started from /updates and runs in the
// background against the server's lifetime context, so an HTTP timeout cannot
// cancel it.
package ops

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/fetcher"
	"github.com/VxVxN/financialanalyzer/internal/notify"
)

// ErrBusy means this process is already running that job. A second HTTP
// click is rejected rather than queued; the scheduler still waits on the
// fetch lock as before.
var ErrBusy = errors.New("already running")

// Ops is the in-process job runner wired into cmd/plot.
type Ops struct {
	ctx    context.Context
	logger *slog.Logger

	runFetch func(context.Context, fetcher.Request) error

	mu           sync.Mutex
	fetchRunning bool
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
