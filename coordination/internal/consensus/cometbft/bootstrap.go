package cometbft

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// JoinTokenHeader is accepted by GET /v1/cometbft/bootstrap as an alternative
// to the operator API bearer token (joining nodes share the join-token).
const JoinTokenHeader = "X-Nexus-Join-Token"

// BootstrapInfo is the shared-genesis + peer payload a seed node serves so
// joining nodes need not copy genesis.json by hand.
type BootstrapInfo struct {
	Genesis   json.RawMessage `json:"genesis"`
	Peer      string          `json:"peer"` // id@host:port for persistent_peers
	P2PNodeID string          `json:"p2p_node_id"`
	P2PListen string          `json:"p2p_listen"` // host:port (no tcp://)
	ChainID   string          `json:"chain_id,omitempty"`
}

// GenesisPath returns <home>/config/genesis.json.
func GenesisPath(home string) string {
	return filepath.Join(home, "config", "genesis.json")
}

// LoadBootstrapInfo reads genesis + p2p node id from an existing CometBFT home.
func LoadBootstrapInfo(home, p2pListen string) (*BootstrapInfo, error) {
	if home == "" {
		return nil, fmt.Errorf("cometbft: home dir required")
	}
	genFile := GenesisPath(home)
	genBytes, err := os.ReadFile(genFile)
	if err != nil {
		return nil, fmt.Errorf("read genesis: %w", err)
	}
	if !json.Valid(genBytes) {
		return nil, fmt.Errorf("genesis is not valid JSON")
	}
	nidBytes, err := os.ReadFile(filepath.Join(home, "p2p-node-id.txt"))
	if err != nil {
		return nil, fmt.Errorf("read p2p node id: %w", err)
	}
	nid := strings.TrimSpace(string(nidBytes))
	if nid == "" {
		return nil, fmt.Errorf("empty p2p node id")
	}
	hostPort := PeerListenHostPort(p2pListen)
	info := &BootstrapInfo{
		Genesis:   json.RawMessage(genBytes),
		P2PNodeID: nid,
		P2PListen: hostPort,
		Peer:      nid + "@" + hostPort,
	}
	var meta struct {
		ChainID string `json:"chain_id"`
	}
	if err := json.Unmarshal(genBytes, &meta); err == nil {
		info.ChainID = meta.ChainID
	}
	return info, nil
}

// InstallGenesis writes genesis.json under home when missing.
// If a genesis already exists, it is left unchanged (returns nil).
func InstallGenesis(home string, genesis json.RawMessage) error {
	if home == "" {
		return fmt.Errorf("cometbft: home dir required")
	}
	if len(genesis) == 0 || !json.Valid(genesis) {
		return fmt.Errorf("cometbft: invalid genesis JSON")
	}
	genFile := GenesisPath(home)
	if _, err := os.Stat(genFile); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(genFile), 0o700); err != nil {
		return err
	}
	return os.WriteFile(genFile, append(append([]byte{}, genesis...), '\n'), 0o644)
}

// FetchBootstrapOptions configures an HTTP fetch from a seed coordinator.
type FetchBootstrapOptions struct {
	SeedURL    string // coordinator base URL, e.g. https://127.0.0.1:8080
	JoinToken  string // sent as X-Nexus-Join-Token
	APIToken   string // optional Bearer token
	TLSInsecure bool
	Timeout    time.Duration
}

// FetchBootstrap GETs /v1/cometbft/bootstrap from a seed peer.
func FetchBootstrap(ctx context.Context, opts FetchBootstrapOptions) (*BootstrapInfo, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.SeedURL), "/")
	if base == "" {
		return nil, fmt.Errorf("cometbft: seed URL required")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if opts.TLSInsecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	client := &http.Client{Timeout: timeout, Transport: tr}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/cometbft/bootstrap", nil)
	if err != nil {
		return nil, err
	}
	if opts.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+opts.APIToken)
	}
	if opts.JoinToken != "" {
		req.Header.Set(JoinTokenHeader, opts.JoinToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch bootstrap: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch bootstrap: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var info BootstrapInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("decode bootstrap: %w", err)
	}
	if len(info.Genesis) == 0 || !json.Valid(info.Genesis) {
		return nil, fmt.Errorf("bootstrap response missing valid genesis")
	}
	if info.Peer == "" && info.P2PNodeID != "" && info.P2PListen != "" {
		info.Peer = info.P2PNodeID + "@" + info.P2PListen
	}
	return &info, nil
}

// FetchAndInstallGenesis fetches bootstrap from seed and installs genesis under home.
// Returns the peer string from the seed (may be empty).
func FetchAndInstallGenesis(ctx context.Context, home string, opts FetchBootstrapOptions) (peer string, err error) {
	info, err := FetchBootstrap(ctx, opts)
	if err != nil {
		return "", err
	}
	if err := InstallGenesis(home, info.Genesis); err != nil {
		return "", err
	}
	return info.Peer, nil
}
