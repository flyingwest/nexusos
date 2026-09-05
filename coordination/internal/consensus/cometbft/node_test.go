package cometbft

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
)

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return fmt.Sprintf("tcp://%s", ln.Addr().String())
}

func TestNodeSubmitRegisterImageViaMempool(t *testing.T) {
	dir := t.TempDir()
	led, err := ledger.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	members, err := membership.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := members.Upsert(membership.Member{
		NodeID:    kp.NodeID,
		PublicKey: kp.PublicJSON().PublicKey,
		Label:     "self",
	}); err != nil {
		t.Fatal(err)
	}

	app := NewApp(led, members)
	home := filepath.Join(dir, "cometbft")
	node, err := StartNode(app, NodeOptions{
		HomeDir:      home,
		ChainID:      "nexusos-test",
		RPCListen:    freeTCPAddr(t),
		P2PListen:    freeTCPAddr(t),
		Moniker:      "test",
		Members:      members,
		IdentityPriv: kp.PrivateKey,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		if err := node.Stop(); err != nil {
			t.Errorf("stop: %v", err)
		}
	}()

	// Wait until the node is producing blocks (height >= 1).
	deadline := time.Now().Add(20 * time.Second)
	for app.Height() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for first block; height=%d", app.Height())
		}
		time.Sleep(100 * time.Millisecond)
	}

	digest := "sha256:cometbft-smoke-" + kp.NodeID[:8]
	tx, err := ledger.NewTx(kp, ledger.MsgRegisterImage, ledger.ImagePayload{
		Digest: digest,
		Size:   42,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := node.Submit(ctx, tx); err != nil {
		t.Fatalf("submit: %v", err)
	}

	img, ok := led.Snapshot().Images[digest]
	if !ok {
		t.Fatalf("image %s not applied via ABCI; height=%d", digest, app.Height())
	}
	if img.Size != 42 {
		t.Fatalf("size=%d", img.Size)
	}
	if app.Height() < 1 {
		t.Fatalf("expected height >= 1 after commit, got %d", app.Height())
	}
}

func TestSuggestedValidatorsFromMembership(t *testing.T) {
	dir := t.TempDir()
	members, err := membership.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	_ = members.Upsert(membership.Member{
		NodeID:    kp.NodeID,
		PublicKey: kp.PublicJSON().PublicKey,
	})
	_ = members.Upsert(membership.Member{NodeID: "nokey"})

	suggested := SuggestedValidatorsFromMembership(members)
	if len(suggested) != 2 {
		t.Fatalf("len=%d", len(suggested))
	}
	updates := ValidatorUpdatesFromMembership(members)
	if len(updates) != 1 {
		t.Fatalf("updates=%d want 1 (only keyed member)", len(updates))
	}
	if err := WriteMembershipValidatorSnapshot(filepath.Join(dir, "cmt"), members); err != nil {
		t.Fatal(err)
	}
}
