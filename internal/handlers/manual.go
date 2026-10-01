package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

const (
	// minManualYear is the earliest year a manual entry may describe.
	minManualYear = 1990
	// maxManualBody caps a manual-entry request body.
	maxManualBody = 64 << 10
	// maxManualValue is the column limit (NUMERIC(15,2) is below 1e13).
	maxManualValue = 1e13
)

// ManualFinancialsResponse lists a company's manual entries.
type ManualFinancialsResponse struct {
	Company string                    `json:"company"`
	Entries []models.ManualFinancials `json:"entries"`
}

// GetManualFinancials returns a company's manual entries, by year.
func (controller *Controller) GetManualFinancials(w http.ResponseWriter, r *http.Request) {
	company := strings.TrimSpace(r.URL.Query().Get("company"))
	if company == "" {
		writeJSONError(w, http.StatusBadRequest, "company name is required")
		return
	}
	entries, err := controller.repo.GetManualFinancials(r.Context(), company)
	if err != nil {
		controller.serverError(w, "failed to get manual financials", err)
		return
	}
	if entries == nil {
		entries = []models.ManualFinancials{}
	}
	writeJSON(w, http.StatusOK, ManualFinancialsResponse{Company: company, Entries: entries})
}

// SaveManualFinancials stores one company-year entry, replacing that year's
// previous entry entirely (a field sent as null or left out clears it).
func (controller *Controller) SaveManualFinancials(w http.ResponseWriter, r *http.Request) {
	var m models.ManualFinancials
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxManualBody))
	dec.DisallowUnknownFields() // a typo in a field name must not silently drop a figure
	if err := dec.Decode(&m); err != nil || dec.More() {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	m.Company = strings.TrimSpace(m.Company)
	m.UpdatedAt = time.Time{} // set by the database; ignored on input
	if msg := controller.validateManual(m); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}

	if err := controller.repo.SaveManualFinancials(r.Context(), m); err != nil {
		if errors.Is(err, database.ErrCompanyNotFound) {
			writeJSONError(w, http.StatusNotFound, "company not found")
			return
		}
		controller.serverError(w, "failed to save manual financials", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Manual financials saved"})
}

// validateManual returns why an entry is rejected, or "" if it is valid.
func (controller *Controller) validateManual(m models.ManualFinancials) string {
	if m.Company == "" {
		return "company name is required"
	}
	if maxYear := controller.now().Year(); m.Year < minManualYear || m.Year > maxYear {
		return fmt.Sprintf("year must be in %d..%d", minManualYear, maxYear)
	}
	if m.IsEmpty() {
		return "enter at least one figure"
	}
	// Amounts that cannot be negative; profits, cash flow and equity can.
	nonNegative := map[string]bool{"revenue": true, "capex": true, "debt": true, "cash": true, "dividends": true}
	for _, f := range manualFields {
		v := f.get(m)
		if v == nil {
			continue
		}
		if nonNegative[f.key] && *v < 0 {
			return f.key + " must not be negative"
		}
		// NUMERIC(15,2) holds less than 1e13; anything near that is almost
		// surely thousands of RUB typed into a billions field.
		if math.Abs(*v) >= maxManualValue {
			return fmt.Sprintf("%s is too large: values are in billions of RUB", f.key)
		}
	}
	return ""
}

// DeleteManualFinancials removes a company-year entry; the fetched figures
// for that year show again.
func (controller *Controller) DeleteManualFinancials(w http.ResponseWriter, r *http.Request) {
	company := strings.TrimSpace(r.URL.Query().Get("company"))
	year, err := strconv.Atoi(r.URL.Query().Get("year"))
	if company == "" || err != nil {
		writeJSONError(w, http.StatusBadRequest, "company and year are required")
		return
	}
	if err := controller.repo.DeleteManualFinancials(r.Context(), company, year); err != nil {
		if errors.Is(err, database.ErrManualNotFound) {
			writeJSONError(w, http.StatusNotFound, "manual entry not found")
			return
		}
		controller.serverError(w, "failed to delete manual financials", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Manual financials deleted"})
}
