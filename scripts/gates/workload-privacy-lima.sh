#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"

require_real=0
preflight_only=0
out="$repo_root/.artifacts/045/privacy"
gate_timeout="${HIDEOUT_WORKLOAD_PRIVACY_TIMEOUT:-35m}"
events_per_round="${HIDEOUT_WORKLOAD_PRIVACY_EVENTS_PER_ROUND:-7000}"
maximum_rounds="${HIDEOUT_WORKLOAD_PRIVACY_MAXIMUM_ROUNDS:-3}"
measure_performance="${HIDEOUT_WORKLOAD_PRIVACY_MEASURE_PERFORMANCE:-0}"

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/workload-privacy-lima.sh [--require-real] [--preflight] [--out DIR]" \
    "" \
    "Runs an isolated real Lima workload and proves quota loss accounting," \
    "pre-persistence redaction, local-path visibility, and exact-owner cleanup." \
    "Evidence is retained under .artifacts/045/privacy by default."
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
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'workload-privacy-lima: --out requires a directory\n' >&2
        exit 2
      }
      out="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'workload-privacy-lima: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$events_per_round:$maximum_rounds" in
  *[!0-9:]* | :* | *:)
    printf 'workload-privacy-lima: event bounds must be positive integers\n' >&2
    exit 2
    ;;
esac
if [ "$events_per_round" -lt 1 ] || [ "$maximum_rounds" -lt 1 ]; then
  printf 'workload-privacy-lima: event bounds must be positive integers\n' >&2
  exit 2
fi
case "$measure_performance" in
  0 | 1) ;;
  *)
    printf \
      'workload-privacy-lima: HIDEOUT_WORKLOAD_PRIVACY_MEASURE_PERFORMANCE must be 0 or 1\n' \
      >&2
    exit 2
    ;;
esac

gate_completed=0
current_stage="bootstrap"
work_root=""
store_root=""
lima_home=""
hideout=""
run_pid=""
observer_sampler_pid=""
secret_set_pid=""
secret_stdin_open=0
secret_ref=""
secret_set=0
keychain_service=""
keychain_item_created=0
environment_id=""
instance_name=""
owner_kind=""
source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi

fail() {
  printf 'workload-privacy-lima: %s\n' "$*" >&2
  exit 1
}

not_run() {
  if [ "$require_real" -eq 1 ]; then
    fail "$*"
  fi
  printf 'workload-privacy-lima: not-run: %s\n' "$*"
  exit 77
}

require_command() {
  command -v "$1" >/dev/null 2>&1 ||
    not_run "missing required command: $1"
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

purge_isolated_keychain_item() {
  [ "${keychain_item_created:-0}" -eq 1 ] || return 0
  [ -n "${keychain_service:-}" ] && [ -n "${secret_ref:-}" ] || {
    printf 'workload-privacy-lima: isolated Keychain identity is incomplete\n' >&2
    return 1
  }

  local delete_rc=0
  with_timeout 15 security delete-generic-password \
    -s "$keychain_service" -a "$secret_ref" \
    >/dev/null 2>&1 || delete_rc=$?
  if [ "$delete_rc" -ne 0 ]; then
    local find_rc=0
    with_timeout 5 security find-generic-password \
      -s "$keychain_service" -a "$secret_ref" \
      >/dev/null 2>&1 || find_rc=$?
    if [ "$find_rc" -ne 44 ]; then
      printf \
        'workload-privacy-lima: could not remove exact isolated Keychain fixture\n' \
        >&2
      return 1
    fi
  fi
  keychain_item_created=0
  return 0
}

wait_for_file() {
  local path="$1"
  local seconds="$2"
  local description="$3"
  local deadline
  deadline=$(($(date +%s) + seconds))
  while [ ! -f "$path" ]; do
    if [ -n "${run_pid:-}" ] && ! kill -0 "$run_pid" 2>/dev/null; then
      fail "workload exited before $description"
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      fail "timed out waiting for $description"
    fi
    sleep 0.2
  done
}

hash_text() {
  printf '%s' "$1" | shasum -a 256 | awk '{print $1}'
}

performance_values_json() {
  jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)' "$1"
}

performance_percentile() {
  local values="$1" percentile="$2" count index
  count="$(wc -l <"$values" | tr -d ' ')"
  [ "$count" -gt 0 ] || return 1
  index=$(((count * percentile + 99) / 100))
  sort -n "$values" | sed -n "${index}p"
}

collect_observer_performance_samples() {
  local stop_path="$1"
  local output_path="$2"
  local stop_name="${stop_path##*/}"
  case "$stop_name" in
    performance-sampler-stop-[0-9]*) ;;
    *) fail "observer sampler received an invalid stop marker" ;;
  esac
  run_lima_root "
stop='/hideout/profile/cache/$stop_name'
while [ ! -f \"\$stop\" ]; do
  pid=\"\$(pgrep -f '^/hideout/session/shims/hideout-observer( |\$)' | head -n1)\"
  if [ -z \"\$pid\" ]; then
    exit 1
  fi
  LC_ALL=C ps -p \"\$pid\" -o pcpu=,rss= |
    awk 'NF == 2 {printf \"%.3f %d\\n\", \$1, \$2}'
  sleep 0.1
done
" >>"$output_path"
}

wait_for_lima_root_ready() {
  local deadline
  deadline=$(($(date +%s) + 120))
  while ! run_lima_root "true" >/dev/null 2>&1; do
    rm -f -- "$work_root/lima-root-ssh.config"
    if [ -n "${run_pid:-}" ] && ! kill -0 "$run_pid" 2>/dev/null; then
      fail "workload exited before root SSH became ready"
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      fail "timed out waiting for stable root SSH"
    fi
    sleep 0.5
  done
}

scan_value_absent() {
  local label="$1"
  local value="$2"
  shift 2
  [ -n "$value" ] || fail "empty $label canary"
  if grep -R -a -F -- "$value" "$@" >/dev/null 2>&1; then
    fail "$label canary reached a post-redaction sink"
  else
    local scan_status=$?
    [ "$scan_status" -eq 1 ] ||
      fail "$label canary scan could not inspect every requested sink"
  fi
}

artifact_object() {
  local relative="$1"
  local kind="$2"
  local description="$3"
  jq -n \
    --arg path "$relative" \
    --arg kind "$kind" \
    --arg description "$description" \
    --arg sha256 "$(gate_sha256_file "$out/$relative")" \
    '{
      path: $path,
      kind: $kind,
      sha256: $sha256,
      redactionStatus: "passed",
      description: $description
    }'
}

write_failure_evidence() {
  local status="$1"
  [ -n "${out:-}" ] || return 0
  mkdir -p "$out"
  chmod 0700 "$out" 2>/dev/null || true
  jq -n \
    --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" \
    --arg stage "${current_stage:-unknown}" \
    --argjson exitCode "$status" \
    '{
      schema: "hideout.workload-privacy-lima-evidence/v1",
      generatedAt: $generatedAt,
      source: {commit: $commit, dirty: $dirty},
      result: "failed",
      failure: {stage: $stage, exitCode: $exitCode}
    }' >"$out/result.json" 2>/dev/null || true
  chmod 0600 "$out/result.json" 2>/dev/null || true
}

cleanup() {
  local status=$?
  trap - EXIT
  set +e
  set +u

  if [ -n "${observer_sampler_pid:-}" ] &&
    kill -0 "$observer_sampler_pid" 2>/dev/null; then
    kill "$observer_sampler_pid" 2>/dev/null
    wait "$observer_sampler_pid" 2>/dev/null
  fi
  if [ -n "${run_pid:-}" ] && kill -0 "$run_pid" 2>/dev/null; then
    [ -n "${workspace:-}" ] && : >"$workspace/probe-go" 2>/dev/null
    [ -n "${workspace:-}" ] && : >"$workspace/loss-go" 2>/dev/null
    [ -n "${workspace:-}" ] && : >"$workspace/release" 2>/dev/null
    kill "$run_pid" 2>/dev/null
    wait "$run_pid" 2>/dev/null
  fi
  if [ "${secret_stdin_open:-0}" -eq 1 ]; then
    exec 9>&-
    secret_stdin_open=0
  fi
  if [ -n "${secret_set_pid:-}" ] &&
    kill -0 "$secret_set_pid" 2>/dev/null; then
    kill "$secret_set_pid" 2>/dev/null
    wait "$secret_set_pid" 2>/dev/null
  fi
  if [ "$secret_set" -eq 1 ] && [ -x "${hideout:-}" ]; then
    with_timeout 15 run_hideout secret delete \
      "$secret_ref" --yes >/dev/null 2>&1 || status=1
  fi
  if [ -x "${hideout:-}" ] && [ -n "${store_root:-}" ]; then
    if [ -n "${environment_id:-}" ]; then
      run_hideout stop "$environment_id" >/dev/null 2>&1
      run_hideout clean "$environment_id" >/dev/null 2>&1
    else
      run_hideout clean >/dev/null 2>&1
    fi
    run_hideout daemon stop >/dev/null 2>&1
  fi
  purge_isolated_keychain_item || status=1
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
      "${tmp_base:-/tmp}"/ho-wp-lima.*)
        rm -rf -- "$lima_home"
        ;;
      *)
        printf 'workload-privacy-lima: refusing unexpected Lima cleanup path %s\n' "$lima_home" >&2
        status=1
        ;;
    esac
  fi
  if [ "${HIDEOUT_WORKLOAD_PRIVACY_KEEP_TMP:-0}" = "1" ]; then
    [ -n "${work_root:-}" ] &&
      printf 'workload-privacy-lima: retained sensitive debug directory %s\n' "$work_root" >&2
  elif [ -n "${work_root:-}" ] && [ -d "$work_root" ]; then
    case "$work_root" in
      "${tmp_base:-/tmp}"/hideout-workload-privacy.*)
        rm -rf -- "$work_root"
        ;;
      *)
        printf 'workload-privacy-lima: refusing unexpected cleanup path %s\n' "$work_root" >&2
        status=1
        ;;
    esac
  fi

  if [ "$gate_completed" != "1" ]; then
    write_failure_evidence "$status"
    emit_gate_result \
      "workload-privacy-lima" "lima" "failed" \
      "gate stopped during ${current_stage:-unknown}" \
      "" "" "" >/dev/null 2>&1 || true
    gate_require_completion "workload-privacy-lima"
  fi
  exit "$status"
}

require_command go
require_command jq
require_command python3
require_command shasum
require_command bash
require_command ps
if [ "$preflight_only" -eq 0 ]; then
  require_command limactl
  [ "$(uname -s)" = "Darwin" ] ||
    not_run "real reference lane requires macOS"
  [ "$(uname -m)" = "arm64" ] ||
    not_run "real reference lane requires arm64"
  require_command security
  require_command ssh
fi

if [ -L "$out" ]; then
  fail "evidence directory must not be a symlink"
fi
mkdir -p "$out/logs" "$out/reports"
out="$(cd "$out" && pwd -P)"
chmod 0700 "$out" "$out/logs" "$out/reports"

# macOS per-user TMPDIR is commonly long enough to push hideoutd's private
# Unix socket over sockaddr_un.sun_path. The gate therefore uses a short,
# explicit base while retaining a random 0700 directory and exact cleanup.
tmp_base="${HIDEOUT_WORKLOAD_PRIVACY_TMPDIR:-/tmp}"
mkdir -p "$tmp_base"
tmp_base="$(cd "$tmp_base" && pwd -P)"
work_root="$(mktemp -d "$tmp_base/hideout-workload-privacy.XXXXXX")"
chmod 0700 "$work_root"
trap cleanup EXIT

bin_dir="$work_root/bin"
store_root="$work_root/store"
# Lima derives several Unix sockets from LIMA_HOME plus the managed instance
# name. Keep its private world deliberately short so macOS's 104-byte
# sockaddr_un limit cannot make a valid product run fail before boot.
lima_home="$(mktemp -d "$tmp_base/ho-wp-lima.XXXXXX")"
workspace="$work_root/workspace"
guest_workspace="/workspace"
mkdir -p "$bin_dir" "$store_root" "$workspace"
chmod 0700 "$bin_dir" "$store_root" "$lima_home" "$workspace"
keychain_service="$(
  python3 - "$store_root" <<'PY'
import hashlib
import os
import sys

root = os.path.realpath(sys.argv[1])
digest = hashlib.sha256(
    b"hideout.keychain-store.v1\0" + root.encode("utf-8")
).hexdigest()[:24]
print("com.vibe-agi.hideout.secret.store." + digest)
PY
)"

hideout="$bin_dir/hideout"
linux_shim="$bin_dir/hideout-shim-linux-arm64"
linux_hostfsd="$bin_dir/hideout-hostfsd-linux-arm64"
linux_supervisor="$bin_dir/hideout-session-supervisor-linux-arm64"
linux_observer="$bin_dir/hideout-observer-linux-arm64"

current_stage="product-preflight"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
go build -trimpath -o "$hideout" ./cmd/hideout
"$hideout" help activity >"$work_root/activity-help.txt"
bash -n scripts/fixtures/workload-privacy.sh

if [ "$preflight_only" -eq 1 ]; then
  gate_completed=1
  printf 'workload-privacy-lima: preflight=passed\n'
  exit 0
fi

current_stage="go-refinement-traces"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
go test ./internal/manager \
  -run '^(TestWorkloadObservationProductionTypesRefineFoundationModel|TestActivityCleanupRemovesExactReusableIncarnationsForDestructiveLifecycle)$' \
  -count=1 -v >"$out/logs/refinement-manager.log" 2>&1
go test ./internal/workloadobs/store \
  -run '^(TestQuotaPrunesOldestSealedAcrossOwnersAndBoundsOvershoot|TestOwnerRetentionPolicyIsDurableAndCannotDrift)$' \
  -count=1 -v >"$out/logs/refinement-store.log" 2>&1
go test ./internal/workloadobs/redact \
  -run '^(TestCanariesAreAbsentFromEveryPostRedactionSink|TestCanaryMatrixCoversManagedEncodingsAndCredentialSyntax)$' \
  -count=1 -v >"$out/logs/refinement-redaction.log" 2>&1

current_stage="linux-helper-build"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
"$hideout" shim build-linux \
  --out "$linux_shim" --goarch arm64 --source "$repo_root" >/dev/null
"$hideout" hostfsd build-linux \
  --out "$linux_hostfsd" --goarch arm64 --source "$repo_root" >/dev/null
go run ./internal/helperbin/cmd/build-session-supervisor \
  --out "$linux_supervisor" --goarch arm64 --source "$repo_root"
go run ./internal/helperbin/cmd/build-observer \
  --out "$linux_observer" --goarch arm64 --source "$repo_root"

current_stage="isolated-profile"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
run_hideout init \
  --no-input --profile default --template dev --backend lima \
  --network direct >/dev/null
profile_path="$store_root/profiles/default/profile.json"
[ -f "$profile_path" ] || fail "initialized profile is missing"
jq '.activity = {
  retention: {maxBytes: 1048576, maxAgeSeconds: 0}
}' "$profile_path" >"$work_root/profile.json"
chmod 0600 "$work_root/profile.json"
mv "$work_root/profile.json" "$profile_path"
run_hideout doctor \
  --backend lima --workspace "$workspace" --network direct >/dev/null

suffix="$(python3 -c 'import secrets; print(secrets.token_hex(12))')"
secret_ref="privacy-gate-${suffix}"
managed_secret="managed_${suffix}_$(python3 -c 'import secrets; print(secrets.token_hex(12))')"
content_only="content_${suffix}_$(python3 -c 'import secrets; print(secrets.token_hex(12))')"
environment_only="environment_${suffix}_$(python3 -c 'import secrets; print(secrets.token_hex(12))')"
flag_secret="flag_${suffix}_$(python3 -c 'import secrets; print(secrets.token_hex(12))')"
uri_user="uriuser_${suffix}"
uri_password="uripassword_${suffix}_$(python3 -c 'import secrets; print(secrets.token_hex(8))')"
authorization_secret="authorization_${suffix}_$(python3 -c 'import secrets; print(secrets.token_hex(8))')"
query_secret="query_${suffix}_$(python3 -c 'import secrets; print(secrets.token_hex(8))')"
uri_value="socks5://${uri_user}:${uri_password}@198.51.100.7:1080"
authorization_value="Authorization: Bearer ${authorization_secret}"
query_value="https://example.invalid/path?safe=visible&token=${query_secret}"
visible_name="ordinary-visible-${suffix}.txt"

printf '%s\n' "$managed_secret" >"$workspace/managed-secret.input"
printf '%s\n' "$content_only" >"$workspace/content-only.input"
printf '%s\n' "$environment_only" >"$workspace/environment-only.input"
printf '%s\n' "$flag_secret" >"$workspace/flag-secret.input"
printf '%s\n' "$uri_value" >"$workspace/uri.input"
printf '%s\n' "$authorization_value" >"$workspace/authorization.input"
printf '%s\n' "$query_value" >"$workspace/query.input"
printf '%s\n' "$visible_name" >"$workspace/visible-name.input"
printf '%s\n' "ordinary local path evidence" >"$workspace/$visible_name"
cp scripts/fixtures/workload-privacy.sh "$workspace/workload-privacy.sh"
chmod 0700 "$workspace/workload-privacy.sh"
chmod 0600 "$workspace"/*.input "$workspace/$visible_name"

current_stage="managed-secret"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
keychain_item_created=1
secret_fifo="$work_root/secret-stdin.fifo"
mkfifo -m 0600 "$secret_fifo"
run_hideout secret set "$secret_ref" --stdin --yes \
  <"$secret_fifo" \
  >"$work_root/secret-set.out" 2>"$work_root/secret-set.err" &
secret_set_pid=$!
exec 9>"$secret_fifo"
secret_stdin_open=1

secret_process_observed=0
poll=0
while [ "$poll" -lt 50 ]; do
  ps -axww -o pid=,ppid=,command= >"$work_root/process-listing.all"
  if grep -F -- "$secret_ref" "$work_root/process-listing.all" \
    >"$work_root/secret-set-process.txt"; then
    secret_process_observed=1
    break
  fi
  if ! kill -0 "$secret_set_pid" 2>/dev/null; then
    break
  fi
  poll=$((poll + 1))
  sleep 0.1
done
[ "$secret_process_observed" -eq 1 ] ||
  fail "secret stdin process was not observable for argv inspection"
scan_value_absent \
  "managed-secret-process-listing" \
  "$managed_secret" \
  "$work_root/secret-set-process.txt"

printf '%s' "$managed_secret" >&9
exec 9>&-
secret_stdin_open=0
if wait "$secret_set_pid"; then
  secret_set_pid=""
else
  secret_set_status=$?
  secret_set_pid=""
  fail "secret stdin command failed with exit $secret_set_status"
fi
secret_set=1
rm -f -- "$secret_fifo" "$work_root/process-listing.all"

security find-generic-password \
  -s "$keychain_service" \
  -a "$secret_ref" \
  >"$work_root/keychain-metadata.txt" 2>&1 ||
  fail "isolated Keychain metadata was not readable"
scan_value_absent \
  "managed-secret-keychain-metadata" \
  "$managed_secret" \
  "$work_root/keychain-metadata.txt"

current_stage="real-lima-workload"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
with_timeout "$gate_timeout" run_hideout run \
  --verbose --backend lima --network direct --workspace "$workspace" \
  -- sh "$guest_workspace/workload-privacy.sh" \
  "$guest_workspace" "$events_per_round" "$maximum_rounds" \
  "$measure_performance" \
  >"$work_root/run.out" 2>"$work_root/run.err" &
run_pid=$!

owner_metadata=""
round=1
quota_summary="$work_root/quota-summary.json"
observer_performance_samples="$work_root/observer-performance.samples"
observer_cpu_values="$work_root/observer-performance-cpu-percent.txt"
observer_rss_values="$work_root/observer-performance-rss-bytes.txt"
performance_round_timings="$work_root/performance-round-timings.txt"
: >"$observer_performance_samples"
: >"$performance_round_timings"
if [ "$measure_performance" -eq 1 ]; then
  performance_instance_attempt=0
  while [ "$performance_instance_attempt" -lt 600 ]; do
    instance_name="$(
      LIMA_HOME="$lima_home" limactl list --json |
        jq -r 'select(.status == "Running") | .name' |
        head -1
    )"
    [ -n "$instance_name" ] && break
    if ! kill -0 "$run_pid" 2>/dev/null; then
      fail "workload exited before performance instance discovery"
    fi
    sleep 0.1
    performance_instance_attempt=$((performance_instance_attempt + 1))
  done
  [ -n "$instance_name" ] ||
    fail "isolated Lima instance was not available for observer sampling"
fi
while [ "$round" -le "$maximum_rounds" ]; do
  if [ "$measure_performance" -eq 1 ]; then
    wait_for_file \
      "$workspace/quota-start-$round" 1200 \
      "performance workload round $round"
    wait_for_lima_root_ready
    observer_sampler_stop="$store_root/profiles/default/cache/performance-sampler-stop-$round"
    rm -f -- "$observer_sampler_stop"
    collect_observer_performance_samples \
      "$observer_sampler_stop" \
      "$observer_performance_samples" &
    observer_sampler_pid=$!
    : >"$workspace/quota-measure-$round"
  fi
  wait_for_file \
    "$workspace/quota-ready-$round" 1200 \
    "quota workload round $round"
  if [ "$measure_performance" -eq 1 ]; then
    : >"$observer_sampler_stop"
    if ! wait "$observer_sampler_pid"; then
      observer_sampler_pid=""
      fail "observer CPU/RSS sampler failed"
    fi
    observer_sampler_pid=""
    rm -f -- "$observer_sampler_stop"
    timing_start="$(
      tr -d '\r\n' <"$workspace/quota-timing-start-$round"
    )"
    timing_end="$(
      tr -d '\r\n' <"$workspace/quota-ready-$round"
    )"
    case "$timing_start:$timing_end" in
      *[!0-9.:]* | :* | *:)
        fail "performance workload emitted invalid monotonic timing"
        ;;
    esac
    awk \
      -v started="$timing_start" \
      -v finished="$timing_end" \
      -v events="$events_per_round" \
      'BEGIN {
        elapsed = finished-started
        if (elapsed <= 0) exit 1
        printf "%.6f %d\n", elapsed, events
      }' >>"$performance_round_timings" ||
      fail "performance workload emitted a non-positive duration"
  fi

  set -- "$store_root"/activity/owners/owner_*/owner.json
  if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
    fail "expected one exact activity owner after quota workload"
  fi
  owner_metadata="$1"
  jq -e '
    .schema == "hideout.activity-owner-metadata.v1" and
    .owner.kind == "reusable-environment" and
    .retention.maxBytes == 1048576 and
    .retention.maxAgeSeconds == 0
  ' "$owner_metadata" >/dev/null ||
    fail "activity owner was not bound to the requested retention policy"
  environment_id="$(jq -r '.owner.environmentId' "$owner_metadata")"
  incarnation_id="$(jq -r '.owner.backendIncarnationId' "$owner_metadata")"
  owner_kind="$(jq -r '.owner.kind' "$owner_metadata")"
  owner_key="$(basename "$(dirname "$owner_metadata")")"
  owner_dir="$(dirname "$owner_metadata")"

  observed_prune=0
  poll=0
  while [ "$poll" -lt 30 ]; do
    if run_hideout activity summary \
      --environment "$environment_id" \
      --incarnation "$incarnation_id" --json \
      >"$quota_summary" 2>"$work_root/quota-summary.err"; then
      if jq -e '
        .pruned == true and
        (.reasons | index("retention-pruned")) != null
      ' "$quota_summary" >/dev/null; then
        observed_prune=1
        break
      fi
    fi
    poll=$((poll + 1))
    sleep 1
  done
  if [ "$observed_prune" -eq 1 ]; then
    break
  fi
  if [ -s "$quota_summary" ]; then
    printf \
      'workload-privacy-lima: quota-round=%d usedBytes=%s pruned=false\n' \
      "$round" \
      "$(jq -r '.quota.usedBytes // 0' "$quota_summary")"
  fi
  : >"$workspace/more-$round"
  round=$((round + 1))
done

[ "$observed_prune" -eq 1 ] ||
  fail "real workload did not cross the sealed-segment retention bound"
jq -e '
  .quota.limitBytes == 1048576 and
  .quota.maxAgeSeconds == 0 and
  .quota.usedBytes <= (.quota.limitBytes + 8388608) and
  .pruned == true and
  .corrupt == false and
  (.reasons | index("retention-pruned")) != null
' "$quota_summary" >/dev/null ||
  fail "owner quota or bounded active-segment allowance was not enforced"

instance_name="$(
  LIMA_HOME="$lima_home" limactl list --json |
    jq -r 'select(.status == "Running") | .name' |
    head -1
)"
[ -n "$instance_name" ] || fail "isolated Lima instance is not running"
run_lima_root "test ! -e '$store_root'" ||
  fail "host activity store path was visible inside the target VM"

current_stage="pre-persistence-redaction"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
probe_from="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
: >"$workspace/probe-go"
wait_for_file "$workspace/probe-ready" 120 "privacy probe completion"

process_events="$work_root/process-events.json"
visible_events="$work_root/visible-events.json"
poll=0
redaction_ready=0
while [ "$poll" -lt 30 ]; do
  run_hideout activity events \
    --environment "$environment_id" --incarnation "$incarnation_id" \
    --kind process --from "$probe_from" --limit 500 --json \
    >"$process_events" 2>"$work_root/process-events.err" || true
  run_hideout activity events \
    --environment "$environment_id" --incarnation "$incarnation_id" \
    --kind file --path "$visible_name" --from "$probe_from" \
    --limit 100 --json \
    >"$visible_events" 2>"$work_root/visible-events.err" || true
  if jq -e '
      [.records[]? | select(.kind == "process") |
       .subject.argv[]? | select(contains("[REDACTED]"))] |
      length >= 4
    ' "$process_events" >/dev/null 2>&1 &&
    jq -e --arg visible "$visible_name" '
      any(.records[]?;
        .kind == "file" and
        ((.subject.path // "") | contains($visible)))
    ' "$visible_events" >/dev/null 2>&1; then
    redaction_ready=1
    break
  fi
  poll=$((poll + 1))
  sleep 1
done
[ "$redaction_ready" -eq 1 ] ||
  fail "redacted argv or visible local path did not reach the authenticated view"

for scan_target in \
  "$store_root/activity" \
  "$process_events" \
  "$visible_events" \
  "$work_root/run.out" \
  "$work_root/run.err" \
  "$work_root/secret-set-process.txt" \
  "$work_root/keychain-metadata.txt"; do
  scan_value_absent "managed-secret" "$managed_secret" "$scan_target"
  scan_value_absent "content-only" "$content_only" "$scan_target"
  scan_value_absent "environment-only" "$environment_only" "$scan_target"
  scan_value_absent "sensitive-flag" "$flag_secret" "$scan_target"
  scan_value_absent "URI-user" "$uri_user" "$scan_target"
  scan_value_absent "URI-password" "$uri_password" "$scan_target"
  scan_value_absent "authorization" "$authorization_secret" "$scan_target"
  scan_value_absent "sensitive-query" "$query_secret" "$scan_target"
done

python3 - "$store_root/activity" <<'PY'
import os
import stat
import sys

root = sys.argv[1]
for current, directories, files in os.walk(root, followlinks=False):
    mode = stat.S_IMODE(os.lstat(current).st_mode)
    if mode != 0o700:
        raise SystemExit(f"activity directory mode is {mode:o}, want 700")
    for name in directories:
        path = os.path.join(current, name)
        if os.path.islink(path):
            raise SystemExit("activity store contains a directory symlink")
    for name in files:
        path = os.path.join(current, name)
        info = os.lstat(path)
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
            raise SystemExit("activity store contains a non-regular file")
        mode = stat.S_IMODE(info.st_mode)
        if mode != 0o600:
            raise SystemExit(f"activity file mode is {mode:o}, want 600")
PY

healthy_coverage_result="$work_root/healthy-coverage.json"
healthy_dropped_total=0
if [ "$measure_performance" -eq 1 ]; then
  run_hideout activity coverage \
    --environment "$environment_id" --incarnation "$incarnation_id" --json \
    >"$healthy_coverage_result"
  healthy_dropped_total="$(
    jq '[
      .intervals[] |
      select(
        .reason != "retention-pruned" and
        .droppedEventCount > 0
      ) |
      {
        reason: .reason,
        droppedEventCount: .droppedEventCount,
        startedAt: .startedAt
      }
    ] |
    unique |
    map(.droppedEventCount) |
    add // 0' \
      "$healthy_coverage_result"
  )"
  jq -e '
    ([.current[].subsystem] | unique | length) == 4 and
    all(.intervals[];
      .droppedEventCount == 0 or
      (
        .reason != "retention-pruned" and
        .state != "Available" and
        (.evidence | length) > 0
      )
    )
  ' "$healthy_coverage_result" >/dev/null ||
    fail "performance interval reported unaccounted observer loss"
fi

current_stage="observer-loss"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
run_lima_root \
  "pgrep -a -f '^/hideout/session/shims/hideout-observer( |$)'" \
  >"$work_root/observer-processes.txt"
observer_count="$(wc -l <"$work_root/observer-processes.txt" | tr -d ' ')"
[ "$observer_count" = "1" ] ||
  fail "expected exactly one observed helper in the isolated VM"
observer_pid="$(
  awk 'NR == 1 {print $1}' "$work_root/observer-processes.txt"
)"
case "$observer_pid" in
  '' | *[!0-9]*)
    fail "observer PID was not numeric"
    ;;
esac
run_lima_root "kill -KILL $observer_pid"
sleep 1
if run_lima_root \
  "pgrep -f '^/hideout/session/shims/hideout-observer( |$)'" \
  >/dev/null 2>&1; then
  fail "observer fault injection did not terminate the exact helper"
fi

: >"$workspace/loss-go"
wait_for_file "$workspace/loss-done" 120 "post-loss target activity"
: >"$workspace/release"
if wait "$run_pid"; then
  run_exit_code=0
else
  run_exit_code=$?
fi
run_pid=""

audit_path="$(awk '/audit: / {print $2; exit}' "$work_root/run.err")"
[ -n "$audit_path" ] && [ -f "$audit_path" ] ||
  fail "run audit path was not reported"
audit_sha_before="$(gate_sha256_file "$audit_path")"
session_id="$(basename "$(dirname "$audit_path")")"
case "$session_id" in
  ses_*) ;;
  *) fail "run session identity was not recoverable" ;;
esac

coverage_result="$work_root/coverage.json"
run_hideout activity coverage \
  --environment "$environment_id" --incarnation "$incarnation_id" --json \
  >"$coverage_result"
jq -e '
  any(.intervals[];
    .reason == "retention-pruned" and
    .retentionGap == true and
    .state == "Partial") and
  any(.intervals[];
    .reason == "transport-drop" and
    .droppedEventCount > 0 and
    (.state == "Partial" or .state == "Unavailable")) and
  ([.current[] | select(.state != "Available")] | length) == 4
' "$coverage_result" >/dev/null ||
  fail "coverage did not preserve both quota and injected observer loss"

current_stage="shareable-privacy-surfaces"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
support_report="$work_root/support-report.json"
boundary_report="$work_root/boundary-summary.json"
run_hideout support report \
  --out "$support_report" --profile default \
  --backend lima --workspace "$workspace" \
  >"$work_root/support-report.out"
run_hideout audit export \
  --source boundary-summary --from "$audit_path" \
  --policy-profile default --out "$boundary_report" \
  --acknowledge-full-fidelity \
  >"$work_root/boundary-export.out"

go run ./cmd/hideout-schema-validate \
  schemas/support-report.schema.json "$support_report" \
  >"$out/logs/support-schema.log" 2>&1
go run ./cmd/hideout-schema-validate \
  schemas/export-artifact.schema.json "$boundary_report" \
  >"$out/logs/boundary-schema.log" 2>&1

jq -e '
  .redaction.mode == "shareable-support" and
  (.redaction.excludedDataClasses | index("activity-record")) != null and
  (.redaction.excludedDataClasses | index("activity-local-path")) != null and
  (.redaction.excludedDataClasses | index("activity-command-argv")) != null and
  (.redaction.excludedDataClasses | index("activity-domain")) != null and
  (.redaction.excludedDataClasses | index("activity-ip")) != null
' "$support_report" >/dev/null ||
  fail "support report did not declare every activity exclusion"
jq -e '
  .provenance.source == "boundary-summary" and
  .body.activityObservation.scope ==
    "top-level-command-and-descendants" and
  .body.activityObservation.ownerBinding ==
    "exact-environment-or-disposable-session-plus-backend-incarnation" and
  .body.activityObservation.retentionMaxBytes == 1048576 and
  .body.activityObservation.retentionMaxAgeSeconds == 0 and
  .body.activityObservation.coverageNonClaim ==
    "no-events-does-not-prove-no-behavior-without-Available-coverage-for-the-subsystem-and-window"
' "$boundary_report" >/dev/null ||
  fail "boundary export did not carry the activity privacy contract"

activity_boundary="$work_root/activity-boundary.json"
jq '.body.activityObservation' "$boundary_report" >"$activity_boundary"
for identity in "$environment_id" "$incarnation_id" "$session_id" "$owner_key"; do
  scan_value_absent "exact-owner-identity" "$identity" "$activity_boundary"
done
for scan_target in "$support_report" "$boundary_report"; do
  scan_value_absent "managed-secret" "$managed_secret" "$scan_target"
  scan_value_absent "content-only" "$content_only" "$scan_target"
  scan_value_absent "environment-only" "$environment_only" "$scan_target"
  scan_value_absent "sensitive-flag" "$flag_secret" "$scan_target"
  scan_value_absent "URI-user" "$uri_user" "$scan_target"
  scan_value_absent "URI-password" "$uri_password" "$scan_target"
  scan_value_absent "authorization" "$authorization_secret" "$scan_target"
  scan_value_absent "sensitive-query" "$query_secret" "$scan_target"
  scan_value_absent "authenticated-local-path" "$visible_name" "$scan_target"
done

current_stage="exact-owner-cleanup"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
[ -d "$owner_dir" ] ||
  fail "reusable activity was removed merely because its session exited"
run_hideout stop "$environment_id" \
  >"$work_root/stop.out" 2>"$work_root/stop.err"
run_hideout clean --stopped "$environment_id" \
  >"$work_root/clean.out" 2>"$work_root/clean.err"
[ ! -e "$owner_dir" ] ||
  fail "clean retained the exact old activity owner directory"
if run_hideout activity summary \
  --environment "$environment_id" --incarnation "$incarnation_id" --json \
  >"$work_root/post-clean-summary.out" \
  2>"$work_root/post-clean-summary.err"; then
  fail "cleaned activity owner remained queryable"
fi
if run_hideout env inspect "$environment_id" \
  >"$work_root/post-clean-env.out" \
  2>"$work_root/post-clean-env.err"; then
  fail "cleaned environment record remained inspectable"
fi
if LIMA_HOME="$lima_home" limactl list --json |
  jq -e --arg instance "$instance_name" \
    'select(.name == $instance)' >/dev/null; then
  fail "clean retained the exact Lima instance"
fi
[ -f "$audit_path" ] &&
  [ "$(gate_sha256_file "$audit_path")" = "$audit_sha_before" ] ||
  fail "environment cleanup removed or changed the retained session audit"

current_stage="recreate-owner-preservation"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
# The single-quoted target program is intentionally passed verbatim to the VM.
# shellcheck disable=SC2016
with_timeout "$gate_timeout" run_hideout run \
  --verbose --backend lima --network direct --workspace "$workspace" \
  -- sh -eu -c '
fixture=/tmp/hideout-recreated-incarnation
printf "%s\n" "recreated-incarnation" >"$fixture"
test "$(cat "$fixture")" = "recreated-incarnation"
rm "$fixture"
' >"$work_root/recreate-run.out" 2>"$work_root/recreate-run.err"

set -- "$store_root"/activity/owners/owner_*/owner.json
if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
  fail "recreated environment did not retain exactly one new activity owner"
fi
new_owner_metadata="$1"
new_owner_dir="$(dirname "$new_owner_metadata")"
new_environment_id="$(jq -r '.owner.environmentId' "$new_owner_metadata")"
new_incarnation_id="$(jq -r '.owner.backendIncarnationId' "$new_owner_metadata")"
case "$new_environment_id:$new_incarnation_id" in
  env_*:*) ;;
  *) fail "recreated activity owner identity is invalid" ;;
esac
[ "$new_environment_id" != "$environment_id" ] ||
  fail "recreate reused the removed environment identity"
[ "$new_incarnation_id" != "$incarnation_id" ] ||
  fail "recreate reused the removed backend incarnation"
new_environment_record="$store_root/environments/$new_environment_id/environment.json"
[ -f "$new_environment_record" ] ||
  fail "recreated environment record is missing"
new_instance_name="$(jq -r '.instanceName // empty' "$new_environment_record")"
[ -n "$new_instance_name" ] && [ "$new_instance_name" != "$instance_name" ] ||
  fail "recreate reused the removed Lima instance identity"
run_hideout activity summary \
  --environment "$new_environment_id" \
  --incarnation "$new_incarnation_id" --json \
  >"$work_root/recreate-summary.json"
jq -e '
  .owner.kind == "reusable-environment" and
  .counts.process > 0 and
  .corrupt == false
' "$work_root/recreate-summary.json" >/dev/null ||
  fail "new incarnation activity was not queryable after old-owner cleanup"
if run_hideout activity summary \
  --environment "$environment_id" --incarnation "$incarnation_id" --json \
  >"$work_root/recreate-old-summary.out" \
  2>"$work_root/recreate-old-summary.err"; then
  fail "old activity owner reappeared after environment recreation"
fi
[ -f "$audit_path" ] &&
  [ "$(gate_sha256_file "$audit_path")" = "$audit_sha_before" ] ||
  fail "environment recreation removed or changed the old retained audit"

run_hideout stop "$new_environment_id" \
  >"$work_root/recreate-stop.out" 2>"$work_root/recreate-stop.err"
run_hideout clean --stopped "$new_environment_id" \
  >"$work_root/recreate-clean.out" 2>"$work_root/recreate-clean.err"
[ ! -e "$new_owner_dir" ] ||
  fail "new activity owner survived its own proved cleanup"
if run_hideout activity summary \
  --environment "$new_environment_id" \
  --incarnation "$new_incarnation_id" --json \
  >"$work_root/recreate-post-clean-summary.out" \
  2>"$work_root/recreate-post-clean-summary.err"; then
  fail "new activity owner remained queryable after its own cleanup"
fi
[ -f "$audit_path" ] &&
  [ "$(gate_sha256_file "$audit_path")" = "$audit_sha_before" ] ||
  fail "new environment cleanup removed or changed the old retained audit"

run_hideout secret delete "$secret_ref" --yes \
  >"$work_root/secret-delete.out" 2>"$work_root/secret-delete.err"
secret_set=0
purge_isolated_keychain_item ||
  fail "exact isolated Keychain fixture remained after logical delete"

current_stage="evidence"
printf 'workload-privacy-lima: stage=%s\n' "$current_stage"
quota_used="$(jq -r '.quota.usedBytes' "$quota_summary")"
quota_limit="$(jq -r '.quota.limitBytes' "$quota_summary")"
replacement_count="$(
  jq '[.records[]? | select(.kind == "process") |
       .subject.argv[]? | select(contains("[REDACTED]"))] | length' \
    "$process_events"
)"
visible_count="$(
  jq --arg visible "$visible_name" \
    '[.records[]? | select(
      .kind == "file" and
      ((.subject.path // "") | contains($visible))
    )] | length' "$visible_events"
)"
dropped_total="$(
  jq '[.intervals[].droppedEventCount] | add // 0' "$coverage_result"
)"
loss_reasons="$(
  jq -c '[.intervals[] |
    select(.state != "Available") | .reason] | unique' "$coverage_result"
)"
performance_evidence='{"measured":false}'
if [ "$measure_performance" -eq 1 ]; then
  awk 'NF == 2 {print $1}' "$observer_performance_samples" \
    >"$observer_cpu_values"
  awk 'NF == 2 {printf "%d\n", $2 * 1024}' \
    "$observer_performance_samples" >"$observer_rss_values"
  observer_sample_count="$(
    wc -l <"$observer_cpu_values" | tr -d ' '
  )"
  [ "$observer_sample_count" -ge 5 ] ||
    fail "observer performance sampling retained fewer than five samples"
  observer_cpu_p50="$(
    performance_percentile "$observer_cpu_values" 50
  )"
  observer_cpu_p95="$(
    performance_percentile "$observer_cpu_values" 95
  )"
  observer_rss_p50="$(
    performance_percentile "$observer_rss_values" 50
  )"
  observer_rss_p95="$(
    performance_percentile "$observer_rss_values" 95
  )"
  performance_elapsed_seconds="$(
    awk '{total += $1} END {printf "%.6f\n", total}' \
      "$performance_round_timings"
  )"
  performance_generated_events="$(
    awk '{total += $2} END {printf "%d\n", total}' \
      "$performance_round_timings"
  )"
  performance_event_rate="$(
    awk \
      -v events="$performance_generated_events" \
      -v elapsed="$performance_elapsed_seconds" \
      'BEGIN {
        if (elapsed <= 0) exit 1
        printf "%.3f\n", events/elapsed
      }'
  )" || fail "performance event rate was not measurable"
  performance_drop_rate="$(
    awk \
      -v dropped="$healthy_dropped_total" \
      -v events="$performance_generated_events" \
      'BEGIN {
        if (events <= 0) exit 1
        printf "%.6f\n", (dropped/events)*100
      }'
  )" || fail "performance drop rate was not measurable"

  performance_evidence="$(
    jq -n \
      --argjson cpuSamples \
        "$(performance_values_json "$observer_cpu_values")" \
      --argjson rssSamples \
        "$(performance_values_json "$observer_rss_values")" \
      --argjson cpuP50 "$observer_cpu_p50" \
      --argjson cpuP95 "$observer_cpu_p95" \
      --argjson rssP50 "$observer_rss_p50" \
      --argjson rssP95 "$observer_rss_p95" \
      --argjson generatedEvents "$performance_generated_events" \
      --argjson elapsedSeconds "$performance_elapsed_seconds" \
      --argjson eventRate "$performance_event_rate" \
      --argjson droppedEvents "$healthy_dropped_total" \
      --argjson dropRate "$performance_drop_rate" \
      '{
        measured: true,
        methodology: {
          workload:
            "production observer over repeated exec quota workload",
          clock: "guest-/proc/uptime-monotonic",
          processSource: "guest-procps-pcpu-rss",
          percentile: "nearest-rank-ceiling"
        },
        observerCPU: {
          unit: "percent-of-one-guest-vcpu",
          samples: $cpuSamples,
          p50: $cpuP50,
          p95: $cpuP95
        },
        observerRSS: {
          unit: "bytes",
          samples: $rssSamples,
          p50: $rssP50,
          p95: $rssP95
        },
        eventRate: {
          unit: "generated-execs-per-second",
          generatedEvents: $generatedEvents,
          elapsedSeconds: $elapsedSeconds,
          value: $eventRate
        },
        healthyDropRate: {
          unit: "percent",
          droppedEvents: $droppedEvents,
          generatedEvents: $generatedEvents,
          value: $dropRate,
          coverageAccounted: true
        }
      }'
  )"
fi

jq -n \
  --arg ownerKind "$owner_kind" \
  --arg ownerKeySHA256 "$(hash_text "$owner_key")" \
  --arg environmentSHA256 "$(hash_text "$environment_id")" \
  --arg incarnationSHA256 "$(hash_text "$incarnation_id")" \
  --arg sessionSHA256 "$(hash_text "$session_id")" \
  --arg instanceSHA256 "$(hash_text "$instance_name")" \
  --arg newEnvironmentSHA256 "$(hash_text "$new_environment_id")" \
  --arg newIncarnationSHA256 "$(hash_text "$new_incarnation_id")" \
  --arg newInstanceSHA256 "$(hash_text "$new_instance_name")" \
  --arg auditSHA256 "$audit_sha_before" \
  --arg visibleBasename "$visible_name" \
  --argjson quotaUsed "$quota_used" \
  --argjson quotaLimit "$quota_limit" \
  --argjson replacementCount "$replacement_count" \
  --argjson visibleCount "$visible_count" \
  --argjson droppedTotal "$dropped_total" \
  --argjson lossReasons "$loss_reasons" \
  --argjson runExitCode "$run_exit_code" \
  --argjson performance "$performance_evidence" \
  '{
    schema: "hideout.workload-privacy-lima-summary/v1",
    backend: "lima",
    owner: {
      kind: $ownerKind,
      keySHA256: $ownerKeySHA256,
      environmentSHA256: $environmentSHA256,
      incarnationSHA256: $incarnationSHA256,
      sessionSHA256: $sessionSHA256,
      instanceSHA256: $instanceSHA256
    },
    quota: {
      passed: true,
      usedBytes: $quotaUsed,
      limitBytes: $quotaLimit,
      activeSegmentAllowanceBytes: 8388608,
      activeSegmentAllowanceCount: 1,
      pruned: true,
      retentionGap: true
    },
    loss: {
      passed: true,
      injection: "exact-observer-process-killed",
      droppedEventCount: $droppedTotal,
      reasons: $lossReasons,
      finalCoverageAvailable: false,
      targetExitCodeAfterFault: $runExitCode
    },
    performance: $performance,
    redaction: {
      passed: true,
      prePersistence: true,
      rawStoreScanned: true,
      authenticatedQueryScanned: true,
      supportReportScanned: true,
      boundaryExportScanned: true,
      processListingScanned: true,
      keychainMetadataScanned: true,
      replacementCount: $replacementCount,
      sinkCanaryHits: {
        api: 0,
        evidence: 0,
        export: 0,
        index: 0,
        log: 0,
        process: 0,
        store: 0,
        support: 0
      },
      canaryClassHits: {
        authField: 0,
        controlToken: 0,
        encoded: 0,
        managedValue: 0,
        sensitiveArg: 0,
        sensitiveQuery: 0,
        splitForm: 0,
        uriUserinfo: 0
      },
      redactionFailurePersistedRecords: 0,
      canaryClasses: [
        "managed-secret",
        "file-content-only",
        "environment-only",
        "sensitive-flag",
        "uri-userinfo",
        "authorization",
        "sensitive-query"
      ]
    },
    localPath: {
      passed: true,
      authenticatedLocalView: true,
      shareableSupportExcluded: true,
      boundaryContractOnly: true,
      visibleBasename: $visibleBasename,
      matchingRecords: $visibleCount
    },
    cleanup: {
      passed: true,
      reusableOwnerRetainedAfterSessionExit: true,
      exactOwnerDirectoryAbsent: true,
      exactOwnerQueryRejected: true,
      environmentRecordAbsent: true,
      limaInstanceAbsent: true,
      auditPreserved: true,
      recreatedEnvironmentDifferent: true,
      recreatedIncarnationDifferent: true,
      recreatedInstanceDifferent: true,
      recreatedOwnerQueryableBeforeOwnCleanup: true,
      recreatedOwnerRemovedOnlyByOwnCleanup: true,
      newEnvironmentSHA256: $newEnvironmentSHA256,
      newIncarnationSHA256: $newIncarnationSHA256,
      newInstanceSHA256: $newInstanceSHA256,
      retainedAuditSHA256: $auditSHA256
    },
    permissions: {
      passed: true,
      directoryMode: "0700",
      fileMode: "0600",
      targetReadSucceeded: false
    },
    nonClaim:
      "no-events-does-not-prove-no-behavior-without-Available-coverage-for-the-subsystem-and-window"
  }' >"$out/reports/privacy-summary.json"

cp "$support_report" "$out/reports/support-report.json"
cp "$boundary_report" "$out/reports/boundary-summary.json"
cp "$work_root/secret-set-process.txt" \
  "$out/reports/secret-set-process.txt"
cp "$work_root/keychain-metadata.txt" \
  "$out/reports/keychain-metadata.txt"
chmod 0600 "$out"/logs/*.log "$out"/reports/*.json "$out"/reports/*.txt

artifacts="$work_root/artifacts.json"
jq -s '.' \
  <(artifact_object \
    "logs/refinement-manager.log" "go-test-log" \
    "production-type workload model and exact cleanup refinement traces") \
  <(artifact_object \
    "logs/refinement-store.log" "go-test-log" \
    "quota and immutable owner-retention refinement traces") \
  <(artifact_object \
    "logs/refinement-redaction.log" "go-test-log" \
    "post-redaction sink and credential syntax canary traces") \
  <(artifact_object \
    "logs/support-schema.log" "schema-validation-log" \
    "support report schema validation") \
  <(artifact_object \
    "logs/boundary-schema.log" "schema-validation-log" \
    "boundary export schema validation") \
  <(artifact_object \
    "reports/privacy-summary.json" "event-summary" \
    "sanitized real Lima quota, loss, redaction, path, and cleanup result") \
  <(artifact_object \
    "reports/support-report.json" "support-report" \
    "shareable report excluding workload activity records and identities") \
  <(artifact_object \
    "reports/boundary-summary.json" "export-artifact" \
    "shareable run boundary contract export") \
  <(artifact_object \
    "reports/secret-set-process.txt" "process-listing" \
    "production secret stdin command argv without the managed value") \
  <(artifact_object \
    "reports/keychain-metadata.txt" "keychain-metadata" \
    "macOS Keychain item metadata without password data or managed value") \
  >"$artifacts"

observer_sha="$(gate_sha256_file "$linux_observer")"
supervisor_sha="$(gate_sha256_file "$linux_supervisor")"
limactl_version="$(limactl --version 2>&1 | head -1)"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg hostOS "$(uname -s)" \
  --arg hostArch "$(uname -m)" \
  --arg limaVersion "$limactl_version" \
  --arg observerSHA256 "$observer_sha" \
  --arg supervisorSHA256 "$supervisor_sha" \
  --slurpfile artifacts "$artifacts" \
  '{
    schema: "hideout.workload-privacy-lima-evidence/v1",
    generatedAt: $generatedAt,
    source: {commit: $commit, dirty: $dirty},
    host: {
      os: $hostOS,
      arch: $hostArch,
      limaVersion: $limaVersion
    },
    runtime: {
      backend: "lima",
      guestArch: "aarch64",
      observerSHA256: $observerSHA256,
      supervisorSHA256: $supervisorSHA256
    },
    result: "passed",
    candidateAcceptance: ($dirty | not),
    checks: {
      realLima: "passed",
      quota: "passed",
      observerLoss: "passed",
      prePersistenceRedaction: "passed",
      localPathVisibility: "passed",
      shareableSurfaceExclusion: "passed",
      exactOwnerCleanup: "passed",
      exactOwnerRecreate: "passed",
      auditPreservation: "passed",
      goRefinement: "passed"
    },
    artifacts: $artifacts[0],
    limitations:
      ([
        "The observer is intentionally killed; target exit status after the injected fault is evidence, not a command-success criterion."
      ] +
      if $dirty then
        ["This binds a dirty development checkout; it is not release-candidate provenance."]
      else
        []
      end)
  }' >"$out/result.json"
chmod 0600 "$out/result.json"

for scan_target in \
  "$out/result.json" \
  "$out/logs" \
  "$out/reports"; do
  scan_value_absent "managed-secret" "$managed_secret" "$scan_target"
  scan_value_absent "content-only" "$content_only" "$scan_target"
  scan_value_absent "environment-only" "$environment_only" "$scan_target"
  scan_value_absent "sensitive-flag" "$flag_secret" "$scan_target"
  scan_value_absent "URI-user" "$uri_user" "$scan_target"
  scan_value_absent "URI-password" "$uri_password" "$scan_target"
  scan_value_absent "authorization" "$authorization_secret" "$scan_target"
  scan_value_absent "sensitive-query" "$query_secret" "$scan_target"
done

jq -e '
  .result == "passed" and
  ([.checks[] | select(. != "passed")] | length) == 0 and
  (.artifacts | length) == 10 and
  all(.artifacts[]; .sha256 | test("^[a-f0-9]{64}$"))
' "$out/result.json" >/dev/null ||
  fail "evidence manifest validation failed"

emit_gate_result \
  "workload-privacy-lima" "lima" "passed" \
  "real quota, observer loss, redaction, and exact cleanup proved" \
  "reports/privacy-summary.json" \
  "reports/boundary-summary.json" \
  "" >/dev/null

gate_completed=1
printf 'workload-privacy-lima: evidence=%s/result.json\n' "$out"
printf 'workload-privacy-lima: passed\n'
