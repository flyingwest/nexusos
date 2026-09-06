package orchestrate

import (
	"context"
	"fmt"
	"sort"

	"github.com/nexusos/coordination/internal/ledger"
)

// Cordon sets a node to Draining so the scheduler skips it for new placements.
// Status is ledger-local (HTTP sync); not a consensus tx.
func Cordon(led *ledger.Store, nodeID string) (ledger.NodeRecord, error) {
	if led == nil {
		return ledger.NodeRecord{}, fmt.Errorf("ledger not configured")
	}
	if nodeID == "" {
		return ledger.NodeRecord{}, fmt.Errorf("node_id is required")
	}
	rec, ok := led.GetNode(nodeID)
	if !ok {
		rec = ledger.NodeRecord{NodeID: nodeID, Mode: "permissioned"}
	}
	if rec.Status == ledger.StatusOffline {
		return rec, fmt.Errorf("node %s is Offline; cannot cordon", nodeID)
	}
	rec.Status = ledger.StatusDraining
	if err := led.UpsertNode(rec); err != nil {
		return ledger.NodeRecord{}, err
	}
	out, _ := led.GetNode(nodeID)
	return out, nil
}

// Uncordon restores Online (scheduling enabled) when the node is Draining.
func Uncordon(led *ledger.Store, nodeID string) (ledger.NodeRecord, error) {
	if led == nil {
		return ledger.NodeRecord{}, fmt.Errorf("ledger not configured")
	}
	if nodeID == "" {
		return ledger.NodeRecord{}, fmt.Errorf("node_id is required")
	}
	rec, ok := led.GetNode(nodeID)
	if !ok {
		return ledger.NodeRecord{}, fmt.Errorf("node %s not found", nodeID)
	}
	if rec.Status == ledger.StatusOffline {
		return rec, fmt.Errorf("node %s is Offline; cannot uncordon", nodeID)
	}
	rec.Status = ledger.StatusOnline
	if err := led.UpsertNode(rec); err != nil {
		return ledger.NodeRecord{}, err
	}
	out, _ := led.GetNode(nodeID)
	return out, nil
}

// EvacuatePlan describes what drain will do after cordon.
type EvacuatePlan struct {
	NodeID              string   `json:"node_id"`
	WorkloadContainers  []string `json:"workload_containers"`  // left to reconciler/reschedule
	StandaloneMigrate   []string `json:"standalone_migrate"`   // cold-migrate via MigrationController
	SkippedMigrating    []string `json:"skipped_migrating,omitempty"`
	NoDestination       []string `json:"no_destination,omitempty"`
	DestinationNode     string   `json:"destination_node,omitempty"`
}

// PlanEvacuate lists containers on nodeID and chooses a destination Online node.
// Workload-owned replicas are rescheduled by the reconciler once the node is Draining
// (sticky placement does not preserve draining nodes). Standalone containers need cold migrate.
func PlanEvacuate(st ledger.State, nodeID string) EvacuatePlan {
	plan := EvacuatePlan{NodeID: nodeID}
	online := OnlineNodeIDs(st.Nodes)
	var dests []string
	for _, id := range online {
		if id != nodeID {
			dests = append(dests, id)
		}
	}
	sort.Strings(dests)
	if len(dests) > 0 {
		loads := CountContainersByNode(st.Containers)
		plan.DestinationNode = pickLeastLoaded(dests, loads)
	}

	ids := make([]string, 0)
	for id, c := range st.Containers {
		if c.CurrentNode == nodeID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := st.Containers[id]
		if SkipMigrating(c, st) {
			plan.SkippedMigrating = append(plan.SkippedMigrating, id)
			continue
		}
		if c.WorkloadID != "" {
			plan.WorkloadContainers = append(plan.WorkloadContainers, id)
			continue
		}
		if plan.DestinationNode == "" {
			plan.NoDestination = append(plan.NoDestination, id)
			continue
		}
		plan.StandaloneMigrate = append(plan.StandaloneMigrate, id)
	}
	return plan
}

// DrainResult is returned by Drain after cordon (+ optional evacuate).
type DrainResult struct {
	Node     ledger.NodeRecord `json:"node"`
	Plan     EvacuatePlan      `json:"plan"`
	Migrated []MigrateResult   `json:"migrated,omitempty"`
	Errors   []string          `json:"errors,omitempty"`
}

// Drain cordons the node and optionally evacuates standalone containers via migrator.
// Workload replicas are listed in the plan for reconciler reschedule (no live migration).
func Drain(ctx context.Context, led *ledger.Store, migrator *MigrationController, nodeID string, evacuate bool) (*DrainResult, error) {
	node, err := Cordon(led, nodeID)
	if err != nil {
		return nil, err
	}
	st := led.Snapshot()
	plan := PlanEvacuate(st, nodeID)
	out := &DrainResult{Node: node, Plan: plan}
	if !evacuate {
		return out, nil
	}
	if migrator == nil {
		if len(plan.StandaloneMigrate) > 0 {
			out.Errors = append(out.Errors, "migration controller not configured; standalone containers not moved")
		}
		return out, nil
	}
	for _, cid := range plan.StandaloneMigrate {
		res, err := migrator.Run(ctx, MigrateRequest{
			ContainerID: cid,
			ToNode:      plan.DestinationNode,
			FromNode:    nodeID,
		})
		if res != nil {
			out.Migrated = append(out.Migrated, *res)
		}
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("%s: %v", cid, err))
		}
	}
	// Refresh node after migrations.
	if n, ok := led.GetNode(nodeID); ok {
		out.Node = n
	}
	return out, nil
}

// SchedulingDisabled reports whether the node refuses new placements.
func SchedulingDisabled(n ledger.NodeRecord) bool {
	return n.Status == ledger.StatusDraining || n.Status == ledger.StatusOffline
}
