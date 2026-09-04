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
		cmdSync(),
		cmdChain(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func applyAuth(req *http.Request) {
	if apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+apiToken)
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
		Short: "Remove a member",
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

func cmdSync() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Push/pull signed ledger snapshots with all peers now",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doJSON("POST", "/v1/cluster/sync", map[string]string{}, nil)
		},
	}
}

func cmdChain() *cobra.Command {
	return &cobra.Command{
		Use:   "chain",
		Short: "Show consensus chain height, leader, and quorum",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doGET("/v1/chain", nil)
		},
	}
}
