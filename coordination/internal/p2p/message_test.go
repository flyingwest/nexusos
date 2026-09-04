package p2p

import (
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
)

func TestSignVerifyHelloAndSync(t *testing.T) {
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	h := SignHello(kp, "http://127.0.0.1:8080", now)
	if err := VerifyHello(h, now); err != nil {
		t.Fatalf("hello: %v", err)
	}
	h.Advertise = "http://evil"
	if err := VerifyHello(h, now); err == nil {
		t.Fatal("tampered hello should fail")
	}

	st := ledger.NewState()
	st.Images["sha256:x"] = ledger.ImageRecord{Digest: "sha256:x", Size: 1, VerifiedBy: []string{kp.NodeID}, FirstSeen: now}
	msg, err := SignSync(kp, "http://127.0.0.1:8080", *st, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySync(msg, now); err != nil {
		t.Fatalf("sync: %v", err)
	}
	img := msg.State.Images["sha256:x"]
	img.Size = 99
	msg.State.Images["sha256:x"] = img
	if err := VerifySync(msg, now); err == nil {
		t.Fatal("tampered snapshot should fail")
	}
}

func TestHelloRejectsStaleTimestamp(t *testing.T) {
	kp, _ := identity.Generate()
	now := time.Now().UTC()
	h := SignHello(kp, "http://n", now.Add(-10*time.Minute))
	if err := VerifyHello(h, now); err == nil {
		t.Fatal("expected stale timestamp error")
	}
}
