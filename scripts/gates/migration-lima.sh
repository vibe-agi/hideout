#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)"
cd "$repo_root"
# shellcheck source=scripts/lib/gate-result.sh
# shellcheck disable=SC1091
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0

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

retain_failure_diagnostics() {
  [ -n "${scratch:-}" ] && [ -d "$scratch" ] || return 0
  [ -n "${run_dir:-}" ] && [ -d "$run_dir" ] || return 0
  local diagnostic_dir=""
  local diagnostic_name source destination
  for diagnostic_name in package-verify.log install.log; do
    source="$scratch/$diagnostic_name"
    [ -f "$source" ] && [ ! -L "$source" ] || continue
    if [ -z "$diagnostic_dir" ]; then
      diagnostic_dir="$run_dir/diagnostics"
      mkdir -p "$diagnostic_dir"
      chmod 0700 "$diagnostic_dir"
    fi
    destination="$diagnostic_dir/$diagnostic_name"
    cp "$source" "$destination"
    chmod 0600 "$destination"
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

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/migration-lima.sh --candidate-result FILE [--out DIR]" \
    "       scripts/gates/migration-lima.sh --preflight" \
    "" \
    "Consumes an already accepted package candidate without rebuilding it, installs it" \
    "into a private prefix, and exercises one real stopped Lima source with root and" \
    "attached disks. The same encrypted bundle is imported into three independent Safe" \
    "Clone stores and one Exact Guest Restore store. The third Safe Clone is killed" \
    "during materialization and adoption to prove daemon-restart recovery." \
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
    (.artifacts | length) == 6 and
    ([.artifacts[].path] | unique | length) == 6 and
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

migration_lima_summary_fixture() {
  jq -nc '
    def digest($character): $character * 64;
    {
      schema:"hideout.migration-lima-evidence/v1",
      result:"passed",
      candidateAcceptance:true,
      candidate:{
        pointerSHA256:digest("a"),
        archiveSHA256:digest("b"),
        installedBinarySHA256:digest("c")
      },
      bundle:{sha256:digest("d"),reusedDestinations:4},
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
        sameBundleThreeSafeClones:true,
        freshControlIdentity:true,
        freshBackendIdentity:true,
        safeCloneGuestIdentityFresh:true,
        exactRestoreGuestIdentityPreserved:true,
        materializationCrashResumed:true,
        adoptionCrashRecovered:true,
        daemonIdentityFreshAcrossCrashRecovery:true
      },
      artifacts:[range(0;6) | {
        path:("artifact-" + tostring + ".json"),
        bytes:1,
        mode:"0600",
        sha256:digest((["5","6","7","8","9","a"][.] ))
      }]
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
  require_command jq
  require_command mktemp
  require_command shellcheck
  require_command stat
  bash -n scripts/gates/migration-lima.sh scripts/gates/migration.sh
  shellcheck scripts/gates/migration-lima.sh scripts/gates/migration.sh
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
  scratch_supports_daemon_sockets "/private/tmp/ho-mig.fixture" ||
    fail "short scratch fixture cannot host daemon sockets"
  long_scratch_fixture="/private/tmp/$(printf '%090d' 0)"
  if scratch_supports_daemon_sockets "$long_scratch_fixture"; then
    fail "long scratch fixture was accepted for daemon sockets"
  fi
  diagnostic_fixture="$(mktemp -d "${TMPDIR:-/tmp}/ho-mig-preflight.XXXXXX")"
  scratch="$diagnostic_fixture/scratch"
  run_dir="$diagnostic_fixture/evidence"
  mkdir -p "$scratch" "$run_dir"
  printf 'candidate install diagnostic fixture\n' >"$scratch/install.log"
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
  find "$diagnostic_fixture" -depth -delete
  scratch=""
  run_dir=""
  printf 'migration-lima: preflight=passed semantic-fixtures=9\n'
  exit 0
fi

for command in awk cp date find grep jq limactl lsof mktemp mv openssl sed shasum sort stat tail tar tr uname; do
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
    "$scratch_parent"/ho-mig.*)
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

tmp_base="${HIDEOUT_MIGRATION_LIMA_TMPDIR:-/tmp}"
mkdir -p "$tmp_base"
scratch_parent="$(CDPATH='' cd -- "$tmp_base" && pwd -P)"
scratch="$(mktemp -d "$scratch_parent/ho-mig.XXXXXX")"
scratch="$(CDPATH='' cd -- "$scratch" && pwd -P)"
chmod 0700 "$scratch"
scratch_supports_daemon_sockets "$scratch" ||
  fail "private scratch root is too long for daemon sockets: $scratch"
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

tar -xzf "$archive" -C "$package_extract"
package_root="$package_extract/hideout"
[ -x "$package_root/bin/hideout" ] && [ -x "$package_root/install.sh" ] ||
  fail "candidate archive does not contain the canonical package root"
"$package_root/bin/hideout" package verify "$package_root" \
  >"$scratch/package-verify.log" 2>&1 ||
  fail "candidate package verification failed"
"$package_root/install.sh" \
  --prefix "$prefix" --store "$source_store" --backend lima --network direct \
  >"$scratch/install.log" 2>&1 ||
  fail "candidate installation failed"
hideout_binary="$prefix/bin/hideout"
[ -x "$hideout_binary" ] || fail "installed candidate binary is missing"
installed_sha="$(sha256_file "$hideout_binary")"
[ "$installed_sha" = "$(sha256_file "$package_root/bin/hideout")" ] ||
  fail "installed binary differs from the accepted package"

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
# shellcheck disable=SC2016 # evaluated by the guest shell
lima shell --tty=false "$source_instance" -- sh -c '
  set -eu
  path="/mnt/lima-$1"
  count=0
  while [ ! -d "$path" ] && [ "$count" -lt 120 ]; do
    sleep 1
    count=$((count + 1))
  done
  [ -d "$path" ]
  printf "%s\n" "$2" >"$path/migration-attached-proof"
  sync
' hideout-migration-attached "$source_disk" "$attached_canary" \
  >"$scratch/source-attached-write.log" 2>&1 ||
  fail "write attached-disk sentinel"
source_machine_id="$(
  lima shell --tty=false "$source_instance" -- cat /etc/machine-id | tr -d '[:space:]'
)"
# shellcheck disable=SC2016 # evaluated by the guest shell
source_ssh_digest="$(
  lima shell --tty=false "$source_instance" -- sh -c \
    'set -eu; set -- /etc/ssh/ssh_host_*_key.pub; [ -e "$1" ]; cat "$@" | sha256sum | sed "s/ .*//"' |
    tr -d '[:space:]'
)"
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
      --passphrase-stdin --yes --idempotency-key "migration-import-$label-0001" \
      <"$passphrase_file" >"$import_log" 2>&1 ||
      return 1
  else
    hideout_for_store "$store" migrate import "$bundle" --all \
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
  wait_migration "$store" "$operation" import "$scratch/import-$label-status.json"
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

wait_operation_phase() {
  local store="$1" operation="$2" wanted="$3"
  local path="$store/migration/operations/$operation.json"
  local started now phase
  started="$(date +%s)"
  while :; do
    phase="$(jq -er '.phase' "$path" 2>/dev/null || true)"
    if [ "$phase" = "$wanted" ]; then
      return 0
    fi
    case "$phase" in
      complete | cancelled | rolled-back | failed | recoverable-failure)
        return 1
        ;;
    esac
    now="$(date +%s)"
    [ $((now - started)) -lt "$timeout_seconds" ] || return 1
    sleep 0.05
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

import_bundle "$safe_one_store" safe-one safe || fail "first Safe Clone import"
import_bundle "$safe_two_store" safe-two safe || fail "second Safe Clone import"
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
wait_operation_phase "$safe_three_store" "$safe_three_operation" adopting ||
  fail "resumed third Safe Clone never reached durable adoption"
second_crash_instance="$(
  kill_daemon_at_phase \
    "$safe_three_store" "$safe_three_operation" safe-three-adopting adopting
)" || fail "crash daemon during third Safe Clone adoption"
wait_migration \
  "$safe_three_store" "$safe_three_operation" import \
  "$scratch/import-safe-three-status.json" ||
  fail "third Safe Clone did not recover after the adoption crash"
final_crash_instance="$(daemon_instance_for_store "$safe_three_store" safe-three-final)" ||
  fail "observe final daemon after crash recovery"
all_distinct "$first_crash_instance" "$second_crash_instance" "$final_crash_instance" ||
  fail "crash recovery reused a daemon instance identity"
import_bundle "$exact_store" exact exact || fail "Exact Guest Restore import"
for status_path in \
  "$scratch/import-safe-one-status.json" \
  "$scratch/import-safe-two-status.json" \
  "$scratch/import-safe-three-status.json"; do
  jq -e '
    .terminalReceipt.identityPolicies == {
      safeClone:1,
      exactGuestRestore:0,
      freshControl:1,
      freshBackend:1
    }
  ' "$status_path" >/dev/null ||
    fail "Safe Clone terminal receipt has the wrong identity policy"
done
jq -e '
  .terminalReceipt.identityPolicies == {
    safeClone:0,
    exactGuestRestore:1,
    freshControl:1,
    freshBackend:1
  }
' "$scratch/import-exact-status.json" >/dev/null ||
  fail "Exact Guest Restore terminal receipt has the wrong identity policy"

verify_import() {
  local store="$1" workspace="$2" label="$3"
  local inspect_path="$scratch/environment-$label.txt"
  local run_path="$scratch/verify-$label.txt"
  hideout_for_store "$store" env inspect "$source_name" >"$inspect_path" 2>&1 ||
    return 1
  local environment_id instance
  environment_id="$(awk '/^  id: / {print $2}' "$inspect_path")"
  instance="$(awk '/^  instance: / {print $2}' "$inspect_path")"
  [ -n "$environment_id" ] && [ -n "$instance" ] || return 1
  # shellcheck disable=SC2016 # evaluated by the guest shell
  hideout_for_store "$store" run --env "$source_name" --workspace "$workspace" \
    --terminal never -- sh -c '
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
    ' hideout-migration-verify >"$run_path" 2>&1 || return 1
  grep -Fq "root=$root_canary" "$run_path" || return 1
  grep -Fq "attached=$attached_canary" "$run_path" || return 1
  if grep -Fq "$host_canary" "$run_path"; then return 1; fi
  machine="$(sed -n 's/^machine=//p' "$run_path" | tail -1 | tr -d '[:space:]')"
  ssh_digest="$(sed -n 's/^ssh=//p' "$run_path" | tail -1 | tr -d '[:space:]')"
  printf '%s\n' "$machine" | grep -Eq '^[a-f0-9]{32}$' || return 1
  printf '%s\n' "$ssh_digest" | grep -Eq '^[a-f0-9]{64}$' || return 1
  printf '%s\n%s\n%s\n%s\n' "$environment_id" "$instance" "$machine" "$ssh_digest" \
    >"$scratch/identity-$label"
}

verify_import "$safe_one_store" "$safe_one_workspace" safe-one ||
  fail "verify first Safe Clone destination"
verify_import "$safe_two_store" "$safe_two_workspace" safe-two ||
  fail "verify second Safe Clone destination"
verify_import "$safe_three_store" "$safe_three_workspace" safe-three ||
  fail "verify crash-recovered third Safe Clone destination"
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

for store in \
  "$safe_one_store" "$safe_two_store" "$safe_three_store" "$exact_store"; do
  hideout_for_store "$store" stop "$source_name" >/dev/null 2>&1 ||
    fail "stop imported environment in $(basename "$store")"
done
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

# shellcheck disable=SC2034 # consumed by the sourced EXIT guard
gate_completed=1
printf 'migration-lima: passed evidence=%s\n' "$summary"
