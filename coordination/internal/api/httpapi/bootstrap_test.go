package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nexusos/coordination/internal/consensus/cometbft"
	"github.com/nexusos/coordination/internal/identity"
)

func TestCometBFTBootstrapJoinTokenAndAPIToken(t *testing.T) {
	home := t.TempDir()
	gen := []byte(`{"chain_id":"nexusos-boot","validators":[]}`)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "genesis.json"), gen, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "p2p-node-id.txt"), []byte("peerid1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	kp, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Addr:         "127.0.0.1:0",
		APIToken:     "api-secret",
		JoinToken:    "join-secret",
		KeyPair:      kp,
		CometBFTHome: home,
		CometBFTP2P:  "tcp://127.0.0.1:26656",
	})
	h := s.Handler()

	// No auth → 401
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/cometbft/bootstrap", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}

	// Join-token OK
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/v1/cometbft/bootstrap", nil)
	req2.Header.Set(cometbft.JoinTokenHeader, "join-secret")
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("join-token: want 200, got %d body=%s", rr2.Code, rr2.Body.String())
	}
	var info cometbft.BootstrapInfo
	if err := json.Unmarshal(rr2.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Peer != "peerid1@127.0.0.1:26656" {
		t.Fatalf("peer=%q", info.Peer)
	}

	// API token OK
	rr3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/v1/cometbft/bootstrap", nil)
	req3.Header.Set("Authorization", "Bearer api-secret")
	h.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("api token: want 200, got %d", rr3.Code)
	}
}
