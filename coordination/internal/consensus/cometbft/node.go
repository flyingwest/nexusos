package cometbft

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cfg "github.com/cometbft/cometbft/config"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	nm "github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/p2p"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"
	rpclocal "github.com/cometbft/cometbft/rpc/client/local"
	cmttypes "github.com/cometbft/cometbft/types"
	cmttime "github.com/cometbft/cometbft/types/time"

	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
)

const (
	// DefaultRPCListen is the CometBFT RPC listen address (does not clash with :8080).
	DefaultRPCListen = "tcp://127.0.0.1:26657"
	// DefaultP2PListen is the CometBFT P2P listen address.
	DefaultP2PListen = "tcp://127.0.0.1:26656"
	// DefaultChainID used for single-node / --dev clusters.
	DefaultChainID = "nexusos-local"
)

// NodeOptions configures the in-process CometBFT node.
type NodeOptions struct {
	HomeDir   string // typically <data-dir>/cometbft
	ChainID   string
	RPCListen string // e.g. tcp://127.0.0.1:26657
	P2PListen string // e.g. tcp://127.0.0.1:26656
	Moniker   string
	Members   *membership.Store // optional; writes membership validator snapshot
	// PersistentPeers is a CometBFT persistent_peers string
	// (comma-separated id@host:port). Used for multi-node e2e.
	PersistentPeers string
	// IdentityPriv is the NexusOS Ed25519 private key (64-byte Go format).
	// When set and no FilePV key exists yet, consensus FilePV is seeded from it
	// so membership PublicKey == voting pubkey.
	IdentityPriv ed25519.PrivateKey
}

// Node wraps an in-process CometBFT consensus node bound to App.
type Node struct {
	App    *App
	Home   string
	node   *nm.Node
	client *rpclocal.Local
	logger cmtlog.Logger
	nodeID string
}

// StartNode creates config/datadir under opts.HomeDir, writes genesis from the
// local FilePV when missing, starts CometBFT in-process with a LocalClient
// creator (no ABCI socket), and returns a Node ready for Submit.
func StartNode(app *App, opts NodeOptions) (*Node, error) {
	if app == nil {
		return nil, fmt.Errorf("cometbft: nil app")
	}
	home := opts.HomeDir
	if home == "" {
		return nil, fmt.Errorf("cometbft: home dir required")
	}
	if opts.ChainID == "" {
		opts.ChainID = DefaultChainID
	}
	if opts.RPCListen == "" {
		opts.RPCListen = DefaultRPCListen
	}
	if opts.P2PListen == "" {
		opts.P2PListen = DefaultP2PListen
	}
	if opts.Moniker == "" {
		opts.Moniker = "nexusos"
	}

	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	if err := app.SetPersistDir(home); err != nil {
		return nil, fmt.Errorf("abci meta: %w", err)
	}

	cmtCfg := cfg.DefaultConfig().SetRoot(home)
	cfg.EnsureRoot(home)
	cmtCfg.Moniker = opts.Moniker
	cmtCfg.ProxyApp = "nexusos"
	cmtCfg.ABCI = "socket" // unused with LocalClientCreator
	cmtCfg.RPC.ListenAddress = opts.RPCListen
	cmtCfg.RPC.GRPCListenAddress = "" // avoid extra port
	cmtCfg.P2P.ListenAddress = opts.P2PListen
	cmtCfg.P2P.AddrBookStrict = false
	cmtCfg.P2P.AllowDuplicateIP = true
	cmtCfg.P2P.PexReactor = false
	cmtCfg.P2P.Seeds = ""
	cmtCfg.P2P.PersistentPeers = opts.PersistentPeers
	cmtCfg.Instrumentation.Prometheus = false
	// Fast local blocks for --dev / single-node smoke.
	cmtCfg.Consensus.CreateEmptyBlocksInterval = 1 * time.Second
	cmtCfg.Consensus.TimeoutCommit = 500 * time.Millisecond
	cmtCfg.RPC.TimeoutBroadcastTxCommit = 10 * time.Second

	pv, err := loadOrGenFilePV(cmtCfg.PrivValidatorKeyFile(), cmtCfg.PrivValidatorStateFile(), opts.IdentityPriv)
	if err != nil {
		return nil, err
	}
	pub, err := pv.GetPubKey()
	if err != nil {
		return nil, fmt.Errorf("filepv pubkey: %w", err)
	}
	app.SetLocalConsensusPubKey(pub.Bytes())

	if err := ensureGenesis(cmtCfg, pv, opts.ChainID); err != nil {
		return nil, err
	}

	nodeKey, err := p2p.LoadOrGenNodeKey(cmtCfg.NodeKeyFile())
	if err != nil {
		return nil, fmt.Errorf("node key: %w", err)
	}

	logger := cmtlog.NewTMLogger(cmtlog.NewSyncWriter(os.Stderr))
	logger = logger.With("module", "cometbft-embed")
	// Keep noise down: info is enough for operators.
	logger = cmtlog.NewFilter(logger, cmtlog.AllowInfo())

	n, err := nm.NewNode(
		cmtCfg,
		pv,
		nodeKey,
		proxy.NewLocalClientCreator(app),
		nm.DefaultGenesisDocProviderFunc(cmtCfg),
		cfg.DefaultDBProvider,
		nm.DefaultMetricsProvider(cmtCfg.Instrumentation),
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("cometbft new node: %w", err)
	}
	if err := n.Start(); err != nil {
		return nil, fmt.Errorf("cometbft start: %w", err)
	}

	client := rpclocal.New(n)
	if err := WriteMembershipValidatorSnapshot(home, opts.Members); err != nil {
		_ = n.Stop()
		return nil, fmt.Errorf("membership validator snapshot: %w", err)
	}
	nid := string(nodeKey.ID())
	_ = os.WriteFile(filepath.Join(home, "p2p-node-id.txt"), []byte(nid+"\n"), 0o644)

	return &Node{
		App:    app,
		Home:   home,
		node:   n,
		client: client,
		logger: logger,
		nodeID: nid,
	}, nil
}

// loadOrGenFilePV loads an existing FilePV, or creates one. When creating and
// identityPriv is a 64-byte Go Ed25519 key, seed FilePV from it so consensus
// pubkey == NexusOS membership PublicKey.
func loadOrGenFilePV(keyFile, stateFile string, identityPriv ed25519.PrivateKey) (*privval.FilePV, error) {
	if _, err := os.Stat(keyFile); err == nil {
		return privval.LoadFilePV(keyFile, stateFile), nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if len(identityPriv) == ed25519.PrivateKeySize {
		pv := privval.NewFilePV(cmted25519.PrivKey(identityPriv), keyFile, stateFile)
		pv.Save()
		return pv, nil
	}
	pv := privval.GenFilePV(keyFile, stateFile)
	pv.Save()
	return pv, nil
}

func ensureGenesis(cmtCfg *cfg.Config, pv *privval.FilePV, chainID string) error {
	genFile := cmtCfg.GenesisFile()
	if _, err := os.Stat(genFile); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	val, err := genesisValidatorFromFilePV(pv, "local")
	if err != nil {
		return err
	}
	genDoc := &cmttypes.GenesisDoc{
		GenesisTime:     cmttime.Now(),
		ChainID:         chainID,
		InitialHeight:   1,
		ConsensusParams: cmttypes.DefaultConsensusParams(),
		Validators:      []cmttypes.GenesisValidator{val},
		AppHash:         nil,
	}
	if err := genDoc.ValidateAndComplete(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(genFile), 0o700); err != nil {
		return err
	}
	return genDoc.SaveAs(genFile)
}

// Submit encodes a ledger.Tx and broadcasts it via the local CometBFT mempool,
// waiting for commit (BroadcastTxCommit).
func (n *Node) Submit(ctx context.Context, tx ledger.Tx) error {
	if n == nil || n.client == nil {
		return fmt.Errorf("cometbft: node not started")
	}
	bz, err := EncodeTx(tx)
	if err != nil {
		return err
	}
	res, err := n.client.BroadcastTxCommit(ctx, bz)
	if err != nil {
		return fmt.Errorf("broadcast: %w", err)
	}
	if res.CheckTx.Code != CodeOK {
		return fmt.Errorf("checktx code=%d log=%s", res.CheckTx.Code, res.CheckTx.Log)
	}
	if res.TxResult.Code != CodeOK {
		return fmt.Errorf("deliver code=%d log=%s", res.TxResult.Code, res.TxResult.Log)
	}
	return nil
}

// RPCAddress returns the configured RPC listen address.
func (n *Node) RPCAddress() string {
	if n == nil || n.node == nil {
		return ""
	}
	return n.node.Config().RPC.ListenAddress
}

// P2PAddress returns the configured P2P listen address.
func (n *Node) P2PAddress() string {
	if n == nil || n.node == nil {
		return ""
	}
	return n.node.Config().P2P.ListenAddress
}

// NodeID returns the CometBFT P2P node ID (hex) for persistent_peers.
func (n *Node) NodeID() string {
	if n == nil {
		return ""
	}
	return n.nodeID
}

// PeerListenHostPort strips the tcp:// scheme from P2PListen for peer strings.
func PeerListenHostPort(p2pListen string) string {
	const prefix = "tcp://"
	if len(p2pListen) > len(prefix) && p2pListen[:len(prefix)] == prefix {
		return p2pListen[len(prefix):]
	}
	return p2pListen
}

// Stop shuts down the CometBFT node and waits for exit.
func (n *Node) Stop() error {
	if n == nil || n.node == nil {
		return nil
	}
	if err := n.node.Stop(); err != nil {
		return err
	}
	n.node.Wait()
	return nil
}
