package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
	"github.com/nexusos/coordination/internal/runtime"
	"github.com/nexusos/coordination/internal/state"
)

func TestOperatorUIEmbeddedAndPublic(t *testing.T) {
	dir := t.TempDir()
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.NewStore(dir, kp.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	led, err := ledger.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	mem, err := membership.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Addr:     "127.0.0.1:0",
		APIToken: "secret-token",
		KeyPair:  kp,
		Runtime:  runtime.NewMockRuntime(),
		Store:    st,
		Ledger:   led,
		Members:  mem,
		Mode:     "permissioned",
	})
	h := srv.Handler()

	// /ui/ must not require bearer token (shell only; API still protected).
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/ status=%d body=%s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "NexusOS Operator") {
		snip := string(body)
		if len(snip) > 120 {
			snip = snip[:120]
		}
		t.Fatalf("index missing title marker: %q", snip)
	}

	for _, path := range []string{"/ui/app.js", "/ui/styles.css"} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, rec.Code)
		}
		if rec.Body.Len() < 100 {
			t.Fatalf("GET %s unexpectedly small body", path)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /ui status=%d want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/ui/" {
		t.Fatalf("Location=%q", loc)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/node", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/node without token status=%d", rec.Code)
	}
}
