package orchestrate

import (
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/ledger"
)

func TestCordonUncordon(t *testing.T) {
	led, err := ledger.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = led.UpsertNode(ledger.NodeRecord{NodeID: "n1", Status: ledger.StatusOnline})

	n, err := Cordon(led, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if n.Status != ledger.StatusDraining {
		t.Fatalf("status=%s", n.Status)
	}
	if !SchedulingDisabled(n) {
		t.Fatal("expected scheduling disabled")
	}

	n, err = Uncordon(led, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if n.Status != ledger.StatusOnline {
		t.Fatalf("status=%s", n.Status)
	}
}

func TestCordonRejectsOffline(t *testing.T) {
	led, err := ledger.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = led.UpsertNode(ledger.NodeRecord{NodeID: "dead", Status: ledger.StatusOffline, LastHeartbeat: time.Now().UTC()})
	// UpsertNode refreshes heartbeat and would overwrite Offline — set via Merge.
	st := led.Snapshot()
	st.Nodes["dead"] = ledger.NodeRecord{NodeID: "dead", Status: ledger.StatusOffline, LastHeartbeat: time.Now().UTC()}
	_ = led.Merge(st)

	if _, err := Cordon(led, "dead"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanEvacuateSplitsWorkloadAndStandalone(t *testing.T) {
	st := ledger.NewState()
	st.Nodes["a"] = ledger.NodeRecord{NodeID: "a", Status: ledger.StatusDraining}
	st.Nodes["b"] = ledger.NodeRecord{NodeID: "b", Status: ledger.StatusOnline}
	idx := uint32(0)
	st.Containers["wl:web:0"] = ledger.ContainerRecord{
		ContainerID: "wl:web:0", WorkloadID: "web", ReplicaIndex: &idx, CurrentNode: "a", Desired: "Running",
	}
	st.Containers["alone"] = ledger.ContainerRecord{
		ContainerID: "alone", CurrentNode: "a", Desired: "Running", ImageDigest: "sha256:x",
	}

	plan := PlanEvacuate(*st, "a")
	if plan.DestinationNode != "b" {
		t.Fatalf("dest=%s", plan.DestinationNode)
	}
	if len(plan.WorkloadContainers) != 1 || plan.WorkloadContainers[0] != "wl:web:0" {
		t.Fatalf("workload: %+v", plan.WorkloadContainers)
	}
	if len(plan.StandaloneMigrate) != 1 || plan.StandaloneMigrate[0] != "alone" {
		t.Fatalf("standalone: %+v", plan.StandaloneMigrate)
	}
}

func TestScheduleSkipsDrainingSticky(t *testing.T) {
	st := ledger.NewState()
	st.Nodes["a"] = ledger.NodeRecord{NodeID: "a", Status: ledger.StatusDraining}
	st.Nodes["b"] = ledger.NodeRecord{NodeID: "b", Status: ledger.StatusOnline}
	idx := uint32(0)
	st.Containers["wl:web:0"] = ledger.ContainerRecord{
		ContainerID: "wl:web:0", WorkloadID: "web", ReplicaIndex: &idx, CurrentNode: "a", Desired: "Running",
	}
	wl := ledger.WorkloadRecord{WorkloadID: "web", Replicas: 1, ImageDigest: "sha256:x"}
	got := ScheduleLeastLoaded(wl, *st)
	if len(got) != 1 || got[0].NodeID != "b" {
		t.Fatalf("want reschedule to b, got %+v", got)
	}
}
