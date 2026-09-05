package cometbft

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
	"github.com/nexusos/coordination/internal/orchestrate"
	"github.com/nexusos/coordination/internal/runtime"
)

// TestTwoNodeColdMigrationProveProposeComplete updates placement current_node
// across two CometBFT nodes using the mock runtime + migration controller
// (local checkpoint/restore without HTTP peer transfer).
func TestTwoNodeColdMigrationProposeComplete(t *testing.T) {
	dir := t.TempDir()
	dirA := filepath.Join(dir, "a")
	dirB := filepath.Join(dir, "b")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	kpA, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	kpB, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}

	ledA, err := ledger.NewStore(dirA)
	if err != nil {
		t.Fatal(err)
	}
	ledB, err := ledger.NewStore(dirB)
	if err != nil {
		t.Fatal(err)
	}
	memA, err := membership.NewStore(dirA)
	if err != nil {
		t.Fatal(err)
	}
	memB, err := membership.NewStore(dirB)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []membership.Member{
		{NodeID: kpA.NodeID, PublicKey: kpA.PublicJSON().PublicKey, Label: "a"},
		{NodeID: kpB.NodeID, PublicKey: kpB.PublicJSON().PublicKey, Label: "b"},
	} {
		if err := memA.Upsert(m); err != nil {
			t.Fatal(err)
		}
		if err := memB.Upsert(m); err != nil {
			t.Fatal(err)
		}
	}

	appA := NewApp(ledA, memA)
	rpcA := freeTCPAddr(t)
	p2pA := freeTCPAddr(t)
	homeA := filepath.Join(dirA, "cometbft")
	nodeA, err := StartNode(appA, NodeOptions{
		HomeDir: homeA, ChainID: "nexusos-mig-e2e", RPCListen: rpcA, P2PListen: p2pA,
		Moniker: "a", Members: memA, IdentityPriv: kpA.PrivateKey,
	})
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	defer func() { _ = nodeA.Stop() }()

	deadline := time.Now().Add(25 * time.Second)
	for appA.Height() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("A height timeout; h=%d", appA.Height())
		}
		time.Sleep(50 * time.Millisecond)
	}

	homeB := filepath.Join(dirB, "cometbft")
	if err := os.MkdirAll(filepath.Join(homeB, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	genBytes, err := os.ReadFile(filepath.Join(homeA, "config", "genesis.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeB, "config", "genesis.json"), genBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	peer := nodeA.NodeID() + "@" + PeerListenHostPort(p2pA)
	appB := NewApp(ledB, memB)
	nodeB, err := StartNode(appB, NodeOptions{
		HomeDir: homeB, ChainID: "nexusos-mig-e2e", RPCListen: freeTCPAddr(t), P2PListen: freeTCPAddr(t),
		Moniker: "b", Members: memB, IdentityPriv: kpB.PrivateKey, PersistentPeers: peer,
	})
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	defer func() { _ = nodeB.Stop() }()

	deadline = time.Now().Add(45 * time.Second)
	for appB.Height() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("B sync timeout")
		}
		time.Sleep(100 * time.Millisecond)
	}

	rtA := runtime.NewMockRuntime()
	rtB := runtime.NewMockRuntime()
	ctx := context.Background()
	info, err := rtA.Start(ctx, runtime.StartOptions{ID: "migc", ImageRef: "alpine:mig-e2e"})
	if err != nil {
		t.Fatal(err)
	}

	create, err := ledger.NewTx(kpA, ledger.MsgCreateContainer, ledger.ContainerPayload{
		ContainerID: "migc",
		ImageDigest: info.ImageDigest,
		Desired:     ledger.DesiredRunning,
		CurrentNode: kpA.NodeID,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctxSub, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := nodeA.Submit(ctxSub, create); err != nil {
		t.Fatalf("create: %v", err)
	}

	deadline = time.Now().Add(30 * time.Second)
	for {
		if _, ok := ledB.Snapshot().Containers["migc"]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("B missing container")
		}
		time.Sleep(100 * time.Millisecond)
	}

	ctrlA := &orchestrate.MigrationController{
		KP: kpA, Ledger: ledA, Runtime: rtA, Submit: nodeA, NodeID: kpA.NodeID,
		ArtifactDir: filepath.Join(dirA, "mig"),
	}
	migID := "e2e-mig-1"
	prop, err := ledger.NewTx(kpA, ledger.MsgProposeMigration, ledger.ProposeMigrationPayload{
		MigrationID: migID, ContainerID: "migc", FromNode: kpA.NodeID, ToNode: kpB.NodeID,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(ctx, 20*time.Second)
	defer cancel2()
	if err := nodeA.Submit(ctx2, prop); err != nil {
		t.Fatalf("propose: %v", err)
	}

	// Wait for ProposeMigration to land on both ledgers before Complete on B.
	deadline = time.Now().Add(30 * time.Second)
	for {
		_, okA := ledA.Snapshot().Migrations[migID]
		_, okB := ledB.Snapshot().Migrations[migID]
		if okA && okB {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("propose not applied; A=%v B=%v", ledA.Snapshot().Migrations, ledB.Snapshot().Migrations)
		}
		time.Sleep(100 * time.Millisecond)
	}

	bundle, err := ctrlA.LocalCheckpoint(ctx, migID, "migc")
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	_ = rtA.Remove(ctx, "migc")

	ctrlB := &orchestrate.MigrationController{
		KP: kpB, Ledger: ledB, Runtime: rtB, Submit: nodeB, NodeID: kpB.NodeID,
		ArtifactDir: filepath.Join(dirB, "mig"),
	}
	if err := ctrlB.LocalRestore(ctx, *bundle); err != nil {
		t.Fatalf("restore: %v", err)
	}

	comp, err := ledger.NewTx(kpB, ledger.MsgCompleteMigration, ledger.CompleteMigrationPayload{
		MigrationID: migID, CheckpointHash: bundle.CheckpointHash,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctx3, cancel3 := context.WithTimeout(ctx, 20*time.Second)
	defer cancel3()
	if err := nodeB.Submit(ctx3, comp); err != nil {
		t.Fatalf("complete: %v", err)
	}

	deadline = time.Now().Add(30 * time.Second)
	for {
		a := ledA.Snapshot()
		b := ledB.Snapshot()
		ca, okA := a.Containers["migc"]
		cb, okB := b.Containers["migc"]
		ma, okMA := a.Migrations[migID]
		mb, okMB := b.Migrations[migID]
		if okA && okB && okMA && okMB &&
			ca.CurrentNode == kpB.NodeID && cb.CurrentNode == kpB.NodeID &&
			ca.Desired == ledger.DesiredRunning &&
			ma.Status == ledger.MigrationSuccess && mb.Status == ledger.MigrationSuccess {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("placement not updated; A=%+v B=%+v migA=%+v migB=%+v", ca, cb, ma, mb)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := rtB.Get(ctx, "migc"); err != nil {
		t.Fatal("dest runtime missing restored container")
	}
}
