#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0

umask 077
export LC_ALL=C
export TZ=UTC

artifact_root="${HIDEOUT_045_ARTIFACT_ROOT:-$repo_root/.artifacts/045}"
output="$artifact_root/evidence.json"
require_closure=0
preflight_only=0
tmp_base="${TMPDIR:-/tmp}"
tmp_base="${tmp_base%/}"
installed_validation_binary=""
installed_validation_daemon_pid=""

usage() {
  printf '%s\n' \
    "Usage: scripts/release/collect-evidence.sh [--preflight]" \
    "                                            [--out FILE]" \
    "                                            [--require-closure]" \
    "" \
    "Collects one digest-signed local Feature 045 evidence manifest from an" \
    "exact clean commit and the exact package/gate outputs for that commit." \
    "The collector independently extracts and verifies every packaged file." \
    "" \
    "--require-closure additionally requires exact local-install and" \
    "publication-absence receipts. This command never commits, tags, pushes," \
    "creates a remote release, changes Homebrew, or publishes package bytes."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'collect-evidence: --out requires a file\n' >&2
        exit 2
      fi
      output="$2"
      shift 2
      ;;
    --require-closure)
      require_closure=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'collect-evidence: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

fail() {
  printf 'collect-evidence: %s\n' "$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'collect-evidence: missing required command: %s\n' "$1" >&2
    return 1
  }
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  sha256sum "$1" | awk '{print $1}'
}

file_mode() {
  stat -f '%Lp' "$1" 2>/dev/null ||
    stat -c '%a' "$1" 2>/dev/null
}

file_bytes() {
  stat -f '%z' "$1" 2>/dev/null ||
    stat -c '%s' "$1" 2>/dev/null
}

normalized_mode() {
  local raw
  raw="$(file_mode "$1")"
  case "$raw" in
    [0-7][0-7][0-7])
      printf '0%s\n' "$raw"
      ;;
    [0-7][0-7][0-7][0-7])
      printf '%s\n' "$raw"
      ;;
    *)
      return 1
      ;;
  esac
}

safe_relative_path() {
  case "$1" in
    "" | /* | . | .. | ../* | */.. | */../* | *$'\n'* | *$'\r'* | *$'\t'*)
      return 1
      ;;
  esac
}

cleanup_tree() {
  local target="$1" prefix="$2"
  if [ -z "$target" ] || [ ! -e "$target" ]; then
    return
  fi
  case "$target" in
    "$tmp_base"/"$prefix".*)
      [ ! -L "$target" ] ||
        fail "refusing symlink cleanup target: $target"
      find "$target" -depth -delete
      ;;
    *)
      printf 'collect-evidence: refusing unexpected cleanup target: %s\n' \
        "$target" >&2
      return 1
      ;;
  esac
}

daemon_status_is_serving() {
  local status_file="$1"
  jq -e '
    .version == "hideout.daemon-status/v1" and
    .state == "serving" and
    (.instanceId | type == "string" and length > 0) and
    (.startedAt | type == "string" and length > 0)
  ' "$status_file" >/dev/null
}

stop_installed_validation_daemon() {
  local binary="${installed_validation_binary:-}"
  local process_id="${installed_validation_daemon_pid:-}"
  if [ -n "$binary" ] && [ -x "$binary" ]; then
    "$binary" daemon stop >/dev/null 2>&1 || true
  fi
  if [ -n "$process_id" ] && kill -0 "$process_id" 2>/dev/null; then
    kill -TERM "$process_id" 2>/dev/null || true
  fi
  if [ -n "$process_id" ]; then
    wait "$process_id" 2>/dev/null || true
  fi
  installed_validation_binary=""
  installed_validation_daemon_pid=""
}

verify_sha256() {
	local evidence_file="$1" expected="$2"
	[ -f "$evidence_file" ] &&
		[ ! -L "$evidence_file" ] &&
		[[ "$expected" =~ ^[a-f0-9]{64}$ ]] &&
		[ "$(sha256_file "$evidence_file")" = "$expected" ]
}

package_manifest_executable_value() {
  jq -r '
    if type == "object" and
      has("executable") and
      (.executable | type) == "boolean"
    then
      (.executable | tostring)
    else
      error("package manifest executable must be a boolean")
    end
  '
}

helper_manifest_binding_valid() {
  local manifest_path="$1" binary_path="$2"
  local expected_command="$3" expected_arch="$4"
  local expected_artifact expected_sha

  case "$expected_command" in
    hideout-hostfsd | hideout-observer | hideout-session-supervisor | \
      hideout-shim | hideout-workspace-portal | tun2socks)
      ;;
    *)
      return 1
      ;;
  esac
  [ -f "$manifest_path" ] && [ ! -L "$manifest_path" ] &&
    [ -f "$binary_path" ] && [ ! -L "$binary_path" ] || return 1
  expected_artifact="$(basename -- "$binary_path")"
  expected_sha="$(sha256_file "$binary_path")" || return 1
  jq -e \
    --arg command "$expected_command" \
    --arg arch "$expected_arch" \
    --arg artifact "$expected_artifact" \
    --arg sha256 "$expected_sha" '
      .version == "hideout.helper-manifest/v1" and
      .command == $command and
      .targetOS == "linux" and
      .targetArch == $arch and
      .artifact == $artifact and
      .sha256 == $sha256 and
      ((.builtAt | try fromdateiso8601 catch null) | type) == "number" and
      if $command == "hideout-observer" then
        .builder == "go build -trimpath" and
        .license == "Apache-2.0" and
        .buildMode == "embedded-core-bpf" and
        .packageOwned == true
      elif $command == "tun2socks" then
        .builder == "go build -mod=readonly" and
        .upstreamModule == "github.com/xjasonlyu/tun2socks/v2" and
        .upstreamVersion == "v2.6.0" and
        .license == "MIT" and
        .buildMode == "source-built-pinned-module" and
        .packageOwned == true
      else
        .builder == "go build" and
        ((has("packageOwned") | not) or .packageOwned == true)
      end
    ' "$manifest_path" >/dev/null
}

review_finding_count_for_file() {
  local review_path="$1"
  local finding_ids finding_id expected_id
  local count=0
  finding_ids="$(
    sed -n \
      's/^| CR045-\([0-9][0-9][0-9]\) |.*/\1/p' \
      "$review_path"
  )" || return 1
  [ -n "$finding_ids" ] || return 1
  while IFS= read -r finding_id; do
    count=$((count + 1))
    expected_id="$(printf '%03d' "$count")"
    [ "$finding_id" = "$expected_id" ] || return 1
  done <<<"$finding_ids"
  printf '%s\n' "$count"
}

local_run_artifact_reference() {
  local summary_path="$1" relative_path="$2"
  jq -ce \
    --arg relativePath "$relative_path" '
      .run as $run
      | ($run + "/" + $relativePath) as $expectedPath
      | [
          .artifacts[]?
          | select(.path == $expectedPath)
        ] as $matches
      | if
          ($matches | length) == 1 and
          ($matches[0].sha256 | type) == "string" and
          ($matches[0].sha256 | test("^[a-f0-9]{64}$"))
        then $matches[0]
        else error("missing, duplicate, or invalid local-run artifact")
        end
    ' "$summary_path"
}

validate_performance_evidence_contract() {
  local performance_file="$1"

  jq -e '
    .hostDiagnostics.quietHostConfirmed == true and
    .hostDiagnostics.policy ==
      "operator-confirmed-quiet-host-known-contention-invalidates-run" and
    .hostDiagnostics.initialContentionAssessment.passed == true and
    .hostDiagnostics.initialContentionAssessment.method ==
      "three-one-second-process-snapshots-two-hit-rejection" and
    .hostDiagnostics.initialContentionAssessment.samples == 3 and
    .hostDiagnostics.initialContentionAssessment.minimumHits == 2 and
    .hostDiagnostics.initialContentionAssessment.genericCPUPercentThreshold == 50 and
    .hostDiagnostics.initialContentionAssessment.virtualizationCPUPercentThreshold == 5 and
    .hostDiagnostics.initialContentionAssessment.buildOrTestCPUPercentThreshold == 10 and
    .hostDiagnostics.initialContentionAssessment.path ==
      "host-contention-preflight.txt" and
    (.hostDiagnostics.initialContentionAssessment.sha256 |
      test("^[a-f0-9]{64}$")) and
    .hostDiagnostics.measurementContentionAssessment.passed == true and
    .hostDiagnostics.measurementContentionAssessment.method ==
      "continuous-one-second-three-hit-classified-contention-rejection-generic-diagnostics" and
    .hostDiagnostics.measurementContentionAssessment.samples >= 3 and
    .hostDiagnostics.measurementContentionAssessment.rollingWindow == 3 and
    .hostDiagnostics.measurementContentionAssessment.minimumHits == 3 and
    .hostDiagnostics.measurementContentionAssessment.genericHighCPUPolicy ==
      "diagnostic-only" and
    .hostDiagnostics.measurementContentionAssessment.genericCPUPercentThreshold == 50 and
    .hostDiagnostics.measurementContentionAssessment.virtualizationCPUPercentThreshold == 5 and
    .hostDiagnostics.measurementContentionAssessment.buildOrTestCPUPercentThreshold == 10 and
    .hostDiagnostics.measurementContentionAssessment.path ==
      "host-contention-measurement.txt" and
    (.hostDiagnostics.measurementContentionAssessment.sha256 |
      test("^[a-f0-9]{64}$")) and
    (.hostDiagnostics.snapshots | length) == 3 and
    [.hostDiagnostics.snapshots[] | [.phase, .path]] == [
      ["start", "host-state-start.txt"],
      ["before-real-lima", "host-state-before-real-lima.txt"],
      ["after-real-lima", "host-state-after-real-lima.txt"]
    ] and
    all(.hostDiagnostics.snapshots[];
      (.sha256 | test("^[a-f0-9]{64}$"))) and
    .metrics.referenceWorkload.methodology.samples == 30 and
    .metrics.referenceWorkload.methodology.warmups >= 1 and
    .metrics.referenceWorkload.elapsedOverhead.threshold == 10 and
    .metrics.referenceWorkload.elapsedOverhead.thresholdPassed == true and
    .metrics.referenceWorkload.elapsedOverhead.confidence.level == 0.95 and
    .metrics.referenceWorkload.elapsedOverhead.confidence.method ==
      "one-sided-exact-binomial-order-statistic" and
    .metrics.referenceWorkload.elapsedOverhead.confidence.rank == 20 and
    .metrics.referenceWorkload.elapsedOverhead.confidence.upperBound <= 10 and
    .metrics.referenceWorkload.elapsedOverhead.confidence.thresholdPassed == true and
    .validation.referenceMedianUpperConfidenceBoundWithinTenPercent == true and
    .validation.quietHostExplicitlyConfirmed == true and
    .validation.initialHostContentionAssessmentPassed == true and
    .validation.measurementHostContentionAssessmentPassed == true and
    .validation.hostDiagnosticsRetained == true
  ' "$performance_file" >/dev/null
}

validate_performance_contention_file() {
  local contention_file="$1"

  awk '
    function is_virtualization(name) {
      return name ~ /^qemu-system-/ ||
        name ~ /^com[.]apple[.]Virtua/ ||
        name ~ /^VirtualBoxVM/ ||
        name ~ /^vmware-vmx/ ||
        name ~ /^prl_vm_app/ ||
        name ~ /^UTM/
    }
    function is_build_or_test(name) {
      return name ~ /^(go|compile|link|clang|clang[+][+]|rustc|cargo|pytest|Python|Python3|python|python3|uv|node|nodejs|xcodebuild|ninja|make|java)$/ ||
        name ~ /[.]test$/
    }
    /^schema=hideout[.]performance-host-contention\/v1$/ {
      schema_count++
      next
    }
    /^method=three-one-second-process-snapshots-two-hit-rejection$/ {
      method_count++
      next
    }
    /^samples=3$/ {samples_count++; next}
    /^minimum_hits=2$/ {minimum_count++; next}
    /^generic_cpu_percent_threshold=50$/ {generic_count++; next}
    /^virtualization_cpu_percent_threshold=5$/ {
      virtualization_count++
      next
    }
    /^build_or_test_cpu_percent_threshold=10$/ {build_count++; next}
    /^sample_begin=[1-3]$/ {
      split($0, fields, "=")
      sample = fields[2] + 0
      if (current_sample != 0 || begins[sample] != 0) invalid = 1
      current_sample = sample
      begins[sample]++
      next
    }
    /^sample_end=[1-3]$/ {
      split($0, fields, "=")
      sample = fields[2] + 0
      if (current_sample != sample || ends[sample] != 0) invalid = 1
      ends[sample]++
      current_sample = 0
      next
    }
    current_sample > 0 {
      if ($1 !~ /^[0-9]+$/ || $2 !~ /^[0-9]+$/ ||
          $3 !~ /^[0-9]+([.][0-9]+)?$/ ||
          $4 !~ /^[0-9]+([.][0-9]+)?$/ || $5 == "") {
        invalid = 1
        next
      }
      rows[current_sample]++
      pid = $1 + 0
      cpu = $3 + 0
      name = $5
      threshold = 50
      if (is_virtualization(name)) threshold = 5
      else if (is_build_or_test(name)) threshold = 10
      if (cpu >= threshold) {
        key = pid SUBSEP name
        sample_key = current_sample SUBSEP key
        if (!(sample_key in counted)) {
          counted[sample_key] = 1
          hits[key]++
        }
      }
      next
    }
    END {
      if (current_sample != 0 || schema_count != 1 || method_count != 1 ||
          samples_count != 1 || minimum_count != 1 || generic_count != 1 ||
          virtualization_count != 1 || build_count != 1) invalid = 1
      for (sample = 1; sample <= 3; sample++) {
        if (begins[sample] != 1 || ends[sample] != 1 || rows[sample] < 1)
          invalid = 1
      }
      if (invalid) exit 2
      for (key in hits) if (hits[key] >= 2) exit 1
    }
  ' "$contention_file"
}

validate_performance_measurement_contention_file() {
  local contention_file="$1" expected_samples="$2"

  case "$expected_samples" in
    '' | *[!0-9]* | 0 | 1 | 2)
      return 2
      ;;
  esac

  awk -v expected_samples="$expected_samples" \
    -v expected_window=3 -v minimum_hits=3 '
    function is_virtualization(name) {
      return name ~ /^qemu-system-/ ||
        name ~ /^com[.]apple[.]Virtua/ ||
        name ~ /^VirtualBoxVM/ ||
        name ~ /^vmware-vmx/ ||
        name ~ /^prl_vm_app/ ||
        name ~ /^UTM/
    }
    function is_build_or_test(name) {
      return name ~ /^(go|compile|link|clang|clang[+][+]|rustc|cargo|pytest|Python|Python3|python|python3|uv|node|nodejs|xcodebuild|ninja|make|java)$/ ||
        name ~ /[.]test$/
    }
    /^schema=hideout[.]performance-host-contention\/v4$/ {
      if (sampling_started) invalid = 1
      schema_count++
      next
    }
    /^method=continuous-one-second-three-hit-classified-contention-rejection-generic-diagnostics$/ {
      if (sampling_started) invalid = 1
      method_count++
      next
    }
    /^rolling_window=3$/ {
      if (sampling_started) invalid = 1
      window_count++
      next
    }
    /^minimum_hits=3$/ {
      if (sampling_started) invalid = 1
      minimum_count++
      next
    }
    /^generic_high_cpu_policy=diagnostic-only$/ {
      if (sampling_started) invalid = 1
      generic_policy_count++
      next
    }
    /^generic_cpu_percent_threshold=50$/ {
      if (sampling_started) invalid = 1
      generic_count++
      next
    }
    /^virtualization_cpu_percent_threshold=5$/ {
      if (sampling_started) invalid = 1
      virtualization_count++
      next
    }
    /^build_or_test_cpu_percent_threshold=10$/ {
      if (sampling_started) invalid = 1
      build_count++
      next
    }
    /^gate_process_group=[1-9][0-9]*$/ {
      if (sampling_started) invalid = 1
      split($0, fields, "=")
      excluded_gate_pgid = fields[2] + 0
      gate_group_count++
      next
    }
    /^measurement_process_group=[1-9][0-9]*$/ {
      if (sampling_started) invalid = 1
      split($0, fields, "=")
      excluded_measurement_pgid = fields[2] + 0
      measurement_group_count++
      next
    }
    /^owned_process=[1-9][0-9]*:[^:=[:space:]]+:gate-private-runtime$/ {
      if (current_sample == 0) invalid = 1
      split($0, fields, "=")
      split(fields[2], owner, ":")
      owner_key = owner[1] SUBSEP owner[2]
      owned[owner_key] = 1
      owned_count[owner_key]++
      next
    }
    /^sample_begin=[1-9][0-9]*$/ {
      if (schema_count != 1 || method_count != 1 || window_count != 1 ||
          minimum_count != 1 || generic_policy_count != 1 ||
          generic_count != 1 ||
          virtualization_count != 1 || build_count != 1 ||
          gate_group_count != 1 || measurement_group_count != 1 ||
          excluded_gate_pgid == excluded_measurement_pgid) invalid = 1
      split($0, fields, "=")
      sample = fields[2] + 0
      if (current_sample != 0 || sample != sample_count + 1)
        invalid = 1
      current_sample = sample
      sample_count++
      begins[sample]++
      sampling_started = 1
      next
    }
    /^sample_end=[1-9][0-9]*$/ {
      split($0, fields, "=")
      sample = fields[2] + 0
      if (current_sample != sample || ends[sample] != 0) invalid = 1
      ends[sample]++
      current_sample = 0
      next
    }
    current_sample > 0 {
      if (NF != 6 || $1 !~ /^[0-9]+$/ || $2 !~ /^[0-9]+$/ ||
          $3 !~ /^[0-9]+$/ ||
          $4 !~ /^[0-9]+([.][0-9]+)?$/ ||
          $5 !~ /^[0-9]+([.][0-9]+)?$/ ||
          $6 !~ /^[^:=[:space:]]+$/) {
        invalid = 1
        next
      }
      rows[current_sample]++
      pid = $1 + 0
      pgid = $3 + 0
      cpu = $4 + 0
      name = $6
      observed[pid SUBSEP name]++
      if (pgid == excluded_gate_pgid ||
          pgid == excluded_measurement_pgid) next
      threshold = 50
      reason = "generic-high-cpu"
      if (is_virtualization(name)) {
        threshold = 5
        reason = "active-virtualization"
      }
      else if (is_build_or_test(name)) {
        threshold = 10
        reason = "active-build-or-test"
      }
      if (cpu < threshold) next
      if (reason == "generic-high-cpu") next
      key = pid SUBSEP name
      sample_key = current_sample SUBSEP key
      if (sample_key in counted) next
      counted[sample_key] = 1
      hit_count[key]++
      hit_samples[key, hit_count[key]] = current_sample
      if (hit_count[key] >= minimum_hits) {
        first_hit_index = hit_count[key] - minimum_hits + 1
        first_hit_sample = hit_samples[key, first_hit_index]
        if (current_sample - first_hit_sample < expected_window)
          violations[key] = 1
      }
      next
    }
    {invalid = 1}
    END {
      if (current_sample != 0 || sample_count != expected_samples ||
          schema_count != 1 ||
          method_count != 1 || window_count != 1 || minimum_count != 1 ||
          generic_policy_count != 1 || generic_count != 1 ||
          virtualization_count != 1 ||
          build_count != 1 || gate_group_count != 1 ||
          measurement_group_count != 1 ||
          excluded_gate_pgid == excluded_measurement_pgid ||
          minimum_hits < 2 || minimum_hits > expected_window) invalid = 1
      for (sample = 1; sample <= sample_count; sample++) {
        if (begins[sample] != 1 || ends[sample] != 1 || rows[sample] < 1)
          invalid = 1
      }
      for (key in owned) {
        if (owned_count[key] != 1 || observed[key] < 1) invalid = 1
        split(key, owner, SUBSEP)
        if (!(owner[2] == "hideout" || owner[2] == "limactl" ||
              is_virtualization(owner[2]))) invalid = 1
      }
      if (invalid) exit 2
      for (key in violations) if (!owned[key]) exit 1
    }
  ' "$contention_file"
}

require_private_evidence_file() {
  local evidence_file="$1" allowed_root="${2:-$artifact_root}" resolved_dir
  allowed_root="$(CDPATH='' cd -- "$allowed_root" && pwd -P)"
  [ -f "$evidence_file" ] &&
    [ ! -L "$evidence_file" ] ||
    fail "missing or unsafe evidence file: $evidence_file"
  resolved_dir="$(
    CDPATH='' cd -- "$(dirname -- "$evidence_file")" && pwd -P
  )"
  case "$resolved_dir/$(basename -- "$evidence_file")" in
    "$allowed_root"/*)
      ;;
    *)
      fail "evidence file escapes artifact root: $evidence_file"
      ;;
  esac
  [ "$(normalized_mode "$evidence_file")" = "0600" ] ||
    fail "evidence file mode is not 0600: $evidence_file"
}

verify_performance_host_diagnostics() {
  local summary_file="$1" summary_dir kind phase relative expected_sha
  local allowed_root="${2:-$artifact_root}"
  local diagnostic_file diagnostic_count=0 measurement_samples

  summary_dir="$(
    CDPATH='' cd -- "$(dirname -- "$summary_file")" && pwd -P
  )"
  measurement_samples="$(
    jq -r '.hostDiagnostics.measurementContentionAssessment.samples' \
      "$summary_file"
  )"
  while IFS=$'\t' read -r kind phase relative expected_sha; do
    diagnostic_count=$((diagnostic_count + 1))
    safe_relative_path "$relative" ||
      fail "performance host diagnostic path is unsafe: $relative"
    diagnostic_file="$summary_dir/$relative"
    require_private_evidence_file "$diagnostic_file" "$allowed_root"
    verify_sha256 "$diagnostic_file" "$expected_sha" ||
      fail "performance host diagnostic digest does not match: $relative"
    jq -e \
      --arg path "$relative" \
      --arg sha256 "$expected_sha" '
        ([.artifacts[] |
          select(.path == $path and .sha256 == $sha256 and .mode == "0600")]
          | length) == 1
      ' "$summary_file" >/dev/null ||
      fail "performance summary does not bind one host diagnostic: $relative"
    case "$kind" in
      contention-initial)
        validate_performance_contention_file "$diagnostic_file" ||
          fail "performance raw host contention evidence is invalid or busy"
        ;;
      contention-measurement)
        validate_performance_measurement_contention_file \
          "$diagnostic_file" "$measurement_samples" ||
          fail "performance raw measurement contention evidence is invalid or busy"
        ;;
      snapshot)
        if ! grep -Fxq 'schema=hideout.performance-host-state/v1' \
          "$diagnostic_file" ||
          ! grep -Fxq "phase=$phase" "$diagnostic_file" ||
          ! grep -Fxq 'top_processes_begin' "$diagnostic_file" ||
          ! grep -Fxq 'top_processes_end' "$diagnostic_file"; then
          fail "performance host-state snapshot is invalid: $relative"
        fi
        ;;
      *)
        fail "performance host diagnostic kind is invalid: $kind"
        ;;
    esac
  done < <(
    jq -r '
      ([{
        kind:"contention-initial",
        phase:"preflight",
        path:.hostDiagnostics.initialContentionAssessment.path,
        sha256:.hostDiagnostics.initialContentionAssessment.sha256
      },{
        kind:"contention-measurement",
        phase:"real-lima-measurement",
        path:.hostDiagnostics.measurementContentionAssessment.path,
        sha256:.hostDiagnostics.measurementContentionAssessment.sha256
      }] + [
        .hostDiagnostics.snapshots[] |
        {kind:"snapshot",phase,path,sha256}
      ])[] |
      [.kind, .phase, .path, .sha256] | @tsv
    ' "$summary_file"
  )
  [ "$diagnostic_count" -eq 5 ] ||
    fail "performance host diagnostic inventory is incomplete"
}

run_preflight() {
  local fixture digest review_fixture local_summary_fixture artifact_reference
  local daemon_status_fixture
  local helper_binary helper_manifest helper_sha
  local observer_binary observer_manifest observer_sha
  local tun_binary tun_manifest tun_sha
  local performance_fixture performance_invalid
  local performance_contention_quiet performance_contention_busy
  local performance_measurement_quiet performance_measurement_busy
  local performance_measurement_unowned performance_measurement_invalid_owner
  local performance_bound_fixture performance_snapshot_start
  local performance_snapshot_before performance_snapshot_after
  preflight_root="$(
    mktemp -d "$tmp_base/hideout-collect-evidence-preflight.XXXXXX"
  )"
  # Invoked indirectly by the EXIT trap.
  # shellcheck disable=SC2329
  cleanup_preflight() {
    local exit_status=$?
    cleanup_tree \
      "${preflight_root:-}" \
      "hideout-collect-evidence-preflight"
    if [ "$exit_status" -eq 0 ]; then
      gate_require_completion "collect-evidence-preflight"
    fi
  }
  trap cleanup_preflight EXIT

  fixture="$preflight_root/evidence.json"
  printf '%s\n' '{"schema":"hideout.release-evidence/v1"}' >"$fixture"
  chmod 0600 "$fixture"
  digest="$(sha256_file "$fixture")"
  verify_sha256 "$fixture" "$digest" ||
    fail "valid detached digest fixture was rejected"
  printf '%s\n' '{"mutation":true}' >>"$fixture"
  if verify_sha256 "$fixture" "$digest"; then
    fail "mutated evidence fixture retained detached digest validity"
  fi
  [ "$(package_manifest_executable_value <<<'{"executable":false}')" = \
    "false" ] ||
    fail "false package executable fixture was rejected"
  [ "$(package_manifest_executable_value <<<'{"executable":true}')" = \
    "true" ] ||
    fail "true package executable fixture was rejected"
  if package_manifest_executable_value \
    <<<'{"executable":"false"}' >/dev/null 2>&1 ||
    package_manifest_executable_value \
      <<<'{}' >/dev/null 2>&1; then
    fail "invalid package executable fixture was accepted"
  fi
  helper_binary="$preflight_root/hideout-hostfsd-linux-arm64"
  helper_manifest="$helper_binary.manifest.json"
  printf '%s\n' 'generic helper fixture' >"$helper_binary"
  chmod 0700 "$helper_binary"
  helper_sha="$(sha256_file "$helper_binary")"
  jq -n \
    --arg sha256 "$helper_sha" '
      {
        version:"hideout.helper-manifest/v1",
        command:"hideout-hostfsd",
        targetOS:"linux",
        targetArch:"arm64",
        artifact:"hideout-hostfsd-linux-arm64",
        sha256:$sha256,
        builder:"go build",
        builtAt:"2026-08-01T00:00:00Z"
      }
    ' >"$helper_manifest"
  helper_manifest_binding_valid \
    "$helper_manifest" "$helper_binary" "hideout-hostfsd" "arm64" ||
    fail "generic helper manifest without packageOwned was rejected"
  jq '.packageOwned = false' \
    "$helper_manifest" >"$helper_manifest.not-owned"
  if helper_manifest_binding_valid \
    "$helper_manifest.not-owned" "$helper_binary" \
    "hideout-hostfsd" "arm64"; then
    fail "explicitly non-package-owned helper manifest was accepted"
  fi
  jq '.sha256 = ("0" * 64)' \
    "$helper_manifest" >"$helper_manifest.wrong-sha"
  if helper_manifest_binding_valid \
    "$helper_manifest.wrong-sha" "$helper_binary" \
    "hideout-hostfsd" "arm64"; then
    fail "helper manifest with a mismatched digest was accepted"
  fi
  observer_binary="$preflight_root/hideout-observer-linux-arm64"
  observer_manifest="$observer_binary.manifest.json"
  printf '%s\n' 'observer helper fixture' >"$observer_binary"
  chmod 0700 "$observer_binary"
  observer_sha="$(sha256_file "$observer_binary")"
  jq -n \
    --arg sha256 "$observer_sha" '
      {
        version:"hideout.helper-manifest/v1",
        command:"hideout-observer",
        targetOS:"linux",
        targetArch:"arm64",
        artifact:"hideout-observer-linux-arm64",
        sha256:$sha256,
        builder:"go build -trimpath",
        builtAt:"2026-08-01T00:00:00Z",
        license:"Apache-2.0",
        buildMode:"embedded-core-bpf",
        packageOwned:true
      }
    ' >"$observer_manifest"
  helper_manifest_binding_valid \
    "$observer_manifest" "$observer_binary" "hideout-observer" "arm64" ||
    fail "package-owned observer manifest was rejected"
  jq 'del(.packageOwned)' \
    "$observer_manifest" >"$observer_manifest.unowned"
  if helper_manifest_binding_valid \
    "$observer_manifest.unowned" "$observer_binary" \
    "hideout-observer" "arm64"; then
    fail "observer manifest without package ownership was accepted"
  fi
  tun_binary="$preflight_root/tun2socks-linux-arm64"
  tun_manifest="$tun_binary.manifest.json"
  printf '%s\n' 'tun2socks helper fixture' >"$tun_binary"
  chmod 0700 "$tun_binary"
  tun_sha="$(sha256_file "$tun_binary")"
  jq -n \
    --arg sha256 "$tun_sha" '
      {
        version:"hideout.helper-manifest/v1",
        command:"tun2socks",
        targetOS:"linux",
        targetArch:"arm64",
        artifact:"tun2socks-linux-arm64",
        sha256:$sha256,
        builder:"go build -mod=readonly",
        builtAt:"2026-08-01T00:00:00Z",
        upstreamModule:"github.com/xjasonlyu/tun2socks/v2",
        upstreamVersion:"v2.6.0",
        license:"MIT",
        buildMode:"source-built-pinned-module",
        packageOwned:true
      }
    ' >"$tun_manifest"
  helper_manifest_binding_valid \
    "$tun_manifest" "$tun_binary" "tun2socks" "arm64" ||
    fail "package-owned tun2socks manifest was rejected"
  daemon_status_fixture="$preflight_root/daemon-status.json"
  jq -n '
    {
      version:"hideout.daemon-status/v1",
      state:"serving",
      instanceId:"daemon_fixture",
      startedAt:"2026-08-01T00:00:00Z"
    }
  ' >"$daemon_status_fixture"
  daemon_status_is_serving "$daemon_status_fixture" ||
    fail "serving daemon status fixture was rejected"
  jq '.state = "running"' \
    "$daemon_status_fixture" >"$daemon_status_fixture.running"
  if daemon_status_is_serving "$daemon_status_fixture.running"; then
    fail "non-schema running daemon status fixture was accepted"
  fi
  jq '.instanceId = ""' \
    "$daemon_status_fixture" >"$daemon_status_fixture.missing-instance"
  if daemon_status_is_serving \
    "$daemon_status_fixture.missing-instance"; then
    fail "daemon status fixture without an instance was accepted"
  fi
  safe_relative_path "run-1/summary.json" ||
    fail "safe evidence path was rejected"
  if safe_relative_path "../summary.json" ||
    safe_relative_path "/tmp/summary.json" ||
    safe_relative_path $'run-1/\tsummary.json'; then
    fail "unsafe evidence path was accepted"
  fi
  review_fixture="$preflight_root/review.md"
  printf '%s\n' \
    '| CR045-001 | Low | one |' \
    '| CR045-002 | Low | two |' \
    '| CR045-003 | Low | three |' >"$review_fixture"
  [ "$(review_finding_count_for_file "$review_fixture")" -eq 3 ] ||
    fail "contiguous review finding fixture was rejected"
  printf '%s\n' \
    '| CR045-001 | Low | one |' \
    '| CR045-003 | Low | gap |' >"$review_fixture"
  if review_finding_count_for_file "$review_fixture" >/dev/null; then
    fail "gapped review finding fixture was accepted"
  fi
  printf '%s\n' \
    '| CR045-001 | Low | one |' \
    '| CR045-001 | Low | duplicate |' >"$review_fixture"
  if review_finding_count_for_file "$review_fixture" >/dev/null; then
    fail "duplicate review finding fixture was accepted"
  fi
  : >"$review_fixture"
  if review_finding_count_for_file "$review_fixture" >/dev/null; then
    fail "empty review finding fixture was accepted"
  fi
  local_summary_fixture="$preflight_root/local-summary.json"
  jq -n \
    --arg digest "$(printf 'a%.0s' {1..64})" '
      {
        run:"run-fixture",
        artifacts:[
          {
            path:"run-fixture/dependencies/summary.json",
            sha256:$digest
          }
        ]
      }
    ' >"$local_summary_fixture"
  artifact_reference="$(
    local_run_artifact_reference \
      "$local_summary_fixture" "dependencies/summary.json"
  )" ||
    fail "exact local-run artifact fixture was rejected"
  jq -e '
    .path == "run-fixture/dependencies/summary.json" and
    (.sha256 | test("^[a-f0-9]{64}$"))
  ' <<<"$artifact_reference" >/dev/null ||
    fail "local-run artifact fixture resolved the wrong identity"
  jq '.artifacts += [.artifacts[0]]' \
    "$local_summary_fixture" >"$local_summary_fixture.duplicate"
  if local_run_artifact_reference \
    "$local_summary_fixture.duplicate" \
    "dependencies/summary.json" >/dev/null 2>&1; then
    fail "duplicate local-run artifact fixture was accepted"
  fi
  jq '.artifacts = []' \
    "$local_summary_fixture" >"$local_summary_fixture.missing"
  if local_run_artifact_reference \
    "$local_summary_fixture.missing" \
    "dependencies/summary.json" >/dev/null 2>&1; then
    fail "missing local-run artifact fixture was accepted"
  fi
  performance_fixture="$preflight_root/performance-summary.json"
  performance_invalid="$preflight_root/performance-summary-invalid.json"
  jq -n '
    {
      hostDiagnostics:{
        quietHostConfirmed:true,
        policy:
          "operator-confirmed-quiet-host-known-contention-invalidates-run",
        initialContentionAssessment:{
          passed:true,
          method:"three-one-second-process-snapshots-two-hit-rejection",
          samples:3,
          minimumHits:2,
          genericCPUPercentThreshold:50,
          virtualizationCPUPercentThreshold:5,
          buildOrTestCPUPercentThreshold:10,
          path:"host-contention-preflight.txt",
          sha256:("4" * 64)
        },
        measurementContentionAssessment:{
          passed:true,
          method:
            "continuous-one-second-three-hit-classified-contention-rejection-generic-diagnostics",
          samples:3,
          rollingWindow:3,
          minimumHits:3,
          genericHighCPUPolicy:"diagnostic-only",
          genericCPUPercentThreshold:50,
          virtualizationCPUPercentThreshold:5,
          buildOrTestCPUPercentThreshold:10,
          path:"host-contention-measurement.txt",
          sha256:("5" * 64)
        },
        snapshots:[
          {phase:"start",path:"host-state-start.txt",sha256:("1" * 64)},
          {phase:"before-real-lima",path:"host-state-before-real-lima.txt",sha256:("2" * 64)},
          {phase:"after-real-lima",path:"host-state-after-real-lima.txt",sha256:("3" * 64)}
        ]
      },
      metrics:{
        referenceWorkload:{
          methodology:{samples:30,warmups:2},
          elapsedOverhead:{
            threshold:10,
            thresholdPassed:true,
            confidence:{
              level:0.95,
              method:"one-sided-exact-binomial-order-statistic",
              rank:20,
              upperBound:8.5,
              thresholdPassed:true
            }
          }
        }
      },
      validation:{
        referenceMedianUpperConfidenceBoundWithinTenPercent:true,
        quietHostExplicitlyConfirmed:true,
        initialHostContentionAssessmentPassed:true,
        measurementHostContentionAssessmentPassed:true,
        hostDiagnosticsRetained:true
      }
    }
  ' >"$performance_fixture"
  validate_performance_evidence_contract "$performance_fixture" ||
    fail "valid performance confidence fixture was rejected"
  jq '.metrics.referenceWorkload.elapsedOverhead.confidence.upperBound = 12' \
    "$performance_fixture" >"$performance_invalid"
  if validate_performance_evidence_contract "$performance_invalid"; then
    fail "failing performance confidence fixture was accepted"
  fi
  jq '.hostDiagnostics.quietHostConfirmed = false' \
    "$performance_fixture" >"$performance_invalid"
  if validate_performance_evidence_contract "$performance_invalid"; then
    fail "unconfirmed performance host fixture was accepted"
  fi
  jq '.hostDiagnostics.initialContentionAssessment.passed = false' \
    "$performance_fixture" >"$performance_invalid"
  if validate_performance_evidence_contract "$performance_invalid"; then
    fail "failed performance contention assessment was accepted"
  fi
  jq '.hostDiagnostics.measurementContentionAssessment.passed = false' \
    "$performance_fixture" >"$performance_invalid"
  if validate_performance_evidence_contract "$performance_invalid"; then
    fail "failed measurement contention assessment was accepted"
  fi
  jq 'del(.validation.initialHostContentionAssessmentPassed)' \
    "$performance_fixture" >"$performance_invalid"
  if validate_performance_evidence_contract "$performance_invalid"; then
    fail "missing performance contention validation was accepted"
  fi
  jq 'del(.validation.measurementHostContentionAssessmentPassed)' \
    "$performance_fixture" >"$performance_invalid"
  if validate_performance_evidence_contract "$performance_invalid"; then
    fail "missing measurement contention validation was accepted"
  fi
  performance_contention_quiet="$preflight_root/host-contention-preflight.txt"
  performance_contention_busy="$preflight_root/host-contention-busy.txt"
  {
    printf '%s\n' \
      'schema=hideout.performance-host-contention/v1' \
      'method=three-one-second-process-snapshots-two-hit-rejection' \
      'samples=3' \
      'minimum_hits=2' \
      'generic_cpu_percent_threshold=50' \
      'virtualization_cpu_percent_threshold=5' \
      'build_or_test_cpu_percent_threshold=10' \
      'sample_begin=1' \
      '101 1 4.9 1.0 qemu-system-aarc' \
      'sample_end=1' \
      'sample_begin=2' \
      '101 1 4.9 1.0 qemu-system-aarc' \
      'sample_end=2' \
      'sample_begin=3' \
      '101 1 4.9 1.0 qemu-system-aarc' \
      'sample_end=3'
  } >"$performance_contention_quiet"
  validate_performance_contention_file "$performance_contention_quiet" ||
    fail "quiet raw host contention fixture was rejected"
  sed 's/4[.]9 1[.]0 qemu-system-aarc/12.0 1.0 qemu-system-aarc/' \
    "$performance_contention_quiet" >"$performance_contention_busy"
  if validate_performance_contention_file "$performance_contention_busy"; then
    fail "busy raw host contention fixture was accepted"
  fi
  performance_measurement_quiet="$preflight_root/host-contention-measurement.txt"
  performance_measurement_busy="$preflight_root/host-contention-measurement-busy.txt"
  performance_measurement_generic="$preflight_root/host-contention-measurement-generic.txt"
  performance_measurement_transient="$preflight_root/host-contention-measurement-transient.txt"
  performance_measurement_external_build="$preflight_root/host-contention-measurement-external-build.txt"
  performance_measurement_unowned="$preflight_root/host-contention-measurement-unowned.txt"
  performance_measurement_invalid_owner="$preflight_root/host-contention-measurement-invalid-owner.txt"
  performance_measurement_invalid_group="$preflight_root/host-contention-measurement-invalid-group.txt"
  {
    printf '%s\n' \
      'schema=hideout.performance-host-contention/v4' \
      'method=continuous-one-second-three-hit-classified-contention-rejection-generic-diagnostics' \
      'rolling_window=3' \
      'minimum_hits=3' \
      'generic_high_cpu_policy=diagnostic-only' \
      'generic_cpu_percent_threshold=50' \
      'virtualization_cpu_percent_threshold=5' \
      'build_or_test_cpu_percent_threshold=10' \
      'gate_process_group=900' \
      'measurement_process_group=901' \
      'sample_begin=1' \
      'owned_process=201:com.apple.Virtua:gate-private-runtime' \
      '101 1 101 9.9 1.0 Python' \
      '102 1 102 49.9 1.0 browser' \
      '201 1 201 80.0 1.0 com.apple.Virtua' \
      '301 1 900 99.0 1.0 go' \
      '302 1 901 99.0 1.0 link' \
      'sample_end=1' \
      'sample_begin=2' \
      '101 1 101 9.9 1.0 Python' \
      '102 1 102 49.9 1.0 browser' \
      '201 1 201 80.0 1.0 com.apple.Virtua' \
      '301 1 900 99.0 1.0 go' \
      '302 1 901 99.0 1.0 link' \
      'sample_end=2' \
      'sample_begin=3' \
      '101 1 101 9.9 1.0 Python' \
      '102 1 102 49.9 1.0 browser' \
      '201 1 201 80.0 1.0 com.apple.Virtua' \
      '301 1 900 99.0 1.0 go' \
      '302 1 901 99.0 1.0 link' \
      'sample_end=3'
  } >"$performance_measurement_quiet"
  validate_performance_measurement_contention_file \
    "$performance_measurement_quiet" 3 ||
    fail "quiet raw measurement contention fixture was rejected"
  awk '
    /^sample_begin=/ {
      split($0, fields, "=")
      sample = fields[2] + 0
    }
    sample <= 3 && $6 == "Python" {$4 = "55.0"}
    {print}
  ' "$performance_measurement_quiet" >"$performance_measurement_busy"
  if validate_performance_measurement_contention_file \
    "$performance_measurement_busy" 3; then
    fail "busy raw measurement contention fixture was accepted"
  fi
  awk '
    $6 == "browser" {$4 = "55.0"}
    {print}
  ' "$performance_measurement_quiet" >"$performance_measurement_generic"
  validate_performance_measurement_contention_file \
    "$performance_measurement_generic" 3 ||
    fail "diagnostic generic CPU fixture was treated as blocking"
  awk '
    /^sample_begin=/ {
      split($0, fields, "=")
      sample = fields[2] + 0
    }
    sample <= 2 && $6 == "Python" {$4 = "55.0"}
    {print}
  ' "$performance_measurement_quiet" >"$performance_measurement_transient"
  validate_performance_measurement_contention_file \
    "$performance_measurement_transient" 3 ||
    fail "two-hit transient measurement fixture was rejected"
  sed 's/302 1 901 99[.]0 1[.]0 link/302 1 902 99.0 1.0 link/' \
    "$performance_measurement_quiet" \
    >"$performance_measurement_external_build"
  if validate_performance_measurement_contention_file \
    "$performance_measurement_external_build" 3; then
    fail "external raw measurement build fixture was accepted"
  fi
  sed '/^owned_process=/d' \
    "$performance_measurement_quiet" >"$performance_measurement_unowned"
  if validate_performance_measurement_contention_file \
    "$performance_measurement_unowned" 3; then
    fail "unowned active measurement VM fixture was accepted"
  fi
  sed \
    's/owned_process=201:com[.]apple[.]Virtua/owned_process=101:browser/' \
    "$performance_measurement_quiet" \
    >"$performance_measurement_invalid_owner"
  if validate_performance_measurement_contention_file \
    "$performance_measurement_invalid_owner" 3; then
    fail "invalid measurement ownership fixture was accepted"
  fi
  sed \
    's/measurement_process_group=901/measurement_process_group=900/' \
    "$performance_measurement_quiet" \
    >"$performance_measurement_invalid_group"
  if validate_performance_measurement_contention_file \
    "$performance_measurement_invalid_group" 3; then
    fail "non-isolated raw measurement group fixture was accepted"
  fi
  chmod 0600 "$performance_contention_quiet"
  chmod 0600 "$performance_measurement_quiet"
  performance_snapshot_start="$preflight_root/host-state-start.txt"
  performance_snapshot_before="$preflight_root/host-state-before-real-lima.txt"
  performance_snapshot_after="$preflight_root/host-state-after-real-lima.txt"
  printf '%s\n' \
    'schema=hideout.performance-host-state/v1' \
    'phase=start' \
    'top_processes_begin' \
    'top_processes_end' >"$performance_snapshot_start"
  printf '%s\n' \
    'schema=hideout.performance-host-state/v1' \
    'phase=before-real-lima' \
    'top_processes_begin' \
    'top_processes_end' >"$performance_snapshot_before"
  printf '%s\n' \
    'schema=hideout.performance-host-state/v1' \
    'phase=after-real-lima' \
    'top_processes_begin' \
    'top_processes_end' >"$performance_snapshot_after"
  chmod 0600 \
    "$performance_snapshot_start" \
    "$performance_snapshot_before" \
    "$performance_snapshot_after"
  performance_bound_fixture="$preflight_root/performance-summary-bound.json"
  jq \
    --arg contentionSHA "$(sha256_file "$performance_contention_quiet")" \
    --arg measurementContentionSHA \
      "$(sha256_file "$performance_measurement_quiet")" \
    --arg startSHA "$(sha256_file "$performance_snapshot_start")" \
    --arg beforeSHA "$(sha256_file "$performance_snapshot_before")" \
    --arg afterSHA "$(sha256_file "$performance_snapshot_after")" '
      .hostDiagnostics.initialContentionAssessment.sha256 = $contentionSHA |
      .hostDiagnostics.measurementContentionAssessment.sha256 =
        $measurementContentionSHA |
      .hostDiagnostics.snapshots[0].sha256 = $startSHA |
      .hostDiagnostics.snapshots[1].sha256 = $beforeSHA |
      .hostDiagnostics.snapshots[2].sha256 = $afterSHA |
      .artifacts = [
        {
          path:"host-contention-preflight.txt",
          sha256:$contentionSHA,
          mode:"0600"
        },
        {
          path:"host-contention-measurement.txt",
          sha256:$measurementContentionSHA,
          mode:"0600"
        },
        {path:"host-state-start.txt",sha256:$startSHA,mode:"0600"},
        {
          path:"host-state-before-real-lima.txt",
          sha256:$beforeSHA,
          mode:"0600"
        },
        {
          path:"host-state-after-real-lima.txt",
          sha256:$afterSHA,
          mode:"0600"
        }
      ]
    ' "$performance_fixture" >"$performance_bound_fixture"
  verify_performance_host_diagnostics \
    "$performance_bound_fixture" "$preflight_root"
  jq '.hostDiagnostics.initialContentionAssessment.sha256 = ("f" * 64)' \
    "$performance_bound_fixture" >"$performance_invalid"
  if (
    verify_performance_host_diagnostics \
      "$performance_invalid" "$preflight_root"
  ) >/dev/null 2>&1; then
    fail "mismatched host diagnostic digest fixture was accepted"
  fi
  jq '.hostDiagnostics.measurementContentionAssessment.sha256 = ("e" * 64)' \
    "$performance_bound_fixture" >"$performance_invalid"
  if (
    verify_performance_host_diagnostics \
      "$performance_invalid" "$preflight_root"
  ) >/dev/null 2>&1; then
    fail "mismatched measurement diagnostic digest fixture was accepted"
  fi
  jq '.hostDiagnostics.measurementContentionAssessment.samples = 4' \
    "$performance_bound_fixture" >"$performance_invalid"
  if (
    verify_performance_host_diagnostics \
      "$performance_invalid" "$preflight_root"
  ) >/dev/null 2>&1; then
    fail "mismatched measurement sample inventory was accepted"
  fi
  gate_completed=1
  printf 'collect-evidence: preflight=passed\n'
}

if [ "$preflight_only" -eq 1 ]; then
  run_preflight
  exit 0
fi

for required_command in git go jq tar find sort comm awk sed grep stat cmp \
  seq sleep; do
  require_command "$required_command" || exit 1
done

[ "$(uname -s)" = "Darwin" ] &&
  [ "$(uname -m)" = "arm64" ] ||
  fail "full collection requires Darwin/arm64"

source_status="$(git status --porcelain=v1 --untracked-files=all)"
[ -z "$source_status" ] ||
  fail "exact evidence collection requires a completely clean source tree"

source_commit="$(git rev-parse HEAD)"
source_tree="$(git rev-parse 'HEAD^{tree}')"
source_epoch="$(git show -s --format=%ct HEAD)"
source_committed_at="$(git show -s --format=%cI HEAD)"
[[ "$source_commit" =~ ^[a-f0-9]{40}$ ]] ||
  fail "source commit identity is invalid"
[[ "$source_tree" =~ ^[a-f0-9]{40}$ ]] ||
  fail "source tree identity is invalid"
[[ "$source_epoch" =~ ^[0-9]+$ ]] ||
  fail "source commit timestamp is invalid"

if [ -L "$artifact_root" ]; then
  fail "artifact root must not be a symlink"
fi
[ -d "$artifact_root" ] ||
  fail "artifact root does not exist"
artifact_root="$(CDPATH='' cd -- "$artifact_root" && pwd -P)"
case "$artifact_root" in
  "$repo_root"/.artifacts/045 | "$repo_root"/.artifacts/045/*)
    ;;
  *)
    fail "artifact root must remain under .artifacts/045"
    ;;
esac

output_parent="$(dirname -- "$output")"
mkdir -p "$output_parent"
if [ -L "$output_parent" ]; then
  fail "evidence output parent must not be a symlink"
fi
output_parent="$(CDPATH='' cd -- "$output_parent" && pwd -P)"
output="$output_parent/$(basename -- "$output")"
case "$output" in
  "$artifact_root"/*)
    ;;
  *)
    fail "evidence output must remain inside the artifact root"
    ;;
esac
chmod 0700 "$output_parent"

scratch="$(
  mktemp -d "$tmp_base/hideout-collect-evidence.XXXXXX"
)"
cleanup() {
  local exit_status=$?
  stop_installed_validation_daemon
  cleanup_tree "${scratch:-}" "hideout-collect-evidence"
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "collect-evidence"
  fi
}
trap cleanup EXIT

artifact_ref() {
  local evidence_file="$1" relative
  [ -f "$evidence_file" ] &&
    [ ! -L "$evidence_file" ] ||
    fail "cannot reference missing or unsafe file: $evidence_file"
  relative="${evidence_file#"$repo_root"/}"
  [ "$relative" != "$evidence_file" ] ||
    fail "referenced file is outside repository: $evidence_file"
  jq -nc \
    --arg path "$relative" \
    --arg sha256 "$(sha256_file "$evidence_file")" \
    --argjson bytes "$(file_bytes "$evidence_file")" \
    --arg mode "$(normalized_mode "$evidence_file")" \
    '{path:$path,sha256:$sha256,bytes:$bytes,mode:$mode}'
}

validate_fresh_json() {
  local evidence_file="$1"
  jq -e \
    --argjson sourceEpoch "$source_epoch" '
      (.generatedAt | type) == "string" and
      ((.generatedAt | fromdateiso8601) >= $sourceEpoch)
    ' "$evidence_file" >/dev/null ||
    fail "evidence predates the candidate commit: $evidence_file"
}

validate_closure_receipt() {
  local receipt="$1" schema="$2" receipt_root
  local path expected_sha expected_bytes evidence_file
  go run ./cmd/hideout-schema-validate \
    "$schema" "$receipt" >/dev/null ||
    fail "closure receipt failed JSON schema validation: $receipt"
  jq -e '
    ([.artifacts[].path] | length) > 0 and
    ([.artifacts[].path] | unique | length) ==
      ([.artifacts[].path] | length)
  ' "$receipt" >/dev/null ||
    fail "closure receipt artifact paths are empty or duplicated: $receipt"
  receipt_root="$(
    CDPATH='' cd -- "$(dirname -- "$receipt")" && pwd -P
  )"
  while IFS=$'\t' read -r path expected_sha expected_bytes; do
    safe_relative_path "$path" ||
      fail "closure receipt contains an unsafe artifact path: $path"
    evidence_file="$receipt_root/$path"
    require_private_evidence_file "$evidence_file"
    verify_sha256 "$evidence_file" "$expected_sha" ||
      fail "closure receipt artifact digest is invalid: $path"
    [ "$(file_bytes "$evidence_file")" = "$expected_bytes" ] ||
      fail "closure receipt artifact byte count is invalid: $path"
  done < <(
    jq -r \
      '.artifacts[] | [.path,.sha256,(.bytes|tostring)] | @tsv' \
      "$receipt"
  )
}

validate_installed_candidate() {
  local receipt="$1" receipt_prefix receipt_store expected_prefix expected_store
  local installed expected_binary_sha packaged_binary_sha environment_count
  local daemon_ready=0
  require_command brew ||
    fail "installed-candidate verification requires Homebrew"
  receipt_prefix="$(jq -er '.installation.prefix' "$receipt")"
  receipt_store="$(jq -er '.installation.store' "$receipt")"
  expected_prefix="$(brew --prefix)"
  expected_prefix="$(
    CDPATH='' cd -- "$expected_prefix" && pwd -P
  )"
  expected_store="$(
    CDPATH='' cd -- "$HOME" && printf '%s/.hideout\n' "$(pwd -P)"
  )"
  if [ "$receipt_prefix" != "$expected_prefix" ] ||
    [ "$receipt_store" != "$expected_store" ]; then
    fail "local-install receipt does not target this exact installation/store"
  fi
  if [ ! -d "$receipt_prefix" ] || [ -L "$receipt_prefix" ] ||
    [ ! -d "$receipt_store" ] || [ -L "$receipt_store" ]; then
    fail "installed candidate prefix/store is missing or unsafe"
  fi
  [ "$(normalized_mode "$receipt_store")" = "0700" ] ||
    fail "installed candidate store is not private mode 0700"

  installed="$receipt_prefix/bin/hideout"
  [ -f "$installed" ] && [ ! -L "$installed" ] &&
    [ -x "$installed" ] ||
    fail "installed candidate binary is missing or unsafe"
  expected_binary_sha="$(
    jq -er '.candidate.installedBinarySHA256' "$receipt"
  )"
  packaged_binary_sha="$(sha256_file "$package_root/bin/hideout")"
  if [ "$expected_binary_sha" != "$packaged_binary_sha" ] ||
    ! verify_sha256 "$installed" "$packaged_binary_sha"; then
    fail "installed binary does not match the exact packaged candidate"
  fi

  "$installed" version --json \
    >"$scratch/installed-candidate-version.json"
  jq -e \
    --arg version "$candidate_version" \
    --arg commit "$source_commit" '
      .schema == "hideout.binary-identity/v1" and
      .productVersion == $version and
      .sourceCommit == $commit and
      .hostOS == "darwin" and
      .hostArch == "arm64"
    ' "$scratch/installed-candidate-version.json" >/dev/null ||
    fail "installed candidate binary identity is invalid"
  "$installed" package verify "$receipt_prefix" \
    >"$scratch/installed-candidate-package-verify.out" \
    2>"$scratch/installed-candidate-package-verify.err" ||
    fail "installed candidate package verification failed"
  if "$installed" daemon status \
    >"$scratch/installed-candidate-daemon.out" \
    2>"$scratch/installed-candidate-daemon.err"; then
    fail "installed candidate daemon is not in the required stopped state"
  fi
  installed_validation_binary="$installed"
  "$installed" daemon start \
    >"$scratch/installed-candidate-daemon-start.out" \
    2>"$scratch/installed-candidate-daemon-start.err" &
  installed_validation_daemon_pid=$!
  for _ in $(seq 1 200); do
    if "$installed" daemon status \
      >"$scratch/installed-candidate-daemon-serving.json" \
      2>"$scratch/installed-candidate-daemon-serving.err" &&
      daemon_status_is_serving \
        "$scratch/installed-candidate-daemon-serving.json"; then
      daemon_ready=1
      break
    fi
    kill -0 "$installed_validation_daemon_pid" 2>/dev/null ||
      fail "installed candidate daemon exited before serving"
    sleep 0.1
  done
  [ "$daemon_ready" -eq 1 ] ||
    fail "installed candidate daemon did not reach the serving state"
  "$installed" env list \
    >"$scratch/installed-candidate-environments.out"
  environment_count="$(
    awk -F '\t' \
      '$NF ~ /^env_[A-Za-z0-9_-]+$/ {count++} END {print count+0}' \
      "$scratch/installed-candidate-environments.out"
  )"
  [ "$environment_count" -eq 0 ] ||
    fail "installed candidate still retains an environment"
  "$installed" show connection \
    >"$scratch/installed-candidate-connection.out" \
    2>"$scratch/installed-candidate-connection.err"
  grep -Fqi 'direct' "$scratch/installed-candidate-connection.out" ||
    fail "installed candidate final profile is not direct-network"
  stop_installed_validation_daemon
  if "$installed" daemon status \
    >"$scratch/installed-candidate-daemon-final.out" \
    2>"$scratch/installed-candidate-daemon-final.err"; then
    installed_validation_binary="$installed"
    fail "installed candidate daemon remained reachable after validation"
  fi
}

validate_simple_summary() {
  local evidence_file="$1" expected_schema="$2"
  require_private_evidence_file "$evidence_file"
  jq -e \
    --arg schema "$expected_schema" \
    --arg commit "$source_commit" '
      .schema == $schema and
      .result == "passed" and
      .source.commit == $commit and
      .source.dirty == false
    ' "$evidence_file" >/dev/null ||
    fail "simple gate summary is not an exact passing candidate result: $evidence_file"
  validate_fresh_json "$evidence_file"
}

resolved_summary=""
resolve_source_pointer() {
  local pointer="$1" pointer_schema="$2" summary_schema="$3"
  local summary_relative summary_sha pointer_dir
  require_private_evidence_file "$pointer"
  jq -e \
    --arg schema "$pointer_schema" \
    --arg commit "$source_commit" '
      .schema == $schema and
      .result == "passed" and
      .candidateAcceptance == true and
      .source.commit == $commit and
      .source.dirty == false
    ' "$pointer" >/dev/null ||
    fail "candidate gate pointer is not exact and accepted: $pointer"
  validate_fresh_json "$pointer"
  summary_relative="$(jq -er '.summary' "$pointer")"
  summary_sha="$(jq -er '.summarySHA256' "$pointer")"
  safe_relative_path "$summary_relative" ||
    fail "candidate gate pointer contains an unsafe summary path"
  pointer_dir="$(
    CDPATH='' cd -- "$(dirname -- "$pointer")" && pwd -P
  )"
  resolved_summary="$pointer_dir/$summary_relative"
  require_private_evidence_file "$resolved_summary"
  verify_sha256 "$resolved_summary" "$summary_sha" ||
    fail "candidate gate summary digest does not match pointer: $pointer"
  jq -e \
    --arg schema "$summary_schema" \
    --arg commit "$source_commit" '
      .schema == $schema and
      .result == "passed" and
      .candidateAcceptance == true and
      .source.commit == $commit and
      .source.dirty == false
    ' "$resolved_summary" >/dev/null ||
    fail "candidate gate summary is not exact and accepted: $resolved_summary"
  validate_fresh_json "$resolved_summary"
}

package_pointer="$artifact_root/package/result.json"
package_lifecycle_pointer="$artifact_root/package-lifecycle/result.json"
formal_summary="$artifact_root/formal/summary.json"
local_summary="$artifact_root/local/summary.json"
component_summary="$artifact_root/package-components/summary.json"
recovery_summary="$artifact_root/recovery/summary.json"
privacy_pointer="$artifact_root/privacy/result.json"
ui_pointer="$artifact_root/ui/result.json"
performance_pointer="$artifact_root/performance/result.json"
lima_pointer="$artifact_root/lima/result.json"
review_file="$repo_root/docs/release/045-code-review.md"
claim_matrix="$repo_root/docs/release/045-claim-matrix.md"
formal_inventory_source="$repo_root/formal/inventory.json"

validate_simple_summary "$formal_summary" "hideout.formal-gate/v1"
jq -e '
  .candidateAcceptance == false and
  .inventory.configurationCount == 12 and
  .inventory.moduleCount == 10 and
  .inventory.invariantCount == 76 and
  .inventory.propertyCount == 19 and
  .inventory.goTestCount == 12 and
  all(.configurations[]; .result == "passed") and
  .goRefinement.result == "passed" and
  all(.goRefinement.tests[]; .result == "passed") and
  all(.negativeJudgeProofs[]; .result == "killed")
' "$formal_summary" >/dev/null ||
  fail "formal gate summary is incomplete"

validate_simple_summary "$local_summary" \
  "hideout.local-release-candidate/v1"
jq -e '
  .candidateAcceptance == false and
  all(.lanes[]; .result == "passed") and
  .statistics.failedLanes == 0
' "$local_summary" >/dev/null ||
  fail "local release aggregate is incomplete"

if ! dependency_reference="$(
  local_run_artifact_reference \
    "$local_summary" "dependencies/summary.json"
)"; then
  fail "local release aggregate does not bind one exact dependency summary"
fi
dependency_summary_relative="$(jq -er '.path' <<<"$dependency_reference")"
dependency_summary_sha="$(jq -er '.sha256' <<<"$dependency_reference")"
safe_relative_path "$dependency_summary_relative" ||
  fail "local dependency summary path is unsafe"
dependency_summary="$artifact_root/local/$dependency_summary_relative"
require_private_evidence_file "$dependency_summary"
verify_sha256 "$dependency_summary" "$dependency_summary_sha" ||
  fail "local dependency summary digest does not match its aggregate"
validate_simple_summary "$dependency_summary" \
  "hideout.dependencies-gate/v1"
jq -e '
  .advisories.reachableImportedPackageFindings == 0 and
  all(.checks[]; . == "passed")
' "$dependency_summary" >/dev/null ||
  fail "dependency/advisory evidence is incomplete"

validate_simple_summary "$component_summary" \
  "hideout.package-components-gate/v1"
jq -e '
  .candidateAcceptance == false and
  all(.checks[]; . == "passed")
' "$component_summary" >/dev/null ||
  fail "package-component evidence is incomplete"

validate_simple_summary "$recovery_summary" \
  "hideout.recovery-gate-evidence/v1"
jq -e '
  .crashMatrix.points == 16 and
  all(.mutationProofs[]; .result == "killed")
' "$recovery_summary" >/dev/null ||
  fail "recovery evidence is incomplete"

resolve_source_pointer \
  "$privacy_pointer" \
  "hideout.release-candidate-privacy-pointer/v1" \
  "hideout.release-candidate-privacy-evidence/v1"
privacy_summary="$resolved_summary"

resolve_source_pointer \
  "$ui_pointer" \
  "hideout.release-candidate-ui-pointer/v1" \
  "hideout.release-candidate-ui-evidence/v1"
ui_summary="$resolved_summary"

resolve_source_pointer \
  "$performance_pointer" \
  "hideout.release-candidate-performance-pointer/v1" \
  "hideout.release-candidate-performance/v1"
performance_summary="$resolved_summary"
validate_performance_evidence_contract "$performance_summary" ||
  fail "performance evidence lacks quiet-host contention/confidence proof"
verify_performance_host_diagnostics "$performance_summary"

resolve_source_pointer \
  "$lima_pointer" \
  "hideout.release-candidate-lima-pointer/v1" \
  "hideout.release-candidate-lima-evidence/v1"
lima_summary="$resolved_summary"

require_private_evidence_file "$package_pointer"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" '
    .schema == "hideout.release-package-candidate-pointer/v1" and
    .result == "passed" and
    .candidateAcceptance == true and
    .publicationStatus == "local-only" and
    .source == {commit:$commit,tree:$tree,dirty:false}
  ' "$package_pointer" >/dev/null ||
  fail "package pointer is not the exact accepted local candidate"
validate_fresh_json "$package_pointer"

package_summary_relative="$(jq -er '.summary' "$package_pointer")"
package_summary_sha="$(jq -er '.summarySHA256' "$package_pointer")"
safe_relative_path "$package_summary_relative" ||
  fail "package pointer contains an unsafe summary path"
package_summary="$artifact_root/package/$package_summary_relative"
require_private_evidence_file "$package_summary"
verify_sha256 "$package_summary" "$package_summary_sha" ||
  fail "package summary digest does not match its pointer"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" '
    .schema == "hideout.release-package-candidate/v1" and
    .result == "passed" and
    .source.commit == $commit and
    .source.tree == $tree and
    .source.dirty == false and
    .candidate.acceptance == true and
    .candidate.publicationStatus == "local-only" and
    .reproducibility.archiveBytesIdentical == true and
    .reproducibility.packageManifestBytesIdentical == true and
    .reproducibility.packageTreeInventoryIdentical == true and
    all(.validation[]; . == true)
  ' "$package_summary" >/dev/null ||
  fail "package summary is not an exact reproducible local candidate"
validate_fresh_json "$package_summary"

candidate_version="$(jq -er '.candidate.version' "$package_summary")"
candidate_archive_relative="$(jq -er '.candidate.archive' "$package_summary")"
candidate_archive_sha="$(jq -er '.candidate.archiveSHA256' "$package_summary")"
package_manifest_relative="$(jq -er '.candidate.packageManifest' "$package_summary")"
package_manifest_sha="$(
  jq -er '.candidate.packageManifestSHA256' "$package_summary"
)"
safe_relative_path "$candidate_archive_relative" ||
  fail "package summary contains an unsafe archive path"
safe_relative_path "$package_manifest_relative" ||
  fail "package summary contains an unsafe manifest path"
candidate_archive="$artifact_root/package/$candidate_archive_relative"
package_manifest_evidence="$artifact_root/package/$package_manifest_relative"
require_private_evidence_file "$candidate_archive"
require_private_evidence_file "$package_manifest_evidence"
verify_sha256 "$candidate_archive" "$candidate_archive_sha" ||
  fail "candidate archive digest does not match package summary"
verify_sha256 "$package_manifest_evidence" "$package_manifest_sha" ||
  fail "package manifest digest does not match package summary"

require_private_evidence_file "$package_lifecycle_pointer"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg archiveSHA256 "$candidate_archive_sha" '
    .schema == "hideout.release-package-lifecycle-pointer/v1" and
    .result == "passed" and
    .candidateAcceptance == true and
    .publicationStatus == "local-only" and
    .sourceCandidate == {commit:$commit,tree:$tree,dirty:false} and
    .candidateArchiveSHA256 == $archiveSHA256
  ' "$package_lifecycle_pointer" >/dev/null ||
  fail "package lifecycle pointer is not bound to the exact candidate"
validate_fresh_json "$package_lifecycle_pointer"
lifecycle_summary_relative="$(
  jq -er '.summary' "$package_lifecycle_pointer"
)"
lifecycle_summary_sha="$(
  jq -er '.summarySHA256' "$package_lifecycle_pointer"
)"
safe_relative_path "$lifecycle_summary_relative" ||
  fail "package lifecycle pointer contains an unsafe summary path"
lifecycle_summary="$artifact_root/package-lifecycle/$lifecycle_summary_relative"
require_private_evidence_file "$lifecycle_summary"
verify_sha256 "$lifecycle_summary" "$lifecycle_summary_sha" ||
  fail "package lifecycle summary digest does not match pointer"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg version "$candidate_version" \
  --arg archiveSHA256 "$candidate_archive_sha" '
    .schema == "hideout.release-package-lifecycle/v1" and
    .result == "passed" and
    .sourceCandidate.commit == $commit and
    .sourceCandidate.tree == $tree and
    .sourceCandidate.dirty == false and
    .candidate.version == $version and
    .candidate.archiveSHA256 == $archiveSHA256 and
    .candidate.consumedWithoutRebuild == true and
    .candidate.acceptance == true and
    .publicationStatus == "local-only" and
    all(.checks[]; . == true)
  ' "$lifecycle_summary" >/dev/null ||
  fail "package lifecycle summary is incomplete or mismatched"
validate_fresh_json "$lifecycle_summary"

tar -tzf "$candidate_archive" >"$scratch/archive-entries.txt"
[ -s "$scratch/archive-entries.txt" ] ||
  fail "candidate archive is empty"
while IFS= read -r archive_entry; do
  case "$archive_entry" in
    hideout | hideout/ | hideout/*)
      ;;
    *)
      fail "candidate archive contains an entry outside hideout/: $archive_entry"
      ;;
  esac
  case "$archive_entry" in
    /* | *'/../'* | ../* | */.. | *$'\n'* | *$'\r'* | *$'\t'*)
      fail "candidate archive contains an unsafe entry: $archive_entry"
      ;;
  esac
done <"$scratch/archive-entries.txt"

mkdir "$scratch/extracted"
tar -xzf "$candidate_archive" -C "$scratch/extracted"
package_root="$scratch/extracted/hideout"
[ -d "$package_root" ] &&
  [ ! -L "$package_root" ] ||
  fail "candidate archive lacks one safe hideout root"
if find "$package_root" -type l -print -quit | grep -q . ||
  find "$package_root" ! -type f ! -type d -print -quit | grep -q .; then
  fail "candidate package contains a symlink or special file"
fi
cmp "$package_manifest_evidence" "$package_root/package-manifest.json" \
  >/dev/null ||
  fail "archive package manifest differs from retained package manifest"
jq -e \
  --arg commit "$source_commit" \
  --arg version "$candidate_version" '
    .schema == "hideout.package-manifest/v1" and
    .release.productVersion == $version and
    .release.tag == ("v" + $version) and
    .source.commit == $commit and
    .source.dirty == false and
    .signingSummary.mode == "developer-preview-unsigned" and
    ([.files[].path] == ([.files[].path] | sort)) and
    ([.files[].path] | unique | length) == (.files | length)
  ' "$package_root/package-manifest.json" >/dev/null ||
  fail "retained package manifest identity is invalid"

jq -r '.files[].path' "$package_root/package-manifest.json" \
  >"$scratch/manifest-files.txt"
(
  cd "$package_root"
  find . -type f ! -name package-manifest.json -print |
    sed 's#^\./##' |
    LC_ALL=C sort
) >"$scratch/package-files.txt"
if [ -n "$(
  comm -3 "$scratch/manifest-files.txt" "$scratch/package-files.txt"
)" ]; then
  fail "candidate package file set differs from package manifest"
fi

: >"$scratch/package-files.jsonl"
while IFS= read -r package_entry; do
  package_path="$(jq -er '.path' <<<"$package_entry")"
  package_kind="$(jq -er '.kind' <<<"$package_entry")"
  package_sha="$(jq -er '.sha256' <<<"$package_entry")"
  package_executable="$(
    package_manifest_executable_value <<<"$package_entry"
  )"
  safe_relative_path "$package_path" ||
    fail "package manifest contains an unsafe path: $package_path"
  packaged_file="$package_root/$package_path"
  [ -f "$packaged_file" ] &&
    [ ! -L "$packaged_file" ] ||
    fail "package manifest file is missing or unsafe: $package_path"
  verify_sha256 "$packaged_file" "$package_sha" ||
    fail "package file digest mismatch: $package_path"
  if [ "$package_executable" = "true" ]; then
    [ -x "$packaged_file" ] ||
      fail "declared executable is not executable: $package_path"
  elif [ -x "$packaged_file" ]; then
    fail "undeclared executable bit is present: $package_path"
  fi
  jq -nc \
    --arg path "$package_path" \
    --arg kind "$package_kind" \
    --arg sha256 "$package_sha" \
    --argjson bytes "$(file_bytes "$packaged_file")" \
    --arg mode "$(normalized_mode "$packaged_file")" \
    --argjson executable "$package_executable" '
      {
        path:$path,
        kind:$kind,
        sha256:$sha256,
        bytes:$bytes,
        mode:$mode,
        executable:$executable
      }
    ' >>"$scratch/package-files.jsonl"
done < <(jq -c '.files[]' "$package_root/package-manifest.json")
jq -s . "$scratch/package-files.jsonl" >"$scratch/package-files.json"

package_ref() {
  local relative_path="$1"
  jq -e -c \
    --arg path "$relative_path" '
      .[] | select(.path == $path)
    ' "$scratch/package-files.json"
}

jq '
  [
    .[] |
    select(.kind == "helper" or .kind == "helper-manifest")
  ]
' "$scratch/package-files.json" >"$scratch/helpers.json"
[ "$(jq '[.[] | select(.kind == "helper-manifest")] | length' \
  "$scratch/helpers.json")" -eq 6 ] ||
  fail "candidate helper manifest count is not six"
package_guest_arch="$(jq -er '.target.linuxGuestArch' \
  "$package_root/package-manifest.json")"
helper_suffix="-linux-$package_guest_arch"
while IFS= read -r helper_manifest_relative; do
  helper_manifest="$package_root/$helper_manifest_relative"
  helper_binary_relative="${helper_manifest_relative%.manifest.json}"
  helper_binary="$package_root/$helper_binary_relative"
  [ -f "$helper_binary" ] &&
    [ ! -L "$helper_binary" ] ||
    fail "helper manifest lacks its binary: $helper_manifest_relative"
  helper_artifact="$(basename -- "$helper_binary_relative")"
  case "$helper_artifact" in
    *"$helper_suffix")
      helper_command="${helper_artifact%"$helper_suffix"}"
      ;;
    *)
      fail "helper artifact does not bind the guest architecture: $helper_artifact"
      ;;
  esac
  helper_manifest_binding_valid \
    "$helper_manifest" "$helper_binary" \
    "$helper_command" "$package_guest_arch" ||
    fail "helper manifest binding is invalid: $helper_manifest_relative"
done < <(
  jq -r \
    '.[] | select(.kind == "helper-manifest") | .path' \
    "$scratch/helpers.json"
)

browser_manifest="$package_root/runtime/browser-console.assets.json"
browser_container="$package_root/bin/hideout"
[ -f "$browser_manifest" ] &&
  [ -f "$browser_container" ] ||
  fail "candidate lacks embedded browser evidence"
"$browser_container" package embedded-assets \
  >"$scratch/browser-live.json"
jq -S . "$browser_manifest" >"$scratch/browser-packaged.sorted.json"
jq -S . "$scratch/browser-live.json" >"$scratch/browser-live.sorted.json"
cmp "$scratch/browser-packaged.sorted.json" \
  "$scratch/browser-live.sorted.json" >/dev/null ||
  fail "embedded browser inventory differs from the packaged binary"
jq -e \
  --arg containerSHA256 "$(sha256_file "$browser_container")" '
    .schema == "hideout.embedded-asset-manifest/v1" and
    .id == "browser-console" and
    .container == "bin/hideout" and
    .containerSHA256 == $containerSHA256 and
    (.assets | length) == 8 and
    ([.assets[].path] | unique | length) == 8 and
    all(.assets[]; .sha256 | test("^[a-f0-9]{64}$"))
  ' "$browser_manifest" >/dev/null ||
  fail "embedded browser manifest is incomplete"

runtime_catalog="$package_root/runtime/catalog.json"
runtime_contract="$package_root/runtime/contract.json"
[ -f "$runtime_catalog" ] &&
  [ -f "$runtime_contract" ] ||
  fail "candidate lacks runtime catalog or contract"
jq -e \
  --arg catalogSHA256 "$(sha256_file "$runtime_catalog")" '
    .runtime.catalogFileSHA256 == $catalogSHA256
  ' "$package_root/package-manifest.json" >/dev/null ||
  fail "package runtime catalog digest is not bound"
runtime_revision="$(
  jq -er '.runtime.revision' "$package_root/package-manifest.json"
)"
runtime_artifact_sha="$(
  jq -er '.runtime.artifactSHA256' "$package_root/package-manifest.json"
)"
runtime_contract_sha="$(sha256_file "$runtime_contract")"
jq -e \
  --arg revision "$runtime_revision" \
  --arg artifactSHA256 "$runtime_artifact_sha" \
  --arg contractSHA256 "$runtime_contract_sha" '
    any(.families[];
      .id == "developer-standard" and
      .currentRevision == $revision and
      any(.revisions[];
        .id == $revision and
        .contractDigest == ("sha256:" + $contractSHA256) and
        any(.artifacts[];
          .hostOS == "darwin" and
          .hostArch == "arm64" and
          .sha256 == $artifactSHA256
        )
      )
    )
  ' "$runtime_catalog" >/dev/null ||
  fail "runtime catalog, contract, and artifact binding is invalid"

formal_inventory_relative="$(jq -er '.inventory.path' "$formal_summary")"
formal_inventory_sha="$(jq -er '.inventory.sha256' "$formal_summary")"
safe_relative_path "$formal_inventory_relative" ||
  fail "formal inventory evidence path is unsafe"
formal_inventory_evidence="$artifact_root/formal/$formal_inventory_relative"
require_private_evidence_file "$formal_inventory_evidence"
verify_sha256 "$formal_inventory_evidence" "$formal_inventory_sha" ||
  fail "formal inventory digest does not match formal summary"
cmp "$formal_inventory_source" "$formal_inventory_evidence" >/dev/null ||
  fail "formal evidence inventory differs from the candidate source"

[ -f "$review_file" ] &&
  [ ! -L "$review_file" ] ||
  fail "final code-review report is missing or unsafe"
[ -f "$claim_matrix" ] &&
  [ ! -L "$claim_matrix" ] ||
  fail "claim matrix is missing or unsafe"
if ! review_finding_count="$(
  review_finding_count_for_file "$review_file"
)"; then
  fail "final code-review report finding IDs are empty, duplicated, out of order, or non-contiguous"
fi
grep -Fq 'There is no open required review finding.' "$review_file" ||
  fail "final code-review report lacks closed-required disposition"

source_manifest_relative="$(
  jq -er '.source.manifestSHA256' "$package_summary"
)"
source_manifest_path="$(
  jq -r \
    --arg digest "$source_manifest_relative" '
      .artifacts[] |
      select(
        (.path | endswith("/source-manifest.tsv")) and
        .sha256 == $digest
      ) |
      .path
    ' "$package_summary"
)"
[ -n "$source_manifest_path" ] ||
  fail "package summary lacks the exact source-manifest artifact"
safe_relative_path "$source_manifest_path" ||
  fail "package source-manifest path is unsafe"
source_manifest="$artifact_root/package/$source_manifest_path"
require_private_evidence_file "$source_manifest"
verify_sha256 "$source_manifest" "$source_manifest_relative" ||
  fail "package source-manifest digest is invalid"

gate_lines="$scratch/gates.jsonl"
: >"$gate_lines"
append_gate() {
  local gate_id="$1" scope="$2" acceptance="$3"
  local summary_file="$4" pointer_file="${5:-}"
  local pointer_json
  pointer_json="null"
  if [ -n "$pointer_file" ]; then
    pointer_json="$(artifact_ref "$pointer_file")"
  fi
  jq -nc \
    --arg id "$gate_id" \
    --arg scope "$scope" \
    --argjson candidateAcceptance "$acceptance" \
    --arg schema "$(jq -er '.schema' "$summary_file")" \
    --arg generatedAt "$(jq -er '.generatedAt' "$summary_file")" \
    --argjson evidence "$(artifact_ref "$summary_file")" \
    --argjson pointer "$pointer_json" '
      {
        id:$id,
        scope:$scope,
        schema:$schema,
        generatedAt:$generatedAt,
        result:"passed",
        candidateAcceptance:$candidateAcceptance,
        evidence:$evidence
      }
      + if $pointer == null then {} else {pointer:$pointer} end
    ' >>"$gate_lines"
}

append_gate formal source false "$formal_summary"
append_gate local source false "$local_summary"
append_gate dependencies source false "$dependency_summary"
append_gate package-components source false "$component_summary"
append_gate recovery source false "$recovery_summary"
append_gate privacy candidate true "$privacy_summary" "$privacy_pointer"
append_gate ui candidate true "$ui_summary" "$ui_pointer"
append_gate performance candidate true \
  "$performance_summary" "$performance_pointer"
append_gate lima candidate true "$lima_summary" "$lima_pointer"
append_gate package-build candidate true \
  "$package_summary" "$package_pointer"
append_gate package-lifecycle candidate true \
  "$lifecycle_summary" "$package_lifecycle_pointer"
jq -s . "$gate_lines" >"$scratch/gates.json"

local_install_status="pending"
publication_status="pending"
local_install_ref="null"
publication_ref="null"
local_install_receipt="$artifact_root/local-install/result.json"
publication_receipt="$artifact_root/publication-absence/result.json"

if [ -f "$local_install_receipt" ]; then
  require_private_evidence_file "$local_install_receipt"
  validate_closure_receipt \
    "$local_install_receipt" \
    "schemas/local-install-candidate.schema.json"
  jq -e \
    --arg commit "$source_commit" \
    --arg tree "$source_tree" \
    --arg version "$candidate_version" \
    --arg archiveSHA256 "$candidate_archive_sha" '
      .schema == "hideout.local-install-candidate/v1" and
      .result == "passed" and
      .candidateAcceptance == true and
      .sourceCandidate == {commit:$commit,tree:$tree,dirty:false} and
      .candidate.version == $version and
      .candidate.archiveSHA256 == $archiveSHA256 and
      all(.checks[]; . == true)
    ' "$local_install_receipt" >/dev/null ||
    fail "local-install receipt is stale, failed, or mismatched"
  validate_installed_candidate "$local_install_receipt"
  validate_fresh_json "$local_install_receipt"
  local_install_status="passed"
  local_install_ref="$(artifact_ref "$local_install_receipt")"
fi

if [ -f "$publication_receipt" ]; then
  require_private_evidence_file "$publication_receipt"
  validate_closure_receipt \
    "$publication_receipt" \
    "schemas/publication-absence.schema.json"
  jq -e \
    --arg commit "$source_commit" \
    --arg tree "$source_tree" \
    --arg archiveSHA256 "$candidate_archive_sha" '
      .schema == "hideout.publication-absence/v1" and
      .result == "passed" and
      .sourceCandidate == {commit:$commit,tree:$tree,dirty:false} and
      .candidateArchiveSHA256 == $archiveSHA256 and
      .observations.remoteTagCreated == false and
      .observations.githubReleaseCreated == false and
      .observations.homebrewChanged == false and
      .observations.packagePublished == false
    ' "$publication_receipt" >/dev/null ||
    fail "publication-absence receipt is stale, failed, or mismatched"
  validate_fresh_json "$publication_receipt"
  publication_status="passed"
  publication_ref="$(artifact_ref "$publication_receipt")"
fi

if [ "$require_closure" -eq 1 ] &&
  { [ "$local_install_status" != "passed" ] ||
    [ "$publication_status" != "passed" ]; }; then
  fail "--require-closure needs exact local-install and publication-absence receipts"
fi

stage="package-bound"
release_readiness=false
if [ "$local_install_status" = "passed" ]; then
  stage="installed-local"
fi
if [ "$local_install_status" = "passed" ] &&
  [ "$publication_status" = "passed" ]; then
  stage="final-ready"
  release_readiness=true
fi

jq -n \
  --arg localInstall "$local_install_status" \
  --arg publicationAbsence "$publication_status" \
  --argjson localInstallEvidence "$local_install_ref" \
  --argjson publicationEvidence "$publication_ref" '
    {
      localInstall:{
        status:$localInstall
      } + if $localInstallEvidence == null then {}
          else {evidence:$localInstallEvidence} end,
      publicationAbsence:{
        status:$publicationAbsence
      } + if $publicationEvidence == null then {}
          else {evidence:$publicationEvidence} end
    }
  ' >"$scratch/closure.json"

jq -s '
  [
    .[] |
    (
      .limitations[]?,
      .limitation?,
      .claimBoundary?,
      .packageBinaryBoundary?
    ) |
    select(type == "string" and length > 0)
  ] | unique
' \
  "$package_summary" \
  "$lifecycle_summary" \
  "$privacy_summary" \
  "$ui_summary" \
  "$performance_summary" \
  "$lima_summary" \
  "$dependency_summary" \
  "$component_summary" \
  "$recovery_summary" >"$scratch/input-limitations.json"
jq \
  --arg unsigned \
    "The candidate is local and unsigned; Developer ID signing and notarization are not claimed." \
  --arg coverage \
    "Workload observation is metadata with explicit coverage, not syscall-complete behavior proof or prevention." \
  --arg paths \
    "Authenticated local history shows local paths; reviewed export applies a separate redaction policy." \
  --arg guestRoot \
    "Guest-root containment is not claimed." \
  --arg publication \
    "Remote tag, GitHub Release, Homebrew mutation, and package publication require separate explicit authorization." '
    . + [$unsigned,$coverage,$paths,$guestRoot,$publication] | unique
  ' "$scratch/input-limitations.json" >"$scratch/limitations.json"

jq -n \
  --argjson manifest "$(jq '.runtime' "$package_root/package-manifest.json")" \
  --argjson catalog "$(package_ref "runtime/catalog.json")" \
  --argjson contract "$(package_ref "runtime/contract.json")" '
    $manifest + {catalog:$catalog,contract:$contract}
  ' >"$scratch/runtime.json"

jq -n \
  --argjson manifest "$(package_ref "runtime/browser-console.assets.json")" \
  --argjson container "$(package_ref "bin/hideout")" \
  --argjson inventory "$(cat "$browser_manifest")" '
    {
      manifest:$manifest,
      container:$container,
      inventory:$inventory
    }
  ' >"$scratch/browser.json"

jq -n \
  --argjson inventory "$(artifact_ref "$formal_inventory_evidence")" \
  --argjson inventorySource "$(artifact_ref "$formal_inventory_source")" \
  --argjson configurationCount \
    "$(jq '.inventory.configurationCount' "$formal_summary")" \
  --argjson moduleCount "$(jq '.inventory.moduleCount' "$formal_summary")" \
  --argjson invariantCount \
    "$(jq '.inventory.invariantCount' "$formal_summary")" \
  --argjson propertyCount \
    "$(jq '.inventory.propertyCount' "$formal_summary")" \
  --argjson goTestCount "$(jq '.inventory.goTestCount' "$formal_summary")" '
    {
      inventory:$inventory,
      sourceInventory:$inventorySource,
      configurationCount:$configurationCount,
      moduleCount:$moduleCount,
      invariantCount:$invariantCount,
      propertyCount:$propertyCount,
      goTestCount:$goTestCount
    }
  ' >"$scratch/formal.json"

output_tmp="$output_parent/.evidence.$$.json"
detached_output="$output.sha256"
detached_tmp="$output_parent/.evidence.$$.sha256"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg stage "$stage" \
  --argjson releaseReadiness "$release_readiness" \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg committedAt "$source_committed_at" \
  --argjson sourceManifest "$(artifact_ref "$source_manifest")" \
  --arg version "$candidate_version" \
  --arg tag "v$candidate_version" \
  --argjson archive "$(artifact_ref "$candidate_archive")" \
  --argjson packageManifest "$(artifact_ref "$package_manifest_evidence")" \
  --argjson packageSummary "$(artifact_ref "$package_summary")" \
  --argjson lifecycleSummary "$(artifact_ref "$lifecycle_summary")" \
  --argjson files "$(cat "$scratch/package-files.json")" \
  --argjson helpers "$(cat "$scratch/helpers.json")" \
  --argjson browserConsole "$(cat "$scratch/browser.json")" \
  --argjson runtime "$(cat "$scratch/runtime.json")" \
  --argjson formal "$(cat "$scratch/formal.json")" \
  --argjson gates "$(cat "$scratch/gates.json")" \
  --argjson reviewFindingCount "$review_finding_count" \
  --argjson review "$(artifact_ref "$review_file")" \
  --argjson claims "$(artifact_ref "$claim_matrix")" \
  --argjson limitations "$(cat "$scratch/limitations.json")" \
  --argjson closure "$(cat "$scratch/closure.json")" \
  --arg detachedPath "${detached_output#"$repo_root"/}" '
    {
      schema:"hideout.release-evidence/v1",
      generatedAt:$generatedAt,
      result:"passed",
      stage:$stage,
      releaseReadiness:$releaseReadiness,
      source:{
        commit:$commit,
        tree:$tree,
        dirty:false,
        committedAt:$committedAt,
        manifest:$sourceManifest
      },
      candidate:{
        version:$version,
        tag:$tag,
        channel:"developer-preview",
        signingMode:"developer-preview-unsigned",
        publicationStatus:"local-only",
        archive:$archive,
        packageManifest:$packageManifest,
        packageSummary:$packageSummary,
        lifecycleSummary:$lifecycleSummary
      },
      package:{
        files:$files,
        helpers:$helpers,
        browserConsole:$browserConsole,
        runtime:$runtime
      },
      formal:$formal,
      gates:$gates,
      review:{
        result:"passed",
        requiredFindings:$reviewFindingCount,
        openRequiredFindings:0,
        report:$review,
        claimMatrix:$claims
      },
      limitations:$limitations,
      closure:$closure,
      digest:{
        algorithm:"sha256",
        detachedPath:$detachedPath
      }
    }
  ' >"$output_tmp"
chmod 0600 "$output_tmp"

go run ./cmd/hideout-schema-validate \
  schemas/release-evidence.schema.json \
  "$output_tmp" >/dev/null

jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg version "$candidate_version" \
  --arg archiveSHA256 "$candidate_archive_sha" \
  --arg stage "$stage" \
  --argjson reviewFindingCount "$review_finding_count" \
  --argjson releaseReadiness "$release_readiness" '
    .schema == "hideout.release-evidence/v1" and
    .result == "passed" and
    .stage == $stage and
    .releaseReadiness == $releaseReadiness and
    .source.commit == $commit and
    .source.tree == $tree and
    .source.dirty == false and
    .candidate.version == $version and
    .candidate.archive.sha256 == $archiveSHA256 and
    .candidate.publicationStatus == "local-only" and
    (.package.files | length) >= 100 and
    ([.package.files[].path] | unique | length) ==
      (.package.files | length) and
    ([.package.helpers[] | select(.kind == "helper-manifest")] | length) == 6 and
    (.package.browserConsole.inventory.assets | length) == 8 and
    .formal.configurationCount == 12 and
    .formal.moduleCount == 10 and
    .formal.invariantCount == 76 and
    .formal.propertyCount == 19 and
    .formal.goTestCount == 12 and
    (.gates | length) == 11 and
    ([.gates[].id] | unique | length) == 11 and
    all(.gates[]; .result == "passed") and
    all(.gates[] | select(.scope == "candidate");
      .candidateAcceptance == true) and
    .review.requiredFindings == $reviewFindingCount and
    .review.openRequiredFindings == 0 and
    (.limitations | length) >= 5
  ' "$output_tmp" >/dev/null ||
  fail "final evidence manifest failed semantic validation"

if [ -n "$(git status --porcelain=v1 --untracked-files=all)" ]; then
  fail "source tree changed during evidence collection"
fi

evidence_sha="$(sha256_file "$output_tmp")"
printf '%s  %s\n' "$evidence_sha" "$(basename -- "$output")" \
  >"$detached_tmp"
chmod 0600 "$detached_tmp"
verify_sha256 "$output_tmp" "$evidence_sha" ||
  fail "detached evidence digest did not verify before publication"

mv "$output_tmp" "$output"
mv "$detached_tmp" "$detached_output"
chmod 0600 "$output" "$detached_output"
verify_sha256 "$output" "$evidence_sha" ||
  fail "written evidence manifest does not match detached digest"

gate_completed=1
printf \
  'collect-evidence: passed stage=%s readiness=%s manifest=%s sha256=%s\n' \
  "$stage" \
  "$release_readiness" \
  "$output" \
  "$evidence_sha"
