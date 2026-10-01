package application

import (
	"database/sql"
	"fmt"

	financialanalyzer "github.com/VxVxN/financialanalyzer"
	"github.com/VxVxN/financialanalyzer/internal/config"
	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/notify"
)

type Application struct {
	db   *sql.DB
	Repo *database.Repository
	// Notifier reports failed data refreshes; nil when not configured.
	Notifier notify.Notifier
}

func Init(cfg *config.Config) (*Application, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	db, err := database.NewConnection(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	repo := database.NewRepository(db)

	app := &Application{
		db:   db,
		Repo: repo,
	}
	if cfg.NotifyEnabled() {
		app.Notifier = notify.NewTelegram(cfg.TelegramBotToken, cfg.TelegramChatID)
	}
	return app, nil
}

func (app *Application) MigrateDB() error {
	if err := database.RunMigrations(app.db, financialanalyzer.MigrationsFS); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}
	return nil
}

func (app *Application) Close() {
	app.db.Close()
}
