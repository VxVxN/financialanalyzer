package handlers

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/VxVxN/financialanalyzer/internal/parser"
)

const maxImportBytes = 8 << 20

// ImportCSV reads a semicolon CSV whose filename is COMPANY_CATEGORY.csv
// (the old cmd/import) and upserts the rows.
func (controller *Controller) ImportCSV(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer f.Close()

	name := path.Base(strings.ReplaceAll(hdr.Filename, "\\", "/"))
	if name == "" || name == "." || name == ".." {
		writeJSONError(w, http.StatusBadRequest, "invalid filename")
		return
	}
	limited := io.LimitReader(f, maxImportBytes+1)
	rows, err := parser.NewCSVParser(name).ParseReader(limited)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid CSV")
		return
	}
	if len(rows) == 0 {
		writeJSONError(w, http.StatusBadRequest, "no data rows in CSV")
		return
	}

	saved := 0
	for _, row := range rows {
		if err := controller.repo.SaveQuarterData(r.Context(), row); err != nil {
			controller.logger.Warn("Failed to save imported row",
				"company", row.Company, "year", row.Year, "quarter", row.Quarter, "error", err)
			continue
		}
		saved++
	}
	if saved == 0 {
		controller.serverError(w, "failed to save imported rows", fmt.Errorf("all %d rows failed", len(rows)))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"company": rows[0].Company,
		"saved":   saved,
		"total":   len(rows),
	})
}
