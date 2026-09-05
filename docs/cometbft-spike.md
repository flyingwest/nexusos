# CometBFT embed spike (feature-flagged)

**Status**: spike / not default  
**Date**: 2026-09-04  
**Module**: `github.com/cometbft/cometbft v0.38.26` (ABCI 2.0 line; Go 1.22+). `coordination/go.mod` bumped to `go 1.22.11` (+ toolchain) to satisfy the module.

This document describes the shippable increment that introduces a **feature-flagged** path to run CometBFT as the consensus engine for **image-integrity** and **placement** transactions, while keeping the existing permissioned hash-chain as the **default**.

## Architecture choice: in-process ABCI application

**Choice**: Embed a CometBFT **ABCI application** inside the coordinator process (package `coordination/internal/consensus/cometbft`). Do **not** yet start a full CometBFT consensus node inside `cmd/coordinator`.

**Justification**:

1. **Same ledger types**: The ABCI app decodes existing `ledger.Tx` JSON and applies via `ledger.ApplyTx` / `Store.ApplyTxs`. No parallel schema.
2. **Testable without a network**: Unit tests exercise `CheckTx` / `FinalizeBlock` / `Commit` without multi-node CometBFT.
3. **Clear next step**: Either (a) run CometBFT as a **sidecar** pointed at `cmd/cometbft-abci-harness` (ABCI socket), or (b) later call `node.New` in-process. Both consume the same `abcitypes.Application`.
4. **Ops isolation later**: Sidecar is preferred for the first multi-node attach so CometBFT P2P/ports stay separable from the operator HTTP listener; the Application stays in-process with the ledger.

**Rejected for this spike**: Full in-process `node.New` cutover — heavy dependency surface, genesis/validator key plumbing, and e2e cost beyond a spike. Sidecar-*only* Application (second binary owning ledger) — would duplicate membership/TLS/data-dir wiring.

```
┌─────────────────────────────┐         ABCI socket (later)
│ coordinator (Go)            │◄──── CometBFT process (sidecar)
│  ledger.Store               │         or node.New (later)
│  membership.Store           │
│  cometbft.App  (ABCI)       │
│  consensus.Engine (default) │  ← hash-chain still default
└─────────────────────────────┘
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
`Commit`: `Store.ApplyTxs(pending)` so crash-before-commit does not persist.

## Validators / peers vs NexusOS membership

| Concept | NexusOS today | CometBFT spike |
|---------|---------------|----------------|
| Who may sign ledger txs | `membership.Store` (+ join-token pairing) | Same: ABCI rejects non-members when the set is non-empty |
| Who votes / proposes | Hash-chain: member IDs as validators, `ceil(2n/3)` | Deferred: CometBFT genesis validators should be **derived from** membership (same node IDs / keys mapping TBD) |
| Peer discovery | `--peers`, pair HTTP, advertise URL | CometBFT P2P is separate; join-token still gates NexusOS pairing / membership writes |
| Operator API auth | API token + TLS | Unchanged |

**Join-token** does not become a CometBFT secret. It continues to authorize **membership** changes; validator set updates for CometBFT are a later migration step (EndBlock/InitChain validator updates from membership).

## How to enable the flag

Default remains hash-chain:

```bash
./bin/coordinator --dev --mock --listen :8080 \
  --api-token secret --join-token cluster
# Cons. engine: hashchain
```

Spike path (constructs ABCI app; does **not** start CometBFT consensus):

```bash
./bin/coordinator --dev --mock --listen :8080 \
  --api-token secret --join-token cluster \
  --consensus-engine=cometbft
# or: --cometbft
```

When `cometbft` is selected, the hash-chain `consensus.Engine` is **not** started. Operator image/placement writes fall back to local ledger upserts (same as `--consensus=false`) until a CometBFT mempool/Submit path is wired.

ABCI socket harness (for a later CometBFT sidecar):

```bash
go run ./cmd/cometbft-abci-harness --data-dir /tmp/nexus-abci \
  --addr tcp://127.0.0.1:26658
```

## Migration plan (hash-chain → CometBFT)

1. **Spike (this PR)**: ABCI app + flag + docs; default hash-chain; tests without multi-node CometBFT.
2. **Sidecar attach**: Run CometBFT with `proxy_app` → harness; single-node finalize of image/placement txs; document genesis from one member.
3. **Validator sync**: Map `membership` ↔ CometBFT validator set; join-token pairing triggers validator updates.
4. **Submit path**: Coordinator `tryConsensus` → CometBFT broadcast Tx instead of hash-chain `Submit`.
5. **Dual-run / shadow** (optional): Compare app hashes vs hash-chain heights in a lab cluster.
6. **Cutover**: Flip default `--consensus-engine` only after two-node QEMU e2e and security review. **Do not remove** `internal/consensus` until cutover is proven.
7. **Freeze hash-chain** as fallback / `--consensus-engine=hashchain` for one release.

## Known risks

- **Dependency weight**: CometBFT pulls gogoproto, grpc, crypto stacks; keep imports scoped to `abci/types` (+ harness `abci/server`).
- **AppHash vs ledger persistence**: AppHash is a state digest, not a Merkle tree; fine for spike, not for light clients.
- **Dual write confusion**: Enabling `--cometbft` without a node means no quorum commits — operators must not assume BFT finality.
- **Key mapping**: NexusOS Ed25519 node keys vs CometBFT validator keys need an explicit mapping before multi-node.
- **Version pin**: v0.38.x matches Go 1.22+; v1.x wants newer Go — revisit when the module bumps past 1.22.

## Success criteria (this PR)

- [x] `docs/cometbft-spike.md` + decision log + roadmap note
- [x] ABCI app under `internal/consensus/cometbft`
- [x] `--consensus-engine` / `--cometbft` (default `hashchain`)
- [x] Unit tests for codec + CheckTx/FinalizeBlock/Commit (image + placement)
- [ ] Full CometBFT node + multi-node e2e — **deferred**
