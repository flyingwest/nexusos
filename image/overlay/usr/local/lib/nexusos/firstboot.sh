#!/usr/bin/env bash
# Production first-boot gate: never invent secrets; instruct or start coordinator.
set -euo pipefail

ETC_DIR="${NEXUS_ETC_DIR:-/etc/nexusos}"
ENV_FILE="${ETC_DIR}/coordinator.env"
TLS_CRT="${ETC_DIR}/tls.crt"
TLS_KEY="${ETC_DIR}/tls.key"

msg() {
  printf '%s\n' "$*"
  printf '%s\n' "$*" > /dev/console 2>/dev/null || true
  logger -t nexusos-firstboot "$*" 2>/dev/null || true
}

is_placeholder() {
  local v="${1:-}"
  [[ -z "$v" ]] && return 0
  case "$v" in
    CHANGE_ME|CHANGE_ME*) return 0 ;;
  esac
  return 1
}

configured=1
if [[ ! -f "$ENV_FILE" ]]; then
  configured=0
elif [[ ! -f "$TLS_CRT" || ! -f "$TLS_KEY" ]]; then
  configured=0
else
  # shellcheck disable=SC1090
  set -a
  # shellcheck source=/dev/null
  . "$ENV_FILE"
  set +a
  if is_placeholder "${NEXUS_API_TOKEN:-}" || is_placeholder "${NEXUS_JOIN_TOKEN:-}"; then
    configured=0
  fi
fi

if [[ "$configured" -ne 1 ]]; then
  msg "NEXUSOS_FIRSTBOOT: coordinator not provisioned (fail-closed)."
  msg "NEXUSOS_FIRSTBOOT: run as root:"
  msg "  nexusos-provision --api-token <TOKEN> --join-token <TOKEN> --advertise https://<this-host>:8080 --start"
  msg "NEXUSOS_FIRSTBOOT: see /etc/nexusos/README and docs/node-image.md"
  # Exit 0 so the oneshot stays green; coordinator remains inactive via Conditions / not enabled.
  exit 0
fi

msg "NEXUSOS_FIRSTBOOT: tokens+TLS present; ensuring permissions and starting coordinator"
chmod 0600 "$ENV_FILE" "$TLS_KEY" 2>/dev/null || true
chmod 0644 "$TLS_CRT" 2>/dev/null || true

if command -v systemctl >/dev/null 2>&1; then
  systemctl enable nexusos-coordinator.service 2>/dev/null || true
  systemctl start nexusos-coordinator.service || {
    msg "NEXUSOS_FIRSTBOOT: failed to start nexusos-coordinator (check journalctl -u nexusos-coordinator)"
    exit 0
  }
  msg "NEXUSOS_FIRSTBOOT: nexusos-coordinator started"
else
  msg "NEXUSOS_FIRSTBOOT: systemctl missing; start coordinator manually"
fi
exit 0
