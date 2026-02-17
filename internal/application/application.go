package application

import (
	"database/sql"
	"fmt"

	"github.com/VxVxN/financialanalyzer/internal/config"
	"github.com/VxVxN/financialanalyzer/internal/database"
)

type Application struct {
	db   *sql.DB
	Repo *database.Repository
}

func Init(cfg *config.Config) (*Application, error) {
	db, err := database.NewConnection(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	repo := database.NewRepository(db)

	return &Application{
		db:   db,
		Repo: repo,
	}, nil
}

func (app *Application) Close() {
	app.db.Close()
}
