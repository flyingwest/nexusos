#!/usr/bin/env bash
# Single-node CometBFT + mock runtime: create workload, wait for reconciler containers,
# and prove RollingUpdate does not update all replica digests in one tick.
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
( cd coordination && go test ./internal/orchestrate/ ./internal/api/httpapi/ ./internal/ledger/ -run 'Workload|Reconciler|Schedule|Rolling|Recreate|Strategy' -count=1 )

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

echo "==> Delete demo before rolling scenario"
ctl workloads delete demo

echo "==> Rolling update image (replicas=3, strategy=RollingUpdate)"
ctl workloads create roll --image nginx:roll-v1 --replicas 3 --strategy RollingUpdate --max-unavailable 1

echo "==> Wait for three runtime containers"
ok=0
for i in $(seq 1 40); do
  out="$(ctl containers list 2>/dev/null || true)"
  if echo "$out" | grep -q 'wl:roll:0' && echo "$out" | grep -q 'wl:roll:1' && echo "$out" | grep -q 'wl:roll:2'; then
    ok=1
    break
  fi
  sleep 0.5
done
if [[ "$ok" != "1" ]]; then
  echo "timeout waiting for roll replicas"
  echo "$out"
  cat "$LOG" | tail -40
  exit 1
fi

OLD_DIGEST="$(python3 -c "import json; print(json.load(open('${DIR}/ledger.json'))['workloads']['roll']['image_digest'])")"
echo "    old digest=$OLD_DIGEST"

echo "==> Update image to v2 (rolling)"
ctl workloads update roll --image nginx:roll-v2 --strategy RollingUpdate

echo "==> Assert mid-roll mixed digests (not all replicas updated at once)"
mixed=0
for i in $(seq 1 80); do
  if OLD_DIGEST="$OLD_DIGEST" DIR="$DIR" python3 - <<'PY'
import json, os, pathlib, sys
led = json.loads(pathlib.Path(os.environ["DIR"] + "/ledger.json").read_text())
wl = led["workloads"]["roll"]
new = wl["image_digest"]
old = os.environ["OLD_DIGEST"]
if new == old:
    raise SystemExit("workload digest not updated yet")
ctr = led.get("containers") or {}
digests = []
for idx in (0, 1, 2):
    c = ctr.get(f"wl:roll:{idx}")
    if not c:
        raise SystemExit("missing container")
    digests.append(c.get("image_digest") or "")
updated = sum(1 for d in digests if d == new)
stale = sum(1 for d in digests if d == old)
if updated >= 1 and stale >= 1:
    print(f"    mid-roll OK updated={updated} stale={stale} digests={digests}")
    sys.exit(0)
if updated == 3:
    raise SystemExit("all replicas already new — roll too fast to observe (fail)")
raise SystemExit(f"waiting mixed state updated={updated} stale={stale}")
PY
  then
    mixed=1
    break
  fi
  sleep 0.25
done
if [[ "$mixed" != "1" ]]; then
  echo "FAIL: did not observe mid-roll mixed digests"
  ctl workloads get roll || true
  cat "$LOG" | tail -60
  exit 1
fi

echo "==> Wait for roll to complete (all digests new)"
ok=0
for i in $(seq 1 60); do
  if DIR="$DIR" python3 - <<'PY'
import json, os, pathlib
led = json.loads(pathlib.Path(os.environ["DIR"] + "/ledger.json").read_text())
new = led["workloads"]["roll"]["image_digest"]
ctr = led.get("containers") or {}
for idx in (0, 1, 2):
    c = ctr.get(f"wl:roll:{idx}")
    if not c or c.get("image_digest") != new:
        raise SystemExit("not done")
print("    roll complete")
PY
  then
    ok=1
    break
  fi
  sleep 0.5
done
if [[ "$ok" != "1" ]]; then
  echo "FAIL: rolling update did not complete"
  exit 1
fi

echo "==> Delete roll workload"
ctl workloads delete roll
echo "PASS e2e-workload-mock"
