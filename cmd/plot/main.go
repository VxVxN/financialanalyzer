package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/application"
	"github.com/VxVxN/financialanalyzer/internal/config"
	"github.com/VxVxN/financialanalyzer/internal/fetcher"
	"github.com/VxVxN/financialanalyzer/internal/handlers"
	"github.com/VxVxN/financialanalyzer/internal/models"
	"github.com/VxVxN/financialanalyzer/internal/ops"
	"github.com/VxVxN/financialanalyzer/internal/scheduler"
	"github.com/VxVxN/financialanalyzer/internal/version"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg := config.LoadConfig()
	if cfg.UsesDefaultPassword() {
		logger.Warn("using the default database password; set DB_PASSWORD before deploying")
	}
	if !cfg.AuthEnabled() {
		logger.Warn("AUTH_USER/AUTH_PASSWORD not set; delete and note endpoints are open to anyone who can reach the server")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	app, err := application.Init(cfg)
	if err != nil {
		return err
	}
	defer app.Close()

	if err = app.MigrateDB(); err != nil {
		return err
	}

	controller := handlers.NewController(app.Repo, logger)

	// Cancel the scheduler and in-process fetch/registry jobs on every return
	// path (a failed ListenAndServe included), and wait for the scheduler to
	// stop before the deferred app.Close drops the database pool under a
	// running fetch. Deferred in this order, the cancel runs before the wait.
	ctx, cancel := context.WithCancel(ctx)
	controller.SetJobs(ops.New(ctx, app.Repo, app.Notifier, logger))
	schedDone := make(chan struct{})
	defer func() { <-schedDone }()
	defer cancel()

	sched, err := newScheduler(cfg, app, controller, logger)
	if err != nil {
		close(schedDone)
		return err
	}
	if sched != nil {
		controller.SetSchedule(sched)
		go func() {
			defer close(schedDone)
			sched.Run(ctx)
		}()
	} else {
		close(schedDone)
		logger.Info("Scheduler disabled (set SCHEDULER_ENABLED=1 for a timetable)")
	}

	r := newRouter(cfg, controller)

	info := version.Get()
	logger.Info("Starting server",
		"port", cfg.Port,
		"version", info.Version,
		"commit", info.Commit,
		"build_date", info.Date,
	)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Shutdown makes ListenAndServe return at once, but in-flight requests are
	// still draining; wait for Shutdown to finish before run returns and the
	// deferred app.Close drops the database pool under them.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			// Requests may run up to the 30s middleware timeout; force-close the
			// stragglers rather than let app.Close pull the DB from under them.
			logger.Error("graceful shutdown failed; closing connections", "error", err)
			_ = srv.Close()
		}
	}()

	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-shutdownDone
	logger.Info("Server stopped")
	return nil
}

// newScheduler builds the data-refresh scheduler from the config, or returns
// nil when it is disabled. A malformed schedule is a startup error.
// The Monday cheap-list note goes out with the quotes job and, if that
// Monday was missed, once more after startup catch-up.
func newScheduler(cfg *config.Config, app *application.Application, controller *handlers.Controller, logger *slog.Logger) (*scheduler.Scheduler, error) {
	if !cfg.SchedulerEnabled {
		return nil, nil
	}
	jobs, err := schedulerJobs(cfg)
	if err != nil {
		return nil, err
	}
	run := func(ctx context.Context, req fetcher.Request, trigger string, stillNeeded func(context.Context) bool) error {
		opts := fetcher.RunOptions{Notifier: app.Notifier, StillNeeded: stillNeeded}
		_, err := fetcher.RunRecorded(ctx, app.Repo, req, trigger, opts, logger)
		if spec, ok := quotesSpec(jobs); ok {
			sendCheapList(ctx, app, controller, spec, logger)
		}
		return err
	}
	sched := scheduler.New(jobs, app.Repo, run, logger)
	if spec, ok := quotesSpec(jobs); ok && app.Notifier != nil {
		// A restart on Tuesday still owes Monday's note: the quotes catch-up
		// above only runs when today's quotes slot was missed, so the note
		// has its own check once that catch-up is done.
		sched.SetAfterCatchUp(func(ctx context.Context) {
			sendCheapList(ctx, app, controller, spec, logger)
		})
	}
	return sched, nil
}

// quotesSpec is the quotes job's clock. The Monday note uses that hour on
// the Monday of the current week. No quotes job means no note.
func quotesSpec(jobs []scheduler.Job) (scheduler.Spec, bool) {
	for _, j := range jobs {
		if j.Name() == fetcher.KindQuotes {
			return j.Spec, true
		}
	}
	return scheduler.Spec{}, false
}

// weekMonday is 00:00 Moscow on the Monday of the week that contains t.
// Sunday belongs to the week that started the Monday before.
func weekMonday(t time.Time) time.Time {
	m := t.In(models.Moscow)
	wd := int(m.Weekday())
	if wd == 0 {
		wd = 7
	}
	d := m.AddDate(0, 0, -(wd - 1))
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, models.Moscow)
}

// digestSlot is the Monday quotes clock of the week that contains now.
func digestSlot(now time.Time, quotes scheduler.Spec) time.Time {
	mon := weekMonday(now)
	return time.Date(mon.Year(), mon.Month(), mon.Day(), quotes.Hour, quotes.Minute, 0, 0, models.Moscow)
}

// digestPending reports whether this week's Monday note is owed: its slot
// has arrived, and that Monday is not recorded as sent.
func digestPending(now time.Time, quotes scheduler.Spec, sent bool) bool {
	return !now.Before(digestSlot(now, quotes)) && !sent
}

// sendCheapList delivers this week's Monday note when it is owed. A failed
// send is not recorded, so the next startup tries again.
func sendCheapList(ctx context.Context, app *application.Application, controller *handlers.Controller, spec scheduler.Spec, logger *slog.Logger) {
	if app.Notifier == nil {
		return
	}
	now := time.Now()
	if now.Before(digestSlot(now, spec)) {
		return
	}
	monday := weekMonday(now)
	sent, err := app.Repo.CheapListSent(ctx, monday)
	if err != nil {
		logger.Warn("Cheap-list catch-up skipped", "error", err)
		return
	}
	if !digestPending(now, spec, sent) {
		return
	}
	nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	msg, err := controller.CheapList(nctx)
	if err != nil {
		logger.Warn("Cheap-list not built", "error", err)
		return
	}
	if err := app.Notifier.Notify(nctx, msg); err != nil {
		logger.Warn("Cheap-list notification not sent", "error", err)
		return
	}
	if err := app.Repo.MarkCheapListSent(nctx, monday); err != nil {
		logger.Warn("Cheap-list send not recorded; it may be sent again", "error", err)
	}
}

// schedulerJobs parses the configured slots; "off" leaves a job out.
func schedulerJobs(cfg *config.Config) ([]scheduler.Job, error) {
	var jobs []scheduler.Job
	for _, j := range []struct {
		env, value string
		job        func(scheduler.Spec) scheduler.Job
	}{
		{"SCHEDULE_QUOTES", cfg.ScheduleQuotes, scheduler.QuotesJob},
		{"SCHEDULE_FINANCIALS", cfg.ScheduleFinancials, scheduler.FinancialsJob},
	} {
		if strings.EqualFold(strings.TrimSpace(j.value), "off") {
			continue
		}
		spec, err := scheduler.ParseSpec(j.value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", j.env, err)
		}
		jobs = append(jobs, j.job(spec))
	}
	return jobs, nil
}

// newRouter wires every HTTP route. It is separate from run so the route table,
// including which endpoints sit behind auth, can be tested without a database.
func newRouter(cfg *config.Config, controller *handlers.Controller) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", controller.Health)
	r.Get("/readyz", controller.Ready)
	r.Get("/version", controller.Version)

	r.Handle("/static/*", handlers.StaticHandler())
	r.Get("/", controller.ScreenerHandler)
	r.Get("/compare", controller.CompareHandler)
	r.Get("/api/companies", controller.GetCompanies)
	r.Get("/api/companies-with-categories", controller.GetCompaniesWithCategories)
	r.Get("/api/categories", controller.GetCategories)
	r.Get("/chart/{metric}", controller.ChartHandler)
	r.Get("/company/{name}", controller.DashboardHandler)
	r.Get("/screener", controller.ScreenerRedirect)
	r.Get("/api/screener", controller.ScreenerAPI)
	r.Get("/api/company-note", controller.GetCompanyNote)
	r.Get("/updates", controller.UpdatesHandler)
	r.Get("/api/fetch-runs", controller.FetchRunsAPI)
	r.Get("/api/fetch-ifrs", controller.IFRSStatus)
	r.Get("/api/manual-financials", controller.GetManualFinancials)

	// State-changing endpoints: Basic Auth when configured, and JSON-only
	// bodies so they cannot be triggered by a cross-site form post.
	r.Group(func(r chi.Router) {
		if cfg.AuthEnabled() {
			r.Use(handlers.RequireBasicAuth(cfg.AuthUser, cfg.AuthPassword))
		}
		r.Use(handlers.RequireJSONBody)
		r.Delete("/api/companies", controller.DeleteCompany)
		r.Post("/api/company-note", controller.SaveCompanyNote)
		r.Delete("/api/company-note", controller.DeleteCompanyNote)
		r.Put("/api/manual-financials", controller.SaveManualFinancials)
		r.Delete("/api/manual-financials", controller.DeleteManualFinancials)
		r.Post("/api/fetch", controller.StartFetch)
		r.Post("/api/fetch-ifrs", controller.StartIFRS)
	})
	r.Group(func(r chi.Router) {
		if cfg.AuthEnabled() {
			r.Use(handlers.RequireBasicAuth(cfg.AuthUser, cfg.AuthPassword))
		}
		r.Post("/api/import-manual", controller.ImportManual)
	})
	return r
}
