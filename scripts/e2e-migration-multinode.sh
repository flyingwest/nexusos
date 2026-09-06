#!/usr/bin/env bash
# Two-node CometBFT + mock runtime: Phase 4 cold migration.
#
# Starts a container on A, migrates it to B via POST /v1/migrations
# (propose → checkpoint → transfer → restore → complete), and asserts
# ledger current_node flips to B on both nodes.
#
# Run from repo root:
#   ./scripts/e2e-migration-multinode.sh
#   make e2e-migration
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

API_TOKEN="${API_TOKEN:-e2e-mig-api}"
JOIN_TOKEN="${JOIN_TOKEN:-e2e-mig-join}"
DIR_A="${DIR_A:-/tmp/nexus-mig-e2e-a}"
DIR_B="${DIR_B:-/tmp/nexus-mig-e2e-b}"
PORT_A="${PORT_A:-18480}"
PORT_B="${PORT_B:-18481}"
CMT_RPC_A="${CMT_RPC_A:-tcp://127.0.0.1:32657}"
CMT_P2P_A="${CMT_P2P_A:-tcp://127.0.0.1:32656}"
CMT_RPC_B="${CMT_RPC_B:-tcp://127.0.0.1:33657}"
CMT_P2P_B="${CMT_P2P_B:-tcp://127.0.0.1:33656}"
API_A="https://127.0.0.1:${PORT_A}"
API_B="https://127.0.0.1:${PORT_B}"

COORD="${ROOT}/bin/coordinator"
CTL="${ROOT}/bin/nexusctl"
LOG_A="${DIR_A}/coordinator.log"
LOG_B="${DIR_B}/coordinator.log"
PID_A=""
PID_B=""

cleanup() {
  if [[ -n "${PID_A:-}" ]]; then kill "$PID_A" 2>/dev/null || true; fi
  if [[ -n "${PID_B:-}" ]]; then kill "$PID_B" 2>/dev/null || true; fi
  wait 2>/dev/null || true
}
trap cleanup EXIT

ctl() {
  "$CTL" --api "$1" --token "$API_TOKEN" --insecure "${@:2}"
}

wait_health() {
  local api="$1" pid="$2" log="$3"
  for i in $(seq 1 80); do
    if ctl "$api" health >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "process died:"; cat "$log"; exit 1
    fi
    sleep 0.15
  done
  echo "timeout waiting for $api"; cat "$log"; exit 1
}

die_logs() {
  echo "---- A log (tail) ----"; tail -100 "$LOG_A" || true
  echo "---- B log (tail) ----"; tail -100 "$LOG_B" || true
  exit 1
}

echo "==> Building"
mkdir -p bin
( cd coordination && go build -o ../bin/coordinator ./cmd/coordinator )
( cd coordination && go build -o ../bin/nexusctl ./cmd/nexusctl )

rm -rf "$DIR_A" "$DIR_B"
mkdir -p "$DIR_A" "$DIR_B"

echo "==> Start A"
"$COORD" --dev --mock --cometbft \
  --listen ":${PORT_A}" \
  --data-dir "$DIR_A" \
  --api-token "$API_TOKEN" \
  --join-token "$JOIN_TOKEN" \
  --advertise "$API_A" \
  --sync-interval 2s \
  --cometbft-rpc "$CMT_RPC_A" \
  --cometbft-p2p "$CMT_P2P_A" \
  >"$LOG_A" 2>&1 &
PID_A=$!
wait_health "$API_A" "$PID_A" "$LOG_A"

GEN_A="$DIR_A/cometbft/config/genesis.json"
for i in $(seq 1 40); do
  [[ -f "$GEN_A" ]] && break
  sleep 0.1
done
[[ -f "$GEN_A" ]] || { echo "A genesis missing"; cat "$LOG_A"; exit 1; }

echo "==> Start B with --cometbft-genesis-from"
"$COORD" --dev --mock --cometbft \
  --listen ":${PORT_B}" \
  --data-dir "$DIR_B" \
  --api-token "$API_TOKEN" \
  --join-token "$JOIN_TOKEN" \
  --advertise "$API_B" \
  --sync-interval 2s \
  --cometbft-rpc "$CMT_RPC_B" \
  --cometbft-p2p "$CMT_P2P_B" \
  --cometbft-genesis-from "$API_A" \
  >"$LOG_B" 2>&1 &
PID_B=$!
wait_health "$API_B" "$PID_B" "$LOG_B"

echo "==> Waiting for B CometBFT height >= 1"
for i in $(seq 1 80); do
  if [[ -f "$DIR_B/cometbft/abci-meta.json" ]]; then
    H="$(python3 -c "import json;print(json.load(open(r'$DIR_B/cometbft/abci-meta.json')).get('height',0))" 2>/dev/null || echo 0)"
    if [[ "${H:-0}" -ge 1 ]]; then
      echo "    B abci height=$H"
      break
    fi
  fi
  sleep 0.25
done

echo "==> Pair A <-> B"
ctl "$API_A" pair "$API_B"

echo "==> Waiting for ledger members + Online heartbeats"
for i in $(seq 1 60); do
  ctl "$API_A" sync >/dev/null 2>&1 || true
  ctl "$API_B" sync >/dev/null 2>&1 || true
  if python3 - <<PY
import json, pathlib
for p in ("$DIR_A", "$DIR_B"):
    led=json.loads(pathlib.Path(p+"/ledger.json").read_text())
    if len(led.get("members") or {}) < 2:
        raise SystemExit("members")
    online=[n for n,v in (led.get("nodes") or {}).items() if v.get("status")=="Online"]
    if len(online) < 2:
        raise SystemExit("online")
print("    members+online OK")
PY
  then
    break
  fi
  if [[ "$i" -eq 60 ]]; then
    echo "FAIL: cluster not ready"; die_logs
  fi
  sleep 0.5
done

NODE_A="$(ctl "$API_A" node | python3 -c "import json,sys; print(json.load(sys.stdin)[\"node_id\"])")"
NODE_B="$(ctl "$API_B" node | python3 -c "import json,sys; print(json.load(sys.stdin)[\"node_id\"])")"
echo "    NODE_A=$NODE_A"
echo "    NODE_B=$NODE_B"

echo "==> Start container on A"
START_OUT="$(ctl "$API_A" containers start alpine:mig-e2e)"
echo "$START_OUT"
CID="$(python3 -c "import json,sys; j=json.load(sys.stdin); print(j.get('id') or '')" <<<"$START_OUT")"
if [[ -z "$CID" ]]; then
  echo "FAIL: could not start container on A"
  die_logs
fi
echo "    container_id=$CID"

echo "==> Wait for ledger placement on A"
OK=0
for i in $(seq 1 40); do
  if python3 - "$DIR_A" "$CID" "$NODE_A" <<'PY'
import json, pathlib, sys
led=json.loads(pathlib.Path(sys.argv[1]+"/ledger.json").read_text())
c=(led.get("containers") or {}).get(sys.argv[2])
if not c: raise SystemExit("missing")
if c.get("current_node")!=sys.argv[3]: raise SystemExit(c.get("current_node"))
print("    placed on A")
PY
  then OK=1; break; fi
  sleep 0.25
done
[[ "$OK" == "1" ]] || { echo "FAIL: placement"; die_logs; }

echo "==> Cold migrate $CID A → B"
ctl "$API_A" migrations migrate "$CID" --to "$NODE_B"

echo "==> Assert ledger current_node == B on both nodes + Success migration"
OK=0
for i in $(seq 1 40); do
  if python3 - "$DIR_A" "$DIR_B" "$CID" "$NODE_B" <<'PY'
import json, pathlib, sys
dir_a, dir_b, cid, nb = sys.argv[1:5]
for label, d in [("A", dir_a), ("B", dir_b)]:
    led=json.loads(pathlib.Path(d+"/ledger.json").read_text())
    c=(led.get("containers") or {}).get(cid)
    if not c: raise SystemExit(f"{label} missing container")
    if c.get("current_node")!=nb: raise SystemExit(f"{label} current_node={c.get('current_node')}")
    if c.get("desired_state")!="Running": raise SystemExit(f"{label} desired={c.get('desired_state')}")
    migs=led.get("migrations") or {}
    ok=False
    for m in migs.values():
        if m.get("container_id")==cid and m.get("status")=="Success" and m.get("to_node")==nb:
            ok=True
            break
    if not ok: raise SystemExit(f"{label} no Success migration")
print("    migration Success; current_node=B on A and B")
PY
  then OK=1; break; fi
  sleep 0.25
done
if [[ "$OK" != "1" ]]; then
  echo "FAIL: migration ledger state"
  ctl "$API_A" migrations list || true
  ctl "$API_A" ledger || true
  die_logs
fi

echo "==> Assert runtime: gone on A, present on B"
OK=0
for i in $(seq 1 40); do
  OUT_A="$(ctl "$API_A" containers list 2>/dev/null || true)"
  OUT_B="$(ctl "$API_B" containers list 2>/dev/null || true)"
  if python3 - "$OUT_A" "$OUT_B" "$CID" <<'PY'
import json, sys
def ids(raw):
    j=json.loads(raw)
    return {c.get("id") or c.get("ID") for c in (j.get("containers") or [])}
a,b,cid=sys.argv[1],sys.argv[2],sys.argv[3]
ha,hb=ids(a),ids(b)
if cid in ha: raise SystemExit(f"still on A: {ha}")
if cid not in hb: raise SystemExit(f"missing on B: {hb}")
print("    runtime OK (A empty of cid, B has cid)")
PY
  then OK=1; break; fi
  sleep 0.25
done
[[ "$OK" == "1" ]] || { echo "FAIL: runtime"; ctl "$API_A" containers list; ctl "$API_B" containers list; die_logs; }

echo "==> e2e-migration-multinode: PASS"
