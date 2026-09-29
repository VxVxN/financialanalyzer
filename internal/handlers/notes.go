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
	company := strings.TrimSpace(r.URL.Query().Get("company"))
	if company == "" {
		writeJSONError(w, http.StatusBadRequest, "company name is required")
		return
	}

	note, err := controller.repo.GetCompanyNote(r.Context(), company)
	if err != nil {
		controller.serverError(w, "failed to get company note", err)
		return
	}

	writeJSON(w, http.StatusOK, GetNoteResponse{Company: company, Note: note})
}

func (controller *Controller) SaveCompanyNote(w http.ResponseWriter, r *http.Request) {
	var req SaveNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Company = strings.TrimSpace(req.Company)
	if req.Company == "" {
		writeJSONError(w, http.StatusBadRequest, "company name is required")
		return
	}

	if err := controller.repo.SaveCompanyNote(r.Context(), req.Company, req.Note); err != nil {
		controller.serverError(w, "failed to save company note", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Note saved successfully"})
}

func (controller *Controller) DeleteCompanyNote(w http.ResponseWriter, r *http.Request) {
	company := strings.TrimSpace(r.URL.Query().Get("company"))
	if company == "" {
		writeJSONError(w, http.StatusBadRequest, "company name is required")
		return
	}

	if err := controller.repo.DeleteCompanyNote(r.Context(), company); err != nil {
		controller.serverError(w, "failed to delete company note", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Note deleted successfully"})
}
