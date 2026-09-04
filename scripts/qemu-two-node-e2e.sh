#!/usr/bin/env bash
# Two-guest QEMU e2e: boot two --dev-smoke node images, pair A↔B, pull+start on A,
# sync, assert ledger on B (same spirit as scripts/e2e-two-node-mock.sh).
#
# Why advertise rewriting?
#   QEMU user-mode networking isolates each guest. The baked smoke advertise
#   (https://10.0.2.15:8080) collides across guests ("cannot pair with self") and
#   is not reachable from the peer. After /health is up we serial-login (root /
#   nexusos on --dev-smoke images) and set:
#     A → https://10.0.2.2:<PORT_A>   B → https://10.0.2.2:<PORT_B>
#   so each guest reaches the other via the host's hostfwd ports.
#
# Host requirements: qemu-system-x86_64, qemu-img, curl, python3, nexusctl (built).
# Acceleration: TCG by default (reliable). Set SMOKE_KVM=1 to try /dev/kvm.
#
# Usage:
#   ./scripts/qemu-two-node-e2e.sh [base-image.qcow2]
#   E2E_SKIP_WORKLOAD=1 ./scripts/qemu-two-node-e2e.sh   # pair+sync only (no pull)
#   E2E_DRY=1 ./scripts/qemu-two-node-e2e.sh             # prereq + overlay create only
#
# Env knobs (defaults in brackets):
#   PORT_A/PORT_B [18080/18081]  SERIAL_A/SERIAL_B [22080/22081]
#   SMOKE_MEM [1024]  SMOKE_SMP [1]  SMOKE_TIMEOUT [900]  SMOKE_KVM [0]
#   API_TOKEN / JOIN_TOKEN  (smoke defaults)
#   E2E_WORKDIR [/tmp/nexusos-qemu-e2e]  E2E_KEEP [0]
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

BASE_IMG="${1:-$ROOT/dist/node-image/nexusos-node-bookworm-amd64-devsmoke.qcow2}"
WORKDIR="${E2E_WORKDIR:-/tmp/nexusos-qemu-e2e}"
TIMEOUT="${SMOKE_TIMEOUT:-900}"
PORT_A="${PORT_A:-18080}"
PORT_B="${PORT_B:-18081}"
SERIAL_A="${SERIAL_A:-22080}"
SERIAL_B="${SERIAL_B:-22081}"
API_TOKEN="${NEXUS_API_TOKEN:-${API_TOKEN:-smoke-api-token}}"
JOIN_TOKEN="${NEXUS_JOIN_TOKEN:-${JOIN_TOKEN:-smoke-join-token}}"
MEM="${SMOKE_MEM:-1024}"
SMP="${SMOKE_SMP:-1}"
CTL="${ROOT}/bin/nexusctl"
IMG_A="$WORKDIR/node-a.qcow2"
IMG_B="$WORKDIR/node-b.qcow2"
LOG_A="$WORKDIR/serial-a.log"
LOG_B="$WORKDIR/serial-b.log"
PID_A=""
PID_B=""

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1" >&2; exit 1; }; }
need qemu-system-x86_64
need qemu-img
need curl
need python3

if [[ ! -f "$BASE_IMG" ]]; then
  echo "base image not found: $BASE_IMG" >&2
  echo "Build one first: sudo ./image/build.sh --dev-smoke" >&2
  exit 1
fi

case "${BASE_IMG##*.}" in
  qcow2) BASE_FMT=qcow2 ;;
  raw|img) BASE_FMT=raw ;;
  *) BASE_FMT=qcow2 ;;
esac

ACCEL_ARGS=(-machine pc -cpu qemu64)
if [[ "${SMOKE_KVM:-0}" == "1" ]]; then
  if [[ -r /dev/kvm && -w /dev/kvm ]]; then
    ACCEL_ARGS=(-enable-kvm -cpu host)
    echo "==> Using KVM acceleration (SMOKE_KVM=1)"
  else
    echo "==> SMOKE_KVM=1 but /dev/kvm not usable; falling back to TCG" >&2
  fi
else
  echo "==> Using TCG (set SMOKE_KVM=1 to try KVM)"
fi

cleanup() {
  if [[ -n "${PID_A}" ]] && kill -0 "$PID_A" 2>/dev/null; then
    kill "$PID_A" 2>/dev/null || true
    wait "$PID_A" 2>/dev/null || true
  fi
  if [[ -n "${PID_B}" ]] && kill -0 "$PID_B" 2>/dev/null; then
    kill "$PID_B" 2>/dev/null || true
    wait "$PID_B" 2>/dev/null || true
  fi
  if [[ "${E2E_KEEP:-0}" != "1" ]]; then
    rm -f "$IMG_A" "$IMG_B" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "==> Workdir $WORKDIR"
mkdir -p "$WORKDIR" bin
: > "$LOG_A"
: > "$LOG_B"

if [[ ! -x "$CTL" ]]; then
  echo "==> Building nexusctl"
  ( cd coordination && go build -o ../bin/nexusctl ./cmd/nexusctl )
fi

echo "==> Creating COW overlays from $BASE_IMG"
rm -f "$IMG_A" "$IMG_B"
qemu-img create -f qcow2 -F "$BASE_FMT" -b "$(realpath "$BASE_IMG")" "$IMG_A" >/dev/null
qemu-img create -f qcow2 -F "$BASE_FMT" -b "$(realpath "$BASE_IMG")" "$IMG_B" >/dev/null

if [[ "${E2E_DRY:-0}" == "1" ]]; then
  echo "==> E2E_DRY=1: overlays created; skipping QEMU boot"
  echo "    A=$IMG_A"
  echo "    B=$IMG_B"
  echo "==> qemu-two-node-e2e: DRY PASS"
  exit 0
fi

boot_node() {
  local name="$1" img="$2" host_port="$3" serial_port="$4" log="$5"
  echo "==> Booting $name (hostfwd :${host_port}->:8080, serial tcp:${serial_port}, log $log)" >&2
  qemu-system-x86_64 \
    "${ACCEL_ARGS[@]}" \
    -m "$MEM" \
    -smp "$SMP" \
    -drive "file=${img},format=qcow2,if=ide,cache=writeback" \
    -boot order=c \
    -netdev "user,id=net0,hostfwd=tcp:127.0.0.1:${host_port}-:8080" \
    -device virtio-net-pci,netdev=net0 \
    -display none \
    -chardev "socket,id=serial0,host=127.0.0.1,port=${serial_port},server=on,wait=off,logfile=${log},logappend=on" \
    -serial chardev:serial0 \
    -monitor none \
    -no-reboot \
    >/dev/null 2>&1 &
  echo $!
}

PID_A="$(boot_node A "$IMG_A" "$PORT_A" "$SERIAL_A" "$LOG_A")"
PID_B="$(boot_node B "$IMG_B" "$PORT_B" "$SERIAL_B" "$LOG_B")"
echo "    PID_A=$PID_A PID_B=$PID_B"

wait_health() {
  local name="$1" port="$2" pid="$3" log="$4"
  echo "==> Waiting up to ${TIMEOUT}s for $name HTTPS /health on :${port}"
  local deadline=$((SECONDS + TIMEOUT))
  local code
  while (( SECONDS < deadline )); do
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "$name QEMU exited early; serial tail:" >&2
      tail -n 120 "$log" 2>/dev/null || true
      exit 1
    fi
    code=$(curl -sk --connect-timeout 1 -o /dev/null -w '%{http_code}' \
      "https://127.0.0.1:${port}/health" 2>/dev/null || true)
    if [[ "$code" == "200" ]]; then
      echo "==> $name health OK"
      return 0
    fi
    sleep 3
  done
  echo "timeout waiting for $name health; serial tail:" >&2
  tail -n 200 "$log" || true
  exit 1
}

wait_health A "$PORT_A" "$PID_A" "$LOG_A"
wait_health B "$PORT_B" "$PID_B" "$LOG_B"

# Rewrite NEXUS_ADVERTISE so peers can dial via host gateway + hostfwd.
# --dev-smoke images: serial root password "nexusos".
reconfigure_advertise() {
  local name="$1" serial_port="$2" advertise="$3" host_port="$4" pid="$5" log="$6"
  echo "==> Reconfigure $name advertise → $advertise (serial :${serial_port})"
  python3 - "$serial_port" "$advertise" "$API_TOKEN" "$JOIN_TOKEN" <<'PY'
import socket, sys, time, re

port = int(sys.argv[1])
advertise = sys.argv[2]
api_token = sys.argv[3]
join_token = sys.argv[4]

def recv_until(sock, patterns, timeout=120.0):
    buf = ""
    end = time.time() + timeout
    pats = [re.compile(p, re.I | re.M) for p in patterns]
    while time.time() < end:
        sock.settimeout(max(0.2, end - time.time()))
        try:
            chunk = sock.recv(4096)
        except socket.timeout:
            continue
        if not chunk:
            time.sleep(0.2)
            continue
        buf += chunk.decode("utf-8", "replace")
        for p in pats:
            if p.search(buf):
                return buf
    raise TimeoutError("timeout waiting for %s; last=%r" % (patterns, buf[-400:]))

def send(sock, data):
    sock.sendall(data.encode("utf-8"))

sock = socket.create_connection(("127.0.0.1", port), timeout=10)
sock.settimeout(5)
# Nudge getty
for _ in range(3):
    send(sock, "\n")
    time.sleep(0.4)

buf = recv_until(sock, [r"login:", r"Password:", r"[#\$]\s*$"], timeout=180)
if re.search(r"[#\$]\s*$", buf) and not re.search(r"login:", buf[-80:], re.I):
    # already logged in
    pass
else:
    if not re.search(r"login:", buf, re.I):
        send(sock, "\n")
        buf = recv_until(sock, [r"login:"], timeout=120)
    send(sock, "root\n")
    recv_until(sock, [r"Password:"], timeout=60)
    send(sock, "nexusos\n")
    recv_until(sock, [r"[#\$]\s*$"], timeout=60)

env = (
    "NEXUS_API_TOKEN=%s\n"
    "NEXUS_JOIN_TOKEN=%s\n"
    "NEXUS_ADVERTISE=%s\n"
) % (api_token, join_token, advertise)

# Write env + restart; avoid echo of secrets in shell history noise via heredoc.
script = (
    "set -e\n"
    "cat > /etc/nexusos/coordinator.env <<'NEXUSEOF'\n"
    + env +
    "NEXUSEOF\n"
    "chmod 0600 /etc/nexusos/coordinator.env\n"
    "systemctl restart nexusos-coordinator\n"
    "echo NEXUSOS_ADVERTISE_UPDATED\n"
)
send(sock, script)
recv_until(sock, [r"NEXUSOS_ADVERTISE_UPDATED"], timeout=180)
sock.close()
print("serial reconfigure ok")
PY

  echo "==> Waiting for $name health after restart"
  local deadline=$((SECONDS + 180))
  local code
  while (( SECONDS < deadline )); do
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "$name QEMU died during reconfigure; serial tail:" >&2
      tail -n 120 "$log" 2>/dev/null || true
      exit 1
    fi
    code=$(curl -sk --connect-timeout 1 -o /dev/null -w '%{http_code}' \
      "https://127.0.0.1:${host_port}/health" 2>/dev/null || true)
    if [[ "$code" == "200" ]]; then
      echo "==> $name health OK after advertise rewrite"
      return 0
    fi
    sleep 2
  done
  echo "timeout waiting for $name after advertise rewrite; serial tail:" >&2
  tail -n 200 "$log" || true
  exit 1
}

ADV_A="https://10.0.2.2:${PORT_A}"
ADV_B="https://10.0.2.2:${PORT_B}"
reconfigure_advertise A "$SERIAL_A" "$ADV_A" "$PORT_A" "$PID_A" "$LOG_A"
reconfigure_advertise B "$SERIAL_B" "$ADV_B" "$PORT_B" "$PID_B" "$LOG_B"

API_A="https://127.0.0.1:${PORT_A}"
API_B="https://127.0.0.1:${PORT_B}"
# Peer URL that guest A can dial (host gateway → hostfwd → B)
PEER_B_FROM_A="$ADV_B"

ctl() {
  "$CTL" --api "$1" --token "$API_TOKEN" --insecure "${@:2}"
}

echo "==> Health via nexusctl"
ctl "$API_A" health
ctl "$API_B" health
ctl "$API_A" node
ctl "$API_B" node

echo "==> Pair A <-> B (A dials $PEER_B_FROM_A)"
ctl "$API_A" pair "$PEER_B_FROM_A"

if [[ "${E2E_SKIP_WORKLOAD:-0}" == "1" ]]; then
  echo "==> E2E_SKIP_WORKLOAD=1: skipping pull/start; syncing empty-ish ledgers"
  ctl "$API_A" sync || true
  echo "==> Chain status"
  ctl "$API_A" chain || true
  ctl "$API_B" chain || true
  echo "==> qemu-two-node-e2e: PASS (pair-only)"
  exit 0
fi

echo "==> Pull + start on A (containerd inside guest; needs outbound net)"
ctl "$API_A" images pull docker.io/library/nginx:alpine
ctl "$API_A" containers start docker.io/library/nginx:alpine --name web

echo "==> Sync from A"
ctl "$API_A" sync

echo "==> Assert ledger on B"
LEDGER_B="$(ctl "$API_B" ledger)"
echo "$LEDGER_B" | grep -q '"digest"'
if ! echo "$LEDGER_B" | grep -q 'web\|Running\|current_node'; then
  echo "FAIL: unexpected ledger on B:"
  echo "$LEDGER_B"
  exit 1
fi

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

echo "==> qemu-two-node-e2e: PASS"
echo "    A: $API_A  advertise(guest)=$ADV_A"
echo "    B: $API_B  advertise(guest)=$ADV_B"
