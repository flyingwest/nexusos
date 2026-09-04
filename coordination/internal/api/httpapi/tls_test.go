package httpapi

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/config"
	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
	"github.com/nexusos/coordination/internal/runtime"
	"github.com/nexusos/coordination/internal/state"
)

func TestTLSListenAndServeSmoke(t *testing.T) {
	dir := t.TempDir()
	cert, key, err := config.EnsureDevTLS(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	kp, err := identity.LoadOrCreate(dir)
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

	api := New(Options{
		Addr:        "127.0.0.1:0",
		APIToken:    "test-token",
		TLSCertFile: cert,
		TLSKeyFile:  key,
		Mode:        "permissioned",
		KeyPair:     kp,
		Runtime:     runtime.NewMockRuntime(),
		Store:       st,
		Ledger:      led,
		Members:     mem,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	errCh := make(chan error, 1)
	go func() { errCh <- api.server.ServeTLS(ln, cert, key) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = api.Shutdown(ctx)
		<-errCh
	})

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	var resp *http.Response
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = client.Get("https://" + addr + "/health")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("TLS health: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}

	resp2, err := client.Get("https://" + addr + "/v1/node")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, "https://"+addr+"/v1/node", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp3, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("authed node: %d", resp3.StatusCode)
	}
}
