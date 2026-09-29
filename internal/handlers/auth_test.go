package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireBasicAuth(t *testing.T) {
	h := RequireBasicAuth("admin", "s3cret")(okHandler())

	tests := []struct {
		name       string
		user, pass string
		setAuth    bool
		want       int
	}{
		{name: "no credentials", want: http.StatusUnauthorized},
		{name: "wrong password", user: "admin", pass: "nope", setAuth: true, want: http.StatusUnauthorized},
		{name: "wrong user", user: "root", pass: "s3cret", setAuth: true, want: http.StatusUnauthorized},
		{name: "password prefix", user: "admin", pass: "s3cre", setAuth: true, want: http.StatusUnauthorized},
		{name: "valid", user: "admin", pass: "s3cret", setAuth: true, want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, "/api/companies", nil)
			if tt.setAuth {
				req.SetBasicAuth(tt.user, tt.pass)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if tt.want == http.StatusUnauthorized && !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Basic ") {
				t.Errorf("missing Basic WWW-Authenticate challenge, got %q", rec.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestRequireJSONBody(t *testing.T) {
	h := RequireJSONBody(okHandler())

	tests := []struct {
		name        string
		method      string
		body        string
		contentType string
		want        int
	}{
		{name: "json body", method: http.MethodPost, body: `{"a":1}`, contentType: "application/json", want: http.StatusOK},
		{name: "json with charset", method: http.MethodPost, body: `{"a":1}`, contentType: "application/json; charset=utf-8", want: http.StatusOK},
		{name: "text/plain csrf", method: http.MethodPost, body: `{"a":1}`, contentType: "text/plain", want: http.StatusUnsupportedMediaType},
		{name: "form post", method: http.MethodPost, body: `a=1`, contentType: "application/x-www-form-urlencoded", want: http.StatusUnsupportedMediaType},
		{name: "body without type", method: http.MethodPost, body: `{"a":1}`, want: http.StatusUnsupportedMediaType},
		{name: "bodyless delete", method: http.MethodDelete, want: http.StatusOK},
		{name: "empty form post", method: http.MethodPost, contentType: "application/x-www-form-urlencoded", want: http.StatusUnsupportedMediaType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/company-note", strings.NewReader(tt.body))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
