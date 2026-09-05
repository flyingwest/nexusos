package config

import (
	"fmt"
	"strings"
)

// Consensus engine identifiers.
const (
	ConsensusEngineHashchain = "hashchain"
	ConsensusEngineCometBFT  = "cometbft"
)

// NormalizeConsensusEngine returns a canonical engine name or an error.
// Empty defaults to hashchain (current production path).
func NormalizeConsensusEngine(s string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return ConsensusEngineHashchain, nil
	}
	switch v {
	case ConsensusEngineHashchain, ConsensusEngineCometBFT:
		return v, nil
	default:
		return "", fmt.Errorf("unknown consensus engine %q (want hashchain|cometbft)", s)
	}
}
