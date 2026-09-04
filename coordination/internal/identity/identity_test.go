package identity

import (
	"testing"
)

func TestGenerateSignVerify(t *testing.T) {
	kp, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if kp.NodeID == "" {
		t.Fatal("empty node id")
	}
	msg := []byte("nexusos-placement-v1")
	sig := kp.Sign(msg)
	if !kp.Verify(msg, sig) {
		t.Fatal("signature verify failed")
	}
	if kp.Verify([]byte("tampered"), sig) {
		t.Fatal("tampered message should not verify")
	}
}

func TestLoadOrCreate(t *testing.T) {
	dir := t.TempDir()
	kp1, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	kp2, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if kp1.NodeID != kp2.NodeID {
		t.Fatalf("node id changed across load: %s vs %s", kp1.NodeID, kp2.NodeID)
	}
}
