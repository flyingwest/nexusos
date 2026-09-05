package cometbft

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallAndLoadBootstrapInfo(t *testing.T) {
	home := t.TempDir()
	genesis := json.RawMessage(`{"genesis_time":"2026-01-01T00:00:00Z","chain_id":"nexusos-test","initial_height":"1","validators":[]}`)
	if err := InstallGenesis(home, genesis); err != nil {
		t.Fatal(err)
	}
	// Second install is a no-op (existing file).
	if err := InstallGenesis(home, json.RawMessage(`{"chain_id":"other"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(GenesisPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(got) {
		t.Fatal("installed genesis not valid JSON")
	}
	var meta struct {
		ChainID string `json:"chain_id"`
	}
	if err := json.Unmarshal(got, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.ChainID != "nexusos-test" {
		t.Fatalf("expected original chain_id, got %q", meta.ChainID)
	}

	if err := os.WriteFile(filepath.Join(home, "p2p-node-id.txt"), []byte("abcd1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := LoadBootstrapInfo(home, "tcp://127.0.0.1:26656")
	if err != nil {
		t.Fatal(err)
	}
	if info.P2PNodeID != "abcd1234" {
		t.Fatalf("node id: %q", info.P2PNodeID)
	}
	if info.Peer != "abcd1234@127.0.0.1:26656" {
		t.Fatalf("peer: %q", info.Peer)
	}
	if info.ChainID != "nexusos-test" {
		t.Fatalf("chain_id: %q", info.ChainID)
	}
}

func TestInstallGenesisRejectsInvalid(t *testing.T) {
	if err := InstallGenesis(t.TempDir(), json.RawMessage(`not-json`)); err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchBootstrap(t *testing.T) {
	home := t.TempDir()
	genesis := json.RawMessage(`{"chain_id":"nexusos-fetch","validators":[]}`)
	if err := InstallGenesis(home, genesis); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "p2p-node-id.txt"), []byte("deadbeef"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := LoadBootstrapInfo(home, "tcp://10.0.0.1:26656")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/cometbft/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(JoinTokenHeader) != "join-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(info)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := FetchBootstrap(context.Background(), FetchBootstrapOptions{
		SeedURL:   srv.URL,
		JoinToken: "join-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Peer != "deadbeef@10.0.0.1:26656" {
		t.Fatalf("peer: %q", got.Peer)
	}

	dst := t.TempDir()
	peer, err := FetchAndInstallGenesis(context.Background(), dst, FetchBootstrapOptions{
		SeedURL:   srv.URL,
		JoinToken: "join-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if peer != got.Peer {
		t.Fatalf("peer mismatch: %q vs %q", peer, got.Peer)
	}
	if _, err := os.Stat(GenesisPath(dst)); err != nil {
		t.Fatal(err)
	}

	if _, err := FetchBootstrap(context.Background(), FetchBootstrapOptions{
		SeedURL:   srv.URL,
		JoinToken: "wrong",
	}); err == nil {
		t.Fatal("expected auth failure")
	}
}
