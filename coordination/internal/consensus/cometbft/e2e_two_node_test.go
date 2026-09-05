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
)

// TestTwoNodeConsensusAppliesTxOnPeer proves a tx submitted on A is applied on B
// via CometBFT consensus (shared genesis + P2P), not HTTP snapshot sync.
func TestTwoNodeConsensusAppliesTxOnPeer(t *testing.T) {
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
		HomeDir:      homeA,
		ChainID:      "nexusos-e2e",
		RPCListen:    rpcA,
		P2PListen:    p2pA,
		Moniker:      "a",
		Members:      memA,
		IdentityPriv: kpA.PrivateKey,
	})
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	defer func() { _ = nodeA.Stop() }()

	// Wait for A genesis + first block, then copy genesis to B.
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
	genSrc := filepath.Join(homeA, "config", "genesis.json")
	genDst := filepath.Join(homeB, "config", "genesis.json")
	genBytes, err := os.ReadFile(genSrc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(genDst, genBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	peer := nodeA.NodeID() + "@" + PeerListenHostPort(p2pA)
	appB := NewApp(ledB, memB)
	nodeB, err := StartNode(appB, NodeOptions{
		HomeDir:         homeB,
		ChainID:         "nexusos-e2e",
		RPCListen:       freeTCPAddr(t),
		P2PListen:       freeTCPAddr(t),
		Moniker:         "b",
		Members:         memB,
		IdentityPriv:    kpB.PrivateKey,
		PersistentPeers: peer,
	})
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	defer func() { _ = nodeB.Stop() }()

	// Wait until B has executed at least one block via P2P (consensus sync).
	deadline = time.Now().Add(45 * time.Second)
	for appB.Height() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("B did not sync blocks via P2P; A.h=%d B.h=%d peer=%s", appA.Height(), appB.Height(), peer)
		}
		time.Sleep(100 * time.Millisecond)
	}

	digest := "sha256:e2e-cometbft-" + kpA.NodeID[:8]
	tx, err := ledger.NewTx(kpA, ledger.MsgRegisterImage, ledger.ImagePayload{
		Digest: digest,
		Size:   7,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := nodeA.Submit(ctx, tx); err != nil {
		t.Fatalf("submit on A: %v", err)
	}
	if _, ok := ledA.Snapshot().Images[digest]; !ok {
		t.Fatal("A ledger missing image after submit")
	}

	// B must apply via ABCI Commit — no HTTP sync in this test.
	deadline = time.Now().Add(30 * time.Second)
	for {
		if _, ok := ledB.Snapshot().Images[digest]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("B ledger missing image (consensus apply failed); A.h=%d B.h=%d", appA.Height(), appB.Height())
		}
		time.Sleep(100 * time.Millisecond)
	}
	img := ledB.Snapshot().Images[digest]
	if img.Size != 7 {
		t.Fatalf("B image size=%d", img.Size)
	}
}
