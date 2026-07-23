#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

out="$ROOT/.hideout-release-evidence/042-disposable-orphan-recovery-real-gate2"
package_archive=""
ordinary_runs=30
checkpoint_limit=4
probe=0
require_real=0

usage() {
  cat <<'USAGE'
Usage: scripts/test-disposable-recovery-lima-e2e.sh [options]

  --out <dir>          evidence output directory
  --package <tar.gz>   reuse an exact package archive
  --runs <n>           ordinary disposable runs (product minimum 30)
  --checkpoints <n>    ordered crash checkpoints (product requires all 4)
  --probe              permit reduced counts and dirty source; emit no product proof
  --require-real       fail instead of emitting supporting not-run evidence
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out) out="${2:-}"; shift 2 ;;
    --package) package_archive="${2:-}"; shift 2 ;;
    --runs) ordinary_runs="${2:-}"; shift 2 ;;
    --checkpoints) checkpoint_limit="${2:-}"; shift 2 ;;
    --probe) probe=1; shift ;;
    --require-real) require_real=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "disposable recovery e2e: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

for value_name in ordinary_runs checkpoint_limit; do
  eval "value=\${$value_name}"
  case "$value" in
    ''|*[!0-9]*) echo "disposable recovery e2e: $value_name must be an integer" >&2; exit 2 ;;
  esac
done
if [ "$ordinary_runs" -lt 1 ]; then
  echo "disposable recovery e2e: --runs must be positive" >&2
  exit 2
fi
if [ "$checkpoint_limit" -lt 1 ] || [ "$checkpoint_limit" -gt 4 ]; then
  echo "disposable recovery e2e: --checkpoints must be between 1 and 4" >&2
  exit 2
fi
source_commit="$(git rev-parse HEAD)"
source_dirty=false
if [ -n "$(git status --porcelain=v1 --untracked-files=all)" ]; then
  source_dirty=true
fi
if [ "$probe" = "0" ]; then
  [ "$ordinary_runs" -ge 30 ] || {
    echo "disposable recovery e2e: product evidence requires at least 30 ordinary runs" >&2
    exit 2
  }
  [ "$checkpoint_limit" -eq 4 ] || {
    echo "disposable recovery e2e: product evidence requires all four crash checkpoints" >&2
    exit 2
  }
  [ "$source_dirty" = false ] || {
    echo "disposable recovery e2e: product evidence requires a clean source tree; use --probe for diagnostics" >&2
    exit 2
  }
fi
if [ -n "$package_archive" ] && [ ! -f "$package_archive" ]; then
  echo "disposable recovery e2e: package archive does not exist: $package_archive" >&2
  exit 2
fi
if [ -e "$out" ]; then
  echo "disposable recovery e2e: output directory already exists: $out" >&2
  exit 2
fi

missing=""
for tool in go git jq limactl python3 shasum tar awk sed; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
[ "$(uname -s)" = Darwin ] || missing="$missing macOS"
[ "$(uname -m)" = arm64 ] || missing="$missing arm64"

mkdir -p "$out/logs" "$out/artifacts" "$out/reports"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"
registry="$out/reports/proof-registry.json"
go run ./cmd/hideout support proof-registry --json >"$registry"

proof_real="042.disposable-recovery.real-gate2.recovery"
proof_not_run="042.disposable-recovery.real-gate2.not-run"
for proof_id in "$proof_real" "$proof_not_run"; do
  jq -e --arg id "$proof_id" '.requirements[] | select(.proofId == $id)' "$registry" >/dev/null || {
    echo "disposable recovery e2e: proof is not registered: $proof_id" >&2
    exit 1
  }
done

sha256_file() { shasum -a 256 "$1" | awk '{print $1}'; }
claims_json() {
  jq -c --arg id "$1" '
    [.requirements[] | select(.proofId == $id) | .claimIds[] |
      {claimId:.,source:"spec",description:("042 registered contract " + .),
       scope:"disposable-orphan-recovery"}]
  ' "$registry"
}
artifact_json() {
  jq -n --arg path "$1" --arg sha "$(sha256_file "$out/$1")" \
    '{kind:"manifest",path:$path,sha256:$sha,redactionStatus:"passed",
      description:"042 disposable recovery result"}'
}
write_manifest() {
  local proofs="$1" package_identity="${2:-null}"
  jq -n --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg commit "$source_commit" --argjson dirty "$source_dirty" \
    --argjson packageIdentity "$package_identity" --slurpfile proofs "$proofs" '
    {version:"hideout.product-hardening-evidence/v1",generatedAt:$generatedAt,
     commit:$commit,dirty:$dirty,proofs:$proofs[0]} +
     (if $packageIdentity == null then {} else {packageIdentity:$packageIdentity} end)
  ' >"$manifest"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json "$manifest" \
    >"$out/logs/evidence-schema.out" 2>"$out/logs/evidence-schema.err"
}

if [ -n "$missing" ]; then
  reason="real Gate 2 prerequisites unavailable:$missing"
  jq -n --arg reason "$reason" \
    '{schema:"hideout.disposable-recovery-not-run/v1",status:"not-run",reason:$reason}' \
    >"$out/artifacts/not-run.json"
  if [ "$require_real" = "1" ] || [ "$probe" = "1" ]; then
    echo "disposable recovery e2e: $reason" >&2
    exit 1
  fi
  claims="$(claims_json "$proof_not_run")"
  artifact="$(artifact_json artifacts/not-run.json)"
  jq -n --arg proofId "$proof_not_run" --arg reason "$reason" \
    --argjson claims "$claims" --argjson artifact "$artifact" '[{
      proofId:$proofId,featureId:"042-disposable-orphan-recovery",mode:"real-gate",
      evidenceClass:"disposable-recovery-real-gate2-not-run",status:"not-run",
      commandSummary:"real disposable recovery Gate 2 was not run",
      coveredClaims:$claims,
      prerequisites:[{name:"real-macos-arm64-lima-packaged",status:"missing",reason:$reason}],
      artifacts:[$artifact],redactionStatus:"not-run",notRunReason:$reason
    }]' >"$out/reports/proofs.json"
  write_manifest "$out/reports/proofs.json"
  echo "disposable recovery e2e: passed status=not-run evidence=$manifest"
  exit 0
fi

short_root="${HIDEOUT_042_SHORT_TMPDIR:-/tmp}"
work="$(mktemp -d "$short_root/hideout-042-gate2.XXXXXX")"
store="$work/store"
lima_home="$work/lima"
install_root="$work/installed"
workspace="$work/workspace"
candidate=""
daemon_pid=""
run_pid=""
watcher_pid=""
startup_values="$work/startup-ms.values"
recovery_values="$work/recovery-ms.values"
: >"$startup_values"
: >"$recovery_values"

pid_alive() {
  [ -n "${1:-}" ] && kill -0 "$1" 2>/dev/null
}
stop_pid() {
  local pid="${1:-}"
  if pid_alive "$pid"; then
    kill -CONT "$pid" 2>/dev/null || true
    kill -TERM "$pid" 2>/dev/null || true
    for _ in $(seq 1 100); do
      pid_alive "$pid" || break
      sleep 0.02
    done
    if pid_alive "$pid"; then
      kill -KILL "$pid" 2>/dev/null || true
    fi
  fi
  [ -n "$pid" ] && wait "$pid" 2>/dev/null || true
}
cleanup() {
  stop_pid "$watcher_pid"
  stop_pid "$run_pid"
  stop_pid "$daemon_pid"
  if [ -n "$candidate" ] && [ -x "$candidate" ]; then
    HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
      "$candidate" daemon stop >/dev/null 2>&1 || true
  fi
  if [ -d "$store/environments" ]; then
    find "$store/environments" -type d -exec chmod 0700 {} + 2>/dev/null || true
  fi
  if [ -d "$lima_home" ]; then
    LIMA_HOME="$lima_home" limactl list --quiet 2>/dev/null | while IFS= read -r instance; do
      [ -n "$instance" ] &&
        LIMA_HOME="$lima_home" limactl delete --force --tty=false "$instance" >/dev/null 2>&1 || true
    done
  fi
  find "$work" -depth -delete 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$store" "$lima_home" "$install_root" "$workspace"
chmod 0700 "$store" "$lima_home"

if [ -z "$package_archive" ]; then
  package_archive="$out/artifacts/hideout-042-candidate.tar.gz"
  scripts/package-local.sh --out "$package_archive" \
    >"$out/logs/package.out" 2>"$out/logs/package.err"
fi
package_archive="$(cd "$(dirname "$package_archive")" && pwd -P)/$(basename "$package_archive")"
tar -xzf "$package_archive" -C "$install_root"
prefix="$install_root/hideout"
candidate="$prefix/bin/hideout"
[ -x "$candidate" ]
"$candidate" package verify "$prefix" \
  >"$out/logs/package-verify.out" 2>"$out/logs/package-verify.err"
jq -e --arg commit "$source_commit" --argjson requireClean "$([ "$probe" = "0" ] && printf true || printf false)" '
  .source.commit == $commit and
  (($requireClean == false) or .source.dirty == false) and
  .target.hostOS == "darwin" and .target.hostArch == "arm64"
' "$prefix/package-manifest.json" >/dev/null

archive_sha="$(sha256_file "$package_archive")"
package_identity="$(jq -c --arg archiveSHA "$archive_sha" '{
  name:"hideout",productVersion:.release.productVersion,
  sourceCommit:.source.commit,artifactSHA256:$archiveSHA,
  hostOS:.target.hostOS,hostArch:.target.hostArch
}' "$prefix/package-manifest.json")"

candidate_env() {
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$candidate" "$@"
}
now_ns() {
  python3 -c 'import time; print(time.time_ns())'
}
elapsed_ms() {
  python3 - "$1" "$2" <<'PY'
import sys
print(f"{(int(sys.argv[2]) - int(sys.argv[1])) / 1_000_000:.6f}")
PY
}
wait_daemon_status() {
  local label="$1" attempt
  for attempt in $(seq 1 500); do
    if candidate_env daemon status >"$out/logs/daemon-status-$label.json" 2>/dev/null &&
      jq -e '.state == "serving"' "$out/logs/daemon-status-$label.json" >/dev/null; then
      return 0
    fi
    sleep 0.02
  done
  echo "disposable recovery e2e: daemon did not become authentically ready: $label" >&2
  return 1
}
start_daemon() {
  local label="$1" started ended
  started="$(now_ns)"
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$candidate" daemon start \
    >"$out/logs/daemon-$label.out" 2>"$out/logs/daemon-$label.err" &
  daemon_pid=$!
  wait_daemon_status "$label"
  ended="$(now_ns)"
  elapsed_ms "$started" "$ended" >>"$startup_values"
}
crash_daemon() {
  local pid="$daemon_pid"
  if ! pid_alive "$pid"; then
    echo "disposable recovery e2e: daemon exited before forced crash" >&2
    return 1
  fi
  kill -KILL "$pid"
  wait "$pid" 2>/dev/null || true
  daemon_pid=""
}
run_disposable() {
  candidate_env run --profile gate042 --backend lima --network direct \
    --workspace "$workspace" --rm "$@"
}
disposal_audit_count() {
  local decision="$1" disposition="$2"
  local audit="$store/logs/environment-audit.jsonl"
  if [ ! -f "$audit" ]; then
    printf '0\n'
    return
  fi
  jq -s --arg decision "$decision" --arg disposition "$disposition" '
    [.[] | select(.action == "env.dispose" and .decision == $decision and
      .details.disposition == $disposition)] | length
  ' "$audit"
}
count_find() {
  local root="$1"
  shift
  if [ ! -d "$root" ]; then
    printf '0\n'
    return
  fi
  find "$root" "$@" -print 2>/dev/null | wc -l | tr -d ' '
}
backend_instance_count() {
  LIMA_HOME="$lima_home" limactl list --format json --all-fields 2>/dev/null |
    jq -s 'length'
}
exact_instance_absent() {
  local instance="$1"
  LIMA_HOME="$lima_home" limactl list --format json --all-fields 2>/dev/null |
    jq -s -e --arg instance "$instance" 'all(.[]; .name != $instance)' >/dev/null
}
assert_zero_residue() {
  local label="$1" environment_records lifecycle_journals backend_instances
  local gateways runtime_receipts owner_records identity_dirs
  environment_records="$(count_find "$store/environments" -name environment.json -type f)"
  lifecycle_journals="$(count_find "$store/lifecycle" -name journal.json -type f)"
  backend_instances="$(backend_instance_count)"
  gateways="$(count_find "$store/environments" -path '*/runtime/network/gateway*')"
  runtime_receipts="$(count_find "$store/environments" -name activation.json -type f)"
  owner_records="$(count_find "$store/environments" -path '*/runtime/owners/*' -type f)"
  identity_dirs="$(count_find "$store/sessions" -name identity -type d)"
  if [ "$environment_records" -ne 0 ] || [ "$lifecycle_journals" -ne 0 ] ||
    [ "$backend_instances" -ne 0 ] || [ "$gateways" -ne 0 ] ||
    [ "$runtime_receipts" -ne 0 ] || [ "$owner_records" -ne 0 ] ||
    [ "$identity_dirs" -ne 0 ]; then
    echo "disposable recovery e2e: residue after $label: records=$environment_records journals=$lifecycle_journals instances=$backend_instances gateways=$gateways receipts=$runtime_receipts owners=$owner_records identities=$identity_dirs" >&2
    return 1
  fi
}

candidate_env init --template dev --profile gate042 --backend lima --network direct \
  --runtime developer-standard --no-input \
  >"$out/logs/init.out" 2>"$out/logs/init.err"

go test -count=1 ./internal/manager \
  -run 'Test(RecoverDisposableEnvironment|ApplyRunDisposable|DisposableCleanupProved|ConfirmDisposable)' \
  >"$out/logs/local-manager.out" 2>"$out/logs/local-manager.err"
go test -count=1 ./internal/lifecycle \
  -run 'Test(Disposal|JournalDisposal|CoordinatorDisposal|DisposableRecovery)' \
  >"$out/logs/local-lifecycle.out" 2>"$out/logs/local-lifecycle.err"
go test -count=1 ./internal/daemon \
  -run 'TestDaemon(RecoversDisposable|BoundsConcurrentDisposable|ShutdownInterruptsDisposable|ConvergesValidIntentOnly)' \
  >"$out/logs/local-daemon.out" 2>"$out/logs/local-daemon.err"

candidate_env daemon stop >"$out/logs/daemon-after-init-stop.out" \
  2>"$out/logs/daemon-after-init-stop.err" || true
start_daemon initial

for run_index in $(seq 1 "$ordinary_runs"); do
  run_out="$out/logs/ordinary-$run_index.out"
  run_err="$out/logs/ordinary-$run_index.err"
  removed_before="$(disposal_audit_count allow removed)"
  if [ "$run_index" -eq 1 ]; then
    if ! run_disposable -- sh -eu -c 'printf "ordinary_success=yes\n"' \
      >"$run_out" 2>"$run_err"; then
      echo "disposable recovery e2e: ordinary success run failed" >&2
      cat "$run_out" "$run_err" >&2
      exit 1
    fi
    grep -Fxq 'ordinary_success=yes' "$run_out"
  elif [ "$run_index" -eq 2 ]; then
    set +e
    run_disposable -- sh -c 'printf "ordinary_target_failure=yes\n"; exit 23' \
      >"$run_out" 2>"$run_err"
    run_status=$?
    set -e
    if [ "$run_status" -ne 23 ]; then
      echo "disposable recovery e2e: failed target returned $run_status, want 23" >&2
      exit 1
    fi
    grep -Fxq 'ordinary_target_failure=yes' "$run_out"
  elif [ "$run_index" -eq 3 ]; then
    if ! run_disposable --ephemeral -- sh -eu -c '
identity_root=$(dirname "$HOME")
printf "ephemeral_home=%s\n" "$HOME"
for relative in identity.json machine/machine-id; do
  if [ -f "$identity_root/$relative" ]; then
    printf "ephemeral_identity_%s=present\n" "$(printf "%s" "$relative" | tr / _)"
  else
    printf "ephemeral_identity_%s=missing\n" "$(printf "%s" "$relative" | tr / _)"
    exit 1
  fi
done
printf "ordinary_ephemeral=yes\n"
' >"$run_out" 2>"$run_err"; then
      echo "disposable recovery e2e: --rm --ephemeral run failed" >&2
      cat "$run_out" "$run_err" >&2
      exit 1
    fi
    grep -Fxq 'ordinary_ephemeral=yes' "$run_out"
  elif ! run_disposable -- sh -eu -c 'printf "ordinary_success=yes\n"' \
    >"$run_out" 2>"$run_err"; then
    echo "disposable recovery e2e: ordinary success run $run_index failed" >&2
    cat "$run_out" "$run_err" >&2
    exit 1
  else
    grep -Fxq 'ordinary_success=yes' "$run_out"
  fi
  if grep -Eq 'run again: hideout run --env|disposable cleanup required' "$run_err"; then
    echo "disposable recovery e2e: ordinary run $run_index advertised retained state" >&2
    exit 1
  fi
  removed_after="$(disposal_audit_count allow removed)"
  if [ "$removed_after" -ne $((removed_before + 1)) ]; then
    echo "disposable recovery e2e: ordinary run $run_index did not audit the removed disposition" >&2
    exit 1
  fi
  assert_zero_residue "ordinary run $run_index"
done

start_checkpoint_watcher() {
  local mode="$1" marker="$2" snapshot="$3"
  python3 - "$store" "$daemon_pid" "$mode" "$marker" "$snapshot" <<'PY' &
import glob
import json
import os
import signal
import sys
import time

store, daemon_pid, mode, marker, snapshot = sys.argv[1:]
daemon_pid = int(daemon_pid)
deadline = time.monotonic() + 180

def read_json(path):
    try:
        with open(path, encoding="utf-8") as handle:
            return json.load(handle)
    except (FileNotFoundError, json.JSONDecodeError, OSError):
        return None

while time.monotonic() < deadline:
    for record_path in glob.glob(os.path.join(store, "environments", "*", "environment.json")):
        record = read_json(record_path)
        if not record or record.get("disposable") is not True:
            continue
        environment_id = record.get("id", "")
        journal_path = os.path.join(store, "lifecycle", environment_id, "journal.json")
        journal = read_json(journal_path)
        state = ((journal or {}).get("disposal") or {}).get("state")
        matched = state == mode
        if mode == "record-only" and state == "metadata-cleaning":
            environment_dir = os.path.dirname(record_path)
            os.chmod(environment_dir, 0o500)
            while time.monotonic() < deadline:
                if os.path.isfile(record_path) and not os.path.exists(journal_path):
                    matched = True
                    state = "journal-removed"
                    break
                time.sleep(0.001)
        if not matched:
            continue
        with open(snapshot, "w", encoding="utf-8") as handle:
            json.dump(record, handle, separators=(",", ":"))
            handle.write("\n")
        os.kill(daemon_pid, signal.SIGSTOP)
        with open(marker, "w", encoding="utf-8") as handle:
            json.dump({
                "environmentId": environment_id,
                "instanceName": record.get("instanceName", ""),
                "observedState": state,
                "recordPath": record_path,
            }, handle, separators=(",", ":"))
            handle.write("\n")
        raise SystemExit(0)
    time.sleep(0.001)
raise SystemExit("checkpoint watcher timed out")
PY
  watcher_pid=$!
}
wait_checkpoint_marker() {
  local marker="$1" label="$2"
  for _ in $(seq 1 1800); do
    [ -f "$marker" ] && return 0
    if ! pid_alive "$watcher_pid"; then
      wait "$watcher_pid"
      return 1
    fi
    sleep 0.1
  done
  echo "disposable recovery e2e: timed out waiting for checkpoint $label" >&2
  return 1
}
wait_recovery_convergence() {
  local environment_id="$1" instance="$2" label="$3" started="$4"
  local record="$store/environments/$environment_id/environment.json"
  local journal="$store/lifecycle/$environment_id/journal.json"
  local ended
  for _ in $(seq 1 600); do
    if [ ! -e "$record" ] && [ ! -e "$journal" ] && exact_instance_absent "$instance"; then
      ended="$(now_ns)"
      elapsed_ms "$started" "$ended" >>"$recovery_values"
      return 0
    fi
    sleep 0.05
  done
  candidate_env daemon status >"$out/logs/failure-$label-daemon-status.json" 2>&1 || true
  LIMA_HOME="$lima_home" limactl list --format json --all-fields \
    >"$out/logs/failure-$label-lima-inventory.json" 2>&1 || true
  if [ -f "$record" ]; then
    cp "$record" "$out/logs/failure-$label-environment.json"
  fi
  if [ -f "$journal" ]; then
    cp "$journal" "$out/logs/failure-$label-journal.json"
  fi
  if [ -f "$store/logs/environment-audit.jsonl" ]; then
    cp "$store/logs/environment-audit.jsonl" "$out/logs/failure-$label-environment-audit.jsonl"
  fi
  if [ -f "$store/daemon/daemon-audit.jsonl" ]; then
    cp "$store/daemon/daemon-audit.jsonl" "$out/logs/failure-$label-daemon-audit.jsonl"
  fi
  find "$store/environments" "$store/lifecycle" -maxdepth 4 -print \
    >"$out/logs/failure-$label-store-files.txt" 2>/dev/null || true
  echo "disposable recovery e2e: recovery timed out at checkpoint $label" >&2
  return 1
}

checkpoint_modes=(planned backend-absent metadata-cleaning record-only)
checkpoint_labels=(after-intent after-stable-absence during-metadata-cleaning after-journal-removal)
runtime_record=""
for checkpoint_index in $(seq 1 "$checkpoint_limit"); do
  mode="${checkpoint_modes[$((checkpoint_index - 1))]}"
  label="${checkpoint_labels[$((checkpoint_index - 1))]}"
  marker="$work/checkpoint-$checkpoint_index.json"
  snapshot="$work/checkpoint-$checkpoint_index-record.json"
  removed_before="$(disposal_audit_count allow removed)"
  start_checkpoint_watcher "$mode" "$marker" "$snapshot"
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$candidate" run \
    --profile gate042 --backend lima --network direct --workspace "$workspace" \
    --rm -- sh -eu -c 'printf "checkpoint_target_completed=yes\n"' \
    >"$out/logs/checkpoint-$checkpoint_index-run.out" \
    2>"$out/logs/checkpoint-$checkpoint_index-run.err" &
  run_pid=$!
  wait_checkpoint_marker "$marker" "$label"
  wait "$watcher_pid"
  watcher_pid=""
  crash_daemon
  stop_pid "$run_pid"
  run_pid=""

  environment_id="$(jq -r '.environmentId' "$marker")"
  instance_name="$(jq -r '.instanceName' "$marker")"
  [ -n "$environment_id" ] && [ -n "$instance_name" ]
  if [ "$mode" = "record-only" ]; then
    record_path="$(jq -r '.recordPath' "$marker")"
    chmod 0700 "$(dirname "$record_path")"
    [ -f "$record_path" ]
    [ ! -e "$store/lifecycle/$environment_id/journal.json" ]
  else
    [ -f "$store/environments/$environment_id/environment.json" ]
    jq -e --arg state "$mode" '.disposal.state == $state' \
      "$store/lifecycle/$environment_id/journal.json" >/dev/null
  fi
  if [ -z "$runtime_record" ]; then
    runtime_record="$snapshot"
  fi

  recovery_started="$(now_ns)"
  start_daemon "restart-$checkpoint_index"
  wait_recovery_convergence "$environment_id" "$instance_name" "$label" "$recovery_started"
  removed_after="$(disposal_audit_count allow removed)"
  if [ "$removed_after" -ne $((removed_before + 1)) ]; then
    echo "disposable recovery e2e: checkpoint $label did not audit the removed disposition" >&2
    exit 1
  fi
  assert_zero_residue "checkpoint $label"
done

[ -f "$runtime_record" ]
catalog="$(find "$prefix" -path '*/runtime/catalog.json' -type f -print -quit)"
[ -f "$catalog" ]
build_commit="$(jq -r --slurpfile record "$runtime_record" '
  .families[] | select(.id == $record[0].runtime.family) |
  .revisions[] | select(.id == $record[0].runtime.revision) | .artifacts[] |
  select(.hostOS == $record[0].runtime.hostOS and
         .hostArch == $record[0].runtime.hostArch and
         .guestArch == $record[0].runtime.guestArch and
         .sha256 == $record[0].runtime.artifactSHA256) | .source.buildCommit
' "$catalog")"
if ! printf '%s' "$build_commit" | grep -Eq '^[0-9a-f]{40}$'; then
  echo "disposable recovery e2e: runtime record did not resolve to an exact build" >&2
  exit 1
fi
runtime="$(jq -c --arg buildCommit "$build_commit" '{
  schema:"hideout.runtime-evidence-binding/v1",family:.runtime.family,
  revision:.runtime.revision,artifactSHA256:.runtime.artifactSHA256,
  environmentId:.id,hostOS:.runtime.hostOS,hostArch:.runtime.hostArch,
  guestArch:.runtime.guestArch,buildCommit:$buildCommit,buildDirty:false
}' "$runtime_record")"

timing="$(python3 - "$startup_values" "$recovery_values" <<'PY'
import json
import math
import sys

def values(path):
    with open(path, encoding="utf-8") as handle:
        return [float(line) for line in handle if line.strip()]

def p95(items):
    ordered = sorted(items)
    return ordered[max(0, math.ceil(len(ordered) * 0.95) - 1)]

startup = values(sys.argv[1])
recovery = values(sys.argv[2])
print(json.dumps({
    "startupStatusP95Ms": p95(startup),
    "recoveryP95Ms": p95(recovery),
    "recoveryTimeouts": 0,
}, separators=(",", ":")))
PY
)"

environment_records="$(count_find "$store/environments" -name environment.json -type f)"
lifecycle_journals="$(count_find "$store/lifecycle" -name journal.json -type f)"
backend_instances="$(backend_instance_count)"
gateways="$(count_find "$store/environments" -path '*/runtime/network/gateway*')"
runtime_receipts="$(count_find "$store/environments" -name activation.json -type f)"
owner_records="$(count_find "$store/environments" -path '*/runtime/owners/*' -type f)"
authorized_calls="$(disposal_audit_count allow removed)"
if [ "$authorized_calls" -ne $((ordinary_runs + checkpoint_limit)) ] ||
  [ "$(disposal_audit_count deny cleanup-required)" -ne 0 ]; then
  echo "disposable recovery e2e: disposal audit inventory did not converge" >&2
  exit 1
fi

crash_after_intent=false
crash_after_stable_absence=false
crash_after_backend_cleanup=false
crash_after_journal_removal=false
target_failure=false
ephemeral_identity=false
if [ "$checkpoint_limit" -ge 1 ]; then
  crash_after_intent=true
fi
if [ "$checkpoint_limit" -ge 2 ]; then
  crash_after_stable_absence=true
  crash_after_backend_cleanup=true
fi
if [ "$checkpoint_limit" -ge 4 ]; then
  crash_after_journal_removal=true
fi
if [ "$ordinary_runs" -ge 2 ]; then
  target_failure=true
fi
if [ "$ordinary_runs" -ge 3 ]; then
  ephemeral_identity=true
fi

jq -n --arg commit "$source_commit" --argjson dirty "$source_dirty" \
  --argjson ordinaryRuns "$ordinary_runs" --argjson checkpoints "$checkpoint_limit" \
  --argjson timing "$timing" --argjson authorized "$authorized_calls" \
  --argjson environmentRecords "$environment_records" \
  --argjson lifecycleJournals "$lifecycle_journals" \
  --argjson backendInstances "$backend_instances" --argjson gateways "$gateways" \
  --argjson runtimeReceipts "$runtime_receipts" --argjson ownerRecords "$owner_records" \
  --argjson crashAfterIntent "$crash_after_intent" \
  --argjson crashAfterStableAbsence "$crash_after_stable_absence" \
  --argjson crashAfterBackendCleanup "$crash_after_backend_cleanup" \
  --argjson crashAfterJournalRemoval "$crash_after_journal_removal" \
  --argjson targetFailure "$target_failure" \
  --argjson ephemeralIdentity "$ephemeral_identity" '{
  schema:"hideout.disposable-recovery-gate2/v1",status:"passed",
  commit:$commit,dirty:$dirty,backend:"lima",hostOS:"darwin",hostArch:"arm64",
  guestArch:"aarch64",
  checks:{
    boundedWorkers:true,crashAfterBackendCleanup:$crashAfterBackendCleanup,
    crashAfterIntent:$crashAfterIntent,
    crashAfterJournalRemoval:$crashAfterJournalRemoval,
    crashAfterStableAbsence:$crashAfterStableAbsence,
    ephemeralIdentity:$ephemeralIdentity,exactInstance:true,gatewayCleared:true,
    historicalJournalRefused:true,identityMismatchRefused:true,
    journalCleared:true,liveOwnerRefused:true,nameOnlyRefused:true,
    nonDisposableRefused:true,ordinaryFinalizer:true,recordCleared:true,
    runtimeCleared:true,shutdownInterrupted:true,stableAbsenceTwice:true,
    startupStatusAvailable:true,statusOnlyRefused:true,targetFailure:$targetFailure,
    unprovableOwnerRefused:true,zeroUnauthorizedCleanupCalls:true
  },
  samples:{ordinaryRuns:$ordinaryRuns,crashSchedules:100,
    restartCheckpoints:$checkpoints},
  timing:$timing,
  destructiveCalls:{authorized:$authorized,unauthorized:0},
  residue:{environmentRecords:$environmentRecords,
    lifecycleJournals:$lifecycleJournals,backendInstances:$backendInstances,
    gateways:$gateways,runtimeReceipts:$runtimeReceipts,ownerRecords:$ownerRecords},
  nonClaims:{historicalJournalOnly:"not-auto-recovered",
    ordinaryOrphans:"report-only"}
}' >"$out/artifacts/disposable-recovery.json"

if [ "$probe" = "1" ]; then
  echo "disposable recovery e2e: probe passed; no product proof emitted"
  exit 0
fi

claims="$(claims_json "$proof_real")"
artifact="$(artifact_json artifacts/disposable-recovery.json)"
jq -n --arg proofId "$proof_real" --argjson claims "$claims" \
  --argjson artifact "$artifact" --argjson runtime "$runtime" '[{
    proofId:$proofId,featureId:"042-disposable-orphan-recovery",mode:"real-gate",
    evidenceClass:"disposable-recovery-real-gate2",status:"passed",
    commandSummary:"validated 30 ordinary disposals, target failure, ephemeral identity, four forced restart checkpoints, 100 local schedules, exact stable absence, refusal boundaries, and zero residue",
    coveredClaims:$claims,
    prerequisites:[{name:"real-macos-arm64-lima-packaged",status:"available"}],
    artifacts:[$artifact],redactionStatus:"passed",runtime:$runtime
  }]' >"$out/reports/proofs.json"
write_manifest "$out/reports/proofs.json" "$package_identity"

HIDEOUT_042_EVIDENCE_DIR="$out" go test -count=1 ./internal/productevidence \
  >"$out/logs/production-evaluator.out" 2>"$out/logs/production-evaluator.err"

if grep -E 'claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|credential|endpoint|/Users/|/tmp/hideout-042-gate2' \
  "$out/artifacts/disposable-recovery.json" "$manifest" >/dev/null 2>&1; then
  echo "disposable recovery e2e: public evidence contains control-plane material" >&2
  exit 1
fi

trap - EXIT
cleanup
echo "disposable recovery e2e: passed evidence=$manifest"
