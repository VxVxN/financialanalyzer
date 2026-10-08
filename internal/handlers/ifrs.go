package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/VxVxN/financialanalyzer/internal/ifrs"
	"github.com/VxVxN/financialanalyzer/internal/ops"
)

// ifrsRequest is the JSON body of POST /api/fetch-ifrs. An empty ticker list
// pulls every company already in the database.
type ifrsRequest struct {
	Tickers string `json:"tickers"`
}

// StartIFRS begins a background download of annual IFRS reports from a
// Russian CDN. It fills empty manual fields and does not change figures
// that are already stored.
func (controller *Controller) StartIFRS(w http.ResponseWriter, r *http.Request) {
	if controller.jobs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "jobs not available")
		return
	}
	var body ifrsRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFetchBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || dec.More() {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var companies []string
	if strings.TrimSpace(body.Tickers) != "" {
		all, err := controller.repo.GetAllCompanies(r.Context())
		if err != nil {
			controller.serverError(w, "failed to list companies", err)
			return
		}
		companies = ifrs.MatchCompanies(all, normalizeList(body.Tickers))
		if len(companies) == 0 {
			writeJSONError(w, http.StatusBadRequest, "no matching companies")
			return
		}
	}
	if err := controller.jobs.StartIFRS(companies, ifrs.Years(controller.now())); err != nil {
		if errors.Is(err, ops.ErrBusy) {
			writeJSONError(w, http.StatusConflict, "an IFRS fetch is already running")
			return
		}
		controller.serverError(w, "failed to start IFRS fetch", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// IFRSStatus reports the latest automatic IFRS pull. The page polls it; the
// message is Russian and carries no raw error text.
func (controller *Controller) IFRSStatus(w http.ResponseWriter, r *http.Request) {
	if controller.jobs == nil {
		writeJSON(w, http.StatusOK, ifrs.Status{})
		return
	}
	writeJSON(w, http.StatusOK, controller.jobs.IFRSStatus())
}
