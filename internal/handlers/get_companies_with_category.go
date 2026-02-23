package handlers

import (
	"encoding/json"
	"net/http"
)

type CompanyWithCategory struct {
	Company  string `json:"company"`
	Category string `json:"category"`
}

func (controller *Controller) GetCompaniesWithCategories(w http.ResponseWriter, r *http.Request) {
	companies, err := controller.repo.GetAllCompaniesWithCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(companies)
}
