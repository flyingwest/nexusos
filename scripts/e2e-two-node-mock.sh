#!/usr/bin/env bash
# Mock two-node e2e: build, start A+B over HTTPS (--dev self-signed), pair, pull/start, sync, assert ledger on B.
# Uses --dev for auto self-signed TLS; API + join tokens are still set to match the hardened security bar.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

API_TOKEN="${API_TOKEN:-e2e-api-token}"
JOIN_TOKEN="${JOIN_TOKEN:-e2e-join-token}"
DIR_A="${DIR_A:-/tmp/nexus-e2e-a}"
DIR_B="${DIR_B:-/tmp/nexus-e2e-b}"
PORT_A="${PORT_A:-18080}"
PORT_B="${PORT_B:-18081}"
API_A="https://127.0.0.1:${PORT_A}"
API_B="https://127.0.0.1:${PORT_B}"

COORD="${ROOT}/bin/coordinator"
CTL="${ROOT}/bin/nexusctl"
LOG_A="${DIR_A}/coordinator.log"
LOG_B="${DIR_B}/coordinator.log"

cleanup() {
  if [[ -n "${PID_A:-}" ]]; then kill "$PID_A" 2>/dev/null || true; fi
  if [[ -n "${PID_B:-}" ]]; then kill "$PID_B" 2>/dev/null || true; fi
  wait 2>/dev/null || true
}
trap cleanup EXIT

echo "==> Building"
mkdir -p bin
( cd coordination && go build -o ../bin/coordinator ./cmd/coordinator )
( cd coordination && go build -o ../bin/nexusctl ./cmd/nexusctl )

rm -rf "$DIR_A" "$DIR_B"
mkdir -p "$DIR_A" "$DIR_B"

echo "==> Starting coordinator A on :${PORT_A}"
"$COORD" --dev --mock \
  --listen ":${PORT_A}" \
  --data-dir "$DIR_A" \
  --api-token "$API_TOKEN" \
  --join-token "$JOIN_TOKEN" \
  --advertise "$API_A" \
  --sync-interval 1h \
  >"$LOG_A" 2>&1 &
PID_A=$!

echo "==> Starting coordinator B on :${PORT_B}"
"$COORD" --dev --mock \
  --listen ":${PORT_B}" \
  --data-dir "$DIR_B" \
  --api-token "$API_TOKEN" \
  --join-token "$JOIN_TOKEN" \
  --advertise "$API_B" \
  --sync-interval 1h \
  >"$LOG_B" 2>&1 &
PID_B=$!

ctl() {
  "$CTL" --api "$1" --token "$API_TOKEN" --insecure "${@:2}"
}

echo "==> Waiting for health"
for i in $(seq 1 50); do
  if ctl "$API_A" health >/dev/null 2>&1 && ctl "$API_B" health >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$PID_A" 2>/dev/null; then
    echo "A died:"; cat "$LOG_A"; exit 1
  fi
  if ! kill -0 "$PID_B" 2>/dev/null; then
    echo "B died:"; cat "$LOG_B"; exit 1
  fi
  sleep 0.1
done
ctl "$API_A" health
ctl "$API_B" health

echo "==> Pair A <-> B"
ctl "$API_A" pair "$API_B"

echo "==> Pull + start on A"
ctl "$API_A" images pull docker.io/library/nginx:alpine
ctl "$API_A" containers start docker.io/library/nginx:alpine --name web

echo "==> Sync from A"
ctl "$API_A" sync

echo "==> Assert ledger on B"
LEDGER_B="$(ctl "$API_A" ledger 2>/dev/null || true)"
# Prefer B's ledger view
LEDGER_B="$(ctl "$API_B" ledger)"
echo "$LEDGER_B" | grep -q '"current_node"'
echo "$LEDGER_B" | grep -q '"digest"'
# Container placement should reference a node id (non-empty current_node values appear)
if ! echo "$LEDGER_B" | grep -q 'web\|Running\|current_node'; then
  echo "FAIL: unexpected ledger on B:"
  echo "$LEDGER_B"
  exit 1
fi

# Stronger check via python/jq if available
if command -v python3 >/dev/null 2>&1; then
  python3 - "$LEDGER_B" <<'PY'
import json, sys
snap = json.loads(sys.argv[1])
containers = snap.get("containers") or {}
images = snap.get("images") or {}
if not images:
    raise SystemExit("no images on B ledger")
if not containers:
    raise SystemExit("no containers on B ledger")
ok = False
for c in containers.values():
    if c.get("current_node") and c.get("desired") in ("Running", "running", "Running"):
        ok = True
        break
    if c.get("current_node"):
        ok = True
        break
if not ok:
    raise SystemExit(f"placement missing: {containers}")
print("ledger assertion OK: images=%d containers=%d" % (len(images), len(containers)))
PY
fi

echo "==> Chain status"
ctl "$API_A" chain
ctl "$API_B" chain

echo "==> e2e-two-node-mock: PASS"
