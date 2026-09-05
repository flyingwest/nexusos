package ledger

import (
	"fmt"
	"strings"
)

// NormalizeStrategy maps aliases to canonical strategy names.
// Empty defaults to RollingUpdate (gradual image/spec changes).
func NormalizeStrategy(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "rollingupdate", "rolling", "rolling-update", "rolling_update":
		return StrategyRollingUpdate
	case "recreate":
		return StrategyRecreate
	default:
		// Preserve unknown casing only if already canonical; else return trimmed.
		t := strings.TrimSpace(s)
		if t == StrategyRecreate || t == StrategyRollingUpdate {
			return t
		}
		return t
	}
}

// ValidateStrategy returns an error if strategy is not supported.
func ValidateStrategy(s string) error {
	switch NormalizeStrategy(s) {
	case StrategyRecreate, StrategyRollingUpdate:
		return nil
	default:
		return fmt.Errorf("unsupported workload strategy %q (want Recreate or RollingUpdate)", s)
	}
}

// EffectiveMaxUnavailable returns how many outdated replicas may be updated
// in one reconcile pass under RollingUpdate. Zero means 1 (one-at-a-time).
func EffectiveMaxUnavailable(wl WorkloadRecord) int {
	if wl.MaxUnavailable == 0 {
		return 1
	}
	return int(wl.MaxUnavailable)
}
