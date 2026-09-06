package httpapi

import (
	"net/http"

	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/metrics"
	"github.com/nexusos/coordination/internal/version"
)

// HeightProvider reports CometBFT app height when consensus is running.
type HeightProvider interface {
	Height() int64
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, version.Get())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := s.healthPayload(false)
	writeJSON(w, http.StatusOK, resp)
}

// handleReady is an auth-gated readiness check (ledger + identity present).
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	resp := s.healthPayload(true)
	status := http.StatusOK
	if ready, _ := resp["ready"].(bool); !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, resp)
}

func (s *Server) healthPayload(includeReady bool) map[string]any {
	nodeID := ""
	if s.kp != nil {
		nodeID = s.kp.NodeID
	}
	engine := "none"
	if s.txSubmitter != nil {
		engine = "cometbft"
	}
	var containers, workloads, migrations, images, members int
	var nodesOnline, nodesOffline, nodesDraining int
	if s.ledger != nil {
		st := s.ledger.Snapshot()
		containers = len(st.Containers)
		workloads = len(st.Workloads)
		migrations = len(st.Migrations)
		images = len(st.Images)
		members = len(st.Members)
		for _, n := range st.Nodes {
			switch n.Status {
			case ledger.StatusOffline:
				nodesOffline++
			case ledger.StatusDraining:
				nodesDraining++
			default:
				nodesOnline++
			}
		}
	}
	peers := 0
	if s.engine != nil && s.engine.Peers != nil {
		peers = len(s.engine.Peers.List())
	}
	var height int64
	if s.heightProvider != nil {
		height = s.heightProvider.Height()
	}

	resp := map[string]any{
		"status":  "ok",
		"node_id": nodeID,
		"engine":  engine,
		"mode":    s.mode,
		"live":    true,
		"summary": map[string]any{
			"containers":     containers,
			"workloads":      workloads,
			"migrations":     migrations,
			"images":         images,
			"members":        members,
			"nodes_online":   nodesOnline,
			"nodes_offline":  nodesOffline,
			"nodes_draining": nodesDraining,
			"peers":          peers,
		},
	}
	if height > 0 || s.heightProvider != nil {
		resp["consensus_height"] = height
	}
	if includeReady {
		ready := s.kp != nil && s.ledger != nil
		resp["ready"] = ready
		if !ready {
			resp["status"] = "not_ready"
		}
	}
	return resp
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.refreshMetricsSnapshot()
	metrics.Handler().ServeHTTP(w, r)
}

func (s *Server) refreshMetricsSnapshot() {
	snap := metrics.Snapshot{}
	if s.ledger != nil {
		st := s.ledger.Snapshot()
		snap.Containers = len(st.Containers)
		snap.Workloads = len(st.Workloads)
		snap.Migrations = len(st.Migrations)
		for _, n := range st.Nodes {
			switch n.Status {
			case ledger.StatusDraining:
				snap.NodesDraining++
			case ledger.StatusOffline:
			default:
				snap.NodesOnline++
			}
		}
	}
	if s.engine != nil && s.engine.Peers != nil {
		snap.Peers = len(s.engine.Peers.List())
	}
	if s.heightProvider != nil {
		snap.ConsensusHeight = s.heightProvider.Height()
	}
	metrics.SetSnapshot(snap)
}
