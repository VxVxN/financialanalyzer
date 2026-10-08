package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/VxVxN/financialanalyzer/internal/fetcher"
	"github.com/VxVxN/financialanalyzer/internal/ops"
)

const maxFetchBody = 64 << 10

// fetchRequest is the JSON body of POST /api/fetch. TickersFile is not
// accepted (no paths from the API); unknown fields are rejected.
type fetchRequest struct {
	Tickers      string `json:"tickers"`
	Banks        string `json:"banks"`
	All          bool   `json:"all"`
	Force        bool   `json:"force"`
	Backfill     bool   `json:"backfill"`
	QuotesOnly   bool   `json:"quotes_only"`
	Concurrency  int    `json:"concurrency"`
	BankFromYear int    `json:"bank_from_year"`
}

// StartFetch starts a background data refresh (the recorded fetch that used
// to be cmd/fetch). The run is logged in fetch_runs; the handler returns
// before it finishes.
func (controller *Controller) StartFetch(w http.ResponseWriter, r *http.Request) {
	if controller.jobs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "jobs not available")
		return
	}
	var body fetchRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFetchBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || dec.More() {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Concurrency < 0 || body.BankFromYear < 0 {
		writeJSONError(w, http.StatusBadRequest, "concurrency and bank_from_year must be >= 0")
		return
	}
	req := fetcher.Request{
		Tickers:      normalizeList(body.Tickers),
		Banks:        normalizeList(body.Banks),
		All:          body.All,
		Force:        body.Force,
		Backfill:     body.Backfill,
		QuotesOnly:   body.QuotesOnly,
		Concurrency:  body.Concurrency,
		BankFromYear: body.BankFromYear,
	}
	if err := controller.jobs.StartFetch(req); err != nil {
		if errors.Is(err, ops.ErrBusy) {
			writeJSONError(w, http.StatusConflict, "a fetch is already running")
			return
		}
		controller.serverError(w, "failed to start fetch", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// normalizeList folds newlines and semicolons into a comma-separated list.
func normalizeList(s string) string {
	s = strings.ReplaceAll(s, "\n", ",")
	s = strings.ReplaceAll(s, ";", ",")
	var parts []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ",")
}
