# NexusOS Roadmap

## Guiding Principles
- Ship working increments
- Permissioned mode first
- Reliability over features
- Keep the ledger narrow

---

## Phase 0 – Foundation
**Goal**: Project structure, clear design, and development environment.

- [x] Repository structure
- [x] Architecture document
- [x] Initial roadmap
- [x] Ledger schema document
- [x] Decision log
- [x] Basic development environment notes
- [x] Coordination service Go module skeleton
- [x] Project Summary document

**Exit criteria**: Anyone can clone the repo and understand the plan. ✅ Met

---

## Phase 1 – Single-Node Container Host
**Goal**: A minimal system that reliably runs OCI containers with local state tracking and image integrity checks.

Deliverables:
- Minimal Linux base (or clear instructions to build one)
- containerd + runc working cleanly
- Local coordination service that:
  - Talks to containerd
  - Tracks running containers
  - Verifies image digests
  - Exposes a simple gRPC/CLI API
- Basic start / stop / list / inspect commands

**Exit criteria**: Can pull an image by digest, run a container, and query its state through the coordination service on a single node.

---

## Phase 2 – Multi-Node Permissioned Network (frozen)
**Goal**: Multiple nodes share a consistent view of container placement and image integrity.

Deliverables:
- [x] Signed node identity on the wire (Ed25519 hello / snapshot)
- [x] Permissioned pairing (`--join-token`, `nexusctl pair`)
- [x] HTTP push-pull of ledger snapshots between peers
- [x] Merge of placement (LWW) + image integrity (`verified_by` union) + delete tombstones
- [x] Heartbeats + CLI (`nexusctl peers`, `pair`, `sync`, `ledger`)
- [x] Failure detection / node Offline from missed heartbeats
- [x] Permissioned hash-chain consensus for image integrity + placement (`ceil(2n/3)`, `--consensus`)
- [x] Required join-token + API token + TLS (with `--dev` escape hatch)
- [x] Mock two-node e2e script
- [x] Embed CometBFT as default consensus engine (in-process node + mempool Submit + membership txs + dynamic validators + two-node e2e) — hash-chain deprecated but not deleted (`docs/cometbft-spike.md`)
- [x] Safe leave/eviction for CometBFT membership (LeaveMember → power 0, member tombstone, refuse last validator, HTTP/CLI)
- [x] Shared-genesis bootstrap for multi-node CometBFT (`--cometbft-genesis-from`, bootstrap HTTP, nexusctl)
- [ ] Remove hash-chain engine after deprecation window

**Exit criteria**: Two or more nodes agree on which containers are running where and which images have been verified. ✅ Met for HTTP sync (eventual) and for consensus commits when a quorum is present. Security bar (tokens+TLS) ✅. Full BFT finality remains deferred — see `docs/phase2-freeze.md`.

**CometBFT (2026-09-05)**: **Default** engine under `coordination/internal/consensus/cometbft`. Mempool Submit (strict), membership as consensus txs (`JoinMember`/`LeaveMember`), dynamic validators, **safe leave/eviction**, **shared-genesis bootstrap** (`--cometbft-genesis-from` / `GET /v1/cometbft/bootstrap`), identity-seeded FilePV, `--cometbft-peers`, join/leave e2e. Hash-chain via `--consensus-engine=hashchain` (deprecated warning); `--consensus-shadow-hashchain` stub. Design: `docs/cometbft-spike.md`.

---

## Post–Phase 2 – Minimal Linux Node Image
**Goal**: Ship a buildable Debian Bookworm x86_64 node disk image (QEMU-bootable) with containerd + runc + coordinator (systemd), without reopening frozen Phase 2 scope.

Deliverables:
- [x] `image/` tree: mmdebstrap-based build → raw + qcow2
- [x] Install coordinator + systemd unit from `deploy/` / image overlay; enable containerd
- [x] Document tokens + TLS; `--dev` only via `--dev-smoke` for local QEMU
- [x] `scripts/qemu-node-smoke.sh` + `docs/node-image.md`
- [x] Decision log entry (Debian Bookworm + mmdebstrap)
- [x] `scripts/qemu-two-node-e2e.sh` — two-guest pair/sync/ledger on node images
- [ ] Full image build + KVM smoke on a privileged Linux builder (manual; not GHA)

**Exit criteria**: A documented, scripted path produces a bootable node image; coordinator security bar holds outside `--dev`. Two-guest QEMU e2e scripted for pair/sync/ledger on smoke images. Full build/smoke may require a host with root and KVM.

---

## Phase 3 – Native Orchestration
**Goal**: Declarative Workloads with scheduling and reconciliation.

Deliverables:
- Workload object (replicas, resources, constraints)
- Simple scheduler
- Reconciler loop (desired vs actual)
- Scaling and basic rolling updates
- Integration with the ledger

**Exit criteria**: User can declare a Workload with N replicas and the system places and maintains them across the permissioned cluster.

---

## Phase 4 – Migration
**Goal**: Coordinated container migration between nodes.

Deliverables:
- Cold migration using CRIU
- Migration as a first-class ledger transaction
- Orchestrator-triggered and manual migration
- Failure handling and rollback basics

**Exit criteria**: A running container can be moved from node A to node B under coordination control, with ledger state updated correctly.

---

## Phase 5 – Hardening & Permissionless Path
**Goal**: Production readiness and opening toward permissionless mode.

- Improved security model
- Observability, upgrades, packaging
- Basic permissionless membership (experimental)
- Documentation and operator guides

---

## Longer-Term Ideas (Not committed)
- Live migration
- Stronger isolation (microVMs)
- Advanced scheduling (bin packing, affinity, topology)
- Distributed image store
- Higher-level application abstractions
