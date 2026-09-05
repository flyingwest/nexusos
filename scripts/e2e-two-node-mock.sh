#!/usr/bin/env bash
# Compatibility wrapper: mock two-node e2e is CometBFT-only.
# Delegates to scripts/e2e-cometbft-two-node.sh (shared genesis + pair-after-start).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
echo "==> e2e-two-node-mock: delegating to e2e-cometbft-two-node.sh (hash-chain removed)"
exec "$ROOT/scripts/e2e-cometbft-two-node.sh" "$@"
