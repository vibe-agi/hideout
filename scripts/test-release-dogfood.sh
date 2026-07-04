#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-release-dogfood.sh

Environment:
  HIDEOUT_SECRET_DEFAULT_PROXY   Required operator-controlled proxy URL.
  HIDEOUT_RELEASE_EVIDENCE_DIR   Optional exact evidence output directory.
  HIDEOUT_RELEASE_EVIDENCE_ROOT  Optional parent directory for generated evidence.

The evidence bundle stores command, host prerequisite, tool version, and result
metadata. It never records the full proxy URL.
USAGE
}

case "${1:-}" in
  -h|--help)
    usage
    exit 0
    ;;
  "")
    ;;
  *)
    echo "release-dogfood: unknown argument: $1" >&2
    usage >&2
    exit 2
    ;;
esac

if [ -z "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ]; then
  cat >&2 <<'MSG'
release-dogfood: HIDEOUT_SECRET_DEFAULT_PROXY is required.
Set it to an operator-controlled proxy, for example:
  HIDEOUT_SECRET_DEFAULT_PROXY=socks5://host.lima.internal:<port> scripts/test-release-dogfood.sh
MSG
  exit 2
fi

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "release-dogfood: missing required command: $1" >&2
    exit 127
  fi
}

proxy_scheme() {
  case "$HIDEOUT_SECRET_DEFAULT_PROXY" in
    http://*) printf 'http' ;;
    https://*) printf 'https' ;;
    socks5://*) printf 'socks5' ;;
    socks5h://*) printf 'socks5h' ;;
    *) printf 'unknown' ;;
  esac
}

first_line_or_missing() {
  local tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/hideout-release-version.XXXXXX")"
  if "$@" >"$tmp" 2>&1; then
    sed -n '1p' "$tmp"
  else
    printf 'missing'
  fi
  rm -f "$tmp"
}

git_dirty_json() {
  if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    printf 'null'
    return
  fi
  if [ -n "$(git status --porcelain)" ]; then
    printf 'true'
  else
    printf 'false'
  fi
}

redact_stream() {
  perl -pe 'BEGIN { $s = $ENV{"HIDEOUT_SECRET_DEFAULT_PROXY"} // ""; } if ($s ne "") { s/\Q$s\E/<redacted:operator-proxy>/g; }'
}

require_command jq
require_command perl
require_command go

started_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
stamp="$(date -u +"%Y%m%dT%H%M%SZ")"
commit="$(git rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')"
evidence_root="${HIDEOUT_RELEASE_EVIDENCE_ROOT:-$ROOT/.hideout-release-evidence}"
evidence_dir="${HIDEOUT_RELEASE_EVIDENCE_DIR:-$evidence_root/release-dogfood-$stamp-$commit}"
mkdir -p "$evidence_dir"

log_path="$evidence_dir/test-release-dogfood.log"
manifest_path="$evidence_dir/manifest.json"
manifest_schema_path="$ROOT/schemas/release-dogfood.schema.json"
command_text="scripts/test-phase1.sh --release-candidate"
proxy_scheme_value="$(proxy_scheme)"
go_version="$(first_line_or_missing go version)"
limactl_version="$(first_line_or_missing limactl --version)"
jq_version="$(first_line_or_missing jq --version)"
uname_value="$(uname -srm 2>/dev/null || printf 'unknown')"
sw_vers_value="$(sw_vers -productVersion 2>/dev/null || printf '')"
browser_path_provided=false
if [ -n "${HIDEOUT_BROWSER_PATH:-}" ]; then
  browser_path_provided=true
fi
browser_app="${HIDEOUT_BROWSER_APP:-Google Chrome}"
git_dirty="$(git_dirty_json)"

write_manifest() {
  local status="$1"
  local exit_code="$2"
  local ended_at="$3"
  jq -n \
    --arg schema "hideout.release-dogfood.v1" \
    --arg status "$status" \
    --arg startedAt "$started_at" \
    --arg endedAt "$ended_at" \
    --arg command "$command_text" \
    --arg evidenceDir "$evidence_dir" \
    --arg log "test-release-dogfood.log" \
    --arg gitCommit "$commit" \
    --arg uname "$uname_value" \
    --arg swVers "$sw_vers_value" \
    --arg goVersion "$go_version" \
    --arg limactlVersion "$limactl_version" \
    --arg jqVersion "$jq_version" \
    --arg proxyScheme "$proxy_scheme_value" \
    --arg browserApp "$browser_app" \
    --argjson exitCode "$exit_code" \
    --argjson gitDirty "$git_dirty" \
    --argjson browserPathProvided "$browser_path_provided" \
    '{
      schema: $schema,
      status: $status,
      exitCode: $exitCode,
      startedAt: $startedAt,
      endedAt: $endedAt,
      command: $command,
      evidence: {
        directory: $evidenceDir,
        log: $log
      },
      git: {
        commit: $gitCommit,
        dirty: $gitDirty
      },
      host: {
        uname: $uname,
        macOSProductVersion: $swVers
      },
      tools: {
        go: $goVersion,
        limactl: $limactlVersion,
        jq: $jqVersion
      },
      operatorProxy: {
        provided: true,
        scheme: $proxyScheme,
        url: "redacted"
      },
      browser: {
        realBrowserRequired: true,
        browserPathProvided: $browserPathProvided,
        browserApp: $browserApp
      },
      gates: [
        "gate0-static-contract",
        "gate1-native-smoke",
        "gate2-lima-e2e",
        "gate3-hidden-proxy-operator",
        "gate4-host-escape-real-browser",
        "capability-probe-smoke",
        "generic-cli-dogfood-smoke"
      ]
    }' >"$manifest_path"
  validate_manifest
}

validate_manifest() {
  go run ./cmd/hideout-schema-validate "$manifest_schema_path" "$manifest_path" >/dev/null
  jq -e '
    .operatorProxy.provided == true and
    .operatorProxy.url == "redacted"
  ' "$manifest_path" >/dev/null
}

{
  echo "release-dogfood: started $started_at"
  echo "release-dogfood: evidence $evidence_dir"
  echo "release-dogfood: command $command_text"
  echo "release-dogfood: operator proxy provided (url redacted, scheme=$proxy_scheme_value)"
} >"$log_path"

echo "release-dogfood: running Phase 1 release-candidate gates"
echo "release-dogfood: includes Gate 2 Lima lifecycle, Gate 3 strict proxy, Gate 4 real browser, probes, and generic CLI dogfood"
echo "release-dogfood: evidence directory: $evidence_dir"

set +e
{
  echo "release-dogfood: running $command_text"
  scripts/test-phase1.sh --release-candidate
} 2>&1 | redact_stream | tee -a "$log_path"
status=${PIPESTATUS[0]}
set -e

ended_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
if [ "$status" -eq 0 ]; then
  write_manifest "passed" "$status" "$ended_at"
  echo "release-dogfood: passed"
else
  write_manifest "failed" "$status" "$ended_at"
  echo "release-dogfood: failed with exit code $status" >&2
fi
echo "release-dogfood: evidence manifest: $manifest_path"
echo "release-dogfood: evidence log: $log_path"
exit "$status"
