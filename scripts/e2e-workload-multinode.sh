#!/usr/bin/env bash
# Two-node CometBFT + mock runtime: workload schedule + reconcile across nodes.
#
# Extends patterns from e2e-cometbft-two-node.sh (genesis-from + pair-after-start)
# and e2e-workload-mock.sh (workloads create/scale + reconciler containers).
#
# Proves Phase 3 placements land on different current_node values and that
# each node's local mock runtime starts only its assigned replicas.
#
# Run from repo root:
#   ./scripts/e2e-workload-multinode.sh
#   make e2e-workload-multinode
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

API_TOKEN="${API_TOKEN:-e2e-wlmn-api}"
JOIN_TOKEN="${JOIN_TOKEN:-e2e-wlmn-join}"
DIR_A="${DIR_A:-/tmp/nexus-wlmn-e2e-a}"
DIR_B="${DIR_B:-/tmp/nexus-wlmn-e2e-b}"
PORT_A="${PORT_A:-18380}"
PORT_B="${PORT_B:-18381}"
CMT_RPC_A="${CMT_RPC_A:-tcp://127.0.0.1:30657}"
CMT_P2P_A="${CMT_P2P_A:-tcp://127.0.0.1:30656}"
CMT_RPC_B="${CMT_RPC_B:-tcp://127.0.0.1:31657}"
CMT_P2P_B="${CMT_P2P_B:-tcp://127.0.0.1:31656}"
API_A="https://127.0.0.1:${PORT_A}"
API_B="https://127.0.0.1:${PORT_B}"
WL_ID="${WL_ID:-mn-demo}"

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
  echo "---- A log (tail) ----"; tail -80 "$LOG_A" || true
  echo "---- B log (tail) ----"; tail -80 "$LOG_B" || true
  exit 1
}

echo "==> Building"
mkdir -p bin
( cd coordination && go build -o ../bin/coordinator ./cmd/coordinator )
( cd coordination && go build -o ../bin/nexusctl ./cmd/nexusctl )

rm -rf "$DIR_A" "$DIR_B"
mkdir -p "$DIR_A" "$DIR_B"

# Short sync-interval so Online node heartbeats propagate before scheduling.
# (Nodes/liveness are HTTP-sync, not AppHash — see docs/phase3-orchestration.md.)
echo "==> Start A with --cometbft (mock + short sync)"
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
  if ! kill -0 "$PID_B" 2>/dev/null; then
    echo "B died:"; cat "$LOG_B"; exit 1
  fi
  sleep 0.25
done

echo "==> Pair A <-> B (JoinMember via consensus)"
ctl "$API_A" pair "$API_B"

echo "==> Waiting for ledger members on both nodes"
for i in $(seq 1 60); do
  if python3 - <<PY
import json, pathlib
for label, p in [("A", "$DIR_A"), ("B", "$DIR_B")]:
    led=json.loads(pathlib.Path(p+"/ledger.json").read_text())
    members=led.get("members") or {}
    if len(members) < 2:
        raise SystemExit(f"{label} ledger members want >=2 got {len(members)}")
print("    ledger members OK")
PY
  then
    break
  fi
  if [[ "$i" -eq 60 ]]; then
    echo "FAIL: ledger members did not converge after pair"
    die_logs
  fi
  sleep 0.25
done

echo "==> Settling blocks for validator sync"
sleep 4

echo "==> Sync heartbeats so both nodes are Online before schedule"
ctl "$API_A" sync >/dev/null || true
ctl "$API_B" sync >/dev/null || true

for i in $(seq 1 60); do
  if python3 - <<PY
import json, pathlib
for label, p in [("A", "$DIR_A"), ("B", "$DIR_B")]:
    led=json.loads(pathlib.Path(p+"/ledger.json").read_text())
    nodes=led.get("nodes") or {}
    online=[nid for nid,n in nodes.items() if (n.get("status") or "") == "Online"]
    if len(online) < 2:
        raise SystemExit(f"{label} online nodes want >=2 got {online}")
print("    online nodes OK on A and B")
PY
  then
    break
  fi
  if [[ "$i" -eq 60 ]]; then
    echo "FAIL: both nodes not Online on ledgers (scheduler needs >=2 online)"
    ctl "$API_A" nodes || true
    ctl "$API_B" nodes || true
    die_logs
  fi
  ctl "$API_A" sync >/dev/null 2>&1 || true
  sleep 0.5
done

NODE_A="$(ctl "$API_A" node | python3 -c "import json,sys; print(json.load(sys.stdin)[\"node_id\"])")"
NODE_B="$(ctl "$API_B" node | python3 -c "import json,sys; print(json.load(sys.stdin)[\"node_id\"])")"
echo "    NODE_A=$NODE_A"
echo "    NODE_B=$NODE_B"

echo "==> Create workload replicas=2 (force cross-node least-loaded placement)"
ctl "$API_A" workloads create "$WL_ID" --image nginx:wlmn --replicas 2

echo "==> Wait for ledger placements on different current_node"
OK=0
for i in $(seq 1 60); do
  if python3 - "$DIR_A" "$WL_ID" "$NODE_A" "$NODE_B" <<'PY'
import json, pathlib, sys
dir_a, wl, na, nb = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
led = json.loads(pathlib.Path(dir_a + "/ledger.json").read_text())
ctr = led.get("containers") or {}
c0 = ctr.get(f"wl:{wl}:0")
c1 = ctr.get(f"wl:{wl}:1")
if not c0 or not c1:
    raise SystemExit("missing replica containers")
n0, n1 = c0.get("current_node"), c1.get("current_node")
if not n0 or not n1:
    raise SystemExit("empty current_node")
if n0 == n1:
    raise SystemExit(f"same node for both replicas: {n0}")
nodes = {n0, n1}
if nodes != {na, nb}:
    raise SystemExit(f"unexpected nodes {nodes} want {{{na},{nb}}}")
print(f"    placements OK wl:{wl}:0@{n0} wl:{wl}:1@{n1}")
PY
  then
    OK=1
    break
  fi
  sleep 0.5
done
if [[ "$OK" != "1" ]]; then
  echo "FAIL: cross-node placements did not appear"
  ctl "$API_A" ledger || true
  ctl "$API_A" workloads get "$WL_ID" || true
  die_logs
fi

echo "==> Assert local runtime containers on both nodes"
OK=0
for i in $(seq 1 60); do
  OUT_A="$(ctl "$API_A" containers list 2>/dev/null || true)"
  OUT_B="$(ctl "$API_B" containers list 2>/dev/null || true)"
  if python3 - "$OUT_A" "$OUT_B" "$WL_ID" "$NODE_A" "$NODE_B" "$DIR_A" <<'PY'
import json, pathlib, sys
out_a, out_b, wl, na, nb, dir_a = sys.argv[1:7]
led = json.loads(pathlib.Path(dir_a + "/ledger.json").read_text())
ctr = led.get("containers") or {}
want_a = {cid for cid, c in ctr.items()
          if c.get("workload_id") == wl and c.get("current_node") == na}
want_b = {cid for cid, c in ctr.items()
          if c.get("workload_id") == wl and c.get("current_node") == nb}
if len(want_a) < 1 or len(want_b) < 1:
    raise SystemExit(f"ledger assign A={want_a} B={want_b}")

def ids(raw):
    try:
        j = json.loads(raw)
    except Exception as e:
        raise SystemExit(f"bad containers json: {e}")
    return {c.get("id") or c.get("ID") or "" for c in (j.get("containers") or [])}

have_a, have_b = ids(out_a), ids(out_b)
if not want_a.issubset(have_a):
    raise SystemExit(f"A runtime missing {want_a - have_a}; have={have_a}")
if not want_b.issubset(have_b):
    raise SystemExit(f"B runtime missing {want_b - have_b}; have={have_b}")
# Neither node should run the peer's assigned workload replica.
if want_b & have_a:
    raise SystemExit(f"A incorrectly runs B placements {want_b & have_a}")
if want_a & have_b:
    raise SystemExit(f"B incorrectly runs A placements {want_a & have_b}")
print(f"    runtime OK A={sorted(want_a)} B={sorted(want_b)}")
PY
  then
    OK=1
    break
  fi
  sleep 0.5
done
if [[ "$OK" != "1" ]]; then
  echo "FAIL: local runtime containers did not converge on both nodes"
  echo "A containers:"; ctl "$API_A" containers list || true
  echo "B containers:"; ctl "$API_B" containers list || true
  die_logs
fi

echo "==> Scale up to 3 and re-assert third placement"
ctl "$API_A" workloads scale "$WL_ID" 3
OK=0
for i in $(seq 1 60); do
  if python3 - "$DIR_A" "$WL_ID" <<'PY'
import json, pathlib, sys
dir_a, wl = sys.argv[1], sys.argv[2]
led = json.loads(pathlib.Path(dir_a + "/ledger.json").read_text())
ctr = led.get("containers") or {}
nodes = set()
for idx in (0, 1, 2):
    c = ctr.get(f"wl:{wl}:{idx}")
    if not c or not c.get("current_node"):
        raise SystemExit(f"missing wl:{wl}:{idx}")
    nodes.add(c["current_node"])
if len(nodes) < 2:
    raise SystemExit(f"scale-up still single-node: {nodes}")
print(f"    scale-up OK replicas=3 across {len(nodes)} nodes")
PY
  then
    OK=1
    break
  fi
  sleep 0.5
done
if [[ "$OK" != "1" ]]; then
  echo "FAIL: scale-up placements"
  ctl "$API_A" ledger || true
  die_logs
fi

echo "==> Scale down to 1; orphan replica removed from ledger"
ctl "$API_A" workloads scale "$WL_ID" 1
OK=0
for i in $(seq 1 60); do
  if python3 - "$DIR_A" "$WL_ID" <<'PY'
import json, pathlib, sys
dir_a, wl = sys.argv[1], sys.argv[2]
led = json.loads(pathlib.Path(dir_a + "/ledger.json").read_text())
ctr = led.get("containers") or {}
wl_ctrs = {k: v for k, v in ctr.items() if v.get("workload_id") == wl}
if len(wl_ctrs) != 1:
    raise SystemExit(f"want 1 workload container, got {list(wl_ctrs)}")
if f"wl:{wl}:0" not in wl_ctrs:
    raise SystemExit(f"unexpected remaining: {list(wl_ctrs)}")
if f"wl:{wl}:1" in ctr or f"wl:{wl}:2" in ctr:
    raise SystemExit("orphan replicas still present")
print("    scale-down OK (only wl:%s:0 remains)" % wl)
PY
  then
    OK=1
    break
  fi
  sleep 0.5
done
if [[ "$OK" != "1" ]]; then
  echo "FAIL: scale-down orphan cleanup"
  ctl "$API_A" ledger || true
  die_logs
fi

echo "==> Delete workload"
ctl "$API_A" workloads delete "$WL_ID"

echo "==> e2e-workload-multinode: PASS"
