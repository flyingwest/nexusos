# Contributing to NexusOS

Thank you for your interest.

## Current stage

The project is in **Phase 2 (frozen)**: multi-node permissioned ledger sync + hash-chain consensus, hardened with required **join-token + API token + TLS**.

Do **not** start Phase 3 orchestration, CometBFT replacement, OS image packaging, CRIU, or UI work against this freeze without an explicit new milestone decision in `docs/decisions.md`.

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

## Pull requests

- Keep changes focused
- Update docs when behavior or design changes
- Explain *why* in the PR description
- Do not commit built binaries under `bin/`

## Code of conduct

Be respectful. This is an infrastructure project that aims to stay pragmatic and usable.
