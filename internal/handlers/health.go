package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/version"
)

const readinessTimeout = 3 * time.Second

// Health is a liveness probe: it returns 200 as long as the process is running
// and able to serve HTTP. It does not touch the database.
func (controller *Controller) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Ready is a readiness probe: it returns 200 only when the database is
// reachable, otherwise 503 so orchestrators stop routing traffic.
func (controller *Controller) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()
	if err := controller.repo.Ping(ctx); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// Version reports the build metadata stamped into the binary.
func (controller *Controller) Version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, version.Get())
}
