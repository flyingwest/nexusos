# Phase 2 Freeze Checklist

**Date**: 2026-09-03  
**Milestone**: Harden Phase 2 (tests, TLS/docs/packaging) and freeze.

## Hardened in this freeze

- [x] Required **API token** (`--api-token` or `NEXUS_API_TOKEN`) for normal start
- [x] Required **join-token** (`--join-token` or `NEXUS_JOIN_TOKEN`) for normal start
- [x] Required **TLS** (`--tls-cert` + `--tls-key`) for normal start; same listener for operator + peer HTTP
- [x] Explicit `--dev` / `--insecure-dev` escape hatch (documented); auto self-signed under data-dir when certs omitted
- [x] `nexusctl --insecure` and `--cacert` for self-signed / private CA
- [x] Peer client TLS support (`p2p.NewClientTLS`) including insecure-skip for `--dev`
- [x] Unit/config tests for token/TLS rejection and TLS listen smoke
- [x] Automated `scripts/e2e-two-node-mock.sh` (pair / pull / start / sync / ledger)
- [x] README quick start updated for HTTPS + tokens; stale `hack/` tree mention removed
- [x] CONTRIBUTING aligned to Phase 2 frozen
- [x] systemd unit documents tokens + TLS; `NEXUS_API_TOKEN` wired in Go
- [x] Design dump `grok_conv.txt` removed from the tree
- [x] Binaries remain build artifacts (`make build` / `go build`); not source of truth

## Explicitly deferred (out of Phase 2)

- [ ] Embed CometBFT (or other BFT engine) in place of the permissioned hash-chain proposer
- [ ] Minimal Linux / OS node image packaging — **started post-freeze** (see `docs/node-image.md`; not part of the freeze itself)
- [ ] Phase 3 native orchestration (workloads, scheduler, reconciler)
- [ ] CRIU cold migration
- [ ] Operator UI
- [ ] Full mutual TLS (mTLS) between peers
- [ ] Permissionless membership path

## Verify before calling freeze complete

```bash
cd coordination && go test ./...
cd .. && (cd coordination && go build -o ../bin/coordinator ./cmd/coordinator && go build -o ../bin/nexusctl ./cmd/nexusctl)
./scripts/e2e-two-node-mock.sh
```

## Known limits

- Self-signed / `--dev` skips peer certificate verification (`TLSInsecureSkipVerify`).
- Hash-chain consensus is permissioned majority voting, not BFT finality.
- Mock runtime is the default CI path; containerd is optional and host-specific.
