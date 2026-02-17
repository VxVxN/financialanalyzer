package handlers

import (
	"encoding/json"
	"net/http"
)

func (controller *Controller) GetCompanies(w http.ResponseWriter, r *http.Request) {
	companies, err := controller.repo.GetAllCompanies()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(companies)
}
