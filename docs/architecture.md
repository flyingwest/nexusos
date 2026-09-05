# NexusOS Architecture

## High-Level Overview

```
┌─────────────────────────────────────────────────────────────┐
│                  Management API / UI / CLI                   │
└──────────────────────────────┬──────────────────────────────┘
                               │
┌──────────────────────────────▼──────────────────────────────┐
│         Coordination Service (Go, userspace)                 │
│  • P2P communication between OS nodes                        │
│  • Consensus / shared ledger                                 │
│  • Image integrity                                           │
│  • Placement state                                           │
│  • Node identity & secure communication                      │
│  • (Secondary) Scheduler, reconciler, migration controller   │
│  • Permissioned / Permissionless mode logic                  │
└──────────────────────────────┬──────────────────────────────┘
                               │
┌──────────────────────────────▼──────────────────────────────┐
│                 Container Runtime Layer                      │
│              containerd + runc (OCI)                         │
│  + CRIU integration for migration                            │
└──────────────────────────────┬──────────────────────────────┘
                               │
┌──────────────────────────────▼──────────────────────────────┐
│              Minimal Linux Base (the OS on each node)        │
│     Strong isolation, atomic updates, minimal attack surface │
└─────────────────────────────────────────────────────────────┘
```

Every node runs this full stack. The network of OS instances *is* the system. There is no separate heavy external control plane that is “the real brain.”

## Core Principles (Ordered by Importance)

1. **Distributed OS first** — Each node is a full OS instance. The nodes form a network.
2. **Blockchain-style coordination** — Shared ledger + consensus for critical state and communication between OS instances.
3. **Image integrity & placement as core ledger concerns** — These are foundational, not optional.
4. **Userspace coordination** — The ledger and coordination logic live above the kernel.
5. **Narrow ledger** — Only the facts required for integrity, placement, and coordination between OS nodes.
6. **Simple native orchestration (secondary)** — Built on the distributed OS foundation; kept deliberately limited.
   - Phase 3 increment: declarative Workloads + least-loaded scheduler + reconciler (`docs/phase3-orchestration.md`).
7. **Permissioned first** — Reliable private/edge networks before open permissionless mode.
8. **UI + structured state** — Operators work with data and a UI; YAML is secondary.
9. **OCI compatible & incremental** — Standard containers; ship working slices.

## Main Components

### 1. Base OS (the actual operating system on each node)
- Minimal, preferably immutable Linux
- Optimized for running containers (namespaces, cgroups, seccomp, etc.)
- Atomic / transactional updates
- Target: x86_64 first

### 2. Container Runtime
- containerd + runc
- Content-addressed image store
- Digest verification before run
- CRIU integration path for migration

### 3. Coordination Service (Go, userspace)
Primary responsibilities:
- P2P communication between OS nodes
- Consensus and the shared ledger
- Image integrity registration and verification
- Placement state (“which container runs where”)
- Node identity and secure communication

Secondary responsibilities:
- Simple scheduler and reconciler
- Migration controller
- Local API for UI/CLI

### 4. Ledger (Shared State)
The ledger’s main jobs:
- Image integrity (digests + verification by nodes)
- Placement state (containers ↔ nodes)
- Supporting secure coordination and communication between OS instances
- Later: migration records and simple workload desired state

It is deliberately narrow.

### 5. Orchestration (Secondary Layer)
- Simple declarative Workloads (replicas, basic constraints, resources)
- Scheduling and reconciliation built on top of the distributed OS + ledger
- Designed to be driven from a UI / API

### 6. User Interface (planned)
- First-class web UI for day-to-day operations
- View nodes, capacity, placement, and image integrity
- Create and manage workloads
- Trigger migrations
- Talks directly to the coordination service API

## Modes

- **Permissioned**: Explicit membership, certificates/keys, faster finality. Preferred starting point.
- **Permissionless**: Open joining (later), with an appropriate security model.

Same binary, different configuration and membership rules.

## Current Phase 2 transport

Two layers:

**Consensus (image integrity + placement).** In-process **CometBFT** applies Ed25519-signed `ledger.Tx` values via ABCI (strict mempool Submit). Membership drives dynamic validators (`JoinMember`/`LeaveMember`). HTTP snapshot sync remains for heartbeats and catch-up. See `docs/cometbft-spike.md`. (Historical Phase 2 note: a permissioned hash-chain proposer existed first; it has been removed.)

1. Local start/stop/pull becomes a signed tx (`RegisterImage`, `CreateContainer`, …).
2. The leader for the next height proposes a block (`POST /v1/net/propose`); followers return a signed vote in the same RPC.
3. CometBFT FinalizeBlock applies txs to the ledger; peers learn via ABCI height and/or HTTP sync.
4. A follower that is not leader forwards the tx with `POST /v1/net/tx` and applies the returned commit.

Heartbeats are **not** consensus txs (too frequent). They stay on the HTTP snapshot path.

**HTTP snapshot sync (liveness + catch-up).** Signed ledger snapshots over HTTP:

1. Pair with a join token (`POST /v1/net/pair`) — both sides add each other as members and peers.
2. Each node periodically `POST /v1/net/sync` with an Ed25519-signed snapshot.
3. The receiver verifies the signature, checks membership, merges, and returns its post-merge snapshot (one round-trip is bidirectional).

Merge rules: last-write-wins for containers and nodes; union of image `verified_by`; tombstones for deletes. On equal node heartbeats, `Offline` wins so failure detection spreads. A later live heartbeat clears it.

Each node marks peers **Offline** when `last_heartbeat` is older than `--heartbeat-timeout` (default 30s). Receiving a signed sync from a node counts as a local observation and refreshes that node's heartbeat.

If consensus cannot gather a quorum (peer down), the coordinator falls back to a local ledger upsert so the node still tracks its own runtime; snapshot sync will spread that later.

## Non-Goals (for now)

- Writing a kernel from scratch
- Full Kubernetes API compatibility
- Putting high-frequency metrics or arbitrary data on the ledger
- Making orchestration the primary identity of the system
