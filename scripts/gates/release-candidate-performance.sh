#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0

umask 077
evidence_out="$repo_root/.artifacts/045/performance"
preflight_only=0
attach_samples=30
attach_warmups=2
browser_samples=5
local_samples=30
local_warmups=5
process_samples=15
daemon_socket_path_max=100
host_contention_samples=3
host_contention_minimum_hits=2
host_generic_cpu_threshold=50
host_virtualization_cpu_threshold=5
host_build_cpu_threshold=10
host_contention_method="three-one-second-process-snapshots-two-hit-rejection"
host_measurement_contention_method="continuous-one-second-three-hit-classified-contention-rejection-generic-diagnostics"
host_measurement_contention_window=3
host_measurement_contention_minimum_hits=3
host_measurement_generic_policy="diagnostic-only"

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/release-candidate-performance.sh [--preflight] [--out DIR]" \
    "" \
    "Measures production query/render latency, daemon/TUI RSS, five real-" \
    "Chrome freshness samples, thirty real-Lima warm attaches, paired reference" \
    "overhead, observer CPU/RSS/event/drop rate, and real quota overshoot." \
    "Run only on a quiet host after pausing unrelated CPU-heavy tests, VMs," \
    "and emulators; known contention invalidates wall-clock evidence." \
    "A full run requires HIDEOUT_PERFORMANCE_QUIET_HOST_CONFIRMED=1 and" \
    "rejects sustained process/VM/build CPU contention before building and" \
    "throughout the real-Lima attach/reference measurement." \
    "It retains private preflight/measurement/start/boundary/end diagnostics." \
    "Evidence is private, immutable, digest-bound, and never published."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --out)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'release-candidate-performance: --out requires a directory\n' >&2
        exit 2
      }
      evidence_out="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf \
        'release-candidate-performance: unknown argument: %s\n' \
        "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf \
      'release-candidate-performance: missing required command: %s\n' \
      "$1" >&2
    exit 1
  }
}

validate_summary() {
  local summary_path="$1"
  local expected_tree_sha="$2"
  local expected_artifact_count="$3"

  jq -e \
    --arg treeSHA256 "$expected_tree_sha" \
    --argjson artifactCount "$expected_artifact_count" '
      . as $summary |
      .schema == "hideout.release-candidate-performance/v1" and
      .result == "passed" and
      .source.treeSHA256 == $treeSHA256 and
      .source.stableAcrossRun == true and
      (.source.dirty | type) == "boolean" and
      .candidateAcceptance == (.source.dirty | not) and
      .candidate.exactSourceTreeBound == true and
      .candidate.acceptance == .candidateAcceptance and
      .methodology.rawSamplesPresent == true and
      .methodology.percentilesIndependentlyRecomputed == true and
      .methodology.unitsStable == true and
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
        (.phase | type) == "string" and (.phase | length) > 0 and
        (.path | type) == "string" and (.path | length) > 0 and
        (.sha256 | test("^[a-f0-9]{64}$"))) and
      .validation.referenceMedianUpperConfidenceBoundWithinTenPercent == true and
      .validation.quietHostExplicitlyConfirmed == true and
      .validation.initialHostContentionAssessmentPassed == true and
      .validation.measurementHostContentionAssessmentPassed == true and
      .validation.hostDiagnosticsRetained == true and
      all(.validation[]; . == true) and
      (.claimReceipts | length) == 3 and
      all(.claimReceipts[]; .passed == true) and
      (.artifacts | length) == $artifactCount and
      ([.artifacts[].path] | unique | length) == $artifactCount and
      all(.artifacts[];
        (.path | type) == "string" and (.path | length) > 0 and
        (.sha256 | test("^[a-f0-9]{64}$")) and
        (.bytes | type) == "number" and
        .bytes >= 0 and
        .bytes == (.bytes | floor) and
        .mode == "0600") and
      all(.hostDiagnostics.snapshots[];
        . as $snapshot |
        any($summary.artifacts[];
          .path == $snapshot.path and .sha256 == $snapshot.sha256)) and
      (.hostDiagnostics.initialContentionAssessment as $assessment |
        any($summary.artifacts[];
          .path == $assessment.path and .sha256 == $assessment.sha256)) and
      (.hostDiagnostics.measurementContentionAssessment as $assessment |
        any($summary.artifacts[];
          .path == $assessment.path and .sha256 == $assessment.sha256))
    ' "$summary_path" >/dev/null
}

record_host_state() {
  local destination="$1" phase="$2"

  {
    printf 'schema=hideout.performance-host-state/v1\n'
    printf 'phase=%s\n' "$phase"
    printf 'captured_at='
    date -u +'%Y-%m-%dT%H:%M:%SZ'
    printf 'uname='
    uname -a
    printf 'logical_cpu='
    sysctl -n hw.logicalcpu
    printf 'load='
    uptime
    printf 'thermal_state_begin\n'
    pmset -g therm 2>&1 || true
    printf 'thermal_state_end\n'
    printf 'power_state_begin\n'
    pmset -g batt 2>&1 || true
    printf 'power_state_end\n'
    printf 'top_processes_begin\n'
    ps -Ao pid=,ppid=,pcpu=,pmem=,etime=,ucomm= -r | sed -n '1,25p'
    printf 'top_processes_end\n'
  } >"$destination"
  chmod 0600 "$destination"
}

record_initial_host_contention() {
  local destination="$1" sample

  {
    printf 'schema=hideout.performance-host-contention/v1\n'
    printf 'method=%s\n' "$host_contention_method"
    printf 'samples=%d\n' "$host_contention_samples"
    printf 'minimum_hits=%d\n' "$host_contention_minimum_hits"
    printf 'generic_cpu_percent_threshold=%d\n' \
      "$host_generic_cpu_threshold"
    printf 'virtualization_cpu_percent_threshold=%d\n' \
      "$host_virtualization_cpu_threshold"
    printf 'build_or_test_cpu_percent_threshold=%d\n' \
      "$host_build_cpu_threshold"
    sample=1
    while [ "$sample" -le "$host_contention_samples" ]; do
      printf 'sample_begin=%d\n' "$sample"
      LC_ALL=C ps -Ao pid=,ppid=,pcpu=,pmem=,ucomm= -r |
        sed -n '1,50p'
      printf 'sample_end=%d\n' "$sample"
      if [ "$sample" -lt "$host_contention_samples" ]; then
        sleep 1
      fi
      sample=$((sample + 1))
    done
  } >"$destination"
  chmod 0600 "$destination"
}

assess_initial_host_contention() {
  local source="$1"

  awk \
    -v expected_samples="$host_contention_samples" \
    -v minimum_hits="$host_contention_minimum_hits" \
    -v generic_threshold="$host_generic_cpu_threshold" \
    -v virtualization_threshold="$host_virtualization_cpu_threshold" \
    -v build_threshold="$host_build_cpu_threshold" \
    -v excluded_pid="$$" '
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
      /^sample_begin=[1-9][0-9]*$/ {
        split($0, fields, "=")
        current_sample = fields[2] + 0
        samples[current_sample] = 1
        next
      }
      /^sample_end=[1-9][0-9]*$/ {
        current_sample = 0
        next
      }
      current_sample > 0 &&
        $1 ~ /^[0-9]+$/ &&
        $2 ~ /^[0-9]+$/ &&
        $3 ~ /^[0-9]+([.][0-9]+)?$/ {
        pid = $1 + 0
        if (pid == excluded_pid) next
        cpu = $3 + 0
        name = $5
        threshold = generic_threshold
        reason = "generic-high-cpu"
        if (is_virtualization(name)) {
          threshold = virtualization_threshold
          reason = "active-virtualization"
        } else if (is_build_or_test(name)) {
          threshold = build_threshold
          reason = "active-build-or-test"
        }
        if (cpu >= threshold) {
          key = pid SUBSEP name SUBSEP reason
          sample_key = current_sample SUBSEP key
          if (!(sample_key in counted)) {
            counted[sample_key] = 1
            hits[key]++
          }
          if (cpu > maximum_cpu[key]) maximum_cpu[key] = cpu
        }
      }
      END {
        sample_count = 0
        for (sample in samples) sample_count++
        if (sample_count != expected_samples) {
          printf "invalid host contention sample inventory: expected=%d actual=%d\n", expected_samples, sample_count > "/dev/stderr"
          exit 2
        }
        offenders = 0
        for (key in hits) {
          if (hits[key] < minimum_hits) continue
          split(key, parts, SUBSEP)
          printf "pid=%s process=%s reason=%s hits=%d/%d max_cpu_percent=%.1f\n", parts[1], parts[2], parts[3], hits[key], expected_samples, maximum_cpu[key] > "/dev/stderr"
          offenders++
        }
        if (offenders > 0) exit 1
      }
    ' "$source"
}

measurement_lsof_paths_prove_gate_owned() {
  awk '
    /^n\/private\/tmp\/h34[.][^\/]+\// {found = 1; next}
    /^n/ && /\/hideout-034-gate2[.][^\/]+\/bin\/hideout$/ {
      found = 1
    }
    END {exit(found ? 0 : 1)}
  '
}

measurement_process_is_gate_owned() {
  local pid="$1"

  lsof -n -p "$pid" -Fn 2>/dev/null |
    measurement_lsof_paths_prove_gate_owned
}

isolated_process_group_exec() {
  exec python3 -c '
import os
import sys
os.setsid()
os.execvp(sys.argv[1], sys.argv[1:])
' "$@"
}

resolve_isolated_process_group() {
  local pid="$1" attempt pgid

  attempt=1
  while [ "$attempt" -le 50 ]; do
    pgid="$(ps -p "$pid" -o pgid= 2>/dev/null | tr -d ' ')"
    if [ "$pgid" = "$pid" ]; then
      printf '%s\n' "$pgid"
      return 0
    fi
    kill -0 "$pid" 2>/dev/null || return 1
    sleep 0.1
    attempt=$((attempt + 1))
  done
  return 1
}

watch_measurement_contention_signal() {
  local signal_file="$1" stop_file="$2" child_pid="$3" child_pgid="$4"
  local actual_pgid

  while [ ! -e "$stop_file" ] && kill -0 "$child_pid" 2>/dev/null; do
    if [ -s "$signal_file" ]; then
      actual_pgid="$(
        ps -p "$child_pid" -o pgid= 2>/dev/null | tr -d ' '
      )"
      [ "$actual_pgid" = "$child_pgid" ] &&
        [ "$child_pgid" = "$child_pid" ] || return 2
      kill -TERM -- "-$child_pgid" 2>/dev/null || return 3
      return 0
    fi
    sleep 0.1
  done
}

record_measurement_host_contention() {
  local destination="$1" stop_file="$2" sample_file="$3" gate_pgid="$4"
  local measurement_pgid="$5" signal_file="$6"
  local sample=0 owned_processes=" " monitor_status=0
  local pid ppid pgid cpu memory name process_key
  local assessment_status

  {
    printf 'schema=hideout.performance-host-contention/v4\n'
    printf 'method=%s\n' "$host_measurement_contention_method"
    printf 'rolling_window=%d\n' "$host_measurement_contention_window"
    printf 'minimum_hits=%d\n' \
      "$host_measurement_contention_minimum_hits"
    printf 'generic_high_cpu_policy=%s\n' \
      "$host_measurement_generic_policy"
    printf 'generic_cpu_percent_threshold=%d\n' \
      "$host_generic_cpu_threshold"
    printf 'virtualization_cpu_percent_threshold=%d\n' \
      "$host_virtualization_cpu_threshold"
    printf 'build_or_test_cpu_percent_threshold=%d\n' \
      "$host_build_cpu_threshold"
    printf 'gate_process_group=%s\n' "$gate_pgid"
    printf 'measurement_process_group=%s\n' "$measurement_pgid"
  } >"$destination"
  chmod 0600 "$destination"

  while [ ! -e "$stop_file" ]; do
    sample=$((sample + 1))
    LC_ALL=C ps -Ao pid=,ppid=,pgid=,pcpu=,pmem=,ucomm= -r |
      sed -n '1,50p' >"$sample_file"
    {
      printf 'sample_begin=%d\n' "$sample"
      while read -r pid ppid pgid cpu memory name; do
        case "$pid:$ppid:$pgid" in
          *[!0-9:]* | *::* | *:)
            continue
            ;;
        esac
        case "$cpu:$memory" in
          *[!0-9.:]* | *::* | *:)
            continue
            ;;
        esac
        [ -n "$name" ] || continue
        name="${name//[[:space:]]/_}"
        name="${name//:/_}"
        name="${name//=/_}"
        process_key="$pid:$name"
        case "$name" in
          hideout | limactl | qemu-system-* | com.apple.Virtua* | \
            VirtualBoxVM | vmware-vmx | prl_vm_app | UTM)
            case "$owned_processes" in
              *" $process_key "*) ;;
              *)
                if measurement_process_is_gate_owned "$pid"; then
                  owned_processes="$owned_processes$process_key "
                  printf 'owned_process=%s:gate-private-runtime\n' \
                    "$process_key"
                fi
                ;;
            esac
            ;;
        esac
        printf '%s %s %s %s %s %s\n' \
          "$pid" "$ppid" "$pgid" "$cpu" "$memory" "$name"
      done <"$sample_file"
      printf 'sample_end=%d\n' "$sample"
    } >>"$destination"
    if [ "$sample" -ge "$host_measurement_contention_window" ]; then
      assessment_status=0
      write_measurement_host_contention_signal \
        "$destination" "$signal_file" || assessment_status=$?
      if [ "$assessment_status" -ne 0 ]; then
        monitor_status="$assessment_status"
        if [ "$assessment_status" -eq 1 ]; then
          monitor_status=0
        fi
        break
      fi
    fi
    [ -e "$stop_file" ] || sleep 1
  done
  rm -f -- "$sample_file"
  return "$monitor_status"
}

assess_measurement_host_contention() {
  local source="$1"

  awk \
    -v expected_method="$host_measurement_contention_method" \
    -v expected_window="$host_measurement_contention_window" \
    -v minimum_hits="$host_measurement_contention_minimum_hits" \
    -v expected_generic_policy="$host_measurement_generic_policy" \
    -v generic_threshold="$host_generic_cpu_threshold" \
    -v virtualization_threshold="$host_virtualization_cpu_threshold" \
    -v build_threshold="$host_build_cpu_threshold" '
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
      /^method=/ {
        if (sampling_started) invalid = 1
        split($0, fields, "=")
        if (fields[2] == expected_method) method_count++
        else invalid = 1
        next
      }
      /^rolling_window=/ {
        if (sampling_started) invalid = 1
        split($0, fields, "=")
        if ((fields[2] + 0) == expected_window) window_count++
        else invalid = 1
        next
      }
      /^minimum_hits=/ {
        if (sampling_started) invalid = 1
        split($0, fields, "=")
        if ((fields[2] + 0) == minimum_hits) minimum_count++
        else invalid = 1
        next
      }
      /^generic_high_cpu_policy=/ {
        if (sampling_started) invalid = 1
        split($0, fields, "=")
        if (fields[2] == expected_generic_policy) generic_policy_count++
        else invalid = 1
        next
      }
      /^generic_cpu_percent_threshold=/ {
        if (sampling_started) invalid = 1
        split($0, fields, "=")
        if ((fields[2] + 0) == generic_threshold) generic_count++
        else invalid = 1
        next
      }
      /^virtualization_cpu_percent_threshold=/ {
        if (sampling_started) invalid = 1
        split($0, fields, "=")
        if ((fields[2] + 0) == virtualization_threshold)
          virtualization_count++
        else invalid = 1
        next
      }
      /^build_or_test_cpu_percent_threshold=/ {
        if (sampling_started) invalid = 1
        split($0, fields, "=")
        if ((fields[2] + 0) == build_threshold) build_count++
        else invalid = 1
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
        owned[owner[1] SUBSEP owner[2]] = 1
        owned_count[owner[1] SUBSEP owner[2]]++
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
        threshold = generic_threshold
        reason = "generic-high-cpu"
        if (is_virtualization(name)) {
          threshold = virtualization_threshold
          reason = "active-virtualization"
        } else if (is_build_or_test(name)) {
          threshold = build_threshold
          reason = "active-build-or-test"
        }
        if (cpu < threshold) next
        if (reason == "generic-high-cpu") next
        key = pid SUBSEP name SUBSEP reason
        sample_key = current_sample SUBSEP key
        if (sample_key in counted) next
        counted[sample_key] = 1
        hit_count[key]++
        hit_samples[key, hit_count[key]] = current_sample
        if (hit_count[key] >= minimum_hits && !(key in violations)) {
          first_hit_index = hit_count[key] - minimum_hits + 1
          first_hit_sample = hit_samples[key, first_hit_index]
          if (current_sample - first_hit_sample < expected_window) {
            violation_samples = first_hit_sample
            for (hit_index = first_hit_index + 1;
                 hit_index <= hit_count[key]; hit_index++) {
              violation_samples = violation_samples "," \
                hit_samples[key, hit_index]
            }
            violations[key] = violation_samples
          }
        }
        if (cpu > maximum_cpu[key]) maximum_cpu[key] = cpu
        next
      }
      {invalid = 1}
      END {
        if (current_sample != 0 || sample_count < expected_window ||
            schema_count != 1 || method_count != 1 || window_count != 1 ||
            minimum_count != 1 || generic_policy_count != 1 ||
            generic_count != 1 ||
            virtualization_count != 1 || build_count != 1 ||
            gate_group_count != 1 || measurement_group_count != 1 ||
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
        if (invalid) {
          printf "invalid continuous host contention evidence\n" > "/dev/stderr"
          exit 2
        }
        offenders = 0
        for (key in violations) {
          split(key, parts, SUBSEP)
          owner_key = parts[1] SUBSEP parts[2]
          if (owned[owner_key]) continue
          printf "pid=%s process=%s reason=%s samples=%s rolling_window=%d max_cpu_percent=%.1f\n", parts[1], parts[2], parts[3], violations[key], expected_window, maximum_cpu[key] > "/dev/stderr"
          offenders++
        }
        if (offenders > 0) exit 1
      }
    ' "$source"
}

write_measurement_host_contention_signal() {
  local source="$1" signal_file="$2" signal_tmp="${2}.tmp"
  local assessment_status=0 assessment_findings signal_kind="invalid"

  assessment_findings="$(
    assess_measurement_host_contention "$source" 2>&1
  )" || assessment_status=$?
  [ "$assessment_status" -ne 0 ] || return 0
  if [ "$assessment_status" -eq 1 ]; then
    signal_kind="contention"
  fi
  {
    printf 'schema=hideout.performance-host-contention-signal/v1\n'
    printf 'kind=%s\n' "$signal_kind"
    printf 'assessment_status=%d\n' "$assessment_status"
    printf '%s\n' "$assessment_findings"
  } >"$signal_tmp"
  chmod 0600 "$signal_tmp"
  mv -- "$signal_tmp" "$signal_file"
  return "$assessment_status"
}

process_store_socket_path() {
  printf '%s/daemon/hideoutd.sock' "$1"
}

process_store_path_is_safe() {
  local socket_path

  socket_path="$(process_store_socket_path "$1")"
  [ "${#socket_path}" -le "$daemon_socket_path_max" ]
}

for required_command in \
  awk bash cmp date find git go grep jq limactl lsof mv node perl ps python3 rg \
  sed shasum sleep sort ssh stat tail tr wc; do
  require_command "$required_command"
done

# HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_BEGIN final-evidence-preflight
# Performance is the most expensive candidate lane. Compile and execute the
# complete final-evidence builder/schema/semantic fixture before doing any host
# sampling or starting Lima so deterministic collector drift fails cheaply.
scripts/release/collect-evidence.sh --preflight >/dev/null
# HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_END final-evidence-preflight

expected_contract_claims='["C05","C06","CL03"]'
if ! jq -e \
  --argjson expected "$expected_contract_claims" '
    [.claims[] |
      select((.judges // []) | index("release-candidate-performance")) |
      .id] | sort == ($expected | sort)
  ' scripts/mutation/045/contracts.json >/dev/null; then
  printf \
    'release-candidate-performance: claim contract set drifted\n' \
    >&2
  exit 1
fi

if [ "$attach_samples" -lt 30 ] || [ "$attach_warmups" -lt 1 ]; then
  printf \
    'release-candidate-performance: real-Lima methodology requires at least 30 samples and one warmup\n' \
    >&2
  exit 1
fi

scratch_parent="$(CDPATH='' cd -- "${TMPDIR:-/tmp}" && pwd -P)"
short_scratch_parent="$(CDPATH='' cd -- /tmp && pwd -P)"
if [ "$preflight_only" -eq 1 ]; then
  preflight_root="$(mktemp -d "$scratch_parent/hideout-performance-preflight.XXXXXX")"
  preflight_process_scratch=""
  preflight_isolated_pid=""
  preflight_isolated_pgid=""
  preflight_isolated_watchdog_pid=""
  preflight_unrelated_pid=""
  preflight_live_monitor_pid=""
  preflight_live_monitor_stop=""
  preflight_live_measurement_pid=""
  preflight_live_measurement_pgid=""
  preflight_live_hog_pid=""
  preflight_live_hog_pgid=""
  # Invoked indirectly by the EXIT trap.
  # shellcheck disable=SC2329
  cleanup_preflight() {
    local exit_status=$?
    local cleanup_pgid
    if [ -n "${preflight_isolated_watchdog_pid:-}" ] &&
      kill -0 "$preflight_isolated_watchdog_pid" 2>/dev/null; then
      kill "$preflight_isolated_watchdog_pid" 2>/dev/null || true
      wait "$preflight_isolated_watchdog_pid" 2>/dev/null || true
    fi
    if [ -n "${preflight_isolated_pid:-}" ] &&
      kill -0 "$preflight_isolated_pid" 2>/dev/null; then
      cleanup_pgid="$(
        ps -p "$preflight_isolated_pid" -o pgid= 2>/dev/null | tr -d ' '
      )"
      if [ "$cleanup_pgid" = "$preflight_isolated_pid" ]; then
        kill -TERM -- "-$cleanup_pgid" 2>/dev/null || true
      else
        kill "$preflight_isolated_pid" 2>/dev/null || true
      fi
      wait "$preflight_isolated_pid" 2>/dev/null || true
    fi
    if [ -n "${preflight_unrelated_pid:-}" ] &&
      kill -0 "$preflight_unrelated_pid" 2>/dev/null; then
      kill "$preflight_unrelated_pid" 2>/dev/null || true
      wait "$preflight_unrelated_pid" 2>/dev/null || true
    fi
    if [ -n "${preflight_live_monitor_pid:-}" ]; then
      [ -z "${preflight_live_monitor_stop:-}" ] ||
        : >"$preflight_live_monitor_stop"
      wait "$preflight_live_monitor_pid" 2>/dev/null || true
    fi
    if [ -n "${preflight_live_measurement_pid:-}" ] &&
      kill -0 "$preflight_live_measurement_pid" 2>/dev/null; then
      cleanup_pgid="$(
        ps -p "$preflight_live_measurement_pid" -o pgid= 2>/dev/null |
          tr -d ' '
      )"
      if [ "$cleanup_pgid" = "$preflight_live_measurement_pid" ]; then
        kill -TERM -- "-$cleanup_pgid" 2>/dev/null || true
      else
        kill "$preflight_live_measurement_pid" 2>/dev/null || true
      fi
      wait "$preflight_live_measurement_pid" 2>/dev/null || true
    fi
    if [ -n "${preflight_live_hog_pid:-}" ] &&
      kill -0 "$preflight_live_hog_pid" 2>/dev/null; then
      cleanup_pgid="$(
        ps -p "$preflight_live_hog_pid" -o pgid= 2>/dev/null | tr -d ' '
      )"
      if [ "$cleanup_pgid" = "$preflight_live_hog_pid" ]; then
        kill -TERM -- "-$cleanup_pgid" 2>/dev/null || true
      else
        kill "$preflight_live_hog_pid" 2>/dev/null || true
      fi
      wait "$preflight_live_hog_pid" 2>/dev/null || true
    fi
    case "${preflight_process_scratch:-}" in
      "") ;;
      "$short_scratch_parent"/hp.*)
        [ ! -d "$preflight_process_scratch" ] ||
          find "$preflight_process_scratch" -depth -delete
        ;;
      *)
        printf \
          'release-candidate-performance: refusing unexpected short-path preflight cleanup\n' \
          >&2
        ;;
    esac
    case "${preflight_root:-}" in
      "$scratch_parent"/hideout-performance-preflight.*)
        [ ! -d "$preflight_root" ] ||
          find "$preflight_root" -depth -delete
        ;;
      *)
        printf \
          'release-candidate-performance: refusing unexpected preflight cleanup\n' \
          >&2
        ;;
    esac
    if [ "$exit_status" -eq 0 ]; then
      gate_require_completion "release-candidate-performance-preflight"
    fi
  }
  trap cleanup_preflight EXIT
  preflight_process_scratch="$(mktemp -d "$short_scratch_parent/hp.XXXXXX")"
  preflight_process_store="$preflight_process_scratch/store"
  preflight_process_socket="$(
    process_store_socket_path "$preflight_process_store"
  )"
  preflight_process_mode="$(
    stat -f '%Lp' "$preflight_process_scratch" 2>/dev/null ||
      stat -c '%a' "$preflight_process_scratch" 2>/dev/null
  )"
  if [ -L "$preflight_process_scratch" ] ||
    [ "$preflight_process_mode" != "700" ] ||
    ! process_store_path_is_safe "$preflight_process_store"; then
    printf \
      'release-candidate-performance: short private process store preflight failed: path=%s mode=%s socket-bytes=%d\n' \
      "$preflight_process_store" "$preflight_process_mode" \
      "${#preflight_process_socket}" >&2
    exit 1
  fi
  preflight_long_component="$(printf 'x%.0s' {1..101})"
  if process_store_path_is_safe \
    "$preflight_process_scratch/$preflight_long_component"; then
    printf \
      'release-candidate-performance: overlong process store negative fixture was accepted\n' \
      >&2
    exit 1
  fi
  summary_contract_fixture="$preflight_root/summary.json"
  summary_contract_negative="$preflight_root/summary-negative.json"
  summary_quiet_negative="$preflight_root/summary-quiet-negative.json"
  summary_contention_negative="$preflight_root/summary-contention-negative.json"
  summary_measurement_negative="$preflight_root/summary-measurement-negative.json"
  summary_validation_negative="$preflight_root/summary-validation-negative.json"
  summary_measurement_validation_negative="$preflight_root/summary-measurement-validation-negative.json"
  summary_duplicate_negative="$preflight_root/summary-duplicate-negative.json"
  summary_acceptance_negative="$preflight_root/summary-acceptance-negative.json"
  jq -n '
    {
      schema:"hideout.release-candidate-performance/v1",
      result:"passed",
      source:{treeSHA256:"preflight-tree",dirty:false,stableAcrossRun:true},
      candidateAcceptance:true,
      candidate:{exactSourceTreeBound:true,acceptance:true},
      methodology:{
        rawSamplesPresent:true,
        percentilesIndependentlyRecomputed:true,
        unitsStable:true
      },
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
      validation:{
        thresholds:true,
        referenceMedianUpperConfidenceBoundWithinTenPercent:true,
        quietHostExplicitlyConfirmed:true,
        initialHostContentionAssessmentPassed:true,
        measurementHostContentionAssessmentPassed:true,
        hostDiagnosticsRetained:true
      },
      claimReceipts:[
        {passed:true},
        {passed:true},
        {passed:true}
      ],
      artifacts:[
        {path:"fixture.txt",sha256:("0" * 64),bytes:0,mode:"0600"},
        {path:"host-state-start.txt",sha256:("1" * 64),bytes:0,mode:"0600"},
        {path:"host-state-before-real-lima.txt",sha256:("2" * 64),bytes:0,mode:"0600"},
        {path:"host-state-after-real-lima.txt",sha256:("3" * 64),bytes:0,mode:"0600"},
        {path:"host-contention-preflight.txt",sha256:("4" * 64),bytes:0,mode:"0600"},
        {path:"host-contention-measurement.txt",sha256:("5" * 64),bytes:0,mode:"0600"}
      ]
    }
  ' >"$summary_contract_fixture"
  validate_summary "$summary_contract_fixture" "preflight-tree" 6 || {
    printf \
      'release-candidate-performance: empty evidence contract regressed\n' \
      >&2
    exit 1
  }
  jq '.artifacts[0].bytes = -1' \
    "$summary_contract_fixture" >"$summary_contract_negative"
  if validate_summary "$summary_contract_negative" "preflight-tree" 6; then
    printf \
      'release-candidate-performance: negative evidence size was accepted\n' \
      >&2
    exit 1
  fi
  jq '.hostDiagnostics.quietHostConfirmed = false' \
    "$summary_contract_fixture" >"$summary_quiet_negative"
  if validate_summary "$summary_quiet_negative" "preflight-tree" 6; then
    printf \
      'release-candidate-performance: unconfirmed quiet host was accepted\n' \
      >&2
    exit 1
  fi
  jq '.hostDiagnostics.initialContentionAssessment.passed = false' \
    "$summary_contract_fixture" >"$summary_contention_negative"
  if validate_summary "$summary_contention_negative" "preflight-tree" 6; then
    printf \
      'release-candidate-performance: failed contention assessment was accepted\n' \
      >&2
    exit 1
  fi
  jq '.hostDiagnostics.measurementContentionAssessment.passed = false' \
    "$summary_contract_fixture" >"$summary_measurement_negative"
  if validate_summary "$summary_measurement_negative" "preflight-tree" 6; then
    printf \
      'release-candidate-performance: failed measurement contention assessment was accepted\n' \
      >&2
    exit 1
  fi
  jq 'del(.validation.initialHostContentionAssessmentPassed)' \
    "$summary_contract_fixture" >"$summary_validation_negative"
  if validate_summary "$summary_validation_negative" "preflight-tree" 6; then
    printf \
      'release-candidate-performance: missing validation field was accepted\n' \
      >&2
    exit 1
  fi
  jq 'del(.validation.measurementHostContentionAssessmentPassed)' \
    "$summary_contract_fixture" >"$summary_measurement_validation_negative"
  if validate_summary \
    "$summary_measurement_validation_negative" "preflight-tree" 6; then
    printf \
      'release-candidate-performance: missing measurement validation field was accepted\n' \
      >&2
    exit 1
  fi
  jq '.artifacts += [.artifacts[-1]]' \
    "$summary_contract_fixture" >"$summary_duplicate_negative"
  if validate_summary "$summary_duplicate_negative" "preflight-tree" 7; then
    printf \
      'release-candidate-performance: duplicate artifact path was accepted\n' \
      >&2
    exit 1
  fi
  jq '.candidateAcceptance = false' \
    "$summary_contract_fixture" >"$summary_acceptance_negative"
  if validate_summary "$summary_acceptance_negative" "preflight-tree" 6; then
    printf \
      'release-candidate-performance: mismatched candidate acceptance was accepted\n' \
      >&2
    exit 1
  fi
  if [ "$(uname -s)" = "Darwin" ]; then
    preflight_host_state="$preflight_root/host-state.txt"
    record_host_state "$preflight_host_state" "preflight"
    if [ "$(stat -f '%Lp' "$preflight_host_state")" != "600" ] ||
      ! grep -Fq 'schema=hideout.performance-host-state/v1' \
        "$preflight_host_state" ||
      ! grep -Fq 'phase=preflight' "$preflight_host_state" ||
      ! grep -Fq 'top_processes_begin' "$preflight_host_state" ||
      ! grep -Fq 'top_processes_end' "$preflight_host_state"; then
      printf \
        'release-candidate-performance: host diagnostics preflight failed\n' \
        >&2
      exit 1
    fi
  fi
  contention_quiet_fixture="$preflight_root/contention-quiet.txt"
  contention_busy_fixture="$preflight_root/contention-busy.txt"
  contention_busy_log="$preflight_root/contention-busy.log"
  contention_generic_fixture="$preflight_root/contention-generic.txt"
  contention_build_fixture="$preflight_root/contention-build.txt"
  contention_transient_fixture="$preflight_root/contention-transient.txt"
  {
    printf '%s\n' \
      'schema=hideout.performance-host-contention/v1' \
      'sample_begin=1' \
      '101 1 49.9 1.0 browser' \
      '102 1 4.9 1.0 qemu-system-aarc' \
      '103 1 9.9 1.0 go' \
      'sample_end=1' \
      'sample_begin=2' \
      '101 1 49.9 1.0 browser' \
      '102 1 4.9 1.0 qemu-system-aarc' \
      '103 1 9.9 1.0 go' \
      'sample_end=2' \
      'sample_begin=3' \
      '101 1 49.9 1.0 browser' \
      '102 1 4.9 1.0 qemu-system-aarc' \
      '103 1 9.9 1.0 go' \
      'sample_end=3'
  } >"$contention_quiet_fixture"
  assess_initial_host_contention "$contention_quiet_fixture" || {
    printf \
      'release-candidate-performance: quiet contention fixture was rejected\n' \
      >&2
    exit 1
  }
  sed 's/4[.]9 1[.]0 qemu-system-aarc/12.0 1.0 qemu-system-aarc/' \
    "$contention_quiet_fixture" >"$contention_busy_fixture"
  if assess_initial_host_contention "$contention_busy_fixture" \
    >"$contention_busy_log" 2>&1; then
    printf \
      'release-candidate-performance: sustained VM contention was accepted\n' \
      >&2
    exit 1
  fi
  if ! grep -Fq \
    'process=qemu-system-aarc reason=active-virtualization hits=3/3' \
    "$contention_busy_log"; then
    printf \
      'release-candidate-performance: contention rejection evidence regressed\n' \
      >&2
    exit 1
  fi
  sed 's/49[.]9 1[.]0 browser/55.0 1.0 browser/' \
    "$contention_quiet_fixture" >"$contention_generic_fixture"
  if assess_initial_host_contention "$contention_generic_fixture" \
    >"$contention_busy_log" 2>&1 ||
    ! grep -Fq \
      'process=browser reason=generic-high-cpu hits=3/3' \
      "$contention_busy_log"; then
    printf \
      'release-candidate-performance: generic CPU contention was not rejected\n' \
      >&2
    exit 1
  fi
  sed 's/9[.]9 1[.]0 go/15.0 1.0 go/' \
    "$contention_quiet_fixture" >"$contention_build_fixture"
  if assess_initial_host_contention "$contention_build_fixture" \
    >"$contention_busy_log" 2>&1 ||
    ! grep -Fq \
      'process=go reason=active-build-or-test hits=3/3' \
      "$contention_busy_log"; then
    printf \
      'release-candidate-performance: build CPU contention was not rejected\n' \
      >&2
    exit 1
  fi
  awk '
    /^sample_begin=/ {
      split($0, fields, "=")
      sample = fields[2] + 0
    }
    sample == 1 && $5 == "qemu-system-aarc" {$3 = "12.0"}
    {print}
  ' "$contention_quiet_fixture" >"$contention_transient_fixture"
  assess_initial_host_contention "$contention_transient_fixture" || {
    printf \
      'release-candidate-performance: one transient contention hit was rejected\n' \
      >&2
    exit 1
  }
  printf '%s\n' \
    'p201' \
    'n/private/tmp/h34.fixture/diffdisk' |
    measurement_lsof_paths_prove_gate_owned || {
    printf \
      'release-candidate-performance: private Lima ownership proof was rejected\n' \
      >&2
    exit 1
  }
  printf '%s\n' \
    'p202' \
    'n/private/var/folders/fixture/hideout-034-gate2.fixture/bin/hideout' |
    measurement_lsof_paths_prove_gate_owned || {
    printf \
      'release-candidate-performance: private Hideout ownership proof was rejected\n' \
      >&2
    exit 1
  }
  if printf '%s\n' \
    'p203' \
    'n/Users/operator/.lima/unrelated/diffdisk' |
    measurement_lsof_paths_prove_gate_owned; then
    printf \
      'release-candidate-performance: unrelated Lima ownership proof was accepted\n' \
      >&2
    exit 1
  fi
  measurement_ownership_probe="$preflight_root/hideout-034-gate2.fixture/bin/hideout"
  mkdir -p "$(dirname -- "$measurement_ownership_probe")"
  : >"$measurement_ownership_probe"
  exec 9<"$measurement_ownership_probe"
  measurement_process_is_gate_owned "$$" || {
    exec 9<&-
    printf \
      'release-candidate-performance: live lsof ownership proof was rejected\n' \
      >&2
    exit 1
  }
  exec 9<&-
  preflight_isolated_cleanup="$preflight_root/isolated-cleanup.txt"
  preflight_isolated_signal="$preflight_root/isolated-signal.txt"
  preflight_isolated_watchdog_stop="$preflight_root/isolated-watchdog.stop"
  sleep 30 &
  preflight_unrelated_pid=$!
  # The child expands its own positional cleanup-marker argument.
  # shellcheck disable=SC2016
  isolated_process_group_exec bash -c '
    cleanup_marker="$1"
    cleanup_probe() {
      printf "cleaned\n" >"$cleanup_marker"
    }
    terminate_probe() {
      exit 143
    }
    trap cleanup_probe EXIT
    trap terminate_probe TERM
    while :; do sleep 1; done
  ' contention-preflight-child "$preflight_isolated_cleanup" \
    >"$preflight_root/isolated-child.out" \
    2>"$preflight_root/isolated-child.err" &
  preflight_isolated_pid=$!
  if ! preflight_isolated_pgid="$(
    resolve_isolated_process_group "$preflight_isolated_pid"
  )"; then
    kill "$preflight_isolated_pid" 2>/dev/null || true
    wait "$preflight_isolated_pid" 2>/dev/null || true
    printf \
      'release-candidate-performance: isolated process group was not established\n' \
      >&2
    exit 1
  fi
  watch_measurement_contention_signal \
    "$preflight_isolated_signal" "$preflight_isolated_watchdog_stop" \
    "$preflight_isolated_pid" "$preflight_isolated_pgid" &
  preflight_isolated_watchdog_pid=$!
  printf '%s\n' \
    'schema=hideout.performance-host-contention-signal/v1' \
    'kind=contention' >"$preflight_isolated_signal"
  chmod 0600 "$preflight_isolated_signal"
  preflight_isolated_watchdog_status=0
  wait "$preflight_isolated_watchdog_pid" ||
    preflight_isolated_watchdog_status=$?
  if [ "$preflight_isolated_watchdog_status" -ne 0 ]; then
    kill -TERM -- "-$preflight_isolated_pgid" 2>/dev/null || true
  fi
  preflight_isolated_status=0
  wait "$preflight_isolated_pid" || preflight_isolated_status=$?
  : >"$preflight_isolated_watchdog_stop"
  if [ "$preflight_isolated_watchdog_status" -ne 0 ] ||
    [ "$preflight_isolated_status" -eq 0 ] ||
    ! kill -0 "$preflight_unrelated_pid" 2>/dev/null ||
    ! grep -Fxq 'cleaned' "$preflight_isolated_cleanup"; then
    printf \
      'release-candidate-performance: contention watchdog cleanup proof failed\n' \
      >&2
    exit 1
  fi
  kill "$preflight_unrelated_pid" 2>/dev/null || true
  wait "$preflight_unrelated_pid" 2>/dev/null || true
  preflight_unrelated_pid=""
  preflight_early_cleanup="$preflight_root/early-cleanup"
  mkdir -p \
    "$preflight_early_cleanup/out" \
    "$preflight_early_cleanup/tmp" \
    "$preflight_early_cleanup/short"
  preflight_early_cleanup_status=0
  # The child expands its own repository and output arguments.
  # shellcheck disable=SC2016
  TMPDIR="$preflight_early_cleanup/tmp" \
    HIDEOUT_LIMA_SHORT_TMPDIR="$preflight_early_cleanup/short" \
    bash -c '
      set -euo pipefail
      repo_root="$1"
      out="$2"
      . "$repo_root/scripts/lib/gate2-concurrent-sessions.sh"
      go() {
        return 86
      }
      gate2_concurrent_sessions_run "$repo_root" "$out" 30 2
    ' early-cleanup-child "$repo_root" "$preflight_early_cleanup/out" \
    >"$preflight_early_cleanup/stdout" \
    2>"$preflight_early_cleanup/stderr" ||
    preflight_early_cleanup_status=$?
  if [ "$preflight_early_cleanup_status" -ne 86 ] ||
    find \
      "$preflight_early_cleanup/tmp" \
      "$preflight_early_cleanup/short" \
      -mindepth 1 -maxdepth 1 -type d \
      \( -name 'hideout-034-gate2.*' -o -name 'h34-store.*' -o \
        -name 'h34.*' -o -name 'hideout-034-hostfs.*' \) \
      -print -quit | grep -q .; then
    printf \
      'release-candidate-performance: early Gate 2 failure leaked scratch\n' \
      >&2
    exit 1
  fi
  measurement_quiet_fixture="$preflight_root/measurement-quiet.txt"
  measurement_busy_fixture="$preflight_root/measurement-busy.txt"
  measurement_busy_log="$preflight_root/measurement-busy.log"
  measurement_generic_fixture="$preflight_root/measurement-generic.txt"
  measurement_transient_fixture="$preflight_root/measurement-transient.txt"
  measurement_external_build_fixture="$preflight_root/measurement-external-build.txt"
  measurement_unowned_vm_fixture="$preflight_root/measurement-unowned-vm.txt"
  measurement_invalid_owner_fixture="$preflight_root/measurement-invalid-owner.txt"
  measurement_invalid_group_fixture="$preflight_root/measurement-invalid-group.txt"
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
  } >"$measurement_quiet_fixture"
  assess_measurement_host_contention "$measurement_quiet_fixture" || {
    printf \
      'release-candidate-performance: quiet measurement contention fixture was rejected\n' \
      >&2
    exit 1
  }
  awk '
    /^sample_begin=/ {
      split($0, fields, "=")
      sample = fields[2] + 0
    }
    sample <= 3 && $6 == "Python" {$4 = "55.0"}
    {print}
  ' "$measurement_quiet_fixture" >"$measurement_busy_fixture"
  if assess_measurement_host_contention "$measurement_busy_fixture" \
    >"$measurement_busy_log" 2>&1 ||
    ! grep -Fq \
      'process=Python reason=active-build-or-test samples=1,2,3 rolling_window=3' \
      "$measurement_busy_log"; then
    printf \
      'release-candidate-performance: classified measurement contention was not rejected\n' \
      >&2
    exit 1
  fi
  awk '
    $6 == "browser" {$4 = "55.0"}
    {print}
  ' "$measurement_quiet_fixture" >"$measurement_generic_fixture"
  assess_measurement_host_contention "$measurement_generic_fixture" || {
    printf \
      'release-candidate-performance: diagnostic generic CPU was treated as blocking\n' \
      >&2
    exit 1
  }
  awk '
    /^sample_begin=/ {
      split($0, fields, "=")
      sample = fields[2] + 0
    }
    sample <= 2 && $6 == "Python" {$4 = "55.0"}
    {print}
  ' "$measurement_quiet_fixture" >"$measurement_transient_fixture"
  assess_measurement_host_contention "$measurement_transient_fixture" || {
    printf \
      'release-candidate-performance: two transient measurement hits were rejected\n' \
      >&2
    exit 1
  }
  sed 's/302 1 901 99[.]0 1[.]0 link/302 1 902 99.0 1.0 link/' \
    "$measurement_quiet_fixture" >"$measurement_external_build_fixture"
  if assess_measurement_host_contention "$measurement_external_build_fixture" \
    >"$measurement_busy_log" 2>&1 ||
    ! grep -Fq \
      'process=link reason=active-build-or-test samples=1,2,3 rolling_window=3' \
      "$measurement_busy_log"; then
    printf \
      'release-candidate-performance: external measurement build was not rejected\n' \
      >&2
    exit 1
  fi
  sed '/^owned_process=/d' \
    "$measurement_quiet_fixture" >"$measurement_unowned_vm_fixture"
  if assess_measurement_host_contention "$measurement_unowned_vm_fixture" \
    >"$measurement_busy_log" 2>&1 ||
    ! grep -Fq 'reason=active-virtualization samples=1,2,3' \
      "$measurement_busy_log"; then
    printf \
      'release-candidate-performance: unrelated measurement VM was not rejected\n' \
      >&2
    exit 1
  fi
  sed 's/owned_process=201:com[.]apple[.]Virtua/owned_process=101:browser/' \
    "$measurement_quiet_fixture" >"$measurement_invalid_owner_fixture"
  if assess_measurement_host_contention "$measurement_invalid_owner_fixture" \
    >/dev/null 2>&1; then
    printf \
      'release-candidate-performance: invalid measurement ownership proof was accepted\n' \
      >&2
    exit 1
  fi
  sed \
    's/measurement_process_group=901/measurement_process_group=900/' \
    "$measurement_quiet_fixture" >"$measurement_invalid_group_fixture"
  if assess_measurement_host_contention "$measurement_invalid_group_fixture" \
    >/dev/null 2>&1; then
    printf \
      'release-candidate-performance: non-isolated measurement group was accepted\n' \
      >&2
    exit 1
  fi
  measurement_quiet_signal_fixture="$preflight_root/measurement-quiet-signal.txt"
  if ! write_measurement_host_contention_signal \
    "$measurement_quiet_fixture" "$measurement_quiet_signal_fixture" ||
    [ -e "$measurement_quiet_signal_fixture" ]; then
    printf \
      'release-candidate-performance: quiet measurement emitted a signal\n' \
      >&2
    exit 1
  fi
  measurement_signal_fixture="$preflight_root/measurement-signal.txt"
  measurement_signal_status=0
  write_measurement_host_contention_signal \
    "$measurement_busy_fixture" "$measurement_signal_fixture" ||
    measurement_signal_status=$?
  if [ "$measurement_signal_status" -ne 1 ] ||
    ! grep -Fxq 'kind=contention' "$measurement_signal_fixture" ||
    ! grep -Fxq 'assessment_status=1' "$measurement_signal_fixture" ||
    ! grep -Fq 'process=Python reason=active-build-or-test' \
      "$measurement_signal_fixture"; then
    printf \
      'release-candidate-performance: online contention signal was not bound\n' \
      >&2
    exit 1
  fi
  measurement_invalid_signal_fixture="$preflight_root/measurement-invalid-signal.txt"
  measurement_signal_status=0
  write_measurement_host_contention_signal \
    "$measurement_invalid_owner_fixture" \
    "$measurement_invalid_signal_fixture" ||
    measurement_signal_status=$?
  if [ "$measurement_signal_status" -ne 2 ] ||
    ! grep -Fxq 'kind=invalid' "$measurement_invalid_signal_fixture" ||
    ! grep -Fxq 'assessment_status=2' \
      "$measurement_invalid_signal_fixture"; then
    printf \
      'release-candidate-performance: invalid monitor signal was not fail-closed\n' \
      >&2
    exit 1
  fi
  preflight_live_evidence="$preflight_root/live-measurement-contention.txt"
  preflight_live_monitor_stop="$preflight_root/live-measurement.stop"
  preflight_live_sample="$preflight_root/live-measurement.sample"
  preflight_live_signal="$preflight_root/live-measurement.signal"
  preflight_gate_pgid="$(ps -p $$ -o pgid= | tr -d ' ')"
  isolated_process_group_exec python3 -c '
while True:
    pass
' &
  preflight_live_measurement_pid=$!
  if ! preflight_live_measurement_pgid="$(
    resolve_isolated_process_group "$preflight_live_measurement_pid"
  )"; then
    kill "$preflight_live_measurement_pid" 2>/dev/null || true
    wait "$preflight_live_measurement_pid" 2>/dev/null || true
    preflight_live_measurement_pid=""
    printf \
      'release-candidate-performance: live measurement process was not isolated\n' \
      >&2
    exit 1
  fi
  isolated_process_group_exec python3 -c '
while True:
    pass
' &
  preflight_live_hog_pid=$!
  if ! preflight_live_hog_pgid="$(
    resolve_isolated_process_group "$preflight_live_hog_pid"
  )"; then
    kill "$preflight_live_hog_pid" 2>/dev/null || true
    wait "$preflight_live_hog_pid" 2>/dev/null || true
    preflight_live_hog_pid=""
    printf \
      'release-candidate-performance: live contention process was not isolated\n' \
      >&2
    exit 1
  fi
  sleep 0.2
  record_measurement_host_contention \
    "$preflight_live_evidence" \
    "$preflight_live_monitor_stop" \
    "$preflight_live_sample" \
    "$preflight_gate_pgid" \
    "$preflight_live_measurement_pgid" \
    "$preflight_live_signal" &
  preflight_live_monitor_pid=$!
  preflight_live_attempt=1
  while [ "$preflight_live_attempt" -le 100 ] &&
    [ ! -s "$preflight_live_signal" ] &&
    kill -0 "$preflight_live_monitor_pid" 2>/dev/null; do
    sleep 0.1
    preflight_live_attempt=$((preflight_live_attempt + 1))
  done
  : >"$preflight_live_monitor_stop"
  preflight_live_monitor_status=0
  wait "$preflight_live_monitor_pid" || preflight_live_monitor_status=$?
  preflight_live_monitor_pid=""
  kill -TERM -- "-$preflight_live_hog_pgid" 2>/dev/null || true
  wait "$preflight_live_hog_pid" 2>/dev/null || true
  kill -TERM -- "-$preflight_live_measurement_pgid" 2>/dev/null || true
  wait "$preflight_live_measurement_pid" 2>/dev/null || true
  measurement_signal_status=0
  assess_measurement_host_contention "$preflight_live_evidence" \
    >/dev/null 2>&1 || measurement_signal_status=$?
  if [ "$preflight_live_monitor_status" -ne 0 ] ||
    [ "$measurement_signal_status" -ne 1 ] ||
    ! grep -Fxq 'kind=contention' "$preflight_live_signal" ||
    ! grep -Fq "pid=$preflight_live_hog_pid" "$preflight_live_signal"; then
    printf \
      'release-candidate-performance: live Darwin contention monitor did not signal\n' \
      >&2
    exit 1
  fi
  preflight_live_hog_pid=""
  preflight_live_hog_pgid=""
  preflight_live_measurement_pid=""
  preflight_live_measurement_pgid=""
  reference_baseline="$preflight_root/reference-baseline.txt"
  reference_observed="$preflight_root/reference-observed.txt"
  reference_result="$preflight_root/reference-result.json"
  reference_coverage="$preflight_root/reference-coverage.jsonl"
  reference_resources="$preflight_root/reference-resources.jsonl"
  reference_resources_invalid="$preflight_root/reference-resources-invalid.jsonl"
  reference_coverage_loss="$preflight_root/reference-coverage-loss.jsonl"
  reference_coverage_inconsistent="$preflight_root/reference-coverage-inconsistent.jsonl"
  reference_raw_coverage="$preflight_root/reference-raw-coverage.json"
  reference_extracted_coverage="$preflight_root/reference-extracted-coverage.json"
  reference_failure_log="$preflight_root/reference-failure.log"
  confidence_baseline="$preflight_root/confidence-baseline.txt"
  confidence_observed="$preflight_root/confidence-observed.txt"
  confidence_coverage="$preflight_root/confidence-coverage.jsonl"
  confidence_resources="$preflight_root/confidence-resources.jsonl"
  confidence_result="$preflight_root/confidence-result.json"
  confidence_failure_log="$preflight_root/confidence-failure.log"
  printf '%s\n' 100 101 102 >"$reference_baseline"
  printf '%s\n' 105 106 107 >"$reference_observed"
  : >"$reference_coverage"
  : >"$reference_resources"
  for fixture_index in 1 2 3 4; do
    jq -cn \
      --argjson sampleIndex "$fixture_index" \
      --argjson recorded "$([ "$fixture_index" -gt 1 ] && printf true || printf false)" \
      '{sampleIndex:$sampleIndex,recorded:$recorded,
        sessionId:("ses_preflight_" + ($sampleIndex|tostring)),
        droppedEventCount:0,ringOverflow:false,
        kernelDropped:0,ringDropped:0,
        localDropped:{process:0,file:0,network:0,dns:0},
        fileCollectorCounters:{
          matchedEvents:(100 + $sampleIndex),
          reservedEvents:(100 + $sampleIndex),
          ringbufDrops:0,stateDrops:0,stateDegradations:6,
          pathFailures:0,identityFailures:1
        }}' \
      >>"$reference_coverage"
    jq -cn \
      --argjson sampleIndex "$fixture_index" \
      --argjson recorded "$([ "$fixture_index" -gt 1 ] && printf true || printf false)" '
      {
        sampleIndex:$sampleIndex,
        recorded:$recorded,
        baseline:{
          userMs:(80 + $sampleIndex),systemMs:10,
          voluntaryContextSwitches:2,
          involuntaryContextSwitches:(3 + $sampleIndex)
        },
        observed:{
          userMs:(84 + $sampleIndex),systemMs:12,
          voluntaryContextSwitches:3,
          involuntaryContextSwitches:(5 + $sampleIndex)
        }
      }' >>"$reference_resources"
  done
  # shellcheck source=scripts/lib/gate2-concurrent-performance.sh
  . "$repo_root/scripts/lib/gate2-concurrent-performance.sh"
  if [ "$(gate2_034_median_upper_confidence_rank 30)" != "20" ]; then
    printf \
      'release-candidate-performance: exact median confidence rank regressed\n' \
      >&2
    exit 1
  fi
  reference_runner_script="$(declare -f gate2_034_run_performance)"
  reference_workload_runner_script="$(declare -f gate2_034_run_reference_workload)"
  reference_baseline_script="$(declare -f gate2_034_measure_reference_baseline)"
  if ! awk '
    /gate2_034_run_reference_workload/ { measured = NR }
    /kill "\$candidate_anchor_pid"/ {
      if (measured > 0 && measured < NR) proved = 1
    }
    END { exit !proved }
  ' <<<"$reference_runner_script" ||
    ! grep -Fq 'signal.pause()' <<<"$reference_baseline_script" ||
    ! grep -Fq 'gate2_034_wait_active_session_count' \
      <<<"$reference_baseline_script" ||
    ! grep -Fq 'performance-reference-resources.jsonl' \
      <<<"$reference_workload_runner_script" ||
    ! grep -Fq 'involuntary_context_switches' \
      <<<"$reference_workload_runner_script"; then
    printf \
      'release-candidate-performance: reference observer symmetry regressed\n' \
      >&2
    exit 1
  fi
  jq -n '
    {
      intervals:[{
        id:"cov_preflight_extract",
        sessionId:"ses_preflight_extract",
        reason:"target-exited",
        droppedEventCount:0,
        evidence: (
          [{code:"target-exited"}] +
          ([
            ["kernel-dropped","0"],
            ["ring-dropped","0"],
            ["local-process-dropped","0"],
            ["local-file-dropped","0"],
            ["local-network-dropped","0"],
            ["local-dns-dropped","0"],
            ["file-matched-events","101"],
            ["file-reserved-events","101"],
            ["file-ringbuf-drops","0"],
            ["file-state-drops","0"],
            ["file-state-degradations","6"],
            ["file-path-failures","0"],
            ["file-identity-failures","1"]
          ] | map({code:.[0],value:.[1]}))
        )
      }],
      current:[]
    }
  ' >"$reference_raw_coverage"
  gate2_034_extract_reference_coverage_sample \
    "$reference_raw_coverage" 9 1 >"$reference_extracted_coverage"
  jq -e '
    .sampleIndex == 9 and .recorded == true and
    .sessionId == "ses_preflight_extract" and
    .droppedEventCount == 0 and .ringOverflow == false and
    .kernelDropped == 0 and .ringDropped == 0 and
    .fileCollectorCounters.matchedEvents == 101 and
    .fileCollectorCounters.reservedEvents == 101 and
    .fileCollectorCounters.stateDegradations == 6 and
    .fileCollectorCounters.identityFailures == 1
  ' "$reference_extracted_coverage" >/dev/null || {
    printf \
      'release-candidate-performance: coverage counter extraction regressed\n' \
      >&2
    exit 1
  }
  gate2_034_finalize_reference_result \
    "$reference_result" "$reference_baseline" "$reference_observed" \
    3 1 1000 "$(printf '0%.0s' {1..64})" \
    "$reference_coverage" "$reference_resources" \
    "$(printf '0%.0s' {1..64})" >/dev/null
  jq -e '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .elapsedOverhead.thresholdPassed == true and
    .elapsedOverhead.threshold == 10 and
    (.elapsedOverhead.samples | length) == 3 and
    .elapsedOverhead.median ==
      nr(.elapsedOverhead.samples; 50) and
    .methodology.samplePairing ==
      "index-aligned-adjacent-counterbalanced" and
    .methodology.overheadAggregation ==
      "nearest-rank-median-of-paired-percent-deltas" and
    .methodology.fixturePreparation ==
      "once-via-control-before-all-warmup-and-recorded-samples" and
    .methodology.pairProximity ==
      "adjacent-halves-reuse-one-immutable-warmed-source-with-no-drain-sleep" and
    .methodology.backgroundObserverPolicy ==
      "concurrent-anchor-plus-arm-equivalent-inert-baseline-session" and
    .observationIntegrity.noReportedLoss == true and
    (.observationIntegrity.fileBPFObjectSHA256 |
      test("^[a-f0-9]{64}$")) and
    (.observationIntegrity.coverageSamples | length) == 4 and
    (.observationIntegrity.fileCollectorCounters | length) == 4 and
    all(.observationIntegrity.coverageSamples[];
      .droppedEventCount == 0 and .ringOverflow == false and
      .kernelDropped == 0 and .ringDropped == 0 and
      .fileCollectorCounters.matchedEvents ==
        .fileCollectorCounters.reservedEvents) and
    all(.observationIntegrity.fileCollectorCounters[];
      .matchedEvents > 0 and
      .matchedEvents == (.reservedEvents + .ringbufDrops)) and
    .resourceUsage.scope == "reference-workload-child-process" and
    .resourceUsage.source == "getrusage(RUSAGE_CHILDREN)" and
    .resourceUsage.acceptanceFilter == false and
    (.resourceUsage.samples | length) == 4 and
    ([.resourceUsage.samples[] | select(.recorded)] | length) == 3 and
    all(.resourceUsage.samples[];
      .baseline.userMs >= 0 and .baseline.systemMs >= 0 and
      .baseline.voluntaryContextSwitches >= 0 and
      .baseline.involuntaryContextSwitches >= 0 and
      .observed.userMs >= 0 and .observed.systemMs >= 0 and
      .observed.voluntaryContextSwitches >= 0 and
      .observed.involuntaryContextSwitches >= 0) and
    (.baseline.samples | length) == 3 and
    (.observed.samples | length) == 3
  ' "$reference_result" >/dev/null || {
    printf \
      'release-candidate-performance: passing reference fixture was rejected\n' \
      >&2
    exit 1
  }
  jq -c '
    if .sampleIndex == 4 then .droppedEventCount = 1 else . end
  ' "$reference_coverage" >"$reference_coverage_loss"
  if gate2_034_finalize_reference_result \
    "$reference_result" "$reference_baseline" "$reference_observed" \
    3 1 1000 "$(printf '0%.0s' {1..64})" \
    "$reference_coverage_loss" "$reference_resources" \
    "$(printf '0%.0s' {1..64})" \
    >"$reference_failure_log" 2>&1; then
    printf \
      'release-candidate-performance: reported observer loss was accepted\n' \
      >&2
    exit 1
  fi
  jq -c '
    if .sampleIndex == 4 then
      .fileCollectorCounters.reservedEvents -= 1
    else . end
  ' "$reference_coverage" >"$reference_coverage_inconsistent"
  if gate2_034_finalize_reference_result \
    "$reference_result" "$reference_baseline" "$reference_observed" \
    3 1 1000 "$(printf '0%.0s' {1..64})" \
    "$reference_coverage_inconsistent" "$reference_resources" \
    "$(printf '0%.0s' {1..64})" \
    >"$reference_failure_log" 2>&1; then
    printf \
      'release-candidate-performance: inconsistent file counters were accepted\n' \
      >&2
    exit 1
  fi
  jq -c '
    if .sampleIndex == 4 then
      .observed.involuntaryContextSwitches = -1
    else . end
  ' "$reference_resources" >"$reference_resources_invalid"
  if gate2_034_finalize_reference_result \
    "$reference_result" "$reference_baseline" "$reference_observed" \
    3 1 1000 "$(printf '0%.0s' {1..64})" \
    "$reference_coverage" "$reference_resources_invalid" \
    "$(printf '0%.0s' {1..64})" \
    >"$reference_failure_log" 2>&1; then
    printf \
      'release-candidate-performance: invalid resource sample was accepted\n' \
      >&2
    exit 1
  fi
  printf '%s\n' 120 121 122 >"$reference_observed"
  if gate2_034_finalize_reference_result \
    "$reference_result" "$reference_baseline" "$reference_observed" \
    3 1 1000 "$(printf '0%.0s' {1..64})" \
    "$reference_coverage" "$reference_resources" \
    "$(printf '0%.0s' {1..64})" \
    >"$reference_failure_log" 2>&1; then
    printf \
      'release-candidate-performance: failing reference fixture was accepted\n' \
      >&2
    exit 1
  fi
  if ! jq -e '
      def nr($values; $percentile):
        ($values | sort) as $sorted |
        (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
        $sorted[$rank - 1];
      .elapsedOverhead.thresholdPassed == false and
      .elapsedOverhead.threshold == 10 and
      (.elapsedOverhead.samples | length) == 3 and
      .elapsedOverhead.median ==
        nr(.elapsedOverhead.samples; 50) and
      (.baseline.samples | length) == 3 and
      (.observed.samples | length) == 3
    ' "$reference_result" >/dev/null ||
    ! grep -Fq 'reference median overhead' "$reference_failure_log"; then
    printf \
      'release-candidate-performance: failing reference evidence was not retained\n' \
      >&2
    exit 1
  fi
  paired_baseline="$preflight_root/paired-baseline.txt"
  paired_observed="$preflight_root/paired-observed.txt"
  paired_result="$preflight_root/paired-result.json"
  paired_resources="$preflight_root/paired-resources.jsonl"
  printf '%s\n' 80 120 100 90 70 60 65 >"$paired_baseline"
  printf '%s\n' 90 87 95 97 76 71 92 >"$paired_observed"
  : >"$reference_coverage"
  : >"$paired_resources"
  for fixture_index in 1 2 3 4 5 6 7 8; do
    jq -cn \
      --argjson sampleIndex "$fixture_index" \
      --argjson recorded "$([ "$fixture_index" -gt 1 ] && printf true || printf false)" \
      '{sampleIndex:$sampleIndex,recorded:$recorded,
        sessionId:("ses_paired_" + ($sampleIndex|tostring)),
        droppedEventCount:0,ringOverflow:false,
        kernelDropped:0,ringDropped:0,
        localDropped:{process:0,file:0,network:0,dns:0},
        fileCollectorCounters:{
          matchedEvents:(100 + $sampleIndex),
          reservedEvents:(100 + $sampleIndex),
          ringbufDrops:0,stateDrops:0,stateDegradations:6,
          pathFailures:0,identityFailures:1
        }}' \
      >>"$reference_coverage"
    jq -cn \
      --argjson sampleIndex "$fixture_index" \
      --argjson recorded "$([ "$fixture_index" -gt 1 ] && printf true || printf false)" '
      {
        sampleIndex:$sampleIndex,
        recorded:$recorded,
        baseline:{
          userMs:(70 + $sampleIndex),systemMs:8,
          voluntaryContextSwitches:1,
          involuntaryContextSwitches:(2 + $sampleIndex)
        },
        observed:{
          userMs:(75 + $sampleIndex),systemMs:9,
          voluntaryContextSwitches:2,
          involuntaryContextSwitches:(4 + $sampleIndex)
        }
      }' >>"$paired_resources"
  done
  gate2_034_finalize_reference_result \
    "$paired_result" "$paired_baseline" "$paired_observed" \
    7 1 1000 "$(printf '0%.0s' {1..64})" \
    "$reference_coverage" "$paired_resources" \
    "$(printf '0%.0s' {1..64})" >/dev/null
  jq -e '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .elapsedOverhead.thresholdPassed == true and
    .elapsedOverhead.median ==
      nr(.elapsedOverhead.samples; 50) and
    (.resourceUsage.samples | length) == 8 and
    ([.resourceUsage.samples[] | select(.recorded)] | length) == 7 and
    (
      (
        (.observed.median - .baseline.median) /
        .baseline.median
      ) * 100
    ) > 10
  ' "$paired_result" >/dev/null || {
    printf \
      'release-candidate-performance: paired A/B aggregation regressed\n' \
      >&2
    exit 1
  }
  : >"$confidence_baseline"
  : >"$confidence_observed"
  : >"$confidence_coverage"
  : >"$confidence_resources"
  confidence_index=1
  while [ "$confidence_index" -le 31 ]; do
    confidence_recorded=false
    if [ "$confidence_index" -gt 1 ]; then
      confidence_recorded=true
      printf '100\n' >>"$confidence_baseline"
      printf '105\n' >>"$confidence_observed"
    fi
    jq -cn \
      --argjson sampleIndex "$confidence_index" \
      --argjson recorded "$confidence_recorded" '
      {
        sampleIndex:$sampleIndex,
        recorded:$recorded,
        sessionId:("ses_confidence_" + ($sampleIndex|tostring)),
        droppedEventCount:0,
        ringOverflow:false,
        kernelDropped:0,
        ringDropped:0,
        localDropped:{process:0,file:0,network:0,dns:0},
        fileCollectorCounters:{
          matchedEvents:(100 + $sampleIndex),
          reservedEvents:(100 + $sampleIndex),
          ringbufDrops:0,
          stateDrops:0,
          stateDegradations:0,
          pathFailures:0,
          identityFailures:0
        }
      }' >>"$confidence_coverage"
    jq -cn \
      --argjson sampleIndex "$confidence_index" \
      --argjson recorded "$confidence_recorded" '
      {
        sampleIndex:$sampleIndex,
        recorded:$recorded,
        baseline:{
          userMs:80,systemMs:10,
          voluntaryContextSwitches:2,
          involuntaryContextSwitches:3
        },
        observed:{
          userMs:84,systemMs:12,
          voluntaryContextSwitches:3,
          involuntaryContextSwitches:5
        }
      }' >>"$confidence_resources"
    confidence_index=$((confidence_index + 1))
  done
  gate2_034_finalize_reference_result \
    "$confidence_result" "$confidence_baseline" "$confidence_observed" \
    30 1 1000 "$(printf '0%.0s' {1..64})" \
    "$confidence_coverage" "$confidence_resources" \
    "$(printf '0%.0s' {1..64})" >/dev/null
  jq -e '
    .elapsedOverhead.median == 5 and
    .elapsedOverhead.confidence.level == 0.95 and
    .elapsedOverhead.confidence.method ==
      "one-sided-exact-binomial-order-statistic" and
    .elapsedOverhead.confidence.rank == 20 and
    .elapsedOverhead.confidence.upperBound == 5 and
    .elapsedOverhead.confidence.thresholdPassed == true and
    .elapsedOverhead.thresholdPassed == true
  ' "$confidence_result" >/dev/null || {
    printf \
      'release-candidate-performance: median confidence receipt regressed\n' \
      >&2
    exit 1
  }
  : >"$confidence_observed"
  confidence_index=1
  while [ "$confidence_index" -le 30 ]; do
    if [ "$confidence_index" -le 19 ]; then
      printf '105\n' >>"$confidence_observed"
    else
      printf '112\n' >>"$confidence_observed"
    fi
    confidence_index=$((confidence_index + 1))
  done
  if gate2_034_finalize_reference_result \
    "$confidence_result" "$confidence_baseline" "$confidence_observed" \
    30 1 1000 "$(printf '0%.0s' {1..64})" \
    "$confidence_coverage" "$confidence_resources" \
    "$(printf '0%.0s' {1..64})" \
    >"$confidence_failure_log" 2>&1; then
    printf \
      'release-candidate-performance: noisy median-only pass was accepted\n' \
      >&2
    exit 1
  fi
  if ! jq -e '
      .elapsedOverhead.median == 5 and
      .elapsedOverhead.confidence.rank == 20 and
      .elapsedOverhead.confidence.upperBound == 12 and
      .elapsedOverhead.confidence.thresholdPassed == false and
      .elapsedOverhead.thresholdPassed == false
    ' "$confidence_result" >/dev/null ||
    ! grep -Fq 'one-sided 95% median upper bound' \
      "$confidence_failure_log"; then
    printf \
      'release-candidate-performance: noisy confidence failure was not retained\n' \
      >&2
    exit 1
  fi
  reference_setup_script="$(gate2_034_reference_fixture_setup)"
  reference_measured_script="$(gate2_034_reference_workload)"
  if ! grep -Fq \
      'mktemp -d /var/tmp/hideout-reference.XXXXXX' \
      <<<"$reference_setup_script" ||
    grep -Eq \
      'mktemp|(^|[[:space:]])sleep([[:space:]]|$)' \
      <<<"$reference_measured_script" ||
    ! grep -Fq "work_root=\"\${1:-}\"" <<<"$reference_measured_script" ||
    ! grep -Fq 'resource.getrusage(resource.RUSAGE_CHILDREN)' \
      <<<"$reference_measured_script" ||
    ! grep -Fq 'user_ms=' <<<"$reference_measured_script" ||
    ! grep -Fq 'system_ms=' <<<"$reference_measured_script"; then
    printf \
      'release-candidate-performance: reference fixture/sample separation regressed\n' \
      >&2
    exit 1
  fi
  nested_errexit_marker="$preflight_root/nested-errexit-continued"
  set +e
  bash -c '
    set -e
    nested_failure() {
      false
      : >"$1"
    }
    nested_failure "$1"
  ' release-performance-preflight "$nested_errexit_marker" \
    >"$preflight_root/nested-errexit.log" 2>&1
  nested_errexit_status=$?
  set -e
  if [ "$nested_errexit_status" -eq 0 ] ||
    [ -e "$nested_errexit_marker" ]; then
    printf \
      'release-candidate-performance: nested child failure was not fail-closed\n' \
      >&2
    exit 1
  fi
  # HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_BEGIN preflight-order-assertion
  collector_preflight_line="$(
    grep -nF \
      'scripts/release/collect-evidence.sh --preflight >/dev/null' \
      scripts/gates/release-candidate-performance.sh |
      sed -n '1s/:.*//p'
  )"
  # The dollar expression is an intentional literal source-order sentinel.
  # shellcheck disable=SC2016
  host_sample_line="$(
    grep -nF \
      'record_initial_host_contention "$host_contention_evidence"' \
      scripts/gates/release-candidate-performance.sh |
      sed -n '1s/:.*//p'
  )"
  if ! [[ "$collector_preflight_line" =~ ^[0-9]+$ ]] ||
    ! [[ "$host_sample_line" =~ ^[0-9]+$ ]] ||
    [ "$collector_preflight_line" -ge "$host_sample_line" ]; then
    printf '%s\n' \
      'release-candidate-performance: final-evidence preflight is not ahead of host sampling' \
      >&2
    exit 1
  fi
  # HIDEOUT_INCREMENTAL_PERFORMANCE_IGNORE_END preflight-order-assertion
  bash -n \
    scripts/gates/release-candidate-performance.sh \
    scripts/gates/browser-console.sh \
    scripts/gates/workload-privacy-lima.sh \
    scripts/fixtures/workload-privacy.sh \
    scripts/lib/gate2-concurrent-performance.sh \
    scripts/lib/gate2-concurrent-sessions.sh
  go test -run '^$' \
    ./scripts/gates/performance-local \
    ./scripts/gates/performance-process \
    ./internal/tui/render \
    ./internal/workloadobs/query \
    ./internal/workloadobs/store >/dev/null
  scripts/gates/workload-privacy-lima.sh \
    --preflight --out "$preflight_root/privacy" >/dev/null
  gate_completed=1
  printf 'release-candidate-performance: preflight=passed\n'
  exit 0
fi

[ "$(uname -s)" = "Darwin" ] || {
  printf 'release-candidate-performance: full gate requires macOS\n' >&2
  exit 1
}
[ "$(uname -m)" = "arm64" ] || {
  printf 'release-candidate-performance: full gate requires arm64\n' >&2
  exit 1
}
for full_run_command in pmset sysctl uname uptime; do
  require_command "$full_run_command"
done
if [ "${HIDEOUT_PERFORMANCE_QUIET_HOST_CONFIRMED:-0}" != "1" ]; then
  printf '%s\n' \
    'release-candidate-performance: full gate requires explicit quiet-host confirmation' \
    'pause unrelated tests, VMs, and emulators, then set HIDEOUT_PERFORMANCE_QUIET_HOST_CONFIRMED=1' \
    >&2
  exit 1
fi

if [ -L "$evidence_out" ]; then
  printf \
    'release-candidate-performance: evidence directory must not be a symlink\n' \
    >&2
  exit 1
fi
mkdir -p "$evidence_out"
evidence_out="$(cd "$evidence_out" && pwd -P)"
chmod 0700 "$evidence_out"

source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi
run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$evidence_out/$run_id"
[ ! -e "$run_dir" ] || {
  printf \
    'release-candidate-performance: run directory already exists\n' \
    >&2
  exit 1
}
mkdir -p \
  "$run_dir/browser" \
  "$run_dir/lanes" \
  "$run_dir/tests"
chmod 0700 \
  "$run_dir" \
  "$run_dir/browser" \
  "$run_dir/lanes" \
  "$run_dir/tests"

host_contention_evidence="$run_dir/host-contention-preflight.txt"
record_initial_host_contention "$host_contention_evidence"
if ! contention_findings="$(
  assess_initial_host_contention "$host_contention_evidence" 2>&1
)"; then
  printf '%s\n' \
    'release-candidate-performance: sustained host contention detected; run is invalid before measurement' \
    "$contention_findings" \
    'pause the reported workload and retry; Hideout did not stop any process' \
    >&2
  exit 1
fi

scratch="$(mktemp -d "$scratch_parent/hideout-release-performance.XXXXXX")"
process_scratch=""
measurement_contention_pid=""
measurement_contention_watchdog_pid=""
concurrent_pid=""
concurrent_pgid=""
measurement_contention_stop="$scratch/host-contention-measurement.stop"
measurement_contention_sample="$scratch/host-contention-measurement.sample"
measurement_contention_signal="$scratch/host-contention-measurement.signal"
measurement_contention_watchdog_stop="$scratch/host-contention-measurement-watchdog.stop"
measurement_contention_evidence="$run_dir/host-contention-measurement.txt"
host_gate_pgid="$(ps -p $$ -o pgid= | tr -d ' ')"
case "$host_gate_pgid" in
  '' | *[!0-9]*)
    printf \
      'release-candidate-performance: could not resolve the gate process group\n' \
      >&2
    exit 1
    ;;
esac

stop_measurement_contention_monitor() {
  local monitor_status=0

  if [ -n "${measurement_contention_pid:-}" ]; then
    : >"$measurement_contention_stop"
    wait "$measurement_contention_pid" || monitor_status=$?
    measurement_contention_pid=""
  fi
  return "$monitor_status"
}

stop_measurement_contention_watchdog() {
  local watchdog_status=0

  if [ -n "${measurement_contention_watchdog_pid:-}" ]; then
    : >"$measurement_contention_watchdog_stop"
    wait "$measurement_contention_watchdog_pid" || watchdog_status=$?
    measurement_contention_watchdog_pid=""
  fi
  return "$watchdog_status"
}

stop_concurrent_measurement() {
  local actual_pgid="" stop_status=0

  if [ -n "${concurrent_pid:-}" ]; then
    if kill -0 "$concurrent_pid" 2>/dev/null; then
      actual_pgid="$(
        ps -p "$concurrent_pid" -o pgid= 2>/dev/null | tr -d ' '
      )"
      if [ "$actual_pgid" = "$concurrent_pgid" ] &&
        [ "$concurrent_pgid" = "$concurrent_pid" ]; then
        kill -TERM -- "-$concurrent_pgid" 2>/dev/null || true
      else
        stop_status=2
      fi
    fi
    if [ "$stop_status" -eq 0 ]; then
      wait "$concurrent_pid" 2>/dev/null || true
      concurrent_pid=""
      concurrent_pgid=""
    fi
  fi
  return "$stop_status"
}

cleanup() {
  local exit_status=$?
  stop_measurement_contention_watchdog >/dev/null 2>&1 || true
  stop_concurrent_measurement >/dev/null 2>&1 || true
  stop_measurement_contention_monitor >/dev/null 2>&1 || true
  case "${process_scratch:-}" in
    "") ;;
    "$short_scratch_parent"/hp.*)
      [ ! -d "$process_scratch" ] ||
        find "$process_scratch" -depth -delete
      ;;
    *)
      printf \
        'release-candidate-performance: refusing unexpected short-path scratch cleanup\n' \
        >&2
      ;;
  esac
  case "${scratch:-}" in
    "$scratch_parent"/hideout-release-performance.*)
      [ ! -d "$scratch" ] || find "$scratch" -depth -delete
      ;;
    *)
      printf \
        'release-candidate-performance: refusing unexpected scratch cleanup\n' \
        >&2
      ;;
  esac
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "release-candidate-performance"
  fi
}
trap cleanup EXIT

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

file_mode() {
  stat -f '%Lp' "$1" 2>/dev/null ||
    stat -c '%a' "$1" 2>/dev/null
}

file_bytes() {
  stat -f '%z' "$1" 2>/dev/null ||
    stat -c '%s' "$1" 2>/dev/null
}

safe_relative_path() {
  case "$1" in
    "" | /* | .. | ../* | */.. | */../*) return 1 ;;
    *) return 0 ;;
  esac
}

percentile_file() {
  local values="$1" percentile="$2" count index
  count="$(wc -l <"$values" | tr -d ' ')"
  [ "$count" -gt 0 ] || return 1
  index=$(((count * percentile + 99) / 100))
  sort -n "$values" | sed -n "${index}p"
}

values_json() {
  jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)' "$1"
}

write_source_manifest() {
  local destination="$1"
  local source_path mode bytes sha
  : >"$destination"
  git ls-files --cached --others --exclude-standard |
    LC_ALL=C sort |
    while IFS= read -r source_path; do
      case "$source_path" in
        .artifacts/* | .codegraph/* | hideout) continue ;;
      esac
      case "$source_path" in
        *"	"* | *"
"*)
          printf \
            'release-candidate-performance: unsupported source path: %q\n' \
            "$source_path" >&2
          return 1
          ;;
      esac
      [ -f "$source_path" ] && [ ! -L "$source_path" ] || {
        printf \
          'release-candidate-performance: source is not a regular file: %s\n' \
          "$source_path" >&2
        return 1
      }
      mode="$(file_mode "$source_path")"
      bytes="$(file_bytes "$source_path")"
      sha="$(sha256_file "$source_path")"
      printf '%s\t%s\t%s\t%s\n' \
        "$source_path" "$mode" "$bytes" "$sha"
    done >"$destination"
  [ -s "$destination" ]
}

source_manifest="$run_dir/source-manifest.tsv"
record_host_state "$run_dir/host-state-start.txt" "start"
write_source_manifest "$source_manifest"
source_tree_sha="$(sha256_file "$source_manifest")"
source_file_count="$(wc -l <"$source_manifest" | tr -d ' ')"

candidate_dir="$scratch/candidate"
candidate_bin="$candidate_dir/hideout"
mkdir -p "$candidate_dir"
printf 'release-candidate-performance: stage=candidate-build\n'
go build -trimpath -o "$candidate_bin" ./cmd/hideout
candidate_sha="$(sha256_file "$candidate_bin")"
jq -n \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg treeSHA256 "$source_tree_sha" \
  --arg binarySHA256 "$candidate_sha" \
  --arg goVersion "$(go version)" \
  '{
    schema:"hideout.release-performance-candidate/v1",
    source:{commit:$commit,dirty:$dirty,treeSHA256:$treeSHA256},
    binary:{sha256:$binarySHA256,buildMode:"go-build-trimpath"},
    toolchain:$goVersion
  }' >"$run_dir/candidate.json"

printf 'release-candidate-performance: stage=local-query-render\n'
go run ./scripts/gates/performance-local \
  --out "$run_dir/lanes/local-query-render.json" \
  --samples "$local_samples" \
  --warmups "$local_warmups" \
  >"$run_dir/lanes/local-query-render.log" 2>&1
jq -e \
  --argjson samples "$local_samples" '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .schema == "hideout.release-performance-local/v1" and
    .result == "passed" and
    .methodology.samples == $samples and
    .methodology.percentile == "nearest-rank-ceiling" and
    .query.unit == "milliseconds" and
    .render.unit == "milliseconds" and
    (.query.samples | length) == $samples and
    (.render.samples | length) == $samples and
    .query.p50 == nr(.query.samples; 50) and
    .query.p95 == nr(.query.samples; 95) and
    .render.p50 == nr(.render.samples; 50) and
    .render.p95 == nr(.render.samples; 95) and
    .query.thresholdPassed == true and
    .render.thresholdPassed == true
  ' "$run_dir/lanes/local-query-render.json" >/dev/null

printf 'release-candidate-performance: stage=daemon-tui-process\n'
process_scratch="$(mktemp -d "$short_scratch_parent/hp.XXXXXX")"
process_store="$process_scratch/store"
process_socket="$(process_store_socket_path "$process_store")"
process_scratch_mode="$(
  stat -f '%Lp' "$process_scratch" 2>/dev/null ||
    stat -c '%a' "$process_scratch" 2>/dev/null
)"
if [ -L "$process_scratch" ] ||
  [ "$process_scratch_mode" != "700" ] ||
  ! process_store_path_is_safe "$process_store"; then
  printf \
    'release-candidate-performance: private process store is unsafe: path=%s mode=%s socket-bytes=%d\n' \
    "$process_store" "$process_scratch_mode" "${#process_socket}" >&2
  exit 1
fi
set +e
go run ./scripts/gates/performance-process \
  --hideout "$candidate_bin" \
  --store "$process_store" \
  --out "$run_dir/lanes/daemon-tui-process.json" \
  --samples "$process_samples" \
  >"$run_dir/lanes/daemon-tui-process.log" 2>&1
process_status=$?
set -e
if [ "$process_status" -ne 0 ]; then
  process_failure="$(
    sed -n '$p' "$run_dir/lanes/daemon-tui-process.log"
  )"
  [ -n "$process_failure" ] ||
    process_failure="no terminal reason was recorded"
  printf \
    'release-candidate-performance: daemon/TUI process failed: %s (status=%d log=%s)\n' \
    "$process_failure" "$process_status" \
    "$run_dir/lanes/daemon-tui-process.log" >&2
  exit 1
fi
jq -e \
  --argjson samples "$process_samples" '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .schema == "hideout.release-performance-process/v1" and
    .result == "passed" and
    .methodology.samples == $samples and
    .methodology.percentile == "nearest-rank-ceiling" and
    .daemonRSS.unit == "bytes" and
    .tuiRSS.unit == "bytes" and
    .tuiReady.unit == "milliseconds" and
    (.daemonRSS.samples | length) == $samples and
    (.tuiRSS.samples | length) == $samples and
    .daemonRSS.p50 == nr(.daemonRSS.samples; 50) and
    .daemonRSS.p95 == nr(.daemonRSS.samples; 95) and
    .tuiRSS.p50 == nr(.tuiRSS.samples; 50) and
    .tuiRSS.p95 == nr(.tuiRSS.samples; 95) and
    .daemonRSS.thresholdPassed == true and
    .tuiRSS.thresholdPassed == true and
    .tuiReady.thresholdPassed == true
  ' "$run_dir/lanes/daemon-tui-process.json" >/dev/null

store_expected='[
  "TestActiveSegmentRepairsTornTailAndReportsCoverageGap",
  "TestActiveSegmentCRCFailureTruncatesAfterLastValidFrame",
  "TestCorruptSealedSegmentIsQuarantinedAndNeverReturned",
  "TestOwnerRetentionMaxAgePrunesExpiredSealedHistory",
  "TestQuotaPrunesOldestSealedAcrossOwnersAndBoundsOvershoot",
  "TestReusableOwnerQueriesRemainInsideSelectedSession"
]'
store_expected_path="$run_dir/tests/store-recovery.expected.json"
store_observed_path="$run_dir/tests/store-recovery.observed.json"
store_log="$run_dir/tests/store-recovery.go-test.jsonl"
jq -S . <<<"$store_expected" >"$store_expected_path"
store_regex="$(
  jq -r 'map("^" + . + "$") | join("|")' "$store_expected_path"
)"
printf 'release-candidate-performance: stage=quota-recovery-tests\n'
set +e
go test -json -count=1 -run "$store_regex" \
  ./internal/workloadobs/query \
  ./internal/workloadobs/store >"$store_log" 2>&1
store_test_exit=$?
set -e
jq -s \
  --slurpfile expected "$store_expected_path" '
    [.[] |
      select(.Action == "pass" and
        ((.Test // "") as $test |
          ($expected[0] | index($test)) != null)) |
      .Test] | unique | sort
  ' "$store_log" >"$store_observed_path"
if [ "$store_test_exit" -ne 0 ] ||
  ! jq -e -n \
    --slurpfile expected "$store_expected_path" \
    --slurpfile observed "$store_observed_path" '
      ($expected[0] | sort) == ($observed[0] | sort)
    ' >/dev/null; then
  tail -40 "$store_log" >&2
  printf \
    'release-candidate-performance: exact quota/recovery suite failed\n' \
    >&2
  exit 1
fi
jq -n \
  --argjson tests "$store_expected" \
  --arg log "tests/store-recovery.go-test.jsonl" \
  --arg logSHA256 "$(sha256_file "$store_log")" \
  '{
    schema:"hideout.release-performance-test-suite/v1",
    result:"passed",
    exactPassSet:true,
    tests:$tests,
    log:{path:$log,sha256:$logSHA256}
  }' >"$run_dir/tests/store-recovery.result.json"

browser_values="$run_dir/lanes/browser-live-update-ms.txt"
: >"$browser_values"
browser_index=1
while [ "$browser_index" -le "$browser_samples" ]; do
  browser_out="$run_dir/browser/sample-$browser_index"
  browser_log="$run_dir/lanes/browser-$browser_index.log"
  printf \
    'release-candidate-performance: stage=browser sample=%d/%d\n' \
    "$browser_index" "$browser_samples"
  scripts/gates/browser-console.sh \
    --out "$browser_out" >"$browser_log" 2>&1
  browser_summary="$browser_out/summary.json"
  jq -e \
    --arg commit "$source_commit" \
    --argjson dirty "$source_dirty" '
      .schema == "hideout.browser-console-gate/v1" and
      .source == {commit:$commit,dirty:$dirty} and
      .result == "passed" and
      .journeys.authenticatedSSE == "passed" and
      .observed.performance.liveUpdateMs > 0
    ' "$browser_summary" >/dev/null
  jq -r '.observed.performance.liveUpdateMs' \
    "$browser_summary" >>"$browser_values"
  browser_index=$((browser_index + 1))
done
browser_p50="$(percentile_file "$browser_values" 50)"
browser_p95="$(percentile_file "$browser_values" 95)"
awk -v value="$browser_p95" 'BEGIN {exit !(value <= 2000)}' || {
  printf \
    'release-candidate-performance: browser live-update p95 %sms exceeds 2000ms\n' \
    "$browser_p95" >&2
  exit 1
}
jq -n \
  --argjson samples "$(values_json "$browser_values")" \
  --argjson p50 "$browser_p50" \
  --argjson p95 "$browser_p95" \
  --argjson sampleCount "$browser_samples" \
  '{
    schema:"hideout.release-performance-browser/v1",
    result:"passed",
    methodology:{
      sampleCount:$sampleCount,
      journey:"independent-real-Chrome-SSE-visible-update",
      percentile:"nearest-rank-ceiling"
    },
    liveUpdate:{
      unit:"milliseconds",
      samples:$samples,
      p50:$p50,
      p95:$p95,
      thresholdP95:2000,
      thresholdPassed:($p95 <= 2000)
    }
  }' >"$run_dir/lanes/browser-performance.json"

concurrent_dir="$run_dir/lima-concurrent"
concurrent_log="$run_dir/lanes/lima-concurrent.log"
record_host_state \
  "$run_dir/host-state-before-real-lima.txt" "before-real-lima"
printf \
  'release-candidate-performance: stage=real-lima-attach-reference samples=%d warmups=%d\n' \
  "$attach_samples" "$attach_warmups"
set +e
# The isolated child receives and expands its own positional arguments.
# shellcheck disable=SC2016
isolated_process_group_exec bash -c '
  set -euo pipefail
  repo_root="$1"
  concurrent_dir="$2"
  attach_samples="$3"
  attach_warmups="$4"
  # shellcheck source=scripts/lib/gate2-concurrent-sessions.sh
  . "$repo_root/scripts/lib/gate2-concurrent-sessions.sh"
  # shellcheck source=scripts/lib/gate2-concurrent-performance.sh
  . "$repo_root/scripts/lib/gate2-concurrent-performance.sh"
  unset HIDEOUT_RELEASE_BINARY
  export HIDEOUT_034_EXTENDED_PERFORMANCE=1
  gate2_concurrent_sessions_run \
    "$repo_root" "$concurrent_dir" \
    "$attach_samples" "$attach_warmups"
' hideout-performance-child \
  "$repo_root" "$concurrent_dir" "$attach_samples" "$attach_warmups" \
  >"$concurrent_log" 2>&1 &
concurrent_pid=$!
if ! concurrent_pgid="$(resolve_isolated_process_group "$concurrent_pid")"; then
  kill "$concurrent_pid" 2>/dev/null || true
  wait "$concurrent_pid" 2>/dev/null || true
  concurrent_pid=""
  stop_measurement_contention_monitor >/dev/null 2>&1 || true
  set -e
  printf \
    'release-candidate-performance: real-Lima child process group was not isolated\n' \
    >&2
  exit 1
fi
record_measurement_host_contention \
  "$measurement_contention_evidence" \
  "$measurement_contention_stop" \
  "$measurement_contention_sample" \
  "$host_gate_pgid" \
  "$concurrent_pgid" \
  "$measurement_contention_signal" &
measurement_contention_pid=$!
watch_measurement_contention_signal \
  "$measurement_contention_signal" \
  "$measurement_contention_watchdog_stop" \
  "$concurrent_pid" "$concurrent_pgid" &
measurement_contention_watchdog_pid=$!
wait "$concurrent_pid" 2>/dev/null
concurrent_status=$?
concurrent_pid=""
concurrent_pgid=""
stop_measurement_contention_watchdog
measurement_contention_watchdog_status=$?
stop_measurement_contention_monitor
measurement_contention_status=$?
set -e
if [ "$measurement_contention_watchdog_status" -ne 0 ]; then
  printf \
    'release-candidate-performance: contention watchdog failed safely (status=%d)\n' \
    "$measurement_contention_watchdog_status" >&2
  exit 1
fi
if [ "$measurement_contention_status" -ne 0 ]; then
  printf '%s\n' \
    "release-candidate-performance: continuous host contention monitor failed (status=$measurement_contention_status evidence=$measurement_contention_evidence)" \
    "$(sed -n '1,8p' "$measurement_contention_signal" 2>/dev/null)" \
    >&2
  exit 1
fi
measurement_contention_samples="$(
  awk '/^sample_begin=/{count++} END {print count + 0}' \
    "$measurement_contention_evidence"
)"
set +e
measurement_contention_findings="$(
  assess_measurement_host_contention "$measurement_contention_evidence" 2>&1
)"
measurement_contention_assessment_status=$?
set -e
if [ "$measurement_contention_assessment_status" -eq 1 ]; then
  printf '%s\n' \
    'release-candidate-performance: sustained host contention detected during real-Lima measurement; run is invalid' \
    "$measurement_contention_findings" \
    'pause the reported workload and retry; Hideout did not stop any process' \
    >&2
  exit 1
fi
if [ "$concurrent_status" -ne 0 ]; then
  concurrent_failure="$(sed -n '$p' "$concurrent_log")"
  [ -n "$concurrent_failure" ] ||
    concurrent_failure="no terminal reason was recorded"
  printf \
    'release-candidate-performance: real-Lima attach/reference failed: %s (log=%s)\n' \
    "$concurrent_failure" "$concurrent_log" >&2
  exit 1
fi
if [ "$measurement_contention_assessment_status" -ne 0 ]; then
  printf '%s\n' \
    'release-candidate-performance: continuous host contention evidence is invalid' \
    "$measurement_contention_findings" >&2
  exit 1
fi
concurrent_result="$concurrent_dir/result.json"
concurrent_performance="$concurrent_dir/logs/performance.json"
jq -e \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --argjson samples "$attach_samples" \
  --argjson warmups "$attach_warmups" '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .schema == "hideout.concurrent-sessions-performance/v4" and
    .status == "passed" and
    .candidate.commit == $commit and
    .candidate.dirty == $dirty and
    .methodology.samples == $samples and
    .methodology.warmups == $warmups and
    .methodology.hostContentionPolicy ==
      "operator-confirmed-quiet-host-known-contention-invalidates-run" and
    .methodology.hostQuietConfirmed == true and
    (.warmAttach.samplesMs | length) == $samples and
    .warmAttach.medianMs == nr(.warmAttach.samplesMs; 50) and
    .warmAttach.p95Ms == nr(.warmAttach.samplesMs; 95) and
    .warmAttach.p95Ms <= .methodology.readyThresholdMs and
    (.referenceWorkload.baseline.samples | length) == $samples and
    (.referenceWorkload.observed.samples | length) == $samples and
    .referenceWorkload.baseline.median ==
      nr(.referenceWorkload.baseline.samples; 50) and
    .referenceWorkload.baseline.p95 ==
      nr(.referenceWorkload.baseline.samples; 95) and
    .referenceWorkload.observed.median ==
      nr(.referenceWorkload.observed.samples; 50) and
    .referenceWorkload.observed.p95 ==
      nr(.referenceWorkload.observed.samples; 95) and
    .referenceWorkload.methodology.samplePairing ==
      "index-aligned-adjacent-counterbalanced" and
    .referenceWorkload.methodology.overheadAggregation ==
      "nearest-rank-median-of-paired-percent-deltas" and
    .referenceWorkload.methodology.fixturePreparation ==
      "once-via-control-before-all-warmup-and-recorded-samples" and
    .referenceWorkload.methodology.pairProximity ==
      "adjacent-halves-reuse-one-immutable-warmed-source-with-no-drain-sleep" and
    .referenceWorkload.methodology.backgroundObserverPolicy ==
      "concurrent-anchor-plus-arm-equivalent-inert-baseline-session" and
    .referenceWorkload.observationIntegrity.noReportedLoss == true and
    (.referenceWorkload.observationIntegrity.fileBPFObjectSHA256 |
      test("^[a-f0-9]{64}$")) and
    (.referenceWorkload.observationIntegrity.coverageSamples | length) ==
      ($samples + $warmups) and
    ([.referenceWorkload.observationIntegrity.coverageSamples[] |
      select(.recorded)] | length) == $samples and
    (.referenceWorkload.observationIntegrity.fileCollectorCounters | length) ==
      ($samples + $warmups) and
    all(.referenceWorkload.observationIntegrity.coverageSamples[];
      .droppedEventCount == 0 and .ringOverflow == false and
      .kernelDropped == 0 and .ringDropped == 0 and
      .localDropped.process == 0 and .localDropped.file == 0 and
      .localDropped.network == 0 and .localDropped.dns == 0 and
      .fileCollectorCounters.matchedEvents > 0 and
      .fileCollectorCounters.matchedEvents ==
        (.fileCollectorCounters.reservedEvents +
          .fileCollectorCounters.ringbufDrops) and
      .fileCollectorCounters.ringbufDrops == 0 and
      .fileCollectorCounters.stateDrops == 0) and
    .referenceWorkload.resourceUsage.scope ==
      "reference-workload-child-process" and
    .referenceWorkload.resourceUsage.source ==
      "getrusage(RUSAGE_CHILDREN)" and
    .referenceWorkload.resourceUsage.acceptanceFilter == false and
    (.referenceWorkload.resourceUsage.samples | length) ==
      ($samples + $warmups) and
    ([.referenceWorkload.resourceUsage.samples[] |
      select(.recorded)] | length) == $samples and
    all(.referenceWorkload.resourceUsage.samples[];
      .baseline.userMs >= 0 and .baseline.systemMs >= 0 and
      .baseline.voluntaryContextSwitches >= 0 and
      .baseline.involuntaryContextSwitches >= 0 and
      .observed.userMs >= 0 and .observed.systemMs >= 0 and
      .observed.voluntaryContextSwitches >= 0 and
      .observed.involuntaryContextSwitches >= 0) and
    .referenceWorkload.elapsedOverhead.unit == "percent" and
    (.referenceWorkload.elapsedOverhead.samples | length) == $samples and
    .referenceWorkload.elapsedOverhead.samples == [
      range(0; $samples) as $index |
      (
        (
          (
            (
              .referenceWorkload.observed.samples[$index] -
              .referenceWorkload.baseline.samples[$index]
            ) /
            .referenceWorkload.baseline.samples[$index]
          ) * 100000
        ) | round
      ) / 1000
    ] and
    .referenceWorkload.elapsedOverhead.median ==
      nr(.referenceWorkload.elapsedOverhead.samples; 50) and
    .referenceWorkload.elapsedOverhead.threshold == 10 and
    .referenceWorkload.elapsedOverhead.confidence.level == 0.95 and
    .referenceWorkload.elapsedOverhead.confidence.method ==
      "one-sided-exact-binomial-order-statistic" and
    .referenceWorkload.elapsedOverhead.confidence.rank == 20 and
    .referenceWorkload.elapsedOverhead.confidence.upperBound ==
      ((.referenceWorkload.elapsedOverhead.samples | sort)[19]) and
    .referenceWorkload.elapsedOverhead.confidence.thresholdPassed == true and
    .referenceWorkload.elapsedOverhead.confidence.upperBound <= 10 and
    .referenceWorkload.elapsedOverhead.thresholdPassed == true
  ' "$concurrent_performance" >/dev/null
jq -e \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" '
    .schema == "hideout.concurrent-sessions-gate2/v1" and
    .status == "passed" and
    .commit == $commit and
    .dirty == $dirty and
    .candidateAcceptance == ($dirty | not)
  ' "$concurrent_result" >/dev/null

privacy_dir="$run_dir/lima-privacy"
privacy_log="$run_dir/lanes/lima-privacy.log"
printf \
  'release-candidate-performance: stage=real-lima-observer-quota\n'
set +e
HIDEOUT_WORKLOAD_PRIVACY_MEASURE_PERFORMANCE=1 \
HIDEOUT_WORKLOAD_PRIVACY_EVENTS_PER_ROUND=7000 \
HIDEOUT_WORKLOAD_PRIVACY_MAXIMUM_ROUNDS=3 \
  scripts/gates/workload-privacy-lima.sh \
    --require-real --out "$privacy_dir" \
    >"$privacy_log" 2>&1
privacy_status=$?
set -e
if [ "$privacy_status" -ne 0 ]; then
  privacy_failure="$(sed -n '$p' "$privacy_log")"
  [ -n "$privacy_failure" ] ||
    privacy_failure="no terminal reason was recorded"
  printf \
    'release-candidate-performance: real-Lima observer/quota failed: %s (status=%d log=%s)\n' \
    "$privacy_failure" "$privacy_status" "$privacy_log" >&2
  exit 1
fi
privacy_result="$privacy_dir/result.json"
privacy_summary="$privacy_dir/reports/privacy-summary.json"
jq -e \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" '
    .schema == "hideout.workload-privacy-lima-evidence/v1" and
    .source == {commit:$commit,dirty:$dirty} and
    .result == "passed" and
    .candidateAcceptance == ($dirty | not) and
    (.artifacts | length) == 10 and
    all(.checks[]; . == "passed")
  ' "$privacy_result" >/dev/null
jq -e '
    def nr($values; $percentile):
      ($values | sort) as $sorted |
      (($sorted | length) * $percentile + 99) / 100 | floor as $rank |
      $sorted[$rank - 1];
    .schema == "hideout.workload-privacy-lima-summary/v1" and
    .quota.passed == true and
    .quota.pruned == true and
    .quota.retentionGap == true and
    .quota.activeSegmentAllowanceCount == 1 and
    .quota.activeSegmentAllowanceBytes == 8388608 and
    .quota.usedBytes <=
      (.quota.limitBytes + .quota.activeSegmentAllowanceBytes) and
    .performance.measured == true and
    .performance.methodology.percentile == "nearest-rank-ceiling" and
    .performance.observerCPU.unit == "percent-of-one-guest-vcpu" and
    .performance.observerRSS.unit == "bytes" and
    (.performance.observerCPU.samples | length) >= 5 and
    (.performance.observerRSS.samples | length) ==
      (.performance.observerCPU.samples | length) and
    .performance.observerCPU.p50 ==
      nr(.performance.observerCPU.samples; 50) and
    .performance.observerCPU.p95 ==
      nr(.performance.observerCPU.samples; 95) and
    .performance.observerRSS.p50 ==
      nr(.performance.observerRSS.samples; 50) and
    .performance.observerRSS.p95 ==
      nr(.performance.observerRSS.samples; 95) and
    .performance.observerCPU.p95 <= 200 and
    .performance.observerRSS.p95 <= 268435456 and
    .performance.eventRate.unit == "generated-execs-per-second" and
    .performance.eventRate.generatedEvents >= 7000 and
    .performance.eventRate.value >= 100 and
    .performance.healthyDropRate.unit == "percent" and
    .performance.healthyDropRate.coverageAccounted == true and
    .performance.healthyDropRate.value <= 1
  ' "$privacy_summary" >/dev/null
record_host_state \
  "$run_dir/host-state-after-real-lima.txt" "after-real-lima"

receipts="$run_dir/claim-receipts.json"
jq -n '[
  {
    claim:"C05",
    passed:true,
    requirements:{
      queryOwnerStable:true,
      pruneOrder:"oldest-sealed",
      foreignOwnerPruned:false,
      historyCompleteAfterPrune:false,
      quotaOvershootActiveSegments:1
    },
    evidence:[
      "tests/store-recovery.result.json",
      "lima-privacy/reports/privacy-summary.json"
    ]
  },
  {
    claim:"C06",
    passed:true,
    requirements:{
      rawSamplesPresent:true,
      percentilesRecomputed:true,
      unitsStable:true,
      thresholdsPassed:true,
      exactCandidateBound:true
    },
    evidence:[
      "source-manifest.tsv",
      "lanes/local-query-render.json",
      "lanes/daemon-tui-process.json",
      "lanes/browser-performance.json",
      "lima-concurrent/logs/performance.json",
      "lima-privacy/reports/privacy-summary.json"
    ]
  },
  {
    claim:"CL03",
    passed:true,
    requirements:{
      corruptFramesReturned:0,
      pruneOrder:"oldest-sealed",
      coverageGapVisible:true,
      quarantinePresent:true
    },
    evidence:[
      "tests/store-recovery.result.json",
      "lima-privacy/reports/privacy-summary.json"
    ]
  }
]' >"$receipts"
if ! jq -e -n \
  --slurpfile contracts scripts/mutation/045/contracts.json \
  --slurpfile receipts "$receipts" \
  --argjson expected "$expected_contract_claims" '
    ($contracts[0].claims |
      map(select((.judges // []) |
        index("release-candidate-performance")))) as $claims |
    ($receipts[0]) as $actual |
    ($claims | map(.id) | sort) == ($expected | sort) and
    ($actual | map(.claim) | sort) == ($expected | sort) and
    all($claims[]; . as $claim |
      ($actual[] | select(.claim == $claim.id)) as $receipt |
      $receipt.passed == true and
      all($claim.requirements[];
        $receipt.requirements[.id] == .expected))
  ' >/dev/null; then
  printf \
    'release-candidate-performance: claim receipt contract mismatch\n' \
    >&2
  exit 1
fi

source_manifest_after="$scratch/source-manifest-after.tsv"
write_source_manifest "$source_manifest_after"
if ! cmp -s "$source_manifest" "$source_manifest_after"; then
  printf \
    'release-candidate-performance: source tree changed during measurement\n' \
    >&2
  exit 1
fi

find "$run_dir" -type d -exec chmod 0700 {} +
find "$run_dir" -type f -exec chmod 0600 {} +
if find "$run_dir" -type l -print -quit | grep -q .; then
  printf \
    'release-candidate-performance: evidence contains a symlink\n' \
    >&2
  exit 1
fi
if find "$run_dir" -type f ! -name '*.png' -print0 |
  xargs -0 rg -a -n \
    'ui_[0-9a-fA-F]{48}|cap_[0-9a-fA-F]{32,}|HIDEOUT_SECRET_[A-Za-z0-9_]+=[^[:space:]]+|((https?|socks5h?)://[^/@[:space:]:]+:[^/@[:space:]]+@)|Authorization:[[:space:]]*Bearer[[:space:]]+[A-Za-z0-9._~-]{8,}' \
    >/dev/null 2>&1; then
  printf \
    'release-candidate-performance: private material reached retained evidence\n' \
    >&2
  exit 1
fi

artifact_lines="$scratch/artifacts.jsonl"
: >"$artifact_lines"
while IFS= read -r evidence_file; do
  relative_path="${evidence_file#"$run_dir"/}"
  safe_relative_path "$relative_path" || {
    printf \
      'release-candidate-performance: unsafe evidence path: %s\n' \
      "$relative_path" >&2
    exit 1
  }
  [ "$(file_mode "$evidence_file")" = "600" ] || {
    printf \
      'release-candidate-performance: evidence mode is not 0600: %s\n' \
      "$relative_path" >&2
    exit 1
  }
  jq -n -c \
    --arg path "$relative_path" \
    --arg sha256 "$(sha256_file "$evidence_file")" \
    --argjson bytes "$(file_bytes "$evidence_file")" \
    '{
      path:$path,
      sha256:$sha256,
      bytes:$bytes,
      mode:"0600"
    }' >>"$artifact_lines"
done < <(find "$run_dir" -type f | LC_ALL=C sort)
artifacts="$scratch/artifacts.json"
jq -s . "$artifact_lines" >"$artifacts"
artifact_count="$(jq 'length' "$artifacts")"

summary="$run_dir/summary.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg treeSHA256 "$source_tree_sha" \
  --arg manifest "source-manifest.tsv" \
  --argjson sourceFiles "$source_file_count" \
  --arg binarySHA256 "$candidate_sha" \
  --arg hostContentionSHA256 \
    "$(sha256_file "$host_contention_evidence")" \
  --arg hostMeasurementContentionSHA256 \
    "$(sha256_file "$measurement_contention_evidence")" \
  --argjson hostMeasurementContentionSamples \
    "$measurement_contention_samples" \
  --arg hostStateStartSHA256 \
    "$(sha256_file "$run_dir/host-state-start.txt")" \
  --arg hostStateBeforeRealLimaSHA256 \
    "$(sha256_file "$run_dir/host-state-before-real-lima.txt")" \
  --arg hostStateAfterRealLimaSHA256 \
    "$(sha256_file "$run_dir/host-state-after-real-lima.txt")" \
  --argjson local "$(cat "$run_dir/lanes/local-query-render.json")" \
  --argjson process "$(cat "$run_dir/lanes/daemon-tui-process.json")" \
  --argjson browser "$(cat "$run_dir/lanes/browser-performance.json")" \
  --argjson concurrent "$(cat "$concurrent_performance")" \
  --argjson privacy "$(cat "$privacy_summary")" \
  --argjson tests "$(cat "$run_dir/tests/store-recovery.result.json")" \
  --argjson receipts "$(cat "$receipts")" \
  --argjson artifacts "$(cat "$artifacts")" \
  '{
    schema:"hideout.release-candidate-performance/v1",
    generatedAt:$generatedAt,
    result:"passed",
    candidateAcceptance:($dirty | not),
    source:{
      commit:$commit,
      dirty:$dirty,
      treeSHA256:$treeSHA256,
      manifest:$manifest,
      files:$sourceFiles,
      stableAcrossRun:true
    },
    candidate:{
      binarySHA256:$binarySHA256,
      exactSourceTreeBound:true,
      acceptance:($dirty | not)
    },
    methodology:{
      rawSamplesPresent:true,
      percentile:"nearest-rank-ceiling",
      percentilesIndependentlyRecomputed:true,
      unitsStable:true
    },
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
        sha256:$hostContentionSHA256
      },
      measurementContentionAssessment:{
        passed:true,
        method:
          "continuous-one-second-three-hit-classified-contention-rejection-generic-diagnostics",
        samples:$hostMeasurementContentionSamples,
        rollingWindow:3,
        minimumHits:3,
        genericHighCPUPolicy:"diagnostic-only",
        genericCPUPercentThreshold:50,
        virtualizationCPUPercentThreshold:5,
        buildOrTestCPUPercentThreshold:10,
        path:"host-contention-measurement.txt",
        sha256:$hostMeasurementContentionSHA256
      },
      snapshots:[
        {
          phase:"start",
          path:"host-state-start.txt",
          sha256:$hostStateStartSHA256
        },
        {
          phase:"before-real-lima",
          path:"host-state-before-real-lima.txt",
          sha256:$hostStateBeforeRealLimaSHA256
        },
        {
          phase:"after-real-lima",
          path:"host-state-after-real-lima.txt",
          sha256:$hostStateAfterRealLimaSHA256
        }
      ]
    },
    metrics:{
      query:$local.query,
      render:$local.render,
      daemonRSS:$process.daemonRSS,
      tuiRSS:$process.tuiRSS,
      tuiReady:$process.tuiReady,
      browserFreshness:$browser.liveUpdate,
      warmAttach:$concurrent.warmAttach,
      referenceWorkload:$concurrent.referenceWorkload,
      observerCPU:$privacy.performance.observerCPU,
      observerRSS:$privacy.performance.observerRSS,
      eventRate:$privacy.performance.eventRate,
      healthyDropRate:$privacy.performance.healthyDropRate,
      quota:$privacy.quota
    },
    recoveryAndQuotaTests:$tests,
    claimReceipts:$receipts,
    validation:{
      localThresholds:true,
      processThresholds:true,
      browserFreshnessP95WithinTwoSeconds:true,
      warmAttachP95WithinTwoSeconds:true,
      referenceMedianOverheadWithinTenPercent:true,
      referenceMedianUpperConfidenceBoundWithinTenPercent:true,
      observerCPUAndRSSWithinBudgets:true,
      healthyDropRateWithinOnePercentAndAccounted:true,
      quotaWithinOneActiveSegment:true,
      exactRecoveryPassSet:true,
      sourceStableAcrossRun:true,
      contractReceiptsExact:true,
      quietHostExplicitlyConfirmed:true,
      initialHostContentionAssessmentPassed:true,
      measurementHostContentionAssessmentPassed:true,
      hostDiagnosticsRetained:true
    },
    artifacts:$artifacts,
    limitations:
      (if $dirty then
        [
          "This is exact dirty-source performance evidence; release-candidate acceptance remains false until the clean installed candidate is rerun."
        ]
      else
        []
      end)
  }' >"$summary"
chmod 0600 "$summary"

if ! validate_summary "$summary" "$source_tree_sha" "$artifact_count"; then
  printf \
    'release-candidate-performance: summary semantic validation failed\n' \
    >&2
  exit 1
fi

summary_relative="$run_id/summary.json"
summary_sha="$(sha256_file "$summary")"
pointer_tmp="$evidence_out/.result.$$.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg treeSHA256 "$source_tree_sha" \
  --arg run "$run_id" \
  --arg summary "$summary_relative" \
  --arg summarySHA256 "$summary_sha" \
  '{
    schema:"hideout.release-candidate-performance-pointer/v1",
    generatedAt:$generatedAt,
    source:{commit:$commit,dirty:$dirty,treeSHA256:$treeSHA256},
    result:"passed",
    run:$run,
    summary:$summary,
    summarySHA256:$summarySHA256,
    candidateAcceptance:($dirty | not)
  }' >"$pointer_tmp"
chmod 0600 "$pointer_tmp"
mv "$pointer_tmp" "$evidence_out/result.json"

gate_completed=1
printf \
  'release-candidate-performance: passed evidence=%s summary-sha256=%s artifacts=%s\n' \
  "$summary" "$summary_sha" "$artifact_count"
