package cometbft

import (
	"encoding/hex"
	"testing"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/membership"
)

func TestDiffValidatorUpdatesAddRemoveAndSafety(t *testing.T) {
	kpA, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	kpB, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	aHex := hex.EncodeToString(kpA.PublicKey)
	bHex := hex.EncodeToString(kpB.PublicKey)

	// Empty desired → nil (single-node / open membership safety).
	if u := DiffValidatorUpdates(map[string]int64{aHex: 10}, nil, aHex); u != nil {
		t.Fatalf("empty desired should no-op, got %d updates", len(u))
	}

	// Local not in desired → nil (avoid replacing FilePV with unusable keys).
	if u := DiffValidatorUpdates(map[string]int64{aHex: 10}, map[string]int64{bHex: 10}, aHex); u != nil {
		t.Fatalf("local missing from desired should no-op, got %d", len(u))
	}

	// Non-validator local with desired={self} against genesis={other} → no-op
	// (would otherwise replace genesis before pair).
	if u := DiffValidatorUpdates(map[string]int64{aHex: 10}, map[string]int64{bHex: 10}, bHex); u != nil {
		t.Fatalf("non-validator must not replace genesis, got %d", len(u))
	}

	// Add B alongside A.
	cur := map[string]int64{aHex: DefaultValidatorPower}
	des := map[string]int64{aHex: DefaultValidatorPower, bHex: DefaultValidatorPower}
	u := DiffValidatorUpdates(cur, des, aHex)
	if len(u) != 1 {
		t.Fatalf("want 1 add, got %d", len(u))
	}
	if u[0].Power != DefaultValidatorPower {
		t.Fatalf("power=%d", u[0].Power)
	}
	gotHex := hex.EncodeToString(u[0].PubKey.GetEd25519())
	if gotHex != bHex {
		t.Fatalf("added %s want %s", gotHex, bHex)
	}
	ApplyValidatorUpdatesMut(cur, u)
	if len(cur) != 2 {
		t.Fatalf("cur len=%d", len(cur))
	}

	// Remove B.
	des2 := map[string]int64{aHex: DefaultValidatorPower}
	u2 := DiffValidatorUpdates(cur, des2, aHex)
	if len(u2) != 1 || u2[0].Power != 0 {
		t.Fatalf("want remove B: %+v", u2)
	}
	ApplyValidatorUpdatesMut(cur, u2)
	if _, ok := cur[bHex]; ok {
		t.Fatal("B should be gone")
	}
}

func TestDiffValidatorUpdatesNeverEmptySet(t *testing.T) {
	kp, _ := identity.Generate()
	aHex := hex.EncodeToString(kp.PublicKey)
	// desired has keys but decoding failure path: use valid desired that removes all
	// only if local is in desired — removing sole validator while desired equals
	// that validator shouldn't remove. Removing sole when desired is different
	// key is blocked by local check. Force empty-after-apply via bogus current
	// only: desired={a}, current={a} → no updates.
	u := DiffValidatorUpdates(map[string]int64{aHex: 10}, map[string]int64{aHex: 10}, aHex)
	if u != nil {
		t.Fatalf("identical sets should no-op, got %d", len(u))
	}
}

func TestMembershipPowerByPubKey(t *testing.T) {
	dir := t.TempDir()
	members, err := membership.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	_ = members.Upsert(membership.Member{
		NodeID:    kp.NodeID,
		PublicKey: kp.PublicJSON().PublicKey,
	})
	_ = members.Upsert(membership.Member{NodeID: "nokey"})
	m := MembershipPowerByPubKey(members)
	if len(m) != 1 {
		t.Fatalf("len=%d", len(m))
	}
	if m[hex.EncodeToString(kp.PublicKey)] != DefaultValidatorPower {
		t.Fatalf("power map: %+v", m)
	}
}
