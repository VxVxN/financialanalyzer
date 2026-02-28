package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/VxVxN/financialanalyzer/internal/config"
	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/parser"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg := config.LoadConfig()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("Application failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	db, err := database.NewConnection(cfg)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	if err := database.RunMigrations(db); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	csvParser := parser.NewCSVParser(cfg.CSVPath, logger)
	data, err := csvParser.Parse()
	if err != nil {
		return fmt.Errorf("failed to parse CSV: %w", err)
	}

	repo := database.NewRepository(db)

	for _, item := range data {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := repo.SaveQuarterData(item); err != nil {
				logger.Warn("Failed to save quarter data",
					"company", item.Company,
					"year", item.Year,
					"quarter", item.Quarter,
					"error", err)
			}
		}
	}

	logger.Info("Data import completed successfully",
		"records_processed", len(data))

	return nil
}
