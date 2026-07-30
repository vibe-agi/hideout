#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
manifest="$root/scripts/mutation/045/production-mutations.json"
contracts="$root/scripts/mutation/045/contracts.json"
out="$root/.artifacts/045/local/mutations/production"
claim=""

usage() {
  printf '%s\n' \
    "Usage: scripts/mutation/045/run-production-mutations.sh [--out DIR] [--claim ID]" \
    "" \
    "Runs exact source-overlay production mutants. Each original direct assertion" \
    "must pass and the same assertion must execute and fail under its named mutant." \
    "Compile failures do not count as killed mutants."
}

while (($# > 0)); do
  case "$1" in
    --out)
      [[ $# -ge 2 ]] || {
        usage >&2
        exit 2
      }
      out="$2"
      shift 2
      ;;
    --claim)
      [[ $# -ge 2 ]] || {
        usage >&2
        exit 2
      }
      claim="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "$out" != /* ]]; then
  out="$root/$out"
fi
mkdir -p "$out"
chmod 0700 "$out"

args=(
  run "$root/scripts/mutation/045/runner.go"
  --root "$root"
  --manifest "$manifest"
  --contracts "$contracts"
  --out "$out"
)
if [[ -n "$claim" ]]; then
  args+=(--claim "$claim")
fi

exec go "${args[@]}"
