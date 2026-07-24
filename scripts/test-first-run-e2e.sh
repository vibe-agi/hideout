#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/daemon-temp.sh"

mode="local-fast"
require_real=0
fixture=""
out=""
package_path=""
manifest_package_identity_path=""
cleanup_daemon_binary=""
cleanup_daemon_store=""
cleanup_daemon_store_owned=0
cleanup_environment_name=""

cleanup_test_daemon() {
  if [ -n "$cleanup_daemon_binary" ] && [ -n "$cleanup_daemon_store" ]; then
    if [ -n "$cleanup_environment_name" ]; then
      env HIDEOUT_STORE_ROOT="$cleanup_daemon_store" \
        "$cleanup_daemon_binary" env remove "$cleanup_environment_name" --force >/dev/null 2>&1 || true
    fi
    env HIDEOUT_STORE_ROOT="$cleanup_daemon_store" \
      "$cleanup_daemon_binary" daemon stop >/dev/null 2>&1 || true
  fi
  if [ "$cleanup_daemon_store_owned" -eq 1 ] && [ -n "$cleanup_daemon_store" ]; then
    rm -rf "$cleanup_daemon_store"
  fi
}

trap cleanup_test_daemon EXIT

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-first-run-e2e.sh [--local-fast|--real-backend|--setup-local-fast|--setup-real-backend] [--require-real]
                                [--fixture <name>] [--out <dir>]
                                [--package <path>]

Fixtures:
  bad-checksum | missing-manifest | missing-helper | stale-obsolete |
  duplicate-profile | unsafe-workspace | redaction
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --local-fast)
      mode="local-fast"
      shift
      ;;
    --real-backend)
      mode="real-backend"
      shift
      ;;
    --setup-local-fast)
      mode="setup-local-fast"
      shift
      ;;
    --setup-real-backend)
      mode="setup-real-backend"
      shift
      ;;
    --require-real)
      require_real=1
      shift
      ;;
    --fixture)
      fixture="${2:-}"
      shift 2
      ;;
    --out)
      out="${2:-}"
      shift 2
      ;;
    --package)
      package_path="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "first-run-e2e: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ -z "$out" ]; then
  out="$(mktemp -d "${TMPDIR:-/tmp}/hideout-first-run-e2e.XXXXXX")"
fi
mkdir -p "$out/logs" "$out/reports"
out="$(cd "$out" && pwd -P)"
logs="$out/logs"
reports="$out/reports"
proofs_file="$out/.proofs.json"
manifest="$out/product-hardening-evidence.json"
printf '[]\n' >"$proofs_file"

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "first-run-e2e: missing shasum or sha256sum" >&2
    exit 127
  fi
}

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "first-run-e2e: missing required tool: $1" >&2
    exit 127
  fi
}

require_tool jq

artifact_json() {
  local kind="$1"
  local rel="$2"
  local desc="$3"
  if [ -z "$rel" ]; then
    printf '[]\n'
    return
  fi
  local path="$out/$rel"
  local sum=""
  if [ -f "$path" ]; then
    sum="$(sha256_file "$path")"
  fi
  jq -n \
    --arg kind "$kind" \
    --arg path "$rel" \
    --arg sha "$sum" \
    --arg desc "$desc" \
    '[{
      kind: $kind,
      path: $path,
      sha256: (if $sha == "" then empty else $sha end),
      redactionStatus: "passed",
      description: $desc
    }]'
}

claims_json() {
  local ids="$1"
  local desc="$2"
  local scope="$3"
  jq -n \
    --arg ids "$ids" \
    --arg desc "$desc" \
    --arg scope "$scope" \
    '$ids | split(",") | map(select(length > 0) | {
      claimId: ., source: "spec", description: $desc, scope: $scope
    })'
}

add_proof() {
  local proof_id="$1"
  local proof_mode="$2"
  local status="$3"
  local summary="$4"
  local claim_id="$5"
  local claim_desc="$6"
  local scope="$7"
  local artifact_kind="${8:-}"
  local artifact_rel="${9:-}"
  local artifact_desc="${10:-}"
  local reason="${11:-}"
  local prereq_name="${12:-first-run-e2e}"
  local prereq_status="${13:-available}"
  local prereq_reason="${14:-}"
  local feature_id="${15:-022-alpha-first-run-e2e}"
  local runtime_json="${16:-null}"
  local artifact2_kind="${17:-log}"
  local artifact2_rel="${18:-}"
  local artifact2_desc="${19:-}"
  local redaction="passed"
  if [ "$status" = "not-run" ]; then
    redaction="not-run"
  fi
  local artifacts claims prereqs tmp
  artifacts="$(artifact_json "$artifact_kind" "$artifact_rel" "$artifact_desc")"
  if [ -n "$artifact2_rel" ]; then
    artifacts="$(jq -n \
      --argjson first "$artifacts" \
      --argjson second "$(artifact_json "$artifact2_kind" "$artifact2_rel" "$artifact2_desc")" \
      '$first + $second')"
  fi
  claims="$(claims_json "$claim_id" "$claim_desc" "$scope")"
  prereqs="$(jq -n \
    --arg name "$prereq_name" \
    --arg status "$prereq_status" \
    --arg reason "$prereq_reason" \
    '[{
      name: $name,
      status: $status,
      reason: (if $reason == "" then empty else $reason end)
    }]')"
  tmp="$proofs_file.tmp"
  jq \
    --arg proof_id "$proof_id" \
    --arg feature "$feature_id" \
    --arg mode "$proof_mode" \
    --arg status "$status" \
    --arg summary "$summary" \
    --arg redaction "$redaction" \
    --arg reason "$reason" \
    --argjson claims "$claims" \
    --argjson artifacts "$artifacts" \
    --argjson prereqs "$prereqs" \
    --argjson runtime "$runtime_json" \
    '. + [({
      proofId: $proof_id,
      featureId: $feature,
      mode: $mode,
      evidenceClass: "first-run-e2e",
      status: $status,
      commandSummary: $summary,
      coveredClaims: $claims,
      prerequisites: $prereqs,
      artifacts: $artifacts,
      redactionStatus: $redaction,
      host: {os: "'$(go env GOOS)'", arch: "'$(go env GOARCH)'"}
    } + (if $reason == "" then {} else {notRunReason: $reason} end) +
      (if $runtime == null then {} else {runtime: $runtime} end))]' "$proofs_file" >"$tmp"
  mv "$tmp" "$proofs_file"
}

git_commit() {
  git rev-parse HEAD 2>/dev/null || printf 'unknown'
}

git_dirty() {
  if [ -n "$(git status --porcelain --untracked-files=normal 2>/dev/null)" ]; then
    printf 'true'
  else
    printf 'false'
  fi
}

write_manifest() {
  local package_identity="null"
  if [ -n "$manifest_package_identity_path" ]; then
    package_identity="$(jq -ce . "$manifest_package_identity_path")"
  fi
  jq -n \
    --arg generated "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg commit "$(git_commit)" \
    --argjson dirty "$(git_dirty)" \
    --argjson packageIdentity "$package_identity" \
    --slurpfile proofs "$proofs_file" \
    '{
      version: "hideout.product-hardening-evidence/v1",
      generatedAt: $generated,
      commit: $commit,
      dirty: $dirty,
      proofs: $proofs[0]
    } + (if $packageIdentity == null then {} else {packageIdentity: $packageIdentity} end)' >"$manifest"
}

setup_runtime_binding() {
  local package_root="$1"
  local environment_id="$2"
  local catalog="$package_root/runtime/catalog.json"
  jq -ce \
    --arg environmentId "$environment_id" \
    --arg hostOS "$(go env GOOS)" \
    --arg hostArch "$(go env GOARCH)" '
    .families[] | select(.id == "developer-standard") as $family |
    $family.revisions[] | select(.id == $family.currentRevision) as $revision |
    $revision.artifacts[] | select(.hostOS == $hostOS and .hostArch == $hostArch) |
    {
      schema: "hideout.runtime-evidence-binding/v1",
      family: $family.id,
      revision: $revision.id,
      artifactSHA256: .sha256,
      environmentId: $environmentId,
      hostOS: .hostOS,
      hostArch: .hostArch,
      guestArch: .guestArch,
      buildCommit: .source.buildCommit,
      buildDirty: false
    }
  ' "$catalog"
}

validate_evidence() {
  jq -e '
    .version == "hideout.product-hardening-evidence/v1" and
    (.proofs | length > 0) and
    all(.proofs[];
      (.proofId | length > 0) and
      (.featureId == "022-alpha-first-run-e2e" or .featureId == "038-zero-friction-setup") and
      (.status == "passed" or .status == "failed" or .status == "not-run") and
      (.coveredClaims | length > 0) and
      (.redactionStatus == "passed" or .redactionStatus == "failed" or .redactionStatus == "not-run")
    )
  ' "$manifest" >"$logs/evidence-content.out"
  go run ./cmd/hideout-schema-validate \
    schemas/product-hardening-evidence.schema.json \
    "$manifest" >"$logs/evidence-schema.out" 2>"$logs/evidence-schema.err"
  if jq -e 'any(.proofs[]; .proofId == "038.setup.gate0.intent-plan-parity")' "$manifest" >/dev/null; then
    HIDEOUT_038_EVIDENCE_DIR="$out" go test ./internal/productevidence \
      -run '^TestRetainedSetupLocalEvidencePassesProductionEvaluator$' -count=1 \
      >"$logs/evidence-evaluator.out" 2>"$logs/evidence-evaluator.err"
  fi
  if jq -e 'any(.proofs[]; .proofId == "038.setup.real-gate2.first-run")' "$manifest" >/dev/null; then
    HIDEOUT_038_REAL_EVIDENCE_DIR="$out" go test ./internal/productevidence \
      -run '^TestRetainedSetupRealEvidenceUsesProductionReleaseEvaluator$' -count=1 \
      >"$logs/evidence-real-evaluator.out" 2>"$logs/evidence-real-evaluator.err"
  fi
  if grep -R -E 'cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5://[^[:space:]]+:[^[:space:]]+@' "$out" >/dev/null 2>&1; then
    echo "first-run-e2e: evidence contains control-plane material" >&2
    grep -R -n -E 'cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5://[^[:space:]]+:[^[:space:]]+@' "$out" >&2 || true
    exit 1
  fi
}

run_logged() {
  local name="$1"
  shift
  if ! "$@" >"$logs/$name.out" 2>"$logs/$name.err"; then
    echo "first-run-e2e: step $name failed:" >&2
    tail -20 "$logs/$name.out" "$logs/$name.err" >&2 || true
    exit 1
  fi
}

wait_environment_status() {
  local hideout_binary="$1"
  local store_root="$2"
  local environment_name="$3"
  local expected="$4"
  local log_name="$5"
  local deadline=$((SECONDS + 30))
  while [ "$SECONDS" -lt "$deadline" ]; do
    env HIDEOUT_STORE_ROOT="$store_root" "$hideout_binary" env inspect "$environment_name" \
      >"$logs/$log_name.out" 2>"$logs/$log_name.err"
    if grep -q "  status: $expected" "$logs/$log_name.out"; then
      return 0
    fi
    sleep 1
  done
  echo "first-run-e2e: environment $environment_name did not reach $expected within 30s" >&2
  cat "$logs/$log_name.out" >&2
  return 1
}

build_package() {
  local tmp="$1"
  local pkg="$tmp/hideout.tar.gz"
  if [ -n "$package_path" ]; then
    cp "$package_path" "$pkg"
  else
    scripts/package-local.sh --out "$pkg" >"$logs/package-local.out" 2>"$logs/package-local.err"
  fi
  mkdir -p "$tmp/package"
  tar -xzf "$pkg" -C "$tmp/package"
  printf '%s\n' "$tmp/package/hideout"
}

assert_docs_order() {
	local doc="docs/first-run-alpha.md"
	grep -q 'brew install vibe-agi/tap/hideout' "$doc"
	grep -q '^hideout setup$' "$doc"
	local install_line setup_line
	install_line="$(grep -n -m1 'brew install vibe-agi/tap/hideout' "$doc" | cut -d: -f1)"
	setup_line="$(grep -n -m1 '^hideout setup$' "$doc" | cut -d: -f1)"
	if [ -z "$install_line" ] || [ -z "$setup_line" ] || [ "$install_line" -ge "$setup_line" ]; then
		echo "first-run-e2e: docs install/setup order is invalid" >&2
		exit 1
	fi
	printf 'docs-order: Homebrew install precedes interactive setup\n' >"$reports/docs-order.txt"
}

install_skip_init() {
  local package_root="$1"
  local prefix="$2"
  local store="$3"
  run_logged install "$package_root/install.sh" --prefix "$prefix" --store "$store" --skip-init
  if ! grep -q 'init skipped' "$logs/install.out"; then
    echo "first-run-e2e: installer did not report init skipped:" >&2
    tail -20 "$logs/install.out" >&2 || true
    exit 1
  fi
  if [ ! -x "$prefix/bin/hideout" ]; then
    echo "first-run-e2e: installed hideout binary is missing or not executable at $prefix/bin/hideout" >&2
    exit 1
  fi
  if [ -e "$store/profiles/default/profile.json" ]; then
    echo "first-run-e2e: --skip-init created default profile" >&2
    exit 1
  fi
}

local_fast() {
  local tmp package_root prefix store workspace hideout
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-first-run-local.XXXXXX")"
  package_root="$(build_package "$tmp")"
  prefix="$tmp/install"
  # The installed run auto-starts hideoutd. Its store-rooted Unix socket needs
  # the shared short-root test helper when macOS exports a long TMPDIR.
  store="$(hideout_mktemp_daemon_store)"
	workspace="$tmp/workspace"
	mkdir -p "$workspace"
	workspace="$(cd "$workspace" && pwd -P)"
	install_skip_init "$package_root" "$prefix" "$store"
  hideout="$prefix/bin/hideout"

  run_logged verify "$hideout" package verify "$prefix"
  grep -q 'package: ok mode=installed' "$logs/verify.out"
  run_logged support "$hideout" support matrix
  run_logged doctor env HIDEOUT_STORE_ROOT="$store" "$hideout" doctor --backend native --workspace "$workspace"
  grep -q 'profile: missing default' "$logs/doctor.out" || true

  run_logged init env HIDEOUT_STORE_ROOT="$store" "$hideout" init \
    --template dev \
    --profile default \
    --backend native \
    --network direct \
    --no-input
  grep -q 'Hideout init' "$logs/init.out"
  if [ ! -f "$store/profiles/default/profile.json" ]; then
    echo "first-run-e2e: setup did not create the default profile:" >&2
    tail -40 "$logs/setup-pty.out" >&2 || true
    exit 1
  fi

  run_logged doctor-after-init env HIDEOUT_STORE_ROOT="$store" "$hideout" doctor --backend native --workspace "$workspace"
  grep -q 'profile: ok default' "$logs/doctor-after-init.out"

  run_logged run env HIDEOUT_STORE_ROOT="$store" "$hideout" run \
    --profile default \
    --backend native \
    --allow-weak-isolation \
    --workspace "$workspace" \
    --verbose \
    -- pwd
  grep -q "$workspace" "$logs/run.out"
  grep -q 'Hideout boundary:' "$logs/run.err"

  run_logged audit env HIDEOUT_STORE_ROOT="$store" "$hideout" audit show --limit 20
  test -s "$logs/audit.out"
  run_logged daemon-stop env HIDEOUT_STORE_ROOT="$store" "$hideout" daemon stop
  rm -rf "$store"
  cleanup_daemon_binary=""
  cleanup_daemon_store=""
  cleanup_daemon_store_owned=0
  if grep -R 'go run ./cmd/hideout' "$logs" >/dev/null 2>&1; then
    echo "first-run-e2e: source-tree go run appeared after package install" >&2
    exit 1
  fi

  assert_docs_order
  add_proof "022.first-run.local-fast.install" "local-fast" "passed" \
    "package install --skip-init" "022.SC-001" \
    "Local-fast first-run installs from package" "first-run" \
    "log" "logs/install.out" "packaged installer output"
  add_proof "022.first-run.local-fast.verify" "local-fast" "passed" \
    "installed package verify and doctor checks" "022.FR-004" \
    "Package verification and local checks run before success" "package" \
    "log" "logs/verify.out" "package verification output"
  add_proof "022.first-run.local-fast.init" "local-fast" "passed" \
    "initialize weak/dev default profile once" "022.FR-005" \
    "Selected first-run profile is initialized exactly once" "profile" \
    "log" "logs/init.out" "init output"
  add_proof "022.first-run.local-fast.run" "local-fast" "passed" \
    "run one installed-binary native command" "022.SC-001" \
    "Local-fast first-run runs one installed-binary command" "run" \
    "log" "logs/run.out" "first command stdout"
  add_proof "022.first-run.local-fast.audit-boundary" "local-fast" "passed" \
    "capture audit and Boundary evidence" "022.FR-006" \
    "First command captures audit and Boundary evidence" "evidence" \
    "log" "logs/run.err" "first command Boundary output"
  add_proof "022.first-run.docs.order" "docs" "passed" \
    "docs use --skip-init before explicit init" "022.FR-013" \
    "First-run docs match install/init order" "docs" \
    "docs-report" "reports/docs-order.txt" "docs order report"
  add_proof "022.first-run.failure.fixtures" "local-fast" "passed" \
    "failure fixtures available in first-run runner" "022.SC-004" \
    "Failure fixtures do not produce passing first-run evidence" "fail-closed" \
    "log" "logs/verify.out" "fixture support anchored by package verification"
  write_manifest
  validate_evidence
  echo "first-run-e2e: local-fast passed evidence=$manifest"
}

setup_local_fast() {
  local tmp package_root prefix store hideout fakebin marker
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-setup-local.XXXXXX")"
  package_root="$(build_package "$tmp")"
  prefix="$tmp/install"
  store="$(hideout_mktemp_daemon_store)"
  install_skip_init "$package_root" "$prefix" "$store"
  hideout="$prefix/bin/hideout"
  manifest_package_identity_path="$reports/setup-package-identity.json"
  if ! "$hideout" support release package-identity \
    --archive "$tmp/hideout.tar.gz" --out "$manifest_package_identity_path" \
    >"$logs/setup-package-identity.out" 2>"$logs/setup-package-identity.err"; then
    echo "first-run-e2e: setup package-identity failed:" >&2
    tail -20 "$logs/setup-package-identity.out" "$logs/setup-package-identity.err" >&2 || true
    exit 1
  fi
  cleanup_daemon_binary="$hideout"
  cleanup_daemon_store="$store"
  cleanup_daemon_store_owned=1

  # Setup must not reach Lima. The fake executable turns an accidental backend
  # probe into a deterministic local-fast failure.
  fakebin="$tmp/fakebin"
  marker="$tmp/limactl-invoked"
  mkdir -p "$fakebin"
  printf '#!/bin/sh\nprintf invoked >"$HIDEOUT_SETUP_LIMACTL_MARKER"\nexit 99\n' >"$fakebin/limactl"
  chmod 0755 "$fakebin/limactl"
  PATH="$fakebin:$PATH" HIDEOUT_SETUP_LIMACTL_MARKER="$marker" \
    go run ./test/e2e/setuppty \
      --hideout "$hideout" --store "$store" --out "$logs/setup-pty.out"

  if [ ! -f "$store/profiles/default/profile.json" ]; then
    echo "first-run-e2e: setup did not create the default profile:" >&2
    tail -40 "$logs/setup-pty.out" >&2 || true
    exit 1
  fi
  if [ -e "$marker" ]; then
    echo "first-run-e2e: setup invoked limactl" >&2
    exit 1
  fi
  if [ -d "$store/environments" ] && find "$store/environments" -mindepth 1 -maxdepth 1 -type d ! -name '.slots' -print -quit | grep -q .; then
    echo "first-run-e2e: setup created an environment" >&2
    exit 1
  fi
  # Filesystem judge for "no runtime download": the fake limactl already traps
  # backend calls; this independently proves setup staged no runtime-scale
  # artifact in the store through any other path.
  if find "$store" -type f \( -name '*.qcow2' -o -name '*.img' -o -size +8M \) -print -quit | grep -q .; then
    echo "first-run-e2e: setup left runtime-scale artifacts in the store" >&2
    exit 1
  fi
  run_logged setup-doctor env HIDEOUT_STORE_ROOT="$store" "$hideout" doctor
  grep -q 'profile: ok default' "$logs/setup-doctor.out"
  run_logged setup-parity-tests go test \
    ./internal/operatorintent ./internal/manager ./internal/app \
    -run 'Test(ParseNaturalOperatorIntents|ParseRejectsAmbiguous|SetupRejectsEveryArgument|InitServiceSetup|InitServiceRejectsSetupOverrides|InitCommandUsesOnlyDaemon|SetupFreshReview)'
  run_logged setup-state-tests go test \
    ./internal/manager ./internal/app \
    -run 'Test(SetupCancel|ConfirmSetupStops|SetupReadySendsNoApply|SetupBlockedAndRepairable|InitServiceApplyRejects|InitServiceSemanticDigest|InitServiceReady|InitServiceBlocks|InitServiceExisting|InitServiceAdvancedNative|ProfileMutationLock|CoreApplyInitSerializes)'
  run_logged setup-daemon-tests go test \
    ./internal/app ./internal/daemon \
    -run 'Test(InitDaemonRequest|CodedInitErrorUsesTypedDaemonBoundary|EnsureStarted)'
  run_logged setup-docs scripts/test-first-run-docs-smoke.sh

  add_proof "038.setup.gate0.intent-plan-parity" "unit" "passed" \
    "natural setup intent and Manager semantic plan parity tests passed" \
    "038.FR-001,038.FR-002,038.FR-008,038.FR-010,038.FR-030,038.FR-031,038.SC-005,038.SC-015" \
    "Setup and equivalent advanced init share one authority plan" "authority" \
    "log" "logs/setup-parity-tests.out" "setup authority parity test output" \
    "" "setup-gate0" "available" "" "038-zero-friction-setup"
  add_proof "038.setup.gate0.cancel-drift-readonly" "unit" "passed" \
    "cancellation, drift rejection, and ready-state read-only tests passed" \
    "038.FR-005,038.FR-006,038.FR-007,038.FR-014,038.FR-015,038.FR-016,038.SC-003,038.SC-004" \
    "Setup cancellation and state races fail before mutation" "state" \
    "log" "logs/setup-state-tests.out" "setup state contract test output" \
    "" "setup-gate0" "available" "" "038-zero-friction-setup"
  add_proof "038.setup.gate0.daemon-recovery" "unit" "passed" \
    "authenticated daemon init client and cancellation tests passed" \
    "038.FR-029,038.FR-032,038.FR-035,038.SC-010,038.SC-012" \
    "Setup daemon failures do not gain an embedded fallback" "daemon" \
    "log" "logs/setup-daemon-tests.out" "setup daemon client test output" \
    "" "setup-gate0" "available" "" "038-zero-friction-setup"
  add_proof "038.setup.local-fast.package-pty" "local-fast" "passed" \
    "installed candidate setup completed through a real PTY without Lima" \
    "038.FR-001,038.FR-003,038.FR-004,038.FR-005,038.FR-009,038.FR-012,038.FR-013,038.FR-017,038.FR-018,038.FR-019,038.FR-020,038.FR-021,038.FR-026,038.FR-027,038.SC-001,038.SC-002,038.SC-009,038.SC-013" \
    "Packaged interactive setup reviews and writes configuration only" "setup" \
    "terminal-capture" "logs/setup-pty.out" "packaged setup PTY transcript" \
    "" "setup-local-fast" "available" "" "038-zero-friction-setup"
  add_proof "038.setup.docs.truth" "docs" "passed" \
    "setup-first docs and source/published Homebrew caveats agree" "038.FR-028,038.SC-013" \
    "Canonical setup documentation maps to registered proof" "docs" \
    "docs-report" "logs/setup-docs.out" "setup docs truth output" \
    "" "setup-docs" "available" "" "038-zero-friction-setup"
  write_manifest
  validate_evidence

  run_logged setup-daemon-stop env HIDEOUT_STORE_ROOT="$store" "$hideout" daemon stop
  rm -rf "$store"
  cleanup_daemon_binary=""
  cleanup_daemon_store=""
  cleanup_daemon_store_owned=0
  echo "first-run-e2e: setup-local-fast passed evidence=$manifest"
}

write_failed_fixture() {
  local name="$1"
  local reason="$2"
  printf '%s\n' "$reason" >"$reports/$name.txt"
  add_proof "022.first-run.failure.fixtures" "local-fast" "failed" \
    "fixture $name failed closed" "022.SC-004" \
    "Failure fixtures do not produce passing first-run evidence" "fail-closed" \
    "docs-report" "reports/$name.txt" "$name fixture report" \
    "" "$name" "available"
  write_manifest
  validate_evidence
  echo "first-run-e2e: fixture $name failed closed evidence=$manifest" >&2
  exit 1
}

fixture_bad_checksum() {
  local tmp package_root
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-first-run-fixture.XXXXXX")"
  package_root="$(build_package "$tmp")"
  printf '\ncorrupt\n' >>"$package_root/README.md"
  if "$package_root/install.sh" --prefix "$tmp/install" --store "$tmp/store" --skip-init >"$logs/bad-checksum.out" 2>"$logs/bad-checksum.err"; then
    echo "first-run-e2e: bad-checksum fixture unexpectedly passed" >&2
    exit 1
  fi
  grep -q 'checksum mismatch' "$logs/bad-checksum.err"
  write_failed_fixture "bad-checksum" "package checksum mismatch rejected"
}

fixture_missing_manifest() {
  local tmp package_root
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-first-run-fixture.XXXXXX")"
  package_root="$(build_package "$tmp")"
  rm -f "$package_root/package-manifest.json"
  if "$package_root/install.sh" --prefix "$tmp/install" --store "$tmp/store" --skip-init >"$logs/missing-manifest.out" 2>"$logs/missing-manifest.err"; then
    echo "first-run-e2e: missing-manifest fixture unexpectedly passed" >&2
    exit 1
  fi
  grep -q 'package-manifest.json' "$logs/missing-manifest.err"
  write_failed_fixture "missing-manifest" "missing package manifest rejected"
}

fixture_missing_helper() {
  local tmp package_root arch
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-first-run-fixture.XXXXXX")"
  package_root="$(build_package "$tmp")"
  arch="$(go env GOARCH)"
  rm -f "$package_root/bin/hideout-hostfsd-linux-$arch"
  if "$package_root/install.sh" --prefix "$tmp/install" --store "$tmp/store" --skip-init >"$logs/missing-helper.out" 2>"$logs/missing-helper.err"; then
    echo "first-run-e2e: missing-helper fixture unexpectedly passed" >&2
    exit 1
  fi
  grep -q 'hideout-hostfsd-linux' "$logs/missing-helper.err"
  write_failed_fixture "missing-helper" "missing package-owned helper rejected"
}

fixture_stale_obsolete() {
  local tmp package_root prefix store stale
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-first-run-fixture.XXXXXX")"
  package_root="$(build_package "$tmp")"
  prefix="$tmp/install"
  store="$tmp/store"
  "$package_root/install.sh" --prefix "$prefix" --store "$store" --skip-init >"$logs/stale-install.out" 2>"$logs/stale-install.err"
  stale="$tmp/stale-package"
  cp -R "$package_root" "$stale"
  jq '
    .layout.entrypoints = (.layout.entrypoints | map(select(. != "README.zh-CN.md"))) |
    .files = (.files | map(select(.path != "README.zh-CN.md")))
  ' "$stale/package-manifest.json" >"$stale/package-manifest.json.tmp"
  mv "$stale/package-manifest.json.tmp" "$stale/package-manifest.json"
  "$stale/install.sh" --prefix "$prefix" --store "$store" --skip-init >"$logs/stale-upgrade.out" 2>"$logs/stale-upgrade.err"
  if "$prefix/bin/hideout" package verify "$prefix" >"$logs/stale-verify.out" 2>"$logs/stale-verify.err"; then
    echo "first-run-e2e: stale-obsolete fixture unexpectedly passed" >&2
    exit 1
  fi
  grep -q 'package repair --prefix' "$logs/stale-verify.err"
  write_failed_fixture "stale-obsolete" "obsolete package-owned file rejected with repair hint"
}

fixture_duplicate_profile() {
  local tmp package_root prefix store hideout
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-first-run-fixture.XXXXXX")"
  package_root="$(build_package "$tmp")"
  prefix="$tmp/install"
  store="$tmp/store"
  install_skip_init "$package_root" "$prefix" "$store"
  hideout="$prefix/bin/hideout"
  cleanup_daemon_binary="$hideout"
  cleanup_daemon_store="$store"
  cleanup_daemon_store_owned=1
  HIDEOUT_STORE_ROOT="$store" "$hideout" init --template dev --profile default --backend native --network direct --no-input >"$logs/duplicate-profile-first.out" 2>"$logs/duplicate-profile-first.err"
  if HIDEOUT_STORE_ROOT="$store" "$hideout" init --template dev --profile default --backend native --network direct --no-input >"$logs/duplicate-profile.out" 2>"$logs/duplicate-profile.err"; then
    echo "first-run-e2e: duplicate-profile fixture unexpectedly passed" >&2
    exit 1
  fi
	grep -Eiq 'exists|already' "$logs/duplicate-profile.err" "$logs/duplicate-profile.out"
  write_failed_fixture "duplicate-profile" "duplicate default profile rejected"
}

fixture_unsafe_workspace() {
  local tmp package_root prefix store hideout
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-first-run-fixture.XXXXXX")"
  package_root="$(build_package "$tmp")"
  prefix="$tmp/install"
  store="$tmp/store"
  install_skip_init "$package_root" "$prefix" "$store"
  hideout="$prefix/bin/hideout"
  HIDEOUT_STORE_ROOT="$store" "$hideout" init --template dev --profile default --backend native --network direct --no-input >"$logs/unsafe-init.out" 2>"$logs/unsafe-init.err"
  if HIDEOUT_STORE_ROOT="$store" "$hideout" run --profile default --backend native --allow-weak-isolation --workspace "$store" -- pwd >"$logs/unsafe-workspace.out" 2>"$logs/unsafe-workspace.err"; then
    echo "first-run-e2e: unsafe-workspace fixture unexpectedly passed" >&2
    exit 1
  fi
	grep -Eiq 'workspace|reserved|store' "$logs/unsafe-workspace.err" "$logs/unsafe-workspace.out"
  write_failed_fixture "unsafe-workspace" "reserved workspace/store path rejected"
}

fixture_redaction() {
  printf 'cap_0123456789abcdef0123456789abcdef\n' >"$logs/redaction-secret.log"
  if ! grep -R 'cap_0123456789abcdef0123456789abcdef' "$logs" >/dev/null; then
    echo "first-run-e2e: redaction fixture setup failed" >&2
    exit 1
  fi
  rm -f "$logs/redaction-secret.log"
  write_failed_fixture "redaction" "control-plane material injection is detected by scan"
}

real_backend() {
  local missing=()
  command -v limactl >/dev/null 2>&1 || missing+=("limactl")
  command -v tun2socks >/dev/null 2>&1 || missing+=("tun2socks")
  if [ -z "${HIDEOUT_SECRET_PROXY_URL:-}" ]; then
    missing+=("HIDEOUT_SECRET_PROXY_URL")
  fi
  if [ "${#missing[@]}" -gt 0 ]; then
    local reason="missing real backend prerequisites: ${missing[*]}"
    printf '%s\n' "$reason" >"$reports/real-backend-prerequisites.txt"
    add_proof "022.first-run.real-backend.not-run" "real-gate" "not-run" \
      "real backend prerequisites unavailable" "022.FR-008" \
      "Real backend proof is explicit and prerequisite gated" "backend" \
      "docs-report" "reports/real-backend-prerequisites.txt" \
      "real backend prerequisite report" "$reason" "real-backend" "missing" "$reason"
    write_manifest
    validate_evidence
    echo "first-run-e2e: real-backend not-run evidence=$manifest"
    if [ "$require_real" -eq 1 ]; then
      exit 1
    fi
    return
  fi

  local tmp package_root prefix store workspace hideout
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-first-run-real.XXXXXX")"
  package_root="$(build_package "$tmp")"
  prefix="$tmp/install"
  store="$tmp/store"
  workspace="$tmp/workspace"
  mkdir -p "$workspace"
  install_skip_init "$package_root" "$prefix" "$store"
  hideout="$prefix/bin/hideout"
  run_logged verify "$hideout" package verify "$prefix"
  run_logged init-real env HIDEOUT_STORE_ROOT="$store" "$hideout" init \
    --template privacy \
    --profile default \
    --backend lima \
    --network tun2socks \
    --proxy-secret proxy-url \
    --mediated-resolver 1.1.1.1 \
    --no-input
  run_logged run-real env HIDEOUT_STORE_ROOT="$store" "$hideout" run \
    --profile default \
    --backend lima \
    --workspace "$workspace" \
    --verbose \
    -- pwd
  grep -q 'Hideout boundary:' "$logs/run-real.err"
  add_proof "022.first-run.real-backend" "real-gate" "passed" \
    "real Lima/privacy first-run executed" "022.FR-008" \
    "Real backend proof passes only through the real backend path" "backend" \
    "log" "logs/run-real.err" "real backend Boundary output"
  write_manifest
  validate_evidence
  echo "first-run-e2e: real-backend passed evidence=$manifest"
}

setup_real_backend() {
  local missing=()
  command -v limactl >/dev/null 2>&1 || missing+=("limactl")
  if [ "$(go env GOOS)" != "darwin" ] || [ "$(go env GOARCH)" != "arm64" ]; then
    missing+=("darwin/arm64")
  fi
  if [ "${#missing[@]}" -gt 0 ]; then
    local reason="missing setup real-backend prerequisites: ${missing[*]}"
    printf '%s\n' "$reason" >"$reports/setup-real-backend-prerequisites.txt"
    add_proof "038.setup.real-gate2.not-run" "real-gate" "not-run" \
      "setup real backend prerequisites unavailable" \
      "038.FR-024,038.FR-028,038.SC-006,038.SC-011" \
      "Real setup proof is explicit and prerequisite gated" "backend" \
      "docs-report" "reports/setup-real-backend-prerequisites.txt" \
      "setup real backend prerequisite report" "$reason" "setup-real-backend" "missing" "$reason" \
      "038-zero-friction-setup"
    write_manifest
    validate_evidence
    echo "first-run-e2e: setup-real-backend not-run evidence=$manifest"
    if [ "$require_real" -eq 1 ]; then
      exit 1
    fi
    return
  fi

  local tmp package_root prefix store workspace hideout env_name env_id instance first_env_id second_env_id runtime_binding
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-setup-real.XXXXXX")"
  package_root="$(build_package "$tmp")"
  prefix="$tmp/install"
  store="$(hideout_mktemp_daemon_store)"
  workspace="$tmp/workspace"
  mkdir -p "$workspace"
  cp scripts/test-runtime-agent-install.sh "$workspace/test-runtime-agent-install.sh"
  chmod 0755 "$workspace/test-runtime-agent-install.sh"
  install_skip_init "$package_root" "$prefix" "$store"
  hideout="$prefix/bin/hideout"
  manifest_package_identity_path="$reports/setup-package-identity.json"
  if ! "$hideout" support release package-identity \
    --archive "$tmp/hideout.tar.gz" --out "$manifest_package_identity_path" \
    >"$logs/setup-package-identity.out" 2>"$logs/setup-package-identity.err"; then
    echo "first-run-e2e: setup package-identity failed:" >&2
    tail -20 "$logs/setup-package-identity.out" "$logs/setup-package-identity.err" >&2 || true
    exit 1
  fi
  cleanup_daemon_binary="$hideout"
  cleanup_daemon_store="$store"
  cleanup_daemon_store_owned=1

  go run ./test/e2e/setuppty \
    --hideout "$hideout" --store "$store" --out "$logs/setup-real-pty.out"

  run_logged setup-real-first env HIDEOUT_STORE_ROOT="$store" "$hideout" run \
    --workspace "$workspace" --verbose -- sh -c '
set -eu
printf "pwd=%s\n" "$PWD"
printf "target_uid=%s\n" "$(id -u)"
printf "target_user=%s\n" "$(id -un)"
printf "target_home=%s\n" "$HOME"
printf "passwd_home=%s\n" "$(getent passwd "$(id -u)" | cut -d: -f6)"
printf "path=%s\n" "$PATH"
'
  grep -q '^pwd=/workspace$' "$logs/setup-real-first.out"
  grep -q '^target_user=developer$' "$logs/setup-real-first.out"
  grep -q '^target_home=/hideout/profile/home$' "$logs/setup-real-first.out"
  grep -q '^passwd_home=/home/developer$' "$logs/setup-real-first.out"
  grep -q '/hideout/profile/home/.local/bin' "$logs/setup-real-first.out"
  if grep -q '^target_uid=0$' "$logs/setup-real-first.out"; then
    echo "first-run-e2e: setup target ran as root" >&2
    exit 1
  fi
  grep -q 'Hideout boundary:' "$logs/setup-real-first.err"

  run_logged setup-real-env-list env HIDEOUT_STORE_ROOT="$store" "$hideout" env list
  env_name="$(awk 'NR == 2 {print $1}' "$logs/setup-real-env-list.out")"
  env_id="$(awk 'NR == 2 {print $NF}' "$logs/setup-real-env-list.out")"
  if [ -z "$env_name" ] || [ -z "$env_id" ] || [ "$env_name" = "environments:" ]; then
    echo "first-run-e2e: could not identify setup environment" >&2
    exit 1
  fi
  cleanup_environment_name="$env_name"
  wait_environment_status "$hideout" "$store" "$env_name" stopped setup-real-env-first
  grep -q "  id: $env_id" "$logs/setup-real-env-first.out"
  grep -q '  mode: shared' "$logs/setup-real-env-first.out"
  grep -q '  status: stopped' "$logs/setup-real-env-first.out"
  grep -q 'family: developer-standard revision=2026.07.0' "$logs/setup-real-env-first.out"
  instance="$(awk '/^  instance: / {print $2}' "$logs/setup-real-env-first.out")"
  test -n "$instance"
  runtime_binding="$(setup_runtime_binding "$package_root" "$env_id")"

  run_logged setup-agent-install env HIDEOUT_STORE_ROOT="$store" "$hideout" run \
    --workspace "$workspace" -- sh /workspace/test-runtime-agent-install.sh --guest
  grep -q 'runtime_agent_version=.*0.144.1' "$logs/setup-agent-install.out"
  grep -q 'runtime_agent_integrity=passed' "$logs/setup-agent-install.out"
  grep -q 'runtime_agent_target_owner=passed' "$logs/setup-agent-install.out"
  grep -q 'runtime_agent_no_sudo=passed' "$logs/setup-agent-install.out"
  grep -q 'runtime_agent_no_auth=passed' "$logs/setup-agent-install.out"
  grep -q 'runtime_agent_secret_scan=passed' "$logs/setup-agent-install.out"

  run_logged setup-agent-run env HIDEOUT_STORE_ROOT="$store" "$hideout" run \
    --workspace "$workspace" -- codex --version
  grep -q '0.144.1' "$logs/setup-agent-run.out"
  wait_environment_status "$hideout" "$store" "$env_name" stopped setup-real-env-second
  first_env_id="$(awk '/^  id: / {print $2}' "$logs/setup-real-env-first.out")"
  second_env_id="$(awk '/^  id: / {print $2}' "$logs/setup-real-env-second.out")"
  test "$first_env_id" = "$second_env_id"
  grep -q "  instance: $instance" "$logs/setup-real-env-second.out"
  grep -q '  status: stopped' "$logs/setup-real-env-second.out"

  run_logged setup-real-audit env HIDEOUT_STORE_ROOT="$store" "$hideout" audit show --limit 100
  test -s "$logs/setup-real-audit.out"
  if grep -R -E 'OPENAI_API_KEY=|HIDEOUT_SECRET_|socks5://[^[:space:]]+:[^[:space:]]+@' \
      "$logs/setup-real-first.out" "$logs/setup-agent-install.out" "$logs/setup-agent-run.out" >/dev/null 2>&1; then
    echo "first-run-e2e: setup real lane exposed credential material" >&2
    exit 1
  fi

  add_proof "038.setup.real-gate2.first-run" "real-gate" "passed" \
    "packaged setup and real Lima first run completed with exact runtime and reuse" \
    "038.FR-022,038.FR-023,038.FR-024,038.FR-025,038.SC-006,038.SC-007,038.SC-008,038.SC-016" \
    "Real setup proves workspace, identity, runtime, audit, lifecycle, and reuse" "backend" \
    "log" "logs/setup-real-env-second.out" "real setup environment inspection" \
    "" "setup-real-backend" "available" "" "038-zero-friction-setup" "$runtime_binding"
  add_proof "038.setup.real-gate2.agent-install-run" "real-gate" "passed" \
    "exact-integrity Codex fixture installed and ran by name in a later session" \
    "038.FR-033,038.FR-034,038.SC-014" \
    "Named exact agent installs as target and persists across sessions" "agent" \
    "log" "logs/setup-agent-install.out" "exact agent install proof" \
    "" "setup-real-backend" "available" "" "038-zero-friction-setup" "$runtime_binding" \
    "log" "logs/setup-agent-run.out" "separate-session agent run proof"
  write_manifest
  validate_evidence

  run_logged setup-real-remove env HIDEOUT_STORE_ROOT="$store" "$hideout" env remove "$env_name" --force
  cleanup_environment_name=""
  run_logged setup-real-daemon-stop env HIDEOUT_STORE_ROOT="$store" "$hideout" daemon stop
  rm -rf "$store"
  cleanup_daemon_binary=""
  cleanup_daemon_store=""
  cleanup_daemon_store_owned=0
  echo "first-run-e2e: setup-real-backend passed evidence=$manifest"
}

case "$fixture" in
  "")
    ;;
  bad-checksum)
    fixture_bad_checksum
    ;;
  missing-manifest)
    fixture_missing_manifest
    ;;
  missing-helper)
    fixture_missing_helper
    ;;
  stale-obsolete)
    fixture_stale_obsolete
    ;;
  duplicate-profile)
    fixture_duplicate_profile
    ;;
  unsafe-workspace)
    fixture_unsafe_workspace
    ;;
  redaction)
    fixture_redaction
    ;;
  *)
    echo "first-run-e2e: unknown fixture: $fixture" >&2
    usage >&2
    exit 2
    ;;
esac

case "$mode" in
  local-fast)
    local_fast
    ;;
  real-backend)
    real_backend
    ;;
  setup-local-fast)
    setup_local_fast
    ;;
  setup-real-backend)
    setup_real_backend
    ;;
  *)
    echo "first-run-e2e: unknown mode: $mode" >&2
    exit 2
    ;;
esac
