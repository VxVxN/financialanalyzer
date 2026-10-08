package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/VxVxN/financialanalyzer/internal/analytics"
)

// capReviewRequest classifies one capitalization jump. An empty kind deletes
// the stored review.
type capReviewRequest struct {
	Company string `json:"company"`
	From    int    `json:"from"`
	To      int    `json:"to"`
	Kind    string `json:"kind"`
}

func (controller *Controller) SaveCapReview(w http.ResponseWriter, r *http.Request) {
	var req capReviewRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Company = strings.TrimSpace(req.Company)
	if req.Company == "" || utf8.RuneCountInString(req.Company) > 32 || strings.ContainsAny(req.Company, "/\\") {
		writeJSONError(w, http.StatusBadRequest, "company is required")
		return
	}
	year := controller.now().Year()
	if req.From < 1990 || req.To != req.From+1 || req.To > year+1 {
		writeJSONError(w, http.StatusBadRequest, "from and to must be consecutive years")
		return
	}
	if req.Kind == "" {
		if err := controller.repo.DeleteCapReview(r.Context(), req.Company, req.From, req.To); err != nil {
			controller.serverError(w, "failed to delete cap review", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if !analytics.ValidCapReview(req.Kind) {
		writeJSONError(w, http.StatusBadRequest, "unknown review kind")
		return
	}
	if err := controller.repo.SaveCapReview(r.Context(), analytics.CapReview{
		Company: req.Company, From: req.From, To: req.To, Kind: req.Kind,
	}); err != nil {
		controller.serverError(w, "failed to save cap review", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
