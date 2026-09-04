package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRequiresTokensAndTLSWithoutDev(t *testing.T) {
	c := Default()
	c.NodeID = "n1"
	c.DataDir = t.TempDir()
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for empty api-token")
	}
	c.APIToken = "secret"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for empty join-token")
	}
	c.JoinToken = "join"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing TLS")
	}
	c.TLSCertFile = "/tmp/cert.pem"
	c.TLSKeyFile = "/tmp/key.pem"
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestValidateDevAllowsEmpty(t *testing.T) {
	c := Default()
	c.NodeID = "n1"
	c.DataDir = t.TempDir()
	c.Dev = true
	if err := c.Validate(); err != nil {
		t.Fatalf("dev should allow empty tokens/TLS: %v", err)
	}
}

func TestEnsureDevTLSGeneratesFiles(t *testing.T) {
	dir := t.TempDir()
	cert, key, err := EnsureDevTLS(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cert); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(key); err != nil {
		t.Fatal(err)
	}
	cert2, key2, err := EnsureDevTLS(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cert2 != cert || key2 != key {
		t.Fatalf("paths changed: %s/%s vs %s/%s", cert, key, cert2, key2)
	}
	if filepath.Base(cert) != "dev-tls.crt" {
		t.Fatalf("unexpected cert name: %s", cert)
	}
}

func TestAdvertiseFromListen(t *testing.T) {
	if got := AdvertiseFromListen(":8080", "https"); got != "https://127.0.0.1:8080" {
		t.Fatalf("got %s", got)
	}
	if got := AdvertiseFromListen("127.0.0.1:9", "http"); got != "http://127.0.0.1:9" {
		t.Fatalf("got %s", got)
	}
}

func TestTLSEnabled(t *testing.T) {
	c := Default()
	if c.TLSEnabled() {
		t.Fatal("expected false")
	}
	c.TLSCertFile = "a"
	c.TLSKeyFile = "b"
	if !c.TLSEnabled() {
		t.Fatal("expected true")
	}
}
