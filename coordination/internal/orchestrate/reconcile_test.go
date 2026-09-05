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

func TestRollingUpdateChangesOneReplicaPerPass(t *testing.T) {
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
	oldDigest := "sha256:oldoldoldoldoldoldoldoldoldoldoldoldoldoldoldoldoldoldoldoldoldo"
	newDigest := "sha256:newnewnewnewnewnewnewnewnewnewnewnewnewnewnewnewnewnewnewnewnewn"
	create, err := ledger.NewTx(kp, ledger.MsgCreateWorkload, ledger.WorkloadPayload{
		WorkloadID: "app", ImageDigest: oldDigest, ImageRef: "app:v1",
		Replicas: 3, Strategy: ledger.StrategyRollingUpdate,
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
	for i := uint32(0); i < 3; i++ {
		cid := ledger.ReplicaContainerID("app", i)
		if _, err := rt.Get(context.Background(), cid); err != nil {
			t.Fatalf("missing runtime %s: %v", cid, err)
		}
	}

	up, err := ledger.NewTx(kp, ledger.MsgUpdateWorkload, ledger.WorkloadPayload{
		WorkloadID: "app", ImageDigest: newDigest, ImageRef: "app:v2",
		Replicas: 3, Strategy: ledger.StrategyRollingUpdate,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := led.ApplyTxs([]ledger.Tx{up}); err != nil {
		t.Fatal(err)
	}

	if err := rec.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := led.Snapshot()
	var updated, stale int
	for i := uint32(0); i < 3; i++ {
		c := st.Containers[ledger.ReplicaContainerID("app", i)]
		switch c.ImageDigest {
		case newDigest:
			updated++
		case oldDigest:
			stale++
		default:
			t.Fatalf("replica %d digest=%s", i, c.ImageDigest)
		}
	}
	if updated != 1 || stale != 2 {
		t.Fatalf("after first roll pass: updated=%d stale=%d (want 1 and 2); containers=%v", updated, stale, digestsOf(st, "app"))
	}

	// Second pass advances exactly one more replica.
	if err := rec.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	st = led.Snapshot()
	updated, stale = 0, 0
	for i := uint32(0); i < 3; i++ {
		c := st.Containers[ledger.ReplicaContainerID("app", i)]
		if c.ImageDigest == newDigest {
			updated++
		} else {
			stale++
		}
	}
	if updated != 2 || stale != 1 {
		t.Fatalf("after second roll pass: updated=%d stale=%d", updated, stale)
	}

	if err := rec.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	st = led.Snapshot()
	for i := uint32(0); i < 3; i++ {
		c := st.Containers[ledger.ReplicaContainerID("app", i)]
		if c.ImageDigest != newDigest {
			t.Fatalf("replica %d still %s", i, c.ImageDigest)
		}
		info, err := rt.Get(context.Background(), ledger.ReplicaContainerID("app", i))
		if err != nil {
			t.Fatal(err)
		}
		if info.ImageDigest != newDigest {
			t.Fatalf("runtime %d digest=%s want %s", i, info.ImageDigest, newDigest)
		}
	}
}

func TestRecreateUpdatesAllReplicasInOnePass(t *testing.T) {
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
	oldDigest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newDigest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	create, err := ledger.NewTx(kp, ledger.MsgCreateWorkload, ledger.WorkloadPayload{
		WorkloadID: "app", ImageDigest: oldDigest, ImageRef: "app:v1",
		Replicas: 3, Strategy: ledger.StrategyRecreate,
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

	up, err := ledger.NewTx(kp, ledger.MsgUpdateWorkload, ledger.WorkloadPayload{
		WorkloadID: "app", ImageDigest: newDigest, ImageRef: "app:v2",
		Replicas: 3, Strategy: ledger.StrategyRecreate,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := led.ApplyTxs([]ledger.Tx{up}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := led.Snapshot()
	for i := uint32(0); i < 3; i++ {
		c := st.Containers[ledger.ReplicaContainerID("app", i)]
		if c.ImageDigest != newDigest {
			t.Fatalf("recreate left replica %d on %s", i, c.ImageDigest)
		}
	}
}

func digestsOf(st ledger.State, wl string) map[string]string {
	out := map[string]string{}
	for id, c := range st.Containers {
		if c.WorkloadID == wl {
			out[id] = c.ImageDigest
		}
	}
	return out
}
