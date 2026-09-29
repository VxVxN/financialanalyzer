package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
	"mime"
	"net/http"
)

// authRealm is sent in WWW-Authenticate so the browser shows its native login
// dialog and then caches the credentials for later requests to the same origin.
const authRealm = `Basic realm="financialanalyzer", charset="UTF-8"`

// RequireBasicAuth rejects requests whose HTTP Basic credentials do not match
// user/password. Both are compared as SHA-256 digests in constant time so
// neither the content nor the length of the secret leaks through timing.
func RequireBasicAuth(user, password string) func(http.Handler) http.Handler {
	wantUser := sha256.Sum256([]byte(user))
	wantPass := sha256.Sum256([]byte(password))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			gotUser := sha256.Sum256([]byte(u))
			gotPass := sha256.Sum256([]byte(p))
			userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:])
			passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:])
			if !ok || userOK&passOK != 1 {
				w.Header().Set("WWW-Authenticate", authRealm)
				writeJSONError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireJSONBody rejects requests that carry a body with a Content-Type other
// than application/json. Browsers cannot send such a request cross-origin
// without a CORS preflight (which this server never approves), so this closes
// the CSRF hole where another site submits a text/plain form whose body happens
// to be valid JSON — with the victim's cached Basic Auth credentials attached.
// Bodyless requests (e.g. DELETE with a query string) pass through.
func RequireJSONBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 || r.Header.Get("Content-Type") != "" {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				writeJSONError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
