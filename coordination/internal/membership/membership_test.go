package membership

import "testing"

func TestMembershipUpsertListRemove(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsEmpty() {
		t.Fatal("expected empty")
	}
	// empty = open
	if !s.Contains("anyone") {
		t.Fatal("empty membership should allow any node")
	}
	if err := s.Upsert(Member{NodeID: "abc", Label: "n1"}); err != nil {
		t.Fatal(err)
	}
	if s.IsEmpty() || !s.Contains("abc") || s.Contains("xyz") {
		t.Fatal("membership check failed")
	}
	if len(s.List()) != 1 {
		t.Fatal("list len")
	}
	// reload
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Contains("abc") {
		t.Fatal("not persisted")
	}
	_ = s2.Remove("abc")
	if s2.Contains("abc") && !s2.IsEmpty() {
		t.Fatal("remove failed")
	}
}
