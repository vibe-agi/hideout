#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
# shellcheck disable=SC1091
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0
gate_review_started=0
gate_review_result=""
gate_review_started_at=""
gate_review_started_epoch=0
gate_stage="argument-validation"
candidate_commit=""
candidate_tree=""

umask 077
export LC_ALL=C
export TZ=UTC

candidate_result=""
out="$repo_root/.artifacts/046/migration-lima"
preflight_only=0
timeout_seconds="${HIDEOUT_MIGRATION_LIMA_TIMEOUT_SECONDS:-1800}"
scratch=""
scratch_parent=""
run_dir=""
hideout_binary=""
lima_home=""
source_store=""
safe_one_store=""
safe_two_store=""
safe_three_store=""
exact_store=""
wrong_store=""
compat_store=""
daemon_socket_path_max=100
lima_socket_path_max=104
lima_socket_probe="ssh.sock.1234567890123456"
# shellcheck disable=SC2016 # evaluated by the guest shell
migration_guest_verify_script='
  set -eu
  printf "root="
  cat /home/developer/migration-root-proof
  printf "machine="
  tr -d "[:space:]" </etc/machine-id
  printf "\nssh="
  set -- /etc/ssh/ssh_host_*_key.pub
  [ -e "$1" ]
  cat "$@" | sha256sum | sed "s/ .*//"
  found=0
  for path in /mnt/lima-*/migration-attached-proof; do
    [ -f "$path" ] || continue
    printf "attached="
    cat "$path"
    found=1
  done
  [ "$found" -eq 1 ]
  [ ! -e /workspace/host-only.txt ]
'

retain_failure_diagnostics() {
  [ -n "${scratch:-}" ] && [ -d "$scratch" ] || return 0
  [ -n "${run_dir:-}" ] && [ -d "$run_dir" ] || return 0
  local diagnostic_dir=""
  local source diagnostic_name destination diagnostic_bytes
  local store_path store_label relative
  for source in \
    "$scratch"/*.log "$scratch"/*.txt "$scratch"/*.json \
    "$scratch"/*.err "$scratch"/*.out; do
    [ -f "$source" ] && [ ! -L "$source" ] || continue
    diagnostic_bytes="$(stat -f '%z' "$source" 2>/dev/null || stat -c '%s' "$source")"
    case "$diagnostic_bytes" in
      "" | *[!0-9]*) continue ;;
    esac
    [ "$diagnostic_bytes" -le 1048576 ] || continue
    if [ -z "$diagnostic_dir" ]; then
      diagnostic_dir="$run_dir/diagnostics"
      mkdir -p "$diagnostic_dir"
      chmod 0700 "$diagnostic_dir"
    fi
    diagnostic_name="${source##*/}"
    destination="$diagnostic_dir/$diagnostic_name"
    cp "$source" "$destination"
    chmod 0600 "$destination"
  done
  for store_path in \
    "${source_store:-}" "${safe_one_store:-}" "${safe_two_store:-}" \
    "${safe_three_store:-}" "${exact_store:-}" "${wrong_store:-}" \
    "${compat_store:-}"; do
    [ -n "$store_path" ] && [ -d "$store_path" ] || continue
    store_label="${store_path##*/}"
    for source in \
      "$store_path"/daemon/*.log "$store_path"/daemon/*.jsonl \
      "$store_path"/migration/operations/*.json \
      "$store_path"/logs/*.jsonl; do
      [ -f "$source" ] && [ ! -L "$source" ] || continue
      diagnostic_bytes="$(stat -f '%z' "$source" 2>/dev/null || stat -c '%s' "$source")"
      case "$diagnostic_bytes" in
        "" | *[!0-9]*) continue ;;
      esac
      [ "$diagnostic_bytes" -le 1048576 ] || continue
      if [ -z "$diagnostic_dir" ]; then
        diagnostic_dir="$run_dir/diagnostics"
        mkdir -p "$diagnostic_dir"
        chmod 0700 "$diagnostic_dir"
      fi
      relative="${source#"$store_path"/}"
      diagnostic_name="$store_label--${relative//\//--}"
      destination="$diagnostic_dir/$diagnostic_name"
      cp "$source" "$destination"
      chmod 0600 "$destination"
    done
  done
}

scratch_supports_daemon_sockets() {
  local root="$1"
  local store_name control_socket session_socket
  for store_name in \
    source-store safe-one-store safe-two-store safe-three-store exact-store \
    wrong-store compat-store; do
    control_socket="$root/$store_name/daemon/hideoutd.sock"
    session_socket="$root/$store_name/daemon/hideoutd-session.sock"
    [ "${#control_socket}" -le "$daemon_socket_path_max" ] &&
      [ "${#session_socket}" -le "$daemon_socket_path_max" ] || return 1
  done
}

scratch_supports_lima_sockets() {
  local root="$1"
  local longest_instance="backend_0000000000000000000000000000000000000000"
  local socket_path="$root/lima/$longest_instance/$lima_socket_probe"
  [ "${#socket_path}" -lt "$lima_socket_path_max" ]
}

install_candidate_package() {
  local package_root_value="$1"
  local prefix_value="$2"
  local store_value="$3"
  local lima_home_value="$4"
  local log_value="$5"
  LIMA_HOME="$lima_home_value" "$package_root_value/install.sh" \
    --prefix "$prefix_value" --store "$store_value" \
    --backend lima --network direct >"$log_value" 2>&1
}

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/migration-lima.sh --candidate-result FILE [--out DIR]" \
    "       scripts/gates/migration-lima.sh --preflight" \
    "" \
    "Consumes an already accepted package candidate without rebuilding it, installs it" \
    "into a private prefix, and exercises one real stopped Lima source with root and" \
    "attached disks. The same encrypted bundle is imported into three independent Safe" \
    "Clone stores and one Exact Guest Restore store. The third Safe Clone is killed" \
    "during materialization and after a durable stopped-adoption response to prove" \
    "daemon-restart recovery without replaying an ambiguous guest mutation." \
    "" \
    "The gate verifies data fidelity, source immutability, fresh control/backend" \
    "identities, guest identity policy, disabled host-workspace authority, terminal" \
    "receipts, bundle reuse, fail-closed compatibility, and candidate/package binding." \
    "It has no performance lane and never publishes."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --candidate-result)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'migration-lima: --candidate-result requires a file\n' >&2
        exit 2
      }
      candidate_result="$2"
      shift 2
      ;;
    --out)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'migration-lima: --out requires a directory\n' >&2
        exit 2
      }
      out="$2"
      shift 2
      ;;
    --preflight)
      preflight_only=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'migration-lima: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

fail() {
  if [ "${gate_review_started:-0}" -eq 1 ]; then
    write_gate_run_review failed "$1" || true
  fi
  retain_failure_diagnostics
  if [ -n "${run_dir:-}" ] && [ -d "$run_dir/diagnostics" ]; then
    printf 'migration-lima: private diagnostics: %s\n' \
      "$run_dir/diagnostics" >&2
  fi
  printf 'migration-lima: %s\n' "$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 ||
    fail "missing required command: $1"
}

wait_operation_phase() {
  local store="$1" operation="$2" wanted="$3" stale_revision="${4:-}"
  local path="$store/migration/operations/$operation.json"
  local started now snapshot phase revision
  started="$(date +%s)"
  while :; do
    snapshot="$(jq -er '[.phase, .revision] | @tsv' "$path" 2>/dev/null || true)"
    phase="${snapshot%%$'\t'*}"
    revision="${snapshot#*$'\t'}"
    if [ "$phase" = "$wanted" ]; then
      if [ -z "$stale_revision" ] || {
        case "$revision:$stale_revision" in
          *[!0-9:]* | :* | *:) false ;;
          *) [ "$revision" -gt "$stale_revision" ] ;;
        esac
      }; then
        return 0
      fi
    fi
    case "$phase" in
      recoverable-failure)
        if [ -n "$stale_revision" ]; then
          case "$revision:$stale_revision" in
            *[!0-9:]* | :* | *:) return 1 ;;
            *) [ "$revision" -le "$stale_revision" ] || return 1 ;;
          esac
        else
          return 1
        fi
        ;;
      complete | cancelled | rolled-back | failed)
        return 1
        ;;
    esac
    now="$(date +%s)"
    [ $((now - started)) -lt "$timeout_seconds" ] || return 1
    sleep 0.05
  done
}

migration_destination_profile() {
  local store="$1" environment_id="$2" environment_name="$3"
  local record profile_path profile_name
  case "$environment_id" in
    env_[A-Za-z0-9_-]*) ;;
    *) return 1 ;;
  esac
  record="$store/environments/$environment_id/environment.json"
  [ -f "$record" ] && [ ! -L "$record" ] || return 1
  profile_name="$(jq -er \
    --arg id "$environment_id" --arg name "$environment_name" '
      select(
        .version == "hideout.environment/v2" and
        .id == $id and .name == $name and
        .mode == "dedicated-portal" and .status == "stopped"
      ) |
      .profile |
      select(
        type == "string" and length >= 1 and length <= 128 and
        . != "." and . != ".." and test("^[A-Za-z0-9._-]+$")
      )
    ' "$record")" || return 1
  profile_path="$store/profiles/$profile_name/profile.json"
  [ -f "$profile_path" ] && [ ! -L "$profile_path" ] || return 1
  printf '%s\n' "$profile_name"
}

migration_inspected_profile() {
  local inspect_path="$1"
  awk '
    /^  backend: / {
      for (field_number = 1; field_number <= NF; field_number++) {
        if ($field_number == "profile:") {
          profile_value = $(field_number + 1)
          profile_fields++
        }
      }
    }
    END {
      if (profile_fields != 1 || profile_value == "") {
        exit 1
      }
      print profile_value
    }
  ' "$inspect_path"
}

migration_network_authority_ref() {
  local inspect_path="$1" source_ref="$2"
  jq -er --arg source "$source_ref" '
    .inventory as $inventory |
    ($inventory.environments |
      map(select(.sourceRef == $source))) as $environments |
    select($environments | length == 1) |
    $environments[0].authorityProposalIds as $environment_refs |
    [
      $inventory.authorityProposals[] as $proposal |
      $proposal |
      select(
        .class == "network" and .state == "disabled" and
        .sourceSummary == "{\"mode\":\"direct\"}" and
        ($environment_refs | index($proposal.proposalId)) != null
      )
    ] as $matches |
    select($matches | length == 1) |
    $matches[0].proposalId |
    select(
      type == "string" and
      test("^authority_[a-z0-9_-]{8,120}$")
    )
  ' "$inspect_path"
}

migration_network_authority_receipt() {
  local status_path="$1" authority_ref="$2" source_ref="$3"
  local proposal_count="$4"
  jq -e \
    --arg authority "$authority_ref" \
    --arg source "$source_ref" \
    --argjson proposalCount "$proposal_count" '
      $proposalCount >= 1 and
      .terminalReceipt.approvedAuthority == [{
        proposalId:$authority,
        environmentRef:$source,
        class:"network"
      }] and
      (.terminalReceipt.disabledAuthorityProposalIds | type == "array") and
      (.terminalReceipt.disabledAuthorityProposalIds | length) ==
        ($proposalCount - 1) and
      (.terminalReceipt.disabledAuthorityProposalIds |
        index($authority)) == null
    ' "$status_path" >/dev/null
}

migration_identity_policy_receipt() {
  local status_path="$1" policy="$2"
  case "$policy" in
    safe)
      jq -e '
        .terminalReceipt.identityPolicies == {
          safeClone:1,
          exactGuestRestore:0,
          freshControl:1,
          freshBackend:1
        }
      ' "$status_path" >/dev/null
      ;;
    exact)
      jq -e '
        .terminalReceipt.identityPolicies == {
          safeClone:0,
          exactGuestRestore:1,
          freshControl:1,
          freshBackend:1
        }
      ' "$status_path" >/dev/null
      ;;
    *) return 1 ;;
  esac
}

migration_guest_profile_authorized() {
  local profile_path="$1"
  [ -f "$profile_path" ] && [ ! -L "$profile_path" ] || return 1
  jq -e '
    .network.mode == "direct" and
    (.network.proxySecretRef // "") == "" and
    (.policy.maxCapabilities | type == "array") and
    (.policy.maxCapabilities | index("guest.exec")) != null and
    (.policy.maxCapabilities | index("network.connect")) != null
  ' "$profile_path" >/dev/null
}

migration_lima_stopped_inventory() {
  local inventory_path="$1" instance="$2"
  jq -s -e --arg instance "$instance" '
    [.[] | select(.name == $instance)] as $matches |
    ($matches | length) == 1 and
    $matches[0].status == "Stopped" and
    $matches[0].vmType == "vz" and
    $matches[0].arch == "aarch64" and
    $matches[0].limaVersion == "2.2.0" and
    (($matches[0].errors // []) | length) == 0
  ' "$inventory_path" >/dev/null
}

validate_migration_lima_summary() {
  jq -e '
    .schema == "hideout.migration-lima-evidence/v1" and
    .result == "passed" and
    .candidateAcceptance == true and
    .bundle.reusedDestinations == 4 and
    .checks == {
      packageCandidateInstalled:true,
      encryptedBundleSealed:true,
      rootDiskFidelity:true,
      attachedDiskFidelity:true,
      hostWorkspaceExcluded:true,
      sourceImmutable:true,
      wrongPassphraseNoDestinationEnvironment:true,
      incompatibleAdoptionExecutorRejectedBeforeEffects:true,
      terminalReceipts:true,
      limaInventoryStopped:true,
      networkAuthorityReapproved:true,
      sameBundleThreeSafeClones:true,
      freshControlIdentity:true,
      freshBackendIdentity:true,
      safeCloneGuestIdentityFresh:true,
      exactRestoreGuestIdentityPreserved:true,
      materializationCrashResumed:true,
      adoptionCrashRecovered:true,
      daemonIdentityFreshAcrossCrashRecovery:true
    } and
    all(.sourceImmutability[]; .beforeSHA256 == .afterSHA256) and
    (.identityEvidence.control as $identity |
      ($identity.destinationDigests | length) == 4 and
      ($identity.destinationDigests | unique | length) == 4 and
      ($identity.destinationDigests | index($identity.sourceDigest)) == null) and
    (.identityEvidence.backend as $identity |
      ($identity.destinationDigests | length) == 4 and
      ($identity.destinationDigests | unique | length) == 4 and
      ($identity.destinationDigests | index($identity.sourceDigest)) == null) and
    (.identityEvidence.guest as $identity |
      ($identity.safeCloneDigests | length) == 3 and
      ($identity.safeCloneDigests | unique | length) == 3 and
      ($identity.safeCloneDigests | index($identity.sourceDigest)) == null and
      $identity.exactRestoreDigest == $identity.sourceDigest) and
    .crashRecovery.materializationRequiredProtectedResume == true and
    .crashRecovery.adoptionRestartedWithoutBundleSecret == true and
    [.crashRecovery.cuts[].phase] == ["materializing","adopting"] and
    ([
      .crashRecovery.cuts[].daemonInstanceDigest,
      .crashRecovery.finalDaemonInstanceDigest
    ] as $daemonInstances |
      ($daemonInstances | length) == 3 and
      ($daemonInstances | unique | length) == 3) and
    .compatibilityEvidence == {
      fixture:"missing-package-owned-zero-network-executor",
      errorCode:"migration.capability.unavailable",
      operationCreated:false,
      destinationEnvironmentCreated:false
    } and
    (.artifacts | length) == 8 and
    ([.artifacts[].path] | unique | length) == 8 and
    all(.artifacts[];
      .bytes > 0 and .mode == "0600" and
      (.sha256 | test("^[a-f0-9]{64}$"))) and
    all([
      .candidate.pointerSHA256,
      .candidate.archiveSHA256,
      .candidate.installedBinarySHA256,
      .bundle.sha256,
      .sourceImmutability.rootDisk.beforeSHA256,
      .sourceImmutability.rootDisk.afterSHA256,
      .sourceImmutability.attachedDisk.beforeSHA256,
      .sourceImmutability.attachedDisk.afterSHA256,
      .sourceImmutability.environmentRecord.beforeSHA256,
      .sourceImmutability.environmentRecord.afterSHA256,
      .identityEvidence.control.sourceDigest,
      .identityEvidence.control.destinationDigests[],
      .identityEvidence.backend.sourceDigest,
      .identityEvidence.backend.destinationDigests[],
      .identityEvidence.guest.sourceDigest,
      .identityEvidence.guest.safeCloneDigests[],
      .identityEvidence.guest.exactRestoreDigest,
      .crashRecovery.cuts[].daemonInstanceDigest,
      .crashRecovery.finalDaemonInstanceDigest
    ][]; test("^[a-f0-9]{64}$"))
  ' "${1:--}" >/dev/null
}

write_migration_product_evidence() {
  local manifest="$1" summary="$2" summary_relative="$3"
  local package_identity="$4" proof_registry="$5"
  local manifest_parent manifest_tmp summary_actual_relative summary_sha
  case "$summary_relative" in
    "" | /* | . | .. | ../* | */.. | */../*)
      return 1
      ;;
  esac
  [ -f "$summary" ] && [ ! -L "$summary" ] &&
    [ -f "$package_identity" ] && [ ! -L "$package_identity" ] &&
    [ -f "$proof_registry" ] && [ ! -L "$proof_registry" ] || return 1
  manifest_parent="$(dirname "$manifest")"
  mkdir -p "$manifest_parent"
  manifest_parent="$(CDPATH='' cd -- "$manifest_parent" && pwd -P)"
  summary="$(
    CDPATH='' cd -- "$(dirname "$summary")" &&
      printf '%s/%s\n' "$(pwd -P)" "$(basename "$summary")"
  )"
  case "$summary" in
    "$manifest_parent"/*)
      summary_actual_relative="${summary#"$manifest_parent"/}"
      ;;
    *)
      return 1
      ;;
  esac
  [ "$summary_relative" = "$summary_actual_relative" ] || return 1
  summary_sha="$(shasum -a 256 "$summary" | awk '{print $1}')" || return 1
  manifest_tmp="$manifest_parent/.product-hardening-evidence.$$.json"
  jq -e -n \
    --arg generatedAt "$(jq -er '.generatedAt' "$summary")" \
    --arg commit "$(jq -er '.source.commit' "$summary")" \
    --arg summary "$summary_relative" \
    --arg summarySHA256 "$summary_sha" \
    --slurpfile package "$package_identity" \
    --slurpfile registry "$proof_registry" '
      [
        $registry[0].requirements[] |
        select(
          .featureId == "046-portable-hideout-migration" and
          .proofId == "046.migration.real-lima"
        )
      ] as $requirements |
      if ($requirements | length) != 1 then
        error("046 migration proof registry entry is missing or duplicated")
      else
        $requirements[0] as $requirement |
        {
          version:"hideout.product-hardening-evidence/v1",
          generatedAt:$generatedAt,
          commit:$commit,
          dirty:false,
          packageIdentity:$package[0],
          proofs:[{
            proofId:$requirement.proofId,
            featureId:$requirement.featureId,
            mode:"real-gate",
            evidenceClass:"portable-migration-real-lima",
            status:"passed",
            commandSummary:"exact packaged migration across independent Lima stores with Safe Clone, Exact Guest Restore, crash recovery, and compatibility refusal",
            coveredClaims:[
              $requirement.claimIds[] | {
                claimId:.,
                source:"spec",
                description:"exact-package real-Lima migration release proof"
              }
            ],
            prerequisites:[{
              name:"real-macos-arm64-lima",
              status:"available"
            }],
            artifacts:[{
              kind:"event-summary",
              path:$summary,
              sha256:$summarySHA256,
              redactionStatus:"passed",
              description:"strict migration fidelity and identity summary"
            }],
            redactionStatus:"passed",
            host:{os:"darwin",arch:"arm64"},
            notes:[
              "Functional fidelity and identity-policy evidence; no performance claim.",
              "One physical macOS host with independent source and destination stores."
            ]
          }]
        }
      end
    ' >"$manifest_tmp" || {
      rm -f "$manifest_tmp"
      return 1
    }
  chmod 0600 "$manifest_tmp"
  mv "$manifest_tmp" "$manifest"
}

migration_lima_summary_fixture() {
  jq -nc '
    def digest($character): $character * 64;
    def commit($character): $character * 40;
    def artifact($path;$character): {
      path:$path,
      bytes:1,
      mode:"0600",
      sha256:digest($character)
    };
    {
      schema:"hideout.migration-lima-evidence/v1",
      generatedAt:"2026-08-05T00:00:00Z",
      result:"passed",
      candidateAcceptance:true,
      source:{commit:commit("a"),tree:commit("b"),dirty:false},
      candidate:{
        pointerSHA256:digest("a"),
        archiveSHA256:digest("b"),
        installedBinarySHA256:digest("c")
      },
      bundle:{sha256:digest("d"),bytes:1,reusedDestinations:4},
      sourceImmutability:{
        rootDisk:{beforeSHA256:digest("1"),afterSHA256:digest("1")},
        attachedDisk:{beforeSHA256:digest("2"),afterSHA256:digest("2")},
        environmentRecord:{beforeSHA256:digest("3"),afterSHA256:digest("3")}
      },
      identityEvidence:{
        control:{
          sourceDigest:digest("4"),
          destinationDigests:[digest("5"),digest("6"),digest("7"),digest("8")]
        },
        backend:{
          sourceDigest:digest("9"),
          destinationDigests:[digest("a"),digest("b"),digest("c"),digest("d")]
        },
        guest:{
          sourceDigest:digest("e"),
          safeCloneDigests:[digest("f"),digest("0"),digest("1")],
          exactRestoreDigest:digest("e")
        }
      },
      crashRecovery:{
        cuts:[
          {phase:"materializing",daemonInstanceDigest:digest("2")},
          {phase:"adopting",daemonInstanceDigest:digest("3")}
        ],
        finalDaemonInstanceDigest:digest("4"),
        materializationRequiredProtectedResume:true,
        adoptionRestartedWithoutBundleSecret:true
      },
      compatibilityEvidence:{
        fixture:"missing-package-owned-zero-network-executor",
        errorCode:"migration.capability.unavailable",
        operationCreated:false,
        destinationEnvironmentCreated:false
      },
      checks:{
        packageCandidateInstalled:true,
        encryptedBundleSealed:true,
        rootDiskFidelity:true,
        attachedDiskFidelity:true,
        hostWorkspaceExcluded:true,
        sourceImmutable:true,
        wrongPassphraseNoDestinationEnvironment:true,
        incompatibleAdoptionExecutorRejectedBeforeEffects:true,
        terminalReceipts:true,
        limaInventoryStopped:true,
        networkAuthorityReapproved:true,
        sameBundleThreeSafeClones:true,
        freshControlIdentity:true,
        freshBackendIdentity:true,
        safeCloneGuestIdentityFresh:true,
        exactRestoreGuestIdentityPreserved:true,
        materializationCrashResumed:true,
        adoptionCrashRecovered:true,
        daemonIdentityFreshAcrossCrashRecovery:true
      },
      artifacts:[
        artifact("export-terminal.json";"5"),
        artifact("gate.log";"6"),
        artifact("import-exact-terminal.json";"7"),
        artifact("import-safe-one-terminal.json";"8"),
        artifact("import-safe-three-terminal.json";"9"),
        artifact("import-safe-two-terminal.json";"a"),
        artifact("run-review.json";"b"),
        artifact("stage-events.jsonl";"c")
      ],
      limitations:[
        "This is a functional fidelity and identity-policy gate; it makes no performance claim.",
        "The transfer is exercised between independent stores on one physical macOS host."
      ]
    }
  '
}

if [ "$preflight_only" -eq 1 ]; then
  [ -z "$candidate_result" ] ||
    fail "--preflight and --candidate-result are mutually exclusive"
  require_command bash
  require_command cmp
  require_command cp
  require_command find
  require_command go
  require_command grep
  require_command jq
  require_command mktemp
  require_command shasum
  require_command shellcheck
  require_command stat
  bash -n scripts/gates/migration-lima.sh scripts/gates/migration.sh
  shellcheck scripts/gates/migration-lima.sh scripts/gates/migration.sh
  sh -n -c "$migration_guest_verify_script"
  summary_fixture="$(migration_lima_summary_fixture)"
  printf '%s\n' "$summary_fixture" | validate_migration_lima_summary ||
    fail "summary validator rejected its valid preflight fixture"
  for mutation in \
    '.identityEvidence.guest.safeCloneDigests[2] = .identityEvidence.guest.safeCloneDigests[1]' \
    '.crashRecovery.finalDaemonInstanceDigest = .crashRecovery.cuts[0].daemonInstanceDigest' \
    '.compatibilityEvidence.operationCreated = true' \
    '.artifacts[0].mode = "0644"'; do
    invalid_fixture="$(jq -c "$mutation" <<<"$summary_fixture")"
    if printf '%s\n' "$invalid_fixture" | validate_migration_lima_summary; then
      fail "summary validator accepted invalid preflight mutation: $mutation"
    fi
  done
  scratch_supports_daemon_sockets "/private/tmp/hm.ABCDEF" ||
    fail "short scratch fixture cannot host daemon sockets"
  long_scratch_fixture="/private/tmp/$(printf '%090d' 0)"
  if scratch_supports_daemon_sockets "$long_scratch_fixture"; then
    fail "long scratch fixture was accepted for daemon sockets"
  fi
  scratch_supports_lima_sockets "/private/tmp/hm.ABCDEF" ||
    fail "short scratch fixture cannot host the longest imported Lima socket"
  if scratch_supports_lima_sockets "/private/tmp/ho-mig.ABCDEF"; then
    fail "overlong historical migration scratch was accepted for Lima sockets"
  fi
  diagnostic_fixture="$(mktemp -d "${TMPDIR:-/tmp}/ho-mig-preflight.XXXXXX")"
  scratch="$diagnostic_fixture/scratch"
  run_dir="$diagnostic_fixture/evidence"
  source_store="$scratch/source-store"
  mkdir -p "$scratch" "$run_dir" "$source_store/daemon"

  product_fixture="$diagnostic_fixture/product"
  product_summary_dir="$product_fixture/run-fixture"
  product_summary="$product_summary_dir/summary.json"
  product_package="$product_fixture/package-identity.json"
  product_registry="$product_fixture/proof-registry.json"
  product_manifest="$product_fixture/product-hardening-evidence.json"
  mkdir -p "$product_summary_dir"
  printf '%s\n' "$summary_fixture" >"$product_summary"
  jq -n '
    {
      name:"hideout",
      productVersion:"0.1.0-alpha.4",
      sourceCommit:("a" * 40),
      artifactSHA256:("b" * 64),
      hostOS:"darwin",
      hostArch:"arm64"
    }
  ' >"$product_package"
  go run ./cmd/hideout support proof-registry --json >"$product_registry"
  write_migration_product_evidence \
    "$product_manifest" "$product_summary" \
    "run-fixture/summary.json" "$product_package" "$product_registry" ||
    fail "valid migration product evidence fixture could not be assembled"
  go run ./cmd/hideout-schema-validate \
    schemas/product-hardening-evidence.schema.json \
    "$product_manifest" >/dev/null ||
    fail "valid migration product evidence fixture failed schema validation"
  go run ./internal/productevidence/cmd/validate-046 \
    --commit "$(jq -er '.source.commit' "$product_summary")" \
    --package-identity "$product_package" \
    "$product_manifest" >/dev/null ||
    fail "valid migration product evidence fixture failed semantic validation"
  jq '.candidate.archiveSHA256 = ("d" * 64)' \
    "$product_summary" >"$product_summary.mutated"
  mv "$product_summary.mutated" "$product_summary"
  write_migration_product_evidence \
    "$product_manifest" "$product_summary" \
    "run-fixture/summary.json" "$product_package" "$product_registry" ||
    fail "archive-mismatch migration fixture could not be assembled"
  if go run ./internal/productevidence/cmd/validate-046 \
    --commit "$(jq -er '.source.commit' "$product_summary")" \
    --package-identity "$product_package" \
    "$product_manifest" >/dev/null 2>&1; then
    fail "migration product validator accepted a different package archive"
  fi
  printf '%s\n' "$summary_fixture" >"$product_summary"
  jq '.identityEvidence.guest.safeCloneDigests[2] = .identityEvidence.guest.safeCloneDigests[1]' \
    "$product_summary" >"$product_summary.mutated"
  mv "$product_summary.mutated" "$product_summary"
  write_migration_product_evidence \
    "$product_manifest" "$product_summary" \
    "run-fixture/summary.json" "$product_package" "$product_registry" ||
    fail "duplicate-identity migration fixture could not be assembled"
  if go run ./internal/productevidence/cmd/validate-046 \
    --commit "$(jq -er '.source.commit' "$product_summary")" \
    --package-identity "$product_package" \
    "$product_manifest" >/dev/null 2>&1; then
    fail "migration product validator accepted a duplicate Safe Clone identity"
  fi

  printf 'candidate install diagnostic fixture\n' >"$scratch/install.log"
  printf 'must not be retained\n' >"$scratch/passphrase"
  printf 'migration worker diagnostic fixture\n' \
    >"$source_store/daemon/daemon-audit.jsonl"
  retain_failure_diagnostics
  cmp -s "$scratch/install.log" "$run_dir/diagnostics/install.log" || {
    find "$diagnostic_fixture" -depth -delete
    fail "failure diagnostic fixture was not retained"
  }
  [ "$(stat -f '%Lp' "$run_dir/diagnostics/install.log" 2>/dev/null ||
    stat -c '%a' "$run_dir/diagnostics/install.log")" = "600" ] || {
    find "$diagnostic_fixture" -depth -delete
    fail "retained failure diagnostic is not private"
  }
  [ ! -e "$run_dir/diagnostics/passphrase" ] || {
    find "$diagnostic_fixture" -depth -delete
    fail "non-diagnostic secret fixture was retained"
  }
  cmp -s "$source_store/daemon/daemon-audit.jsonl" \
    "$run_dir/diagnostics/source-store--daemon--daemon-audit.jsonl" || {
    find "$diagnostic_fixture" -depth -delete
    fail "nested migration diagnostic fixture was not retained"
  }
  install_fixture_root="$diagnostic_fixture/package"
  mkdir -p "$install_fixture_root"
  shell_dollar='$'
  printf '%s\n' \
    '#!/bin/sh' \
    'set -eu' \
    "printf \"lima=%s\\\\n\" \"${shell_dollar}LIMA_HOME\"" \
    'printf "args=%s\\n" "$*"' >"$install_fixture_root/install.sh"
  chmod 0700 "$install_fixture_root/install.sh"
  install_candidate_package \
    "$install_fixture_root" "$diagnostic_fixture/prefix" \
    "$diagnostic_fixture/store" "$diagnostic_fixture/lima" \
    "$diagnostic_fixture/install-environment.log"
  if ! grep -Fxq "lima=$diagnostic_fixture/lima" \
    "$diagnostic_fixture/install-environment.log" ||
    ! grep -Fxq \
      "args=--prefix $diagnostic_fixture/prefix --store $diagnostic_fixture/store --backend lima --network direct" \
      "$diagnostic_fixture/install-environment.log"; then
    find "$diagnostic_fixture" -depth -delete
    fail "candidate installer did not inherit the isolated Lima home and exact arguments"
  fi

  phase_fixture_store="$diagnostic_fixture/phase-store"
  phase_fixture_operation="op_migration_phase_fixture"
  phase_fixture_path="$phase_fixture_store/migration/operations/$phase_fixture_operation.json"
  mkdir -p "$(dirname "$phase_fixture_path")"
  printf '%s\n' '{"phase":"recoverable-failure","revision":5}' \
    >"$phase_fixture_path"
  (
    sleep 0.1
    printf '%s\n' '{"phase":"materializing","revision":6}' \
      >"$phase_fixture_path.next"
    mv "$phase_fixture_path.next" "$phase_fixture_path"
    sleep 0.1
    printf '%s\n' '{"phase":"adopting","revision":7}' \
      >"$phase_fixture_path.next"
    mv "$phase_fixture_path.next" "$phase_fixture_path"
  ) &
  phase_fixture_writer=$!
  saved_timeout_seconds="$timeout_seconds"
  timeout_seconds=2
  if ! wait_operation_phase \
    "$phase_fixture_store" "$phase_fixture_operation" adopting 5; then
    wait "$phase_fixture_writer" || true
    find "$diagnostic_fixture" -depth -delete
    fail "phase waiter rejected an accepted resume before its revision advanced"
  fi
  wait "$phase_fixture_writer"
  printf '%s\n' '{"phase":"recoverable-failure","revision":6}' \
    >"$phase_fixture_path"
  if wait_operation_phase \
    "$phase_fixture_store" "$phase_fixture_operation" adopting 5; then
    find "$diagnostic_fixture" -depth -delete
    fail "phase waiter accepted a new recoverable failure after resume"
  fi
  timeout_seconds="$saved_timeout_seconds"

  authority_fixture="$diagnostic_fixture/migration-inspect.json"
  authority_fixture_source="env_source_fixture1"
  authority_fixture_network="authority_network_fixture1"
  printf '%s\n' \
    '{"inventory":{"environments":[{"sourceRef":"env_source_fixture1","authorityProposalIds":["authority_host_fixture1","authority_network_fixture1"]}],"authorityProposals":[{"proposalId":"authority_host_fixture1","class":"host-app","sourceSummary":"{\"open\":{}}","state":"disabled"},{"proposalId":"authority_network_fixture1","class":"network","sourceSummary":"{\"mode\":\"direct\"}","state":"disabled"}]}}' \
    >"$authority_fixture"
  [ "$(migration_network_authority_ref \
    "$authority_fixture" "$authority_fixture_source")" = \
    "$authority_fixture_network" ] || {
    find "$diagnostic_fixture" -depth -delete
    fail "authenticated network authority fixture was not resolved exactly"
  }
  jq '
    .inventory.environments[0].authorityProposalIds +=
      ["authority_network_fixture2"] |
    .inventory.authorityProposals += [{
      proposalId:"authority_network_fixture2",
      class:"network",
      sourceSummary:"{\"mode\":\"direct\"}",
      state:"disabled"
    }]
  ' "$authority_fixture" >"$authority_fixture.ambiguous"
  if migration_network_authority_ref \
    "$authority_fixture.ambiguous" "$authority_fixture_source" >/dev/null; then
    find "$diagnostic_fixture" -depth -delete
    fail "ambiguous authenticated network authority fixture was accepted"
  fi

  authority_receipt_fixture="$diagnostic_fixture/import-terminal.json"
  printf '%s\n' \
    '{"terminalReceipt":{"approvedAuthority":[{"proposalId":"authority_network_fixture1","environmentRef":"env_source_fixture1","class":"network"}],"disabledAuthorityProposalIds":["authority_host_fixture1"],"identityPolicies":{"safeClone":1,"exactGuestRestore":0,"freshControl":1,"freshBackend":1}}}' \
    >"$authority_receipt_fixture"
  migration_network_authority_receipt \
    "$authority_receipt_fixture" "$authority_fixture_network" \
    "$authority_fixture_source" 2 || {
    find "$diagnostic_fixture" -depth -delete
    fail "approved network authority receipt fixture was rejected"
  }
  migration_identity_policy_receipt "$authority_receipt_fixture" safe || {
    find "$diagnostic_fixture" -depth -delete
    fail "Safe Clone identity receipt fixture was rejected"
  }
  if migration_identity_policy_receipt "$authority_receipt_fixture" exact; then
    find "$diagnostic_fixture" -depth -delete
    fail "Safe Clone identity receipt fixture was accepted as Exact Restore"
  fi
  jq '.terminalReceipt.approvedAuthority = []' \
    "$authority_receipt_fixture" >"$authority_receipt_fixture.disabled"
  if migration_network_authority_receipt \
    "$authority_receipt_fixture.disabled" "$authority_fixture_network" \
    "$authority_fixture_source" 2; then
    find "$diagnostic_fixture" -depth -delete
    fail "disabled network authority receipt fixture was accepted"
  fi

  destination_fixture_store="$diagnostic_fixture/destination-store"
  destination_fixture_id="env_destination_fixture1"
  destination_fixture_name="migration-source"
  mkdir -p \
    "$destination_fixture_store/environments/$destination_fixture_id" \
    "$destination_fixture_store/profiles/$destination_fixture_name"
  printf '%s\n' '{"version":"hideout.environment/v2","id":"env_destination_fixture1","name":"migration-source","profile":"migration-source","mode":"dedicated-portal","status":"stopped"}' \
    >"$destination_fixture_store/environments/$destination_fixture_id/environment.json"
  printf '%s\n' \
    '{"network":{"mode":"direct"},"policy":{"maxCapabilities":["guest.exec","network.connect"]}}' \
    >"$destination_fixture_store/profiles/$destination_fixture_name/profile.json"
  [ "$(migration_destination_profile \
    "$destination_fixture_store" "$destination_fixture_id" \
    "$destination_fixture_name")" = "$destination_fixture_name" ] || {
    find "$diagnostic_fixture" -depth -delete
    fail "destination profile fixture was not resolved from its exact environment"
  }
  migration_guest_profile_authorized \
    "$destination_fixture_store/profiles/$destination_fixture_name/profile.json" || {
    find "$diagnostic_fixture" -depth -delete
    fail "explicitly authorized migration profile fixture was rejected"
  }
  jq '.policy.maxCapabilities = ["guest.exec"]' \
    "$destination_fixture_store/profiles/$destination_fixture_name/profile.json" \
    >"$diagnostic_fixture/profile-without-network.json"
  if migration_guest_profile_authorized \
    "$diagnostic_fixture/profile-without-network.json"; then
    find "$diagnostic_fixture" -depth -delete
    fail "migration profile without network authority was accepted for guest verification"
  fi
  printf '%s\n' '{"version":"hideout.environment/v1","id":"env_destination_fixture1","name":"migration-source","profile":"migration-source","mode":"dedicated-portal","status":"stopped"}' \
    >"$destination_fixture_store/environments/$destination_fixture_id/environment.json"
  if migration_destination_profile \
    "$destination_fixture_store" "$destination_fixture_id" \
    "$destination_fixture_name" >/dev/null; then
    find "$diagnostic_fixture" -depth -delete
    fail "destination profile fixture accepted an obsolete environment record"
  fi
  inspect_profile_fixture="$diagnostic_fixture/environment-inspect.txt"
  printf '%s\n' \
    'environment: migration-source' \
    '  id: env_destination_fixture1' \
    '  backend: lima profile: migration-source' \
    '  instance: backend_destination_fixture1' \
    >"$inspect_profile_fixture"
  [ "$(migration_inspected_profile "$inspect_profile_fixture")" = \
    "$destination_fixture_name" ] || {
    find "$diagnostic_fixture" -depth -delete
    fail "destination inspection profile fixture was not parsed"
  }
  printf '%s\n' \
    'environment: migration-source' \
    '  backend: lima' \
    >"$inspect_profile_fixture"
  if migration_inspected_profile "$inspect_profile_fixture" >/dev/null; then
    find "$diagnostic_fixture" -depth -delete
    fail "destination inspection profile fixture accepted a missing profile"
  fi
  inventory_fixture="$diagnostic_fixture/lima-inventory.json"
  printf '%s\n' \
    '{"name":"backend_destination_fixture1","status":"Stopped","vmType":"vz","arch":"aarch64","errors":null,"limaVersion":"2.2.0"}' \
    >"$inventory_fixture"
  migration_lima_stopped_inventory \
    "$inventory_fixture" backend_destination_fixture1 || {
    find "$diagnostic_fixture" -depth -delete
    fail "stopped imported Lima inventory fixture was rejected"
  }
  jq '.status = "" | .vmType = "" | .arch = ""' \
    "$inventory_fixture" >"$inventory_fixture.unknown"
  if migration_lima_stopped_inventory \
    "$inventory_fixture.unknown" backend_destination_fixture1; then
    find "$diagnostic_fixture" -depth -delete
    fail "unknown imported Lima inventory fixture was accepted"
  fi
  find "$diagnostic_fixture" -depth -delete
  scratch=""
  run_dir=""
  source_store=""
  printf 'migration-lima: preflight=passed semantic-fixtures=34\n'
  exit 0
fi

for command in awk cp date find go grep jq limactl lsof mktemp mv openssl ps sed shasum sort ssh stat tail tar tr uname; do
  require_command "$command"
done

case "$timeout_seconds" in
  "" | *[!0-9]*)
    fail "HIDEOUT_MIGRATION_LIMA_TIMEOUT_SECONDS must be a positive integer"
    ;;
esac
[ "$timeout_seconds" -gt 0 ] ||
  fail "HIDEOUT_MIGRATION_LIMA_TIMEOUT_SECONDS must be positive"

[ -n "$candidate_result" ] || {
  usage >&2
  exit 2
}
[ "$(uname -s)" = "Darwin" ] && [ "$(uname -m)" = "arm64" ] ||
  fail "real migration gate requires macOS arm64"

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

sha256_text() {
  printf '%s' "$1" | shasum -a 256 | awk '{print $1}'
}

guest_identity_digest() {
  printf 'hideout.migration-guest-identity/v1\nmachine=%s\nssh=%s\n' "$1" "$2" |
    shasum -a 256 | awk '{print $1}'
}

file_mode() {
  stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1" 2>/dev/null
}

file_bytes() {
  stat -f '%z' "$1" 2>/dev/null || stat -c '%s' "$1" 2>/dev/null
}

mark_gate_stage() {
  gate_stage="$1"
  [ "${gate_review_started:-0}" -eq 1 ] || return 0
  jq -nc \
    --arg at "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg stage "$gate_stage" '{at:$at,stage:$stage}' \
    >>"$run_dir/stage-events.jsonl"
  chmod 0600 "$run_dir/stage-events.jsonl"
}

write_gate_run_review() {
  local result="$1" reason="${2:-}"
  [ "${gate_review_started:-0}" -eq 1 ] &&
    [ -n "${run_dir:-}" ] && [ -d "$run_dir" ] || return 0
  local finished_at finished_epoch elapsed_seconds
  local logical_bytes=0 encoded_bytes=0 bundle_bytes=0 completed_imports=0
  local status_path stages_json='[]' review_tmp
  finished_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  finished_epoch="$(date +%s)"
  elapsed_seconds=$((finished_epoch - gate_review_started_epoch))
  if [ -f "${scratch:-}/export-status.json" ]; then
    logical_bytes="$(jq -r '.progress.totalLogicalBytes // 0' "$scratch/export-status.json" 2>/dev/null || printf '0')"
    encoded_bytes="$(jq -r '.progress.totalEncodedBytes // 0' "$scratch/export-status.json" 2>/dev/null || printf '0')"
  fi
  if [ -n "${bundle:-}" ] && [ -f "$bundle" ] && [ ! -L "$bundle" ]; then
    bundle_bytes="$(file_bytes "$bundle" 2>/dev/null || printf '0')"
  fi
  for status_path in "${scratch:-}"/import-*-status.json; do
    [ -f "$status_path" ] || continue
    if jq -e '.state == "complete"' "$status_path" >/dev/null 2>&1; then
      completed_imports=$((completed_imports + 1))
    fi
  done
  if [ -f "$run_dir/stage-events.jsonl" ]; then
    stages_json="$(jq -s . "$run_dir/stage-events.jsonl" 2>/dev/null || printf '[]')"
  fi
  review_tmp="$run_dir/.run-review.$$.json"
  jq -n \
    --arg startedAt "$gate_review_started_at" \
    --arg finishedAt "$finished_at" \
    --arg result "$result" \
    --arg commit "$candidate_commit" \
    --arg tree "$candidate_tree" \
    --arg failureStage "$gate_stage" \
    --arg failureReason "$reason" \
    --argjson elapsedSeconds "$elapsed_seconds" \
    --argjson logicalBytes "$logical_bytes" \
    --argjson encodedBytes "$encoded_bytes" \
    --argjson bundleBytes "$bundle_bytes" \
    --argjson completedImports "$completed_imports" \
    --argjson stages "$stages_json" '
      {
        schema:"hideout.gate-run-review/v1",
        gate:"migration-lima",
        result:$result,
        candidate:{commit:$commit,tree:$tree},
        timing:{startedAt:$startedAt,finishedAt:$finishedAt,elapsedSeconds:$elapsedSeconds},
        start:{mode:"from-scratch",checkpointReused:false,reusedCandidatePackage:true},
        execution:{stages:$stages,completedImports:$completedImports},
        resources:{sourceLogicalBytes:$logicalBytes,sourceEncodedBytes:$encodedBytes,bundleBytes:$bundleBytes},
        failure:(if $result == "failed" then {stage:$failureStage,reason:$failureReason} else null end),
        rerun:(if $result == "failed" then {
          minimumScope:"full-gate",
          startMode:"from-scratch",
          reason:"no authenticated cross-run migration checkpoint exists"
        } else null end),
        efficiency:{
          crossRunCheckpointAvailable:false,
          expensiveWorkExecuted:($logicalBytes > 0),
          crossRunWorkReused:false,
          repeatAssessmentRequiresPostRunReview:true,
          avoidableWasteRequiresPostRunReview:true,
          metrics:["elapsedSeconds","sourceLogicalBytes","sourceEncodedBytes","bundleBytes","completedImports"]
        }
      }
    ' >"$review_tmp"
  chmod 0600 "$review_tmp"
  mv "$review_tmp" "$run_dir/run-review.json"
  gate_review_result="$result"
}

safe_relative_path() {
  case "$1" in
    "" | /* | . | .. | ../* | */.. | */../* | *$'\n'* | *$'\r'* | *$'\t'*)
      return 1
      ;;
  esac
}

hideout_for_store() {
  local store="$1"
  shift
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout_binary" "$@"
}

lima() {
  LIMA_HOME="$lima_home" limactl "$@"
}

cleanup() {
  local exit_status=$?
  set +e
  if [ "$exit_status" -ne 0 ]; then
    if [ "${gate_review_started:-0}" -eq 1 ] &&
      [ "${gate_review_result:-}" != "failed" ]; then
      write_gate_run_review failed "unclassified command failure" || true
    fi
    retain_failure_diagnostics
  fi
  for store in \
    "${source_store:-}" "${safe_one_store:-}" "${safe_two_store:-}" \
    "${safe_three_store:-}" "${exact_store:-}" "${wrong_store:-}" \
    "${compat_store:-}"; do
    if [ -n "$store" ] && [ -n "${hideout_binary:-}" ] && [ -x "$hideout_binary" ]; then
      hideout_for_store "$store" daemon stop >/dev/null 2>&1 || true
    fi
  done
  if [ -n "${lima_home:-}" ] && [ -d "$lima_home" ]; then
    lima list -q 2>/dev/null |
      while IFS= read -r instance; do
        [ -z "$instance" ] || lima delete -f "$instance" >/dev/null 2>&1 || true
      done
    lima disk list --json 2>/dev/null | jq -r '.name // empty' |
      while IFS= read -r disk; do
        [ -z "$disk" ] || lima disk delete -f "$disk" >/dev/null 2>&1 || true
      done
  fi
  case "${scratch:-}" in
    "$scratch_parent"/hm.*)
      [ ! -d "$scratch" ] || find "$scratch" -depth -delete
      ;;
    "") ;;
    *)
      printf 'migration-lima: refusing unexpected scratch cleanup: %s\n' "$scratch" >&2
      ;;
  esac
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "migration-lima"
  fi
  exit "$exit_status"
}
trap cleanup EXIT

candidate_result="$(cd "$(dirname "$candidate_result")" && pwd -P)/$(basename "$candidate_result")"
[ -f "$candidate_result" ] && [ ! -L "$candidate_result" ] ||
  fail "candidate result is missing or unsafe"
jq -e '
  .schema == "hideout.release-package-candidate-pointer/v1" and
  .result == "passed" and
  .candidateAcceptance == true and
  .publicationStatus == "local-only" and
  .source.dirty == false and
  (.source.commit | test("^[a-f0-9]{40}$")) and
  (.source.tree | test("^[a-f0-9]{40}$")) and
  (.archiveSHA256 | test("^[a-f0-9]{64}$"))
' "$candidate_result" >/dev/null ||
  fail "candidate result is not an accepted clean package pointer"

candidate_root="$(dirname "$candidate_result")"
archive_relative="$(jq -er '.archive' "$candidate_result")"
safe_relative_path "$archive_relative" ||
  fail "candidate archive path is unsafe"
archive="$candidate_root/$archive_relative"
[ -f "$archive" ] && [ ! -L "$archive" ] ||
  fail "candidate archive is missing or unsafe"
archive_sha="$(jq -er '.archiveSHA256' "$candidate_result")"
[ "$(sha256_file "$archive")" = "$archive_sha" ] ||
  fail "candidate archive digest mismatch"
if tar -tzf "$archive" | awk '
  /^\// || /(^|\/)\.\.($|\/)/ || NF == 0 {bad=1}
  END {exit bad ? 0 : 1}
'; then
  fail "candidate archive contains an unsafe entry"
fi

if [ -L "$out" ]; then fail "evidence directory must not be a symlink"; fi
mkdir -p "$out"
out="$(cd "$out" && pwd -P)"
chmod 0700 "$out"
run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out/$run_id"
mkdir -p "$run_dir"
chmod 0700 "$run_dir"
candidate_commit="$(jq -er '.source.commit' "$candidate_result")"
candidate_tree="$(jq -er '.source.tree' "$candidate_result")"
gate_review_started_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
gate_review_started_epoch="$(date +%s)"
gate_review_started=1
mark_gate_stage scratch-preflight

tmp_base="${HIDEOUT_MIGRATION_LIMA_TMPDIR:-/tmp}"
mkdir -p "$tmp_base"
scratch_parent="$(CDPATH='' cd -- "$tmp_base" && pwd -P)"
scratch="$(mktemp -d "$scratch_parent/hm.XXXXXX")"
scratch="$(CDPATH='' cd -- "$scratch" && pwd -P)"
chmod 0700 "$scratch"
scratch_supports_daemon_sockets "$scratch" ||
  fail "private scratch root is too long for daemon sockets: $scratch"
scratch_supports_lima_sockets "$scratch" ||
  fail "private scratch root is too long for imported Lima sockets: $scratch"
lima_home="$scratch/lima"
source_store="$scratch/source-store"
safe_one_store="$scratch/safe-one-store"
safe_two_store="$scratch/safe-two-store"
safe_three_store="$scratch/safe-three-store"
exact_store="$scratch/exact-store"
prefix="$scratch/prefix"
source_workspace="$scratch/source-workspace"
safe_one_workspace="$scratch/safe-one-workspace"
safe_two_workspace="$scratch/safe-two-workspace"
safe_three_workspace="$scratch/safe-three-workspace"
exact_workspace="$scratch/exact-workspace"
package_extract="$scratch/package"
passphrase_file="$scratch/passphrase"
mkdir -p \
  "$lima_home" "$source_workspace" "$safe_one_workspace" \
  "$safe_two_workspace" "$safe_three_workspace" "$exact_workspace" \
  "$package_extract"
chmod 0700 "$lima_home" "$source_workspace" "$safe_one_workspace" \
  "$safe_two_workspace" "$safe_three_workspace" "$exact_workspace" \
  "$package_extract"
openssl rand -hex 32 >"$passphrase_file"
chmod 0600 "$passphrase_file"

mark_gate_stage candidate-install
tar -xzf "$archive" -C "$package_extract"
package_root="$package_extract/hideout"
[ -x "$package_root/bin/hideout" ] && [ -x "$package_root/install.sh" ] ||
  fail "candidate archive does not contain the canonical package root"
"$package_root/bin/hideout" package verify "$package_root" \
  >"$scratch/package-verify.log" 2>&1 ||
  fail "candidate package verification failed"
install_candidate_package \
  "$package_root" "$prefix" "$source_store" "$lima_home" \
  "$scratch/install.log" ||
  fail "candidate installation failed"
hideout_binary="$prefix/bin/hideout"
[ -x "$hideout_binary" ] || fail "installed candidate binary is missing"
installed_sha="$(sha256_file "$hideout_binary")"
[ "$installed_sha" = "$(sha256_file "$package_root/bin/hideout")" ] ||
  fail "installed binary differs from the accepted package"

mark_gate_stage source-preparation
for store in \
  "$safe_one_store" "$safe_two_store" "$safe_three_store" "$exact_store"; do
  hideout_for_store "$store" init --no-input --profile default --template dev \
    --backend lima --network direct >"$scratch/init-$(basename "$store").log" 2>&1 ||
    fail "initialize independent destination store $(basename "$store")"
done

source_name="migration-source"
source_disk="migration-attached"
root_canary="hideout-migration-root-fidelity-v1"
attached_canary="hideout-migration-attached-fidelity-v1"
host_canary="hideout-host-workspace-must-not-migrate-v1"
printf '%s\n' "$host_canary" >"$source_workspace/host-only.txt"
hideout_for_store "$source_store" env create "$source_name" \
  --workspace "$source_workspace" --profile default --backend lima \
  >"$scratch/source-create.log" 2>&1 ||
  fail "create source environment"
# shellcheck disable=SC2016 # evaluated by the guest shell
hideout_for_store "$source_store" run --env "$source_name" \
  --workspace "$source_workspace" --terminal never -- \
  sh -c 'printf "%s\n" "$1" > /home/developer/migration-root-proof; sync' \
  hideout-migration-root "$root_canary" \
  >"$scratch/source-root-write.log" 2>&1 ||
  fail "write source root-disk sentinel"
hideout_for_store "$source_store" env inspect "$source_name" \
  >"$scratch/source-inspect.txt" 2>&1 ||
  fail "inspect source environment"
source_environment_id="$(awk '/^  id: / {print $2}' "$scratch/source-inspect.txt")"
source_instance="$(awk '/^  instance: / {print $2}' "$scratch/source-inspect.txt")"
[ -n "$source_environment_id" ] && [ -n "$source_instance" ] ||
  fail "source environment identity is incomplete"
hideout_for_store "$source_store" stop "$source_name" \
  >"$scratch/source-stop-before-disk.log" 2>&1 ||
  fail "stop source before attaching disk"
lima disk create "$source_disk" --size 1GiB --format qcow2 \
  >"$scratch/disk-create.log" 2>&1 ||
  fail "create attached Lima disk"
lima edit --tty=false \
  --set ".additionalDisks = [\"$source_disk\"]" "$source_instance" \
  >"$scratch/disk-attach.log" 2>&1 ||
  fail "attach Lima disk to source"
lima start "$source_instance" >"$scratch/source-start-attached.log" 2>&1 ||
  fail "start source with attached disk"
source_ssh_config="$(
  lima list --format '{{.SSHConfigFile}}' "$source_instance"
)"
case "$source_ssh_config" in
  "$lima_home/$source_instance"/*) ;;
  *) fail "source root-control SSH config escaped the isolated Lima home" ;;
esac
[ -f "$source_ssh_config" ] && [ ! -L "$source_ssh_config" ] ||
  fail "source root-control SSH config is missing or unsafe"
if ! ssh \
  -F "$source_ssh_config" \
  -o BatchMode=yes \
  -o User=root \
  -o ControlMaster=no \
  -o ControlPath=none \
  -o ConnectionAttempts=1 \
  -o ConnectTimeout=15 \
  "lima-$source_instance" -- sh -s -- "$source_disk" "$attached_canary" \
  >"$scratch/source-attached-write.log" 2>&1 <<'ROOTSH'
  set -eu
  path="/mnt/lima-$1"
  count=0
  while [ ! -d "$path" ] && [ "$count" -lt 120 ]; do
    sleep 1
    count=$((count + 1))
  done
  [ -d "$path" ]
  printf "%s\n" "$2" >"$path/migration-attached-proof"
  chmod 0644 "$path/migration-attached-proof"
  sync
ROOTSH
then
  fail "write attached-disk sentinel"
fi
if ! lima shell --tty=false "$source_instance" -- cat /etc/machine-id \
  >"$scratch/source-machine-id.txt" 2>&1; then
  fail "read source guest machine identity"
fi
# shellcheck disable=SC2016 # evaluated by the guest shell
lima shell --tty=false "$source_instance" -- sh -c \
  'set -eu; set -- /etc/ssh/ssh_host_*_key.pub; [ -e "$1" ]; cat "$@" | sha256sum | sed "s/ .*//"' \
  >"$scratch/source-ssh-digest.txt" 2>&1 ||
  fail "read source guest SSH identity"
source_machine_id="$(tr -d '[:space:]' <"$scratch/source-machine-id.txt")"
source_ssh_digest="$(tr -d '[:space:]' <"$scratch/source-ssh-digest.txt")"
if ! printf '%s\n' "$source_machine_id" | grep -Eq '^[a-f0-9]{32}$' ||
  ! printf '%s\n' "$source_ssh_digest" | grep -Eq '^[a-f0-9]{64}$'; then
  fail "observe source guest identity"
fi
lima stop "$source_instance" >"$scratch/source-stop-final.log" 2>&1 ||
  fail "stop source for export"

source_instance_dir="$(
  lima list --format json --all-fields |
    jq -sr --arg name "$source_instance" '.[] | select(.name == $name) | .dir'
)"
source_disk_dir="$(
  lima disk list --json |
    jq -sr --arg name "$source_disk" '.[] | select(.name == $name) | .dir'
)"
case "$source_instance_dir" in
  "$lima_home"/*) ;;
  *) fail "source instance directory escaped the isolated Lima home" ;;
esac
case "$source_disk_dir" in
  "$lima_home"/_disks/*) ;;
  *) fail "source disk directory escaped the isolated Lima home" ;;
esac
source_root_path="$source_instance_dir/disk"
source_attached_path="$source_disk_dir/datadisk"
source_record_path="$source_store/environments/$source_environment_id/environment.json"
for path in "$source_root_path" "$source_attached_path" "$source_record_path"; do
  [ -f "$path" ] && [ ! -L "$path" ] ||
    fail "source fidelity path is missing or unsafe: $path"
done
root_sha_before="$(sha256_file "$source_root_path")"
attached_sha_before="$(sha256_file "$source_attached_path")"
record_sha_before="$(sha256_file "$source_record_path")"

bundle="$scratch/source.hideout-migration"
export_log="$scratch/export.log"
mark_gate_stage source-export
hideout_for_store "$source_store" migrate export \
  --environment "$source_name" --out "$bundle" --ack-guest-content \
  --passphrase-stdin --yes --idempotency-key migration-export-gate-0001 \
  <"$passphrase_file" >"$export_log" 2>&1 ||
  fail "start full migration export"
export_operation="$(
  sed -n 's/^Migration operation \([^ ]*\) accepted.*/\1/p' "$export_log" |
    tail -1
)"
[ -n "$export_operation" ] || fail "export operation identity is missing"

wait_migration() {
  local store="$1" operation="$2" kind="$3" status_path="$4"
  local started now state
  started="$(date +%s)"
  while :; do
    if hideout_for_store "$store" migrate status "$operation" --json \
      >"$status_path.tmp" 2>"$status_path.err"; then
      mv "$status_path.tmp" "$status_path"
      state="$(jq -er '.state' "$status_path")"
      case "$state" in
        complete)
          jq -e --arg operation "$operation" --arg kind "$kind" '
            .operationId == $operation and
            .kind == $kind and
            .state == "complete" and
            .terminalReceipt != null and
            .terminalReceipt.terminalState == "complete" and
            .terminalReceipt.allEffectsSucceeded == true and
            .terminalReceipt.claimsReleased == true
          ' "$status_path" >/dev/null || return 1
          return 0
          ;;
        cancelled | rolled-back | failed | recoverable-failure)
          return 1
          ;;
      esac
    fi
    now="$(date +%s)"
    [ $((now - started)) -lt "$timeout_seconds" ] || return 1
    sleep 1
  done
}

wait_migration "$source_store" "$export_operation" export \
  "$scratch/export-status.json" ||
  fail "export did not reach a verified complete terminal state"
[ -f "$bundle" ] && [ ! -L "$bundle" ] && [ "$(file_mode "$bundle")" = "600" ] ||
  fail "completed bundle is missing, linked, or not owner-only"
bundle_sha_before="$(sha256_file "$bundle")"
if grep -aFq "$root_canary" "$bundle" || grep -aFq "$attached_canary" "$bundle"; then
  fail "encrypted bundle exposes a plaintext guest sentinel"
fi

mark_gate_stage bundle-negative-and-compatibility
inspect_log="$scratch/inspect.json"
hideout_for_store "$safe_one_store" migrate inspect "$bundle" \
  --passphrase-stdin --json <"$passphrase_file" >"$inspect_log" 2>&1 ||
  fail "inspect authenticated migration bundle"
jq -e '
  .inventory.sealed == true and
  (.inventory.environments | length) == 1 and
  (.inventory.disks | length) == 2 and
  (.inventory.disks | map(.role) | sort) == ["attached","root"] and
  (.inventory.excludedClasses | index("host-workspace-content")) != null
' "$inspect_log" >/dev/null ||
  fail "authenticated bundle inventory is incomplete"
source_ref="$(jq -er '.inventory.environments[0].sourceRef' "$inspect_log")"
network_authority_ref="$(
  migration_network_authority_ref "$inspect_log" "$source_ref"
)" || fail "authenticated bundle has no single direct-network authority proposal"
authority_proposal_count="$(
  jq -er '.inventory.authorityProposals | length | select(. >= 1)' "$inspect_log"
)" || fail "authenticated bundle authority inventory is invalid"

wrong_store="$scratch/wrong-pass-store"
hideout_for_store "$wrong_store" init --no-input --profile default --template dev \
  --backend lima --network direct >"$scratch/wrong-init.log" 2>&1 ||
  fail "initialize wrong-pass destination"
printf '%s\n' 'not-the-bundle-passphrase' >"$scratch/wrong-passphrase"
chmod 0600 "$scratch/wrong-passphrase"
if hideout_for_store "$wrong_store" migrate inspect "$bundle" \
  --passphrase-stdin --json <"$scratch/wrong-passphrase" \
  >"$scratch/wrong-inspect.out" 2>"$scratch/wrong-inspect.err"; then
  fail "wrong passphrase unlocked the migration bundle"
fi
if find "$wrong_store/environments" -mindepth 1 -print -quit 2>/dev/null |
  grep -q .; then
  fail "wrong-passphrase inspection created destination state"
fi
hideout_for_store "$wrong_store" daemon stop >/dev/null 2>&1 || true

# Exercise a release-shaped compatibility refusal with the exact candidate bytes
# in an isolated copy of the installed prefix. Removing only the package-owned
# zero-network VZ executor makes full import unprovable while leaving config-only
# mechanics and the accepted installation untouched. The request must fail before
# an operation or destination environment is created.
compat_store="$scratch/incompatible-store"
compat_prefix="$scratch/incompatible-prefix"
cp -R "$prefix" "$compat_prefix"
compat_binary="$compat_prefix/bin/hideout"
compat_executor="$compat_prefix/bin/hideout-migration-vz-adopt-darwin-arm64"
[ -x "$compat_binary" ] && [ -x "$compat_executor" ] ||
  fail "compatibility fixture is missing candidate executables"
HIDEOUT_STORE_ROOT="$compat_store" LIMA_HOME="$lima_home" \
  "$compat_binary" init --no-input --profile default --template dev \
  --backend lima --network direct >"$scratch/compat-init.log" 2>&1 ||
  fail "initialize incompatible destination store"
HIDEOUT_STORE_ROOT="$compat_store" LIMA_HOME="$lima_home" \
  "$compat_binary" daemon stop >/dev/null 2>&1 || true
mv "$compat_executor" "$compat_executor.unavailable"
if HIDEOUT_STORE_ROOT="$compat_store" LIMA_HOME="$lima_home" \
  "$compat_binary" migrate import "$bundle" --all \
  --passphrase-stdin --yes --idempotency-key migration-import-incompatible-0001 \
  <"$passphrase_file" >"$scratch/compat-import.log" 2>&1; then
  fail "full import was accepted without the package-owned zero-network executor"
fi
grep -Fq 'migration.capability.unavailable' "$scratch/compat-import.log" ||
  fail "incompatible full import did not report the stable capability code"
if find "$compat_store/migration/operations" -type f -print -quit 2>/dev/null |
  grep -q .; then
  fail "incompatible full import created a migration operation"
fi
if find "$compat_store/environments" -mindepth 1 -print -quit 2>/dev/null |
  grep -q .; then
  fail "incompatible full import created a destination environment"
fi
HIDEOUT_STORE_ROOT="$compat_store" LIMA_HOME="$lima_home" \
  "$compat_binary" daemon stop >/dev/null 2>&1 || true

start_import() {
  local store="$1" label="$2" policy="$3"
  local import_log="$scratch/import-$label.log"
  local operation
  if [ "$policy" = "exact" ]; then
    hideout_for_store "$store" migrate import "$bundle" --all \
      --policy "$source_ref=exact-guest-restore" \
      --ack migration.identity.exact_guest_restore_collision \
      --approve "$network_authority_ref" \
      --passphrase-stdin --yes --idempotency-key "migration-import-$label-0001" \
      <"$passphrase_file" >"$import_log" 2>&1 ||
      return 1
  else
    hideout_for_store "$store" migrate import "$bundle" --all \
      --approve "$network_authority_ref" \
      --passphrase-stdin --yes --idempotency-key "migration-import-$label-0001" \
      <"$passphrase_file" >"$import_log" 2>&1 ||
      return 1
  fi
  operation="$(
    sed -n 's/^Migration operation \([^ ]*\) accepted.*/\1/p' "$import_log" |
      tail -1
  )"
  [ -n "$operation" ] || return 1
  printf '%s\n' "$operation"
}

import_bundle() {
  local store="$1" label="$2" policy="$3"
  local operation
  operation="$(start_import "$store" "$label" "$policy")" || return 1
  wait_migration \
    "$store" "$operation" import "$scratch/import-$label-status.json" || return 1
  migration_network_authority_receipt \
    "$scratch/import-$label-status.json" "$network_authority_ref" \
    "$source_ref" "$authority_proposal_count" || return 1
  migration_identity_policy_receipt \
    "$scratch/import-$label-status.json" "$policy"
}

all_distinct() {
  local values=("$@")
  local left right
  for ((left = 0; left < ${#values[@]}; left++)); do
    [ -n "${values[$left]}" ] || return 1
    for ((right = left + 1; right < ${#values[@]}; right++)); do
      [ "${values[$left]}" != "${values[$right]}" ] || return 1
    done
  done
}

daemon_instance_for_store() {
  local store="$1" label="$2"
  local status_path="$scratch/daemon-$label.json"
  hideout_for_store "$store" daemon status >"$status_path" 2>&1 || return 1
  jq -er '
    select(.version == "hideout.daemon-status/v1" and .state == "serving") |
    .instanceId | select(type == "string" and length > 0)
  ' "$status_path"
}

kill_daemon_at_phase() {
  local store="$1" operation="$2" label="$3" phase="$4"
  local operation_path="$store/migration/operations/$operation.json"
  local socket="$store/daemon/hideoutd.sock"
  local instance owner_pids pid attempt
  instance="$(daemon_instance_for_store "$store" "$label-before-cut")" || return 1
  [ "$(jq -er '.phase' "$operation_path")" = "$phase" ] || return 1
  owner_pids="$(lsof -n -t -- "$socket" 2>/dev/null | LC_ALL=C sort -u || true)"
  case "$owner_pids" in
    "" | *$'\n'*) return 1 ;;
  esac
  pid="$owner_pids"
  case "$pid" in
    *[!0-9]*) return 1 ;;
  esac
  [ "$pid" -ne "$$" ] && kill -0 "$pid" 2>/dev/null || return 1
  kill -KILL "$pid" 2>/dev/null || return 1
  for ((attempt = 0; attempt < 200; attempt++)); do
    if ! lsof -n -t -- "$socket" >/dev/null 2>&1; then
      printf '%s\n' "$instance"
      return 0
    fi
    sleep 0.05
  done
  return 1
}

crash_daemon_after_adoption_response() {
  local store="$1" operation="$2" label="$3"
  local operation_path="$store/migration/operations/$operation.json"
  local socket="$store/daemon/hideoutd.sock"
  local executor="$prefix/bin/hideout-migration-vz-adopt-darwin-arm64"
  local stage environment control response receipt
  local instance owner_pids pid child_pids attempt started now
  local response_sha stopped=0 ready=0

  instance="$(daemon_instance_for_store "$store" "$label-before-cut")" || return 1
  [ "$(jq -er '.phase' "$operation_path")" = "adopting" ] || return 1
  stage="$(jq -er '
    .destinationStage.stageHandle |
    select(type == "string" and test("^stage_[a-z0-9_-]{8,120}$"))
  ' "$operation_path")" || return 1
  environment="$(jq -er '
    .identityActions |
    select(length == 1) | .[0].sourceRef |
    select(type == "string" and test("^[a-z0-9][a-z0-9_-]{7,127}$"))
  ' "$operation_path")" || return 1
  control="$lima_home/_hideout-migration/stages/$stage/adoption/$environment/control"
  response="$control/executor-response.json"
  receipt="$control/receipt/receipt.json"
  [ -x "$executor" ] || return 1

  owner_pids="$(lsof -n -t -- "$socket" 2>/dev/null | LC_ALL=C sort -u || true)"
  case "$owner_pids" in
    "" | *$'\n'*) return 1 ;;
  esac
  pid="$owner_pids"
  case "$pid" in
    *[!0-9]*) return 1 ;;
  esac
  [ "$pid" -ne "$$" ] && kill -0 "$pid" 2>/dev/null || return 1

  started="$(date +%s)"
  while :; do
    child_pids="$(
      ps -axo pid=,ppid=,command= |
        awk -v parent="$pid" -v executable="$executor" \
          '$2 == parent && $3 == executable { print $1 }'
    )"
    case "$child_pids" in
      *$'\n'*) return 1 ;;
      "") ;;
      *) break ;;
    esac
    [ "$(jq -er '.phase' "$operation_path" 2>/dev/null || true)" = "adopting" ] ||
      return 1
    kill -0 "$pid" 2>/dev/null || return 1
    now="$(date +%s)"
    [ $((now - started)) -lt "$timeout_seconds" ] || return 1
    sleep 0.01
  done

  kill -STOP "$pid" 2>/dev/null || return 1
  stopped=1
  if [ "$(jq -er '.phase' "$operation_path" 2>/dev/null || true)" != "adopting" ]; then
    kill -KILL "$pid" 2>/dev/null || true
    return 1
  fi

  while :; do
    if jq -e '
      .schema == "hideout.migration-vz-adopt-response/v1" and
      .started == true and .stopped == true and
      .networkDeviceCount == 0 and .receiptObserved == true and
      .stopReason == "receipt-and-guest-shutdown" and
      (.executionNonce | type == "string") and
      (.shutdownProof | test("^sha256:[a-f0-9]{64}$"))
    ' "$response" >/dev/null 2>&1 &&
      jq -e --arg operation "$operation" --arg environment "$environment" '
        .schema == "hideout.migration-adoption-receipt/v1" and
        .operationId == $operation and .environmentRef == $environment and
        .status == "completed" and .completionMarker == true and
        (.postIdentity | type == "object") and
        all(.actionResults[]; .status == "completed")
      ' "$receipt" >/dev/null 2>&1; then
      response_sha="$(sha256_file "$response")"
      sleep 0.05
      if [ "$response_sha" = "$(sha256_file "$response" 2>/dev/null || true)" ]; then
        ready=1
        break
      fi
    fi
    kill -0 "$pid" 2>/dev/null || break
    now="$(date +%s)"
    [ $((now - started)) -lt "$timeout_seconds" ] || break
    sleep 0.05
  done

  if [ "$ready" -ne 1 ] || [ "$stopped" -ne 1 ]; then
    kill -KILL "$pid" 2>/dev/null || true
    return 1
  fi
  [ "$(jq -er '.phase' "$operation_path" 2>/dev/null || true)" = "adopting" ] || {
    kill -KILL "$pid" 2>/dev/null || true
    return 1
  }
  kill -KILL "$pid" 2>/dev/null || return 1
  for ((attempt = 0; attempt < 200; attempt++)); do
    if ! lsof -n -t -- "$socket" >/dev/null 2>&1; then
      printf '%s\n' "$instance"
      return 0
    fi
    sleep 0.05
  done
  return 1
}

wait_for_resume_action() {
  local store="$1" operation="$2" status_path="$3"
  local started now
  started="$(date +%s)"
  while :; do
    if hideout_for_store "$store" migrate status "$operation" --json \
      >"$status_path.tmp" 2>"$status_path.err"; then
      mv "$status_path.tmp" "$status_path"
      if jq -e '
        .state == "recoverable-failure" and
        .recovery.required == true and
        .recovery.allowedActions == ["resume"]
      ' "$status_path" >/dev/null; then
        return 0
      fi
      jq -e '.state == "complete" or .state == "cancelled" or
        .state == "rolled-back" or .state == "failed"' \
        "$status_path" >/dev/null && return 1
    fi
    now="$(date +%s)"
    [ $((now - started)) -lt "$timeout_seconds" ] || return 1
    sleep 0.1
  done
}

verify_import() {
  local store="$1" workspace="$2" label="$3"
  local inspect_path="$scratch/environment-$label.txt"
  local run_path="$scratch/verify-$label.txt"
  local inventory_path="$scratch/lima-inventory-$label.json"
  local inventory_error_path="$scratch/lima-inventory-$label.err"
  local environment_id instance inspected_profile destination_profile profile_path
  local machine ssh_digest
  hideout_for_store "$store" env inspect "$source_name" >"$inspect_path" 2>&1 ||
    return 1
  environment_id="$(awk '/^  id: / {print $2}' "$inspect_path")"
  instance="$(awk '/^  instance: / {print $2}' "$inspect_path")"
  inspected_profile="$(migration_inspected_profile "$inspect_path")" || return 1
  [ -n "$environment_id" ] && [ -n "$instance" ] &&
    [ -n "$inspected_profile" ] || return 1
  lima list --format json --all-fields \
    >"$inventory_path" 2>"$inventory_error_path" || return 1
  migration_lima_stopped_inventory "$inventory_path" "$instance" || return 1
  destination_profile="$(migration_destination_profile \
    "$store" "$environment_id" "$source_name")" || return 1
  [ "$destination_profile" = "$inspected_profile" ] || return 1
  profile_path="$store/profiles/$destination_profile/profile.json"
  migration_guest_profile_authorized "$profile_path" || return 1
  # shellcheck disable=SC2016 # evaluated by the guest shell
  hideout_for_store "$store" run \
    --profile "$destination_profile" --env "$source_name" \
    --workspace "$workspace" --terminal never -- \
    sh -c "$migration_guest_verify_script" hideout-migration-verify \
    >"$run_path" 2>&1 || return 1
  grep -Fq "root=$root_canary" "$run_path" || return 1
  grep -Fq "attached=$attached_canary" "$run_path" || return 1
  if grep -Fq "$host_canary" "$run_path"; then return 1; fi
  machine="$(sed -n 's/^machine=//p' "$run_path" | tail -1 | tr -d '[:space:]')"
  ssh_digest="$(sed -n 's/^ssh=//p' "$run_path" | tail -1 | tr -d '[:space:]')"
  printf '%s\n' "$machine" | grep -Eq '^[a-f0-9]{32}$' || return 1
  printf '%s\n' "$ssh_digest" | grep -Eq '^[a-f0-9]{64}$' || return 1
  printf '%s\n%s\n%s\n%s\n' "$environment_id" "$instance" "$machine" "$ssh_digest" \
    >"$scratch/identity-$label"
  hideout_for_store "$store" stop "$source_name" \
    >"$scratch/stop-$label.log" 2>&1 || return 1
}

mark_gate_stage safe-one-import-and-verify
import_bundle "$safe_one_store" safe-one safe || fail "first Safe Clone import"
verify_import "$safe_one_store" "$safe_one_workspace" safe-one ||
  fail "verify first Safe Clone destination"
mark_gate_stage safe-two-import-and-verify
import_bundle "$safe_two_store" safe-two safe || fail "second Safe Clone import"
verify_import "$safe_two_store" "$safe_two_workspace" safe-two ||
  fail "verify second Safe Clone destination"
mark_gate_stage safe-three-crash-recovery
safe_three_operation="$(start_import "$safe_three_store" safe-three safe)" ||
  fail "start third Safe Clone import"
wait_operation_phase "$safe_three_store" "$safe_three_operation" materializing ||
  fail "third Safe Clone never reached durable materialization"
first_crash_instance="$(
  kill_daemon_at_phase \
    "$safe_three_store" "$safe_three_operation" safe-three-materializing materializing
)" || fail "crash daemon during third Safe Clone materialization"
wait_for_resume_action \
  "$safe_three_store" "$safe_three_operation" \
  "$scratch/import-safe-three-after-materializing-crash.json" ||
  fail "materialization crash did not advertise protected resume"
hideout_for_store "$safe_three_store" migrate resume "$safe_three_operation" \
  --passphrase-stdin --json <"$passphrase_file" \
  >"$scratch/import-safe-three-resume.json" 2>&1 ||
  fail "resume third Safe Clone after materialization crash"
safe_three_resume_revision="$(jq -er '
  .revision | select(type == "number" and . >= 0 and floor == .)
' "$scratch/import-safe-three-resume.json")" ||
  fail "resumed third Safe Clone omitted its accepted revision"
wait_operation_phase \
  "$safe_three_store" "$safe_three_operation" adopting \
  "$safe_three_resume_revision" ||
  fail "resumed third Safe Clone never reached durable adoption"
second_crash_instance="$(
  crash_daemon_after_adoption_response \
    "$safe_three_store" "$safe_three_operation" safe-three-adopting
)" || fail "crash daemon after third Safe Clone durable adoption response"
wait_migration \
  "$safe_three_store" "$safe_three_operation" import \
  "$scratch/import-safe-three-status.json" ||
  fail "third Safe Clone did not recover after the adoption crash"
migration_network_authority_receipt \
  "$scratch/import-safe-three-status.json" "$network_authority_ref" \
  "$source_ref" "$authority_proposal_count" ||
  fail "third Safe Clone did not commit the reviewed network authority"
migration_identity_policy_receipt \
  "$scratch/import-safe-three-status.json" safe ||
  fail "third Safe Clone committed the wrong identity policy"
final_crash_instance="$(daemon_instance_for_store "$safe_three_store" safe-three-final)" ||
  fail "observe final daemon after crash recovery"
all_distinct "$first_crash_instance" "$second_crash_instance" "$final_crash_instance" ||
  fail "crash recovery reused a daemon instance identity"
verify_import "$safe_three_store" "$safe_three_workspace" safe-three ||
  fail "verify crash-recovered third Safe Clone destination"
mark_gate_stage exact-restore-import-and-verify
import_bundle "$exact_store" exact exact || fail "Exact Guest Restore import"
verify_import "$exact_store" "$exact_workspace" exact ||
  fail "verify Exact Guest Restore destination"

safe_one_environment_id="$(sed -n '1p' "$scratch/identity-safe-one")"
safe_one_instance="$(sed -n '2p' "$scratch/identity-safe-one")"
safe_one_machine_id="$(sed -n '3p' "$scratch/identity-safe-one")"
safe_one_ssh_digest="$(sed -n '4p' "$scratch/identity-safe-one")"
safe_two_environment_id="$(sed -n '1p' "$scratch/identity-safe-two")"
safe_two_instance="$(sed -n '2p' "$scratch/identity-safe-two")"
safe_two_machine_id="$(sed -n '3p' "$scratch/identity-safe-two")"
safe_two_ssh_digest="$(sed -n '4p' "$scratch/identity-safe-two")"
safe_three_environment_id="$(sed -n '1p' "$scratch/identity-safe-three")"
safe_three_instance="$(sed -n '2p' "$scratch/identity-safe-three")"
safe_three_machine_id="$(sed -n '3p' "$scratch/identity-safe-three")"
safe_three_ssh_digest="$(sed -n '4p' "$scratch/identity-safe-three")"
exact_environment_id="$(sed -n '1p' "$scratch/identity-exact")"
exact_instance="$(sed -n '2p' "$scratch/identity-exact")"
exact_machine_id="$(sed -n '3p' "$scratch/identity-exact")"
exact_ssh_digest="$(sed -n '4p' "$scratch/identity-exact")"

all_distinct \
  "$source_environment_id" "$safe_one_environment_id" \
  "$safe_two_environment_id" "$safe_three_environment_id" \
  "$exact_environment_id" ||
  fail "destination control identities were reused"
all_distinct \
  "$source_instance" "$safe_one_instance" "$safe_two_instance" \
  "$safe_three_instance" "$exact_instance" ||
  fail "destination backend identities were reused"
all_distinct \
  "$source_machine_id" "$safe_one_machine_id" "$safe_two_machine_id" \
  "$safe_three_machine_id" ||
  fail "Safe Clone did not create independent guest machine identity"
all_distinct \
  "$source_ssh_digest" "$safe_one_ssh_digest" "$safe_two_ssh_digest" \
  "$safe_three_ssh_digest" ||
  fail "Safe Clone did not create independent guest identity"
[ "$source_machine_id" = "$exact_machine_id" ] &&
  [ "$source_ssh_digest" = "$exact_ssh_digest" ] ||
  fail "Exact Guest Restore did not preserve guest identity"

mark_gate_stage final-fidelity-and-evidence
root_sha_after="$(sha256_file "$source_root_path")"
attached_sha_after="$(sha256_file "$source_attached_path")"
record_sha_after="$(sha256_file "$source_record_path")"
bundle_sha_after="$(sha256_file "$bundle")"
[ "$root_sha_before" = "$root_sha_after" ] &&
  [ "$attached_sha_before" = "$attached_sha_after" ] &&
  [ "$record_sha_before" = "$record_sha_after" ] ||
  fail "migration mutated source disks or environment declaration"
[ "$bundle_sha_before" = "$bundle_sha_after" ] ||
  fail "bundle changed while reused across destinations"

write_gate_run_review passed ""
evidence_log="$run_dir/gate.log"
{
  printf 'candidate=%s\n' "$archive_sha"
  printf 'installed=%s\n' "$installed_sha"
  printf 'bundle=%s bytes=%s\n' "$bundle_sha_before" "$(file_bytes "$bundle")"
  printf 'source-root-before=%s after=%s\n' \
    "$root_sha_before" "$root_sha_after"
  printf 'source-attached-before=%s after=%s\n' \
    "$attached_sha_before" "$attached_sha_after"
  printf 'source-record-before=%s after=%s\n' \
    "$record_sha_before" "$record_sha_after"
  printf 'safe-clone-destinations=3 exact-restore-destinations=1\n'
  printf 'network-authority=reviewed-proposal-approved disabled-imported-authority=retained\n'
  printf 'lima-inventory=all-four-imported-instances-stopped-and-error-free\n'
  printf 'crash-cuts=materializing,adopting daemon-restarts=2\n'
  printf 'compatibility-fixture=missing-zero-network-executor result=refused-before-operation\n'
} >"$evidence_log"
cp "$scratch/export-status.json" "$run_dir/export-terminal.json"
cp "$scratch/import-safe-one-status.json" "$run_dir/import-safe-one-terminal.json"
cp "$scratch/import-safe-two-status.json" "$run_dir/import-safe-two-terminal.json"
cp "$scratch/import-safe-three-status.json" "$run_dir/import-safe-three-terminal.json"
cp "$scratch/import-exact-status.json" "$run_dir/import-exact-terminal.json"
find "$run_dir" -type f -exec chmod 0600 {} +

artifact_lines="$scratch/evidence-artifacts.jsonl"
: >"$artifact_lines"
while IFS= read -r evidence_file; do
  jq -nc \
    --arg path "$(basename "$evidence_file")" \
    --arg sha256 "$(sha256_file "$evidence_file")" \
    --argjson bytes "$(file_bytes "$evidence_file")" '
      {path:$path,sha256:$sha256,bytes:$bytes,mode:"0600"}
    ' >>"$artifact_lines"
done < <(find "$run_dir" -type f | LC_ALL=C sort)
artifacts_json="$(jq -s . "$artifact_lines")"

source_control_digest="$(sha256_text "control:$source_environment_id")"
safe_one_control_digest="$(sha256_text "control:$safe_one_environment_id")"
safe_two_control_digest="$(sha256_text "control:$safe_two_environment_id")"
safe_three_control_digest="$(sha256_text "control:$safe_three_environment_id")"
exact_control_digest="$(sha256_text "control:$exact_environment_id")"
source_backend_digest="$(sha256_text "backend:$source_instance")"
safe_one_backend_digest="$(sha256_text "backend:$safe_one_instance")"
safe_two_backend_digest="$(sha256_text "backend:$safe_two_instance")"
safe_three_backend_digest="$(sha256_text "backend:$safe_three_instance")"
exact_backend_digest="$(sha256_text "backend:$exact_instance")"
source_guest_digest="$(guest_identity_digest "$source_machine_id" "$source_ssh_digest")"
safe_one_guest_digest="$(guest_identity_digest "$safe_one_machine_id" "$safe_one_ssh_digest")"
safe_two_guest_digest="$(guest_identity_digest "$safe_two_machine_id" "$safe_two_ssh_digest")"
safe_three_guest_digest="$(guest_identity_digest "$safe_three_machine_id" "$safe_three_ssh_digest")"
exact_guest_digest="$(guest_identity_digest "$exact_machine_id" "$exact_ssh_digest")"
first_crash_daemon_digest="$(sha256_text "daemon:$first_crash_instance")"
second_crash_daemon_digest="$(sha256_text "daemon:$second_crash_instance")"
final_crash_daemon_digest="$(sha256_text "daemon:$final_crash_instance")"
summary="$run_dir/summary.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$(jq -er '.source.commit' "$candidate_result")" \
  --arg tree "$(jq -er '.source.tree' "$candidate_result")" \
  --arg candidatePointerSHA256 "$(sha256_file "$candidate_result")" \
  --arg archiveSHA256 "$archive_sha" \
  --arg installedBinarySHA256 "$installed_sha" \
  --arg bundleSHA256 "$bundle_sha_before" \
  --argjson bundleBytes "$(file_bytes "$bundle")" \
  --arg sourceRootBeforeSHA256 "$root_sha_before" \
  --arg sourceRootAfterSHA256 "$root_sha_after" \
  --arg sourceAttachedBeforeSHA256 "$attached_sha_before" \
  --arg sourceAttachedAfterSHA256 "$attached_sha_after" \
  --arg sourceRecordBeforeSHA256 "$record_sha_before" \
  --arg sourceRecordAfterSHA256 "$record_sha_after" \
  --arg sourceControlDigest "$source_control_digest" \
  --arg safeOneControlDigest "$safe_one_control_digest" \
  --arg safeTwoControlDigest "$safe_two_control_digest" \
  --arg safeThreeControlDigest "$safe_three_control_digest" \
  --arg exactControlDigest "$exact_control_digest" \
  --arg sourceBackendDigest "$source_backend_digest" \
  --arg safeOneBackendDigest "$safe_one_backend_digest" \
  --arg safeTwoBackendDigest "$safe_two_backend_digest" \
  --arg safeThreeBackendDigest "$safe_three_backend_digest" \
  --arg exactBackendDigest "$exact_backend_digest" \
  --arg sourceGuestDigest "$source_guest_digest" \
  --arg safeOneGuestDigest "$safe_one_guest_digest" \
  --arg safeTwoGuestDigest "$safe_two_guest_digest" \
  --arg safeThreeGuestDigest "$safe_three_guest_digest" \
  --arg exactGuestDigest "$exact_guest_digest" \
  --arg firstCrashDaemonDigest "$first_crash_daemon_digest" \
  --arg secondCrashDaemonDigest "$second_crash_daemon_digest" \
  --arg finalCrashDaemonDigest "$final_crash_daemon_digest" \
  --argjson artifacts "$artifacts_json" '
  {
    schema:"hideout.migration-lima-evidence/v1",
    generatedAt:$generatedAt,
    result:"passed",
    candidateAcceptance:true,
    source:{commit:$commit,tree:$tree,dirty:false},
    candidate:{
      pointerSHA256:$candidatePointerSHA256,
      archiveSHA256:$archiveSHA256,
      installedBinarySHA256:$installedBinarySHA256
    },
    bundle:{sha256:$bundleSHA256,bytes:$bundleBytes,reusedDestinations:4},
    sourceImmutability:{
      rootDisk:{beforeSHA256:$sourceRootBeforeSHA256,afterSHA256:$sourceRootAfterSHA256},
      attachedDisk:{beforeSHA256:$sourceAttachedBeforeSHA256,afterSHA256:$sourceAttachedAfterSHA256},
      environmentRecord:{beforeSHA256:$sourceRecordBeforeSHA256,afterSHA256:$sourceRecordAfterSHA256}
    },
    identityEvidence:{
      control:{
        sourceDigest:$sourceControlDigest,
        destinationDigests:[
          $safeOneControlDigest,$safeTwoControlDigest,$safeThreeControlDigest,
          $exactControlDigest
        ]
      },
      backend:{
        sourceDigest:$sourceBackendDigest,
        destinationDigests:[
          $safeOneBackendDigest,$safeTwoBackendDigest,$safeThreeBackendDigest,
          $exactBackendDigest
        ]
      },
      guest:{
        sourceDigest:$sourceGuestDigest,
        safeCloneDigests:[
          $safeOneGuestDigest,$safeTwoGuestDigest,$safeThreeGuestDigest
        ],
        exactRestoreDigest:$exactGuestDigest
      }
    },
    crashRecovery:{
      cuts:[
        {phase:"materializing",daemonInstanceDigest:$firstCrashDaemonDigest},
        {phase:"adopting",daemonInstanceDigest:$secondCrashDaemonDigest}
      ],
      finalDaemonInstanceDigest:$finalCrashDaemonDigest,
      materializationRequiredProtectedResume:true,
      adoptionRestartedWithoutBundleSecret:true
    },
    compatibilityEvidence:{
      fixture:"missing-package-owned-zero-network-executor",
      errorCode:"migration.capability.unavailable",
      operationCreated:false,
      destinationEnvironmentCreated:false
    },
    checks:{
      packageCandidateInstalled:true,
      encryptedBundleSealed:true,
      rootDiskFidelity:true,
      attachedDiskFidelity:true,
      hostWorkspaceExcluded:true,
      sourceImmutable:true,
      wrongPassphraseNoDestinationEnvironment:true,
      incompatibleAdoptionExecutorRejectedBeforeEffects:true,
      terminalReceipts:true,
      limaInventoryStopped:true,
      networkAuthorityReapproved:true,
      sameBundleThreeSafeClones:true,
      freshControlIdentity:true,
      freshBackendIdentity:true,
      safeCloneGuestIdentityFresh:true,
      exactRestoreGuestIdentityPreserved:true,
      materializationCrashResumed:true,
      adoptionCrashRecovered:true,
      daemonIdentityFreshAcrossCrashRecovery:true
    },
    artifacts:$artifacts,
    limitations:[
      "This is a functional fidelity and identity-policy gate; it makes no performance claim.",
      "The transfer is exercised between independent stores on one physical macOS host."
    ]
  }
' >"$summary"
chmod 0600 "$summary"
validate_migration_lima_summary "$summary" ||
  fail "generated evidence is internally inconsistent"

summary_sha="$(sha256_file "$summary")"
pointer_tmp="$out/.result.$$.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$(jq -er '.source.commit' "$candidate_result")" \
  --arg tree "$(jq -er '.source.tree' "$candidate_result")" \
  --arg run "$run_id" \
  --arg summary "$run_id/summary.json" \
  --arg summarySHA256 "$summary_sha" '
  {
    schema:"hideout.migration-lima-pointer/v1",
    generatedAt:$generatedAt,
    result:"passed",
    candidateAcceptance:true,
    source:{commit:$commit,tree:$tree,dirty:false},
    run:$run,
    summary:$summary,
    summarySHA256:$summarySHA256
  }
' >"$pointer_tmp"
chmod 0600 "$pointer_tmp"
mv "$pointer_tmp" "$out/result.json"

package_identity="$scratch/package-identity.json"
proof_registry="$scratch/proof-registry.json"
"$hideout_binary" support release package-identity \
  --archive "$archive" --out "$package_identity" >/dev/null ||
  fail "candidate package identity could not be derived for migration proof"
"$hideout_binary" support proof-registry --json >"$proof_registry" ||
  fail "candidate proof registry could not be read"
write_migration_product_evidence \
  "$out/product-hardening-evidence.json" \
  "$summary" "$run_id/summary.json" \
  "$package_identity" "$proof_registry" ||
  fail "migration product evidence could not be assembled"
go run ./cmd/hideout-schema-validate \
  schemas/product-hardening-evidence.schema.json \
  "$out/product-hardening-evidence.json" >/dev/null ||
  fail "migration product evidence failed schema validation"
go run ./internal/productevidence/cmd/validate-046 \
  --commit "$candidate_commit" \
  --package-identity "$package_identity" \
  "$out/product-hardening-evidence.json" >/dev/null ||
  fail "migration product evidence failed semantic validation"

# shellcheck disable=SC2034 # consumed by the sourced EXIT guard
gate_completed=1
printf 'migration-lima: passed evidence=%s\n' "$summary"
