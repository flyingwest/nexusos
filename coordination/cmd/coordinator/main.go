package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nexusos/coordination/internal/api/httpapi"
	"github.com/nexusos/coordination/internal/config"
	"github.com/nexusos/coordination/internal/consensus/cometbft"
	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
	"github.com/nexusos/coordination/internal/p2p"
	"github.com/nexusos/coordination/internal/runtime"
	"github.com/nexusos/coordination/internal/state"
)

func main() {
	dataDir := flag.String("data-dir", "", "override data directory")
	listen := flag.String("listen", "", "override listen address")
	mock := flag.Bool("mock", true, "use mock runtime (no containerd required)")
	socket := flag.String("containerd-socket", "", "containerd socket path")
	apiToken := flag.String("api-token", "", "bearer token for operator API auth (or NEXUS_API_TOKEN)")
	requireDigest := flag.Bool("require-digest", false, "only allow starting containers by image digest")
	mode := flag.String("mode", "permissioned", "permissioned | permissionless")
	advertise := flag.String("advertise", "", "URL other nodes use to reach this coordinator (default: localhost + listen)")
	peers := flag.String("peers", "", "comma-separated bootstrap peer URLs")
	joinToken := flag.String("join-token", "", "shared token required to pair")
	syncInterval := flag.Duration("sync-interval", 0, "peer ledger sync interval (default: 10s)")
	heartbeatTimeout := flag.Duration("heartbeat-timeout", 0, "mark peers offline after this silence (default: 30s)")
	useConsensus := flag.Bool("consensus", true, "commit image integrity and placement via CometBFT")
	consensusEngine := flag.String("consensus-engine", config.ConsensusEngineCometBFT, "consensus engine (only cometbft is supported)")
	cometbftFlag := flag.Bool("cometbft", false, "shorthand for --consensus-engine=cometbft (default engine)")
	cometbftRPC := flag.String("cometbft-rpc", "", "CometBFT RPC listen (default tcp://127.0.0.1:26657)")
	cometbftP2P := flag.String("cometbft-p2p", "", "CometBFT P2P listen (default tcp://127.0.0.1:26656)")
	cometbftPeers := flag.String("cometbft-peers", "", "CometBFT persistent_peers (id@host:port,...)")
	cometbftGenesisFrom := flag.String("cometbft-genesis-from", "", "fetch shared CometBFT genesis (+ peer hint) from seed coordinator URL before start")
	dev := flag.Bool("dev", false, "insecure local experiments: allow empty tokens and auto self-signed TLS")
	insecureDev := flag.Bool("insecure-dev", false, "alias for --dev")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (required unless --dev)")
	tlsKey := flag.String("tls-key", "", "TLS private key file (required unless --dev)")
	tlsInsecurePeers := flag.Bool("tls-insecure-peers", false, "skip TLS verification when dialing peer HTTPS URLs")
	flag.Parse()

	cfg := config.Default()
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if *listen != "" {
		cfg.ListenAddr = *listen
	}
	if *socket != "" {
		cfg.ContainerdSocket = *socket
	}
	cfg.UseMockRuntime = *mock
	cfg.APIToken = *apiToken
	if cfg.APIToken == "" {
		cfg.APIToken = os.Getenv("NEXUS_API_TOKEN")
	}
	cfg.RequireImageDigest = *requireDigest
	cfg.Mode = *mode
	cfg.AdvertiseURL = *advertise
	cfg.JoinToken = *joinToken
	if cfg.JoinToken == "" {
		cfg.JoinToken = os.Getenv("NEXUS_JOIN_TOKEN")
	}
	if *syncInterval > 0 {
		cfg.SyncInterval = *syncInterval
	}
	if *heartbeatTimeout > 0 {
		cfg.HeartbeatTimeout = *heartbeatTimeout
	}
	cfg.Consensus = *useConsensus
	engineName := *consensusEngine
	if *cometbftFlag {
		engineName = config.ConsensusEngineCometBFT
	}
	normalized, err := config.NormalizeConsensusEngine(engineName)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.ConsensusEngine = normalized
	if *cometbftRPC != "" {
		cfg.CometBFTRPC = *cometbftRPC
	}
	if *cometbftP2P != "" {
		cfg.CometBFTP2P = *cometbftP2P
	}
	if *cometbftPeers != "" {
		cfg.CometBFTPeers = *cometbftPeers
	}
	cfg.CometBFTGenesisFrom = strings.TrimSpace(*cometbftGenesisFrom)
	cfg.Dev = *dev || *insecureDev
	cfg.TLSCertFile = *tlsCert
	cfg.TLSKeyFile = *tlsKey
	cfg.TLSInsecureSkipVerify = *tlsInsecurePeers
	if *peers != "" {
		for _, p := range splitCSV(*peers) {
			cfg.Peers = append(cfg.Peers, p)
		}
	}

	if cfg.DataDir == "/var/lib/nexusos" {
		home, _ := os.UserHomeDir()
		if home != "" {
			cfg.DataDir = filepath.Join(home, ".nexusos")
		} else {
			cfg.DataDir = "./data"
		}
	}

	// In --dev, auto-generate self-signed TLS under data-dir when cert/key omitted.
	if cfg.Dev {
		cert, key, err := config.EnsureDevTLS(cfg.DataDir, cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			log.Fatalf("dev TLS: %v", err)
		}
		cfg.TLSCertFile = cert
		cfg.TLSKeyFile = key
		cfg.TLSInsecureSkipVerify = true
	}

	if cfg.AdvertiseURL == "" {
		scheme := "http"
		if cfg.TLSEnabled() {
			scheme = "https"
		}
		cfg.AdvertiseURL = config.AdvertiseFromListen(cfg.ListenAddr, scheme)
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}

	// Cryptographic node identity (persistent)
	kp, err := identity.LoadOrCreate(cfg.DataDir)
	if err != nil {
		log.Fatalf("identity: %v", err)
	}
	cfg.NodeID = kp.NodeID

	fmt.Println("NexusOS Coordination Service")
	fmt.Println("============================")
	fmt.Printf("Node ID     : %s\n", kp.NodeID)
	fmt.Printf("Data dir    : %s\n", cfg.DataDir)
	fmt.Printf("Listen      : %s\n", cfg.ListenAddr)
	fmt.Printf("Runtime     : %s\n", map[bool]string{true: "mock", false: "containerd"}[cfg.UseMockRuntime])
	fmt.Printf("Auth        : %v\n", cfg.APIToken != "")
	fmt.Printf("TLS         : %v\n", cfg.TLSEnabled())
	fmt.Printf("Dev mode    : %v\n", cfg.Dev)
	fmt.Printf("Digest req  : %v\n", cfg.RequireImageDigest)
	fmt.Printf("Mode        : %s\n", cfg.Mode)
	fmt.Printf("Advertise   : %s\n", cfg.AdvertiseURL)
	fmt.Printf("Join token  : %v\n", cfg.JoinToken != "")
	fmt.Printf("Peers       : %v\n", cfg.Peers)
	fmt.Printf("HB timeout  : %s\n", cfg.HeartbeatTimeout)
	fmt.Printf("Consensus   : %v\n", cfg.Consensus)
	fmt.Printf("Cons. engine: %s\n", cfg.ConsensusEngine)
	if cfg.CometBFTGenesisFrom != "" {
		fmt.Printf("CMT genesis : fetch from %s\n", cfg.CometBFTGenesisFrom)
	}
	fmt.Println()

	store, err := state.NewStore(cfg.DataDir, kp.NodeID)
	if err != nil {
		log.Fatalf("state store: %v", err)
	}

	ledgerStore, err := ledger.NewStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("ledger store: %v", err)
	}

	members, err := membership.NewStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("membership: %v", err)
	}
	ledgerStore.BindMembership(func(recs map[string]ledger.MemberRecord) error {
		list := make([]membership.Member, 0, len(recs))
		for _, m := range recs {
			list = append(list, membership.Member{
				NodeID:    m.NodeID,
				PublicKey: m.PublicKey,
				Addresses: m.Addresses,
				Label:     m.Label,
			})
		}
		return members.ReplaceAll(list)
	})

	peerStore, err := p2p.NewStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("peers: %v", err)
	}
	for _, u := range cfg.Peers {
		if err := peerStore.Upsert(p2p.Peer{URL: u}); err != nil {
			log.Fatalf("bootstrap peer %s: %v", u, err)
		}
	}

	// Register self on the ledger and membership (bootstrap)
	pub := kp.PublicJSON()
	_ = ledgerStore.UpsertNode(ledger.NodeRecord{
		NodeID:    pub.NodeID,
		PublicKey: pub.PublicKey,
		Status:    ledger.StatusOnline,
		Mode:      cfg.Mode,
	})
	_ = members.Upsert(membership.Member{
		NodeID:    pub.NodeID,
		PublicKey: pub.PublicKey,
		Label:     "self",
	})

	peerClient := p2p.NewClient()
	if cfg.TLSEnabled() {
		peerClient = p2p.NewClientTLS(cfg.TLSInsecureSkipVerify)
	}

	engine := &p2p.Engine{
		KP:               kp,
		Advertise:        cfg.AdvertiseURL,
		JoinToken:        cfg.JoinToken,
		Mode:             cfg.Mode,
		Ledger:           ledgerStore,
		Members:          members,
		Peers:            peerStore,
		Client:           peerClient,
		Interval:         cfg.SyncInterval,
		HeartbeatTimeout: cfg.HeartbeatTimeout,
	}

	var cmtNode *cometbft.Node
	if cfg.Consensus {
		if cfg.ConsensusEngine != config.ConsensusEngineCometBFT {
			log.Fatalf("consensus: unsupported engine %q (only cometbft)", cfg.ConsensusEngine)
		}
		cmtApp := cometbft.NewApp(ledgerStore, members)
		home := filepath.Join(cfg.DataDir, "cometbft")
		if cfg.CometBFTGenesisFrom != "" {
			peer, err := cometbft.FetchAndInstallGenesis(context.Background(), home, cometbft.FetchBootstrapOptions{
				SeedURL:     cfg.CometBFTGenesisFrom,
				JoinToken:   cfg.JoinToken,
				APIToken:    cfg.APIToken,
				TLSInsecure: cfg.TLSInsecureSkipVerify || cfg.Dev,
			})
			if err != nil {
				log.Fatalf("cometbft genesis from %s: %v", cfg.CometBFTGenesisFrom, err)
			}
			if cfg.CometBFTPeers == "" && peer != "" {
				cfg.CometBFTPeers = peer
				log.Printf("CometBFT persistent peer from seed bootstrap: %s", peer)
			} else {
				log.Printf("CometBFT shared genesis installed from %s", cfg.CometBFTGenesisFrom)
			}
		}
		var err error
		cmtNode, err = cometbft.StartNode(cmtApp, cometbft.NodeOptions{
			HomeDir:         home,
			RPCListen:       cfg.CometBFTRPC,
			P2PListen:       cfg.CometBFTP2P,
			PersistentPeers: cfg.CometBFTPeers,
			Moniker:         truncateID(kp.NodeID, 12),
			Members:         members,
			IdentityPriv:    kp.PrivateKey,
		})
		if err != nil {
			log.Fatalf("cometbft node: %v", err)
		}
		log.Printf("CometBFT node started (engine=%s, home=%s, rpc=%s, p2p=%s, node_id=%s, height=%d); dynamic validators via FinalizeBlock; see docs/cometbft-spike.md",
			cfg.ConsensusEngine, home, cmtNode.RPCAddress(), cmtNode.P2PAddress(), cmtNode.NodeID(), cmtApp.Height())
	}

	var rt runtime.Runtime
	if cfg.UseMockRuntime {
		rt = runtime.NewMockRuntime()
		log.Println("Using mock runtime (safe for development)")
	} else {
		crt, err := runtime.NewContainerdRuntime(cfg.ContainerdSocket, cfg.Namespace)
		if err != nil {
			log.Fatalf("containerd runtime: %v\n(Hint: is containerd running? Try --mock)", err)
		}
		rt = crt
		log.Printf("Connected to containerd at %s", cfg.ContainerdSocket)
	}
	defer rt.Close()

	var txSubmitter httpapi.TxSubmitter
	if cmtNode != nil {
		txSubmitter = cmtNode
		engine.MembershipTx = cmtNode
	}
	cmtHome := ""
	cmtP2P := ""
	if cmtNode != nil {
		cmtHome = filepath.Join(cfg.DataDir, "cometbft")
		cmtP2P = cfg.CometBFTP2P
	}
	api := httpapi.New(httpapi.Options{
		Addr:               cfg.ListenAddr,
		APIToken:           cfg.APIToken,
		TLSCertFile:        cfg.TLSCertFile,
		TLSKeyFile:         cfg.TLSKeyFile,
		RequireImageDigest: cfg.RequireImageDigest,
		Mode:               cfg.Mode,
		KeyPair:            kp,
		Runtime:            rt,
		Store:              store,
		Ledger:             ledgerStore,
		Members:            members,
		Engine:             engine,
		TxSubmitter:        txSubmitter,
		JoinToken:          cfg.JoinToken,
		CometBFTHome:       cmtHome,
		CometBFTP2P:        cmtP2P,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := api.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("API server: %v", err)
		}
	}()
	go engine.Run(ctx)

	log.Println("Coordinator is running. Press Ctrl+C to stop.")
	<-ctx.Done()
	log.Println("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := api.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	if cmtNode != nil {
		if err := cmtNode.Stop(); err != nil {
			log.Printf("cometbft stop: %v", err)
		}
	}
	log.Println("Bye.")
}

func truncateID(id string, n int) string {
	if len(id) <= n {
		return id
	}
	return id[:n]
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
