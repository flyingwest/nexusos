# CometBFT embed (feature-flagged)

**Status**: in-process node + mempool Submit / not default  
**Date**: 2026-09-04 (updated)  
**Module**: `github.com/cometbft/cometbft v0.38.26` (ABCI 2.0 line; Go 1.22+). `coordination/go.mod` uses `go 1.22.11` (+ toolchain).

This document describes the feature-flagged path that runs **CometBFT as the consensus engine** for **image-integrity** and **placement** transactions, while keeping the existing permissioned hash-chain as the **default**.

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
│  cometbft.App  (ABCI)                    │
│  cometbft.Node (in-process consensus)    │── local BroadcastTxCommit
│  httpapi → TxSubmitter (strict)          │
│  consensus.Engine (hash-chain; default)  │
└──────────────────────────────────────────┘
```

## Tx mapping (ledger → ABCI)

| NexusOS `ledger.Tx.Type` | ABCI path | Notes |
|--------------------------|-----------|--------|
| `RegisterImage` / `VerifyImage` | CheckTx → FinalizeBlock → Commit | `ImagePayload` JSON in `ledger.Tx` |
| `CreateContainer` / `UpdateContainer` | same | placement |
| `RemoveContainer` | same | tombstone via `ApplyTx` |
| Heartbeats / node upserts | **not** on ABCI | stay on HTTP snapshot sync |

Wire encoding: **JSON of `ledger.Tx`** (`EncodeTx` / `DecodeTx`). Signatures remain Ed25519 over the existing `nexusos-tx-v1` domain string.

`CheckTx`: decode → `ledger.VerifyTx` → membership check → dry-run `ApplyTx` on a snapshot.  
`FinalizeBlock`: same validation; stage successful txs; compute `AppHash` = SHA-256 of marshaled working state.  
`Commit`: `Store.ApplyTxs(pending)` + persist ABCI height/app-hash meta under `data-dir/cometbft/abci-meta.json` so handshake does not replay onto an already-applied ledger.

## Operator Submit path (strict)

When `--consensus-engine=cometbft` (or `--cometbft`):

1. Coordinator starts an in-process CometBFT node.
2. `httpapi` wires `TxSubmitter` to `cometbft.Node.Submit`.
3. Image/placement commits call `BroadcastTxCommit` and **do not** fall back to local `UpsertImage` / `UpsertContainer` / `RemoveContainer` on failure.

Hash-chain mode is unchanged: still uses `consensus.Engine.Submit` with local upsert fallback if quorum fails.

## Validators / peers vs NexusOS membership

| Concept | NexusOS today | CometBFT (this PR) |
|---------|---------------|---------------------|
| Who may sign ledger txs | `membership.Store` (+ join-token pairing) | Same: ABCI rejects non-members when the set is non-empty |
| Who votes / proposes | Hash-chain: member IDs as validators, `ceil(2n/3)` | **Genesis** = local CometBFT FilePV (single-node). Membership → suggested validator snapshot written to `data-dir/cometbft/validators-from-membership.json` |
| Dynamic validator set | N/A | **Deferred**: `ValidatorUpdatesFromMembership` builds ABCI updates for a future EndBlock path; pairing does **not** yet resize the CometBFT set |
| Peer discovery | `--peers`, pair HTTP, advertise URL | CometBFT P2P ports are separate; multi-validator P2P mesh is **deferred** |
| Operator API auth | API token + TLS | Unchanged |

**Join-token** does not become a CometBFT secret. It continues to authorize **membership** changes.

**Key mapping limit**: NexusOS Ed25519 node identity keys and CometBFT consensus FilePV keys are **separate keyrings**. Suggested validators map member `public_key` hex → CometBFT Ed25519 pubkey when present; they are not automatically installed as voting power.

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

ABCI socket harness (sidecar experiments):

```bash
go run ./cmd/cometbft-abci-harness --data-dir /tmp/nexus-abci \
  --addr tcp://127.0.0.1:26658
```

## Migration plan (hash-chain → CometBFT)

1. ~~**Spike**: ABCI app + flag + docs~~ ✅
2. **This PR**: In-process node + mempool Submit (strict) + membership→validator scaffolding; single-node smoke test.
3. **Dynamic validators**: Apply `ValidatorUpdatesFromMembership` in EndBlock / InitChain; correlate identity ↔ consensus keys.
4. **Multi-validator P2P**: Seed/persistent peers from membership advertise URLs; two-node QEMU e2e under `--cometbft`.
5. **Dual-run / shadow** (optional): Compare app hashes vs hash-chain heights in a lab cluster.
6. **Cutover**: Flip default `--consensus-engine` only after multi-node e2e and security review. **Do not remove** `internal/consensus` until cutover is proven.
7. **Freeze hash-chain** as fallback / `--consensus-engine=hashchain` for one release.

## Known risks / deferred

- **Single-node only for voting**: Genesis validator is the local FilePV; membership suggestions are advisory until EndBlock updates ship.
- **Multi-validator P2P**: Not wired; `--peers` still means NexusOS HTTP sync peers, not CometBFT P2P.
- **Dependency weight**: Full `node` import pulls DB/P2P/RPC stacks; scoped behind the engine flag at runtime.
- **AppHash**: State digest (SHA-256 of marshaled ledger), not a Merkle tree — fine for permissioned ops, not for light clients.
- **Version pin**: v0.38.x matches Go 1.22+; v1.x wants newer Go — revisit when the module bumps past 1.22.

## Success criteria

- [x] In-process CometBFT node under `data-dir/cometbft/` when engine is `cometbft`
- [x] Operator Submit → mempool (`BroadcastTxCommit`); no silent local-upsert fallback
- [x] RPC/P2P flags with safe defaults (no clash with `:8080`); clean shutdown
- [x] Membership → validator scaffolding + honest docs on EndBlock limits
- [x] Unit + single-node integration test (`TestNodeSubmitRegisterImageViaMempool`)
- [x] Default engine remains `hashchain`
- [ ] Full multi-validator P2P + dynamic EndBlock validator sync — **deferred**
