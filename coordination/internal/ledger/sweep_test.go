package ledger

import (
	"testing"
	"time"
)

func TestSweepExpiredMarksStaleNotSelf(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	st := NewState()
	st.Nodes["self"] = NodeRecord{NodeID: "self", Status: StatusOnline, LastHeartbeat: now.Add(-time.Hour)}
	st.Nodes["dead"] = NodeRecord{NodeID: "dead", Status: StatusOnline, LastHeartbeat: now.Add(-time.Minute)}
	st.Nodes["live"] = NodeRecord{NodeID: "live", Status: StatusOnline, LastHeartbeat: now.Add(-time.Second)}
	if err := s.Merge(*st); err != nil {
		t.Fatal(err)
	}

	marked, err := s.SweepExpired(now, 10*time.Second, "self")
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 1 || marked[0] != "dead" {
		t.Fatalf("marked: %v", marked)
	}
	if n, _ := s.GetNode("self"); n.Status != StatusOnline {
		t.Fatalf("self should stay Online, got %s", n.Status)
	}
	if n, _ := s.GetNode("dead"); n.Status != StatusOffline {
		t.Fatalf("dead should be Offline, got %s", n.Status)
	}
	if n, _ := s.GetNode("live"); n.Status != StatusOnline {
		t.Fatalf("live should stay Online, got %s", n.Status)
	}
	// LastHeartbeat must stay put so a later live record still wins.
	if n, _ := s.GetNode("dead"); !n.LastHeartbeat.Equal(now.Add(-time.Minute)) {
		t.Fatalf("heartbeat mutated: %v", n.LastHeartbeat)
	}
}

func TestSweepExpiredDisabledWhenTimeoutZero(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	st := NewState()
	st.Nodes["x"] = NodeRecord{NodeID: "x", Status: StatusOnline, LastHeartbeat: now.Add(-time.Hour)}
	_ = s.Merge(*st)
	marked, err := s.SweepExpired(now, 0, "self")
	if err != nil || len(marked) != 0 {
		t.Fatalf("expected no-op, marked=%v err=%v", marked, err)
	}
}
