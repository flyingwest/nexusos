# Phase 2 Harden + Freeze — Summary

**Date**: 2026-09-03 (America/Chicago)  
**Tree**: `/workspace/nexusos/nexusos`

## What changed (paths)

### Code
- `coordination/internal/config/config.go` — require API token + join-token + TLS unless `Dev`; `AdvertiseFromListen(scheme)`; `TLSEnabled()`
- `coordination/internal/config/tls.go` — `EnsureDevTLS` self-signed P-256 cert under data-dir
- `coordination/internal/config/config_test.go` — validation + cert generation tests
- `coordination/cmd/coordinator/main.go` — `--dev`/`--insecure-dev`, `--tls-cert`/`--tls-key`/`--tls-insecure-peers`; `NEXUS_API_TOKEN` / `NEXUS_JOIN_TOKEN` fallbacks; TLS peer client
- `coordination/internal/api/httpapi/server.go` — `ListenAndServeTLS` when cert/key set
- `coordination/internal/api/httpapi/middleware.go` — comment clarified
- `coordination/internal/api/httpapi/tls_test.go` — HTTPS smoke + auth
- `coordination/internal/api/httpapi/auth_test.go` — bearer middleware tests
- `coordination/internal/p2p/client.go` — `NewClientTLS(insecure)`
- `coordination/cmd/nexusctl/main.go` — default HTTPS base; `--insecure`, `--cacert`

### Scripts / packaging / docs
- `scripts/e2e-two-node-mock.sh` — automated mock two-node over HTTPS (`--dev` self-signed + tokens)
- `scripts/install.sh` — Phase 2 hardened messaging
- `scripts/dev-setup.md` — aligned to freeze / deferred list
- `deploy/nexusos-coordinator.service` — `NEXUS_API_TOKEN` / `NEXUS_JOIN_TOKEN` + TLS paths
- `Makefile` — `run` uses `--dev`; `e2e` target
- `README.md` — HTTPS + tokens quick start; removed stale `hack/` mention
- `CONTRIBUTING.md` — Phase 2 frozen
- `docs/decisions.md` — 2026-09-03 harden+freeze decision
- `docs/phase2-freeze.md` — exit checklist + deferred
- `docs/roadmap.md` — Phase 2 marked frozen
- **Removed** `grok_conv.txt`

## How to re-verify

```bash
cd /workspace/nexusos/nexusos/coordination && go test ./...

cd /workspace/nexusos/nexusos
(cd coordination && go build -o ../bin/coordinator ./cmd/coordinator)
(cd coordination && go build -o ../bin/nexusctl ./cmd/nexusctl)

# Empty tokens rejected without --dev:
./bin/coordinator --mock --listen :18099 --data-dir /tmp/nexus-reject
# expect: config: api-token is required ...

./scripts/e2e-two-node-mock.sh
```

## Judgments

- Security bar is join-token + API token + TLS (same HTTP listener for operator and peer `/v1/net/*`). Not localhost-only; not mTLS.
- E2E uses `--dev` for auto self-signed TLS while still setting tokens, matching README.
- `NEXUS_API_TOKEN` wired as fallback (kept systemd Environment= meaningful) rather than removing it.
- Also wired `NEXUS_JOIN_TOKEN` for symmetry.
- Permissioned hash-chain left as-is; CometBFT / OS image / Phase 3 / CRIU / UI explicitly deferred.

## Known limits

- `--dev` peer dials use `InsecureSkipVerify`.
- Hash-chain is not BFT finality.
- `make` may be absent on minimal boxes; use `go build` as shown above.
- Built binaries under `bin/` are artifacts only (gitignored).
