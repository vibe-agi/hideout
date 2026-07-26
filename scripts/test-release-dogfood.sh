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
  HIDEOUT_RUNTIME_BUILD_PROVENANCE
                                 Required exact runtime build-provenance JSON.
  HIDEOUT_RELEASE_RUNTIME_FAMILY Optional runtime family (default developer-standard).
  HIDEOUT_RELEASE_EVIDENCE_DIR   Optional exact evidence output directory.
  HIDEOUT_RELEASE_EVIDENCE_ROOT  Optional parent directory for generated evidence.

The evidence bundle stores command, host prerequisite, tool version, release
artifact, and result metadata. It never records the full proxy URL.
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
  HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:<port> scripts/test-release-dogfood.sh
MSG
  exit 2
fi
if [ "${HIDEOUT_PHASE1_PRINT_PLAN:-0}" != "1" ]; then
  if [ -z "${HIDEOUT_RUNTIME_BUILD_PROVENANCE:-}" ] || [ ! -f "$HIDEOUT_RUNTIME_BUILD_PROVENANCE" ]; then
    echo "release-dogfood: HIDEOUT_RUNTIME_BUILD_PROVENANCE must name the exact candidate build provenance" >&2
    exit 2
  fi
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

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "release-dogfood: missing shasum or sha256sum" >&2
    exit 127
  fi
}

file_size_bytes() {
  wc -c <"$1" | tr -d '[:space:]'
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
commit="$(git rev-parse HEAD 2>/dev/null || printf 'unknown')"
evidence_root="${HIDEOUT_RELEASE_EVIDENCE_ROOT:-$ROOT/.hideout-release-evidence}"
evidence_dir="${HIDEOUT_RELEASE_EVIDENCE_DIR:-$evidence_root/release-dogfood-$stamp-$commit}"
# Export so the gate subprocess and its per-gate emission (scripts/lib/gate-result.sh)
# write into this bundle; without the export the gates no-op and isolationGates
# aggregates empty.
export HIDEOUT_RELEASE_EVIDENCE_DIR="$evidence_dir"
mkdir -p "$evidence_dir"

log_path="$evidence_dir/test-release-dogfood.log"
manifest_path="$evidence_dir/manifest.json"
manifest_schema_path="$ROOT/schemas/release-dogfood.schema.json"
command_text="scripts/test-phase1.sh --release-candidate"
artifact_file="hideout-$(go env GOOS)-$(go env GOARCH)-$commit.tar.gz"
artifact_path="$evidence_dir/$artifact_file"
artifact_sha256=""
artifact_bytes=0
artifact_hideout_version=""
artifact_hideout_commit=""
artifact_hideout_built_at=""
artifact_hideout_go=""
artifact_hideout_platform=""
artifact_extract=""
package_identity_path="$evidence_dir/package-identity.json"
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
cleanup_gate4_browser_processes=0
cleanup_gate4_temp_dirs=0
cleanup_hideout_lima_instances=0
initial_hideout_lima_instances=""

count_gate4_browser_processes() {
  ps -axo pid=,command= |
    awk '
      BEGIN {
        marker = "--user" "-data-dir="
        needle = "/hideout-gate4."
      }
      $1 ~ /^[0-9]+$/ &&
      index($0, marker) > 0 &&
      index($0, needle) > 0 {
        count++
      }
      END { print count+0 }
    '
}

gate4_temp_roots() {
  printf '%s\n' "${TMPDIR:-/tmp}" "${HIDEOUT_GATE4_SHORT_TMPDIR:-/tmp}" |
    awk 'length && !seen[$0]++'
}

count_gate4_temp_dirs() {
  while IFS= read -r root; do
    [ -d "$root" ] || continue
    find "$root" -maxdepth 1 -type d -name 'hideout-gate4.*' -print 2>/dev/null
  done < <(gate4_temp_roots) |
    LC_ALL=C sort -u |
    awk 'NF {count++} END {print count+0}'
}

list_hideout_lima_instances() {
  if ! command -v limactl >/dev/null 2>&1; then
    return
  fi
  limactl list --format '{{.Name}}' 2>/dev/null |
    awk '$1 ~ /^hideout-/ {print $1}' |
    sort -u
}

count_new_hideout_lima_instances() {
  comm -13 \
    <(printf '%s\n' "$initial_hideout_lima_instances" | sed '/^$/d' | sort -u) \
    <(list_hideout_lima_instances) |
    awk 'NF {count++} END {print count+0}'
}

# Cleanup evidence measures resources leaked by this gate, not unrelated
# operator-owned Hideout environments that were already running on the host.
initial_hideout_lima_instances="$(list_hideout_lima_instances)"

gate4_browser_pids() {
  ps -axo pid=,command= |
    awk '
      BEGIN {
        marker = "--user" "-data-dir="
        needle = "/hideout-gate4."
      }
      $1 ~ /^[0-9]+$/ &&
      index($0, marker) > 0 &&
      index($0, needle) > 0 {
        print $1
      }
    '
}

signal_gate4_browsers() {
  local signal="$1"
  local pid
  while IFS= read -r pid; do
    [ -n "$pid" ] || continue
    /bin/kill "-$signal" "$pid" >/dev/null 2>&1 || true
  done < <(gate4_browser_pids)
}

post_run_cleanup() {
  local quiet=0
  local browser_count
  local temp_count

  signal_gate4_browsers TERM
  for _ in 1 2 3; do
    sleep 1
    [ "$(count_gate4_browser_processes)" = "0" ] && break
  done
  if [ "$(count_gate4_browser_processes)" != "0" ]; then
    signal_gate4_browsers KILL
  fi

  for _ in 1 2 3 4 5 6 7 8 9 10; do
    while IFS= read -r root; do
      [ -d "$root" ] || continue
      find "$root" -maxdepth 1 -type d -name 'hideout-gate4.*' -exec rm -rf {} + >/dev/null 2>&1 || true
    done < <(gate4_temp_roots)
    browser_count="$(count_gate4_browser_processes)"
    temp_count="$(count_gate4_temp_dirs)"
    if [ "$browser_count" = "0" ] && [ "$temp_count" = "0" ]; then
      quiet=$((quiet + 1))
      [ "$quiet" -ge 3 ] && return 0
    else
      quiet=0
    fi
    sleep 1
  done
}

collect_cleanup_evidence() {
  cleanup_gate4_browser_processes="$(count_gate4_browser_processes)"
  cleanup_gate4_temp_dirs="$(count_gate4_temp_dirs)"
  cleanup_hideout_lima_instances="$(count_new_hideout_lima_instances)"
}

cleanup_evidence_passed() {
  [ "$cleanup_gate4_browser_processes" = "0" ] &&
    [ "$cleanup_gate4_temp_dirs" = "0" ] &&
    [ "$cleanup_hideout_lima_instances" = "0" ]
}

build_release_artifact() {
  echo "release-dogfood: building release-like artifact $artifact_file" | tee -a "$log_path"
  scripts/package-local.sh --out "$artifact_path" >/dev/null
  artifact_sha256="$(sha256_file "$artifact_path")"
  artifact_bytes="$(file_size_bytes "$artifact_path")"
  collect_artifact_hideout_version
  prepare_release_artifact_inputs
}

collect_artifact_hideout_version() {
  local tmp
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-artifact-version.XXXXXX")"
  tar -xzf "$artifact_path" -C "$tmp"
  "$tmp/hideout/bin/hideout" version >"$tmp/version.out"
  artifact_hideout_version="$(sed -n '1s/^hideout //p' "$tmp/version.out")"
  artifact_hideout_commit="$(sed -n 's/^commit: //p' "$tmp/version.out")"
  artifact_hideout_built_at="$(sed -n 's/^builtAt: //p' "$tmp/version.out")"
  artifact_hideout_go="$(sed -n 's/^go: //p' "$tmp/version.out")"
  artifact_hideout_platform="$(sed -n 's/^platform: //p' "$tmp/version.out")"
  rm -rf "$tmp"
  if [ -z "$artifact_hideout_version" ] ||
    [ -z "$artifact_hideout_commit" ] ||
    [ -z "$artifact_hideout_built_at" ] ||
    [ -z "$artifact_hideout_go" ] ||
    [ -z "$artifact_hideout_platform" ]; then
    echo "release-dogfood: artifact hideout version output is incomplete" >&2
    exit 1
  fi
}

prepare_release_artifact_inputs() {
  local arch
  artifact_extract="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-dogfood-package.XXXXXX")"
  tar -xzf "$artifact_path" -C "$artifact_extract"
  arch="$(go env GOARCH)"
  export HIDEOUT_RELEASE_BINARY="$artifact_extract/hideout/bin/hideout"
  export HIDEOUT_LINUX_SHIM_PATH="$artifact_extract/hideout/bin/hideout-shim-linux-$arch"
  export HIDEOUT_LINUX_HOSTFSD_PATH="$artifact_extract/hideout/bin/hideout-hostfsd-linux-$arch"
  export HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH="$artifact_extract/hideout/bin/hideout-session-supervisor-linux-$arch"
  export HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH="$artifact_extract/hideout/bin/hideout-workspace-portal-linux-$arch"
  export HIDEOUT_LINUX_DNS_STUB_PATH="$artifact_extract/hideout/bin/hideout-dns-stub-linux-$arch"
  unset HIDEOUT_LINUX_TUN2SOCKS_PATH

  "$HIDEOUT_RELEASE_BINARY" package verify "$artifact_extract/hideout" >/dev/null
  "$HIDEOUT_RELEASE_BINARY" support release package-identity \
    --archive "$artifact_path" --out "$package_identity_path" >/dev/null
  export HIDEOUT_RUNTIME_PACKAGE_IDENTITY="$package_identity_path"
}

cleanup_release_artifact_inputs() {
  if [ -n "$artifact_extract" ] && [ -d "$artifact_extract" ]; then
    rm -rf "$artifact_extract"
  fi
}

trap cleanup_release_artifact_inputs EXIT

write_manifest() {
  local status="$1"
  local exit_code="$2"
  local ended_at="$3"
  # Aggregate the per-gate result files emitted by isolation gates (via
  # scripts/lib/gate-result.sh) into isolationGates. Absent gates dir => [].
  local isolation_gates="[]"
  if ls "$evidence_dir"/gates/*.json >/dev/null 2>&1; then
    isolation_gates=$(jq -s '.' "$evidence_dir"/gates/*.json)
  fi
  local env_image_declared=false
  [ -n "${HIDEOUT_ENV_IMAGE_URL:-}" ] && env_image_declared=true
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
    --arg artifactFile "$artifact_file" \
    --arg artifactSha256 "$artifact_sha256" \
    --arg artifactHideoutVersion "$artifact_hideout_version" \
    --arg artifactHideoutCommit "$artifact_hideout_commit" \
    --arg artifactHideoutBuiltAt "$artifact_hideout_built_at" \
    --arg artifactHideoutGo "$artifact_hideout_go" \
    --arg artifactHideoutPlatform "$artifact_hideout_platform" \
    --argjson exitCode "$exit_code" \
    --argjson gitDirty "$git_dirty" \
    --argjson browserPathProvided "$browser_path_provided" \
    --argjson artifactBytes "$artifact_bytes" \
    --argjson cleanupGate4BrowserProcesses "$cleanup_gate4_browser_processes" \
    --argjson cleanupGate4TempDirs "$cleanup_gate4_temp_dirs" \
    --argjson cleanupHideoutLimaInstances "$cleanup_hideout_lima_instances" \
    --argjson isolationGates "$isolation_gates" \
    --argjson gate4Browser "$browser_path_provided" \
    --argjson envImageDeclared "$env_image_declared" \
    --arg externalContext "$uname_value" \
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
      releaseArtifact: {
        file: $artifactFile,
        sha256: $artifactSha256,
        bytes: $artifactBytes,
        hideoutVersion: {
          version: $artifactHideoutVersion,
          commit: $artifactHideoutCommit,
          builtAt: $artifactHideoutBuiltAt,
          go: $artifactHideoutGo,
          platform: $artifactHideoutPlatform
        }
      },
      gates: [
        "gate0-static-contract",
        "gate1-native-smoke",
        "gate2-lima-e2e",
        "gate3-hidden-proxy-operator",
        "gate4-host-escape-real-browser",
        "capability-probe-smoke",
        "generic-cli-dogfood-smoke"
      ],
      cleanup: {
        gate4BrowserProcesses: $cleanupGate4BrowserProcesses,
        gate4TempDirs: $cleanupGate4TempDirs,
        hideoutLimaInstances: $cleanupHideoutLimaInstances
      },
      isolationGates: $isolationGates,
      environmentSnapshot: {
        proxyMode: "tun2socks",
        hostPrerequisites: {
          gate4Browser: $gate4Browser,
          envImageURL: $envImageDeclared
        },
        externalContext: $externalContext
      }
    }' >"$manifest_path"
  validate_manifest
}

validate_manifest() {
  go run ./cmd/hideout-schema-validate "$manifest_schema_path" "$manifest_path" >/dev/null
  jq -e '
    .operatorProxy.provided == true and
    .operatorProxy.url == "redacted" and
    .releaseArtifact.hideoutVersion.commit == .git.commit
  ' "$manifest_path" >/dev/null
}

{
  echo "release-dogfood: started $started_at"
  echo "release-dogfood: evidence $evidence_dir"
  echo "release-dogfood: command $command_text"
  echo "release-dogfood: operator proxy provided (url redacted, scheme=$proxy_scheme_value)"
} >"$log_path"

build_release_artifact

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
post_run_cleanup
collect_cleanup_evidence
if [ "$status" -eq 0 ] && ! cleanup_evidence_passed; then
  {
    echo "release-dogfood: cleanup evidence failed"
    echo "release-dogfood: gate4BrowserProcesses=$cleanup_gate4_browser_processes"
    echo "release-dogfood: gate4TempDirs=$cleanup_gate4_temp_dirs"
    echo "release-dogfood: hideoutLimaInstances=$cleanup_hideout_lima_instances"
  } | tee -a "$log_path" >&2
  status=1
fi

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
