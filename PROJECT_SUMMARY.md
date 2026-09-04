# NexusOS — Project Summary

**NexusOS** is a distributed operating system.  
Each node is a minimal Linux-based OS built to run containers. The nodes form a network that communicates and coordinates using blockchain-style mechanisms.

The shared ledger tracks critical facts about the system — especially **which containers are running where** and the **integrity of container images** — and enables secure, verifiable communication and coordination between OS instances.

Native orchestration exists, but it is built on top of this distributed OS foundation. It is not the primary identity of the project.

## One-sentence description

A distributed operating system whose nodes form a blockchain-style network for running containers with strong image integrity and verifiable placement.

## Core Idea (Primary)

Most systems treat the operating system as a local, isolated thing and put distribution and coordination in a separate control plane.

NexusOS inverts that:

- Every machine runs a purpose-built **container-oriented OS** (minimal Linux).
- These OS instances **form a network** with each other.
- They communicate and maintain shared state using **blockchain-style mechanisms** (consensus + ledger).
- The ledger’s most important jobs are:
  - Recording and verifying **container image integrity** (content-addressed digests)
  - Tracking **which containers are running on which nodes**
  - Supporting secure, attributable communication and coordination between OS nodes
- The result is a **distributed OS** rather than a collection of independent hosts managed by an external orchestrator.

This distributed, integrity-focused OS layer is the foundation. Everything else builds on it.

## Secondary Capabilities

On top of the distributed OS foundation we also provide:

- Native (but simple) orchestration — Workloads, scheduling, reconciliation
- Coordinated migration of containers between nodes
- A UI and API that work with structured state (ledger + database) instead of being YAML-first
- Dual operating modes: Permissioned (first) and Permissionless (later)

These are important, but they are not the defining characteristic of NexusOS.

## Why This Matters

- The OS nodes themselves are the coordinating fabric.
- Image integrity and placement are first-class, cryptographically attributable facts on a shared ledger.
- Communication between OS instances happens in a structured, verifiable way.
- The system is distributed by nature, not by bolting an orchestrator on top of ordinary Linux hosts.

## Key Characteristics of the Blockchain / Coordination Layer

| Concern                         | Role in NexusOS                                              |
|---------------------------------|--------------------------------------------------------------|
| Shared ledger                   | Source of truth for placement and image integrity            |
| Image integrity                 | Digests are registered and verified across nodes             |
| Placement state                 | Network-wide view of which container runs where              |
| Node identity & communication   | OS instances authenticate and talk to each other securely    |
| Consensus                       | Nodes agree on the above state                               |
| Modes                           | Permissioned (initial) and Permissionless (later)            |

The ledger is deliberately narrow. It does not try to store high-frequency metrics or replace a general database. Its job is the critical shared facts that make the OS network coherent and trustworthy.

## Architecture (Simplified)

```
UI / CLI / API
      ↓
Coordination Service (Go) — userspace
  • P2P communication between OS nodes
  • Consensus & shared ledger
  • Image integrity
  • Placement state
  • (Secondary) Orchestration & migration control
      ↓
containerd + runc (+ CRIU)
      ↓
Minimal Linux Base (the actual OS on each node)
```

Every node runs the full stack. There is no separate heavy control plane that is “the real system.” The network of OS instances *is* the system.

## Design Principles (Ordered by Importance)

1. **Distributed OS first** — Nodes are full OS instances that form a network.
2. **Blockchain-style coordination** — Shared ledger + consensus for critical state and communication.
3. **Image integrity & placement as core ledger concerns** — These are not optional add-ons.
4. **Userspace coordination** — The ledger layer lives above the kernel.
5. **Narrow ledger** — Only the facts required for integrity, placement, and coordination.
6. **Simple native orchestration** — Built on the foundation above; kept deliberately limited.
7. **Permissioned first** — Reliable private/edge networks before open permissionless mode.
8. **UI + structured state** — Operators work with data and a UI; YAML is secondary.
9. **OCI compatible & incremental** — Standard containers; ship working slices.

## Technology Choices

- **Base OS**: Minimal Linux (immutable / atomic updates preferred)
- **Runtime**: containerd + runc
- **Coordination service**: Go (userspace)
- **Consensus (initial)**: CometBFT-style
- **Migration**: CRIU (cold first)
- **Primary architecture**: x86_64
- **Identity**: Ed25519 keys (+ certificates in permissioned mode)

## Current Status (August 2026)

**Phase 0 – Foundation**: Complete

**Phase 1 – Single-node host**: Hardened (mock + containerd, API, CLI, identity, local state)

**Phase 2 – Multi-node ledger (in progress)**:
- Permissioned pairing with a join token
- Ed25519-signed HTTP snapshot sync between peers
- Shared view of placement + image `verified_by` (eventual consistency)
- Nodes marked Offline after missed heartbeats (default 30s)
- Permissioned hash-chain consensus for image integrity and placement (`ceil(2n/3)` votes)
- Not yet: embedded CometBFT / BFT finality, bootable OS image

**Next**: Embed CometBFT, or package a minimal Linux node image.

## Roadmap Overview

| Phase | Focus                                      | Goal |
|-------|--------------------------------------------|------|
| 0     | Foundation                                 | Clear design & priorities |
| 1     | Single-node host                           | Run & track containers + image integrity locally |
| 2     | Multi-node OS network                      | Shared ledger for placement + image integrity |
| 3     | Native orchestration (secondary)           | Simple Workloads on top of the distributed OS |
| 4     | Migration                                  | Coordinated movement of containers between OS nodes |
| 5     | Hardening + permissionless path            | Broader readiness |

## What This Is Not

- Not “just another orchestrator”
- Not a full Kubernetes replacement
- Not a from-scratch kernel (we use minimal Linux)
- Not a system that puts arbitrary data on a blockchain

## Summary

NexusOS is first and foremost a **distributed operating system**.  
Its nodes run a minimal container-oriented Linux, form a network, and use blockchain-style mechanisms so they can securely share state about container images and placement.  

Orchestration, migration, and a clean UI are valuable layers built on that foundation. They are not the foundation itself.

---

*This summary is the canonical high-level overview of the project.*
