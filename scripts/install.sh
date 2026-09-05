#!/usr/bin/env bash
# NexusOS single-node install script (Phase 2 hardened)
set -euo pipefail

PREFIX="${PREFIX:-/usr/local}"
DATA_DIR="${DATA_DIR:-/var/lib/nexusos}"
MODE="${MODE:-mock}"   # mock | containerd

echo "==> NexusOS installer"
echo "    PREFIX=$PREFIX"
echo "    DATA_DIR=$DATA_DIR"
echo "    MODE=$MODE"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "==> Building"
mkdir -p bin
( cd coordination && go build -o ../bin/coordinator ./cmd/coordinator )
( cd coordination && go build -o ../bin/nexusctl ./cmd/nexusctl )

echo "==> Installing binaries to $PREFIX/bin"
install -d "$PREFIX/bin"
install -m 755 bin/coordinator "$PREFIX/bin/nexusos-coordinator"
install -m 755 bin/nexusctl "$PREFIX/bin/nexusctl"

echo "==> Creating data directory $DATA_DIR"
install -d -m 755 "$DATA_DIR"

if [[ -d /etc/systemd/system ]]; then
  echo "==> Installing systemd unit"
  install -m 644 deploy/nexusos-coordinator.service /etc/systemd/system/nexusos-coordinator.service
  if [[ "$MODE" == "mock" ]]; then
    sed -i 's|--mock=false|--mock=true|' /etc/systemd/system/nexusos-coordinator.service
  fi
  systemctl daemon-reload
  echo "    Edit tokens + TLS paths, then: systemctl enable --now nexusos-coordinator"
  echo "    Required: NEXUS_API_TOKEN, NEXUS_JOIN_TOKEN, --tls-cert/--tls-key (see unit file)"
else
  echo "==> No systemd detected; start manually (production needs tokens + TLS):"
  echo "    nexusos-coordinator --mock --listen :8080 --data-dir $DATA_DIR \"
  echo "      --api-token SECRET --join-token CLUSTER --tls-cert CERT --tls-key KEY"
  echo "    Or local experiments: nexusos-coordinator --dev --mock --listen :8080 --data-dir $DATA_DIR"
  echo "    Default consensus engine: cometbft (use --consensus-engine=hashchain only if needed)"
fi

echo "==> Done"
echo "    CLI: nexusctl --api https://127.0.0.1:8080 --token SECRET --insecure health"
echo "    Docs: see README.md and docs/phase2-freeze.md"
