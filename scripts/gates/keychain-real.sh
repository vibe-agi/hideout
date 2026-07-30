#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

require_real=0
preflight_only=0

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/keychain-real.sh [--require-real] [--preflight]" \
    "" \
    "Exercises one random, self-cleaning generic-password item through the" \
    "production Security.framework backend. Secret values stay inside the test" \
    "process and are never passed through argv or the environment."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --require-real)
      require_real=1
      shift
      ;;
    --preflight)
      preflight_only=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'keychain-real: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

not_run() {
  if [ "$require_real" -eq 1 ]; then
    printf 'keychain-real: %s\n' "$*" >&2
    exit 1
  fi
  printf 'keychain-real: not-run: %s\n' "$*"
  exit 77
}

command -v go >/dev/null 2>&1 || not_run "missing required command: go"
[ "$(go env GOOS)" = "darwin" ] ||
  not_run "Security.framework gate requires macOS"
[ "$(go env CGO_ENABLED)" = "1" ] ||
  not_run "Security.framework gate requires CGO_ENABLED=1"

if [ "$preflight_only" -eq 1 ]; then
  go test -tags=keychainreal ./internal/secrets -run '^$' -count=1
  printf 'keychain-real: preflight=passed\n'
  exit 0
fi

HIDEOUT_KEYCHAIN_REAL=1 \
  go test -tags=keychainreal ./internal/secrets \
  -run '^TestRealMacOSKeychainSetRotateDeleteAndRestartReconcile$' \
  -count=1 \
  -timeout=2m

printf 'keychain-real: status=passed provider=%s\n' \
  "Security.framework generic-password"
