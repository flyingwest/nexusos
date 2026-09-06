# Phase 5 – Hardening (observability, drain, version)

Production-oriented operator surfaces on the **permissioned, CometBFT-only** coordinator.
**No permissionless membership** and **no live migration** in this increment.

## Delivered

| Area | What shipped |
|------|----------------|
| **Metrics** | Prometheus text at `GET /metrics` and `GET /v1/metrics` |
| **Health** | Richer `GET /health` (+ alias `/v1/health`); ready at `GET /v1/ready` |
| **Drain** | Cordon / uncordon / drain (+ evacuate) via API, `nexusctl`, operator UI |
| **Version** | `GET /v1/version` (ldflags + `runtime/debug`); shown in UI Settings/Dashboard |
| **Docs** | This file + decision log + roadmap + README + operator-ui touch |

## Metrics

Uses `prometheus/client_golang` on an isolated registry (not the default CometBFT gatherer).

| Metric | Type | Meaning |
|--------|------|---------|
| `nexusos_api_requests_total` | counter | HTTP requests by method / path label / status class |
| `nexusos_containers` | gauge | Ledger container count |
| `nexusos_workloads` | gauge | Ledger workload count |
| `nexusos_migrations` | gauge | Migration records |
| `nexusos_nodes_online` / `nexusos_nodes_draining` | gauge | Node status counts |
| `nexusos_peers` | gauge | HTTP sync peers |
| `nexusos_consensus_height` | gauge | CometBFT ABCI height when consensus is on |

**Auth**: same API bearer as other `/v1` routes by default. For Prometheus scrape without a token, start with `--metrics-public` (documents exposure — prefer network ACL).

```bash
nexusctl --api https://127.0.0.1:8080 --token secret --insecure metrics
curl -k -H "Authorization: Bearer secret" https://127.0.0.1:8080/metrics
```

## Health / ready / version

`GET /health` (public) returns:

```json
{
  "status": "ok",
  "node_id": "...",
  "engine": "cometbft",
  "mode": "permissioned",
  "live": true,
  "consensus_height": 12,
  "summary": {
    "containers": 0,
    "workloads": 0,
    "migrations": 0,
    "images": 0,
    "members": 1,
    "nodes_online": 1,
    "nodes_offline": 0,
    "nodes_draining": 0,
    "peers": 0
  }
}
```

| Endpoint | Auth | Role |
|----------|------|------|
| `/health`, `/v1/health` | public | Liveness + summary |
| `/v1/ready` | bearer | Readiness (`ready` bool; 503 if identity/ledger missing) |
| `/v1/version` | bearer | `version`, `commit`, `go_version`, `module` |

Build stamps via Makefile ldflags:

```bash
make build   # injects VERSION + COMMIT into internal/version
```

## Node drain / evacuate

Ledger node `status` already included `Draining`. Phase 5 makes it operable:

1. **Cordon** → `Status=Draining` (scheduling disabled). Heartbeats **preserve** Draining (no auto-uncordon).
2. **Uncordon** → `Status=Online`.
3. **Drain** → cordon + optional evacuate:
   - **Workload** replicas: sticky scheduling skips draining nodes → reconciler re-places (cold path; no live migration).
   - **Standalone** containers: cold migrate via existing Phase 4 controller to a least-loaded Online peer.

Status is ledger-local (HTTP snapshot sync), not a new consensus tx. Membership `MemberRecord` is unchanged; scheduling uses node status only.

```text
POST /v1/nodes/{id}/cordon
POST /v1/nodes/{id}/uncordon
POST /v1/nodes/{id}/drain     {"evacuate": true|false}

nexusctl nodes cordon <id>
nexusctl nodes uncordon <id>
nexusctl nodes drain <id> [--evacuate=true]
```

Operator UI: Dashboard → Nodes → Cordon / Drain / Uncordon.

## Constraints (unchanged)

- Permissioned mode only (no permissionless path in this PR)
- CometBFT-only consensus
- Cold migration only (Phase 4); drain uses that for standalone containers
- TLS + API token security bar; metrics gated unless `--metrics-public`

## Verify

```bash
cd coordination && go test ./...
make test-provision
make e2e                    # cometbft two-node wrapper
make e2e-workload           # workload mock
# optional: make e2e-workload-multinode / e2e-migration
```

## Deferred

- Permissionless membership (experimental) — explicitly out of this increment
- OpenTelemetry traces / log shipping
- Consensus txs for cordon (cluster-wide ordered drain) if HTTP merge races become an issue
- Streaming CRIU artifacts; live migration
- Prometheus recording rules / Grafana dashboards as first-class assets
