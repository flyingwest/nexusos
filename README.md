# NexusOS

**A distributed operating system for containers.**

Each node is a minimal Linux-based OS.  
The nodes form a network and coordinate using blockchain-style mechanisms.  
The shared ledger tracks **container image integrity** and **which containers run where**, and enables secure communication between OS instances.

Native orchestration exists, but it is secondary to the distributed OS foundation.

> **Status**: Phase 2 **FROZEN** — permissioned peer ledger sync + hash-chain consensus, hardened with required join-token + API token + TLS. Phase 1 single-node path remains the local runtime (mock by default).

---

## Core Vision

- Every machine runs a purpose-built container-oriented OS (minimal Linux).
- These OS instances form a network with one another.
- They maintain shared, verifiable state (especially image integrity and placement) via a ledger and consensus.
- Communication between OS nodes is structured and attributable.
- The result is a **distributed OS**, not a set of ordinary hosts managed by an external orchestrator.

## What the Ledger Is For

- Container image integrity (content-addressed digests, verification)
- Placement state (“which container is running on which node”)
- Secure, verifiable coordination and communication between OS instances
- Supporting later features such as migration (CRIU) and simple orchestration

## Quick Start (hardened Phase 2)

### Prerequisites

- Go 1.22+
- Linux (or macOS for development with the mock runtime)

### Build

```bash
git clone https://github.com/YOUR_USERNAME/nexusos.git
cd nexusos
make build   # or: (cd coordination && go build -o ../bin/coordinator ./cmd/coordinator && go build -o ../bin/nexusctl ./cmd/nexusctl)
```

This produces (do not commit these; build with `make build`):

- `bin/coordinator` — the coordination service
- `bin/nexusctl` — CLI client

### Security bar (normal start)

Normal starts **require**:

1. `--api-token` (or env `NEXUS_API_TOKEN`)
2. `--join-token` (or env `NEXUS_JOIN_TOKEN`)
3. `--tls-cert` + `--tls-key`

For local experiments only, pass `--dev` (alias `--insecure-dev`): allows empty tokens and auto-generates a self-signed cert under `--data-dir` (`dev-tls.crt` / `dev-tls.key`). Use `nexusctl --insecure` with self-signed certs.

### Run a single node (dev escape hatch)

```bash
./bin/coordinator --dev --mock --listen :8080 \
  --api-token secret --join-token cluster
```

```bash
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure health
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure node
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure images pull docker.io/library/nginx:alpine
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure containers start docker.io/library/nginx:alpine --name web
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure containers list
```

### Production-style start (tokens + your TLS files)

```bash
export NEXUS_API_TOKEN=secret
./bin/coordinator --mock --listen :8080 --data-dir /var/lib/nexusos \
  --join-token cluster \
  --tls-cert /etc/nexusos/tls.crt --tls-key /etc/nexusos/tls.key \
  --advertise https://coordinator.example:8080
```

```bash
./bin/nexusctl --api https://coordinator.example:8080 --token secret health
# or with a private CA:
./bin/nexusctl --api https://coordinator.example:8080 --token secret --cacert /etc/nexusos/ca.crt health
```

### Two-node ledger sync (Phase 2)

```bash
# terminal 1
./bin/coordinator --dev --mock --listen :8080 --data-dir /tmp/nexus-a \
  --api-token secret --join-token cluster \
  --advertise https://127.0.0.1:8080

# terminal 2
./bin/coordinator --dev --mock --listen :8081 --data-dir /tmp/nexus-b \
  --api-token secret --join-token cluster \
  --advertise https://127.0.0.1:8081
```

Pair, create state on A, inspect B:

```bash
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure pair https://127.0.0.1:8081
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure images pull docker.io/library/nginx:alpine
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure containers start docker.io/library/nginx:alpine --name web
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure sync
./bin/nexusctl --api https://127.0.0.1:8081 --token secret --insecure ledger
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure chain
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure nodes
```

Automated equivalent:

```bash
./scripts/e2e-two-node-mock.sh
```

B should show the same image digest (`verified_by` includes A's node id) and the container's `current_node` as A. With `--consensus` (the default), B often already has that state from the commit, before `sync`.

A node that misses heartbeats for `--heartbeat-timeout` (default 30s) is marked `Offline` on the ledger.

containerd remains supported via `--mock=false` but is **not** required for CI or the mock e2e path.

---

## Project Structure

```
nexusos/
├── PROJECT_SUMMARY.md      # Canonical high-level overview
├── README.md
├── LICENSE                 # Apache 2.0
├── Makefile
├── docs/
│   ├── architecture.md
│   ├── roadmap.md
│   ├── ledger-schema.md
│   ├── decisions.md
│   └── phase2-freeze.md    # Phase 2 exit / freeze checklist
├── coordination/           # Go module – coordination service
│   ├── cmd/coordinator/
│   ├── cmd/nexusctl/
│   └── internal/
│       ├── api/httpapi/
│       ├── config/
│       ├── identity/       # Ed25519 node keys
│       ├── membership/
│       ├── runtime/        # mock + containerd
│       ├── ledger/         # placement + image integrity
│       ├── p2p/            # signed snapshot sync
│       ├── consensus/      # permissioned hash-chain (placement + images)
│       └── state/
├── scripts/                # install.sh, e2e-two-node-mock.sh
└── deploy/                 # systemd unit
```

## Roadmap (short)

| Phase | Focus |
|-------|--------|
| 0     | Foundation (done) |
| 1     | Single-node host + image integrity (done) |
| 2     | Multi-node OS network + shared ledger — **FROZEN** (HTTP sync + hash-chain; tokens+TLS) |
| 3     | Simple native orchestration (secondary) — deferred |
| 4     | Coordinated cold migration (CRIU) — deferred |
| 5     | Hardening + permissionless path |

## Install on a host

```bash
# Development / mock mode (--dev = self-signed TLS)
make build
./bin/coordinator --dev --mock --listen :8080 --api-token secret --join-token cluster

# System-style install (needs Go on the machine); edit the unit for real tokens + TLS files
sudo ./scripts/install.sh          # mock mode
sudo MODE=containerd ./scripts/install.sh
# Then set NEXUS_API_TOKEN / NEXUS_JOIN_TOKEN and TLS paths in the unit:
sudo systemctl edit nexusos-coordinator
sudo systemctl enable --now nexusos-coordinator
```

## Publishing to your GitHub

1. Create a new empty repository on GitHub (e.g. `nexusos`).
2. In this directory:

```bash
git init
git add .
git commit -m "Phase 2 freeze: harden tokens + TLS"
git branch -M main
git remote add origin https://github.com/YOUR_USERNAME/nexusos.git
git push -u origin main
```

Replace `YOUR_USERNAME` with your GitHub username.

You own the repository. This codebase is released under the **Apache License 2.0**.

## Contributing

This is an early-stage project. Issues and pull requests are welcome once the repository is public. See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/phase2-freeze.md](docs/phase2-freeze.md).

## License

Apache License 2.0 — see [LICENSE](LICENSE).

---

**Distributed OS first. Orchestration second.**
