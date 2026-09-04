#!/bin/bash
# Emit a clear serial marker once the coordinator answers /health (best-effort).
set -euo pipefail
TOKEN="${NEXUS_API_TOKEN:-}"
if [[ -f /etc/nexusos/coordinator.env ]]; then
  # shellcheck disable=SC1091
  set -a
  # shellcheck source=/dev/null
  . /etc/nexusos/coordinator.env
  set +a
  TOKEN="${NEXUS_API_TOKEN:-$TOKEN}"
fi

msg() { printf '%s\n' "$*" > /dev/console 2>/dev/null || printf '%s\n' "$*"; }

msg "NEXUSOS_BOOT: waiting for coordinator"
for i in $(seq 1 60); do
  if [[ -n "$TOKEN" ]]; then
    code=$(curl -sk -o /dev/null -w '%{http_code}' -H "Authorization: Bearer ${TOKEN}" https://127.0.0.1:8080/health 2>/dev/null || true)
  else
    code=$(curl -sk -o /dev/null -w '%{http_code}' https://127.0.0.1:8080/health 2>/dev/null || true)
  fi
  if [[ "$code" == "200" ]]; then
    msg "NEXUSOS_READY"
    exit 0
  fi
  sleep 1
done
msg "NEXUSOS_READY_TIMEOUT"
exit 0
