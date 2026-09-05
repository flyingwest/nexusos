package cometbft

import (
	"context"
	"encoding/hex"
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

func TestFinalizeBlockValidatorUpdatesFromMembershipTx(t *testing.T) {
	dir := t.TempDir()
	led, err := ledger.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	members, err := membership.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	led.BindMembership(func(recs map[string]ledger.MemberRecord) error {
		list := make([]membership.Member, 0, len(recs))
		for _, m := range recs {
			list = append(list, membership.Member{
				NodeID: m.NodeID, PublicKey: m.PublicKey, Addresses: m.Addresses, Label: m.Label,
			})
		}
		return members.ReplaceAll(list)
	})
	kpA, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	kpB, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	aHex := kpA.PublicJSON().PublicKey
	bHex := kpB.PublicJSON().PublicKey

	if err := members.Upsert(membership.Member{NodeID: kpA.NodeID, PublicKey: aHex}); err != nil {
		t.Fatal(err)
	}

	app := NewApp(led, members)
	app.SetLocalConsensusPubKey(kpA.PublicKey)
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	_, err = app.InitChain(ctx, &abcitypes.RequestInitChain{
		Validators: []abcitypes.ValidatorUpdate{
			abcitypes.Ed25519ValidatorUpdate(kpA.PublicKey, DefaultValidatorPower),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Empty ledger Members → no updates (local members.json alone is not enough).
	fb, err := app.FinalizeBlock(ctx, &abcitypes.RequestFinalizeBlock{Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(fb.ValidatorUpdates) != 0 {
		t.Fatalf("expected no updates, got %d", len(fb.ValidatorUpdates))
	}
	_, _ = app.Commit(ctx, &abcitypes.RequestCommit{})

	// JoinMember(B) signed by genesis validator A → add B.
	join, err := ledger.NewTx(kpA, ledger.MsgJoinMember, ledger.MemberPayload{
		NodeID: kpB.NodeID, PublicKey: bHex, Label: "peer",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	joinBz, err := EncodeTx(join)
	if err != nil {
		t.Fatal(err)
	}
	fb, err = app.FinalizeBlock(ctx, &abcitypes.RequestFinalizeBlock{Height: 2, Txs: [][]byte{joinBz}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fb.TxResults) != 1 || fb.TxResults[0].Code != CodeOK {
		t.Fatalf("JoinMember result: %+v", fb.TxResults)
	}
	if len(fb.ValidatorUpdates) != 1 || fb.ValidatorUpdates[0].Power != DefaultValidatorPower {
		t.Fatalf("want add B: %+v", fb.ValidatorUpdates)
	}
	gotB := hex.EncodeToString(fb.ValidatorUpdates[0].PubKey.GetEd25519())
	if gotB != bHex {
		t.Fatalf("got %s want %s", gotB, bHex)
	}
	_, err = app.Commit(ctx, &abcitypes.RequestCommit{})
	if err != nil {
		t.Fatal(err)
	}
	if !members.Contains(kpB.NodeID) || !members.Contains(kpA.NodeID) {
		t.Fatalf("membership cache not synced: %v", members.IDs())
	}
	if len(led.Snapshot().Members) != 2 {
		t.Fatalf("ledger members=%d", len(led.Snapshot().Members))
	}

	// LeaveMember(B) → power 0.
	leave, err := ledger.NewTx(kpA, ledger.MsgLeaveMember, ledger.MemberPayload{NodeID: kpB.NodeID}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	leaveBz, err := EncodeTx(leave)
	if err != nil {
		t.Fatal(err)
	}
	fb, err = app.FinalizeBlock(ctx, &abcitypes.RequestFinalizeBlock{Height: 3, Txs: [][]byte{leaveBz}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fb.ValidatorUpdates) != 1 || fb.ValidatorUpdates[0].Power != 0 {
		t.Fatalf("want remove B: %+v", fb.ValidatorUpdates)
	}
}

func TestFinalizeBlockNoValidatorUpdateWhenMembershipEmpty(t *testing.T) {
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
	app := NewApp(led, members)
	app.SetLocalConsensusPubKey(kp.PublicKey)
	ctx := context.Background()
	_, err = app.InitChain(ctx, &abcitypes.RequestInitChain{
		Validators: []abcitypes.ValidatorUpdate{
			abcitypes.Ed25519ValidatorUpdate(kp.PublicKey, DefaultValidatorPower),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fb, err := app.FinalizeBlock(ctx, &abcitypes.RequestFinalizeBlock{Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(fb.ValidatorUpdates) != 0 {
		t.Fatalf("empty membership must preserve genesis FilePV; got %+v", fb.ValidatorUpdates)
	}
	if len(app.ValidatorSetSnapshot()) != 1 {
		t.Fatal("genesis validator should remain tracked")
	}
}
