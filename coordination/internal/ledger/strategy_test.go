package ledger

import (
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/identity"
)

func TestNormalizeStrategy(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", StrategyRollingUpdate},
		{"RollingUpdate", StrategyRollingUpdate},
		{"rolling", StrategyRollingUpdate},
		{"Rolling", StrategyRollingUpdate},
		{"Recreate", StrategyRecreate},
		{"recreate", StrategyRecreate},
	}
	for _, tc := range cases {
		if got := NormalizeStrategy(tc.in); got != tc.want {
			t.Fatalf("NormalizeStrategy(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
	if err := ValidateStrategy("nope"); err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}

func TestEffectiveMaxUnavailable(t *testing.T) {
	if EffectiveMaxUnavailable(WorkloadRecord{}) != 1 {
		t.Fatal("default 1")
	}
	if EffectiveMaxUnavailable(WorkloadRecord{MaxUnavailable: 3}) != 3 {
		t.Fatal("explicit")
	}
}

func TestApplyWorkloadStrategyDefaultAndUpdate(t *testing.T) {
	kp, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 18, 0, 0, 0, time.UTC)
	st := NewState()
	tx, err := NewTx(kp, MsgCreateWorkload, WorkloadPayload{
		WorkloadID: "w", ImageDigest: "sha256:a", Replicas: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyTx(st, tx); err != nil {
		t.Fatal(err)
	}
	if st.Workloads["w"].Strategy != StrategyRollingUpdate {
		t.Fatalf("default strategy=%s", st.Workloads["w"].Strategy)
	}
	up, err := NewTx(kp, MsgUpdateWorkload, WorkloadPayload{
		WorkloadID: "w", ImageDigest: "sha256:b", Replicas: 2, Strategy: StrategyRecreate, MaxUnavailable: 2,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyTx(st, up); err != nil {
		t.Fatal(err)
	}
	wl := st.Workloads["w"]
	if wl.Strategy != StrategyRecreate || wl.MaxUnavailable != 2 || wl.ImageDigest != "sha256:b" {
		t.Fatalf("%+v", wl)
	}
	// Empty strategy on update preserves Recreate.
	up2, err := NewTx(kp, MsgUpdateWorkload, WorkloadPayload{
		WorkloadID: "w", ImageDigest: "sha256:c", Replicas: 2,
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyTx(st, up2); err != nil {
		t.Fatal(err)
	}
	if st.Workloads["w"].Strategy != StrategyRecreate {
		t.Fatalf("preserved strategy=%s", st.Workloads["w"].Strategy)
	}
}
