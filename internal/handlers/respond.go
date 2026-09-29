package handlers

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes v as a JSON response with the given status code. A nil v
// writes only the status (useful for 204-style responses).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeJSONError writes a uniform {"error": "..."} envelope so API clients can
// rely on a consistent error shape across every endpoint.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
