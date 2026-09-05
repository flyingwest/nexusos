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
