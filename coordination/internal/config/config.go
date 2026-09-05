package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds the coordinator configuration.
type Config struct {
	NodeID     string `json:"node_id"`
	DataDir    string `json:"data_dir"`
	ListenAddr string `json:"listen_addr"`

	ContainerdSocket string `json:"containerd_socket"`
	Namespace        string `json:"namespace"`
	UseMockRuntime   bool   `json:"use_mock_runtime"`

	// Security — join-token + API token + TLS are required unless Dev is set.
	APIToken           string `json:"api_token"`
	JoinToken          string `json:"join_token"`
	RequireImageDigest bool   `json:"require_image_digest"`
	Dev                bool   `json:"dev"` // --dev / --insecure-dev escape hatch

	// TLS for the operator (+ peer) HTTP listener.
	TLSCertFile string `json:"tls_cert_file"`
	TLSKeyFile  string `json:"tls_key_file"`
	// TLSInsecureSkipVerify is set when using self-signed certs (typically --dev).
	// Peer HTTP clients skip certificate verification.
	TLSInsecureSkipVerify bool `json:"tls_insecure_skip_verify"`

	// Mode
	Mode string `json:"mode"` // "permissioned" | "permissionless"

	HeartbeatInterval time.Duration `json:"heartbeat_interval"`

	// P2P / ledger sync
	AdvertiseURL     string        `json:"advertise_url"`
	Peers            []string      `json:"peers"`
	SyncInterval     time.Duration `json:"sync_interval"`
	HeartbeatTimeout time.Duration `json:"heartbeat_timeout"`

	// Consensus orders image-integrity and placement txs into a hash chain.
	// Heartbeats still use HTTP snapshot sync.
	Consensus        bool          `json:"consensus"`
	ConsensusTimeout time.Duration `json:"consensus_timeout"`
	// ConsensusEngine selects the ordered-commit path: "hashchain" (default)
	// or "cometbft" (in-process CometBFT node + ABCI app).
	ConsensusEngine string `json:"consensus_engine"`
	// CometBFT listen addresses (only used when ConsensusEngine=cometbft).
	// Defaults avoid clashing with the operator HTTP listener (:8080).
	CometBFTRPC string `json:"cometbft_rpc"`
	CometBFTP2P string `json:"cometbft_p2p"`
	// CometBFTPeers is a CometBFT persistent_peers list (id@host:port,...).
	CometBFTPeers string `json:"cometbft_peers"`
}

// Default returns a sensible development configuration.
func Default() *Config {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "nexus-node"
	}

	return &Config{
		NodeID:             hostname,
		DataDir:            "/var/lib/nexusos",
		ListenAddr:         ":8080",
		ContainerdSocket:   "/run/containerd/containerd.sock",
		Namespace:          "nexusos",
		UseMockRuntime:     true,
		APIToken:           "",
		JoinToken:          "",
		RequireImageDigest: false,
		Mode:               "permissioned",
		HeartbeatInterval:  10 * time.Second,
		SyncInterval:       10 * time.Second,
		HeartbeatTimeout:   30 * time.Second,
		Consensus:          true,
		ConsensusTimeout:   5 * time.Second,
		ConsensusEngine:    ConsensusEngineHashchain,
		CometBFTRPC:        "tcp://127.0.0.1:26657",
		CometBFTP2P:        "tcp://127.0.0.1:26656",
	}
}

// AdvertiseFromListen derives a localhost URL from a listen address like ":8080".
// scheme is "http" or "https". Real deployments should set AdvertiseURL explicitly.
func AdvertiseFromListen(listen, scheme string) string {
	if listen == "" {
		return ""
	}
	if scheme == "" {
		scheme = "http"
	}
	if len(listen) > 0 && listen[0] == ':' {
		return scheme + "://127.0.0.1" + listen
	}
	return scheme + "://" + listen
}

// Validate checks required fields. Unless Dev is true, APIToken, JoinToken,
// and TLS cert/key paths must be non-empty.
func (c *Config) Validate() error {
	if c.NodeID == "" {
		return fmt.Errorf("node_id is required")
	}
	if c.DataDir == "" {
		return fmt.Errorf("data_dir is required")
	}
	if c.ListenAddr == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if c.Dev {
		return nil
	}
	if strings.TrimSpace(c.APIToken) == "" {
		return fmt.Errorf("api-token is required (set --api-token / NEXUS_API_TOKEN, or pass --dev for local experiments)")
	}
	if strings.TrimSpace(c.JoinToken) == "" {
		return fmt.Errorf("join-token is required (set --join-token, or pass --dev for local experiments)")
	}
	if c.TLSCertFile == "" || c.TLSKeyFile == "" {
		return fmt.Errorf("TLS is required: set --tls-cert and --tls-key (or pass --dev to auto-generate self-signed certs)")
	}
	return nil
}

// TLSEnabled reports whether the HTTP listener should use TLS.
func (c *Config) TLSEnabled() bool {
	return c.TLSCertFile != "" && c.TLSKeyFile != ""
}
