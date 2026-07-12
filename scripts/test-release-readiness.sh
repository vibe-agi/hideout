#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-release-readiness.sh --local-fast [--out <path>]
  scripts/test-release-readiness.sh --release-candidate \
    --package-root <path> --product-evidence <path> [--product-evidence <path>...] \
    [--runtime-family <id>] [--out <path>]

Local-fast evidence is useful for development but is not release readiness.
Release-candidate mode requires real Gate 2 and Gate 3 evidence through:
  HIDEOUT_GATE2_EVIDENCE
  HIDEOUT_GATE3_EVIDENCE

Release-candidate defaults to runtime family developer-standard. Package and
product evidence are explicit trusted inputs; the script never discovers an
arbitrary nearby artifact or silently omits the product-evidence spine.
USAGE
}

mode=""
out=""
runtime_family="${HIDEOUT_RELEASE_RUNTIME_FAMILY:-}"
package_root="${HIDEOUT_RELEASE_PACKAGE_ROOT:-}"
product_evidence=()
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
    --runtime-family)
      runtime_family="${2:-}"
      [ -n "$runtime_family" ] || { echo "release-readiness: --runtime-family requires an id" >&2; exit 2; }
      shift 2
      ;;
    --package-root)
      package_root="${2:-}"
      [ -n "$package_root" ] || { echo "release-readiness: --package-root requires a path" >&2; exit 2; }
      shift 2
      ;;
    --product-evidence)
      evidence_path="${2:-}"
      [ -n "$evidence_path" ] || { echo "release-readiness: --product-evidence requires a path" >&2; exit 2; }
      product_evidence+=("$evidence_path")
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
  out_dir="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-readiness.XXXXXX")"
  out="$out_dir/readiness.json"
fi

if [ "$mode" = "release-candidate" ]; then
  runtime_family="${runtime_family:-developer-standard}"
  [ -n "${HIDEOUT_GATE2_EVIDENCE:-}" ] || { echo "release-readiness: release-candidate requires HIDEOUT_GATE2_EVIDENCE" >&2; exit 2; }
  [ -n "${HIDEOUT_GATE3_EVIDENCE:-}" ] || { echo "release-readiness: release-candidate requires HIDEOUT_GATE3_EVIDENCE" >&2; exit 2; }
  [ -n "$runtime_family" ] || { echo "release-readiness: release-candidate requires --runtime-family" >&2; exit 2; }
  [ -n "$package_root" ] || { echo "release-readiness: release-candidate requires --package-root" >&2; exit 2; }
  [ "${#product_evidence[@]}" -gt 0 ] || { echo "release-readiness: release-candidate requires at least one --product-evidence" >&2; exit 2; }
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

if ! run_local_fast_checks; then
  local_status="failed"
fi

readiness_args=(
  support readiness
  --mode "$mode"
  --out "$out"
  --commit "$commit"
  --local-status "$local_status"
  --gate2-evidence "${HIDEOUT_GATE2_EVIDENCE:-}"
  --gate3-evidence "${HIDEOUT_GATE3_EVIDENCE:-}"
)
if [ -n "$runtime_family" ]; then
  readiness_args+=(--runtime-family "$runtime_family")
fi
if [ -n "$package_root" ]; then
  readiness_args+=(--package-root "$package_root")
fi
for evidence_path in "${product_evidence[@]}"; do
  readiness_args+=(--product-evidence "$evidence_path")
done

set +e
go run ./cmd/hideout "${readiness_args[@]}"
status=$?
set -e

go run ./cmd/hideout-schema-validate schemas/release-readiness.schema.json "$out" >/dev/null
echo "release-readiness: artifact $out"

exit "$status"
