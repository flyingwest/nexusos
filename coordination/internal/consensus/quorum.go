package consensus

import "sort"

// Quorum is ceil(2n/3) of permissioned validators — CometBFT-style.
// n=1 → 1, n=2 → 2, n=3 → 2, n=4 → 3.
func Quorum(n int) int {
	if n < 1 {
		return 1
	}
	return (2*n + 2) / 3
}

// Leader is round-robin by height over sorted validator ids.
func Leader(ids []string, height uint64) string {
	if len(ids) == 0 || height == 0 {
		return ""
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	return sorted[int((height-1)%uint64(len(sorted)))]
}
