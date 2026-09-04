package p2p

import "testing"

func TestPeerStorePersist(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(Peer{URL: "http://127.0.0.1:8081/"}); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := s2.Get("http://127.0.0.1:8081")
	if !ok || p.URL != "http://127.0.0.1:8081" {
		t.Fatalf("not persisted: %+v", p)
	}
}
