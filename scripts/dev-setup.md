# Development Setup Notes

## Recommended Development Environment

- Linux (Ubuntu 22.04/24.04 or similar) or a Linux VM
- Go 1.22+
- containerd + runc installed (optional — mock runtime is enough for Phase 2 CI)
- Docker (optional, useful for building and testing images)
- Git

## Quick local run (no containerd)

```bash
make build   # or go build the two cmd packages into bin/
./bin/coordinator --dev --mock --listen :8080 --api-token secret --join-token cluster
# default consensus engine: cometbft
# deprecated: add --consensus-engine=hashchain
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure health
./scripts/e2e-two-node-mock.sh
```

## Optional containerd check

```bash
sudo apt update
sudo apt install -y containerd runc
sudo systemctl enable --now containerd
sudo ctr version
```

Then start with `--mock=false` plus tokens and TLS files (not `--dev` for production-like checks).

## Deferred past Phase 2 freeze

1. Replace the homegrown hash-chain proposer with embedded CometBFT when we need BFT finality
2. Package a minimal Linux node image — tooling in `image/` + `docs/node-image.md` (manual privileged build)
3. CRIU cold migration (after the network fabric is boring)
4. Phase 3 orchestration and UI
