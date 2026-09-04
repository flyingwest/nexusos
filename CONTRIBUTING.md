# Contributing to NexusOS

Thank you for your interest.

## Current stage

The project is in **Phase 2 (frozen)**: multi-node permissioned ledger sync + hash-chain consensus, hardened with required **join-token + API token + TLS**.

Do **not** start Phase 3 orchestration, CometBFT replacement, CRIU, or UI work against this freeze without an explicit new milestone decision in `docs/decisions.md`. Minimal Debian Bookworm node image packaging is an accepted **post-freeze** milestone (`image/`, `docs/node-image.md`, decision 2026-09-04).

Highest-priority foundation (still true):

1. The distributed OS concept
2. Image integrity
3. Placement state
4. Secure communication between OS nodes

Orchestration and migration remain important but secondary / deferred.

## Development principles

- Prefer simplicity and clarity
- Keep the ledger narrow
- Userspace coordination only
- Mock runtime must remain usable for development and CI without root/containerd
- Normal starts require tokens + TLS; use `--dev` / `--insecure-dev` only for local experiments
- Document decisions in `docs/decisions.md`

## How to work on the code

```bash
cd coordination
go test ./...
go build -o ../bin/coordinator ./cmd/coordinator
go build -o ../bin/nexusctl ./cmd/nexusctl
```

Mock two-node e2e (HTTPS + tokens, `--dev` for self-signed):

```bash
./scripts/e2e-two-node-mock.sh
```

Node image (privileged Linux builder; optional):

```bash
sudo ./image/build.sh --dev-smoke
./scripts/qemu-node-smoke.sh
```


## Pull requests

- Keep changes focused
- Update docs when behavior or design changes
- Explain *why* in the PR description
- Do not commit built binaries under `bin/`

## Contributors

See [`CONTRIBUTORS.md`](CONTRIBUTORS.md) for the project maintainer and how to be listed.

## Code of conduct

Be respectful. This is an infrastructure project that aims to stay pragmatic and usable.
