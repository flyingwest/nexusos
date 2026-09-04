#!/usr/bin/env bash
# Boot a NexusOS node qcow2/raw image under QEMU and verify coordinator /health.
#
# Host requirements: qemu-system-x86_64.
# Acceleration: TCG by default (reliable). Set SMOKE_KVM=1 to try /dev/kvm.
# Usage:
#   ./scripts/qemu-node-smoke.sh [path-to-image.qcow2]
#   SMOKE_TIMEOUT=600 ./scripts/qemu-node-smoke.sh dist/node-image/nexusos-node-bookworm-amd64-devsmoke.qcow2
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMG="${1:-$ROOT/dist/node-image/nexusos-node-bookworm-amd64-devsmoke.qcow2}"
TIMEOUT="${SMOKE_TIMEOUT:-600}"
HOST_PORT="${SMOKE_HOST_PORT:-18080}"
API_TOKEN="${NEXUS_API_TOKEN:-smoke-api-token}"
LOG="${SMOKE_LOG:-/tmp/nexusos-qemu-serial.log}"
QEMU_PID=""

if [[ ! -f "$IMG" ]]; then
  echo "image not found: $IMG" >&2
  echo "Build one first: sudo ./image/build.sh --dev-smoke" >&2
  exit 1
fi

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1" >&2; exit 1; }; }
need qemu-system-x86_64
need curl

case "${IMG##*.}" in
  qcow2) FORMAT=qcow2 ;;
  raw|img) FORMAT=raw ;;
  *) FORMAT=qcow2 ;;
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
  if [[ -n "${QEMU_PID}" ]] && kill -0 "$QEMU_PID" 2>/dev/null; then
    kill "$QEMU_PID" 2>/dev/null || true
    wait "$QEMU_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

: > "$LOG"
echo "==> Booting $IMG (serial log: $LOG, hostfwd :${HOST_PORT} -> :8080)"

qemu-system-x86_64 \
  "${ACCEL_ARGS[@]}" \
  -m "${SMOKE_MEM:-2048}" \
  -smp "${SMOKE_SMP:-2}" \
  -drive "file=${IMG},format=${FORMAT},if=ide,cache=writeback" \
  -boot order=c \
  -netdev "user,id=net0,hostfwd=tcp:127.0.0.1:${HOST_PORT}-:8080" \
  -device virtio-net-pci,netdev=net0 \
  -display none \
  -serial "file:${LOG}" \
  -monitor none \
  -no-reboot \
  &
QEMU_PID=$!

echo "==> Waiting up to ${TIMEOUT}s for HTTPS /health"
deadline=$((SECONDS + TIMEOUT))
ok=0
while (( SECONDS < deadline )); do
  if ! kill -0 "$QEMU_PID" 2>/dev/null; then
    echo "QEMU exited early; last serial lines:" >&2
    tail -n 120 "$LOG" 2>/dev/null || true
    exit 1
  fi
  code=$(curl -sk --connect-timeout 1 -o /dev/null -w '%{http_code}' \
    "https://127.0.0.1:${HOST_PORT}/health" 2>/dev/null || true)
  if [[ "$code" == "200" ]]; then
    ok=1
    break
  fi
  sleep 3
done

if [[ "$ok" -ne 1 ]]; then
  echo "timeout waiting for coordinator health; serial tail:" >&2
  tail -n 200 "$LOG" || true
  exit 1
fi

echo "==> Health OK; checking authenticated node endpoint"
code=$(curl -sk -o /tmp/nexusos-smoke-node.json -w '%{http_code}' \
  -H "Authorization: Bearer ${API_TOKEN}" \
  "https://127.0.0.1:${HOST_PORT}/v1/node" || true)
if [[ "$code" != "200" ]]; then
  echo "FAIL: /v1/node returned HTTP $code" >&2
  cat /tmp/nexusos-smoke-node.json 2>/dev/null || true
  exit 1
fi

if grep -q 'NEXUSOS_READY' "$LOG" 2>/dev/null; then
  echo "==> Serial marker NEXUSOS_READY observed"
else
  echo "==> Serial marker not seen yet; health check is authoritative"
fi

echo "==> qemu-node-smoke: PASS"
echo "    API: https://127.0.0.1:${HOST_PORT} (token=${API_TOKEN})"
echo "    nexusctl example:"
echo "      ./bin/nexusctl --api https://127.0.0.1:${HOST_PORT} --token ${API_TOKEN} --insecure health"
