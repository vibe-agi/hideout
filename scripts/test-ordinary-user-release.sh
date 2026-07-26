#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/gate-result.sh"

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-ordinary-user-release.sh --local-fast [--out <path>]
  scripts/test-ordinary-user-release.sh --release-candidate \
    --package-root <dir> --package-artifact <tar.gz> \
    --gate2-evidence <json> --gate3-evidence <json> --gate3-log <path> \
    --gate2-product-evidence <json> --gate3-product-evidence <json> \
    --ui-evidence <json> --clean-install <json> \
    --signing-observation <json> --notarization-observation <json> \
    [--out <path>]

Local-fast writes schema-valid 044 targeted-completion evidence but cannot
promote a real or public claim. Release-candidate mode binds the exact package,
real Gate 2/3, required UI, clean-install, signing, and notarization inputs.
USAGE
}

mode=""
package_root=""
package_artifact=""
gate2_evidence=""
gate3_evidence=""
gate3_log=""
gate2_product_evidence=""
gate3_product_evidence=""
ui_evidence=""
clean_install=""
signing_observation=""
notarization_observation=""
out=""
gate_completed=0
work=""

cleanup() {
  exit_status=$?
  trap - EXIT
  if [ -n "$work" ] && [ -d "$work" ]; then
    find "$work" -depth -type f -delete 2>/dev/null || true
    find "$work" -depth -type l -delete 2>/dev/null || true
    find "$work" -depth -type d -empty -delete 2>/dev/null || true
  fi
  if [ "$gate_completed" != "1" ]; then
    gate_require_completion "ordinary-user-release"
  fi
  exit "$exit_status"
}
trap cleanup EXIT

while [ "$#" -gt 0 ]; do
  case "$1" in
    --local-fast) mode="local-fast"; shift ;;
    --release-candidate) mode="release-candidate"; shift ;;
    --package-root) package_root="${2:-}"; shift 2 ;;
    --package-artifact) package_artifact="${2:-}"; shift 2 ;;
    --gate2-evidence) gate2_evidence="${2:-}"; shift 2 ;;
    --gate3-evidence) gate3_evidence="${2:-}"; shift 2 ;;
    --gate3-log) gate3_log="${2:-}"; shift 2 ;;
    --gate2-product-evidence) gate2_product_evidence="${2:-}"; shift 2 ;;
    --gate3-product-evidence) gate3_product_evidence="${2:-}"; shift 2 ;;
    --ui-evidence) ui_evidence="${2:-}"; shift 2 ;;
    --clean-install) clean_install="${2:-}"; shift 2 ;;
    --signing-observation) signing_observation="${2:-}"; shift 2 ;;
    --notarization-observation) notarization_observation="${2:-}"; shift 2 ;;
    --out) out="${2:-}"; shift 2 ;;
    -h|--help)
      usage
      gate_completed=1
      exit 0
      ;;
    *)
      echo "ordinary-user-release: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$mode" in
  local-fast) ;;
  release-candidate)
    for pair in \
      "package-root:$package_root" \
      "package-artifact:$package_artifact" \
      "gate2-evidence:$gate2_evidence" \
      "gate3-evidence:$gate3_evidence" \
      "gate3-log:$gate3_log" \
      "gate2-product-evidence:$gate2_product_evidence" \
      "gate3-product-evidence:$gate3_product_evidence" \
      "ui-evidence:$ui_evidence" \
      "clean-install:$clean_install" \
      "signing-observation:$signing_observation" \
      "notarization-observation:$notarization_observation"; do
      label="${pair%%:*}"
      value="${pair#*:}"
      if [ -z "$value" ] || [ ! -e "$value" ]; then
        echo "ordinary-user-release: release-candidate requires existing --$label" >&2
        exit 2
      fi
    done
    package_root="$(cd "$package_root" && pwd -P)"
    ;;
  *)
    echo "ordinary-user-release: choose exactly one mode" >&2
    exit 2
    ;;
esac

for command in go jq; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "ordinary-user-release: missing required command: $command" >&2
    exit 127
  }
done

for required in \
  specs/044-ordinary-user-release/spec.md \
  specs/044-ordinary-user-release/plan.md \
  specs/044-ordinary-user-release/tasks.md \
  specs/044-ordinary-user-release/acceptance.md \
  specs/044-ordinary-user-release/contracts/cli-journey.md \
  specs/044-ordinary-user-release/contracts/support-report.md \
  specs/044-ordinary-user-release/contracts/package-privacy-helper.md \
  specs/044-ordinary-user-release/contracts/ordinary-user-acceptance.md; do
  [ -f "$required" ] || {
    echo "ordinary-user-release: missing contract: $required" >&2
    exit 1
  }
done

work="$(mktemp -d "${TMPDIR:-/tmp}/hideout-ordinary-user-release.XXXXXX")"
work="$(cd "$work" && pwd -P)"
if [ -z "$out" ]; then
  retained="$(mktemp -d "${TMPDIR:-/tmp}/hideout-ordinary-user-evidence.XXXXXX")"
  out="$retained/product-hardening-evidence.json"
else
  case "$out" in
    /*) ;;
    *) out="$PWD/$out" ;;
  esac
fi
out_parent="$(dirname "$out")"
out_name="$(basename "$out" .json)"
artifact_root="$out_parent/$out_name.artifacts"
artifact_rel_root="$(basename "$artifact_root")"
if [ -e "$out" ] || [ -e "$artifact_root" ]; then
  echo "ordinary-user-release: output already exists: $out or $artifact_root" >&2
  exit 2
fi
mkdir -p "$out_parent" "$artifact_root"

sha256_file() {
  gate_sha256_file "$1"
}

retain_artifact() {
  source_path="$1"
  name="$2"
  [ -f "$source_path" ] || {
    echo "ordinary-user-release: retained artifact source is missing: $source_path" >&2
    return 1
  }
  cp "$source_path" "$artifact_root/$name"
  printf '%s/%s\n' "$artifact_rel_root" "$name"
}

artifact_json() {
  relative="$1"
  kind="$2"
  description="$3"
  jq -n \
    --arg kind "$kind" \
    --arg path "$relative" \
    --arg sha256 "$(sha256_file "$out_parent/$relative")" \
    --arg description "$description" \
    '{kind:$kind,path:$path,sha256:$sha256,redactionStatus:"passed",description:$description}'
}

claims_json() {
  proof_id="$1"
  jq -c --arg id "$proof_id" '
    [.requirements[] | select(.proofId == $id) | .claimIds[] |
      {claimId:.,source:"spec",description:"044 ordinary-user release requirement"}]
  ' "$work/proof-registry.json"
}

append_proof() {
  proof_id="$1"
  proof_mode="$2"
  evidence_class="$3"
  command_summary="$4"
  artifacts_json="$5"
  runtime_json="${6:-null}"
  jq -n \
    --arg proofId "$proof_id" \
    --arg mode "$proof_mode" \
    --arg evidenceClass "$evidence_class" \
    --arg commandSummary "$command_summary" \
    --argjson claims "$(claims_json "$proof_id")" \
    --argjson artifacts "$artifacts_json" \
    --argjson runtime "$runtime_json" '
    {
      proofId:$proofId,
      featureId:"044-ordinary-user-release",
      mode:$mode,
      evidenceClass:$evidenceClass,
      status:"passed",
      commandSummary:$commandSummary,
      coveredClaims:$claims,
      prerequisites:[{name:"044-acceptance",status:"available"}],
      artifacts:$artifacts,
      redactionStatus:"passed"
    } + if $runtime == null then {} else {runtime:$runtime} end
  ' >>"$work/proofs.jsonl"
}

commit="$(git rev-parse HEAD)"
dirty=false
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  dirty=true
fi
go run ./cmd/hideout support proof-registry --json >"$work/proof-registry.json"
: >"$work/proofs.jsonl"

# Targeted local contracts. The recovery lane includes real package staging,
# helper provenance, prior-version upgrade, repair, uninstall, purge, and
# destructive-scope fixtures.
go test ./internal/app ./internal/doctor ./internal/supportreport \
  ./internal/helperbin ./internal/packagekit ./internal/productevidence \
  >"$work/local-tests.out"
scripts/test-first-run-docs-smoke.sh >"$work/first-run-docs.out"
scripts/test-doctor-package-recovery-e2e.sh --local-fast \
  --out "$work/recovery-evidence" >"$work/recovery.out"

support_bin="$work/hideout"
if [ "$mode" = "release-candidate" ]; then
  support_bin="$package_root/bin/hideout"
  "$support_bin" package verify "$package_root" >"$work/package-verify.out"
else
  go build -trimpath -o "$support_bin" ./cmd/hideout
fi

# Exercise the real support-report command with injected protected material.
support_home="$work/support-home"
support_workspace="$work/support-workspace"
support_out_dir="$work/support-output"
support_report="$support_out_dir/hideout-support.json"
support_token="cap_0123456789abcdef0123456789abcdef"
support_ui_token="ui_0123456789abcdef0123456789abcdef"
support_proxy="socks5://user:password@127.0.0.1:1080"
support_workspace_sentinel="PRIVATE_WORKSPACE_SENTINEL_044"
mkdir -p "$support_home" "$support_workspace" "$support_out_dir"
chmod 700 "$support_home" "$support_workspace" "$support_out_dir"
printf '%s\n' "$support_workspace_sentinel" >"$support_workspace/private.txt"
env \
  HOME="$support_home" \
  HIDEOUT_SECRET_DEFAULT_PROXY="$support_proxy" \
  HIDEOUT_UI_TOKEN="$support_ui_token" \
  HIDEOUT_TEST_CAPABILITY="$support_token" \
  "$support_bin" support report \
    --out "$support_report" \
    --profile missing \
    --backend lima \
    --workspace "$support_workspace" \
    >"$work/support-report.out"
go run ./cmd/hideout-schema-validate schemas/support-report.schema.json "$support_report"
support_size="$(wc -c <"$support_report" | tr -d '[:space:]')"
[ "$support_size" -le 1048576 ] || {
  echo "ordinary-user-release: support report exceeds 1 MiB" >&2
  exit 1
}
if stat -f '%Lp' "$support_report" >/dev/null 2>&1; then
  support_mode="$(stat -f '%Lp' "$support_report")"
else
  support_mode="$(stat -c '%a' "$support_report")"
fi
[ "$support_mode" = "600" ] || {
  echo "ordinary-user-release: support report mode=$support_mode want=600" >&2
  exit 1
}
for forbidden in \
  "$support_home" "$support_workspace" "$support_workspace_sentinel" \
  "$support_token" "$support_ui_token" "$support_proxy" \
  "HIDEOUT_SECRET_DEFAULT_PROXY"; do
  if grep -Fq "$forbidden" "$support_report"; then
    echo "ordinary-user-release: support report leaked protected material" >&2
    exit 1
  fi
done
[ ! -e "$support_home/.hideout" ] || {
  echo "ordinary-user-release: support report mutated the default store" >&2
  exit 1
}
if "$support_bin" support report --out "$support_report" \
  >"$work/support-overwrite.out" 2>"$work/support-overwrite.err"; then
  echo "ordinary-user-release: support report overwrote an existing file" >&2
  exit 1
fi
support_symlink="$support_out_dir/support-symlink.json"
ln -s "$support_report" "$support_symlink"
if "$support_bin" support report --out "$support_symlink" --overwrite \
  >"$work/support-symlink.out" 2>"$work/support-symlink.err"; then
  echo "ordinary-user-release: support report accepted a symlink" >&2
  exit 1
fi

local_tests_rel="$(retain_artifact "$work/local-tests.out" local-tests.out)"
docs_rel="$(retain_artifact "$work/first-run-docs.out" first-run-docs.out)"
recovery_rel="$(retain_artifact "$work/recovery-evidence/product-hardening-evidence.json" recovery-evidence.json)"
support_rel="$(retain_artifact "$support_report" support-report.json)"
local_artifacts="$(jq -s '.' \
  <(artifact_json "$local_tests_rel" "log" "focused ordinary-user contracts") \
  <(artifact_json "$recovery_rel" "manifest" "package recovery and lifecycle evidence") \
  <(artifact_json "$support_rel" "manifest" "bounded local-only support report"))"
docs_artifacts="$(jq -s '.' \
  <(artifact_json "$docs_rel" "docs-report" "first-run documentation contract"))"
append_proof \
  "044.ordinary-user.gate0.journeys" "local-fast" \
  "ordinary-user-local-journeys" \
  "help, doctor, support, package helper, upgrade, repair, uninstall, and purge contracts" \
  "$local_artifacts"
append_proof \
  "044.ordinary-user.docs.truth" "docs" \
  "ordinary-user-docs-truth" \
  "first-run and lifecycle wording remains bounded to the prerelease support contract" \
  "$docs_artifacts"

package_json="null"
if [ "$mode" = "release-candidate" ]; then
  package_manifest="$package_root/package-manifest.json"
  candidate_commit="$(jq -er '.source.commit' "$package_manifest")"
  candidate_dirty="$(jq -er '.source.dirty' "$package_manifest")"
  candidate_version="$(jq -er '.release.productVersion' "$package_manifest")"
  candidate_os="$(jq -er '.target.hostOS' "$package_manifest")"
  candidate_arch="$(jq -er '.target.hostArch' "$package_manifest")"
  if [ "$candidate_commit" != "$commit" ] || [ "$candidate_dirty" != "false" ] ||
    [ "$dirty" != "false" ]; then
    echo "ordinary-user-release: candidate is not bound to the clean current commit" >&2
    exit 1
  fi
  if ! git rev-parse --verify origin/master >/dev/null 2>&1 ||
    ! git merge-base --is-ancestor "$candidate_commit" origin/master; then
    echo "ordinary-user-release: candidate commit is not public on origin/master" >&2
    exit 1
  fi
  package_json="$(jq -n \
    --arg productVersion "$candidate_version" \
    --arg sourceCommit "$candidate_commit" \
    --arg artifactSHA256 "$(sha256_file "$package_artifact")" \
    --arg hostOS "$candidate_os" \
    --arg hostArch "$candidate_arch" '
    {name:"hideout",productVersion:$productVersion,sourceCommit:$sourceCommit,
      artifactSHA256:$artifactSHA256,hostOS:$hostOS,hostArch:$hostArch}')"

  for gate_pair in \
    "gate2-lima:$gate2_evidence" \
    "gate3-hidden-proxy:$gate3_evidence"; do
    gate_id="${gate_pair%%:*}"
    gate_path="${gate_pair#*:}"
    jq -e --arg id "$gate_id" '
      .id == $id and .backend == "lima" and .result == "passed" and
      .runtime.schema == "hideout.runtime-evidence-binding/v1" and
      .runtime.buildDirty == false
    ' "$gate_path" >/dev/null || {
      echo "ordinary-user-release: required real gate is missing, failed, not-run, or unbound: $gate_id" >&2
      exit 1
    }
  done
  gate2_runtime="$(jq -c '.runtime' "$gate2_evidence")"
  gate3_runtime="$(jq -c '.runtime' "$gate3_evidence")"
  jq -e --arg commit "$commit" '
    .version == "hideout.product-hardening-evidence/v1" and
    .commit == $commit and .dirty == false and
    any(.proofs[]; .status == "passed" and .runtime != null)
  ' "$gate2_product_evidence" "$gate3_product_evidence" >/dev/null
  jq -e --arg commit "$commit" '
    .version == "hideout.product-hardening-evidence/v1" and
    .commit == $commit and .dirty == false and
    all(.proofs[]; .status == "passed")
  ' "$ui_evidence" >/dev/null || {
    echo "ordinary-user-release: required UI evidence is missing, failed, or not-run" >&2
    exit 1
  }
  for marker in \
    "tun2socks_source=package-owned" \
    "tun2socks_upstream_version=v2.6.0" \
    "gate3: using package-owned tun2socks helper" \
    "gateway_forward_path=passed" \
    "gate3: passed"; do
    grep -Fq "$marker" "$gate3_log" || {
      echo "ordinary-user-release: Gate 3 log is missing package/privacy marker: $marker" >&2
      exit 1
    }
  done

  "$support_bin" support release validate-signing \
    --package-root "$package_root" --observation "$signing_observation" >/dev/null
  "$support_bin" support release validate-notarization \
    --package-root "$package_root" --observation "$notarization_observation" >/dev/null

  # Exercise setup and installed UI/package lifecycle with a PATH that contains
  # neither Go nor an ambient tun2socks helper.
  setup_store="$work/setup-store"
  setup_transcript="$work/setup-transcript.out"
  setuppty="$work/setuppty"
  go build -trimpath -o "$setuppty" ./test/e2e/setuppty
  runtime_bin="$work/runtime-bin"
  mkdir -p "$runtime_bin"
  if command -v limactl >/dev/null 2>&1; then
    ln -s "$(command -v limactl)" "$runtime_bin/limactl"
  fi
  runtime_path="$runtime_bin:/usr/bin:/bin:/usr/sbin:/sbin"
  if PATH="$runtime_path" command -v go >/dev/null 2>&1; then
    echo "ordinary-user-release: sanitized package PATH still contains Go" >&2
    exit 1
  fi
  (
    cd "$work"
    env PATH="$runtime_path" "$setuppty" \
      --hideout "$package_root/bin/hideout" \
      --store "$setup_store" \
      --out "$setup_transcript"
  )
  for expected in \
    "Hideout configuration is ready." \
    "hideout doctor" \
    "hideout run -- git status --short" \
    "does not hide your network origin" \
    "no VM start or runtime download"; do
    grep -Fq "$expected" "$setup_transcript" || {
      echo "ordinary-user-release: package setup transcript is missing: $expected" >&2
      exit 1
    }
  done
  env PATH="$runtime_path" HIDEOUT_STORE_ROOT="$setup_store" \
    "$package_root/bin/hideout" help >"$work/package-help.out"
  env PATH="$runtime_path" HIDEOUT_STORE_ROOT="$setup_store" \
    "$package_root/bin/hideout" doctor >"$work/package-doctor.out"

  installed_prefix="$work/installed"
  installed_store="$work/installed-store"
  "$package_root/install.sh" --prefix "$installed_prefix" \
    --store "$installed_store" --skip-init >"$work/install.out"
  PATH="$runtime_path" "$installed_prefix/bin/hideout" package verify "$installed_prefix" \
    >"$work/installed-verify.out"
  HIDEOUT_STORE_ROOT="$setup_store" PATH="$runtime_path" \
    "$installed_prefix/bin/hideout" tui --once >"$work/package-tui.out"
  HIDEOUT_STORE_ROOT="$setup_store" PATH="$runtime_path" \
    "$installed_prefix/bin/hideout" ui --print-url >"$work/package-ui.out"
  HIDEOUT_STORE_ROOT="$setup_store" PATH="$runtime_path" \
    "$installed_prefix/bin/hideout" daemon stop >/dev/null
  lifecycle_keep="$installed_store/keep.txt"
  printf 'preserve\n' >"$lifecycle_keep"
  "$installed_prefix/bin/hideout" package uninstall --prefix "$installed_prefix" \
    >"$work/package-uninstall.out"
  [ -f "$lifecycle_keep" ] || {
    echo "ordinary-user-release: normal exact-package uninstall removed durable state" >&2
    exit 1
  }

  gate2_rel="$(retain_artifact "$gate2_evidence" gate2.json)"
  gate3_rel="$(retain_artifact "$gate3_evidence" gate3.json)"
  gate3_log_rel="$(retain_artifact "$gate3_log" gate3.out)"
  clean_install_rel="$(retain_artifact "$clean_install" clean-install.json)"
  ui_rel="$(retain_artifact "$ui_evidence" ui-evidence.json)"
  tui_rel="$(retain_artifact "$work/package-tui.out" package-tui.out)"
  webui_rel="$(retain_artifact "$work/package-ui.out" package-ui.out)"
  signing_rel="$(retain_artifact "$signing_observation" signing-observation.json)"
  notarization_rel="$(retain_artifact "$notarization_observation" notarization-observation.json)"

  gate2_artifacts="$(jq -s '.' \
    <(artifact_json "$gate2_rel" "manifest" "exact-package Gate 2 result") \
    <(artifact_json "$clean_install_rel" "manifest" "clean package install and first result"))"
  gate3_artifacts="$(jq -s '.' \
    <(artifact_json "$gate3_rel" "manifest" "exact-package Gate 3 result") \
    <(artifact_json "$gate3_log_rel" "log" "package-owned helper and privacy forward-path markers"))"
  ui_artifacts="$(jq -s '.' \
    <(artifact_json "$ui_rel" "manifest" "required browser and PTY UI E2E") \
    <(artifact_json "$tui_rel" "terminal-capture" "exact-package TUI rendering") \
    <(artifact_json "$webui_rel" "log" "exact-package WebUI launch surface"))"

  append_proof \
    "044.ordinary-user.real-gate2.first-run" "real-gate" \
    "ordinary-user-real-gate2" \
    "clean exact-package setup, first result, workspace, lifecycle, and boundary proof" \
    "$gate2_artifacts" "$gate2_runtime"
  append_proof \
    "044.ordinary-user.real-gate3.privacy" "real-gate" \
    "ordinary-user-real-gate3" \
    "exact-package privacy uses the attributed package helper and passes the forward path" \
    "$gate3_artifacts" "$gate3_runtime"
  append_proof \
    "044.ordinary-user.release-candidate.ui" "browser-e2e" \
    "ordinary-user-package-ui" \
    "required UI E2E and exact-package TUI/WebUI surfaces executed" \
    "$ui_artifacts"

  aggregate_summary="$work/release-candidate-summary.json"
  jq -n \
    --arg packageSHA "$(sha256_file "$package_artifact")" \
    --arg manifestSHA "$(sha256_file "$package_manifest")" \
    --arg gate2SHA "$(sha256_file "$gate2_evidence")" \
    --arg gate3SHA "$(sha256_file "$gate3_evidence")" \
    --arg uiSHA "$(sha256_file "$ui_evidence")" \
    --arg signingSHA "$(sha256_file "$signing_observation")" \
    --arg notarizationSHA "$(sha256_file "$notarization_observation")" '
    {
      schema:"hideout.ordinary-user-candidate-summary/v1",
      status:"passed",
      packageSHA256:$packageSHA,
      packageManifestSHA256:$manifestSHA,
      gate2SHA256:$gate2SHA,
      gate3SHA256:$gate3SHA,
      uiSHA256:$uiSHA,
      signingObservationSHA256:$signingSHA,
      notarizationObservationSHA256:$notarizationSHA,
      requiredJourneys:["install","setup","help","doctor","support","first-real-run",
        "privacy","upgrade-repair-uninstall","ui","cleanup"],
      publicReceipt:"pending-publication"
    }' >"$aggregate_summary"
  aggregate_rel="$(retain_artifact "$aggregate_summary" release-candidate-summary.json)"
  signing_artifact="$(artifact_json "$signing_rel" "manifest" "validated signing observation")"
  notarization_artifact="$(artifact_json "$notarization_rel" "manifest" "validated notarization observation")"
  aggregate_artifacts="$(jq -s '.' \
    <(artifact_json "$aggregate_rel" "event-summary" "ordinary-user candidate identity and journey aggregate") \
    <(printf '%s\n' "$signing_artifact") \
    <(printf '%s\n' "$notarization_artifact"))"
  append_proof \
    "044.ordinary-user.release-candidate.aggregate" "real-gate" \
    "ordinary-user-release-candidate" \
    "one clean public package identity binds all ordinary-user, security, UI, signing, and notarization inputs" \
    "$aggregate_artifacts"
fi

jq -s \
  --arg generatedAt "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --arg commit "$commit" \
  --argjson dirty "$dirty" \
  --argjson package "$package_json" '
  {
    version:"hideout.product-hardening-evidence/v1",
    generatedAt:$generatedAt,
    commit:$commit,
    dirty:$dirty,
    proofs:.
  } + if $package == null then {} else {packageIdentity:$package} end
' "$work/proofs.jsonl" >"$out"

go run ./cmd/hideout-schema-validate \
  schemas/product-hardening-evidence.schema.json "$out" >/dev/null
if grep -R -E \
  'HIDEOUT_SECRET_[A-Z0-9_]+=|cap_[A-Za-z0-9]{12,}|ui_[A-Za-z0-9]{12,}|claim_[0-9a-f]{16,}|socks5://[^[:space:]]+:[^[:space:]]+@' \
  "$out" "$artifact_root" >/dev/null 2>&1; then
  echo "ordinary-user-release: retained evidence contains protected material" >&2
  exit 1
fi

if [ "$mode" = "local-fast" ]; then
  go run ./internal/productevidence/cmd/validate-044 \
    --target targeted-completion "$out" >"$work/targeted-complete.out"
  jq -e '
    ([.proofs[].proofId] | sort) ==
      (["044.ordinary-user.docs.truth","044.ordinary-user.gate0.journeys"] | sort) and
    all(.proofs[]; .status == "passed" and .redactionStatus == "passed")
  ' "$out" >/dev/null
else
  go run ./internal/productevidence/cmd/validate-044 \
    --target release-candidate "$out" >"$work/release-complete.out"
  jq -e '
    ([.proofs[].proofId] | sort) == ([
      "044.ordinary-user.docs.truth",
      "044.ordinary-user.gate0.journeys",
      "044.ordinary-user.real-gate2.first-run",
      "044.ordinary-user.real-gate3.privacy",
      "044.ordinary-user.release-candidate.aggregate",
      "044.ordinary-user.release-candidate.ui"
    ] | sort) and
    .dirty == false and .packageIdentity.name == "hideout" and
    all(.proofs[]; .status == "passed" and .redactionStatus == "passed")
  ' "$out" >/dev/null
fi

gate_completed=1
echo "ordinary-user-release: $mode passed evidence=$out"
