package handlers

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/parser"
)

// ImportDividends reads a semicolon CSV of annual dividends (one company-year
// per row) and stores only that column, so an IFRS entry for the same year
// is not replaced.
func (controller *Controller) ImportDividends(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer f.Close()

	batch, err := parser.ParseDividendBatch(io.LimitReader(f, maxImportBytes+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid CSV")
		return
	}

	errs := append([]string{}, batch.RowErrors...)
	saved := 0
	for _, m := range batch.Entries {
		if msg := controller.validateManual(m); msg != "" {
			errs = append(errs, fmt.Sprintf("%s %d: %s", m.Company, m.Year, msg))
			continue
		}
		if err := controller.repo.SaveManualDividends(r.Context(), m); err != nil {
			if errors.Is(err, database.ErrCompanyNotFound) {
				errs = append(errs, fmt.Sprintf("%s %d: company not found", m.Company, m.Year))
				continue
			}
			controller.logger.Warn("Failed to save dividend batch row",
				"company", m.Company, "year", m.Year, "error", err)
			errs = append(errs, fmt.Sprintf("%s %d: save failed", m.Company, m.Year))
			continue
		}
		saved++
	}
	total := len(batch.Entries) + len(batch.RowErrors)
	if saved == 0 {
		if len(errs) == 0 {
			writeJSONError(w, http.StatusBadRequest, "no data rows in CSV")
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "no rows saved",
			"saved":  0,
			"total":  total,
			"errors": errs,
		})
		return
	}
	if errs == nil {
		errs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"saved":  saved,
		"total":  total,
		"errors": errs,
	})
}
