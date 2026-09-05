package cometbft

import (
	"context"
	"testing"
	"time"

	abcitypes "github.com/cometbft/cometbft/abci/types"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := ledger.NewTx(kp, ledger.MsgRegisterImage, ledger.ImagePayload{
		Digest: "sha256:abc", Size: 10,
	}, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	bz, err := EncodeTx(tx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTx(bz)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash() != tx.Hash() || got.Type != tx.Type {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, tx)
	}
}

func TestCheckTxAndFinalizeBlockImageAndPlacement(t *testing.T) {
	dir := t.TempDir()
	led, err := ledger.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	members, err := membership.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := members.Upsert(membership.Member{
		NodeID:    kp.NodeID,
		PublicKey: kp.PublicJSON().PublicKey,
	}); err != nil {
		t.Fatal(err)
	}

	app := NewApp(led, members)
	ctx := context.Background()
	now := time.Date(2026, 9, 4, 18, 0, 0, 0, time.UTC)

	imgTx, err := ledger.NewTx(kp, ledger.MsgRegisterImage, ledger.ImagePayload{
		Digest: "sha256:deadbeef", Size: 99,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	imgBz, err := EncodeTx(imgTx)
	if err != nil {
		t.Fatal(err)
	}

	ctrTx, err := ledger.NewTx(kp, ledger.MsgCreateContainer, ledger.ContainerPayload{
		ContainerID: "web",
		ImageDigest: "sha256:deadbeef",
		Desired:     "Running",
		CurrentNode: kp.NodeID,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ctrBz, err := EncodeTx(ctrTx)
	if err != nil {
		t.Fatal(err)
	}

	for _, bz := range [][]byte{imgBz, ctrBz} {
		resp, err := app.CheckTx(ctx, &abcitypes.RequestCheckTx{Tx: bz})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Code != CodeOK {
			t.Fatalf("CheckTx code=%d log=%s", resp.Code, resp.Log)
		}
	}

	bad, err := app.CheckTx(ctx, &abcitypes.RequestCheckTx{Tx: []byte("not-json")})
	if err != nil {
		t.Fatal(err)
	}
	if bad.Code != CodeDecode {
		t.Fatalf("expected decode fail, got %d", bad.Code)
	}

	fb, err := app.FinalizeBlock(ctx, &abcitypes.RequestFinalizeBlock{
		Height: 1,
		Txs:    [][]byte{imgBz, ctrBz},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fb.TxResults) != 2 {
		t.Fatalf("results: %d", len(fb.TxResults))
	}
	for i, r := range fb.TxResults {
		if r.Code != CodeOK {
			t.Fatalf("tx %d code=%d log=%s", i, r.Code, r.Log)
		}
	}
	if len(fb.AppHash) == 0 {
		t.Fatal("expected app hash")
	}

	if _, ok := led.Snapshot().Images["sha256:deadbeef"]; ok {
		t.Fatal("image should not persist before Commit")
	}

	if _, err := app.Commit(ctx, &abcitypes.RequestCommit{}); err != nil {
		t.Fatal(err)
	}

	st := led.Snapshot()
	img, ok := st.Images["sha256:deadbeef"]
	if !ok || img.Size != 99 || len(img.VerifiedBy) != 1 || img.VerifiedBy[0] != kp.NodeID {
		t.Fatalf("image after commit: %+v ok=%v", img, ok)
	}
	ctr, ok := st.Containers["web"]
	if !ok || ctr.CurrentNode != kp.NodeID || ctr.ImageDigest != "sha256:deadbeef" {
		t.Fatalf("container after commit: %+v ok=%v", ctr, ok)
	}
	if app.Height() != 1 {
		t.Fatalf("height=%d", app.Height())
	}

	info, err := app.Info(ctx, &abcitypes.RequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if info.LastBlockHeight != 1 {
		t.Fatalf("info height=%d", info.LastBlockHeight)
	}
}

func TestCheckTxRejectsNonMember(t *testing.T) {
	dir := t.TempDir()
	led, err := ledger.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	members, err := membership.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	insider, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := members.Upsert(membership.Member{
		NodeID:    insider.NodeID,
		PublicKey: insider.PublicJSON().PublicKey,
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp(led, members)
	tx, err := ledger.NewTx(outsider, ledger.MsgRegisterImage, ledger.ImagePayload{
		Digest: "sha256:x", Size: 1,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	bz, _ := EncodeTx(tx)
	resp, err := app.CheckTx(context.Background(), &abcitypes.RequestCheckTx{Tx: bz})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Code != CodeNotMember {
		t.Fatalf("expected not-member, got %d (%s)", resp.Code, resp.Log)
	}
}
