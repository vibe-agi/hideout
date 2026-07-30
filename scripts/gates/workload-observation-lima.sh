#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

require_real=0
preflight_only=0
out="$repo_root/.artifacts/045/workload"
gate_timeout="${HIDEOUT_WORKLOAD_OBSERVATION_TIMEOUT:-20m}"
gate_completed=0
current_stage="bootstrap"
work_root=""
source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/workload-observation-lima.sh [--require-real] [--preflight] [--out DIR]" \
    "" \
    "Runs two concurrent workloads in one isolated real Lima VM, forces guest PID" \
    "reuse, generates unrelated guest noise, and verifies exact activity ownership."
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
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'workload-observation-lima: --out requires a directory\n' >&2
        exit 2
      fi
      out="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'workload-observation-lima: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

fail() {
  printf 'workload-observation-lima: %s\n' "$*" >&2
  exit 1
}

not_run() {
  if [ "$require_real" -eq 1 ]; then
    fail "$*"
  fi
  printf 'workload-observation-lima: not-run: %s\n' "$*"
  exit 77
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || not_run "missing required command: $1"
}

with_timeout() {
  local duration="$1"
  shift
  "$@" &
  local command_pid=$!
  (
    sleep "$duration"
    if kill -0 "$command_pid" 2>/dev/null; then
      kill "$command_pid" 2>/dev/null || true
      sleep 2
      kill -KILL "$command_pid" 2>/dev/null || true
    fi
  ) &
  local timer_pid=$!
  local status=0
  if wait "$command_pid"; then
    status=0
  else
    status=$?
  fi
  kill "$timer_pid" 2>/dev/null || true
  wait "$timer_pid" 2>/dev/null || true
  return "$status"
}

extract_session_id() {
  local stderr_path="$1"
  local audit_path
  audit_path="$(awk '/audit: / { print $2; exit }' "$stderr_path")"
  [ -n "$audit_path" ] || return 1
  basename "$(dirname "$audit_path")"
}

run_hideout() {
  env \
    "HIDEOUT_STORE_ROOT=$store_root" \
    "LIMA_HOME=$lima_home" \
    "HIDEOUT_LINUX_SHIM_PATH=$linux_shim" \
    "HIDEOUT_LINUX_HOSTFSD_PATH=$linux_hostfsd" \
    "HIDEOUT_LINUX_SESSION_SUPERVISOR_PATH=$linux_supervisor" \
    "HIDEOUT_LINUX_OBSERVER_PATH=$linux_observer" \
    "$hideout" "$@"
}

capture_session_events() {
  local session_id="$1"
  local output_prefix="$2"
  local kind
  local cursor
  local next_cursor
  local query_truncated
  local page_index
  local raw_page_path
  local page_path
  local -a pages=()
  for kind in process file connection dns; do
    cursor=""
    page_index=0
    while :; do
      raw_page_path="$work_root/$output_prefix.$kind.page-$page_index.raw.json"
      page_path="$work_root/$output_prefix.$kind.page-$page_index.json"
      local -a query=(
        activity events
        --session "$session_id"
        --kind "$kind"
        --limit 500
        --json
      )
      if [ -n "$cursor" ]; then
        query+=(--cursor "$cursor")
      fi
      run_hideout "${query[@]}" >"$raw_page_path"
      jq -e '
        (.records | type) == "array" and
        (.coverage | type) == "array" and
        (.queryTruncated | type) == "boolean" and
        (.records | length) <= 500 and
        (if .queryTruncated
          then ((.nextCursor | type) == "string" and
            (.nextCursor | length) > 0)
          else ((.nextCursor // "") == "")
        end)
      ' "$raw_page_path" >/dev/null ||
        fail "activity page contract mismatch: $output_prefix/$kind/$page_index"
      jq \
        --arg kind "$kind" \
        --argjson pageIndex "$page_index" \
        '. + {
          _gate: {
            kind: $kind,
            pageIndex: $pageIndex,
            recordCount: (.records | length),
            queryTruncated: .queryTruncated,
            hasNextCursor: ((.nextCursor | length) > 0)
          }
        }' "$raw_page_path" >"$page_path"
      chmod 0600 "$raw_page_path" "$page_path"
      pages+=("$page_path")
      query_truncated="$(jq -r '.queryTruncated' "$raw_page_path")"
      next_cursor="$(jq -r '.nextCursor // ""' "$raw_page_path")"
      if [ "$query_truncated" = "false" ]; then
        break
      fi
      [ "$next_cursor" != "$cursor" ] ||
        fail "activity pagination repeated a cursor: $output_prefix/$kind"
      cursor="$next_cursor"
      page_index=$((page_index + 1))
      [ "$page_index" -lt 128 ] ||
        fail "activity pagination exceeded the 64,000-record gate bound: $output_prefix/$kind"
    done
  done
  jq -s '
    map(.data // .) as $pages |
    {
      records: [$pages[].records[]?],
      coverage: ([$pages[].coverage[]?] | unique_by(.id)),
      queryTruncated: false,
      sourceQueryTruncated: any($pages[]; .queryTruncated),
      pageCount: ($pages | length),
      pageEvidence: [$pages[]._gate]
    }
  ' "${pages[@]}" >"$work_root/$output_prefix.events.json"
  chmod 0600 "$work_root/$output_prefix.events.json"
}

run_lima_root() {
  [ "$#" -eq 1 ] ||
    fail "internal root SSH helper requires one remote command"
  [ -n "${instance_name:-}" ] ||
    fail "internal root SSH helper requires an instance"
  local ssh_config="$work_root/lima-root-ssh.config"
  if [ ! -f "$ssh_config" ]; then
    LIMA_HOME="$lima_home" limactl show-ssh \
      --format=config "$instance_name" >"$ssh_config"
    chmod 0600 "$ssh_config"
  fi
  ssh \
    -F "$ssh_config" \
    -o BatchMode=yes \
    -o ControlMaster=no \
    -o ControlPath=none \
    -o ConnectionAttempts=1 \
    -o ConnectTimeout=15 \
    -l root \
    "lima-$instance_name" \
    -- "$1"
}

cleanup() {
  local status=$?
  trap - EXIT
  set +e
  set +u
  if [ -n "${http_pid:-}" ]; then
    kill "$http_pid" 2>/dev/null || true
    wait "$http_pid" 2>/dev/null || true
  fi
  if [ -x "${hideout:-}" ] && [ -n "${store_root:-}" ]; then
    run_hideout clean >/dev/null 2>&1 || true
    run_hideout daemon stop >/dev/null 2>&1 || true
  fi
  if [ -n "${lima_home:-}" ] && [ -d "$lima_home" ] &&
    command -v limactl >/dev/null 2>&1; then
    LIMA_HOME="$lima_home" limactl list --json 2>/dev/null |
      jq -r '.name // empty' 2>/dev/null |
      while IFS= read -r cleanup_instance; do
        [ -n "$cleanup_instance" ] || continue
        LIMA_HOME="$lima_home" limactl delete \
          --force --tty=false "$cleanup_instance" >/dev/null 2>&1 || true
      done
  fi
  if [ -n "${lima_home:-}" ] && [ -d "$lima_home" ]; then
    case "$lima_home" in
      "${tmp_base:-/tmp}"/ho-wo-lima.*)
        find "$lima_home" -depth -delete
        ;;
      *)
        printf 'workload-observation-lima: refusing unexpected Lima cleanup path %s\n' \
          "$lima_home" >&2
        status=1
        ;;
    esac
  fi
  if [ "${HIDEOUT_WORKLOAD_OBSERVATION_KEEP_TMP:-0}" = "1" ]; then
    [ -n "${work_root:-}" ] &&
      printf 'workload-observation-lima: kept %s\n' "$work_root"
  elif [ -n "${work_root:-}" ] && [ -d "$work_root" ]; then
    case "$work_root" in
      "${tmp_base:-/tmp}"/hideout-observation-lima.*)
        find "$work_root" -depth -delete
        ;;
      *)
        printf 'workload-observation-lima: refusing unexpected work cleanup path %s\n' \
          "$work_root" >&2
        status=1
        ;;
    esac
  fi
  if { [ "$gate_completed" != "1" ] || [ "$status" -ne 0 ]; } &&
    [ -n "${out:-}" ]; then
    mkdir -p "$out"
    chmod 0700 "$out" 2>/dev/null || true
    jq -n \
      --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
      --arg commit "$source_commit" \
      --argjson dirty "$source_dirty" \
      --arg stage "${current_stage:-unknown}" \
      --argjson exitCode "$status" \
      '{
        schema: "hideout.workload-observation-lima-evidence/v1",
        generatedAt: $generatedAt,
        source: {commit: $commit, dirty: $dirty},
        result: "failed",
        failure: {stage: $stage, exitCode: $exitCode}
      }' >"$out/result.json" 2>/dev/null || true
    chmod 0600 "$out/result.json" 2>/dev/null || true
  fi
  exit "$status"
}

require_command go
require_command jq
require_command shasum
if [ "$preflight_only" -eq 0 ]; then
  require_command limactl
  require_command curl
  require_command python3
  require_command ssh
  [ "$(go env GOOS)" = "darwin" ] || not_run "real reference lane requires macOS"
  [ "$(go env GOARCH)" = "arm64" ] || not_run "real reference lane requires darwin/arm64"
fi

tmp_base="${HIDEOUT_WORKLOAD_OBSERVATION_TMPDIR:-/tmp}"
mkdir -p "$tmp_base"
tmp_base="$(cd "$tmp_base" && pwd -P)"
if [ -L "$out" ]; then
  fail "evidence directory must not be a symlink"
fi
mkdir -p "$out"
out="$(cd "$out" && pwd -P)"
chmod 0700 "$out"
work_root="$(mktemp -d "$tmp_base/hideout-observation-lima.XXXXXX")"
chmod 0700 "$work_root"
trap cleanup EXIT

bin_dir="$work_root/bin"
store_root="$work_root/store"
lima_home="$(mktemp -d "$tmp_base/ho-wo-lima.XXXXXX")"
workspace_a="$work_root/workspace-a"
workspace_b="$work_root/workspace-b"
mkdir -p "$bin_dir" "$store_root" "$workspace_a" "$workspace_b"
chmod 0700 \
  "$bin_dir" "$store_root" "$lima_home" "$workspace_a" "$workspace_b"

hideout="$bin_dir/hideout"
linux_shim="$bin_dir/hideout-shim-linux-arm64"
linux_hostfsd="$bin_dir/hideout-hostfsd-linux-arm64"
linux_supervisor="$bin_dir/hideout-session-supervisor-linux-arm64"
linux_observer="$bin_dir/hideout-observer-linux-arm64"

go build -trimpath -o "$hideout" ./cmd/hideout

# This is deliberately a product preflight rather than a source-file check:
# the gate remains red until the supported CLI and packaged observer are real.
current_stage="product-preflight"
if ! "$hideout" help activity >"$work_root/activity-help.txt" 2>&1; then
  fail "activity CLI is unavailable; implement Phase 4 before promoting this gate"
fi
if [ ! -d cmd/hideout-observer ]; then
  fail "packaged hideout-observer command is unavailable"
fi

if [ "$preflight_only" -eq 1 ]; then
  gate_completed=1
  printf 'workload-observation-lima: preflight=passed\n'
  exit 0
fi

"$hideout" shim build-linux \
  --out "$linux_shim" --goarch arm64 --source "$repo_root" >/dev/null
"$hideout" hostfsd build-linux \
  --out "$linux_hostfsd" --goarch arm64 --source "$repo_root" >/dev/null
go run ./internal/helperbin/cmd/build-session-supervisor \
  --out "$linux_supervisor" --goarch arm64 --source "$repo_root"
go run ./internal/helperbin/cmd/build-observer \
  --out "$linux_observer" --goarch arm64 --source "$repo_root"

cp internal/workloadobs/testdata/reference-workload.sh "$workspace_a/reference-workload.sh"
cp internal/workloadobs/testdata/reference-workload.sh "$workspace_b/reference-workload.sh"
chmod 0700 "$workspace_a/reference-workload.sh" "$workspace_b/reference-workload.sh"
current_stage="build-real-kernel-provider-test"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c \
  -o "$workspace_a/file-observer-kernel.test" \
  ./internal/workloadobs/collector/bpf
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c \
  -o "$workspace_a/network-correlation-matrix.test" \
  ./internal/workloadobs/collector
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c \
  -o "$workspace_a/network-kernel-attribution.test" \
  ./internal/workloadobs/collector/network
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c \
  -o "$workspace_a/dns-mediator-attribution.test" \
  ./internal/workloadobs/collector/dns
chmod 0700 \
  "$workspace_a/file-observer-kernel.test" \
  "$workspace_a/network-correlation-matrix.test" \
  "$workspace_a/network-kernel-attribution.test" \
  "$workspace_a/dns-mediator-attribution.test"

run_hideout init \
  --no-input --profile default --template dev --backend lima --network direct >/dev/null
run_hideout doctor --backend lima --workspace "$workspace_a" --network direct >/dev/null

current_stage="real-lima-concurrent-workloads"
python3 -u -m http.server 0 --bind 127.0.0.1 \
  --directory "$work_root" >"$work_root/http-address.txt" 2>"$work_root/http.log" &
http_pid=$!
for _ in $(seq 1 100); do
  if grep -Eq 'Serving HTTP on .* port [0-9]+' "$work_root/http-address.txt"; then
    break
  fi
  kill -0 "$http_pid" 2>/dev/null || fail "HTTP fixture exited before publishing"
  sleep 0.05
done
http_port="$(
  sed -nE 's/.* port ([0-9]+) .*/\1/p' \
    "$work_root/http-address.txt" |
    head -1
)"
[ -n "$http_port" ] || fail "HTTP fixture did not publish a port"
fixture_url="http://host.lima.internal:$http_port/"

run_workload() {
  local label="$1"
  local workspace="$2"
  local stdout_path="$work_root/$label.out"
  local stderr_path="$work_root/$label.err"
  with_timeout "$gate_timeout" run_hideout run \
    --verbose --backend lima --network direct --workspace "$workspace" \
    -- sh -eu -c '
workspace="$1"
dns_name="$2"
url="$3"
touch "$workspace/ready"
while [ ! -f "$workspace/go" ]; do sleep 0.01; done
"$workspace/reference-workload.sh" "$workspace" "$dns_name" "$url"
label="$4"
guest_fixture="/tmp/hideout-gate-local-$label"
mkdir "$guest_fixture"
mkdir "$guest_fixture/nested"
printf "%s\n" "gate-line-one" >"$guest_fixture/source.txt"
IFS= read -r gate_line <"$guest_fixture/source.txt"
[ "$gate_line" = "gate-line-one" ]
printf "%s\n" "gate-line-two" >>"$guest_fixture/source.txt"
printf "%s\n" "truncate-me" >"$guest_fixture/truncated.txt"
: >"$guest_fixture/truncated.txt"
chmod 0600 "$guest_fixture/source.txt"
mv "$guest_fixture/source.txt" "$guest_fixture/renamed.txt"
ln "$guest_fixture/renamed.txt" "$guest_fixture/hardlink.txt"
ln -s "renamed.txt" "$guest_fixture/symlink.txt"
rm "$guest_fixture/hardlink.txt"
rm "$guest_fixture/symlink.txt"
rmdir "$guest_fixture/nested"
rm "$guest_fixture/renamed.txt"
rm "$guest_fixture/truncated.txt"
rmdir "$guest_fixture"
i=0
while [ "$i" -lt 1600 ]; do
  sh -c "true"
  i=$((i + 1))
done
' sh "/workspace" "$label.hideout-observation.lima.internal" "$fixture_url" "$label" \
    >"$stdout_path" 2>"$stderr_path"
}

run_workload session-a "$workspace_a" &
workload_a_pid=$!
run_workload session-b "$workspace_b" &
workload_b_pid=$!
for _ in $(seq 1 1200); do
  [ -f "$workspace_a/ready" ] && [ -f "$workspace_b/ready" ] && break
  kill -0 "$workload_a_pid" 2>/dev/null || fail "session-a exited before readiness"
  kill -0 "$workload_b_pid" 2>/dev/null || fail "session-b exited before readiness"
  sleep 0.05
done
[ -f "$workspace_a/ready" ] && [ -f "$workspace_b/ready" ] ||
  fail "concurrent workloads did not reach readiness"

instance_name="$(
  LIMA_HOME="$lima_home" limactl list --json |
    jq -r 'select(.status == "Running") | .name' |
    head -1
)"
[ -n "$instance_name" ] || fail "isolated Lima instance is not running"

current_stage="real-lima-bpf-kernel-provider"
kernel_test_guest="/tmp/hideout-file-observer-kernel.test"
LIMA_HOME="$lima_home" limactl copy \
  --backend=scp --tty=false \
  "$workspace_a/file-observer-kernel.test" \
  "$instance_name:$kernel_test_guest" \
  >"$work_root/file-observer-kernel-copy.log" 2>&1
if ! run_lima_root \
  "chmod 0700 '$kernel_test_guest' && HIDEOUT_TEST_BPF_ATTACH=1 '$kernel_test_guest' -test.v -test.count=1 -test.run '^TestFileEventReaderRealKernel$'" \
  >"$work_root/file-observer-kernel-test.log" 2>&1; then
  fail "real kernel file provider did not observe its complete operation fixture"
fi
chmod 0600 \
  "$work_root/file-observer-kernel-copy.log" \
  "$work_root/file-observer-kernel-test.log"

current_stage="real-lima-attribution-matrix"
network_matrix_guest="/tmp/hideout-network-correlation-matrix.test"
network_kernel_guest="/tmp/hideout-network-kernel-attribution.test"
dns_mediator_guest="/tmp/hideout-dns-mediator-attribution.test"
LIMA_HOME="$lima_home" limactl copy \
  --backend=scp --tty=false \
  "$workspace_a/network-correlation-matrix.test" \
  "$instance_name:$network_matrix_guest" \
  >"$work_root/network-correlation-copy.log" 2>&1
LIMA_HOME="$lima_home" limactl copy \
  --backend=scp --tty=false \
  "$workspace_a/network-kernel-attribution.test" \
  "$instance_name:$network_kernel_guest" \
  >"$work_root/network-kernel-copy.log" 2>&1
LIMA_HOME="$lima_home" limactl copy \
  --backend=scp --tty=false \
  "$workspace_a/dns-mediator-attribution.test" \
  "$instance_name:$dns_mediator_guest" \
  >"$work_root/dns-mediator-copy.log" 2>&1
if ! run_lima_root \
  "chmod 0700 '$network_matrix_guest' && '$network_matrix_guest' -test.v -test.count=1 -test.run '^(TestNetworkCorrelatorNormalizesConnect4Connect6UDPAndTCP|TestNetworkCorrelatorUsesTTLBoundSameExecutionDNSInference|TestNetworkCorrelatorDoesNotGuessSharedIPCacheLiteralOrEncryptedDNS|TestNetworkCorrelatorUsesValidatedProxyTargetAsExactAndRejectsCrossBoundary)$'" \
  >"$work_root/network-correlation-matrix.log" 2>&1; then
  fail "real Linux network/domain correlation matrix failed"
fi
if ! run_lima_root \
  "chmod 0700 '$network_kernel_guest' && '$network_kernel_guest' -test.v -test.count=1 -test.run '^(TestNormalizeKernelConnectionPreservesExactActorEndpointAndRouteEvidence|TestNormalizeKernelConnectionKeepsMissingActorAndEgressUnknown|TestNormalizeKernelConnectionUsesEventCredentialsForExactExecution|TestNormalizeKernelConnectionAttributesUnexecedChildToInheritedExecution|TestNormalizeKernelConnectionRejectsMismatchedEvidence)$'" \
  >"$work_root/network-kernel-attribution.log" 2>&1; then
  fail "real Linux kernel network attribution matrix failed"
fi
if ! run_lima_root \
  "chmod 0700 '$dns_mediator_guest' && '$dns_mediator_guest' -test.v -test.count=1 -test.run '^(TestPacketFromKernelRecordAttributesForkedChildToInheritedExecution|TestProxyChunkFromKernelRecordPreservesForkedChildActor)$'" \
  >"$work_root/dns-mediator-attribution.log" 2>&1; then
  fail "real Linux DNS/proxy mediator attribution matrix failed"
fi
for matrix_check in \
  "network-correlation-matrix.log:TestNetworkCorrelatorNormalizesConnect4Connect6UDPAndTCP" \
  "network-correlation-matrix.log:TestNetworkCorrelatorUsesTTLBoundSameExecutionDNSInference" \
  "network-correlation-matrix.log:TestNetworkCorrelatorDoesNotGuessSharedIPCacheLiteralOrEncryptedDNS" \
  "network-correlation-matrix.log:TestNetworkCorrelatorUsesValidatedProxyTargetAsExactAndRejectsCrossBoundary" \
  "network-kernel-attribution.log:TestNormalizeKernelConnectionPreservesExactActorEndpointAndRouteEvidence" \
  "network-kernel-attribution.log:TestNormalizeKernelConnectionKeepsMissingActorAndEgressUnknown" \
  "network-kernel-attribution.log:TestNormalizeKernelConnectionUsesEventCredentialsForExactExecution" \
  "network-kernel-attribution.log:TestNormalizeKernelConnectionAttributesUnexecedChildToInheritedExecution" \
  "network-kernel-attribution.log:TestNormalizeKernelConnectionRejectsMismatchedEvidence" \
  "dns-mediator-attribution.log:TestPacketFromKernelRecordAttributesForkedChildToInheritedExecution" \
  "dns-mediator-attribution.log:TestProxyChunkFromKernelRecordPreservesForkedChildActor"; do
  matrix_log="${matrix_check%%:*}"
  matrix_test="${matrix_check#*:}"
  grep -Eq "^--- PASS: ${matrix_test} \\(" \
    "$work_root/$matrix_log" ||
    fail "real Linux attribution matrix omitted $matrix_test"
done
chmod 0600 \
  "$work_root/network-correlation-copy.log" \
  "$work_root/network-kernel-copy.log" \
  "$work_root/dns-mediator-copy.log" \
  "$work_root/network-correlation-matrix.log" \
  "$work_root/network-kernel-attribution.log" \
  "$work_root/dns-mediator-attribution.log"

old_pid_max="$(
  run_lima_root "sysctl -n kernel.pid_max"
)"
case "$old_pid_max" in
  '' | *[!0-9]*)
    fail "isolated VM returned an invalid kernel.pid_max"
    ;;
esac
run_lima_root "sysctl -w kernel.pid_max=1024" >/dev/null
LIMA_HOME="$lima_home" limactl shell "$instance_name" \
  -- sh -c 'i=0; while [ "$i" -lt 800 ]; do sh -c "true"; i=$((i + 1)); done; touch /tmp/hideout-unrelated-noise-canary' &
noise_pid=$!

touch "$workspace_a/go" "$workspace_b/go"
wait "$workload_a_pid"
wait "$workload_b_pid"
wait "$noise_pid"
run_lima_root "sysctl -w kernel.pid_max=$old_pid_max" >/dev/null

session_a="$(extract_session_id "$work_root/session-a.err")"
session_b="$(extract_session_id "$work_root/session-b.err")"
[ -n "$session_a" ] && [ -n "$session_b" ] && [ "$session_a" != "$session_b" ] ||
  fail "two distinct session IDs were not observed"

capture_session_events "$session_a" session-a
capture_session_events "$session_b" session-b
run_hideout activity executions --session "$session_a" --json \
  >"$work_root/session-a.executions.json"
run_hideout activity executions --session "$session_b" --json \
  >"$work_root/session-b.executions.json"
run_hideout activity coverage --session "$session_a" --json \
  >"$work_root/session-a.coverage.json"
run_hideout activity coverage --session "$session_b" --json \
  >"$work_root/session-b.coverage.json"

jq -e --arg session "$session_a" '
  (.data.records // .records) as $records |
  ($records | length) > 0 and
  all($records[]; .sessionId == $session) and
  any($records[]; .kind == "process") and
  any($records[]; .kind == "file") and
  any($records[]; .kind == "connection") and
  any($records[]; .kind == "dns")
' "$work_root/session-a.events.json" >/dev/null ||
  fail "session-a activity is incomplete or cross-attributed"
jq -e --arg session "$session_b" '
  (.data.records // .records) as $records |
  ($records | length) > 0 and
  all($records[]; .sessionId == $session) and
  any($records[]; .kind == "process") and
  any($records[]; .kind == "file") and
  any($records[]; .kind == "connection") and
  any($records[]; .kind == "dns")
' "$work_root/session-b.events.json" >/dev/null ||
  fail "session-b activity is incomplete or cross-attributed"

if grep -R -F "hideout-unrelated-noise-canary" \
  "$work_root/session-a.events.json" "$work_root/session-b.events.json" >/dev/null; then
  fail "unrelated guest noise was attributed to a workload"
fi

for execution_path in \
  "$work_root/session-a.executions.json" \
  "$work_root/session-b.executions.json"; do
  jq -e '
    [(.data.roots // .roots)[] | recurse(.children[]?) | .execution] as $executions |
    ($executions | length) >= 70 and
    ([$executions | group_by(.pid)[] |
      select((map(.id) | unique | length) > 1)] | length) > 0
  ' "$execution_path" >/dev/null ||
    fail "PID reuse did not retain distinct execution identities: $execution_path"
done

for session_label in session-a session-b; do
  events_path="$work_root/$session_label.events.json"
  fixture_path="/tmp/hideout-gate-local-$session_label"
  fixture_alias="hideout-gate-local-$session_label"
  jq -e \
    --arg fixture "$fixture_path" \
    --arg fixtureAlias "$fixture_alias" '
    . as $events |
    ([$events.records[] |
      select(
        .kind == "file" and
        (.subject.pathState // "") == "resolved" and
        ((.subject.path // "") | startswith($fixture)) and
        (.subject.inode // 0) > 0
      ) |
      [(.subject.device // 0), .subject.inode]
    ] | unique) as $fixtureIdentities |
    [$events.records[] |
      . as $record |
      select(
        .kind == "file" and
        (
          ((.subject.path // "") | startswith($fixture)) or
          ((.subject.targetPath // "") | startswith($fixture)) or
          (
            (.subject.pathState // "") == "aliased" and
            (
              ((.subject.path // "") | contains($fixtureAlias)) or
              ((.subject.targetPath // "") | contains($fixtureAlias)) or
              (
                (.subject.inode // 0) > 0 and
                any($fixtureIdentities[];
                  .[0] == ($record.subject.device // 0) and
                  .[1] == ($record.subject.inode // 0))
              )
            )
          )
        )
      )] as $files |
    [
      "open", "read", "write", "create", "truncate", "rename",
      "unlink", "metadata", "hardlink", "symlink", "mkdir", "rmdir"
    ] as $required |
    [
      "rename", "unlink", "metadata", "hardlink",
      "symlink", "mkdir", "rmdir"
    ] as $aliasRequired |
    ($files | length) >= ($required | length) and
    all($required[];
      . as $operation |
      any($files[]; .operation == $operation)) and
    all($aliasRequired[];
      . as $operation |
      any($files[];
        .operation == $operation and
        .subject.pathState == "aliased")) and
    all($files[];
      .attribution == "exact" and
      (.actor.executionId | type) == "string" and
      (.actor.executionId | length) > 0 and
      (.subject.pathState |
        . == "resolved" or . == "aliased" or
        . == "raced" or . == "truncated" or . == "unknown") and
      (.outcome.status |
        . == "succeeded" or . == "failed" or
        . == "denied" or . == "unknown"))
  ' "$events_path" >/dev/null ||
    fail "$session_label did not retain every supported guest file operation"
done

for coverage_path in \
  "$work_root/session-a.coverage.json" \
  "$work_root/session-b.coverage.json"; do
  jq -e '
    (.data.intervals // .intervals) as $coverage |
    ["process", "file", "network", "dns"] |
    map(. as $subsystem |
      any($coverage[];
        .subsystem == $subsystem and
        (.state == "Available" or
          .state == "Partial" or
          .state == "Unavailable"))) |
    all
  ' "$coverage_path" >/dev/null ||
    fail "coverage does not account for every subsystem: $coverage_path"
done

current_stage="persist-real-evidence"
run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
evidence_root="$out/$run_id"
[ ! -e "$evidence_root" ] ||
  fail "evidence run directory already exists"
mkdir "$evidence_root"
chmod 0700 "$evidence_root"
artifact_rows="$evidence_root/.artifacts.jsonl"
: >"$artifact_rows"
chmod 0600 "$artifact_rows"
for artifact_name in \
  session-a.events.json \
  session-a.executions.json \
  session-a.coverage.json \
  file-observer-kernel-copy.log \
  file-observer-kernel-test.log \
  network-correlation-copy.log \
  network-kernel-copy.log \
  dns-mediator-copy.log \
  network-correlation-matrix.log \
  network-kernel-attribution.log \
  dns-mediator-attribution.log \
  session-b.events.json \
  session-b.executions.json \
  session-b.coverage.json; do
  cp "$work_root/$artifact_name" "$evidence_root/$artifact_name"
  chmod 0600 "$evidence_root/$artifact_name"
  artifact_sha="$(
    shasum -a 256 "$evidence_root/$artifact_name" |
      awk '{print $1}'
  )"
  artifact_bytes="$(wc -c <"$evidence_root/$artifact_name" | tr -d '[:space:]')"
  jq -cn \
    --arg path "$artifact_name" \
    --arg sha256 "$artifact_sha" \
    --argjson bytes "$artifact_bytes" \
    '{path:$path, sha256:$sha256, bytes:$bytes, mode:"0600"}' \
    >>"$artifact_rows"
done

jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg runId "$run_id" \
  --arg instance "$instance_name" \
  --arg sessionA "$session_a" \
  --arg sessionB "$session_b" \
  --slurpfile aEvents "$evidence_root/session-a.events.json" \
  --slurpfile aExecutions "$evidence_root/session-a.executions.json" \
  --slurpfile aCoverage "$evidence_root/session-a.coverage.json" \
  --slurpfile bEvents "$evidence_root/session-b.events.json" \
  --slurpfile bExecutions "$evidence_root/session-b.executions.json" \
  --slurpfile bCoverage "$evidence_root/session-b.coverage.json" \
  --slurpfile artifacts "$artifact_rows" '
  def executions($document):
    [($document.data.roots // $document.roots)[] |
      recurse(.children[]?) | .execution];
  def operation_counts($events):
    [$events.records[] | {kind, operation}] |
    group_by([.kind, .operation]) |
    map({kind:.[0].kind, operation:.[0].operation, count:length});
  def coverage_counts($coverage):
    [($coverage.data.intervals // $coverage.intervals)[] |
      {subsystem, state, reason}] |
    group_by([.subsystem, .state, .reason]) |
    map({
      subsystem:.[0].subsystem,
      state:.[0].state,
      reason:.[0].reason,
      count:length
    });
  def fixture_files($events; $fixture; $fixtureAlias):
    ([$events.records[] |
      select(
        .kind == "file" and
        (.subject.pathState // "") == "resolved" and
        ((.subject.path // "") | startswith($fixture)) and
        (.subject.inode // 0) > 0
      ) |
      [(.subject.device // 0), .subject.inode]
    ] | unique) as $fixtureIdentities |
    [$events.records[] |
      . as $record |
      select(
        .kind == "file" and
        (
          ((.subject.path // "") | startswith($fixture)) or
          ((.subject.targetPath // "") | startswith($fixture)) or
          (
            (.subject.pathState // "") == "aliased" and
            (
              ((.subject.path // "") | contains($fixtureAlias)) or
              ((.subject.targetPath // "") | contains($fixtureAlias)) or
              (
                (.subject.inode // 0) > 0 and
                any($fixtureIdentities[];
                  .[0] == ($record.subject.device // 0) and
                  .[1] == ($record.subject.inode // 0))
              )
            )
          )
        )
      )];
  def session_evidence($label; $session; $events; $tree; $coverage):
    executions($tree) as $executions |
    {
      label:$label,
      sessionId:$session,
      owner:($events.records[0].owner),
      recordCount:($events.records | length),
      pageCount:$events.pageCount,
      sourceQueryTruncated:$events.sourceQueryTruncated,
      operationCounts:operation_counts($events),
      executionCount:($executions | length),
      rootExecutionCount:
        ([$executions[] | select(.parentExecutionId == null)] | length),
      exitedExecutionCount:
        ([$executions[] | select(.exit != null)] | length),
      pidReuseGroupCount:
        ([$executions | group_by(.pid)[] |
          select((map(.id) | unique | length) > 1)] | length),
      reparentedFixtureCount:
        ([$executions[] |
          select((.argv // [] | join(" ") | contains("reparented")))] |
          length),
      missingGuestIdentityCount:
        ([$executions[] |
          select(
            (.guestBootId // "") == "" or
            (.observerGeneration // 0) == 0 or
            (.pid // 0) == 0 or
            (.execSequence // 0) == 0 or
            (.guestIdentity.uid // -1) < 0 or
            (.guestIdentity.gid // -1) < 0
          )] | length),
      coverage:coverage_counts($coverage)
    };
  [
    "open", "read", "write", "create", "truncate", "rename",
    "unlink", "metadata", "hardlink", "symlink", "mkdir", "rmdir"
  ] as $requiredFileOperations |
  [
    "rename", "unlink", "metadata", "hardlink",
    "symlink", "mkdir", "rmdir"
  ] as $aliasFileOperations |
  [
    "TestNetworkCorrelatorNormalizesConnect4Connect6UDPAndTCP",
    "TestNetworkCorrelatorUsesTTLBoundSameExecutionDNSInference",
    "TestNetworkCorrelatorDoesNotGuessSharedIPCacheLiteralOrEncryptedDNS",
    "TestNetworkCorrelatorUsesValidatedProxyTargetAsExactAndRejectsCrossBoundary",
    "TestNormalizeKernelConnectionPreservesExactActorEndpointAndRouteEvidence",
    "TestNormalizeKernelConnectionKeepsMissingActorAndEgressUnknown",
    "TestNormalizeKernelConnectionUsesEventCredentialsForExactExecution",
    "TestNormalizeKernelConnectionAttributesUnexecedChildToInheritedExecution",
    "TestNormalizeKernelConnectionRejectsMismatchedEvidence",
    "TestPacketFromKernelRecordAttributesForkedChildToInheritedExecution",
    "TestProxyChunkFromKernelRecordPreservesForkedChildActor"
  ] as $linuxAttributionTests |
  [$aEvents[0], $bEvents[0]] as $eventSets |
  {
    schema:"hideout.workload-observation-lima-evidence/v1",
    generatedAt:$generatedAt,
    result:"passed",
    candidateAcceptance:($dirty | not),
    source:{commit:$commit, dirty:$dirty},
    runId:$runId,
    isolation:{
      limaHome:"isolated-temporary",
      instanceName:$instance,
      existingUserInstancesChanged:false
    },
    assertions:{
      realLima:true,
      completeKernelFileProvider:true,
      distinctSessions:($sessionA != $sessionB),
      completePagination:
        (all($eventSets[]; .queryTruncated == false)),
      exactOwnerBinding:
        (all(range(0;2);
          . as $index |
          ($eventSets[$index]) as $events |
          (if $index == 0 then $sessionA else $sessionB end) as $session |
          ($events.records | length) > 0 and
          all($events.records[]; .sessionId == $session))),
      allActivityKinds:
        (all($eventSets[];
          . as $events |
          all(["process","file","connection","dns"][];
            . as $kind |
            any($events.records[]; .kind == $kind)))),
      networkDNSFieldsComplete:
        (all($eventSets[];
          . as $events |
          ([$events.records[] | select(.kind == "connection")]) as $network |
          ([$events.records[] | select(.kind == "dns")]) as $dns |
          any($network[]; .subject.protocol == "tcp") and
          any($network[]; .subject.protocol == "udp") and
          all($network[];
            .attribution == "exact" and
            (.actor.executionId // "") != "" and
            (.actor.pid // 0) > 0 and
            (.subject.protocol == "tcp" or
              .subject.protocol == "udp") and
            (.subject.ip | test("^[0-9A-Fa-f:.]+$")) and
            (.subject.port // 0) > 0 and
            (.subject.port // 0) <= 65535 and
            (.subject.domainAttribution == "exact" or
              .subject.domainAttribution == "inferred" or
              .subject.domainAttribution == "unknown") and
            (.subject.route == "direct" or
              .subject.route == "proxy" or
              .subject.route == "unknown") and
            .subject.direction == "egress" and
            (.outcome.status == "succeeded" or
              .outcome.status == "failed" or
              .outcome.status == "denied" or
              .outcome.status == "unknown") and
            .count > 0 and
            .firstAt != "" and .lastAt != "" and
            .firstSequence > 0 and .lastSequence >= .firstSequence and
            .coverageId != "" and .redactionStatus == "passed") and
          all($dns[];
            .attribution == "exact" and
            (.actor.executionId // "") != "" and
            (.actor.pid // 0) > 0 and
            (.subject.query // "") != "" and
            (.subject.queryType == "A" or
              .subject.queryType == "AAAA") and
            (.subject.responseCode // "") != "" and
            (.subject.resolver // "") != "" and
            (.outcome.status == "succeeded" or
              .outcome.status == "failed") and
            .count > 0 and
            .firstAt != "" and .lastAt != "" and
            .firstSequence > 0 and .lastSequence >= .firstSequence and
            .coverageId != "" and .redactionStatus == "passed"))),
      honestUnknownDomainAndRoute:
        (all($eventSets[];
          any(.records[];
            .kind == "connection" and
            .subject.domainAttribution == "unknown" and
            .subject.correlationReason ==
              "literal-or-uncorrelated-ip" and
            .subject.route == "unknown" and
            any(.truncation[]?; . == "route-unresolved")))),
      fullDomainCorrelationMatrix:true,
      mediatorActorMatrix:true,
      intentionallyUnattributableSample:true,
      allSupportedFileOperations:
        (all(range(0;2);
          . as $index |
          ("/tmp/hideout-gate-local-" +
            (if $index == 0 then "session-a" else "session-b" end)) as $fixture |
          ("hideout-gate-local-" +
            (if $index == 0 then "session-a" else "session-b" end)) as $fixtureAlias |
          fixture_files(
            $eventSets[$index]; $fixture; $fixtureAlias
          ) as $files |
          all($requiredFileOperations[];
            . as $operation |
            any($files[]; .operation == $operation)))),
      pathAliasesExplicit:
        (all(range(0;2);
          . as $index |
          ("/tmp/hideout-gate-local-" +
            (if $index == 0 then "session-a" else "session-b" end)) as $fixture |
          ("hideout-gate-local-" +
            (if $index == 0 then "session-a" else "session-b" end)) as $fixtureAlias |
          fixture_files(
            $eventSets[$index]; $fixture; $fixtureAlias
          ) as $files |
          all($aliasFileOperations[];
            . as $operation |
            any($files[];
              .operation == $operation and
              .subject.pathState == "aliased")))),
      pidReuse:
        (all([$aExecutions[0],$bExecutions[0]][];
          . as $tree |
          executions($tree) |
          any(group_by(.pid)[]; (map(.id) | unique | length) > 1))),
      parentLineage:
        (all([$aExecutions[0],$bExecutions[0]][];
          . as $tree |
          executions($tree) |
          any(.[]; .parentExecutionId != null))),
      reparentedDescendant:
        (all([$aExecutions[0],$bExecutions[0]][];
          . as $tree |
          executions($tree) |
          any(.[];
            .parentExecutionId != null and
            (.argv // [] | join(" ") | contains("reparented"))))),
      guestIdentityComplete:
        (all([$aExecutions[0],$bExecutions[0]][];
          . as $tree |
          executions($tree) |
          all(.[];
            (.guestBootId // "") != "" and
            (.observerGeneration // 0) > 0 and
            (.pid // 0) > 0 and
            (.execSequence // 0) > 0 and
            (.guestIdentity.uid // -1) >= 0 and
            (.guestIdentity.gid // -1) >= 0))),
      unrelatedNoiseExcluded:
        (all($eventSets[];
          all(.records[];
            ((.subject.path // "") |
              contains("hideout-unrelated-noise-canary") | not))))
    },
    linuxAttributionTests:$linuxAttributionTests,
    sessions:[
      session_evidence(
        "session-a";$sessionA;$aEvents[0];
        $aExecutions[0];$aCoverage[0]
      ),
      session_evidence(
        "session-b";$sessionB;$bEvents[0];
        $bExecutions[0];$bCoverage[0]
      )
    ],
    artifacts:$artifacts
  } |
  if all(.assertions[]; . == true) then .
  else error("persisted assertion mismatch")
  end
' >"$evidence_root/summary.json"
chmod 0600 "$evidence_root/summary.json"
summary_sha="$(
  shasum -a 256 "$evidence_root/summary.json" |
    awk '{print $1}'
)"
result_tmp="$(mktemp "$out/.result.XXXXXX")"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg runId "$run_id" \
  --arg summary "$run_id/summary.json" \
  --arg summarySHA256 "$summary_sha" \
  '{
    schema:"hideout.workload-observation-lima-pointer/v1",
    generatedAt:$generatedAt,
    result:"passed",
    candidateAcceptance:($dirty | not),
    source:{commit:$commit, dirty:$dirty},
    runId:$runId,
    summary:$summary,
    summarySHA256:$summarySHA256
  }' >"$result_tmp"
chmod 0600 "$result_tmp"
mv "$result_tmp" "$out/result.json"
find "$evidence_root" -type f -exec chmod 0600 {} +
current_stage="completed"
gate_completed=1

printf 'workload-observation-lima: sessions=%s,%s\n' "$session_a" "$session_b"
printf 'workload-observation-lima: pid-reuse=proved\n'
printf 'workload-observation-lima: unrelated-noise=excluded\n'
printf 'workload-observation-lima: evidence=%s\n' "$evidence_root/summary.json"
printf 'workload-observation-lima: passed\n'
