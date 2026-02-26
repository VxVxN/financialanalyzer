package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

type SaveNoteRequest struct {
	Company string `json:"company"`
	Note    string `json:"note"`
}

type GetNoteResponse struct {
	Company string `json:"company"`
	Note    string `json:"note"`
}

func (controller *Controller) GetCompanyNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	company := r.URL.Query().Get("company")
	company = strings.TrimSpace(company)
	if company == "" {
		http.Error(w, "Company name is required", http.StatusBadRequest)
		return
	}

	note, err := controller.repo.GetCompanyNote(company)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GetNoteResponse{
		Company: company,
		Note:    note,
	})
}

func (controller *Controller) SaveCompanyNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SaveNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Company = strings.TrimSpace(req.Company)
	if req.Company == "" {
		http.Error(w, "Company name is required", http.StatusBadRequest)
		return
	}

	err := controller.repo.SaveCompanyNote(req.Company, req.Note)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Note saved successfully",
	})
}

func (controller *Controller) DeleteCompanyNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	company := r.URL.Query().Get("company")
	company = strings.TrimSpace(company)
	if company == "" {
		http.Error(w, "Company name is required", http.StatusBadRequest)
		return
	}

	err := controller.repo.DeleteCompanyNote(company)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Note deleted successfully",
	})
}
