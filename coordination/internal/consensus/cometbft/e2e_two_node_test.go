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

// TestTwoNodeJoinMemberAfterStart proves pair-before-CometBFT-start is no longer
// required: nodes start with only local membership, JoinMember goes through
// consensus, then an image tx applies on the peer.
func TestTwoNodeJoinMemberAfterStart(t *testing.T) {
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

	bind := func(led *ledger.Store, mem *membership.Store) {
		led.BindMembership(func(recs map[string]ledger.MemberRecord) error {
			list := make([]membership.Member, 0, len(recs))
			for _, m := range recs {
				list = append(list, membership.Member{
					NodeID: m.NodeID, PublicKey: m.PublicKey, Addresses: m.Addresses, Label: m.Label,
				})
			}
			return mem.ReplaceAll(list)
		})
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
	bind(ledA, memA)
	bind(ledB, memB)

	// Only local self — no pair-before-start.
	for _, pair := range []struct {
		mem *membership.Store
		kp  *identity.KeyPair
		lab string
	}{
		{memA, kpA, "a"},
		{memB, kpB, "b"},
	} {
		if err := pair.mem.Upsert(membership.Member{
			NodeID: pair.kp.NodeID, PublicKey: pair.kp.PublicJSON().PublicKey, Label: pair.lab,
		}); err != nil {
			t.Fatal(err)
		}
	}

	appA := NewApp(ledA, memA)
	rpcA := freeTCPAddr(t)
	p2pA := freeTCPAddr(t)
	homeA := filepath.Join(dirA, "cometbft")
	nodeA, err := StartNode(appA, NodeOptions{
		HomeDir: homeA, ChainID: "nexusos-e2e-join", RPCListen: rpcA, P2PListen: p2pA,
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
		HomeDir: homeB, ChainID: "nexusos-e2e-join", RPCListen: freeTCPAddr(t), P2PListen: freeTCPAddr(t),
		Moniker: "b", Members: memB, IdentityPriv: kpB.PrivateKey, PersistentPeers: peer,
	})
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	defer func() { _ = nodeB.Stop() }()

	deadline = time.Now().Add(45 * time.Second)
	for appB.Height() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("B sync timeout; A.h=%d B.h=%d", appA.Height(), appB.Height())
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Admit B via consensus (no local members.json pair beforehand on both).
	join, err := ledger.NewTx(kpA, ledger.MsgJoinMember, ledger.MemberPayload{
		NodeID: kpB.NodeID, PublicKey: kpB.PublicJSON().PublicKey, Label: "peer",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := nodeA.Submit(ctx, join); err != nil {
		t.Fatalf("JoinMember: %v", err)
	}

	deadline = time.Now().Add(30 * time.Second)
	for {
		if _, ok := ledB.Snapshot().Members[kpB.NodeID]; ok {
			if _, okA := ledB.Snapshot().Members[kpA.NodeID]; okA {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("B missing ledger members after JoinMember; A=%v B=%v",
				ledA.Snapshot().Members, ledB.Snapshot().Members)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Allow ValidatorUpdates (height+2) to settle.
	time.Sleep(3 * time.Second)

	digest := "sha256:e2e-join-" + kpA.NodeID[:8]
	tx, err := ledger.NewTx(kpA, ledger.MsgRegisterImage, ledger.ImagePayload{Digest: digest, Size: 3}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel2()
	if err := nodeA.Submit(ctx2, tx); err != nil {
		t.Fatalf("image submit: %v", err)
	}
	deadline = time.Now().Add(30 * time.Second)
	for {
		if _, ok := ledB.Snapshot().Images[digest]; ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("B missing image; A.h=%d B.h=%d", appA.Height(), appB.Height())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestTwoNodeLeaveMember proves LeaveMember removes a peer from the ledger and
// tracked validator set (power 0), stamps a member tombstone, and refuses
// leaving the last validator.
func TestTwoNodeLeaveMember(t *testing.T) {
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

	bind := func(led *ledger.Store, mem *membership.Store) {
		led.BindMembership(func(recs map[string]ledger.MemberRecord) error {
			list := make([]membership.Member, 0, len(recs))
			for _, m := range recs {
				list = append(list, membership.Member{
					NodeID: m.NodeID, PublicKey: m.PublicKey, Addresses: m.Addresses, Label: m.Label,
				})
			}
			return mem.ReplaceAll(list)
		})
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
	bind(ledA, memA)
	bind(ledB, memB)

	for _, pair := range []struct {
		mem *membership.Store
		kp  *identity.KeyPair
		lab string
	}{
		{memA, kpA, "a"},
		{memB, kpB, "b"},
	} {
		if err := pair.mem.Upsert(membership.Member{
			NodeID: pair.kp.NodeID, PublicKey: pair.kp.PublicJSON().PublicKey, Label: pair.lab,
		}); err != nil {
			t.Fatal(err)
		}
	}

	appA := NewApp(ledA, memA)
	rpcA := freeTCPAddr(t)
	p2pA := freeTCPAddr(t)
	homeA := filepath.Join(dirA, "cometbft")
	nodeA, err := StartNode(appA, NodeOptions{
		HomeDir: homeA, ChainID: "nexusos-e2e-leave", RPCListen: rpcA, P2PListen: p2pA,
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
		HomeDir: homeB, ChainID: "nexusos-e2e-leave", RPCListen: freeTCPAddr(t), P2PListen: freeTCPAddr(t),
		Moniker: "b", Members: memB, IdentityPriv: kpB.PrivateKey, PersistentPeers: peer,
	})
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	defer func() { _ = nodeB.Stop() }()

	deadline = time.Now().Add(45 * time.Second)
	for appB.Height() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("B sync timeout; A.h=%d B.h=%d", appA.Height(), appB.Height())
		}
		time.Sleep(100 * time.Millisecond)
	}

	join, err := ledger.NewTx(kpA, ledger.MsgJoinMember, ledger.MemberPayload{
		NodeID: kpB.NodeID, PublicKey: kpB.PublicJSON().PublicKey, Label: "peer",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := nodeA.Submit(ctx, join); err != nil {
		t.Fatalf("JoinMember: %v", err)
	}

	deadline = time.Now().Add(30 * time.Second)
	for {
		if _, ok := ledB.Snapshot().Members[kpB.NodeID]; ok {
			if _, okA := ledB.Snapshot().Members[kpA.NodeID]; okA {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("B missing ledger members after JoinMember; A=%v B=%v",
				ledA.Snapshot().Members, ledB.Snapshot().Members)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Wait for ValidatorUpdates (height+2) so both are active validators.
	deadline = time.Now().Add(30 * time.Second)
	bHex := kpB.PublicJSON().PublicKey
	for {
		if _, ok := appA.ValidatorSetSnapshot()[bHex]; ok {
			if _, okB := appB.ValidatorSetSnapshot()[bHex]; okB {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("B not in validator set; A=%v B=%v",
				appA.ValidatorSetSnapshot(), appB.ValidatorSetSnapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}

	leave, err := ledger.NewTx(kpA, ledger.MsgLeaveMember, ledger.MemberPayload{NodeID: kpB.NodeID}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel2()
	if err := nodeA.Submit(ctx2, leave); err != nil {
		t.Fatalf("LeaveMember: %v", err)
	}

	deadline = time.Now().Add(30 * time.Second)
	for {
		_, inA := ledA.Snapshot().Members[kpB.NodeID]
		_, inB := ledB.Snapshot().Members[kpB.NodeID]
		_, tombA := ledA.Snapshot().Tombstones[ledger.MemberTomb(kpB.NodeID)]
		_, tombB := ledB.Snapshot().Tombstones[ledger.MemberTomb(kpB.NodeID)]
		if !inA && !inB && tombA && tombB {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("leave not applied; A.members=%v B.members=%v A.tombs=%v B.tombs=%v",
				ledA.Snapshot().Members, ledB.Snapshot().Members,
				ledA.Snapshot().Tombstones, ledB.Snapshot().Tombstones)
		}
		time.Sleep(100 * time.Millisecond)
	}

	deadline = time.Now().Add(30 * time.Second)
	for {
		_, inA := appA.ValidatorSetSnapshot()[bHex]
		_, inB := appB.ValidatorSetSnapshot()[bHex]
		if !inA && !inB {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("B still in validator set after leave; A=%v B=%v",
				appA.ValidatorSetSnapshot(), appB.ValidatorSetSnapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := ledger.CanLeaveMember(ledA.Snapshot().Members, kpA.NodeID); err == nil {
		t.Fatal("expected last-validator refuse after B left")
	}
	last, err := ledger.NewTx(kpA, ledger.MsgLeaveMember, ledger.MemberPayload{NodeID: kpA.NodeID}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ctx3, cancel3 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel3()
	if err := nodeA.Submit(ctx3, last); err == nil {
		t.Fatal("expected Submit of last LeaveMember to fail")
	}
	if _, ok := ledA.Snapshot().Members[kpA.NodeID]; !ok {
		t.Fatal("A must remain in ledger after refused last leave")
	}
}
