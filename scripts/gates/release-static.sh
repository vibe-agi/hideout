#!/usr/bin/env bash
set -euo pipefail

# One source of truth for every deterministic release-static check. Gate 0,
# the signed-candidate preflight, and the local release aggregate all call this
# script so a cheap failure cannot appear for the first time after signing.
export GOFLAGS=-mod=readonly

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$root"

inventory="scripts/gates/release-candidate-inventory.json"
mode="full"

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/release-static.sh [--preflight|--self-test]" \
    "" \
    "Runs the shared release-static contract. --preflight skips only Go" \
    "build/vet; formatting, module tidiness, shell syntax/lint, complete" \
    "Markdown inventory, acceptance-set parity, and diff checks still run."
}

case "${1:-}" in
  "") ;;
  --preflight)
    [ "$#" -eq 1 ] || { usage >&2; exit 2; }
    mode="preflight"
    ;;
  --self-test)
    [ "$#" -eq 1 ] || { usage >&2; exit 2; }
    mode="self-test"
    ;;
  -h | --help)
    [ "$#" -eq 1 ] || { usage >&2; exit 2; }
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

for command in \
  awk bash cmp comm find git go gofmt grep jq markdownlint-cli2 sed shellcheck \
  sort tr wc xargs; do
  command -v "$command" >/dev/null 2>&1 || {
    printf 'release-static: missing required command: %s\n' "$command" >&2
    exit 1
  }
done

[ -f "$inventory" ] && [ ! -L "$inventory" ] || {
  printf 'release-static: inventory is missing or unsafe: %s\n' \
    "$inventory" >&2
  exit 1
}
jq -e '
  .schema == "hideout.local-release-candidate-inventory/v1" and
  (.shellLint | type == "array" and length > 0 and
    all(.[]; type == "string" and length > 0) and
    (unique | length) == length) and
  (.markdownLint | type == "array" and length > 0 and
    all(.[]; type == "string" and length > 0) and
    (unique | length) == length)
' "$inventory" >/dev/null || {
  printf 'release-static: lint inventory shape is invalid\n' >&2
  exit 1
}

scratch="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-static.XXXXXX")"
cleanup() {
  case "$scratch" in
    "${TMPDIR:-/tmp}"/hideout-release-static.*)
      rm -rf -- "$scratch"
      ;;
    *)
      printf 'release-static: refusing unsafe temporary cleanup: %s\n' \
        "$scratch" >&2
      return 1
      ;;
  esac
}
trap cleanup EXIT

check_module_tidy() {
  local diff_path="$1" diagnostics_path="$2" status=0
  if go mod tidy -diff >"$diff_path" 2>"$diagnostics_path"; then
    status=0
  else
    status=$?
  fi
  if [ "$status" -ne 0 ]; then
    printf 'release-static: go mod tidy -diff failed:\n' >&2
    if [ -s "$diagnostics_path" ]; then
      sed 's/^/  /' "$diagnostics_path" >&2
    fi
    if [ -s "$diff_path" ]; then
      sed 's/^/  /' "$diff_path" >&2
    fi
    return 1
  fi
  if [ -s "$diff_path" ]; then
    printf 'release-static: go.mod/go.sum are not tidy:\n' >&2
    sed 's/^/  /' "$diff_path" >&2
    return 1
  fi
}

module_tidy_self_test() {
  local scenario="diagnostics-only"
  go() {
    [ "$#" -eq 3 ] && [ "$1" = "mod" ] && [ "$2" = "tidy" ] &&
      [ "$3" = "-diff" ] || return 97
    case "$scenario" in
      diagnostics-only)
        printf 'go: downloading example.invalid/module v1.0.0\n' >&2
        ;;
      module-diff)
        printf 'diff --git a/go.mod b/go.mod\n+module drift\n'
        ;;
      command-failure)
        printf 'go: proxy unavailable\n' >&2
        return 1
        ;;
      *)
        return 98
        ;;
    esac
  }

  check_module_tidy \
    "$scratch/self-test.diff" "$scratch/self-test.diagnostics" || {
    printf 'release-static: tidy self-test rejected diagnostics-only success\n' >&2
    return 1
  }
  scenario="module-diff"
  if check_module_tidy \
    "$scratch/self-test.diff" "$scratch/self-test.diagnostics" \
    >"$scratch/self-test.stdout" 2>"$scratch/self-test.stderr"; then
    printf 'release-static: tidy self-test accepted a module diff\n' >&2
    return 1
  fi
  grep -Fq '+module drift' "$scratch/self-test.stderr" || {
    printf 'release-static: tidy self-test lost module diff diagnostics\n' >&2
    return 1
  }
  scenario="command-failure"
  if check_module_tidy \
    "$scratch/self-test.diff" "$scratch/self-test.diagnostics" \
    >"$scratch/self-test.stdout" 2>"$scratch/self-test.stderr"; then
    printf 'release-static: tidy self-test accepted a command failure\n' >&2
    return 1
  fi
  grep -Fq 'go: proxy unavailable' "$scratch/self-test.stderr" || {
    printf 'release-static: tidy self-test lost command diagnostics\n' >&2
    return 1
  }
  unset -f go
}

module_tidy_self_test
if [ "$mode" = "self-test" ]; then
  printf 'release-static: module-tidy self-test passed vmBoots=0\n'
  exit 0
fi

if [ "$mode" = "full" ]; then
  go build ./...
  go vet ./...
fi

find cmd internal schemas test tools -type f -name '*.go' -print0 |
  xargs -0 gofmt -l |
  LC_ALL=C sort >"$scratch/unformatted-go"
if [ -s "$scratch/unformatted-go" ]; then
  printf 'release-static: gofmt required for:\n' >&2
  sed 's/^/  /' "$scratch/unformatted-go" >&2
  exit 1
fi

check_module_tidy "$scratch/tidy.diff" "$scratch/tidy.diagnostics"

while IFS= read -r script; do
  bash -n "$script"
done < <(find scripts -type f -name '*.sh' | LC_ALL=C sort)

safe_inventory_path() {
  local path="$1" label="$2"
  case "$path" in
    "" | /* | . | .. | ../* | */.. | */../* | *$'\n'* | *$'\r'*)
      printf 'release-static: %s path is unsafe: %s\n' "$label" "$path" >&2
      return 1
      ;;
  esac
  [ -f "$path" ] && [ ! -L "$path" ] || {
    printf 'release-static: %s path is missing or unsafe: %s\n' \
      "$label" "$path" >&2
    return 1
  }
}

declare -a shell_files=()
while IFS= read -r path; do
  safe_inventory_path "$path" "shell lint"
  shell_files+=("$path")
done < <(jq -er '.shellLint[]' "$inventory")
shellcheck -x "${shell_files[@]}"

declare -a markdown_files=()
while IFS= read -r path; do
  safe_inventory_path "$path" "Markdown lint"
  markdown_files+=("$path")
done < <(jq -er '.markdownLint[]' "$inventory")
markdownlint-cli2 "${markdown_files[@]}"

sed -E -n \
  's/^- \*\*(FR-[0-9]+a?)\*\*:.*/\1/p' \
  specs/045-operator-observability-console/spec.md |
  LC_ALL=C sort >"$scratch/spec-functional-requirements"
sed -E -n \
  's/^\| (FR-[0-9]+a?) \|.*/\1/p' \
  specs/045-operator-observability-console/checklists/acceptance.md |
  LC_ALL=C sort >"$scratch/accepted-functional-requirements"
sed -E -n \
  's/^- \*\*(SC-[0-9]+)\*\*:.*/\1/p' \
  specs/045-operator-observability-console/spec.md |
  LC_ALL=C sort >"$scratch/spec-success-criteria"
sed -E -n \
  's/^\| (SC-[0-9]+) \|.*/\1/p' \
  specs/045-operator-observability-console/checklists/acceptance.md |
  LC_ALL=C sort >"$scratch/accepted-success-criteria"
if [ "$(wc -l <"$scratch/spec-functional-requirements" | tr -d ' ')" -ne 72 ] ||
  [ "$(wc -l <"$scratch/spec-success-criteria" | tr -d ' ')" -ne 15 ] ||
  ! cmp -s \
    "$scratch/spec-functional-requirements" \
    "$scratch/accepted-functional-requirements" ||
  ! cmp -s \
    "$scratch/spec-success-criteria" \
    "$scratch/accepted-success-criteria"; then
  printf 'release-static: Feature 045 acceptance identifiers drifted\n' >&2
  comm -3 \
    "$scratch/spec-functional-requirements" \
    "$scratch/accepted-functional-requirements" >&2 || true
  comm -3 \
    "$scratch/spec-success-criteria" \
    "$scratch/accepted-success-criteria" >&2 || true
  exit 1
fi

git diff --check
printf 'release-static: passed mode=%s shellFiles=%s markdownFiles=%s vmBoots=0\n' \
  "$mode" "${#shell_files[@]}" "${#markdown_files[@]}"
