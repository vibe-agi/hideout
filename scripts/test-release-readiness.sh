#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-release-readiness.sh --local-fast [--out <path>]
  scripts/test-release-readiness.sh --release-candidate [--out <path>]

Local-fast evidence is useful for development but is not release readiness.
Release-candidate mode requires real Gate 2 and Gate 3 evidence through:
  HIDEOUT_GATE2_EVIDENCE
  HIDEOUT_GATE3_EVIDENCE
USAGE
}

mode=""
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --local-fast)
      mode="local-fast"
      shift
      ;;
    --release-candidate)
      mode="release-candidate"
      shift
      ;;
    --out)
      out="${2:-}"
      if [ -z "$out" ]; then
        echo "release-readiness: --out requires a path" >&2
        exit 2
      fi
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "release-readiness: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ -z "$mode" ]; then
  mode="local-fast"
fi
if [ -z "$out" ]; then
  out="$(mktemp "${TMPDIR:-/tmp}/hideout-release-readiness.XXXXXX.json")"
fi

commit="$(git rev-parse HEAD 2>/dev/null || printf unknown)"
local_status="passed"

run_local_fast_checks() {
  go build ./... &&
    go vet ./... &&
    test -z "$(gofmt -l internal cmd)" &&
    git diff --check &&
    go test -count=1 ./... &&
    scripts/test-gate0.sh
}

if [ "$mode" = "local-fast" ]; then
  if ! run_local_fast_checks; then
    local_status="failed"
  fi
fi

set +e
go run ./cmd/hideout support readiness \
  --mode "$mode" \
  --out "$out" \
  --commit "$commit" \
  --local-status "$local_status" \
  --gate2-evidence "${HIDEOUT_GATE2_EVIDENCE:-}" \
  --gate3-evidence "${HIDEOUT_GATE3_EVIDENCE:-}"
status=$?
set -e

go run ./cmd/hideout-schema-validate schemas/release-readiness.schema.json "$out" >/dev/null
echo "release-readiness: artifact $out"

exit "$status"
