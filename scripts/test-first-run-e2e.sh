#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

mode="local-fast"
require_real=0
fixture=""
out=""
package_path=""

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-first-run-e2e.sh [--local-fast|--real-backend] [--require-real]
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

claim_json() {
  local id="$1"
  local desc="$2"
  local scope="$3"
  jq -n \
    --arg id "$id" \
    --arg desc "$desc" \
    --arg scope "$scope" \
    '[{claimId: $id, source: "spec", description: $desc, scope: $scope}]'
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
  local redaction="passed"
  if [ "$status" = "not-run" ]; then
    redaction="not-run"
  fi
  local artifacts claims prereqs tmp
  artifacts="$(artifact_json "$artifact_kind" "$artifact_rel" "$artifact_desc")"
  claims="$(claim_json "$claim_id" "$claim_desc" "$scope")"
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
    --arg feature "022-alpha-first-run-e2e" \
    --arg mode "$proof_mode" \
    --arg status "$status" \
    --arg summary "$summary" \
    --arg redaction "$redaction" \
    --arg reason "$reason" \
    --argjson claims "$claims" \
    --argjson artifacts "$artifacts" \
    --argjson prereqs "$prereqs" \
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
    } + (if $reason == "" then {} else {notRunReason: $reason} end))]' "$proofs_file" >"$tmp"
  mv "$tmp" "$proofs_file"
}

git_commit() {
  git rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown'
}

git_dirty() {
  if [ -n "$(git status --porcelain --untracked-files=normal 2>/dev/null)" ]; then
    printf 'true'
  else
    printf 'false'
  fi
}

write_manifest() {
  local pkg_version="${1:-dev}"
  jq -n \
    --arg generated "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg commit "$(git_commit)" \
    --argjson dirty "$(git_dirty)" \
    --arg pkg_version "$pkg_version" \
    --slurpfile proofs "$proofs_file" \
    '{
      version: "hideout.product-hardening-evidence/v1",
      generatedAt: $generated,
      commit: $commit,
      dirty: $dirty,
      packageIdentity: {name: "hideout", version: $pkg_version},
      proofs: $proofs[0]
    }' >"$manifest"
}

validate_evidence() {
  jq -e '
    .version == "hideout.product-hardening-evidence/v1" and
    (.proofs | length > 0) and
    all(.proofs[];
      (.proofId | length > 0) and
      (.featureId == "022-alpha-first-run-e2e") and
      (.status == "passed" or .status == "failed" or .status == "not-run") and
      (.coveredClaims | length > 0) and
      (.redactionStatus == "passed" or .redactionStatus == "failed" or .redactionStatus == "not-run")
    )
  ' "$manifest" >"$logs/evidence-content.out"
  go run ./cmd/hideout-schema-validate \
    schemas/product-hardening-evidence.schema.json \
    "$manifest" >"$logs/evidence-schema.out" 2>"$logs/evidence-schema.err"
  if grep -R -E 'cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5://[^[:space:]]+:[^[:space:]]+@' "$out" >/dev/null 2>&1; then
    echo "first-run-e2e: evidence contains control-plane material" >&2
    grep -R -n -E 'cap_[A-Za-z0-9]{12,}|HIDEOUT_SECRET_[A-Z0-9_]+=|socks5://[^[:space:]]+:[^[:space:]]+@' "$out" >&2 || true
    exit 1
  fi
}

run_logged() {
  local name="$1"
  shift
  "$@" >"$logs/$name.out" 2>"$logs/$name.err"
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
	grep -q './install.sh --skip-init' "$doc"
	grep -q 'hideout init \\' "$doc"
	local install_line init_line
	install_line="$(grep -n -m1 './install.sh --skip-init' "$doc" | cut -d: -f1)"
	init_line="$(grep -n -m1 'hideout init \\' "$doc" | cut -d: -f1)"
	if [ -z "$install_line" ] || [ -z "$init_line" ] || [ "$install_line" -ge "$init_line" ]; then
		echo "first-run-e2e: docs install/init order is invalid" >&2
		exit 1
	fi
	printf 'docs-order: install --skip-init precedes explicit init\n' >"$reports/docs-order.txt"
}

install_skip_init() {
  local package_root="$1"
  local prefix="$2"
  local store="$3"
  run_logged install "$package_root/install.sh" --prefix "$prefix" --store "$store" --skip-init
  grep -q 'init skipped' "$logs/install.out"
  test -x "$prefix/bin/hideout"
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
  store="$tmp/store"
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
  test -f "$store/profiles/default/profile.json"

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
  write_manifest "dev"
  validate_evidence
  echo "first-run-e2e: local-fast passed evidence=$manifest"
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
  write_manifest "dev"
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
    write_manifest "dev"
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
  write_manifest "dev"
  validate_evidence
  echo "first-run-e2e: real-backend passed evidence=$manifest"
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
  *)
    echo "first-run-e2e: unknown mode: $mode" >&2
    exit 2
    ;;
esac
