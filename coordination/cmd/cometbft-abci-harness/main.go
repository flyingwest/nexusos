// Command cometbft-abci-harness exposes the NexusOS CometBFT ABCI application
// over a socket so an external CometBFT process can drive it.
//
// This is a spike helper for attaching an external CometBFT process.
// The default coordinator path embeds CometBFT in-process.
//
// Example:
//
//	go run ./cmd/cometbft-abci-harness --data-dir /tmp/nexus-abci --addr tcp://127.0.0.1:26658
//	# then point CometBFT proxy_app at that address
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	abciserver "github.com/cometbft/cometbft/abci/server"

	"github.com/nexusos/coordination/internal/consensus/cometbft"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
)

func main() {
	dataDir := flag.String("data-dir", "", "ledger/membership data directory (required)")
	addr := flag.String("addr", "tcp://127.0.0.1:26658", "ABCI listen address")
	transport := flag.String("transport", "socket", "ABCI transport: socket | grpc")
	flag.Parse()

	if *dataDir == "" {
		log.Fatal("--data-dir is required")
	}

	led, err := ledger.NewStore(*dataDir)
	if err != nil {
		log.Fatalf("ledger: %v", err)
	}
	members, err := membership.NewStore(*dataDir)
	if err != nil {
		log.Fatalf("membership: %v", err)
	}

	app := cometbft.NewApp(led, members)
	srv, err := abciserver.NewServer(*addr, *transport, app)
	if err != nil {
		log.Fatalf("abci server: %v", err)
	}
	if err := srv.Start(); err != nil {
		log.Fatalf("abci start: %v", err)
	}
	log.Printf("NexusOS ABCI harness listening on %s (%s); Ctrl+C to stop", *addr, *transport)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	log.Println("stopping ABCI harness...")
	if err := srv.Stop(); err != nil {
		log.Printf("stop: %v", err)
	}
}
