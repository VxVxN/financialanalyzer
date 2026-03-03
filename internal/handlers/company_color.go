package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

type CompanyColorRequest struct {
	Company string `json:"company"`
	Color   string `json:"color"`
}

func (controller *Controller) SaveCompanyColor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CompanyColorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Company = strings.TrimSpace(req.Company)
	req.Color = strings.TrimSpace(req.Color)

	if req.Company == "" {
		http.Error(w, "Company name is required", http.StatusBadRequest)
		return
	}

	if req.Color == "" {
		req.Color = "#000000"
	}

	err := controller.repo.SaveCompanyColor(req.Company, req.Color)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Color saved successfully",
	})
}

func (controller *Controller) DeleteCompanyColor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	company := r.URL.Query().Get("company")
	if company == "" {
		http.Error(w, "Company parameter is required", http.StatusBadRequest)
		return
	}

	err := controller.repo.DeleteCompanyColor(company)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Color deleted successfully",
	})
}

type CompaniesColorsResponse struct {
	Colors map[string]string `json:"colors"`
}

func (controller *Controller) GetCompaniesColors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	category := r.URL.Query().Get("category")

	colors, err := controller.repo.GetCompaniesColorsByCategory(category)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CompaniesColorsResponse{
		Colors: colors,
	})
}
