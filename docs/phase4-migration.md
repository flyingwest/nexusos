# Phase 4 – Coordinated Cold Migration (CRIU)

Cold migration moves a **stopped** (checkpointed) container from node A to node B
as a first-class **CometBFT ledger transaction**. Live migration is out of scope.

## Flow

```text
operator / nexusctl
       │
       ▼
POST /v1/migrations  {container_id, to_node}
       │
       ├─ MsgProposeMigration     → status=InProgress, Desired=Migrating
       ├─ Checkpoint on source    → mock dump or containerd+CRIU
       ├─ Transfer artifact       → POST /v1/migrations/restore on dest
       ├─ Restore on destination
       └─ MsgCompleteMigration    → current_node=to, Desired=Running
            (on failure: MsgFailMigration → Desired=Running on from_node)
```

## Ledger

| Tx | Effect |
|----|--------|
| `ProposeMigration` | Create `MigrationRecord` (InProgress); set container `desired_state=Migrating` |
| `CompleteMigration` | `status=Success`; set `current_node=to_node`, `desired_state=Running`; store `checkpoint_hash` |
| `FailMigration` | `status=Failed`; rollback placement to `from_node` + `Running` |

AppHash already includes `migrations` (canonical state). Security bar unchanged
(API token + TLS; permissioned membership).

### MigrationRecord

```text
MigrationRecord {
  migration_id, container_id, from_node, to_node,
  status: Pending | InProgress | Success | Failed,
  checkpoint_hash, started_at, finished_at
}
```

## Runtime

| Path | Behavior |
|------|----------|
| **Mock** | Always supports checkpoint/restore; writes `nexusos-checkpoint.json` + `dump.marker`; stops source then restores on dest. |
| **containerd + CRIU** | `SupportsCheckpointRestore()` runs `criu check`. If CRIU is absent, checkpoint/restore return `ErrCheckpointUnsupported`. When present, containerd `Task.Checkpoint` is attempted; restore falls back to cold start from checkpoint meta image if a full CRIU image restore is not wired. |

**Host requirement for real CRIU**: install `criu`, enable kernel features (`criu check`), and run containerd with a CRIU-capable runtime. CI/builders without CRIU use the mock path.

## API / CLI

```text
GET    /v1/migrations
GET    /v1/migrations/{id}
POST   /v1/migrations                 {"container_id","to_node","from_node"?}
POST   /v1/migrations/checkpoint      (peer RPC: source dump)
POST   /v1/migrations/restore         (peer RPC: dest apply artifact)

nexusctl migrations list|get
nexusctl migrations migrate <container-id> --to <node_id> [--from <node_id>]
```

Peer RPCs use the same API bearer token. Artifact transfer is a JSON bundle
(base64 files) suitable for mock dumps; large CRIU images should use a future
streaming path.

## Reconciler interaction

While `desired_state=Migrating` (or an InProgress migration exists), the Phase 3
workload reconciler **skips** that container so placement updates do not fight
the migration controller.

## Verify

```bash
cd coordination && go test ./...
./scripts/e2e-migration-multinode.sh   # or: make e2e-migration
./scripts/e2e-workload-multinode.sh    # still PASS
./scripts/e2e-cometbft-two-node.sh     # still PASS
```

## Deferred

- Live migration
- Streaming / content-addressed artifact store for large CRIU dumps
- Full containerd CRIU image restore (vs cold start from image digest)
- UI, orchestrator auto-evacuate on drain
- AcceptMigration as a separate ack tx (propose currently implies accept under permissioned trust)
