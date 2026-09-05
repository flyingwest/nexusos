package config

import (
	"fmt"
	"strings"
)

// Consensus engine identifiers.
const (
	ConsensusEngineCometBFT = "cometbft"
)

// NormalizeConsensusEngine returns a canonical engine name or an error.
// Empty defaults to cometbft. Only CometBFT is supported; the former
// hash-chain engine has been removed.
func NormalizeConsensusEngine(s string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return ConsensusEngineCometBFT, nil
	}
	switch v {
	case ConsensusEngineCometBFT:
		return v, nil
	case "hashchain":
		return "", fmt.Errorf("consensus engine %q was removed; CometBFT is the only engine (omit --consensus-engine or pass cometbft)", s)
	default:
		return "", fmt.Errorf("unknown consensus engine %q (want cometbft)", s)
	}
}
