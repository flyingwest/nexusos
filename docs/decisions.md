# Decision Log

## 2026-08-19 – Project Kickoff

**Decision**: Start the project as **NexusOS**.

**Rationale**: Clear, evocative name that suggests a network of nodes. Can be changed later if needed.

**Status**: Accepted

---

**Decision**: Coordination and orchestration service written in **Go**.

**Rationale**: Excellent concurrency model, strong standard library, good P2P and gRPC ecosystem, fast to develop reliable networked services. C/Rust reserved for any future low-level performance or isolation needs.

**Status**: Accepted

---

**Decision**: Target **x86_64** first.

**Rationale**: Broadest hardware availability for development and early users. ARM support can be added later.

**Status**: Accepted

---

**Decision**: Use a **minimal Linux base** rather than writing a kernel from scratch.

**Rationale**: Practical path to a product. Leverages mature container isolation (namespaces, cgroups, seccomp). Allows focus on the novel coordination and orchestration layer.

**Status**: Accepted

---

**Decision**: Start with **containerd + runc**.

**Rationale**: Industry standard, OCI compatible, good event and image APIs, existing CRIU integration paths.

**Status**: Accepted

---

**Decision**: **Permissioned mode first**, permissionless later.

**Rationale**: Dramatically reduces complexity and security surface for the initial versions. Allows shipping a useful system sooner.

**Status**: Accepted

---

**Decision**: Ledger stays **narrow** (placement, image integrity, workloads, migrations).

**Rationale**: Avoids performance and complexity problems. High-frequency data belongs in monitoring systems, not consensus.

**Status**: Accepted

---

**Decision**: Migration starts with **cold migration via CRIU**.

**Rationale**: Significantly simpler and more reliable than live migration. Live migration can be added after the control plane is solid.

**Status**: Accepted

---

## 2026-08-19 – Product Positioning

**Decision**: Explicitly position NexusOS as a **simple, distributed container platform for the Edge** that sits between Docker Compose and Kubernetes/Rancher.

**Rationale**: Many real-world (especially Edge) use cases are too small or operationally constrained for Kubernetes, yet need more than Compose. Emphasizing simplicity, UI-driven operations, and structured state (instead of YAML-first) gives the project a clear and differentiated goal.

**Status**: Accepted

---

**Decision**: Primary interface is **UI + API + structured state**. YAML is secondary (import/export only).

**Rationale**: Matches the target users’ desire for something easier to operate than Kubernetes-style YAML workflows.

**Status**: Accepted

---

## 2026-08-19 – Priority Correction

**Decision**: Reaffirm that NexusOS is first and foremost a **distributed operating system** whose nodes form a blockchain-style network. Image integrity, placement state, and secure communication between OS instances are core. Native orchestration is secondary.

**Rationale**: Earlier documentation over-emphasized the orchestration / “simple Kubernetes alternative” angle. The original and more important concept is the network of OS instances coordinating via a ledger focused on integrity and placement.

**Status**: Accepted

---

## 2026-08-19 – Phase 1 hardening + Phase 2 skeleton

**Decision**: Harden Phase 1 with unit tests, optional API bearer auth, optional digest-only starts, request logging.

**Decision**: Add Ed25519 node identity package and local ledger types/store as the Phase 2 skeleton (no network consensus yet).

**Decision**: Ship a simple `scripts/install.sh` + systemd unit for single-node deployment.

**Status**: Accepted

---

## 2026-08-21 – Peer ledger sync (HTTP, signed snapshots)

**Decision**: Start Phase 2 networking with **signed HTTP snapshot exchange**, not CometBFT yet.

**Rationale**: The immediate goal is two permissioned nodes agreeing on placement and image integrity. A full consensus engine is the right later step, but a working increment is:

- Ed25519-signed hello / pair / snapshot messages
- Last-write-wins merge for containers and nodes; union of image `verified_by`
- Tombstones so deletes propagate
- Join-token pairing for permissioned membership
- Periodic push-pull so one round-trip is bidirectional

CometBFT (or equivalent) can replace this transport later without changing the ledger schema.

**Status**: Accepted

---

## 2026-08-21 – Heartbeat timeout marks nodes Offline

**Decision**: Infer liveness locally. If a node’s `last_heartbeat` is older than `--heartbeat-timeout` (default 30s), mark it `Offline` on the ledger without changing the heartbeat timestamp. A signed message from that node is recorded with the receiver’s clock (`heardFrom`) so failure detection does not depend on the sender’s clock. On merge with equal heartbeat times, `Offline` wins; a newer heartbeat restores `Online`.

**Rationale**: Placement is only trustworthy if “Online” means recently heard. This is a prerequisite for later rescheduling. Consensus is not required for this inference.

**Status**: Accepted

---

## 2026-08-23 – Permissioned hash-chain consensus

**Decision**: Commit image-integrity and placement mutations through a permissioned, hash-chained log with `ceil(2n/3)` votes. Do **not** embed CometBFT yet. Keep HTTP snapshot sync for heartbeats and catch-up. Fall back to a local upsert if a quorum cannot be reached.

**Rationale**: Phase 2 needed an ordered, attributable write path for the facts the ledger exists to hold. A full CometBFT node is the right later swap (same tx types, same ledger schema) but is a large dependency. A small proposer/vote/commit engine over the existing signed HTTP transport is a working increment, testable with two coordinators, and matches “CometBFT-style” without pretending we have BFT finality.

Heartbeats stay off-chain so consensus is not flooded every few seconds.

**Status**: Accepted


---

## 2026-09-03 – Phase 2 harden + freeze (security bar)

**Decision**: Harden Phase 2 and **freeze** it. Do not implement CometBFT, OS image packaging, Phase 3 orchestration, CRIU, or UI in this milestone.

**Security bar**: Normal coordinator start requires **join-token + API token + TLS** (operator HTTP on the same listener as peer HTTP). This is **not** localhost-only auth and **not** full mTLS. Empty API token or empty join-token is rejected unless `--dev` / `--insecure-dev` is set. `--dev` may auto-generate a self-signed cert under the data directory when `--tls-cert`/`--tls-key` are omitted, and enables insecure peer TLS verification for that mode.

**Env fallbacks**: `NEXUS_API_TOKEN` and `NEXUS_JOIN_TOKEN` are accepted when the corresponding flags are empty (systemd `Environment=` lines are meaningful).

**Primary test path**: Mock two-node (`scripts/e2e-two-node-mock.sh` + in-process `httpapi` tests). containerd remains documented and optional; CI must not require it.

**Rationale**: Phase 2 already met the multi-node ledger/consensus exit criteria for the homegrown path. Freezing after tokens+TLS keeps a shippable, reviewable baseline before larger deferred work.

**Status**: Accepted


---

## 2026-09-04 – Debian Bookworm minimal node image (mmdebstrap)

**Decision**: After the Phase 2 freeze, add a **buildable path** for a QEMU-bootable **x86_64 Debian Bookworm** minimal node image that includes **containerd + runc** and the NexusOS coordinator under **systemd**. Use **mmdebstrap** (not mkosi or debos) as the sole image-construction tool for this milestone.

**Rationale**:
- Phase 2 froze userspace coordination (tokens + TLS + ledger/consensus). Packaging a real minimal Linux node is the next concrete increment toward “distributed OS,” without reopening CometBFT, Phase 3 orchestration, CRIU, or UI.
- mmdebstrap is the simplest viable Debian-native rootfs builder: one binary, clear package lists, works with root + loop devices on a normal Linux host. mkosi and debos remain fine alternatives later if we need unified UKI/secure-boot or richer recipes; we stick to one tool now.
- Security bar is unchanged: production images require API token + join-token + TLS; `--dev` is confined to an optional `--dev-smoke` image for local QEMU.
- Full image builds need privileged host resources; CI continues to run `go test ./...` only and documents manual image builds.

**Status**: Accepted

---

## 2026-09-04 – Node image first-boot tokens/TLS harden

**Decision**: Production (non-smoke) node images **fail closed** on coordinator start until `/etc/nexusos/coordinator.env` exists with non-placeholder `NEXUS_API_TOKEN` / `NEXUS_JOIN_TOKEN`, and `/etc/nexusos/tls.crt` + `tls.key` (key mode `0600`) are present. Operators provision via **`nexusos-provision`** (writes env + optional self-signed TLS); **`nexusos-firstboot.service`** prints serial/journal instructions when incomplete and never invents secrets. The coordinator unit uses `ConditionPathExists` for env+TLS plus `ExecStartPre` preflight; it is **not** enabled at build time on production images. `--dev-smoke` keeps auto-start with `--dev`.

**Rationale**: Build-time `systemctl enable nexusos-coordinator` left production guests restarting/failing without tokens or TLS. Separating provision from first-boot messaging keeps the security bar explicit and preserves the existing QEMU smoke path.

**Status**: Accepted

---

## 2026-09-04 – Two-node QEMU e2e for node images

**Decision**: Add `scripts/qemu-two-node-e2e.sh` to prove Phase 2 pair/sync/ledger across **two QEMU guests** running the Debian `--dev-smoke` node image. Keep `scripts/e2e-two-node-mock.sh` as the fast host/CI path. For user-mode networking, rewrite each guest’s `NEXUS_ADVERTISE` to `https://10.0.2.2:<hostfwd-port>` after boot (serial login on smoke images) so peers can dial through the host; do not invent production secrets—reuse smoke tokens.

**Rationale**: Single-guest `qemu-node-smoke.sh` only checks `/health`. Mock e2e never boots the node image. A two-guest path is the missing exit check that the packaged coordinator + containerd stack can form a permissioned pair and converge ledger state. TCG remains the default accelerator (same as single-node smoke).

**Status**: Accepted

---

## 2026-09-04 – CometBFT embed spike (feature-flagged)

**Decision**: Start a **feature-flagged CometBFT embed spike** behind `--consensus-engine=hashchain|cometbft` (default **`hashchain`**; shorthand `--cometbft`). Implement an **in-process ABCI application** that accepts existing `ledger.Tx` JSON for image integrity and placement and applies via `ledger.ApplyTx` / `Store.ApplyTxs`. Do **not** flip the default, do **not** remove `internal/consensus` hash-chain, and do **not** ship a full multi-node CometBFT e2e in this increment. Pin official module `github.com/cometbft/cometbft v0.38.26` (ABCI 2.0, Go 1.22+).

**Rationale**: Phase 2 freeze deferred full BFT; the hash-chain proved ordered attributable writes. The next honest increment is proving CometBFT can drive the **same** ledger schema without a cutover. In-process ABCI keeps types and tests local; a socket harness (`cmd/cometbft-abci-harness`) documents sidecar attach for a later PR. Security bar (API token + join-token + TLS outside `--dev`) is unchanged.

**Status**: Accepted (spike)

---

## 2026-09-04 – CometBFT in-process node + mempool Submit

**Decision**: Advance the CometBFT embed from ABCI-only spike to an **in-process CometBFT node** (`node.New` + `proxy.NewLocalClientCreator`) when `--consensus-engine=cometbft` / `--cometbft`. Persist under `<data-dir>/cometbft/`. Wire operator image/placement commits through **BroadcastTxCommit** with **no silent local-upsert fallback**. Expose `--cometbft-rpc` / `--cometbft-p2p` (defaults `127.0.0.1:26657` / `26656`). Ship **membership → suggested validator** scaffolding (`validators-from-membership.json`, `ValidatorUpdatesFromMembership`) but keep genesis as the local FilePV (single-node); defer dynamic EndBlock validator updates and multi-validator P2P. Default engine remains **hashchain**.

**Rationale**: The spike proved ledger.Tx over ABCI; operators still got silent local upserts with `--cometbft`. A single-node in-process path unblocks real mempool commits for `--dev/mock` without a sidecar, while honest docs cover the validator-sync gap. Full mesh + EndBlock is a larger follow-up than this PR.

**Status**: Accepted


---

## 2026-09-05 – CometBFT dynamic validators + two-node e2e

**Decision**: Drive CometBFT validator-set changes from NexusOS membership via **ABCI 2.0 `FinalizeBlock.ValidatorUpdates`** (v0.38 has no separate EndBlock). Seed new FilePV keys from the NexusOS Ed25519 identity so membership pubkey == voting pubkey. Preserve single-node safety: empty/unkeyed membership emits no updates; refuse sync that would drop the local signing key when it is absent from the desired set. Add `--cometbft-peers` and a two-node harness (shared genesis copy + Go test `TestTwoNodeConsensusAppliesTxOnPeer`). Default engine remains **hashchain**.

**Rationale**: Membership join/pair/leave is the permissioned source of truth; FinalizerBlock is the ABCI 2.0 place to propose validator diffs. Identity-seeded FilePV closes the keyring gap for new data dirs without breaking existing random FilePV single-node clusters. Multi-node still requires an explicit shared genesis (honest operational step) rather than inventing genesis gossip in this increment.

**Status**: Accepted


---

## 2026-09-05 – Membership as consensus tx (CometBFT)

**Decision**: Admit/leave peers via `ledger.Tx` types `JoinMember` / `LeaveMember` applied through ABCI (and hash-chain Submit when that engine is selected). Drive CometBFT `FinalizeBlock.ValidatorUpdates` from **ledger `State.Members`**, not from HTTP-local `members.json` alone. HTTP pair still checks the join-token, then submits `JoinMember`. Sync the local membership cache on commit (`BindMembership` / `ReplaceAll`). Do **not** merge `Members` over HTTP snapshot sync.

**Leave judgment**: Keep `DiffValidatorUpdates` safety (never empty set; expand-only for non-validator locals). `LeaveMember` is available but operators should prefer expand-only until a stronger eviction story exists.

**Rationale**: Pairing that mutated only local membership caused `NextValidatorsHash` divergence when nodes paired at different heights. Ordering membership with ledger state removes the pair-before-CometBFT-start requirement.

**Status**: Accepted


---

## 2026-09-05 – Flip default consensus engine to CometBFT

**Decision**: Default `--consensus-engine` to **`cometbft`**. Keep `--consensus-engine=hashchain` for a deprecation window and log a clear warning when it is selected. **Do not delete** hash-chain code in this PR. Explicit hash-chain remains the path for mock two-node HTTP e2e and smoke/QEMU two-guest images that lack shared CometBFT genesis/`--cometbft-peers`.

**Rationale**: Membership txs + dynamic validators + two-node e2e unblocked cutting over the default. Operators who need the old path can opt in explicitly during the window.

**Status**: Accepted


---

## 2026-09-05 – Safe CometBFT leave / eviction

**Decision**: Treat `LeaveMember` as a first-class, safe membership path:

1. **Validator power 0** via `FinalizeBlock.ValidatorUpdates` when ledger Members no longer include the peer.
2. **Member tombstone** `member:<id>` so AppHash reflects leave after the Members entry is removed.
3. **Refuse last usable validator** in `ledger.ApplyTx` / `CanLeaveMember` (HTTP `409`) so leave cannot brick the chain.
4. **Operator paths**: `DELETE /v1/members/{id}` (evict) and `POST /v1/cluster/leave` (self-leave), both API-token auth; join-token stays join/pair-only. CLI: `nexusctl members rm`, `nexusctl leave`.

**Rationale**: Expand-only membership left operators without a permissioned way to shrink the validator set. Last-validator refusal + tombstones close the safety gap without Phase 3 scope or removing hash-chain.

**Status**: Accepted

---

## 2026-09-05 – Shared CometBFT genesis bootstrap + shadow stub

**Decision**: Automate multi-node shared genesis via **seed HTTP bootstrap** rather than inventing CometBFT P2P genesis gossip:

1. `GET /v1/cometbft/bootstrap` returns genesis JSON + `peer` (`id@host:port`). Auth: operator **API bearer** **or** `X-Nexus-Join-Token` (join-token check).
2. Joining coordinators use `--cometbft-genesis-from <seed-url>` to install genesis before `StartNode`; empty `--cometbft-peers` inherits the seed peer hint.
3. `nexusctl cometbft bootstrap` / `fetch-genesis` for operators who prefer an explicit offline step.
4. Wire `scripts/e2e-cometbft-two-node.sh` to the genesis-from path (no manual `cp`).

**Shadow**: Accept `--consensus-shadow-hashchain` as a **stub** (log + docs checklist only). Do **not** dual-write in this PR; keep hash-chain code for the deprecation window (removal is a later step). Do **not** start Phase 3.

**Rationale**: Manual genesis copy was the remaining multi-node footgun after leave/eviction. Join-token-gated HTTP matches the existing permissioned pairing model and is the simplest reliable automation. Full dual-run is higher risk than this polish and belongs with the hash-chain deprecation follow-up.

**Status**: Accepted


---

## 2026-09-05 – Remove permissioned hash-chain consensus

**Decision**: Delete the deprecated hash-chain consensus engine (`coordination/internal/consensus` root: Engine/Chain/Propose/Vote/`chain.json`). **CometBFT is the only consensus path.** Reject `--consensus-engine=hashchain` and remove `--consensus-shadow-hashchain` (stub never dual-wrote). Remove hash-chain HTTP (`GET /v1/chain`, `POST /v1/net/propose|commit|tx`) and `nexusctl chain`. Migrate `scripts/e2e-two-node-mock.sh` to a thin wrapper around `e2e-cometbft-two-node.sh`. Smoke/QEMU overlays no longer pin hashchain.

**Rationale**: Deprecation window closed after CometBFT default + membership txs + leave/eviction + shared-genesis bootstrap. Keeping two engines raised operator footguns and test matrix cost. Historical decisions above remain for archaeology.

**Status**: Accepted


---

## 2026-09-05 – Phase 3 workloads: least-loaded + sticky reconcile

**Decision**: Ship Phase 3 as **simple declarative workloads** on CometBFT:

1. **Ledger txs**: `CreateWorkload` / `UpdateWorkload` / `ScaleWorkload` / `DeleteWorkload`; AppHash includes workloads; tombstone `workload:<id>`.
2. **Placement**: deterministic container ids `wl:<id>:<replica>`; scheduler = **least-loaded by ledger container count** among Online nodes, sticky when still Online.
3. **Reconciler**: every coordinator submits placement txs and starts/stops **local** runtime containers assigned to itself. On Start failure, keep desired on ledger and retry (no reschedule in this increment).
4. **Strategy**: `Recreate` only; rolling updates, affinities, and resource bin-packing deferred.
5. **Security bar unchanged**: API token + TLS; no UI / CRIU / permissionless.

**Rationale**: Orchestration stays secondary to the distributed OS. Working create+reconcile+scale beats perfect bin-packing. Deterministic scheduling avoids leader election.

**Status**: Accepted

---

## 2026-09-05 – Workload rolling updates

**Decision**: Support `RollingUpdate` (default) and `Recreate` on Workloads:

1. **RollingUpdate**: on image/key-spec change, each reconciler pass updates at most `max_unavailable` outdated replica placements (default **1**, lowest index first). Scale-up still creates all missing replicas in one pass. Local runtime restarts only when a placement’s ledger `image_digest` diverges from the running container.
2. **Recreate**: update all replica placement digests in one pass (nuke-and-restart).
3. **API/CLI**: `PUT /v1/workloads/{id}`; `nexusctl workloads update`; `--strategy` / `--max-unavailable` on create/update.
4. **Not in this PR**: maxSurge, readiness probes on the ledger, CRIU, UI, affinities.

**Rationale**: Prefer a simple correct one-at-a-time roll over Kubernetes parity. Deterministic lowest-index budgeting keeps multi-coordinator CometBFT submits idempotent without a roll leader.

**Status**: Accepted


---

## 2026-09-05 – Phase 4 cold migration (CRIU)

**Decision**: Ship **cold** migration as CometBFT ledger txs + coordinator controller:

1. **Ledger**: `ProposeMigration` / `CompleteMigration` / `FailMigration`; container `Desired=Migrating` during flight; AppHash already covers `migrations`.
2. **Controller**: propose → checkpoint source → transfer artifact (JSON/base64 peer RPC) → restore dest → complete; on failure submit `FailMigration` (placement stays on `from_node`).
3. **Runtime**: mock always supports checkpoint/restore; containerd path gated on `criu check` and returns `ErrCheckpointUnsupported` when absent.
4. **API/CLI**: `POST /v1/migrations`; `nexusctl migrations migrate --to`.
5. **Not in this PR**: live migration, UI, streaming artifact store, full CRIU image restore parity, separate AcceptMigration ack.

**Rationale**: Cold migration proves coordination + placement integrity without the complexity of live CRIU. Mock+API+ledger is shippable on every CI host; real CRIU remains a documented host hook.

**Status**: Accepted


---

## 2026-09-05 – Operator UI (embedded vanilla SPA)

**Decision**: Ship a practical **operator web UI** as **static assets embedded in the coordinator** (`GET /ui/`), talking only to existing HTTPS JSON APIs.

1. **Embed over separate server**: one binary serves API + UI; `ui/` at repo root is a symlink to `coordination/internal/api/httpapi/ui/`.
2. **Vanilla JS** (no React/Vue/npm build) for reviewability and zero toolchain weight.
3. **Security**: `/ui/*` is unauthenticated shell; bearer token stays in browser `localStorage`; never bake credentials into the image. TLS/token model unchanged; document browser vs `nexusctl --insecure` for `--dev` certs.
4. **Scope**: dashboard (nodes/members/peers/ledger), containers start/stop, workloads create/scale/update strategy + placement, Phase 4 **cold** migrate propose/list. No live migration UI; no new orchestration semantics.

**Rationale**: PROJECT_SUMMARY positions UI + structured state as the primary interface. Embedding keeps the operator path identical to the hardened API. Minimal stack matches "ship working increments" and easy review.

**Status**: Accepted
