# Operator UI

Practical web UI for NexusOS operators. It talks to the existing HTTPS coordinator JSON API (no new orchestration semantics). Primary interface direction remains **UI + API + structured state** (see PROJECT_SUMMARY.md / docs/decisions.md).

## How it is served

The coordinator **embeds** the static assets and serves them at:

| Path | Purpose |
|------|---------|
| `/ui/` | SPA shell (`index.html`) |
| `/ui/app.js` | Client logic |
| `/ui/styles.css` | Styles |

One binary serves API + UI. Source tree:

- Canonical assets: `coordination/internal/api/httpapi/ui/`
- Repo-root `ui/` is a **symlink** to that directory (convenient browsing / `make ui`)

Build (embed happens via `go:embed`):

```bash
make coordinator   # or: make build
./bin/coordinator --dev --mock --listen :8080 --api-token secret --join-token cluster
```

Open `https://127.0.0.1:8080/ui/` (accept the self-signed cert warning when using `--dev`).

Optional check that assets exist before compile:

```bash
make ui
```

There is no separate SPA toolchain. Assets are plain HTML/CSS/vanilla JS.

	## Configuration (browser)

The Settings view stores **API base URL** and **bearer token** in `localStorage` only. Values are never baked into the coordinator image or binary.

- When the UI is loaded from the coordinator, leave base URL empty to use `window.location.origin`.
- Paste the same operator token you pass to `--api-token` / `nexusctl --token`.
- **TLS note**: browsers cannot mirror `nexusctl --insecure`. For `--dev` self-signed certs, trust/accept the certificate in the browser once (or use a real cert / private CA). The UI documents this on the Settings page.

Static `/ui/*` routes do **not** require the bearer token (so the shell can load). All `/v1/*` calls from the UI send `Authorization: Bearer ….

## Views

| View | API used |
|------|---------|
| Dashboard | `GET /health`, `/v1/node`, `/v1/nodes`, `/v1/members`, `/v1/peers`, `/v1/ledger` |
| Containers | `GET/POST /v1/containers`, stop/remove; ledger placement from `/v1/ledger` |
| Workloads | list/create/scale/update/delete via `/v1/workloads*`; replica placement from ledger containers |
| Migrations | list + propose cold migrate via `/v1/migrations` (Phase 4 only — no live migration UI) |
| Settings | local connection config |

## Security model

Unchanged from the coordinator bar:

1. Operator API token on mutating/reading `/v1/*` (except net/bootstrap as today)
2. TLS required outside `--dev`
3. No credentials in the image; browser key `nexusos.operator.ui.v1` only

## Smoke / CI

- `go test ./internal/api/httpapi/ -run TestOperatorUIEmbeddedAndPublic` asserts embed + public `/ui/` + protected `/v1/node`
- CI runs that test and `go build` of the coordinator

## Out of scope (this increment)

- Live migration UI
- YAML-first workflows
- Heavy SPA framework / npm build
- Baking operator credentials into images
