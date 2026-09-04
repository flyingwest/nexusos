package ledger

import (
	"testing"
	"time"
)

func TestMergeImagesUnionVerifiedBy(t *testing.T) {
	dst := NewState()
	src := NewState()
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t0 := t1.Add(-time.Hour)
	dst.Images["sha256:a"] = ImageRecord{Digest: "sha256:a", Size: 10, VerifiedBy: []string{"n1"}, FirstSeen: t1}
	src.Images["sha256:a"] = ImageRecord{Digest: "sha256:a", Size: 20, VerifiedBy: []string{"n2"}, FirstSeen: t0}

	MergeState(dst, *src)
	got := dst.Images["sha256:a"]
	if got.Size != 20 {
		t.Fatalf("size: %d", got.Size)
	}
	if len(got.VerifiedBy) != 2 {
		t.Fatalf("verified_by: %v", got.VerifiedBy)
	}
	if !got.FirstSeen.Equal(t0) {
		t.Fatalf("first_seen: %v", got.FirstSeen)
	}
}

func TestMergeContainerLastWriteWins(t *testing.T) {
	dst := NewState()
	src := NewState()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(time.Minute)
	dst.Containers["c1"] = ContainerRecord{ContainerID: "c1", CurrentNode: "a", UpdatedAt: old, Desired: "Running"}
	src.Containers["c1"] = ContainerRecord{ContainerID: "c1", CurrentNode: "b", UpdatedAt: newer, Desired: "Stopped"}

	MergeState(dst, *src)
	if dst.Containers["c1"].CurrentNode != "b" {
		t.Fatalf("expected node b, got %+v", dst.Containers["c1"])
	}
}

func TestMergeTombstoneHidesOlderContainer(t *testing.T) {
	dst := NewState()
	src := NewState()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	del := old.Add(time.Minute)
	dst.Containers["c1"] = ContainerRecord{ContainerID: "c1", CurrentNode: "a", UpdatedAt: old}
	src.Tombstones[ContainerTomb("c1")] = del

	MergeState(dst, *src)
	if _, ok := dst.Containers["c1"]; ok {
		t.Fatal("container should be tombstoned")
	}
}

func TestMergeNewerContainerResurrects(t *testing.T) {
	dst := NewState()
	src := NewState()
	del := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := del.Add(time.Minute)
	dst.Tombstones[ContainerTomb("c1")] = del
	src.Containers["c1"] = ContainerRecord{ContainerID: "c1", CurrentNode: "b", UpdatedAt: newer}

	MergeState(dst, *src)
	if dst.Containers["c1"].CurrentNode != "b" {
		t.Fatalf("expected resurrected container, got %+v", dst.Containers)
	}
}

func TestMergeNodeHeartbeat(t *testing.T) {
	dst := NewState()
	src := NewState()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(time.Second)
	dst.Nodes["n1"] = NodeRecord{NodeID: "n1", Status: "Online", LastHeartbeat: old, Addresses: []string{"http://a"}}
	src.Nodes["n1"] = NodeRecord{NodeID: "n1", Status: "Draining", LastHeartbeat: newer, Addresses: []string{"http://b"}}

	MergeState(dst, *src)
	n := dst.Nodes["n1"]
	if n.Status != "Draining" {
		t.Fatalf("status: %s", n.Status)
	}
	if len(n.Addresses) != 2 {
		t.Fatalf("addresses not unioned: %v", n.Addresses)
	}
}

func TestMergeEqualHeartbeatOfflineWins(t *testing.T) {
	dst := NewState()
	src := NewState()
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dst.Nodes["n1"] = NodeRecord{NodeID: "n1", Status: StatusOnline, LastHeartbeat: ts, Addresses: []string{"http://a"}}
	src.Nodes["n1"] = NodeRecord{NodeID: "n1", Status: StatusOffline, LastHeartbeat: ts, Addresses: []string{"http://b"}}

	MergeState(dst, *src)
	n := dst.Nodes["n1"]
	if n.Status != StatusOffline {
		t.Fatalf("status: %s", n.Status)
	}
	if len(n.Addresses) != 2 {
		t.Fatalf("addresses: %v", n.Addresses)
	}
}

func TestMergeNewerHeartbeatClearsOffline(t *testing.T) {
	dst := NewState()
	src := NewState()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(time.Second)
	dst.Nodes["n1"] = NodeRecord{NodeID: "n1", Status: StatusOffline, LastHeartbeat: old}
	src.Nodes["n1"] = NodeRecord{NodeID: "n1", Status: StatusOnline, LastHeartbeat: newer}

	MergeState(dst, *src)
	if dst.Nodes["n1"].Status != StatusOnline {
		t.Fatalf("status: %s", dst.Nodes["n1"].Status)
	}
}

func TestCanonicalHashStable(t *testing.T) {
	st := NewState()
	st.Images["sha256:z"] = ImageRecord{Digest: "sha256:z", VerifiedBy: []string{"b", "a"}}
	st.Images["sha256:a"] = ImageRecord{Digest: "sha256:a", VerifiedBy: []string{"c"}}
	h1, err := CanonicalHash(*st)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := CanonicalHash(*st)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 || h1 == "" {
		t.Fatalf("unstable hash: %s vs %s", h1, h2)
	}
}
