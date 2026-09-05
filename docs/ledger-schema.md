# NexusOS Ledger Schema (Initial)

The ledger is deliberately narrow. Only data required for coordination, integrity, and orchestration belongs here.

## Core Objects

### Node
```text
Node {
  node_id          : string          // hash of public key
  public_key       : bytes
  addresses        : []string
  capacity         : ResourceSpec
  labels           : map[string]string
  status           : Online | Offline | Draining
  last_heartbeat   : timestamp
  mode             : Permissioned | Permissionless
}
```

### Image
```text
Image {
  digest           : string          // sha256:...
  size             : uint64
  verified_by      : []node_id
  first_seen       : timestamp
  references       : uint32
}
```

### Container
```text
Container {
  container_id     : string          // globally unique
  image_digest     : string
  owner            : string
  workload_id      : string | null
  replica_index    : uint32 | null
  desired_state    : Running | Stopped | Migrating
  current_node     : node_id | null
  resources        : ResourceSpec
  labels           : map[string]string
  created_at       : timestamp
  updated_at       : timestamp
}
```

### Workload
```text
Workload {
  workload_id      : string
  owner            : string
  image_digest     : string
  replicas         : uint32
  resources        : ResourceSpec
  constraints      : Constraints
  strategy         : Rolling | Recreate
  status           : {
    desired        : uint32
    current        : uint32
    available      : uint32
  }
  created_at       : timestamp
  updated_at       : timestamp
}
```

### MigrationRecord
```text
MigrationRecord {
  migration_id     : string
  container_id     : string
  from_node        : node_id
  to_node          : node_id
  started_at       : timestamp
  finished_at      : timestamp | null
  status           : Pending | InProgress | Success | Failed
  checkpoint_hash  : string | null
}
```

### Member (consensus-ordered)
```text
Member {
  node_id          : string
  public_key       : string          // 32-byte Ed25519 hex
  addresses        : []string
  label            : string
  updated_at       : timestamp
}
```

Tombstone keys include `member:<node_id>` after LeaveMember (in addition to `container:` / `migration:`).

### ResourceSpec (shared)
```text
ResourceSpec {
  cpu_millis       : uint32
  memory_bytes     : uint64
  disk_bytes       : uint64
  // GPU / other resources later
}
```

## Main Transaction Types (initial set)

On-chain today (CometBFT ABCI):

- RegisterImage / VerifyImage
- CreateContainer / UpdateContainer / RemoveContainer
- JoinMember / LeaveMember (permissioned membership; drives CometBFT validators)
  - LeaveMember stamps tombstone key `member:<node_id>` and refuses leaving the last usable validator


Still off-chain (HTTP snapshot / local):

- RegisterNode / Heartbeat (liveness)
- CreateWorkload / UpdateWorkload / ScaleWorkload / DeleteWorkload (Phase 3)
- ProposeMigration / AcceptMigration / CompleteMigration / FailMigration (Phase 4)

## Design Rules

1. Prefer append-only style records where possible.
2. Keep high-frequency data (detailed metrics, logs) off the ledger.
3. Every placement and migration decision must be attributable to a node key.
4. Image digests are the source of truth for what is allowed to run.
