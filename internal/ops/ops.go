// Package ops runs the fetch pipelines inside the HTTP server (replacing the
// old cmd/fetch binary). A job is started from /updates and runs in the
// background against the server's lifetime context, so an HTTP timeout cannot
// cancel it.
package ops

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/fetcher"
	"github.com/VxVxN/financialanalyzer/internal/ifrs"
	"github.com/VxVxN/financialanalyzer/internal/models"
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
	repo   *database.Repository
	ifrs   *ifrs.Client

	runFetch func(context.Context, fetcher.Request) error

	mu           sync.Mutex
	fetchRunning bool
	ifrsRunning  bool
	ifrsStatus   ifrs.Status
}

// New binds jobs to ctx (canceled on server shutdown).
func New(ctx context.Context, repo *database.Repository, n notify.Notifier, logger *slog.Logger) *Ops {
	if logger == nil {
		logger = slog.Default()
	}
	o := &Ops{ctx: ctx, logger: logger, repo: repo, ifrs: ifrs.NewClient()}
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

// StartIFRS downloads annual IFRS reports for the given companies (nil means
// every company in the database) and fills empty manual fields. years is the
// window, usually the five years up to the last completed one.
func (o *Ops) StartIFRS(companies []string, years []int) error {
	o.mu.Lock()
	if o.ifrsRunning {
		o.mu.Unlock()
		return ErrBusy
	}
	o.ifrsRunning = true
	o.ifrsStatus = ifrs.Status{Running: true}
	o.mu.Unlock()

	list := append([]string(nil), companies...)
	ys := append([]int(nil), years...)
	go func() {
		defer func() {
			o.mu.Lock()
			o.ifrsRunning = false
			o.mu.Unlock()
		}()
		// A nil list means "everyone"; copying a nil slice stays nil.
		var companies []string
		if list != nil {
			companies = list
		}
		res := ifrs.Run(o.ctx, o.repo, o.ifrs, companies, ys, o.logger, func(p ifrs.Progress) {
			st := p.Status()
			o.mu.Lock()
			o.ifrsStatus = st
			o.mu.Unlock()
		})
		o.recordNewIFRSYears(res)
		o.mu.Lock()
		o.ifrsStatus = res.Status()
		o.mu.Unlock()
	}()
	return nil
}

// recordNewIFRSYears queues years the pull created for the next Monday note.
// A field filled on a year that already had a manual row is not queued.
func (o *Ops) recordNewIFRSYears(res ifrs.Result) {
	if o.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(o.ctx), 15*time.Second)
	defer cancel()
	for _, f := range res.Fills {
		if !f.NewYear {
			continue
		}
		err := o.repo.AddDigestEvent(ctx, models.DigestEvent{
			Kind: models.DigestIFRSYear, Company: f.Company, Detail: strconv.Itoa(f.Year),
		})
		if err != nil {
			o.logger.Warn("IFRS year not queued for the Monday note", "company", f.Company, "year", f.Year, "error", err)
		}
	}
}

// IFRSStatus is the latest automatic IFRS run, for the updates page to poll.
func (o *Ops) IFRSStatus() ifrs.Status {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ifrsStatus
}
