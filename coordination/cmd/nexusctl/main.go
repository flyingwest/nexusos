package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var apiBase = "https://127.0.0.1:8080"
var apiToken = ""
var joinToken = ""
var tlsInsecure = false
var tlsCAFile = ""

func main() {
	root := &cobra.Command{
		Use:   "nexusctl",
		Short: "NexusOS command-line client",
		Long:  "Talk to a NexusOS coordination service over HTTPS (use --insecure for self-signed / --dev).",
	}

	root.PersistentFlags().StringVar(&apiBase, "api", apiBase, "API base URL")
	root.PersistentFlags().StringVar(&apiToken, "token", "", "API bearer token")
	root.PersistentFlags().StringVar(&joinToken, "join-token", "", "cluster join-token (for CometBFT bootstrap fetch)")
	root.PersistentFlags().BoolVar(&tlsInsecure, "insecure", false, "skip TLS certificate verification (self-signed)")
	root.PersistentFlags().StringVar(&tlsCAFile, "cacert", "", "optional CA certificate PEM for TLS verification")

	root.AddCommand(
		cmdHealth(),
		cmdNode(),
		cmdNodes(),
		cmdContainers(),
		cmdImages(),
		cmdState(),
		cmdLedger(),
		cmdMembers(),
		cmdPeers(),
		cmdPair(),
		cmdLeave(),
		cmdSync(),
		cmdCometBFT(),
		cmdWorkloads(),
		cmdMigrations(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func applyAuth(req *http.Request) {
	if apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+apiToken)
	}
	if joinToken != "" {
		req.Header.Set("X-Nexus-Join-Token", joinToken)
	}
}

func httpClient() (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tlsCfg := &tls.Config{}
	if tlsInsecure {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec
	}
	if tlsCAFile != "" {
		pem, err := os.ReadFile(tlsCAFile)
		if err != nil {
			return nil, fmt.Errorf("read cacert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("cacert: no certificates found in %s", tlsCAFile)
		}
		tlsCfg.RootCAs = pool
	}
	tr.TLSClientConfig = tlsCfg
	return &http.Client{Timeout: 30 * time.Second, Transport: tr}, nil
}

func doGET(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, apiBase+path, nil)
	if err != nil {
		return err
	}
	applyAuth(req)
	client, err := httpClient()
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	if out == nil {
		fmt.Println(string(body))
		return nil
	}
	return json.Unmarshal(body, out)
}

func doJSON(method, path string, in any, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, apiBase+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	applyAuth(req)
	client, err := httpClient()
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	if out == nil {
		fmt.Println(string(data))
		return nil
	}
	return json.Unmarshal(data, out)
}

func cmdHealth() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Check coordinator health",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/health", nil)
		},
	}
}

func cmdNode() *cobra.Command {
	return &cobra.Command{
		Use:   "node",
		Short: "Show local node info",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/node", nil)
		},
	}
}

func cmdNodes() *cobra.Command {
	return &cobra.Command{
		Use:   "nodes",
		Short: "List cluster nodes and Online/Offline status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/nodes", nil)
		},
	}
}

func cmdContainers() *cobra.Command {
	c := &cobra.Command{
		Use:   "containers",
		Short: "Manage containers",
	}

	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/containers", nil)
		},
	})

	startCmd := &cobra.Command{
		Use:   "start [image-ref]",
		Short: "Start a container from an image reference",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			payload := map[string]any{
				"image_ref": args[0],
				"name":      name,
			}
			return doJSON("POST", "/v1/containers", payload, nil)
		},
	}
	startCmd.Flags().String("name", "", "optional container name")
	c.AddCommand(startCmd)

	c.AddCommand(&cobra.Command{
		Use:   "stop [id]",
		Short: "Stop a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON("POST", "/v1/containers/"+args[0]+"/stop", nil, nil)
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "rm [id]",
		Short: "Remove a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON("DELETE", "/v1/containers/"+args[0], nil, nil)
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "get [id]",
		Short: "Get container details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/containers/"+args[0], nil)
		},
	})

	return c
}

func cmdImages() *cobra.Command {
	c := &cobra.Command{
		Use:   "images",
		Short: "Manage images",
	}

	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List known images",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/images", nil)
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "pull [ref]",
		Short: "Pull (or register in mock mode) an image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON("POST", "/v1/images/pull", map[string]string{"ref": args[0]}, nil)
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "verify [ref]",
		Short: "Verify an image digest/ref",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON("POST", "/v1/images/verify", map[string]string{"ref": args[0]}, nil)
		},
	})

	return c
}

func cmdState() *cobra.Command {
	return &cobra.Command{
		Use:   "state",
		Short: "Dump local coordinator state",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/state", nil)
		},
	}
}

func cmdLedger() *cobra.Command {
	return &cobra.Command{
		Use:   "ledger",
		Short: "Show network ledger snapshot (placement + image integrity)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/ledger", nil)
		},
	}
}

func cmdMembers() *cobra.Command {
	c := &cobra.Command{
		Use:   "members",
		Short: "Permissioned membership list",
	}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List members",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/members", nil)
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "add [node-id]",
		Short: "Add a member by node id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pk, _ := cmd.Flags().GetString("public-key")
			label, _ := cmd.Flags().GetString("label")
			return doJSON("POST", "/v1/members", map[string]string{
				"node_id":    args[0],
				"public_key": pk,
				"label":      label,
			}, nil)
		},
	})
	for _, sc := range c.Commands() {
		if sc.Name() == "add" {
			sc.Flags().String("public-key", "", "optional public key hex")
			sc.Flags().String("label", "", "optional label")
		}
	}
	c.AddCommand(&cobra.Command{
		Use:   "rm [node-id]",
		Short: "Evict a member (LeaveMember via API token; refuses last validator)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON("DELETE", "/v1/members/"+args[0], nil, nil)
		},
	})
	return c
}

func cmdPeers() *cobra.Command {
	c := &cobra.Command{
		Use:   "peers",
		Short: "Known coordinator peers",
	}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List peers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/peers", nil)
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "add [url]",
		Short: "Add a peer URL (does not pair membership)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON("POST", "/v1/peers", map[string]string{"url": args[0]}, nil)
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "rm [url]",
		Short: "Remove a peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON("POST", "/v1/peers/remove", map[string]string{"url": args[0]}, nil)
		},
	})
	return c
}

func cmdPair() *cobra.Command {
	return &cobra.Command{
		Use:   "pair [url]",
		Short: "Exchange membership with another coordinator (permissioned join)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON("POST", "/v1/cluster/pair", map[string]string{"url": args[0]}, nil)
		},
	}
}

func cmdLeave() *cobra.Command {
	return &cobra.Command{
		Use:   "leave",
		Short: "Self-leave: LeaveMember for this node (API token; refuses last validator)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON("POST", "/v1/cluster/leave", map[string]string{}, nil)
		},
	}
}

func cmdSync() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Push/pull signed ledger snapshots with all peers now",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON("POST", "/v1/cluster/sync", map[string]string{}, nil)
		},
	}
}

func cmdCometBFT() *cobra.Command {
	c := &cobra.Command{
		Use:   "cometbft",
		Short: "CometBFT helpers (shared genesis bootstrap)",
	}
	c.AddCommand(&cobra.Command{
		Use:   "bootstrap",
		Short: "Fetch shared genesis + peer info from a seed coordinator",
		Long:  "GET /v1/cometbft/bootstrap (API token and/or --join-token). Prints JSON with genesis, peer, and p2p fields.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/cometbft/bootstrap", nil)
		},
	})
	fetch := &cobra.Command{
		Use:   "fetch-genesis",
		Short: "Download seed genesis into a local data-dir (before starting coordinator)",
		Long:  "Writes <data-dir>/cometbft/config/genesis.json from the seed bootstrap endpoint. Prints the suggested --cometbft-peers value.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			if dataDir == "" {
				return fmt.Errorf("--data-dir is required")
			}
			var info struct {
				Genesis   json.RawMessage `json:"genesis"`
				Peer      string          `json:"peer"`
				P2PNodeID string          `json:"p2p_node_id"`
				P2PListen string          `json:"p2p_listen"`
				ChainID   string          `json:"chain_id"`
			}
			if err := doGET("/v1/cometbft/bootstrap", &info); err != nil {
				return err
			}
			if len(info.Genesis) == 0 {
				return fmt.Errorf("bootstrap response missing genesis")
			}
			genPath := dataDir + "/cometbft/config/genesis.json"
			if err := os.MkdirAll(dataDir+"/cometbft/config", 0o700); err != nil {
				return err
			}
			if _, err := os.Stat(genPath); err == nil {
				fmt.Fprintf(os.Stderr, "genesis already exists at %s (leaving unchanged)\n", genPath)
			} else {
				if err := os.WriteFile(genPath, append(info.Genesis, '\n'), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "wrote %s\n", genPath)
			}
			peer := info.Peer
			if peer == "" && info.P2PNodeID != "" && info.P2PListen != "" {
				peer = info.P2PNodeID + "@" + info.P2PListen
			}
			if peer != "" {
				fmt.Println(peer)
			}
			return nil
		},
	}
	fetch.Flags().String("data-dir", "", "local coordinator data directory")
	c.AddCommand(fetch)
	return c
}

func cmdWorkloads() *cobra.Command {
	c := &cobra.Command{
		Use:   "workloads",
		Short: "Manage declarative workloads (Phase 3)",
	}

	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List workloads",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/workloads", nil)
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "get [id]",
		Short: "Get a workload",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/workloads/"+args[0], nil)
		},
	})

	create := &cobra.Command{
		Use:   "create [id]",
		Short: "Create a workload",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image, _ := cmd.Flags().GetString("image")
			digest, _ := cmd.Flags().GetString("digest")
			replicas, _ := cmd.Flags().GetUint32("replicas")
			strategy, _ := cmd.Flags().GetString("strategy")
			maxUnavail, _ := cmd.Flags().GetUint32("max-unavailable")
			payload := map[string]any{
				"workload_id":     args[0],
				"image_ref":       image,
				"image_digest":    digest,
				"replicas":        replicas,
				"strategy":        strategy,
				"max_unavailable": maxUnavail,
			}
			return doJSON(http.MethodPost, "/v1/workloads", payload, nil)
		},
	}
	create.Flags().String("image", "", "image reference (pulled/resolved if digest omitted)")
	create.Flags().String("digest", "", "image digest sha256:...")
	create.Flags().Uint32("replicas", 1, "desired replica count")
	create.Flags().String("strategy", "RollingUpdate", "update strategy: RollingUpdate or Recreate")
	create.Flags().Uint32("max-unavailable", 0, "RollingUpdate: max replicas to update per reconcile tick (0=1)")
	c.AddCommand(create)

	update := &cobra.Command{
		Use:   "update [id]",
		Short: "Update a workload image/strategy (triggers rolling or recreate)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image, _ := cmd.Flags().GetString("image")
			digest, _ := cmd.Flags().GetString("digest")
			replicas, _ := cmd.Flags().GetUint32("replicas")
			strategy, _ := cmd.Flags().GetString("strategy")
			maxUnavail, _ := cmd.Flags().GetUint32("max-unavailable")
			payload := map[string]any{
				"image_ref":       image,
				"image_digest":    digest,
				"strategy":        strategy,
				"max_unavailable": maxUnavail,
			}
			if cmd.Flags().Changed("replicas") {
				payload["replicas"] = replicas
			}
			return doJSON(http.MethodPut, "/v1/workloads/"+args[0], payload, nil)
		},
	}
	update.Flags().String("image", "", "new image reference")
	update.Flags().String("digest", "", "new image digest sha256:...")
	update.Flags().Uint32("replicas", 0, "optional new replica count")
	update.Flags().String("strategy", "", "optional strategy: RollingUpdate or Recreate")
	update.Flags().Uint32("max-unavailable", 0, "optional RollingUpdate budget (0=preserve/default)")
	c.AddCommand(update)

	scale := &cobra.Command{
		Use:   "scale [id] [replicas]",
		Short: "Scale a workload",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var n uint32
			if _, err := fmt.Sscanf(args[1], "%d", &n); err != nil {
				return fmt.Errorf("replicas: %w", err)
			}
			return doJSON(http.MethodPost, "/v1/workloads/"+args[0]+"/scale", map[string]any{"replicas": n}, nil)
		},
	}
	c.AddCommand(scale)

	c.AddCommand(&cobra.Command{
		Use:   "delete [id]",
		Short: "Delete a workload",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON(http.MethodDelete, "/v1/workloads/"+args[0], nil, nil)
		},
	})

	return c
}

func cmdMigrations() *cobra.Command {
	c := &cobra.Command{
		Use:   "migrations",
		Short: "Cold-migrate containers between nodes (Phase 4)",
	}

	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List migration records",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/migrations", nil)
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "get [id]",
		Short: "Get a migration record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/migrations/"+args[0], nil)
		},
	})

	migrate := &cobra.Command{
		Use:   "migrate [container-id]",
		Short: "Cold-migrate a container to another node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			to, _ := cmd.Flags().GetString("to")
			from, _ := cmd.Flags().GetString("from")
			if to == "" {
				return fmt.Errorf("--to node_id is required")
			}
			payload := map[string]any{
				"container_id": args[0],
				"to_node":      to,
			}
			if from != "" {
				payload["from_node"] = from
			}
			return doJSON(http.MethodPost, "/v1/migrations", payload, nil)
		},
	}
	migrate.Flags().String("to", "", "destination node_id")
	migrate.Flags().String("from", "", "optional source node_id (default: ledger current_node)")
	c.AddCommand(migrate)

	return c
}
