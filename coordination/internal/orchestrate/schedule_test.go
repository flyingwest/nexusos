package orchestrate

import (
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/ledger"
)

func TestScheduleLeastLoadedRoundRobinish(t *testing.T) {
	st := ledger.NewState()
	st.Nodes["a"] = ledger.NodeRecord{NodeID: "a", Status: ledger.StatusOnline}
	st.Nodes["b"] = ledger.NodeRecord{NodeID: "b", Status: ledger.StatusOnline}
	st.Nodes["c"] = ledger.NodeRecord{NodeID: "c", Status: ledger.StatusOffline}

	wl := ledger.WorkloadRecord{WorkloadID: "web", Replicas: 3, ImageDigest: "sha256:x"}
	got := ScheduleLeastLoaded(wl, *st)
	if len(got) != 3 {
		t.Fatalf("want 3 placements, got %d", len(got))
	}
	// Offline c must not be used; a and b share load.
	counts := map[string]int{}
	for _, p := range got {
		if p.NodeID == "c" {
			t.Fatal("scheduled onto offline node")
		}
		counts[p.NodeID]++
		if p.ContainerID != ledger.ReplicaContainerID("web", p.ReplicaIndex) {
			t.Fatalf("id: %s", p.ContainerID)
		}
	}
	if counts["a"]+counts["b"] != 3 {
		t.Fatalf("counts=%v", counts)
	}
	// Deterministic: same plan again
	got2 := ScheduleLeastLoaded(wl, *st)
	for i := range got {
		if got[i] != got2[i] {
			t.Fatalf("non-deterministic: %+v vs %+v", got, got2)
		}
	}
}

func TestSchedulePreservesStickyPlacement(t *testing.T) {
	st := ledger.NewState()
	st.Nodes["a"] = ledger.NodeRecord{NodeID: "a", Status: ledger.StatusOnline}
	st.Nodes["b"] = ledger.NodeRecord{NodeID: "b", Status: ledger.StatusOnline}
	idx := uint32(0)
	st.Containers["wl:web:0"] = ledger.ContainerRecord{
		ContainerID:  "wl:web:0",
		WorkloadID:   "web",
		ReplicaIndex: &idx,
		CurrentNode:  "b",
		Desired:      "Running",
		UpdatedAt:    time.Now().UTC(),
	}
	wl := ledger.WorkloadRecord{WorkloadID: "web", Replicas: 1, ImageDigest: "sha256:x"}
	got := ScheduleLeastLoaded(wl, *st)
	if len(got) != 1 || got[0].NodeID != "b" {
		t.Fatalf("want sticky b, got %+v", got)
	}
}
