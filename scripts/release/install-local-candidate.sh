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
export HOMEBREW_NO_AUTO_UPDATE=1

artifact_root="${HIDEOUT_045_ARTIFACT_ROOT:-$repo_root/.artifacts/045}"
candidate_result="$artifact_root/package/result.json"
out="$artifact_root/local-install"
prefix=""
store=""
discard_authorized=0
preflight_only=0
tmp_base="${TMPDIR:-/tmp}"
tmp_base="${tmp_base%/}"

scratch=""
package_root=""
installed_binary=""
candidate_daemon_launcher_pid=""
legacy_daemon_launcher_pid=""
proxy_pid=""
http_pid=""
ui_pid=""
package_removed=0

usage() {
  printf '%s\n' \
    "Usage: scripts/release/install-local-candidate.sh [--preflight]" \
    "       [--candidate-result FILE] [--out DIR]" \
    "       [--prefix DIR] [--store DIR]" \
    "       --yes-discard-legacy-data" \
    "" \
    "Consumes the exact accepted package without rebuilding it, removes the" \
    "currently installed Hideout after an exact environment cleanup, discards" \
    "the current user's exact ~/.hideout store, and exercises setup, managed" \
    "secret, connection, proxied run, Help, TUI, WebUI, clean, same-candidate" \
    "update, normal uninstall, and final reinstall." \
    "" \
    "The final state is the exact standalone candidate installed at the active" \
    "Homebrew prefix, a fresh direct-network default profile, no environment," \
    "and a stopped daemon. This command never tags, pushes, edits a tap, creates" \
    "a remote release, or publishes package bytes."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --preflight)
      preflight_only=1
      shift
      ;;
    --candidate-result)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'local-install: --candidate-result requires a file\n' >&2
        exit 2
      }
      candidate_result="$2"
      shift 2
      ;;
    --out)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'local-install: --out requires a directory\n' >&2
        exit 2
      }
      out="$2"
      shift 2
      ;;
    --prefix)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'local-install: --prefix requires a directory\n' >&2
        exit 2
      }
      prefix="$2"
      shift 2
      ;;
    --store)
      [ "$#" -ge 2 ] && [ -n "${2:-}" ] || {
        printf 'local-install: --store requires a directory\n' >&2
        exit 2
      }
      store="$2"
      shift 2
      ;;
    --yes-discard-legacy-data)
      discard_authorized=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'local-install: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

fail() {
  printf 'local-install: %s\n' "$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'local-install: missing required command: %s\n' "$1" >&2
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
  local target="${1-}" scope="${2-}"
  if [ -z "$target" ] || [ ! -e "$target" ]; then
    return
  fi
  case "$target" in
    "$tmp_base"/"$scope".*)
      [ ! -L "$target" ] ||
        fail "refusing symlink cleanup target: $target"
      find "$target" -depth -delete
      ;;
    *)
      fail "refusing unexpected cleanup target: $target"
      ;;
  esac
}

stop_process() {
  local process_id="${1-}" signal_name="${2-TERM}"
  case "$process_id" in
    "" | *[!0-9]*)
      return
      ;;
  esac
  if kill -0 "$process_id" 2>/dev/null; then
    kill "-$signal_name" "$process_id" 2>/dev/null || true
  fi
  wait "$process_id" 2>/dev/null || true
}

cleanup() {
  local exit_status=$?
  set +e
  stop_process "${ui_pid:-}" INT
  stop_process "${proxy_pid:-}" TERM
  stop_process "${http_pid:-}" TERM
  if [ -n "${installed_binary:-}" ] &&
    [ -x "${installed_binary:-}" ]; then
    "$installed_binary" daemon stop >/dev/null 2>&1 || true
  fi
  stop_process "${candidate_daemon_launcher_pid:-}" TERM
  stop_process "${legacy_daemon_launcher_pid:-}" TERM
  if [ "${package_removed:-0}" -eq 1 ] &&
    [ -n "${package_root:-}" ] &&
    [ -x "${package_root:-}/install.sh" ] &&
    [ -n "${prefix:-}" ] &&
    [ -n "${store:-}" ]; then
    "$package_root/install.sh" \
      --prefix "$prefix" --store "$store" --skip-init \
      >/dev/null 2>&1 || true
  fi
  cleanup_tree "${scratch:-}" "hideout-local-install"
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "local-install"
  fi
  exit "$exit_status"
}

verify_sha256() {
  local file="$1" expected="$2"
  [ -f "$file" ] &&
    [ ! -L "$file" ] &&
    [[ "$expected" =~ ^[a-f0-9]{64}$ ]] &&
    [ "$(sha256_file "$file")" = "$expected" ]
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

daemon_status_matches_identity() {
  local status_file="$1" instance_id="$2" started_at="$3"
  daemon_status_is_serving "$status_file" &&
    jq -e \
      --arg instance "$instance_id" \
      --arg startedAt "$started_at" '
        .instanceId == $instance and
        .startedAt == $startedAt
      ' "$status_file" >/dev/null
}

validate_delete_scope() {
  local candidate_store="$1" expected_store="$2"
  [ "$candidate_store" = "$expected_store" ] &&
    [ "$candidate_store" != "/" ] &&
    [ "$candidate_store" != "$repo_root" ] &&
    [ ! -L "$candidate_store" ]
}

delete_exact_store() {
  local target="$1" expected="$2"
  validate_delete_scope "$target" "$expected" ||
    fail "refusing legacy-data discard outside exact current-user store"
  if [ -e "$target" ]; then
    [ -d "$target" ] ||
      fail "exact store exists but is not a directory"
    find "$target" -depth -delete
  fi
  [ ! -e "$target" ] && [ ! -L "$target" ] ||
    fail "exact store discard did not complete"
}

validate_archive_members() {
  local archive="$1" members="$2" entry normalized
  tar -tzf "$archive" >"$members"
  [ -s "$members" ] || {
    printf 'local-install: candidate archive has no members\n' >&2
    return 1
  }
  while IFS= read -r entry; do
    normalized="${entry%/}"
    case "$normalized" in
      hideout | hideout/*)
        ;;
      *)
        printf 'local-install: archive member escapes package root: %s\n' \
          "$entry" >&2
        return 1
        ;;
    esac
    case "$normalized" in
      *"/../"* | */.. | ../* | /* | *$'\n'* | *$'\r'* | *$'\t'*)
        printf 'local-install: archive member path is unsafe: %s\n' \
          "$entry" >&2
        return 1
        ;;
    esac
  done <"$members"
  [ -z "$(LC_ALL=C sort "$members" | uniq -d)" ] || {
    printf 'local-install: archive contains duplicate members\n' >&2
    return 1
  }
}

extract_package() {
  local archive="$1" destination="$2"
  validate_archive_members "$archive" "$scratch/archive-members.txt"
  mkdir "$destination"
  tar -xzf "$archive" -C "$destination"
  if [ ! -d "$destination/hideout" ] ||
    [ -L "$destination/hideout" ] ||
    find "$destination/hideout" ! -type f ! -type d -print -quit |
      grep -q .; then
    fail "extracted candidate package tree is unsafe"
  fi
  package_root="$destination/hideout"
}

validate_receipt() {
  local receipt="$1" commit="$2" tree="$3" version="$4"
  local archive_sha="$5" manifest_sha="$6" binary_sha="$7"
  local expected_prefix="$8" expected_store="$9"
  jq -e \
    --arg commit "$commit" \
    --arg tree "$tree" \
    --arg version "$version" \
    --arg archiveSHA256 "$archive_sha" \
    --arg manifestSHA256 "$manifest_sha" \
    --arg binarySHA256 "$binary_sha" \
    --arg prefix "$expected_prefix" \
    --arg store "$expected_store" '
      .schema == "hideout.local-install-candidate/v1" and
      .result == "passed" and
      .sourceCandidate == {commit:$commit,tree:$tree,dirty:false} and
      .candidateAcceptance == true and
      .candidate.version == $version and
      .candidate.archiveSHA256 == $archiveSHA256 and
      .candidate.packageManifestSHA256 == $manifestSHA256 and
      .candidate.installedBinarySHA256 == $binarySHA256 and
      .candidate.consumedWithoutRebuild == true and
      .installation.hostOS == "Darwin" and
      .installation.hostArch == "arm64" and
      .installation.prefix == $prefix and
      .installation.store == $store and
      .installation.legacyDataPolicy == "explicitly-discarded" and
      .installation.finalInstallation == "exact-standalone-candidate" and
      .installation.finalDaemonState == "stopped" and
      .installation.finalEnvironmentCount == 0 and
      (.checks | length) == 27 and
      all(.checks[]; . == true) and
      (.artifacts | length) >= 8 and
      all(.artifacts[];
        (.path | type == "string") and
        (.sha256 | test("^[a-f0-9]{64}$")) and
        (.bytes | type == "number") and
        .mode == "0600")
    ' "$receipt" >/dev/null
}

run_preflight() {
  local fixture_root fixture commit tree digest version prefix_fixture store_fixture
  local daemon_status_fixture
  preflight_root="$(
    mktemp -d "$tmp_base/hideout-local-install-preflight.XXXXXX"
  )"
  # Invoked indirectly by the EXIT trap.
  # shellcheck disable=SC2329
  cleanup_preflight() {
    local exit_status=$?
    cleanup_tree \
      "${preflight_root:-}" \
      "hideout-local-install-preflight"
    if [ "$exit_status" -eq 0 ]; then
      gate_require_completion "local-install-preflight"
    fi
  }
  trap cleanup_preflight EXIT
  fixture_root="$preflight_root/home"
  mkdir -p "$fixture_root/.hideout"
  store_fixture="$fixture_root/.hideout"
  prefix_fixture="/opt/homebrew"
  validate_delete_scope "$store_fixture" "$store_fixture" ||
    fail "exact store fixture was rejected"
  if validate_delete_scope "$fixture_root/.config" "$store_fixture"; then
    fail "different destructive store fixture was accepted"
  fi
  find "$store_fixture" -depth -delete
  ln -s "$fixture_root" "$store_fixture"
  if validate_delete_scope "$store_fixture" "$store_fixture"; then
    fail "symlink destructive store fixture was accepted"
  fi

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

  fixture="$preflight_root/receipt.json"
  commit="0123456789abcdef0123456789abcdef01234567"
  tree="89abcdef0123456789abcdef0123456789abcdef"
  digest="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  version="0.1.0-alpha.4"
  jq -n \
    --arg commit "$commit" \
    --arg tree "$tree" \
    --arg digest "$digest" \
    --arg version "$version" \
    --arg prefix "$prefix_fixture" \
    --arg store "$store_fixture" '
      {
        schema:"hideout.local-install-candidate/v1",
        generatedAt:"2026-07-31T00:00:00Z",
        result:"passed",
        sourceCandidate:{commit:$commit,tree:$tree,dirty:false},
        candidateAcceptance:true,
        candidate:{
          version:$version,
          archive:"run-fixture/candidate.tar.gz",
          archiveSHA256:$digest,
          packageManifestSHA256:$digest,
          installedBinarySHA256:$digest,
          consumedWithoutRebuild:true
        },
        installation:{
          hostOS:"Darwin",
          hostArch:"arm64",
          prefix:$prefix,
          store:$store,
          legacyDataPolicy:"explicitly-discarded",
          priorInstallation:"homebrew-0.1.0-alpha.3",
          finalInstallation:"exact-standalone-candidate",
          finalDaemonState:"stopped",
          finalEnvironmentCount:0
        },
        checks:{
          sourceClean:true,
          archiveDigestVerified:true,
          archiveSafetyVerified:true,
          packageVerified:true,
          packageIdentityVerified:true,
          legacyInstallRemoved:true,
          legacyDataDiscarded:true,
          unrelatedHomebrewPreserved:true,
          setupCompleted:true,
          daemonStarted:true,
          secretStoredWithoutRetention:true,
          connectionPlanned:true,
          connectionAppliedWithoutDaemonStop:true,
          runCompletedThroughProxy:true,
          helpJourneysRendered:true,
          tuiSnapshotRendered:true,
          tuiPTYExitedCleanly:true,
          webUIAuthenticated:true,
          environmentCleaned:true,
          sameCandidateUpdate:true,
          uninstallDryRun:true,
          uninstallPreservedStore:true,
          packageFilesAbsentAfterUninstall:true,
          finalReinstallExact:true,
          finalDaemonStopped:true,
          finalEnvironmentAbsent:true,
          noSecretValueInEvidence:true
        },
        artifacts:[
          {path:"run-fixture/a.json",sha256:$digest,bytes:1,mode:"0600"},
          {path:"run-fixture/b.json",sha256:$digest,bytes:1,mode:"0600"},
          {path:"run-fixture/c.json",sha256:$digest,bytes:1,mode:"0600"},
          {path:"run-fixture/d.json",sha256:$digest,bytes:1,mode:"0600"},
          {path:"run-fixture/e.json",sha256:$digest,bytes:1,mode:"0600"},
          {path:"run-fixture/f.json",sha256:$digest,bytes:1,mode:"0600"},
          {path:"run-fixture/g.json",sha256:$digest,bytes:1,mode:"0600"},
          {path:"run-fixture/h.json",sha256:$digest,bytes:1,mode:"0600"}
        ],
        limitations:["local-only candidate"]
      }
    ' >"$fixture"
  chmod 0600 "$fixture"
  validate_receipt \
    "$fixture" "$commit" "$tree" "$version" \
    "$digest" "$digest" "$digest" "$prefix_fixture" "$store_fixture" ||
    fail "valid local-install receipt fixture was rejected"
  jq '.checks.webUIAuthenticated = false' "$fixture" >"$fixture.mutated"
  if validate_receipt \
    "$fixture.mutated" "$commit" "$tree" "$version" \
    "$digest" "$digest" "$digest" "$prefix_fixture" "$store_fixture"; then
    fail "false local-install check was accepted"
  fi
  jq '.candidateAcceptance = false' "$fixture" >"$fixture.mutated"
  if validate_receipt \
    "$fixture.mutated" "$commit" "$tree" "$version" \
    "$digest" "$digest" "$digest" "$prefix_fixture" "$store_fixture"; then
    fail "unaccepted local candidate fixture was accepted"
  fi
  jq '.installation.finalEnvironmentCount = 1' \
    "$fixture" >"$fixture.mutated"
  if validate_receipt \
    "$fixture.mutated" "$commit" "$tree" "$version" \
    "$digest" "$digest" "$digest" "$prefix_fixture" "$store_fixture"; then
    fail "remaining environment fixture was accepted"
  fi
  gate_completed=1
  printf 'local-install: preflight=passed\n'
}

if [ "$preflight_only" -eq 1 ]; then
  for required_command in jq mktemp find; do
    require_command "$required_command" || exit 1
  done
  run_preflight
  exit 0
fi

[ "$discard_authorized" -eq 1 ] || {
  usage >&2
  fail "full run requires --yes-discard-legacy-data"
}

for required_command in awk bash brew cmp curl expect find git grep jq \
  mktemp python3 sed shasum stat tar uniq; do
  require_command "$required_command" || exit 1
done

[ "$(uname -s)" = "Darwin" ] &&
  [ "$(uname -m)" = "arm64" ] ||
  fail "full local installation requires Darwin/arm64"

source_status_before="$(git status --porcelain=v1 --untracked-files=all)"
[ -z "$source_status_before" ] ||
  fail "exact local installation requires a completely clean source tree"
source_commit="$(git rev-parse HEAD)"
source_tree="$(git rev-parse 'HEAD^{tree}')"
[[ "$source_commit" =~ ^[a-f0-9]{40}$ ]] ||
  fail "source commit identity is invalid"
[[ "$source_tree" =~ ^[a-f0-9]{40}$ ]] ||
  fail "source tree identity is invalid"

brew_prefix="$(brew --prefix)"
brew_prefix="$(CDPATH='' cd -- "$brew_prefix" && pwd -P)"
if [ -z "$prefix" ]; then
  prefix="$brew_prefix"
fi
[ -d "$prefix" ] && [ ! -L "$prefix" ] ||
  fail "installation prefix is missing or unsafe"
prefix="$(CDPATH='' cd -- "$prefix" && pwd -P)"
[ "$prefix" = "$brew_prefix" ] ||
  fail "full run only accepts the active Homebrew prefix"

user_home="$(CDPATH='' cd -- "$HOME" && pwd -P)"
expected_store="$user_home/.hideout"
if [ -z "$store" ]; then
  store="$expected_store"
fi
store_parent="$(dirname -- "$store")"
[ -d "$store_parent" ] && [ ! -L "$store_parent" ] ||
  fail "store parent is missing or unsafe"
store="$(
  CDPATH='' cd -- "$store_parent" &&
    printf '%s/%s\n' "$(pwd -P)" "$(basename -- "$store")"
)"
validate_delete_scope "$store" "$expected_store" ||
  fail "full run only accepts the exact current-user ~/.hideout store"

installed_binary="$prefix/bin/hideout"
brew_sentinel="$prefix/bin/brew"
[ -f "$brew_sentinel" ] && [ ! -L "$brew_sentinel" ] ||
  fail "unrelated Homebrew sentinel is missing or unsafe"
brew_sentinel_sha_before="$(sha256_file "$brew_sentinel")"

[ -f "$candidate_result" ] && [ ! -L "$candidate_result" ] ||
  fail "candidate result is missing or unsafe"
candidate_result="$(
  CDPATH='' cd -- "$(dirname -- "$candidate_result")" &&
    printf '%s/%s\n' "$(pwd -P)" "$(basename -- "$candidate_result")"
)"
case "$candidate_result" in
  "$artifact_root"/package/*)
    ;;
  *)
    fail "candidate result must remain under .artifacts/045/package"
    ;;
esac
[ "$(normalized_mode "$candidate_result")" = "0600" ] ||
  fail "candidate result must be private mode 0600"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" '
    .schema == "hideout.release-package-candidate-pointer/v1" and
    .result == "passed" and
    .source == {commit:$commit,tree:$tree,dirty:false} and
    .candidateAcceptance == true and
    .publicationStatus == "local-only"
  ' "$candidate_result" >/dev/null ||
  fail "candidate result is not bound to the exact clean source"

candidate_evidence_root="$(dirname -- "$candidate_result")"
candidate_summary_relative="$(jq -er '.summary' "$candidate_result")"
candidate_summary_sha="$(jq -er '.summarySHA256' "$candidate_result")"
candidate_archive_relative="$(jq -er '.archive' "$candidate_result")"
candidate_archive_sha="$(jq -er '.archiveSHA256' "$candidate_result")"
if ! safe_relative_path "$candidate_summary_relative" ||
  ! safe_relative_path "$candidate_archive_relative"; then
  fail "candidate pointer contains an unsafe path"
fi
candidate_summary="$candidate_evidence_root/$candidate_summary_relative"
candidate_archive="$candidate_evidence_root/$candidate_archive_relative"
verify_sha256 "$candidate_summary" "$candidate_summary_sha" ||
  fail "candidate summary digest is invalid"
verify_sha256 "$candidate_archive" "$candidate_archive_sha" ||
  fail "candidate archive digest is invalid"
[ "$(normalized_mode "$candidate_summary")" = "0600" ] &&
  [ "$(normalized_mode "$candidate_archive")" = "0600" ] ||
  fail "candidate summary and archive must be private mode 0600"
candidate_version="$(jq -er '.candidate.version' "$candidate_summary")"
candidate_manifest_sha="$(
  jq -er '.candidate.packageManifestSHA256' "$candidate_summary"
)"
jq -e \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg archive "$candidate_archive_relative" \
  --arg archiveSHA256 "$candidate_archive_sha" \
  --arg manifestSHA256 "$candidate_manifest_sha" '
    .schema == "hideout.release-package-candidate/v1" and
    .result == "passed" and
    .source.commit == $commit and
    .source.tree == $tree and
    .source.dirty == false and
    .source.stableAcrossRun == true and
    .candidate.acceptance == true and
    .candidate.archive == $archive and
    .candidate.archiveSHA256 == $archiveSHA256 and
    .candidate.packageManifestSHA256 == $manifestSHA256 and
    .candidate.publicationStatus == "local-only" and
    .reproducibility.archiveBytesIdentical == true and
    .validation.packageVerification == true and
    .validation.binaryVulnerabilityScan == true
  ' "$candidate_summary" >/dev/null ||
  fail "candidate summary identity is invalid"

scratch="$(
  mktemp -d "$tmp_base/hideout-local-install.XXXXXX"
)"
trap cleanup EXIT
extract_package "$candidate_archive" "$scratch/extracted"
[ -x "$package_root/install.sh" ] &&
  [ -x "$package_root/bin/hideout" ] &&
  [ -f "$package_root/package-manifest.json" ] ||
  fail "candidate package is incomplete"
verify_sha256 "$package_root/package-manifest.json" "$candidate_manifest_sha" ||
  fail "extracted package manifest digest is invalid"
candidate_binary_sha="$(sha256_file "$package_root/bin/hideout")"
"$package_root/bin/hideout" package verify "$package_root" \
  >"$scratch/package-verify-source.out" \
  2>"$scratch/package-verify-source.err"
"$package_root/bin/hideout" version --json \
  >"$scratch/candidate-version.json"
jq -e \
  --arg version "$candidate_version" \
  --arg commit "$source_commit" '
    .schema == "hideout.binary-identity/v1" and
    .productVersion == $version and
    .sourceCommit == $commit and
    .hostOS == "darwin" and
    .hostArch == "arm64"
  ' "$scratch/candidate-version.json" >/dev/null ||
  fail "packaged candidate binary identity is invalid"

prior_installation="none-detected"
if brew list --formula vibe-agi/tap/hideout >/dev/null 2>&1; then
  prior_installation="$(
    brew list --versions vibe-agi/tap/hideout |
      awk 'NR == 1 {print "homebrew-" $2}'
  )"
elif [ -x "$installed_binary" ]; then
  "$installed_binary" version --json >"$scratch/prior-version.json" \
    2>"$scratch/prior-version.err" ||
    fail "existing standalone Hideout identity is unreadable"
  prior_installation="$(
    jq -r '"standalone-" + .productVersion' "$scratch/prior-version.json"
  )"
fi
legacy_entry_count=0
if [ -d "$store" ] && [ ! -L "$store" ]; then
  legacy_entry_count="$(find "$store" -mindepth 1 | wc -l | tr -d '[:space:]')"
fi
jq -n \
  --arg priorInstallation "$prior_installation" \
  --argjson legacyEntries "$legacy_entry_count" \
  --arg store "$store" '
    {
      priorInstallation:$priorInstallation,
      store:$store,
      legacyEntries:$legacyEntries,
      discardAuthorized:true
    }
  ' >"$scratch/prior-installation.json"

wait_for_daemon() {
  local binary="$1" status_file="$2"
  for _ in $(seq 1 200); do
    if "$binary" daemon status >"$status_file" 2>/dev/null; then
      daemon_status_is_serving "$status_file" &&
        return 0
    fi
    sleep 0.1
  done
  return 1
}

ensure_daemon() {
  local binary="$1" label="$2" status_file="$3"
  if "$binary" daemon status >"$status_file" 2>/dev/null &&
    daemon_status_is_serving "$status_file"; then
    return 0
  fi
  "$binary" daemon start \
    >"$scratch/$label-daemon.raw" \
    2>"$scratch/$label-daemon.err" &
  case "$label" in
    legacy)
      legacy_daemon_launcher_pid=$!
      ;;
    candidate)
      candidate_daemon_launcher_pid=$!
      ;;
    *)
      fail "unknown daemon label: $label"
      ;;
  esac
  wait_for_daemon "$binary" "$status_file" ||
    fail "$label daemon did not become ready"
}

environment_ids() {
  local binary="$1" output_file="$2"
  "$binary" env list >"$output_file"
  awk -F '\t' '$NF ~ /^env_[A-Za-z0-9_-]+$/ {print $NF}' "$output_file"
}

if [ -x "$installed_binary" ]; then
  ensure_daemon \
    "$installed_binary" legacy "$scratch/legacy-daemon-status.json"
  "$installed_binary" stop --idle 0s --verbose \
    >"$scratch/legacy-stop.out" \
    2>"$scratch/legacy-stop.err"
  "$installed_binary" clean --stopped --verbose \
    >"$scratch/legacy-clean.out" \
    2>"$scratch/legacy-clean.err"
  legacy_ids="$(
    environment_ids "$installed_binary" "$scratch/legacy-env-final.out"
  )"
  [ -z "$legacy_ids" ] ||
    fail "legacy Hideout environments remain after exact cleanup"
  "$installed_binary" secret status local-proxy \
    >"$scratch/legacy-secret-status.out" \
    2>"$scratch/legacy-secret-status.err" || true
  if grep -Eq '^local-proxy  available([[:space:]]|$)' \
    "$scratch/legacy-secret-status.out"; then
    "$installed_binary" connect plan \
      --profile default --direct \
      >"$scratch/legacy-connect-direct-plan.out" \
      2>"$scratch/legacy-connect-direct-plan.err"
    if ! grep -Fq 'No change required.' \
      "$scratch/legacy-connect-direct-plan.out"; then
      legacy_direct_operation="$(
        sed -nE \
          's/.*hideout connect apply (op_[A-Za-z0-9_-]+) --yes.*/\1/p' \
          "$scratch/legacy-connect-direct-plan.out" |
          tail -1
      )"
      case "$legacy_direct_operation" in
        op_[A-Za-z0-9_-]*)
          ;;
        *)
          fail "legacy direct connection plan omitted its operation ID"
          ;;
      esac
      "$installed_binary" connect apply \
        "$legacy_direct_operation" --yes \
        >"$scratch/legacy-connect-direct-apply.out" \
        2>"$scratch/legacy-connect-direct-apply.err"
    fi
    "$installed_binary" secret delete local-proxy --yes \
      >"$scratch/legacy-secret-delete.out" \
      2>"$scratch/legacy-secret-delete.err"
  fi
  "$installed_binary" daemon stop \
    >"$scratch/legacy-daemon-stop.out" \
    2>"$scratch/legacy-daemon-stop.err"
  stop_process "$legacy_daemon_launcher_pid" TERM
  legacy_daemon_launcher_pid=""
fi

if brew list --formula vibe-agi/tap/hideout >/dev/null 2>&1; then
  brew uninstall --formula vibe-agi/tap/hideout \
    >"$scratch/brew-uninstall.out" \
    2>"$scratch/brew-uninstall.err"
elif [ -f "$prefix/share/hideout/package-manifest.json" ] &&
  [ -x "$installed_binary" ]; then
  "$installed_binary" package uninstall \
    --prefix "$prefix" --store "$store" \
    >"$scratch/prior-package-uninstall.out" \
    2>"$scratch/prior-package-uninstall.err"
elif [ -e "$installed_binary" ] || [ -L "$installed_binary" ]; then
  fail "existing Hideout binary is not owned by a recognized installation"
fi
package_removed=1
[ ! -e "$installed_binary" ] && [ ! -L "$installed_binary" ] ||
  fail "legacy Hideout binary remains after uninstall"
brew list --formula vibe-agi/tap/hideout >/dev/null 2>&1 &&
  fail "legacy Homebrew formula remains installed"

delete_exact_store "$store" "$expected_store"

install_candidate() {
  local label="$1"
  "$package_root/install.sh" \
    --prefix "$prefix" --store "$store" --skip-init \
    >"$scratch/$label-install.out" \
    2>"$scratch/$label-install.err"
  package_removed=0
  [ -f "$installed_binary" ] && [ ! -L "$installed_binary" ] ||
    fail "$label install did not create an exact regular binary"
  [ "$(sha256_file "$installed_binary")" = "$candidate_binary_sha" ] ||
    fail "$label installed binary digest drifted"
  "$installed_binary" package verify "$prefix" \
    >"$scratch/$label-package-verify.out" \
    2>"$scratch/$label-package-verify.err"
  "$installed_binary" version --json \
    >"$scratch/$label-version.json"
  jq -e \
    --arg version "$candidate_version" \
    --arg commit "$source_commit" '
      .schema == "hideout.binary-identity/v1" and
      .productVersion == $version and
      .sourceCommit == $commit and
      .hostOS == "darwin" and
      .hostArch == "arm64"
    ' "$scratch/$label-version.json" >/dev/null ||
    fail "$label installed binary identity is invalid"
}

install_candidate initial

expect -f - "$installed_binary" >"$scratch/setup.out" \
  2>"$scratch/setup.err" <<'EXPECT_SETUP'
set timeout 90
set binary [lindex $argv 0]
spawn -noecho $binary setup
expect {
  -exact "Set up this configuration? \[y/N\]: " {
    send -- "y\r"
  }
  timeout {
    exit 124
  }
  eof {
    set wait_result [wait]
    exit [lindex $wait_result 3]
  }
}
expect {
  eof {
    set wait_result [wait]
    exit [lindex $wait_result 3]
  }
  timeout {
    send -- "\003"
    exit 124
  }
}
EXPECT_SETUP
grep -Fq 'Hideout configuration is ready.' "$scratch/setup.out" ||
  fail "interactive setup did not report success"

ensure_daemon \
  "$installed_binary" candidate "$scratch/daemon-status-before.json"
daemon_instance_before="$(
  jq -er '.instanceId' "$scratch/daemon-status-before.json"
)"
daemon_started_before="$(
  jq -er '.startedAt' "$scratch/daemon-status-before.json"
)"

fixture_root="$scratch/http-root"
workspace="$scratch/workspace"
mkdir "$fixture_root" "$workspace"
printf '%s\n' 'local-install-proxy-smoke' \
  >"$fixture_root/fixture.txt"
python3 -u -m http.server 0 \
  --bind 127.0.0.1 --directory "$fixture_root" \
  >"$scratch/http.raw" 2>&1 &
http_pid=$!
for _ in $(seq 1 200); do
  grep -Eq 'Serving HTTP on .* port [0-9]+' "$scratch/http.raw" && break
  kill -0 "$http_pid" 2>/dev/null ||
    fail "local HTTP fixture exited before publishing"
  sleep 0.05
done
http_port="$(
  sed -nE 's/.* port ([0-9]+).*/\1/p' "$scratch/http.raw" |
    head -1
)"
case "$http_port" in
  "" | *[!0-9]*)
    fail "local HTTP fixture did not publish a port"
    ;;
esac

go build -trimpath \
  -o "$scratch/hideout-gate-socks5" \
  ./cmd/hideout-gate-socks5
"$scratch/hideout-gate-socks5" \
  --listen 127.0.0.1:0 \
  --url-host 127.0.0.1 \
  --authenticated \
  --map-connect "1.1.1.1:443=127.0.0.1:$http_port" \
  >"$scratch/proxy.url" 2>"$scratch/proxy.raw" &
proxy_pid=$!
for _ in $(seq 1 200); do
  [ -s "$scratch/proxy.url" ] && break
  kill -0 "$proxy_pid" 2>/dev/null ||
    fail "local SOCKS5 fixture exited before publishing"
  sleep 0.05
done
proxy_url="$(sed -n '1p' "$scratch/proxy.url")"
case "$proxy_url" in
  socks5://*@127.0.0.1:*)
    ;;
  *)
    fail "local SOCKS5 fixture URL is not authenticated loopback"
    ;;
esac
proxy_authority="${proxy_url#socks5://}"
proxy_credentials="${proxy_authority%@*}"
proxy_username="${proxy_credentials%%:*}"
proxy_password="${proxy_credentials#*:}"
[ -n "$proxy_username" ] && [ -n "$proxy_password" ] ||
  fail "local SOCKS5 fixture credentials are incomplete"
printf '%s\n%s\n%s\n' \
  "$proxy_url" "$proxy_username" "$proxy_password" \
  >"$scratch/proxy.patterns"
chmod 0600 "$scratch/proxy.patterns"

printf '%s' "$proxy_url" |
  "$installed_binary" secret set local-proxy --stdin --yes \
    >"$scratch/secret-set.out" \
    2>"$scratch/secret-set.err"
"$installed_binary" secret status local-proxy \
  >"$scratch/secret-status-available.out" \
  2>"$scratch/secret-status-available.err"
grep -Eq '^local-proxy  available([[:space:]]|$)' \
  "$scratch/secret-status-available.out" ||
  fail "managed proxy secret is not reported available"

"$installed_binary" connect plan \
  --profile default \
  --through local-proxy \
  --dns 1.1.1.1 \
  >"$scratch/connect-plan.out" \
  2>"$scratch/connect-plan.err"
operation_id="$(
  sed -nE \
    's/.*hideout connect apply (op_[A-Za-z0-9_-]+) --yes.*/\1/p' \
    "$scratch/connect-plan.out" |
    tail -1
)"
case "$operation_id" in
  op_[A-Za-z0-9_-]*)
    ;;
  *)
    fail "connection plan did not publish an exact operation ID"
    ;;
esac
"$installed_binary" connect apply "$operation_id" --yes \
  >"$scratch/connect-apply.out" \
  2>"$scratch/connect-apply.err"
"$installed_binary" show connection \
  >"$scratch/connection.out" \
  2>"$scratch/connection.err"
if ! grep -Fq 'local-proxy' "$scratch/connection.out" ||
  ! grep -Fq '1.1.1.1' "$scratch/connection.out"; then
  fail "applied connection projection is incomplete"
fi
"$installed_binary" daemon status \
  >"$scratch/daemon-status-after-connect.json"
daemon_status_matches_identity \
  "$scratch/daemon-status-after-connect.json" \
  "$daemon_instance_before" "$daemon_started_before" ||
  fail "connection apply changed or lost the serving daemon identity"

# The single-quoted target program is intentionally passed verbatim into the VM.
# shellcheck disable=SC2016
"$installed_binary" run \
  --verbose \
  --workspace "$workspace" \
  --terminal never \
  -- sh -eu -c '
    printf "%s\n" "candidate-workspace-smoke" \
      > /workspace/.hideout-local-install-smoke
    response="$(
      curl -fsS --max-time 30 \
        http://1.1.1.1:443/fixture.txt
    )"
    [ "$response" = "local-install-proxy-smoke" ]
    printf "%s\n" "proxy-route=passed"
  ' >"$scratch/run.out" 2>"$scratch/run.err"
if ! grep -Fq 'proxy-route=passed' "$scratch/run.out" ||
  ! grep -Fq 'candidate-workspace-smoke' \
    "$workspace/.hideout-local-install-smoke"; then
  fail "candidate proxied run did not complete"
fi
grep -q 'hideout-gate-socks5: connect_established' "$scratch/proxy.raw" ||
  fail "candidate run did not reach the exact local SOCKS5 fixture"

"$installed_binary" activity summary --json \
  >"$scratch/activity-summary.json" \
  2>"$scratch/activity-summary.err"
jq -e 'type == "object"' "$scratch/activity-summary.json" >/dev/null ||
  fail "activity summary is not structured JSON"

"$installed_binary" help >"$scratch/help-root.out"
"$installed_binary" help connect >"$scratch/help-connect.out"
"$installed_binary" help activity >"$scratch/help-activity.out"
"$installed_binary" help all >"$scratch/help-all.out"
"$installed_binary" help --all >"$scratch/help-all-flag.out"
if ! grep -Fq 'Start here:' "$scratch/help-root.out" ||
  ! grep -Fq 'Purpose:' "$scratch/help-connect.out" ||
  ! grep -Fq 'command, file-metadata, network/DNS' \
    "$scratch/help-activity.out" ||
  ! cmp -s "$scratch/help-all.out" "$scratch/help-all-flag.out"; then
  fail "task-oriented Help journeys are incomplete or inconsistent"
fi
jq -n \
  --arg rootSHA256 "$(sha256_file "$scratch/help-root.out")" \
  --arg connectSHA256 "$(sha256_file "$scratch/help-connect.out")" \
  --arg activitySHA256 "$(sha256_file "$scratch/help-activity.out")" \
  --arg allSHA256 "$(sha256_file "$scratch/help-all.out")" \
  --argjson rootBytes "$(file_bytes "$scratch/help-root.out")" \
  --argjson connectBytes "$(file_bytes "$scratch/help-connect.out")" \
  --argjson activityBytes "$(file_bytes "$scratch/help-activity.out")" \
  --argjson allBytes "$(file_bytes "$scratch/help-all.out")" '
    {
      result:"passed",
      commands:[
        {command:"help",sha256:$rootSHA256,bytes:$rootBytes},
        {command:"help connect",sha256:$connectSHA256,bytes:$connectBytes},
        {command:"help activity",sha256:$activitySHA256,bytes:$activityBytes},
        {command:"help all",sha256:$allSHA256,bytes:$allBytes},
        {command:"help --all",sha256:$allSHA256,bytes:$allBytes}
      ],
      allSpellingsIdentical:true
    }
  ' >"$scratch/help-summary.json"

"$installed_binary" tui --once \
  >"$scratch/tui-once.out" \
  2>"$scratch/tui-once.err"
grep -Eq 'HIDEOUT|Overview|Activity|Config' "$scratch/tui-once.out" ||
  fail "one-shot TUI did not render operator content"
expect -f - "$installed_binary" "$scratch/tui-pty.raw" \
  >"$scratch/tui-pty-driver.out" \
  2>"$scratch/tui-pty-driver.err" <<'EXPECT_TUI'
set timeout 30
set binary [lindex $argv 0]
set raw [lindex $argv 1]
log_user 1
log_file -noappend $raw
spawn -noecho env TERM=xterm-256color $binary tui
expect {
  -re {HIDEOUT|Hideout|Overview|Activity} {
    send -- "q"
  }
  timeout {
    send -- "\003"
    exit 124
  }
  eof {
    set wait_result [wait]
    exit [lindex $wait_result 3]
  }
}
expect {
  eof {
    set wait_result [wait]
    exit [lindex $wait_result 3]
  }
  timeout {
    send -- "\003"
    exit 124
  }
}
EXPECT_TUI
[ -s "$scratch/tui-pty.raw" ] ||
  fail "interactive TUI PTY produced no terminal output"
jq -n \
  --arg rawSHA256 "$(sha256_file "$scratch/tui-pty.raw")" \
  --argjson rawBytes "$(file_bytes "$scratch/tui-pty.raw")" '
    {
      result:"passed",
      ready:true,
      quitKey:"q",
      exitCode:0,
      rawOutputRetained:false,
      rawOutputSHA256:$rawSHA256,
      rawOutputBytes:$rawBytes
    }
  ' >"$scratch/tui-pty.json"

"$installed_binary" ui \
  --listen 127.0.0.1:0 --no-open \
  >"$scratch/ui.raw" 2>"$scratch/ui.err" &
ui_pid=$!
for _ in $(seq 1 200); do
  grep -Fq 'Hideout UI: ' "$scratch/ui.raw" && break
  kill -0 "$ui_pid" 2>/dev/null ||
    fail "local WebUI exited before publishing"
  sleep 0.05
done
ui_url="$(sed -n 's/^Hideout UI: //p' "$scratch/ui.raw" | head -1)"
case "$ui_url" in
  http://127.0.0.1:*/#token=*)
    ;;
  *)
    fail "local WebUI did not publish a tokenized loopback URL"
    ;;
esac
ui_token="${ui_url##*#token=}"
local_api="$(
  sed -n 's/^Local Hideout API: //p' "$scratch/ui.raw" |
    head -1
)"
case "$local_api" in
  http://127.0.0.1:*/api/v1/overview)
    ;;
  *)
    fail "local WebUI did not publish its loopback API"
    ;;
esac
[ -n "$ui_token" ] ||
  fail "local WebUI control token is empty"
printf '%s\n%s\n' "$ui_url" "$ui_token" \
  >>"$scratch/proxy.patterns"
ui_status="$(
  printf \
    'url = "%s"\nheader = "X-Hideout-UI-Token: %s"\n' \
    "$local_api" "$ui_token" |
    curl --silent --show-error \
      --output "$scratch/ui-body.raw" \
      --write-out '%{http_code}' \
      --config -
)"
[ "$ui_status" = "200" ] ||
  fail "authenticated local WebUI returned HTTP $ui_status"
jq -e '
  .version == "hideout.manager-api/v1" and
  .resource == "overview" and
  (.data | type == "object")
' "$scratch/ui-body.raw" >/dev/null ||
  fail "authenticated local WebUI API response is invalid"
jq -n \
  --argjson httpStatus "$ui_status" \
  --arg bodySHA256 "$(sha256_file "$scratch/ui-body.raw")" \
  --argjson bodyBytes "$(file_bytes "$scratch/ui-body.raw")" '
    {
      result:"passed",
      loopback:true,
      authenticated:true,
      httpStatus:$httpStatus,
      containsHideout:true,
      controlURLRetained:false,
      responseBodyRetained:false,
      responseBodySHA256:$bodySHA256,
      responseBodyBytes:$bodyBytes
    }
  ' >"$scratch/web-ui.json"
stop_process "$ui_pid" INT
ui_pid=""

"$installed_binary" daemon status \
  >"$scratch/daemon-status-after-surfaces.json"
daemon_status_matches_identity \
  "$scratch/daemon-status-after-surfaces.json" \
  "$daemon_instance_before" "$daemon_started_before" ||
  fail "daemon identity changed during installed-candidate smoke"

"$installed_binary" stop --idle 0s --verbose \
  >"$scratch/candidate-stop.out" \
  2>"$scratch/candidate-stop.err"
"$installed_binary" clean --stopped --verbose \
  >"$scratch/candidate-clean.out" \
  2>"$scratch/candidate-clean.err"
candidate_ids="$(
  environment_ids "$installed_binary" "$scratch/candidate-env-final.out"
)"
[ -z "$candidate_ids" ] ||
  fail "candidate environment remains after exact clean"

"$installed_binary" connect plan \
  --profile default --direct \
  >"$scratch/connect-direct-plan.out" \
  2>"$scratch/connect-direct-plan.err"
direct_operation_id="$(
  sed -nE \
    's/.*hideout connect apply (op_[A-Za-z0-9_-]+) --yes.*/\1/p' \
    "$scratch/connect-direct-plan.out" |
    tail -1
)"
case "$direct_operation_id" in
  op_[A-Za-z0-9_-]*)
    ;;
  *)
    fail "direct connection plan did not publish an exact operation ID"
    ;;
esac
"$installed_binary" connect apply "$direct_operation_id" --yes \
  >"$scratch/connect-direct-apply.out" \
  2>"$scratch/connect-direct-apply.err"
"$installed_binary" daemon status \
  >"$scratch/daemon-status-after-direct.json"
daemon_status_matches_identity \
  "$scratch/daemon-status-after-direct.json" \
  "$daemon_instance_before" "$daemon_started_before" ||
  fail "direct connection apply changed or lost the serving daemon identity"

"$installed_binary" secret delete local-proxy --yes \
  >"$scratch/secret-delete.out" \
  2>"$scratch/secret-delete.err"
"$installed_binary" secret status local-proxy \
  >"$scratch/secret-status-deleted.out" \
  2>"$scratch/secret-status-deleted.err"
if grep -Eq '^local-proxy  available([[:space:]]|$)' \
  "$scratch/secret-status-deleted.out"; then
  fail "local proxy secret remains available after delete"
fi

stop_process "$proxy_pid" TERM
proxy_pid=""
stop_process "$http_pid" TERM
http_pid=""

"$installed_binary" daemon stop \
  >"$scratch/candidate-daemon-stop.out" \
  2>"$scratch/candidate-daemon-stop.err"
stop_process "$candidate_daemon_launcher_pid" TERM
candidate_daemon_launcher_pid=""

printf '%s\n' 'preserve-across-update-and-uninstall' \
  >"$store/local-install-preservation-marker"
install_candidate update
[ "$(sed -n '1p' "$store/local-install-preservation-marker")" = \
  "preserve-across-update-and-uninstall" ] ||
  fail "same-candidate update changed durable state"

installed_manifest="$prefix/share/hideout/package-manifest.json"
[ -f "$installed_manifest" ] && [ ! -L "$installed_manifest" ] ||
  fail "installed package state is missing before uninstall"
jq -r '.files[].path, .obsoleteFiles[]?.path' \
  "$installed_manifest" >"$scratch/installed-files.txt"
printf '%s\n' 'share/hideout/package-manifest.json' \
  >>"$scratch/installed-files.txt"
"$installed_binary" package uninstall \
  --prefix "$prefix" --store "$store" --dry-run \
  >"$scratch/uninstall-dry-run.out" \
  2>"$scratch/uninstall-dry-run.err"
grep -Fq 'durableState=preserved' "$scratch/uninstall-dry-run.out" ||
  fail "normal uninstall dry-run did not preserve durable state"
"$installed_binary" package uninstall \
  --prefix "$prefix" --store "$store" \
  >"$scratch/uninstall.out" \
  2>"$scratch/uninstall.err"
package_removed=1
grep -Fq 'durableState=preserved' "$scratch/uninstall.out" ||
  fail "normal uninstall did not preserve durable state"
[ -f "$store/local-install-preservation-marker" ] ||
  fail "normal uninstall removed durable state"
while IFS= read -r relative; do
  safe_relative_path "$relative" ||
    fail "installed package state contains an unsafe path"
  if [ -e "$prefix/$relative" ] || [ -L "$prefix/$relative" ]; then
    fail "package-owned file remains after uninstall: $relative"
  fi
done <"$scratch/installed-files.txt"

delete_exact_store "$store" "$expected_store"
install_candidate final
"$installed_binary" init \
  --no-input \
  --profile default \
  --template dev \
  --backend lima \
  --network direct \
  >"$scratch/final-init.out" \
  2>"$scratch/final-init.err"
"$installed_binary" show connection \
  >"$scratch/final-connection.out" \
  2>"$scratch/final-connection.err"
grep -Fqi 'direct' "$scratch/final-connection.out" ||
  fail "final fresh profile is not direct-network"
"$installed_binary" env list >"$scratch/final-env-list.out"
final_environment_count="$(
  awk -F '\t' '$NF ~ /^env_[A-Za-z0-9_-]+$/ {count++} END {print count+0}' \
    "$scratch/final-env-list.out"
)"
[ "$final_environment_count" -eq 0 ] ||
  fail "final fresh installation unexpectedly contains environments"
"$installed_binary" daemon status >"$scratch/final-daemon-running.json"
daemon_status_is_serving "$scratch/final-daemon-running.json" ||
  fail "final daemon did not reach the serving state before ordered stop"
"$installed_binary" daemon stop \
  >"$scratch/final-daemon-stop.out" \
  2>"$scratch/final-daemon-stop.err"
if "$installed_binary" daemon status \
  >"$scratch/final-daemon-probe.out" \
  2>"$scratch/final-daemon-probe.err"; then
  fail "final daemon remains reachable after ordered stop"
fi
jq -n \
  --arg stoppedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" '
    {
      result:"passed",
      state:"stopped",
      statusProbe:"unreachable-after-ordered-stop",
      stoppedAt:$stoppedAt
    }
  ' >"$scratch/final-daemon-state.json"

[ -f "$installed_binary" ] && [ ! -L "$installed_binary" ] &&
  [ "$(sha256_file "$installed_binary")" = "$candidate_binary_sha" ] ||
  fail "final installed binary is not the exact candidate"
"$installed_binary" package verify "$prefix" \
  >"$scratch/final-package-verify.out" \
  2>"$scratch/final-package-verify.err"
"$installed_binary" version --json >"$scratch/final-version.json"
jq -e \
  --arg version "$candidate_version" \
  --arg commit "$source_commit" '
    .schema == "hideout.binary-identity/v1" and
    .productVersion == $version and
    .sourceCommit == $commit
  ' "$scratch/final-version.json" >/dev/null ||
  fail "final installed binary identity is invalid"
brew_sentinel_sha_after="$(sha256_file "$brew_sentinel")"
[ "$brew_sentinel_sha_after" = "$brew_sentinel_sha_before" ] ||
  fail "unrelated Homebrew sentinel changed"
brew list --formula vibe-agi/tap/hideout >/dev/null 2>&1 &&
  fail "final exact standalone candidate is shadowed by the old formula"

source_status_after="$(git status --porcelain=v1 --untracked-files=all)"
source_commit_after="$(git rev-parse HEAD)"
source_tree_after="$(git rev-parse 'HEAD^{tree}')"
[ -z "$source_status_after" ] &&
  [ "$source_commit_after" = "$source_commit" ] &&
  [ "$source_tree_after" = "$source_tree" ] ||
  fail "source repository changed during local installation"

evidence_stage="$scratch/evidence"
mkdir "$evidence_stage"
for evidence_name in \
  candidate-version.json \
  package-verify-source.out \
  prior-installation.json \
  setup.out \
  daemon-status-before.json \
  daemon-status-after-connect.json \
  secret-set.out \
  secret-status-available.out \
  connect-plan.out \
  connect-apply.out \
  connection.out \
  run.out \
  activity-summary.json \
  help-summary.json \
  tui-once.out \
  tui-pty.json \
  web-ui.json \
  candidate-clean.out \
  candidate-env-final.out \
  connect-direct-plan.out \
  connect-direct-apply.out \
  daemon-status-after-direct.json \
  secret-delete.out \
  secret-status-deleted.out \
  update-install.out \
  update-package-verify.out \
  uninstall-dry-run.out \
  uninstall.out \
  final-init.out \
  final-connection.out \
  final-env-list.out \
  final-daemon-state.json \
  final-package-verify.out \
  final-version.json; do
  [ -f "$scratch/$evidence_name" ] ||
    fail "expected local-install evidence is missing: $evidence_name"
  cp "$scratch/$evidence_name" "$evidence_stage/$evidence_name"
done
jq -n \
  --arg brewPath "$brew_sentinel" \
  --arg before "$brew_sentinel_sha_before" \
  --arg after "$brew_sentinel_sha_after" '
    {
      path:$brewPath,
      sha256Before:$before,
      sha256After:$after,
      unchanged:($before == $after)
    }
  ' >"$evidence_stage/unrelated-homebrew.json"
find "$evidence_stage" -type f -exec chmod 0600 {} +

if grep -FRq -f "$scratch/proxy.patterns" \
  "$evidence_stage" "$store"; then
  fail "proxy secret material entered retained evidence or durable state"
fi

if [ -L "$out" ]; then
  fail "evidence root must not be a symlink"
fi
mkdir -p "$out"
out="$(CDPATH='' cd -- "$out" && pwd -P)"
case "$out" in
  "$artifact_root"/local-install)
    ;;
  *)
    fail "evidence root must be .artifacts/045/local-install"
    ;;
esac
chmod 0700 "$out"
run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out/$run_id"
[ ! -e "$run_dir" ] || fail "evidence run already exists"
mkdir "$run_dir"
cp -R "$evidence_stage/." "$run_dir/"
find "$run_dir" -type d -exec chmod 0700 {} +
find "$run_dir" -type f -exec chmod 0600 {} +

artifact_lines="$scratch/artifacts.jsonl"
: >"$artifact_lines"
while IFS= read -r evidence_file; do
  relative="${evidence_file#"$out"/}"
  jq -nc \
    --arg path "$relative" \
    --arg sha256 "$(sha256_file "$evidence_file")" \
    --argjson bytes "$(file_bytes "$evidence_file")" '
      {path:$path,sha256:$sha256,bytes:$bytes,mode:"0600"}
    ' >>"$artifact_lines"
done < <(find "$run_dir" -type f | LC_ALL=C sort)
artifacts="$(jq -s . "$artifact_lines")"

result_tmp="$out/.result.$$.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --arg tree "$source_tree" \
  --arg version "$candidate_version" \
  --arg archive "$candidate_archive_relative" \
  --arg archiveSHA256 "$candidate_archive_sha" \
  --arg packageManifestSHA256 "$candidate_manifest_sha" \
  --arg binarySHA256 "$candidate_binary_sha" \
  --arg prefix "$prefix" \
  --arg store "$store" \
  --arg priorInstallation "$prior_installation" \
  --argjson artifacts "$artifacts" '
    {
      schema:"hideout.local-install-candidate/v1",
      generatedAt:$generatedAt,
      result:"passed",
      sourceCandidate:{commit:$commit,tree:$tree,dirty:false},
      candidateAcceptance:true,
      candidate:{
        version:$version,
        archive:$archive,
        archiveSHA256:$archiveSHA256,
        packageManifestSHA256:$packageManifestSHA256,
        installedBinarySHA256:$binarySHA256,
        consumedWithoutRebuild:true
      },
      installation:{
        hostOS:"Darwin",
        hostArch:"arm64",
        prefix:$prefix,
        store:$store,
        legacyDataPolicy:"explicitly-discarded",
        priorInstallation:$priorInstallation,
        finalInstallation:"exact-standalone-candidate",
        finalDaemonState:"stopped",
        finalEnvironmentCount:0
      },
      checks:{
        sourceClean:true,
        archiveDigestVerified:true,
        archiveSafetyVerified:true,
        packageVerified:true,
        packageIdentityVerified:true,
        legacyInstallRemoved:true,
        legacyDataDiscarded:true,
        unrelatedHomebrewPreserved:true,
        setupCompleted:true,
        daemonStarted:true,
        secretStoredWithoutRetention:true,
        connectionPlanned:true,
        connectionAppliedWithoutDaemonStop:true,
        runCompletedThroughProxy:true,
        helpJourneysRendered:true,
        tuiSnapshotRendered:true,
        tuiPTYExitedCleanly:true,
        webUIAuthenticated:true,
        environmentCleaned:true,
        sameCandidateUpdate:true,
        uninstallDryRun:true,
        uninstallPreservedStore:true,
        packageFilesAbsentAfterUninstall:true,
        finalReinstallExact:true,
        finalDaemonStopped:true,
        finalEnvironmentAbsent:true,
        noSecretValueInEvidence:true
      },
      artifacts:$artifacts,
      limitations:[
        "The final installation is the exact unsigned standalone local candidate; remote tag, GitHub Release, and Homebrew publication require a separately authorized workflow.",
        "The authenticated SOCKS5 and WebUI control URLs, credentials, raw interactive TUI stream, and WebUI response body are checked in private temporary storage and are not retained."
      ]
    }
  ' >"$result_tmp"
chmod 0600 "$result_tmp"
validate_receipt \
  "$result_tmp" "$source_commit" "$source_tree" "$candidate_version" \
  "$candidate_archive_sha" "$candidate_manifest_sha" \
  "$candidate_binary_sha" "$prefix" "$store" ||
  fail "final local-install receipt semantic validation failed"
while IFS= read -r artifact; do
  path="$(jq -r '.path' <<<"$artifact")"
  expected_sha="$(jq -r '.sha256' <<<"$artifact")"
  expected_bytes="$(jq -r '.bytes' <<<"$artifact")"
  safe_relative_path "$path" ||
    fail "receipt contains an unsafe artifact path"
  file="$out/$path"
  verify_sha256 "$file" "$expected_sha" ||
    fail "receipt artifact digest is invalid: $path"
  [ "$(file_bytes "$file")" = "$expected_bytes" ] &&
    [ "$(normalized_mode "$file")" = "0600" ] ||
    fail "receipt artifact metadata is invalid: $path"
done < <(jq -c '.artifacts[]' "$result_tmp")
if grep -FRq -f "$scratch/proxy.patterns" \
  "$run_dir" "$result_tmp" "$store"; then
  fail "proxy secret material entered final receipt or durable state"
fi
mv "$result_tmp" "$out/result.json"
package_removed=0

gate_completed=1
printf \
  'local-install: passed version=%s archiveSHA256=%s receipt=%s\n' \
  "$candidate_version" "$candidate_archive_sha" "$out/result.json"
