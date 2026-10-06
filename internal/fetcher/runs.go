package fetcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
	"github.com/VxVxN/financialanalyzer/internal/notify"
)

// Run triggers, as stored in fetch_runs.trigger.
const (
	TriggerCLI      = models.TriggerCLI
	TriggerSchedule = models.TriggerSchedule
	TriggerCatchUp  = models.TriggerCatchUp
)

// finishTimeout bounds recording a run's outcome, which happens on a context
// detached from the (possibly canceled) run context.
const finishTimeout = 10 * time.Second

// runStore is the fetch_runs slice of the repository plus the cross-process
// fetch lock (a fake in tests).
type runStore interface {
	LockFetch(ctx context.Context, onWait func()) (release func(), err error)
	StartFetchRun(ctx context.Context, run models.FetchRun) (int64, error)
	FinishFetchRun(ctx context.Context, run models.FetchRun) error
	AbandonStaleRuns(ctx context.Context, olderThan time.Duration, triggers []string) (int64, error)
}

// Notifier delivers an operator message (notify.Telegram in production).
type Notifier interface {
	Notify(ctx context.Context, text string) error
}

// StaleRunAge is how long a fetch_runs row may stay "running" before the
// scheduler's startup sweep (outside the fetch lock) counts it as abandoned.
// A run holding the lock abandons every other "running" row at once.
const StaleRunAge = 12 * time.Hour

// notifyTimeout bounds sending a failure notification.
const notifyTimeout = 15 * time.Second

// ErrNotNeeded is returned by RunRecorded when RunOptions.StillNeeded, checked
// once the lock is held, says another run already did the work.
var ErrNotNeeded = errors.New("fetch no longer needed: another run did it")

// RunOptions are RunRecorded's optional hooks.
type RunOptions struct {
	// Notifier receives a message about a failed or partial run (nil: none).
	Notifier Notifier
	// StillNeeded, when set, is checked once the lock is held; false skips
	// the run with ErrNotNeeded (a catch-up that a run it waited for made
	// redundant).
	StillNeeded func(context.Context) bool
}

// RunRecorded is Run serialized with every other fetch (POST /api/fetch and the
// scheduler) by a Postgres advisory lock — a second run waits for the first —
// and logged in fetch_runs: a "running" row first, then its outcome. A failed
// or partial run, including one that could not take the lock, is reported to
// opts.Notifier. Recording and notifying are best-effort: their failure is
// logged and does not stop or fail the fetch itself.
func RunRecorded(ctx context.Context, repo *database.Repository, req Request, trigger string, opts RunOptions, logger *slog.Logger) (models.FetchRun, error) {
	return record(ctx, repo, req, trigger, time.Now, opts, logger, func(ctx context.Context) (Summary, error) {
		return Run(ctx, repo, req, logger)
	})
}

func record(ctx context.Context, store runStore, req Request, trigger string, now func() time.Time, opts RunOptions, logger *slog.Logger, run func(context.Context) (Summary, error)) (models.FetchRun, error) {
	rec, err := lockedRun(ctx, store, req, trigger, now, opts, logger, run)
	if opts.Notifier != nil && (rec.Status == models.RunFailed || rec.Status == models.RunPartial) {
		nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
		defer cancel()
		if err := opts.Notifier.Notify(nctx, runMessage(rec)); err != nil {
			logger.Warn("Fetch failure notification not sent", "error", err)
		}
	}
	return rec, err
}

// lockedRun takes the fetch lock and runs under it; the lock is released
// before record sends a notification, so a waiting fetch can start.
func lockedRun(ctx context.Context, store runStore, req Request, trigger string, now func() time.Time, opts RunOptions, logger *slog.Logger, run func(context.Context) (Summary, error)) (models.FetchRun, error) {
	release, err := store.LockFetch(ctx, func() {
		logger.Info("Another data refresh is running; waiting for it to finish", "kind", req.Kind(), "trigger", trigger)
	})
	if err != nil {
		// Without the lock the run could overlap another one, so it does not
		// go ahead. Recording it would most likely fail too (the lock is the
		// first database call), so it is only reported.
		finished := now()
		rec := models.FetchRun{
			Kind: req.Kind(), Trigger: trigger, Scope: req.Scope(), FullScope: req.FullScope(),
			Status: models.RunFailed, StartedAt: finished, FinishedAt: &finished, Error: err.Error(),
		}
		if ctx.Err() != nil {
			rec.Status = models.RunCanceled
		}
		return rec, err
	}
	defer release()

	if opts.StillNeeded != nil && !opts.StillNeeded(ctx) {
		return models.FetchRun{Kind: req.Kind(), Trigger: trigger}, ErrNotNeeded
	}
	// No other fetch runs now, so any "running" row was left by a dead
	// process, however recent.
	return recordRun(ctx, store, req, trigger, now, logger, run)
}

func recordRun(ctx context.Context, store runStore, req Request, trigger string, now func() time.Time, logger *slog.Logger, run func(context.Context) (Summary, error)) (models.FetchRun, error) {
	rec := models.FetchRun{
		Kind:      req.Kind(),
		Trigger:   trigger,
		Scope:     req.Scope(),
		FullScope: req.FullScope(),
		Status:    models.RunRunning,
		StartedAt: now(),
	}
	if _, err := store.AbandonStaleRuns(ctx, 0, nil); err != nil {
		logger.Warn("Stale fetch runs not cleaned up", "error", err)
	}
	id, err := store.StartFetchRun(ctx, rec)
	if err != nil {
		logger.Warn("Fetch run not recorded", "error", err)
	}
	rec.ID = id

	sum, runErr := run(ctx)

	finished := now()
	rec.FinishedAt = &finished
	rec.Status = runStatus(sum, runErr)
	rec.Updated, rec.UpToDate, rec.Rows = sum.Updated, sum.UpToDate, sum.Rows
	rec.QuotesSaved = sum.QuotesSaved
	rec.Failed, rec.QuotesFailed = sum.Failed, sum.QuotesFailed
	if runErr != nil {
		rec.Error = runErr.Error()
	}

	if id != 0 {
		// The run context may be canceled (shutdown); the outcome is still
		// worth recording.
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
		defer cancel()
		if err := store.FinishFetchRun(fctx, rec); err != nil {
			logger.Warn("Fetch run outcome not recorded", "id", id, "error", err)
		}
	}
	return rec, runErr
}

// maxNotifiedError bounds the error text quoted in a notification.
const maxNotifiedError = 1000

// runMessage is the Russian notification text for a failed or partial run.
// The chat is the operator's own, so unlike the public updates page it quotes
// the raw error.
func runMessage(r models.FetchRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "⚠️ Обновление данных: %s\n", models.RunStatusLabel(r.Status))
	fmt.Fprintf(&b, "%s, %s", models.RunKindLabel(r.Kind), models.RunTriggerLabel(r.Trigger))
	if !r.FullScope && r.Scope != "" {
		fmt.Fprintf(&b, " (%s)", r.Scope)
	}
	fmt.Fprintf(&b, ", начало %s МСК", r.StartedAt.In(models.Moscow).Format("02.01.2006 15:04"))
	if d := r.Duration(); d > 0 {
		fmt.Fprintf(&b, ", длительность %s", d.Round(time.Second))
	}
	b.WriteString("\n")
	if r.Status == models.RunPartial {
		fmt.Fprintf(&b, "Обновлено компаний: %d, без изменений: %d, сохранено котировок: %d\n", r.Updated, r.UpToDate, r.QuotesSaved)
	}
	if len(r.Failed) > 0 {
		fmt.Fprintf(&b, "Не удалось обновить: %s\n", strings.Join(r.Failed, ", "))
	}
	if r.QuotesSaved == 0 && len(r.QuotesFailed) > 0 {
		fmt.Fprintf(&b, "Ни одной котировки, ошибки по: %s\n", strings.Join(r.QuotesFailed, ", "))
	}
	if r.Error != "" {
		fmt.Fprintf(&b, "Ошибка: %s\n", notify.Truncate(r.Error, maxNotifiedError))
	}
	return strings.TrimRight(b.String(), "\n")
}

// runStatus classifies a run's outcome. Quote failures alone do not make a run
// partial — names that are not MOEX secids (CSV-only companies) fail on every
// run — unless no quote was saved at all. An empty DB is nothing to refresh,
// not a failure.
func runStatus(sum Summary, err error) string {
	switch {
	case errors.Is(err, ErrNoCompanies):
		return models.RunOK
	case isCanceled(err):
		return models.RunCanceled
	case err != nil:
		return models.RunFailed
	case len(sum.Failed) > 0, sum.QuotesSaved == 0 && len(sum.QuotesFailed) > 0:
		return models.RunPartial
	default:
		return models.RunOK
	}
}
