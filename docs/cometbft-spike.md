# CometBFT embed (sole consensus engine)

**Status**: **sole** consensus engine — in-process node + mempool Submit + membership txs + dynamic validators + safe leave/eviction + shared-genesis bootstrap + two-node e2e  
**Date**: 2026-09-05 (updated; hash-chain removed)  
**Module**: `github.com/cometbft/cometbft v0.38.26` (ABCI 2.0 line; Go 1.22+). `coordination/go.mod` uses `go 1.22.11` (+ toolchain).

This document describes **CometBFT as the only consensus engine** for **image-integrity**, **placement**, and **membership** transactions. The former permissioned hash-chain engine has been **removed** (see `docs/decisions.md`).

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

When consensus is enabled (default; engine is always `cometbft`):

1. Coordinator starts an in-process CometBFT node.
2. `httpapi` wires `TxSubmitter` to `cometbft.Node.Submit`.
3. Image/placement commits call `BroadcastTxCommit` and **do not** fall back to local `UpsertImage` / `UpsertContainer` / `RemoveContainer` on failure.

`--consensus-engine=hashchain` is rejected. Local upsert remains only when `--consensus=false` (tests / explicit opt-out).

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
| Who votes / proposes | (hash-chain removed) | Genesis = local FilePV (identity-seeded when new). Dynamic set via FinalizeBlock from membership |
| Peer discovery | `--peers`, pair HTTP, advertise URL | CometBFT: `--cometbft-peers` (`id@host:port`,…). Multi-node needs **shared genesis** via `--cometbft-genesis-from <seed-url>` or `nexusctl cometbft fetch-genesis` (manual copy still works) |
| Operator API auth | API token + TLS | Unchanged |

**Join-token** does not become a CometBFT secret. It continues to authorize **membership** changes.


## Shared genesis bootstrap (multi-node)

Joining nodes must share the seed's `genesis.json` (CometBFT chain ID + initial validators). Operators no longer need to copy files by hand:

| Path | Behavior |
|------|----------|
| `GET /v1/cometbft/bootstrap` | Returns `{genesis, peer, p2p_node_id, p2p_listen, chain_id}`. Auth: **API bearer** **or** `X-Nexus-Join-Token` matching the cluster join-token. |
| `--cometbft-genesis-from <seed-url>` | Before starting CometBFT, fetch bootstrap and install `<data-dir>/cometbft/config/genesis.json` if missing. If `--cometbft-peers` is empty, use the seed `peer` hint. |
| `nexusctl cometbft bootstrap` | Print bootstrap JSON from a running seed. |
| `nexusctl cometbft fetch-genesis --data-dir DIR` | Write genesis into `DIR` offline; prints suggested `--cometbft-peers`. |

Lab/e2e (`scripts/e2e-cometbft-two-node.sh`) starts B with `--cometbft-genesis-from` (no `cp` of genesis).

## Dual-run / shadow (retired)

`--consensus-shadow-hashchain` and the hash-chain engine are **gone**. CometBFT is the only commit path; dual-run was never shipped beyond a stub log.

## How to run

CometBFT is the default (single-node in-process):

```bash
./bin/coordinator --dev --mock --listen :8080 \
  --api-token secret --join-token cluster
# Cons. engine: cometbft
# optional: --cometbft / --consensus-engine=cometbft
# optional: --cometbft-rpc tcp://127.0.0.1:26657 --cometbft-p2p tcp://127.0.0.1:26656
```

Two-node CometBFT (mock e2e harness — uses `--cometbft-genesis-from`):

```bash
./scripts/e2e-cometbft-two-node.sh
# Manual join sketch (seed already running on :8080):
./bin/coordinator --dev --mock --cometbft \
  --listen :8081 --data-dir /tmp/nexus-b \
  --cometbft-rpc tcp://127.0.0.1:28657 --cometbft-p2p tcp://127.0.0.1:28656 \
  --cometbft-genesis-from https://127.0.0.1:8080
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
6. ~~**Cutover**: default `--consensus-engine=cometbft`~~ ✅
7. ~~**Deprecation window**~~ ✅ (completed)
8. ~~**Shared-genesis bootstrap** (HTTP + `--cometbft-genesis-from`)~~ ✅
9. ~~**Remove hash-chain** (engine code, flags, propose/commit HTTP, mock e2e pin)~~ ✅

## Known risks / deferred

- **Pre-existing FilePV ≠ identity**: dynamic sync skipped until re-seed; documented above.
- **Shared genesis required** for multi-node: use `--cometbft-genesis-from` / bootstrap HTTP (or manual copy). Not full P2P genesis gossip — seed HTTP after join-token/API auth.
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
- [x] Default / sole engine is `cometbft`; hash-chain removed (`--consensus-engine=hashchain` errors)
- [x] Shared-genesis bootstrap (`GET /v1/cometbft/bootstrap`, `--cometbft-genesis-from`, nexusctl helpers); e2e uses genesis-from
- [x] Shadow dual-run stub retired with hash-chain removal
