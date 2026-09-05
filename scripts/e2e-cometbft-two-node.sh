#!/usr/bin/env bash
# Two-node CometBFT e2e (mock runtime).
#
# Authoritative consensus proof (always run):
#   Go test TestTwoNodeConsensusAppliesTxOnPeer — shared membership + P2P,
#   tx on A applied on B via ABCI (not HTTP sync).
#
# Coordinator harness:
#   1) Boot A+B on hash-chain, pair (so both members.json match), stop.
#   2) Restart both with --cometbft, shared genesis, --cometbft-peers.
#   3) Submit image/placement on A; assert B ledger (sync-interval=1h).
# This ordering keeps FinalizeBlock ValidatorUpdates deterministic (same
# membership on every node at every height). Pairing mid-catch-up is unsafe.
#
# Run from repo root:
#   ./scripts/e2e-cometbft-two-node.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

API_TOKEN="${API_TOKEN:-e2e-cmt-api}"
JOIN_TOKEN="${JOIN_TOKEN:-e2e-cmt-join}"
DIR_A="${DIR_A:-/tmp/nexus-cmt-e2e-a}"
DIR_B="${DIR_B:-/tmp/nexus-cmt-e2e-b}"
PORT_A="${PORT_A:-18180}"
PORT_B="${PORT_B:-18181}"
CMT_RPC_A="${CMT_RPC_A:-tcp://127.0.0.1:27657}"
CMT_P2P_A="${CMT_P2P_A:-tcp://127.0.0.1:27656}"
CMT_RPC_B="${CMT_RPC_B:-tcp://127.0.0.1:28657}"
CMT_P2P_B="${CMT_P2P_B:-tcp://127.0.0.1:28656}"
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

echo "==> Building"
mkdir -p bin
( cd coordination && go build -o ../bin/coordinator ./cmd/coordinator )
( cd coordination && go build -o ../bin/nexusctl ./cmd/nexusctl )

echo "==> Go two-node consensus e2e (authoritative)"
( cd coordination && go test ./internal/consensus/cometbft/ -run TestTwoNodeConsensusAppliesTxOnPeer -count=1 -timeout 3m )

rm -rf "$DIR_A" "$DIR_B"
mkdir -p "$DIR_A" "$DIR_B"

echo "==> Phase 1: hash-chain pair so membership matches on A and B"
"$COORD" --dev --mock \
  --listen ":${PORT_A}" \
  --data-dir "$DIR_A" \
  --api-token "$API_TOKEN" \
  --join-token "$JOIN_TOKEN" \
  --advertise "$API_A" \
  --sync-interval 1h \
  --consensus-engine=hashchain \
  >"$LOG_A" 2>&1 &
PID_A=$!
"$COORD" --dev --mock \
  --listen ":${PORT_B}" \
  --data-dir "$DIR_B" \
  --api-token "$API_TOKEN" \
  --join-token "$JOIN_TOKEN" \
  --advertise "$API_B" \
  --sync-interval 1h \
  --consensus-engine=hashchain \
  >"$LOG_B" 2>&1 &
PID_B=$!
wait_health "$API_A" "$PID_A" "$LOG_A"
wait_health "$API_B" "$PID_B" "$LOG_B"
ctl "$API_A" pair "$API_B"
# Confirm both members files have 2 entries
python3 - <<PY
import json, pathlib, sys
for label, p in [("A", "$DIR_A/members.json"), ("B", "$DIR_B/members.json")]:
    m=json.loads(pathlib.Path(p).read_text())
    if len(m) < 2:
        raise SystemExit(f"{label} members want >=2 got {len(m)}: {m}")
    print(f"    {label} members={len(m)}")
PY
kill "$PID_A" "$PID_B" 2>/dev/null || true
wait 2>/dev/null || true
PID_A=""
PID_B=""
# Fresh cometbft dirs; keep identity + members
rm -rf "$DIR_A/cometbft" "$DIR_B/cometbft"
: >"$LOG_A"
: >"$LOG_B"

echo "==> Phase 2: start A with --cometbft"
"$COORD" --dev --mock --cometbft \
  --listen ":${PORT_A}" \
  --data-dir "$DIR_A" \
  --api-token "$API_TOKEN" \
  --join-token "$JOIN_TOKEN" \
  --advertise "$API_A" \
  --sync-interval 1h \
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

NODE_ID_A=""
for i in $(seq 1 40); do
  if [[ -f "$DIR_A/cometbft/p2p-node-id.txt" ]]; then
    NODE_ID_A="$(tr -d '[:space:]' < "$DIR_A/cometbft/p2p-node-id.txt")"
    [[ -n "$NODE_ID_A" ]] && break
  fi
  sleep 0.1
done
[[ -n "$NODE_ID_A" ]] || { echo "missing A p2p node id"; cat "$LOG_A"; exit 1; }
P2P_HOSTPORT_A="$(echo "$CMT_P2P_A" | sed 's#^tcp://##')"
PEER_A="${NODE_ID_A}@${P2P_HOSTPORT_A}"
echo "    A peer=$PEER_A"

echo "==> Phase 2: start B with shared genesis + persistent peer"
mkdir -p "$DIR_B/cometbft/config"
cp "$GEN_A" "$DIR_B/cometbft/config/genesis.json"
"$COORD" --dev --mock --cometbft \
  --listen ":${PORT_B}" \
  --data-dir "$DIR_B" \
  --api-token "$API_TOKEN" \
  --join-token "$JOIN_TOKEN" \
  --advertise "$API_B" \
  --sync-interval 1h \
  --cometbft-rpc "$CMT_RPC_B" \
  --cometbft-p2p "$CMT_P2P_B" \
  --cometbft-peers "$PEER_A" \
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

# Allow validator update (add B) to take effect at height+2 while both vote.
echo "==> Settling blocks for validator sync"
sleep 4

echo "==> Pull + start on A (strict CometBFT Submit)"
ctl "$API_A" images pull docker.io/library/nginx:alpine
ctl "$API_A" containers start docker.io/library/nginx:alpine --name web-cmt

echo "==> Assert ledger on B (sync-interval=1h; expect consensus apply)"
OK=0
for i in $(seq 1 60); do
  LEDGER_B="$(ctl "$API_B" ledger 2>/dev/null || true)"
  if command -v python3 >/dev/null 2>&1; then
    if python3 - "$LEDGER_B" <<'PY'
import json,sys
snap=json.loads(sys.argv[1])
images=snap.get("images") or {}
containers=snap.get("containers") or {}
if not images or not containers:
    raise SystemExit(1)
print("B ledger OK images=%d containers=%d" % (len(images), len(containers)))
PY
    then
      OK=1
      break
    fi
  elif echo "$LEDGER_B" | grep -q 'web-cmt\|digest'; then
    OK=1
    break
  fi
  sleep 0.5
done

if [[ "$OK" != "1" ]]; then
  echo "FAIL: coordinator harness — B ledger did not converge"
  echo "---- A log (tail) ----"; tail -60 "$LOG_A" || true
  echo "---- B log (tail) ----"; tail -60 "$LOG_B" || true
  echo "---- B ledger ----"; ctl "$API_B" ledger || true
  echo "NOTE: Go TestTwoNodeConsensusAppliesTxOnPeer is the authoritative proof; see docs/cometbft-spike.md"
  exit 1
fi

echo "==> Heights (ABCI meta)"
python3 - <<PY
import json, pathlib
for label, p in [("A", "$DIR_A/cometbft/abci-meta.json"), ("B", "$DIR_B/cometbft/abci-meta.json")]:
    path=pathlib.Path(p)
    print(label, json.loads(path.read_text()) if path.exists() else "missing")
PY

echo "==> e2e-cometbft-two-node: PASS (Go test + coordinator harness)"
