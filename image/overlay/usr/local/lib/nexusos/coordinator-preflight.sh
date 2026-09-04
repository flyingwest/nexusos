#!/usr/bin/env bash
# Fail-closed preflight for nexusos-coordinator.service (production).
# Exit 0 only when env + non-placeholder tokens + TLS files are present.
set -euo pipefail

ETC_DIR="${NEXUS_ETC_DIR:-/etc/nexusos}"
ENV_FILE="${ETC_DIR}/coordinator.env"
TLS_CRT="${ETC_DIR}/tls.crt"
TLS_KEY="${ETC_DIR}/tls.key"

is_placeholder() {
  local v="${1:-}"
  [[ -z "$v" ]] && return 0
  case "$v" in
    CHANGE_ME|CHANGE_ME*) return 0 ;;
  esac
  return 1
}

fail() {
  echo "nexusos-coordinator preflight: $*" >&2
  exit 1
}

[[ -f "$ENV_FILE" ]] || fail "missing $ENV_FILE — run: nexusos-provision --api-token … --join-token …"
[[ -f "$TLS_CRT" ]] || fail "missing $TLS_CRT"
[[ -f "$TLS_KEY" ]] || fail "missing $TLS_KEY"

# shellcheck disable=SC1090
set -a
# shellcheck source=/dev/null
. "$ENV_FILE"
set +a

is_placeholder "${NEXUS_API_TOKEN:-}" && fail "NEXUS_API_TOKEN unset or CHANGE_ME* placeholder in $ENV_FILE"
is_placeholder "${NEXUS_JOIN_TOKEN:-}" && fail "NEXUS_JOIN_TOKEN unset or CHANGE_ME* placeholder in $ENV_FILE"

key_mode="$(stat -c '%a' "$TLS_KEY" 2>/dev/null || stat -f '%OLp' "$TLS_KEY" 2>/dev/null || echo "")"
if [[ -n "$key_mode" && "$key_mode" != "600" && "$key_mode" != "0600" ]]; then
  echo "nexusos-coordinator preflight: fixing $TLS_KEY mode ${key_mode} -> 0600" >&2
  chmod 0600 "$TLS_KEY"
fi
chmod 0600 "$ENV_FILE" 2>/dev/null || true

exit 0
