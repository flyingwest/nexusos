package httpapi

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// withLogging logs method, path, status, and duration.
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// withAuth enforces a bearer token on operator routes.
// If token is empty, auth is disabled (only legal when the process was started with --dev).
func withAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health and node-to-node protocol are authenticated by signatures, not the operator token.
		// CometBFT bootstrap may authenticate with join-token (handler checks).
		if r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/v1/net/") || r.URL.Path == "/v1/cometbft/bootstrap" {
			next.ServeHTTP(w, r)
			return
		}
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") || strings.TrimPrefix(h, "Bearer ") != token {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
