#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

if [ -z "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ]; then
  cat >&2 <<'MSG'
release-dogfood: HIDEOUT_SECRET_DEFAULT_PROXY is required.
Set it to an operator-controlled proxy, for example:
  HIDEOUT_SECRET_DEFAULT_PROXY=socks5://host.lima.internal:<port> scripts/test-release-dogfood.sh
MSG
  exit 2
fi

echo "release-dogfood: running Phase 1 release-candidate gates"
echo "release-dogfood: includes Gate 2 Lima lifecycle, Gate 3 strict proxy, Gate 4 real browser, probes, and generic CLI dogfood"
exec scripts/test-phase1.sh --release-candidate
