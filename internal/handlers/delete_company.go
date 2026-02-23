package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

type DeleteCompanyRequest struct {
	Company string `json:"company"`
}

func (controller *Controller) DeleteCompany(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DeleteCompanyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Company = strings.TrimSpace(req.Company)
	if req.Company == "" {
		http.Error(w, "Company name is required", http.StatusBadRequest)
		return
	}

	err := controller.repo.DeleteCompany(req.Company)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Company deleted successfully",
	})
}
