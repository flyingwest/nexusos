// Package metrics exposes Prometheus-style registry helpers for the coordinator.
package metrics

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the process-wide NexusOS metrics registry (isolated from CometBFT defaults).
var Registry = prometheus.NewRegistry()

var (
	APIRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nexusos_api_requests_total",
		Help: "HTTP API requests by method, path template, and status class.",
	}, []string{"method", "path", "code"})

	Containers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexusos_containers",
		Help: "Ledger container count.",
	})
	Workloads = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexusos_workloads",
		Help: "Ledger workload count.",
	})
	Migrations = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexusos_migrations",
		Help: "Ledger migration record count.",
	})
	NodesOnline = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexusos_nodes_online",
		Help: "Ledger nodes with Online status.",
	})
	NodesDraining = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexusos_nodes_draining",
		Help: "Ledger nodes with Draining status (scheduling disabled).",
	})
	Peers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexusos_peers",
		Help: "Configured HTTP sync peers.",
	})
	ConsensusHeight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexusos_consensus_height",
		Help: "CometBFT app height when consensus is enabled.",
	})
)

func init() {
	Registry.MustRegister(APIRequests, Containers, Workloads, Migrations, NodesOnline, NodesDraining, Peers, ConsensusHeight)
}

// Handler returns a Prometheus text exposition handler for Registry.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Timeout: 5 * time.Second})
}

// Instrument wraps a handler and increments nexusos_api_requests_total.
func Instrument(method, pathLabel string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rw := &codeCapture{ResponseWriter: w, code: 200}
		next(rw, r)
		class := strconv.Itoa(rw.code/100) + "xx"
		APIRequests.WithLabelValues(method, pathLabel, class).Inc()
	}
}

type codeCapture struct {
	http.ResponseWriter
	code int
}

func (c *codeCapture) WriteHeader(status int) {
	c.code = status
	c.ResponseWriter.WriteHeader(status)
}

// Snapshot holds gauge inputs refreshed before scrape.
type Snapshot struct {
	Containers      int
	Workloads       int
	Migrations      int
	NodesOnline     int
	NodesDraining   int
	Peers           int
	ConsensusHeight int64
}

var last atomic.Value // Snapshot

// SetSnapshot stores the latest gauge inputs.
func SetSnapshot(s Snapshot) {
	last.Store(s)
	Containers.Set(float64(s.Containers))
	Workloads.Set(float64(s.Workloads))
	Migrations.Set(float64(s.Migrations))
	NodesOnline.Set(float64(s.NodesOnline))
	NodesDraining.Set(float64(s.NodesDraining))
	Peers.Set(float64(s.Peers))
	ConsensusHeight.Set(float64(s.ConsensusHeight))
}
