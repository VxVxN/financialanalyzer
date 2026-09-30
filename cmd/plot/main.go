package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/application"
	"github.com/VxVxN/financialanalyzer/internal/config"
	"github.com/VxVxN/financialanalyzer/internal/handlers"
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

	r.Get("/", controller.IndexHandler)
	r.Get("/api/companies", controller.GetCompanies)
	r.Get("/api/companies-with-categories", controller.GetCompaniesWithCategories)
	r.Get("/api/categories", controller.GetCategories)
	r.Get("/chart/{metric}", controller.ChartHandler)
	r.Get("/company/{name}", controller.DashboardHandler)
	r.Get("/screener", controller.ScreenerHandler)
	r.Get("/api/screener", controller.ScreenerAPI)
	r.Get("/api/company-note", controller.GetCompanyNote)

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
	})
	return r
}
