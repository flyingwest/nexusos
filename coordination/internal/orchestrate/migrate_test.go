package orchestrate

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/runtime"
)

type migSubmit struct {
	led *ledger.Store
}

func (m migSubmit) Submit(ctx context.Context, tx ledger.Tx) error {
	return m.led.ApplyTxs([]ledger.Tx{tx})
}

func TestColdMigrationMockLocal(t *testing.T) {
	dir := t.TempDir()
	kpA, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	kpB, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	led, err := ledger.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rt := runtime.NewMockRuntime()
	ctx := context.Background()
	if _, err := rt.Start(ctx, runtime.StartOptions{ID: "box1", ImageRef: "busybox:mig"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tx, err := ledger.NewTx(kpA, ledger.MsgCreateContainer, ledger.ContainerPayload{
		ContainerID: "box1",
		ImageDigest: "sha256:placeholder",
		Desired:     ledger.DesiredRunning,
		CurrentNode: kpA.NodeID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Align digest with mock after start.
	info, _ := rt.Get(ctx, "box1")
	tx, _ = ledger.NewTx(kpA, ledger.MsgCreateContainer, ledger.ContainerPayload{
		ContainerID: "box1",
		ImageDigest: info.ImageDigest,
		Desired:     ledger.DesiredRunning,
		CurrentNode: kpA.NodeID,
	}, now)
	if err := led.ApplyTxs([]ledger.Tx{tx}); err != nil {
		t.Fatal(err)
	}
	_ = led.UpsertNode(ledger.NodeRecord{NodeID: kpA.NodeID, Status: ledger.StatusOnline, Addresses: []string{"http://a"}})
	_ = led.UpsertNode(ledger.NodeRecord{NodeID: kpB.NodeID, Status: ledger.StatusOnline, Addresses: []string{"http://b"}})

	// Local-only path: same process acts as both source and dest by using NodeID=A
	// then manually restoring isn't needed — Run does remote when to!=self.
	// For unit test without HTTP, migrate to self is rejected; instead exercise
	// propose→checkpoint→restore→complete by setting NodeID to A and ToNode to A
	// is invalid. Use a controller that is BOTH: set NodeID empty trick —
	// implement by running with from=A to=B but NodeID switching:
	// First checkpoint as A, then restore as B in-process via LocalRestore.

	ctrl := &MigrationController{
		KP:          kpA,
		Ledger:      led,
		Runtime:     rt,
		Submit:      migSubmit{led: led},
		NodeID:      kpA.NodeID,
		ArtifactDir: filepath.Join(dir, "arts"),
	}

	// Simulate dest also having this runtime by completing local path when to==from is forbidden.
	// Instead: call LocalCheckpoint + LocalRestore + Complete manually after Propose.
	migID := "unit-mig-1"
	if err := ctrl.submit(ctx, ledger.MsgProposeMigration, ledger.ProposeMigrationPayload{
		MigrationID: migID, ContainerID: "box1", FromNode: kpA.NodeID, ToNode: kpB.NodeID,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	bundle, err := ctrl.LocalCheckpoint(ctx, migID, "box1")
	if err != nil {
		t.Fatal(err)
	}
	// Dest side (same mock runtime after remove):
	_ = rt.Remove(ctx, "box1")
	ctrl.NodeID = kpB.NodeID
	if err := ctrl.LocalRestore(ctx, *bundle); err != nil {
		t.Fatal(err)
	}
	ctrl.KP = kpB
	if err := ctrl.submit(ctx, ledger.MsgCompleteMigration, ledger.CompleteMigrationPayload{
		MigrationID: migID, CheckpointHash: bundle.CheckpointHash,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	st := led.Snapshot()
	if st.Containers["box1"].CurrentNode != kpB.NodeID {
		t.Fatalf("current_node=%s want %s", st.Containers["box1"].CurrentNode, kpB.NodeID)
	}
	if st.Migrations[migID].Status != ledger.MigrationSuccess {
		t.Fatalf("status=%s", st.Migrations[migID].Status)
	}
	if _, err := rt.Get(ctx, "box1"); err != nil {
		t.Fatal("dest runtime missing container")
	}
}

func TestSkipMigrating(t *testing.T) {
	st := ledger.NewState()
	c := ledger.ContainerRecord{ContainerID: "c", Desired: ledger.DesiredMigrating}
	if !SkipMigrating(c, *st) {
		t.Fatal("expected skip")
	}
}
