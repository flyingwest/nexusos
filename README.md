# NexusOS

**A distributed operating system for containers.**

Each node is a minimal Linux-based OS.  
The nodes form a network and coordinate using blockchain-style mechanisms.  
The shared ledger tracks **container image integrity** and **which containers run where**, and enables secure communication between OS instances.

Native orchestration exists, but it is secondary to the distributed OS foundation.

> **Status**: Phase 2 **FROZEN** + **Phase 3 workloads** + **Phase 4 cold migration (incremental)**. Permissioned peer ledger sync, required join-token + API token + TLS. **Consensus engine is CometBFT only** (in-process) — see [docs/cometbft-spike.md](docs/cometbft-spike.md). Declarative workloads — see [docs/phase3-orchestration.md](docs/phase3-orchestration.md). Cold migration (mock + CRIU capability hook) — see [docs/phase4-migration.md](docs/phase4-migration.md). Embedded **operator UI** at `/ui/` — see [docs/operator-ui.md](docs/operator-ui.md). Phase 1 single-node path remains the local runtime (mock by default). **Post-freeze**: Debian Bookworm minimal node image tooling (`image/`, see [docs/node-image.md](docs/node-image.md)).

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
git clone https://github.com/flyingwest/nexusos-phase2.git
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

Open the operator UI (same origin; paste the API token under Settings):

```text
https://127.0.0.1:8080/ui/
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
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure ledger
./bin/nexusctl --api https://127.0.0.1:8080 --token secret --insecure nodes
```

Automated equivalent:

```bash
./scripts/e2e-two-node-mock.sh
```

B should show the same image digest (`verified_by` includes A's node id) and the container's `current_node` as A. With `--consensus` (the default), B often already has that state from the commit, before `sync`.

A node that misses heartbeats for `--heartbeat-timeout` (default 30s) is marked `Offline` on the ledger.

containerd remains supported via `--mock=false` but is **not** required for CI or the mock e2e path.

### Consensus engine (CometBFT only)

`--consensus-engine=cometbft` (default; only supported value) runs an in-process CometBFT node (strict mempool Submit, membership `JoinMember`/`LeaveMember` txs, dynamic validators). Multi-node shared genesis: `--cometbft-genesis-from <seed-url>` or `nexusctl cometbft fetch-genesis` (see [docs/cometbft-spike.md](docs/cometbft-spike.md)). The former hash-chain engine is removed.

```bash
./bin/coordinator --dev --mock   # CometBFT by default
./scripts/e2e-cometbft-two-node.sh, e2e-workload-mock.sh   # two-node CometBFT mock harness (genesis-from + pair-after-start)
./scripts/e2e-workload-multinode.sh  # Phase 3: workloads across two CometBFT nodes (make e2e-workload-multinode / make e2e-migration)
./scripts/e2e-two-node-mock.sh       # thin wrapper → e2e-cometbft-two-node.sh
```

---

## Project Structure

```
nexusos/
├── PROJECT_SUMMARY.md      # Canonical high-level overview
├── README.md
├── LICENSE                 # Apache 2.0
├── Makefile
├── ui/                     # symlink → coordination/.../httpapi/ui (operator SPA)
├── docs/
│   ├── architecture.md
│   ├── roadmap.md
│   ├── ledger-schema.md
│   ├── decisions.md
│   ├── phase2-freeze.md    # Phase 2 exit / freeze checklist
│   ├── phase3-orchestration.md  # Workloads / scheduler / reconciler
│   ├── phase4-migration.md     # Cold migration (CRIU/mock)
│   ├── operator-ui.md          # Embedded operator web UI
│   ├── node-image.md       # Debian Bookworm node image build/boot
│   └── cometbft-spike.md   # CometBFT embed (sole consensus engine)
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
│       └── consensus/
│           └── cometbft/   # in-process CometBFT + ABCI (sole engine)
│       └── state/
├── scripts/                # install.sh, e2e, qemu-node-smoke/two-node-e2e.sh
├── deploy/                 # systemd unit
└── image/                  # Debian Bookworm node image (mmdebstrap → qcow2)
```

## Roadmap (short)

| Phase | Focus |
|-------|--------|
| 0     | Foundation (done) |
| 1     | Single-node host + image integrity (done) |
| 2     | Multi-node OS network + shared ledger — **FROZEN** (HTTP sync + CometBFT; tokens+TLS) |
| 2.1   | Minimal Debian Bookworm node image (mmdebstrap) — tooling in-tree; privileged build manual |
| 3     | Simple native orchestration (workloads) — in progress |
| 4     | Coordinated cold migration (CRIU) — incremental (mock + ledger) |
| 5     | Hardening + permissionless path |

## Minimal node image (post–Phase 2)

Build a QEMU-bootable **Debian Bookworm** x86_64 disk image with containerd + runc + the coordinator:

```bash
sudo ./image/build.sh --dev-smoke
./scripts/qemu-node-smoke.sh dist/node-image/nexusos-node-bookworm-amd64-devsmoke.qcow2
# Two-guest pair/sync/ledger (TCG by default; slow):
./scripts/qemu-two-node-e2e.sh
```

Production images omit `--dev-smoke` and still require API token + join-token + TLS at runtime. Details: [docs/node-image.md](docs/node-image.md). Image builds need a Linux host with root (and KVM for comfortable smoke); A sample Go test workflow is in `docs/examples/go-test.yml` (optional to enable).

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
git remote add origin https://github.com/flyingwest/nexusos-phase2.git
git push -u origin main
```

Replace `flyingwest` with your GitHub username.

You own the repository. This codebase is released under the **Apache License 2.0**.

## Contributing

This is an early-stage project. Issues and pull requests are welcome once the repository is public. See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/phase2-freeze.md](docs/phase2-freeze.md).

## License

Apache License 2.0 — see [LICENSE](LICENSE).

---

**Distributed OS first. Orchestration second.**
