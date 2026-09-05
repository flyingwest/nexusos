package ledger

import (
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/identity"
)

func TestProposeCompleteMigrationUpdatesPlacement(t *testing.T) {
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	kpB, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 18, 0, 0, 0, time.UTC)
	st := NewState()

	ctrTx, err := NewTx(kp, MsgCreateContainer, ContainerPayload{
		ContainerID: "c-mig",
		ImageDigest: "sha256:img",
		Desired:     DesiredRunning,
		CurrentNode: kp.NodeID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyTx(st, ctrTx); err != nil {
		t.Fatal(err)
	}

	prop, err := NewTx(kp, MsgProposeMigration, ProposeMigrationPayload{
		MigrationID: "m1",
		ContainerID: "c-mig",
		FromNode:    kp.NodeID,
		ToNode:      kpB.NodeID,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyTx(st, prop); err != nil {
		t.Fatal(err)
	}
	if st.Containers["c-mig"].Desired != DesiredMigrating {
		t.Fatalf("desired=%s", st.Containers["c-mig"].Desired)
	}
	if st.Migrations["m1"].Status != MigrationInProgress {
		t.Fatalf("status=%s", st.Migrations["m1"].Status)
	}

	// AppHash must include migrations.
	h1, err := CanonicalHash(*st)
	if err != nil {
		t.Fatal(err)
	}

	comp, err := NewTx(kp, MsgCompleteMigration, CompleteMigrationPayload{
		MigrationID:    "m1",
		CheckpointHash: "abc",
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyTx(st, comp); err != nil {
		t.Fatal(err)
	}
	got := st.Containers["c-mig"]
	if got.CurrentNode != kpB.NodeID || got.Desired != DesiredRunning {
		t.Fatalf("after complete: %+v", got)
	}
	if st.Migrations["m1"].Status != MigrationSuccess || st.Migrations["m1"].CheckpointHash != "abc" {
		t.Fatalf("migration: %+v", st.Migrations["m1"])
	}
	h2, err := CanonicalHash(*st)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("AppHash/CanonicalHash should change after complete")
	}
}

func TestFailMigrationRollback(t *testing.T) {
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	kpB, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 19, 0, 0, 0, time.UTC)
	st := NewState()
	_ = ApplyTx(st, mustTx(t, kp, MsgCreateContainer, ContainerPayload{
		ContainerID: "c2", ImageDigest: "sha256:x", Desired: DesiredRunning, CurrentNode: kp.NodeID,
	}, now))
	_ = ApplyTx(st, mustTx(t, kp, MsgProposeMigration, ProposeMigrationPayload{
		MigrationID: "m2", ContainerID: "c2", FromNode: kp.NodeID, ToNode: kpB.NodeID,
	}, now.Add(time.Second)))
	if err := ApplyTx(st, mustTx(t, kp, MsgFailMigration, FailMigrationPayload{
		MigrationID: "m2", Reason: "boom",
	}, now.Add(2*time.Second))); err != nil {
		t.Fatal(err)
	}
	got := st.Containers["c2"]
	if got.CurrentNode != kp.NodeID || got.Desired != DesiredRunning {
		t.Fatalf("rollback: %+v", got)
	}
	if st.Migrations["m2"].Status != MigrationFailed {
		t.Fatalf("status=%s", st.Migrations["m2"].Status)
	}
}

func TestProposeRejectsWrongFromNode(t *testing.T) {
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	st := NewState()
	_ = ApplyTx(st, mustTx(t, kp, MsgCreateContainer, ContainerPayload{
		ContainerID: "c3", ImageDigest: "sha256:x", Desired: DesiredRunning, CurrentNode: "node-a",
	}, now))
	err = ApplyTx(st, mustTx(t, kp, MsgProposeMigration, ProposeMigrationPayload{
		MigrationID: "m3", ContainerID: "c3", FromNode: "node-b", ToNode: "node-c",
	}, now))
	if err == nil {
		t.Fatal("expected from_node mismatch error")
	}
}

func mustTx(t *testing.T, kp *identity.KeyPair, typ MessageType, payload any, now time.Time) Tx {
	t.Helper()
	tx, err := NewTx(kp, typ, payload, now)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
