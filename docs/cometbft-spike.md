# CometBFT embed (feature-flagged)

**Status**: **default** consensus engine — in-process node + mempool Submit + membership txs + dynamic validators + safe leave/eviction + two-node e2e  
**Date**: 2026-09-05 (updated)  
**Module**: `github.com/cometbft/cometbft v0.38.26` (ABCI 2.0 line; Go 1.22+). `coordination/go.mod` uses `go 1.22.11` (+ toolchain).

This document describes the path that runs **CometBFT as the default consensus engine** for **image-integrity**, **placement**, and **membership** transactions. The permissioned hash-chain remains available via `--consensus-engine=hashchain` for a **deprecation window** (do not delete that code yet).

## Architecture: in-process CometBFT node + ABCI application

**Choice**: Embed both the ABCI **Application** and a full CometBFT **consensus node** inside the coordinator process (`coordination/internal/consensus/cometbft`). Config and data live under `<data-dir>/cometbft/`. Tx submission uses the **local RPC client** (`BroadcastTxCommit`) so operator mutations go through the mempool — no silent local ledger upsert when `--cometbft` is on.

**Justification**:

1. **Same ledger types**: The ABCI app decodes existing `ledger.Tx` JSON and applies via `ledger.ApplyTx` / `Store.ApplyTxs`. No parallel schema.
2. **Single binary for --dev/mock**: Single-node CometBFT works without a sidecar for local testing.
3. **Ports isolated**: CometBFT RPC/P2P default to `127.0.0.1:26657` / `127.0.0.1:26656` (flags override); operator HTTP stays on `:8080`.
4. **Sidecar still available**: `cmd/cometbft-abci-harness` exposes the same Application over an ABCI socket for experiments.

```
┌──────────────────────────────────────────┐
│ coordinator (Go)                         │
│  ledger.Store  membership.Store          │
│  cometbft.App  (ABCI + FinalizeBlock     │
│                 ValidatorUpdates)        │
│  cometbft.Node (in-process consensus)    │── local BroadcastTxCommit
│  httpapi → TxSubmitter (strict)          │
│  consensus.Engine (hash-chain; deprecated)│
└──────────────────────────────────────────┘
```

## Tx mapping (ledger → ABCI)

| NexusOS `ledger.Tx.Type` | ABCI path | Notes |
|--------------------------|-----------|--------|
| `RegisterImage` / `VerifyImage` | CheckTx → FinalizeBlock → Commit | `ImagePayload` JSON in `ledger.Tx` |
| `CreateContainer` / `UpdateContainer` | same | placement |
| `RemoveContainer` | same | tombstone via `ApplyTx` |
| `JoinMember` / `LeaveMember` | same | consensus-ordered membership; drives ValidatorUpdates |
| Heartbeats / node upserts | **not** on ABCI | stay on HTTP snapshot sync |

Wire encoding: **JSON of `ledger.Tx`** (`EncodeTx` / `DecodeTx`). Signatures remain Ed25519 over the existing `nexusos-tx-v1` domain string.

`CheckTx`: decode → `ledger.VerifyTx` → authorize signer → dry-run `ApplyTx` on a snapshot.  
`JoinMember` authorization: existing member, open/empty membership, **or** active CometBFT validator (so genesis FilePV can admit a peer before `members.json` syncs).  
`FinalizeBlock`: same validation; stage successful txs; compute `AppHash` = SHA-256 of consensus fields (images/containers/migrations/**members**/tombstones — not nodes); **emit `ValidatorUpdates`** from **ledger `Members`** (not local HTTP `members.json` alone).  
`Commit`: `Store.ApplyTxs(pending)` (syncs local membership cache via `BindMembership`) + persist ABCI meta under `data-dir/cometbft/abci-meta.json`.

## Operator Submit path (strict)

When `--consensus-engine=cometbft` (or `--cometbft`):

1. Coordinator starts an in-process CometBFT node.
2. `httpapi` wires `TxSubmitter` to `cometbft.Node.Submit`.
3. Image/placement commits call `BroadcastTxCommit` and **do not** fall back to local `UpsertImage` / `UpsertContainer` / `RemoveContainer` on failure.

Hash-chain mode is unchanged: still uses `consensus.Engine.Submit` with local upsert fallback if quorum fails.

## Dynamic validators (membership → FinalizeBlock)

CometBFT v0.38 has **no separate EndBlock RPC**; apps return `ResponseFinalizeBlock.ValidatorUpdates` (applied at height+2).

| Event | Behavior |
|-------|----------|
| InitChain | Track genesis validators in app `valSet` |
| `JoinMember` tx (after HTTP join-token pair) | FinalizeBlock diffs ledger Members → add/update power |
| `LeaveMember` tx | Diff → power `0` removal; ApplyTx refuses last usable validator; stamps `member:<id>` tombstone |
| Empty ledger Members / no usable keys | **No updates** — preserve genesis FilePV (single-node safe) |
| Non-validator peer (local FilePV not yet in active set) | Only **pure expansions** allowed (desired ⊇ current). |
| HTTP `members.json` alone | **Does not** drive ValidatorUpdates (prevents NextValidatorsHash divergence) |

### Power / pubkey mapping

- **Power**: every usable member gets `DefaultValidatorPower` (10) — equal weight, permissioned.
- **PubKey**: member `public_key` hex must be exactly 32 bytes Ed25519. NexusOS `NodeID` is hex(pubkey), so they normally match.
- **Keyring correlation**: on first start, if no FilePV exists, consensus FilePV is **seeded from the NexusOS identity private key** so local membership pubkey == voting pubkey. Pre-existing random FilePV keys that do **not** match identity: membership sync is **skipped** while the local FilePV pubkey is absent from the desired set (avoids replacing the sole signing key and stalling). Operators can delete `<data-dir>/cometbft/` once to re-seed from identity (destroys that node's CometBFT chain state).

Tracked `valSet` is persisted in `abci-meta.json` so restarts do not re-flood updates.

### Determinism note (multi-node)

`FinalizeBlock` must compute identical `ValidatorUpdates` on every peer at a given height. **Membership join/leave now flow through consensus txs** (`JoinMember` / `LeaveMember`) so ledger `Members` (and thus validator diffs) are ordered with the chain. HTTP pair still verifies the join-token, then submits `JoinMember` via mempool. Local `members.json` is a cache synced on commit.

### Safe leave / eviction

| Path | Auth | Behavior |
|------|------|----------|
| `DELETE /v1/members/{id}` / `nexusctl members rm` | **API token** (Bearer). Tx signed by this node's identity (must be member or active validator) | Evict peer: `LeaveMember` → power `0` + `member:<id>` tombstone |
| `POST /v1/cluster/leave` / `nexusctl leave` | **API token** | Self-leave for local `node_id` |
| Join-token | **Not used for leave** | Join-token remains pair/join only |

**Last validator**: `ledger.ApplyTx` / `CanLeaveMember` refuse leaving when `MembershipPower` would become empty (`409 Conflict` on HTTP). Clear error: `cannot leave the last validator`.

**AppHash**: leave removes the member from `Members` and stamps `Tombstones["member:<id>"]`, so consensus digest changes even though the entry is gone.

`DiffValidatorUpdates` still: empty desired → preserve genesis; non-validator locals expand-only; never emit a zero-alive set. **Leaving node**: when local pubkey is absent from desired, still emit power-0 self-removal if every other current validator remains (keeps FinalizeBlock deterministic across peers).

Lab/e2e: start CometBFT (shared genesis + peers), **then** pair — pair-before-start is no longer required. Leave e2e: `TestTwoNodeLeaveMember` + harness leave step in `scripts/e2e-cometbft-two-node.sh`.

## Validators / peers vs NexusOS membership

| Concept | NexusOS today | CometBFT (this PR) |
|---------|---------------|---------------------|
| Who may sign ledger txs | `membership.Store` (+ join-token pairing) | Same: ABCI rejects non-members when the set is non-empty |
| Who votes / proposes | Hash-chain: member IDs as validators, `ceil(2n/3)` | Genesis = local FilePV (identity-seeded when new). Dynamic set via FinalizeBlock from membership |
| Peer discovery | `--peers`, pair HTTP, advertise URL | CometBFT: `--cometbft-peers` (`id@host:port`,…). Multi-node needs **shared genesis** (copy A's `config/genesis.json` to B) |
| Operator API auth | API token + TLS | Unchanged |

**Join-token** does not become a CometBFT secret. It continues to authorize **membership** changes.

## How to enable

Default remains hash-chain:

```bash
./bin/coordinator --dev --mock --listen :8080 \
  --api-token secret --join-token cluster
# Cons. engine: hashchain
```

CometBFT (single-node in-process):

```bash
./bin/coordinator --dev --mock --listen :8080 \
  --api-token secret --join-token cluster \
  --consensus-engine=cometbft
# or: --cometbft
# optional: --cometbft-rpc tcp://127.0.0.1:26657 --cometbft-p2p tcp://127.0.0.1:26656
```

Two-node CometBFT (mock e2e harness):

```bash
./scripts/e2e-cometbft-two-node.sh
# Go consensus proof (no HTTP coordinator):
cd coordination && go test ./internal/consensus/cometbft/ \
  -run TestTwoNodeConsensusAppliesTxOnPeer -count=1 -timeout 3m
```

ABCI socket harness (sidecar experiments):

```bash
go run ./cmd/cometbft-abci-harness --data-dir /tmp/nexus-abci \
  --addr tcp://127.0.0.1:26658
```

## Migration plan (hash-chain → CometBFT)

1. ~~**Spike**: ABCI app + flag + docs~~ ✅
2. ~~**In-process node + mempool Submit**~~ ✅
3. ~~**Dynamic validators** via FinalizeBlock from membership~~ ✅
4. ~~**Multi-node harness** (shared genesis + `--cometbft-peers` + Go e2e)~~ ✅
5. ~~**Membership as consensus txs** (`JoinMember` / `LeaveMember`)~~ ✅
6. ~~**Cutover**: default `--consensus-engine=cometbft`~~ ✅ (this release)
7. **Deprecation window**: `--consensus-engine=hashchain` still works; logs a clear warning. **Do not delete** `internal/consensus` hash-chain code yet.
8. **Later**: remove hash-chain engine after the deprecation window.

## Known risks / deferred

- **Pre-existing FilePV ≠ identity**: dynamic sync skipped until re-seed; documented above.
- **Shared genesis required** for multi-node: B must copy A's `genesis.json` before start (harness does this). No automatic genesis gossip yet.
- **HTTP `--peers` ≠ CometBFT P2P**: still separate planes; heartbeats remain on HTTP sync.
- **AppHash**: SHA-256 of consensus fields only (`images` / `containers` / `migrations` / `members` / `tombstones`). Member leave stamps `member:<id>` tombstones. **Nodes/heartbeats are excluded** so HTTP sync cannot diverge CometBFT peers. Not a Merkle tree — fine for permissioned ops, not for light clients.
- **Dependency weight**: Full `node` import pulls DB/P2P/RPC stacks; scoped behind the engine flag at runtime.
- **Version pin**: v0.38.x matches Go 1.22+; v1.x wants newer Go — revisit when the module bumps past 1.22.

## Success criteria

- [x] In-process CometBFT node under `data-dir/cometbft/` when engine is `cometbft`
- [x] Operator Submit → mempool (`BroadcastTxCommit`); no silent local-upsert fallback
- [x] RPC/P2P flags with safe defaults (no clash with `:8080`); clean shutdown
- [x] Dynamic FinalizeBlock validator updates from membership (+ unit tests)
- [x] Identity-seeded FilePV for key correlation; safe single-node when membership empty
- [x] Two-node Go e2e: tx on A applied on B via consensus (`TestTwoNodeConsensusAppliesTxOnPeer`)
- [x] Membership as consensus tx + validator updates (`TestFinalizeBlockValidatorUpdatesFromMembershipTx`, `TestTwoNodeJoinMemberAfterStart`)
- [x] Safe leave/eviction (`LeaveMember` last-validator refuse + member tombstone + `TestTwoNodeLeaveMember`)
- [x] Scripted harness `scripts/e2e-cometbft-two-node.sh` (pair-after-start) + run docs
- [x] Default engine is `cometbft`; hash-chain selectable + deprecation warning
