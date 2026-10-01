package fetcher

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
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

// runStore is the fetch_runs slice of the repository (a fake in tests).
type runStore interface {
	StartFetchRun(ctx context.Context, run models.FetchRun) (int64, error)
	FinishFetchRun(ctx context.Context, run models.FetchRun) error
	AbandonStaleRuns(ctx context.Context, olderThan time.Duration, triggers []string) (int64, error)
}

// StaleRunAge is how long a fetch_runs row may stay "running" before it
// counts as abandoned by a process that died mid-run.
const StaleRunAge = 12 * time.Hour

// RunRecorded is Run with the run logged in fetch_runs: a "running" row first,
// then its outcome. Recording is best-effort — a failure to write it is logged
// and does not stop or fail the fetch itself.
func RunRecorded(ctx context.Context, repo *database.Repository, req Request, trigger string, logger *slog.Logger) (models.FetchRun, error) {
	return record(ctx, repo, req, trigger, time.Now, logger, func(ctx context.Context) (Summary, error) {
		return Run(ctx, repo, req, logger)
	})
}

func record(ctx context.Context, store runStore, req Request, trigger string, now func() time.Time, logger *slog.Logger, run func(context.Context) (Summary, error)) (models.FetchRun, error) {
	rec := models.FetchRun{
		Kind:      req.Kind(),
		Trigger:   trigger,
		Scope:     req.Scope(),
		FullScope: req.FullScope(),
		Status:    models.RunRunning,
		StartedAt: now(),
	}
	if _, err := store.AbandonStaleRuns(ctx, StaleRunAge, nil); err != nil {
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
