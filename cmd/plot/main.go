package main

import (
	"context"
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
	r.Delete("/api/companies", controller.DeleteCompany)
	r.Get("/api/companies-with-categories", controller.GetCompaniesWithCategories)
	r.Get("/api/categories", controller.GetCategories)
	r.Get("/chart/{metric}", controller.ChartHandler)
	r.Get("/company/{name}", controller.DashboardHandler)

	r.Get("/api/company-note", controller.GetCompanyNote)
	r.Post("/api/company-note", controller.SaveCompanyNote)
	r.Delete("/api/company-note", controller.DeleteCompanyNote)

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

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	return srv.ListenAndServe()
}
