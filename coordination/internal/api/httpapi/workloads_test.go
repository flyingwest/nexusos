package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
	"github.com/nexusos/coordination/internal/orchestrate"
	"github.com/nexusos/coordination/internal/runtime"
	"github.com/nexusos/coordination/internal/state"
)

type immediateSubmit struct {
	led *ledger.Store
}

func (i immediateSubmit) Submit(ctx context.Context, tx ledger.Tx) error {
	return i.led.ApplyTxs([]ledger.Tx{tx})
}

func TestWorkloadCreateListScaleDelete(t *testing.T) {
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
	_ = led.UpsertNode(ledger.NodeRecord{NodeID: kp.NodeID, Status: ledger.StatusOnline, Mode: "permissioned"})
	mem, err := membership.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rt := runtime.NewMockRuntime()
	sub := immediateSubmit{led: led}
	srv := New(Options{
		Addr:        "127.0.0.1:0",
		APIToken:    "test-token",
		Mode:        "permissioned",
		KeyPair:     kp,
		Runtime:     rt,
		Store:       st,
		Ledger:      led,
		Members:     mem,
		TxSubmitter: sub,
	})

	body := map[string]any{
		"workload_id": "web",
		"image_ref":   "nginx:test",
		"replicas":    1,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/workloads", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated && rr.Code != http.StatusAccepted {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}

	rec := &orchestrate.Reconciler{
		KP: kp, Ledger: led, Runtime: rt, Submit: sub, NodeID: kp.NodeID,
	}
	if err := rec.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, err := rt.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 1 {
		t.Fatalf("expected runtime container, got %d; ledger=%v", len(list), led.Snapshot().Containers)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/workloads", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}

	scaleBody, _ := json.Marshal(map[string]any{"replicas": 2})
	req = httptest.NewRequest(http.MethodPost, "/v1/workloads/web/scale", bytes.NewReader(scaleBody))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK && rr.Code != http.StatusAccepted {
		t.Fatalf("scale: %d %s", rr.Code, rr.Body.String())
	}
	wl, ok := led.GetWorkload("web")
	if !ok || wl.Replicas != 2 {
		t.Fatalf("scale not applied: %+v", wl)
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/workloads/web", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
}


func TestWorkloadUpdateRollingStrategy(t *testing.T) {
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
	_ = led.UpsertNode(ledger.NodeRecord{NodeID: kp.NodeID, Status: ledger.StatusOnline, Mode: "permissioned"})
	mem, err := membership.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rt := runtime.NewMockRuntime()
	sub := immediateSubmit{led: led}
	srv := New(Options{
		Addr: "127.0.0.1:0", APIToken: "test-token", Mode: "permissioned",
		KeyPair: kp, Runtime: rt, Store: st, Ledger: led, Members: mem, TxSubmitter: sub,
	})

	body := map[string]any{
		"workload_id": "web", "image_ref": "nginx:v1", "replicas": 2,
		"strategy": "RollingUpdate", "max_unavailable": 1,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/workloads", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated && rr.Code != http.StatusAccepted {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	wl, ok := led.GetWorkload("web")
	if !ok || wl.Strategy != ledger.StrategyRollingUpdate || wl.MaxUnavailable != 1 {
		t.Fatalf("create strategy: %+v", wl)
	}

	upBody, _ := json.Marshal(map[string]any{"image_ref": "nginx:v2", "strategy": "Recreate"})
	req = httptest.NewRequest(http.MethodPut, "/v1/workloads/web", bytes.NewReader(upBody))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK && rr.Code != http.StatusAccepted {
		t.Fatalf("update: %d %s", rr.Code, rr.Body.String())
	}
	wl, ok = led.GetWorkload("web")
	if !ok || wl.Strategy != ledger.StrategyRecreate || wl.Replicas != 2 {
		t.Fatalf("update: %+v", wl)
	}
	if wl.ImageRef != "nginx:v2" || wl.ImageDigest == "" {
		t.Fatalf("image not updated: %+v", wl)
	}
}
