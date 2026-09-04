package ledger

import (
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/identity"
)

func TestSignVerifyAndApplyImageAndContainer(t *testing.T) {
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	imgTx, err := NewTx(kp, MsgRegisterImage, ImagePayload{Digest: "sha256:abc", Size: 42}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTx(imgTx); err != nil {
		t.Fatal(err)
	}
	imgTx.Payload = []byte(`{"digest":"sha256:evil","size":1}`)
	if err := VerifyTx(imgTx); err == nil {
		t.Fatal("tampered tx should fail")
	}

	imgTx, _ = NewTx(kp, MsgRegisterImage, ImagePayload{Digest: "sha256:abc", Size: 42}, now)
	st := NewState()
	if err := ApplyTx(st, imgTx); err != nil {
		t.Fatal(err)
	}
	got := st.Images["sha256:abc"]
	if got.Size != 42 || len(got.VerifiedBy) != 1 || got.VerifiedBy[0] != kp.NodeID {
		t.Fatalf("image: %+v", got)
	}

	ctrTx, err := NewTx(kp, MsgCreateContainer, ContainerPayload{
		ContainerID: "c1",
		ImageDigest: "sha256:abc",
		Desired:     "Running",
		CurrentNode: kp.NodeID,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyTx(st, ctrTx); err != nil {
		t.Fatal(err)
	}
	if st.Containers["c1"].CurrentNode != kp.NodeID {
		t.Fatalf("container: %+v", st.Containers["c1"])
	}

	rm, err := NewTx(kp, MsgRemoveContainer, RemovePayload{ContainerID: "c1"}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyTx(st, rm); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Containers["c1"]; ok {
		t.Fatal("container should be gone")
	}
	if _, ok := st.Tombstones[ContainerTomb("c1")]; !ok {
		t.Fatal("missing tombstone")
	}
}
