package orchestrate

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/runtime"
)

// TxSubmitter submits signed ledger txs (CometBFT mempool or test stub).
type TxSubmitter interface {
	Submit(ctx context.Context, tx ledger.Tx) error
}

// Reconciler compares desired workload placement on the ledger with local
// runtime state and submits placement txs / starts|stops containers.
type Reconciler struct {
	KP       *identity.KeyPair
	Ledger   *ledger.Store
	Runtime  runtime.Runtime
	Submit   TxSubmitter
	NodeID   string
	Interval time.Duration
	// LocalOnly when true skips cross-node placement txs and only starts local
	// containers for already-placed ledger records (useful in some tests).
	LocalOnly bool
}

// Run loops until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	if r.Interval <= 0 {
		r.Interval = 2 * time.Second
	}
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		if err := r.Once(ctx); err != nil {
			log.Printf("orchestrate reconcile: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once performs a single reconcile pass.
func (r *Reconciler) Once(ctx context.Context) error {
	if r.Ledger == nil || r.Runtime == nil || r.KP == nil {
		return fmt.Errorf("reconciler not configured")
	}
	st := r.Ledger.Snapshot()
	now := time.Now().UTC()

	// 1) Ensure placement containers exist for each workload (deterministic).
	if !r.LocalOnly {
		for _, wl := range st.Workloads {
			if err := r.ensurePlacements(ctx, st, wl, now); err != nil {
				log.Printf("orchestrate placement %s: %v", wl.WorkloadID, err)
			}
		}
		// Refresh after placement mutations may have been submitted (best-effort).
		st = r.Ledger.Snapshot()
	}

	// 2) Remove orphan workload containers (replica index >= replicas or deleted workload).
	if !r.LocalOnly {
		for _, c := range st.Containers {
			if c.WorkloadID == "" {
				continue
			}
			wl, ok := st.Workloads[c.WorkloadID]
			remove := !ok
			if ok && c.ReplicaIndex != nil && *c.ReplicaIndex >= wl.Replicas {
				remove = true
			}
			if remove {
				if err := r.submit(ctx, ledger.MsgRemoveContainer, ledger.RemovePayload{ContainerID: c.ContainerID}, now); err != nil {
					log.Printf("orchestrate remove orphan %s: %v", c.ContainerID, err)
				}
			}
		}
		st = r.Ledger.Snapshot()
	}

	// 3) Local runtime: start desired containers assigned to this node; stop extras.
	return r.reconcileLocal(ctx, st)
}

func (r *Reconciler) ensurePlacements(ctx context.Context, st ledger.State, wl ledger.WorkloadRecord, now time.Time) error {
	if wl.Replicas == 0 {
		return nil
	}
	placements := ScheduleLeastLoaded(wl, st)
	if len(placements) == 0 {
		return fmt.Errorf("no online nodes to place workload %s", wl.WorkloadID)
	}
	for _, p := range placements {
		existing, ok := st.Containers[p.ContainerID]
		if ok && existing.CurrentNode == p.NodeID && existing.Desired == "Running" &&
			existing.ImageDigest == wl.ImageDigest && existing.WorkloadID == wl.WorkloadID {
			continue
		}
		idx := p.ReplicaIndex
		labels := map[string]string{}
		for k, v := range wl.Labels {
			labels[k] = v
		}
		labels["nexusos.workload_id"] = wl.WorkloadID
		labels["nexusos.replica_index"] = fmt.Sprintf("%d", p.ReplicaIndex)
		payload := ledger.ContainerPayload{
			ContainerID:  p.ContainerID,
			ImageDigest:  wl.ImageDigest,
			Desired:      "Running",
			CurrentNode:  p.NodeID,
			WorkloadID:   wl.WorkloadID,
			ReplicaIndex: &idx,
			Labels:       labels,
			Owner:        wl.Owner,
		}
		typ := ledger.MsgCreateContainer
		if ok {
			typ = ledger.MsgUpdateContainer
		}
		if err := r.submit(ctx, typ, payload, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) reconcileLocal(ctx context.Context, st ledger.State) error {
	local := make(map[string]ledger.ContainerRecord)
	for id, c := range st.Containers {
		if c.CurrentNode == r.NodeID && c.Desired == "Running" {
			local[id] = c
		}
	}

	running, err := r.Runtime.List(ctx)
	if err != nil {
		return err
	}
	have := make(map[string]runtime.ContainerInfo, len(running))
	for _, c := range running {
		have[c.ID] = c
	}

	// Start missing
	for id, want := range local {
		if _, ok := have[id]; ok {
			continue
		}
		ref := want.ImageDigest
		if wl, ok := st.Workloads[want.WorkloadID]; ok && wl.ImageRef != "" {
			ref = wl.ImageRef
		}
		if ref == "" {
			log.Printf("orchestrate: skip start %s: empty image", id)
			continue
		}
		_, err := r.Runtime.Start(ctx, runtime.StartOptions{
			ID:       id,
			Name:     id,
			ImageRef: ref,
			Labels:   want.Labels,
		})
		if err != nil {
			// Failure policy: leave desired on ledger; retry next tick.
			log.Printf("orchestrate start %s: %v (will retry)", id, err)
			continue
		}
		log.Printf("orchestrate started container %s for workload %s", id, want.WorkloadID)
	}

	// Stop/remove local containers that belong to workloads but are no longer desired here.
	for id, info := range have {
		if _, ok := local[id]; ok {
			continue
		}
		// Only manage containers we created (deterministic wl: prefix or label).
		if info.Labels != nil {
			if _, ok := info.Labels["nexusos.workload_id"]; !ok {
				continue
			}
		} else if len(id) < 3 || id[:3] != "wl:" {
			continue
		}
		_ = r.Runtime.Stop(ctx, id, 5*time.Second)
		if err := r.Runtime.Remove(ctx, id); err != nil {
			log.Printf("orchestrate remove local %s: %v", id, err)
		}
	}
	return nil
}

func (r *Reconciler) submit(ctx context.Context, typ ledger.MessageType, payload any, now time.Time) error {
	if r.Submit == nil {
		return fmt.Errorf("no tx submitter")
	}
	tx, err := ledger.NewTx(r.KP, typ, payload, now)
	if err != nil {
		return err
	}
	return r.Submit.Submit(ctx, tx)
}
