package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
)

func TestQuorumAndLeader(t *testing.T) {
	cases := []struct{ n, q int }{{1, 1}, {2, 2}, {3, 2}, {4, 3}}
	for _, c := range cases {
		if got := Quorum(c.n); got != c.q {
			t.Fatalf("Quorum(%d)=%d want %d", c.n, got, c.q)
		}
	}
	ids := []string{"b", "a"}
	if Leader(ids, 1) != "a" {
		t.Fatalf("height 1 leader should be a, got %s", Leader(ids, 1))
	}
	if Leader(ids, 2) != "b" {
		t.Fatalf("height 2 leader should be b")
	}
}

func TestSingleNodeCommit(t *testing.T) {
	dir := t.TempDir()
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	led, err := ledger.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	mem, err := membership.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Upsert(membership.Member{NodeID: kp.NodeID, PublicKey: kp.PublicJSON().PublicKey, Label: "self"}); err != nil {
		t.Fatal(err)
	}
	chain, err := NewChain(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng := &Engine{
		KP:      kp,
		Ledger:  led,
		Members: mem,
		Chain:   chain,
		Timeout: time.Second,
	}
	tx, err := ledger.NewTx(kp, ledger.MsgRegisterImage, ledger.ImagePayload{Digest: "sha256:x", Size: 9}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Submit(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if chain.Height() != 1 {
		t.Fatalf("height %d", chain.Height())
	}
	img, ok := led.Snapshot().Images["sha256:x"]
	if !ok || img.Size != 9 {
		t.Fatalf("image not applied: ok=%v %+v", ok, img)
	}
	if !chain.ContainsTx(tx.Hash()) {
		t.Fatal("tx missing from chain")
	}
}
