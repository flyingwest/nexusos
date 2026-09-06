# Phase 3 – Native Orchestration (Workloads)

Declarative **Workloads** sit on top of the distributed OS / CometBFT ledger.
Scope is intentionally small: create → schedule → reconcile → scale → rolling update. Not Kubernetes.

## Objects

### Workload (ledger)

- `workload_id`, `owner`, `image_digest`, optional `image_ref`
- `replicas`, coarse `resources`
- `strategy`: `RollingUpdate` (default) or `Recreate`
- `max_unavailable`: RollingUpdate budget per reconcile tick (default **1** = one-at-a-time)
- `status.{desired,current,available}` (current/available derived from placement containers)

### Placement containers

Each replica becomes a ledger `Container` with deterministic id:

```text
wl:<workload_id>:<replica_index>
```

Fields: `workload_id`, `replica_index`, `current_node`, `desired_state=Running`, `image_digest`.

## Update strategies

| Strategy | On image / key spec change |
|----------|----------------------------|
| **RollingUpdate** (default) | Update at most `max_unavailable` outdated replica placements per reconcile pass (lowest replica index first). Local runtime restarts only replicas whose ledger digest changed. Scale-up still creates all missing replicas immediately. |
| **Recreate** | Update all replica placement digests in one pass (then local runtime restarts all mismatched). |

Prefer simple correct rolls over Kubernetes-parity: no `maxSurge`, no readiness probes on the ledger. Observed availability is best-effort from placement + local runtime.

## Transactions (CometBFT)

| Tx | Effect |
|----|--------|
| `CreateWorkload` / `UpdateWorkload` | Upsert workload intent (image, strategy, replicas, …) |
| `ScaleWorkload` | Change `replicas` only |
| `DeleteWorkload` | Remove workload + `workload:<id>` tombstone |
| `CreateContainer` / `UpdateContainer` / `RemoveContainer` | Placement (scheduler/reconciler) |

AppHash includes `workloads` (with images/containers/members/tombstones), including `strategy` / `max_unavailable`.

## Scheduler

**Least-loaded by ledger container count**, ties broken by sorted `node_id`.
Only **Online** nodes (not offline/Draining). Existing replica→node sticky if still Online.
Deterministic so every coordinator computes the same plan.

## Reconciler

Runs in each coordinator (~2s):

1. For each workload, ensure placement containers match the schedule; submit placement txs via CometBFT mempool.
2. On image/spec change, apply the workload **strategy** (rolling budget vs recreate-all).
3. Remove orphan replica containers (deleted workload or index ≥ replicas).
4. **Local runtime**: start containers assigned to this node; **restart** when ledger `image_digest` diverges; stop/remove local workload containers no longer desired here.

### Failure policy

If `runtime.Start` fails, desired placement **stays on the ledger**; the loop retries next tick.
No automatic reschedule away from a healthy Online node in this increment.

## API / CLI

```text
POST   /v1/workloads
PUT    /v1/workloads/{id}
GET    /v1/workloads
GET    /v1/workloads/{id}
DELETE /v1/workloads/{id}
POST   /v1/workloads/{id}/scale   {"replicas":N}

nexusctl workloads create <id> --image <ref> [--digest sha256:...] --replicas N \
  [--strategy RollingUpdate|Recreate] [--max-unavailable N]
nexusctl workloads update <id> --image <ref> [--strategy ...] [--max-unavailable N]
nexusctl workloads list|get|scale|delete ...
```

Auth: same API bearer token (+ TLS) as Phase 2.

## Verify

```bash
cd coordination && go test ./...
./scripts/e2e-workload-mock.sh
./scripts/e2e-workload-multinode.sh  # two-node CometBFT + cross-node placements
./scripts/e2e-cometbft-two-node.sh   # unchanged two-node consensus
# or: make e2e-workload / make e2e-workload-multinode
```

The multi-node harness (`scripts/e2e-workload-multinode.sh`) starts two
coordinators with shared genesis (`--cometbft-genesis-from`), pairs membership,
syncs Online heartbeats, creates a workload with `replicas=2`, and asserts
ledger `current_node` differs across replicas plus local mock runtime containers
on both nodes. Optional scale up/down is included. The mock e2e also exercises a
**RollingUpdate** image change and asserts a mid-roll mixed digest state.

## Deferred

Affinities/anti-affinities, bin-packing by resources, maxSurge / readiness gates,
UI, live migration, permissionless scheduling. (Cold migration: see `docs/phase4-migration.md`.)
