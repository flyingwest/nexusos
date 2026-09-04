#!/usr/bin/env bash
# Unit-test nexusos-provision + preflight against a temp etc dir (no root required).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROVISION="${ROOT}/image/overlay/usr/local/sbin/nexusos-provision"
PREFLIGHT="${ROOT}/image/overlay/usr/local/lib/nexusos/coordinator-preflight.sh"
FIRSTBOOT="${ROOT}/image/overlay/usr/local/lib/nexusos/firstboot.sh"

need() { [[ -x "$1" ]] || { echo "missing executable: $1" >&2; exit 1; }; }
need "$PROVISION"
need "$PREFLIGHT"
need "$FIRSTBOOT"
command -v openssl >/dev/null 2>&1 || { echo "openssl required" >&2; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
ETC="$TMP/etc/nexusos"
install -d -m 0755 "$ETC"
cp "$ROOT/image/overlay/etc/nexusos/coordinator.env.example" "$ETC/coordinator.env.example"

pass=0
fail=0
check() {
  local name="$1"
  shift
  if "$@"; then
    echo "PASS: $name"
    pass=$((pass + 1))
  else
    echo "FAIL: $name" >&2
    fail=$((fail + 1))
  fi
}

# 1) refuses missing tokens in non-interactive mode
check "refuse missing tokens" bash -c "
  ! NEXUS_ETC_DIR='$ETC' '$PROVISION' --etc-dir '$ETC' --yes 2>/dev/null
"

# 2) refuses CHANGE_ME placeholders
check "refuse CHANGE_ME placeholders" bash -c "
  ! '$PROVISION' --etc-dir '$ETC' --yes --api-token CHANGE_ME_API_TOKEN --join-token real-join 2>/dev/null
"

# 3) writes env + TLS
check "provision writes env+tls" bash -c "
  '$PROVISION' --etc-dir '$ETC' --yes \
    --api-token test-api-token --join-token test-join-token \
    --advertise https://node.example:8080 >/dev/null
  [[ -f '$ETC/coordinator.env' ]] && [[ -f '$ETC/tls.crt' ]] && [[ -f '$ETC/tls.key' ]]
"

# 4) env mode 0600, key mode 0600
check "permissions 0600" bash -c "
  em=\$(stat -c '%a' '$ETC/coordinator.env')
  km=\$(stat -c '%a' '$ETC/tls.key')
  [[ \"\$em\" == '600' && \"\$km\" == '600' ]]
"

# 5) refuse overwrite without --force
check "refuse overwrite without --force" bash -c "
  ! '$PROVISION' --etc-dir '$ETC' --yes \
    --api-token other --join-token other2 --advertise https://node.example:8080 2>/dev/null
"

# 6) --force overwrites
check "force overwrite" bash -c "
  '$PROVISION' --etc-dir '$ETC' --yes --force \
    --api-token new-api --join-token new-join \
    --advertise https://10.0.0.5:8080 >/dev/null
  grep -q 'NEXUS_API_TOKEN=new-api' '$ETC/coordinator.env'
"

# 7) preflight OK when complete
check "preflight ok" bash -c "
  NEXUS_ETC_DIR='$ETC' '$PREFLIGHT'
"

# 8) preflight fails on placeholder
check "preflight rejects placeholder" bash -c "
  cat > '$ETC/bad.env' <<'E'
NEXUS_API_TOKEN=CHANGE_ME_API_TOKEN
NEXUS_JOIN_TOKEN=ok
NEXUS_ADVERTISE=https://x:8080
E
  # temporarily swap
  mv '$ETC/coordinator.env' '$ETC/coordinator.env.good'
  mv '$ETC/bad.env' '$ETC/coordinator.env'
  ! NEXUS_ETC_DIR='$ETC' '$PREFLIGHT' 2>/dev/null
  mv '$ETC/coordinator.env.good' '$ETC/coordinator.env'
"

# 9) firstboot reports configured path (no systemctl start required to exit 0)
check "firstboot configured path" bash -c "
  out=\$(NEXUS_ETC_DIR='$ETC' '$FIRSTBOOT' 2>&1 || true)
  echo \"\$out\" | grep -q 'tokens+TLS present'
"

# 10) firstboot unconfigured instructions
check "firstboot unconfigured instructions" bash -c "
  empty=\$(mktemp -d)
  out=\$(NEXUS_ETC_DIR=\"\$empty\" '$FIRSTBOOT' 2>&1 || true)
  echo \"\$out\" | grep -q 'NEXUSOS_FIRSTBOOT: coordinator not provisioned'
  echo \"\$out\" | grep -q 'nexusos-provision'
  rm -rf \"\$empty\"
"

# 11) cert has expected CN/SAN host
check "tls CN/SAN" bash -c "
  openssl x509 -in '$ETC/tls.crt' -noout -text | grep -q '10.0.0.5'
"

echo
echo "Results: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
