package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/runtime"
	"github.com/nexusos/coordination/internal/state"
)

type fakeHeight int64

func (f fakeHeight) Height() int64 { return int64(f) }

func testServer(t *testing.T) *Server {
	t.Helper()
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := state.NewStore(dir, kp.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	led, err := ledger.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = led.UpsertNode(ledger.NodeRecord{NodeID: kp.NodeID, Status: ledger.StatusOnline, Mode: "permissioned"})
	return New(Options{
		Addr:           ":0",
		APIToken:       "secret",
		KeyPair:        kp,
		Runtime:        runtime.NewMockRuntime(),
		Store:          st,
		Ledger:         led,
		Mode:           "permissioned",
		HeightProvider: fakeHeight(7),
	})
}

func TestVersionEndpoint(t *testing.T) {
	s := testServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	req.Header.Set("Authorization", "Bearer secret")
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var info map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["version"] == nil || info["go_version"] == nil {
		t.Fatalf("missing fields: %v", info)
	}
}

func TestHealthRicher(t *testing.T) {
	s := testServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{`"status":"ok"`, `"live":true`, `"summary"`, `"engine"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
}

func TestReadyAndMetricsAuth(t *testing.T) {
	s := testServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/ready", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ready should require auth, got %d", rr.Code)
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/v1/ready", nil)
	req2.Header.Set("Authorization", "Bearer secret")
	s.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("ready: %d %s", rr2.Code, rr2.Body.String())
	}

	rr3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req3.Header.Set("Authorization", "Bearer secret")
	s.Handler().ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("metrics: %d", rr3.Code)
	}
	if !strings.Contains(rr3.Body.String(), "nexusos_containers") {
		t.Fatalf("metrics body: %s", rr3.Body.String())
	}
	if !strings.Contains(rr3.Body.String(), "nexusos_consensus_height 7") {
		t.Fatalf("expected height gauge, got %s", rr3.Body.String())
	}
}

func TestDrainCordonAPI(t *testing.T) {
	s := testServer(t)
	id := s.kp.NodeID

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/nodes/"+id+"/cordon", nil)
	req.Header.Set("Authorization", "Bearer secret")
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("cordon: %d %s", rr.Code, rr.Body.String())
	}
	n, _ := s.ledger.GetNode(id)
	if n.Status != ledger.StatusDraining {
		t.Fatalf("status=%s", n.Status)
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/nodes/"+id+"/drain", strings.NewReader(`{"evacuate":false}`))
	req2.Header.Set("Authorization", "Bearer secret")
	req2.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("drain: %d %s", rr2.Code, rr2.Body.String())
	}

	rr3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/v1/nodes/"+id+"/uncordon", nil)
	req3.Header.Set("Authorization", "Bearer secret")
	s.Handler().ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("uncordon: %d %s", rr3.Code, rr3.Body.String())
	}
	n, _ = s.ledger.GetNode(id)
	if n.Status != ledger.StatusOnline {
		t.Fatalf("status=%s", n.Status)
	}
}
