package orchestrate

import (
	"sort"

	"github.com/nexusos/coordination/internal/ledger"
)

// Placement is a desired replica → node assignment.
type Placement struct {
	ReplicaIndex uint32
	NodeID       string
	ContainerID  string
}

// OnlineNodeIDs returns sorted node IDs with Status Online (or empty/unknown treated as online).
func OnlineNodeIDs(nodes map[string]ledger.NodeRecord) []string {
	out := make([]string, 0, len(nodes))
	for id, n := range nodes {
		if n.Status == ledger.StatusOffline || n.Status == ledger.StatusDraining {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// CountContainersByNode tallies ledger containers currently assigned per node.
func CountContainersByNode(containers map[string]ledger.ContainerRecord) map[string]int {
	out := make(map[string]int)
	for _, c := range containers {
		if c.CurrentNode == "" {
			continue
		}
		out[c.CurrentNode]++
	}
	return out
}

// ScheduleLeastLoaded places replicas across online nodes preferring the
// currently least-loaded (by ledger container count). Ties break by node_id
// ascending so all coordinators compute the same plan.
//
// Existing placements for the same workload+replica are preserved when the
// assigned node is still Online.
func ScheduleLeastLoaded(wl ledger.WorkloadRecord, st ledger.State) []Placement {
	online := OnlineNodeIDs(st.Nodes)
	if len(online) == 0 {
		return nil
	}
	loads := CountContainersByNode(st.Containers)

	// Preserve sticky placements for existing replica indices still on online nodes.
	existing := make(map[uint32]string) // replica -> node
	for _, c := range st.Containers {
		if c.WorkloadID != wl.WorkloadID || c.ReplicaIndex == nil {
			continue
		}
		idx := *c.ReplicaIndex
		if idx >= wl.Replicas {
			continue
		}
		if contains(online, c.CurrentNode) {
			existing[idx] = c.CurrentNode
		}
	}

	out := make([]Placement, 0, wl.Replicas)
	for i := uint32(0); i < wl.Replicas; i++ {
		node := existing[i]
		if node == "" {
			node = pickLeastLoaded(online, loads)
		}
		loads[node]++
		out = append(out, Placement{
			ReplicaIndex: i,
			NodeID:       node,
			ContainerID:  ledger.ReplicaContainerID(wl.WorkloadID, i),
		})
	}
	return out
}

func pickLeastLoaded(online []string, loads map[string]int) string {
	best := online[0]
	bestLoad := loads[best]
	for _, id := range online[1:] {
		l := loads[id]
		if l < bestLoad || (l == bestLoad && id < best) {
			best = id
			bestLoad = l
		}
	}
	return best
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
