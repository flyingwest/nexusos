package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithAuthRejectsMissingBearer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /v1/node", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := withAuth("secret", mux)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/node", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rr.Code)
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/v1/node", nil)
	req2.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("got %d", rr2.Code)
	}

	rr3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("health should bypass auth, got %d", rr3.Code)
	}
}

func TestWithAuthDisabledWhenEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/node", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := withAuth("", mux)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/node", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("empty token should disable auth, got %d", rr.Code)
	}
}
