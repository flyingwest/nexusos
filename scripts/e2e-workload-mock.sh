#!/usr/bin/env bash
# Single-node CometBFT + mock runtime: create workload, wait for reconciler containers.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

API_TOKEN="${API_TOKEN:-e2e-wl-api}"
JOIN_TOKEN="${JOIN_TOKEN:-e2e-wl-join}"
DIR="${DIR:-/tmp/nexus-wl-e2e}"
PORT="${PORT:-18280}"
CMT_RPC="${CMT_RPC:-tcp://127.0.0.1:29657}"
CMT_P2P="${CMT_P2P:-tcp://127.0.0.1:29656}"
API="https://127.0.0.1:${PORT}"

COORD="${ROOT}/bin/coordinator"
CTL="${ROOT}/bin/nexusctl"
LOG="${DIR}/coordinator.log"
PID=""

cleanup() {
  if [[ -n "${PID:-}" ]]; then kill "$PID" 2>/dev/null || true; fi
  wait 2>/dev/null || true
}
trap cleanup EXIT

ctl() { "$CTL" --api "$API" --token "$API_TOKEN" --insecure "$@"; }

echo "==> Building"
mkdir -p bin
( cd coordination && go build -o ../bin/coordinator ./cmd/coordinator )
( cd coordination && go build -o ../bin/nexusctl ./cmd/nexusctl )

echo "==> Unit/integration workload tests"
( cd coordination && go test ./internal/orchestrate/ ./internal/api/httpapi/ -run 'Workload|Reconciler|Schedule' -count=1 )

rm -rf "$DIR"
mkdir -p "$DIR"

echo "==> Start single-node coordinator (CometBFT + mock)"
"$COORD" --dev --mock --consensus \
  --data-dir "$DIR" --listen ":${PORT}" \
  --api-token "$API_TOKEN" --join-token "$JOIN_TOKEN" \
  --cometbft-rpc "$CMT_RPC" --cometbft-p2p "$CMT_P2P" \
  >"$LOG" 2>&1 &
PID=$!

for i in $(seq 1 80); do
  if ctl health >/dev/null 2>&1; then break; fi
  if ! kill -0 "$PID" 2>/dev/null; then echo "died:"; cat "$LOG"; exit 1; fi
  sleep 0.15
done
ctl health >/dev/null

echo "==> Create workload replicas=1"
ctl workloads create demo --image nginx:e2e --replicas 1

echo "==> Wait for runtime container"
ok=0
for i in $(seq 1 40); do
  out="$(ctl containers list 2>/dev/null || true)"
  if echo "$out" | grep -q 'wl:demo:0'; then
    ok=1
    break
  fi
  sleep 0.5
done
if [[ "$ok" != "1" ]]; then
  echo "timeout waiting for wl:demo:0"
  echo "$out"
  ctl workloads list || true
  ctl ledger || true
  cat "$LOG" | tail -80
  exit 1
fi

echo "==> Scale to 2"
ctl workloads scale demo 2
ok=0
for i in $(seq 1 40); do
  out="$(ctl containers list 2>/dev/null || true)"
  if echo "$out" | grep -q 'wl:demo:1'; then
    ok=1
    break
  fi
  sleep 0.5
done
if [[ "$ok" != "1" ]]; then
  echo "timeout waiting for second replica"
  echo "$out"
  cat "$LOG" | tail -40
  exit 1
fi

echo "==> Delete workload"
ctl workloads delete demo
echo "PASS e2e-workload-mock"
