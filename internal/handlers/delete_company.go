package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/VxVxN/financialanalyzer/internal/database"
)

type DeleteCompanyRequest struct {
	Company string `json:"company"`
}

func (controller *Controller) DeleteCompany(w http.ResponseWriter, r *http.Request) {
	var req DeleteCompanyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Company = strings.TrimSpace(req.Company)
	if req.Company == "" {
		writeJSONError(w, http.StatusBadRequest, "company name is required")
		return
	}

	err := controller.repo.DeleteCompany(req.Company)
	if err != nil {
		if errors.Is(err, database.ErrCompanyNotFound) {
			writeJSONError(w, http.StatusNotFound, "company not found")
			return
		}
		controller.serverError(w, "failed to delete company", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Company deleted successfully"})
}
