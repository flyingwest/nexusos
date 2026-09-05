package orchestrate

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/runtime"
)

type memSubmit struct {
	mu  sync.Mutex
	txs []ledger.Tx
	led *ledger.Store
}

func (m *memSubmit) Submit(ctx context.Context, tx ledger.Tx) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.txs = append(m.txs, tx)
	return m.led.ApplyTxs([]ledger.Tx{tx})
}

func TestReconcilerCreatesPlacementAndStartsLocal(t *testing.T) {
	dir := t.TempDir()
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	led, err := ledger.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = led.UpsertNode(ledger.NodeRecord{
		NodeID: kp.NodeID, Status: ledger.StatusOnline, Mode: "permissioned",
	})
	now := time.Now().UTC()
	create, err := ledger.NewTx(kp, ledger.MsgCreateWorkload, ledger.WorkloadPayload{
		WorkloadID:  "app",
		ImageDigest: "sha256:deadbeef",
		ImageRef:    "nginx:alpine",
		Replicas:    1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := led.ApplyTxs([]ledger.Tx{create}); err != nil {
		t.Fatal(err)
	}

	rt := runtime.NewMockRuntime()
	sub := &memSubmit{led: led}
	rec := &Reconciler{
		KP: kp, Ledger: led, Runtime: rt, Submit: sub,
		NodeID: kp.NodeID, Interval: time.Hour,
	}
	if err := rec.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := led.Snapshot()
	cid := ledger.ReplicaContainerID("app", 0)
	c, ok := st.Containers[cid]
	if !ok {
		t.Fatalf("missing placement; txs=%d containers=%v", len(sub.txs), st.Containers)
	}
	if c.CurrentNode != kp.NodeID {
		t.Fatalf("placed on %s want %s", c.CurrentNode, kp.NodeID)
	}
	info, err := rt.Get(context.Background(), cid)
	if err != nil {
		t.Fatalf("runtime missing container: %v", err)
	}
	if info.State != "running" {
		t.Fatalf("state=%s", info.State)
	}
}

func TestApplyWorkloadScale(t *testing.T) {
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	st := ledger.NewState()
	tx, _ := ledger.NewTx(kp, ledger.MsgCreateWorkload, ledger.WorkloadPayload{
		WorkloadID: "w", ImageDigest: "sha256:a", Replicas: 2,
	}, now)
	if err := ledger.ApplyTx(st, tx); err != nil {
		t.Fatal(err)
	}
	scale, _ := ledger.NewTx(kp, ledger.MsgScaleWorkload, ledger.ScaleWorkloadPayload{
		WorkloadID: "w", Replicas: 5,
	}, now.Add(time.Second))
	if err := ledger.ApplyTx(st, scale); err != nil {
		t.Fatal(err)
	}
	if st.Workloads["w"].Replicas != 5 || st.Workloads["w"].Status.Desired != 5 {
		t.Fatalf("%+v", st.Workloads["w"])
	}
	del, _ := ledger.NewTx(kp, ledger.MsgDeleteWorkload, ledger.DeleteWorkloadPayload{WorkloadID: "w"}, now.Add(2*time.Second))
	if err := ledger.ApplyTx(st, del); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Workloads["w"]; ok {
		t.Fatal("workload should be gone")
	}
	if _, ok := st.Tombstones[ledger.WorkloadTomb("w")]; !ok {
		t.Fatal("missing tombstone")
	}
}
