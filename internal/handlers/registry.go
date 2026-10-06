package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/VxVxN/financialanalyzer/internal/ops"
)

type registryRequest struct {
	Tickers string `json:"tickers"`
}

// StartRegistry starts a background ticker-registry proposal (the old
// cmd/registry). GET /api/registry polls the result; the text is a
// fetch_tickers.txt candidate to review and commit.
func (controller *Controller) StartRegistry(w http.ResponseWriter, r *http.Request) {
	if controller.jobs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "jobs not available")
		return
	}
	var body registryRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFetchBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || dec.More() {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := controller.jobs.StartRegistry(normalizeList(body.Tickers)); err != nil {
		if errors.Is(err, ops.ErrBusy) {
			writeJSONError(w, http.StatusConflict, "a registry build is already running")
			return
		}
		controller.serverError(w, "failed to start registry", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// RegistryAPI returns the last registry job. Raw source errors stay in the
// server log; the page is unauthenticated.
func (controller *Controller) RegistryAPI(w http.ResponseWriter, r *http.Request) {
	if controller.jobs == nil {
		writeJSON(w, http.StatusOK, ops.RegistryStatus{})
		return
	}
	st := controller.jobs.RegistrySnapshot()
	writeJSON(w, http.StatusOK, st)
}
