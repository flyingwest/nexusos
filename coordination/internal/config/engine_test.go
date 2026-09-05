package config

import "testing"

func TestNormalizeConsensusEngine(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", ConsensusEngineCometBFT, false},
		{"CometBFT", ConsensusEngineCometBFT, false},
		{"cometbft", ConsensusEngineCometBFT, false},
		{"hashchain", "", true},
		{"raft", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeConsensusEngine(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q: expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %s want %s", tc.in, got, tc.want)
		}
	}
}
