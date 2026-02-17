package handlers

import "github.com/VxVxN/financialanalyzer/internal/database"

type Controller struct {
	repo *database.Repository
}

func NewController(repo *database.Repository) *Controller {
	return &Controller{
		repo: repo,
	}
}
