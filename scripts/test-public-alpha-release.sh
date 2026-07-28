#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/public-alpha-cleanup.sh"
. "$ROOT/scripts/lib/verified-runtime-cache.sh"
. "$ROOT/scripts/lib/gate-result.sh"
mode="${1:---contract-only}"

case "$mode" in
  --contract-only|--no-publish) ;;
  *) echo "usage: scripts/test-public-alpha-release.sh --contract-only|--no-publish" >&2; exit 2 ;;
esac

for command in go jq git shasum; do
  command -v "$command" >/dev/null 2>&1 || { echo "public-alpha-release: missing $command" >&2; exit 127; }
done

test -f LICENSE
grep -q '^                                 Apache License$' LICENSE
test -f THIRD_PARTY_NOTICES.md
test -f SECURITY.md
test -f .github/ISSUE_TEMPLATE/bug.yml
test -f .github/ISSUE_TEMPLATE/config.yml
test -f .github/pull_request_template.md
while IFS= read -r module; do
  grep -F "| \`$module\` |" THIRD_PARTY_NOTICES.md >/dev/null || {
    echo "public-alpha-release: direct dependency missing from THIRD_PARTY_NOTICES.md: $module" >&2
    exit 1
  }
done < <(go list -m -json all | jq -r 'select((.Main | not) and (.Indirect | not)) | .Path')
test -f releases/current.json
go run ./cmd/hideout-schema-validate \
  schemas/published-release-inventory.schema.json releases/current.json >/dev/null

for schema in \
  schemas/package-manifest.schema.json \
  schemas/product-hardening-evidence.schema.json \
  schemas/release-readiness.schema.json \
  schemas/public-release.schema.json \
  schemas/public-evidence-bundle.schema.json \
  schemas/release-package-verification.schema.json \
  schemas/publication-receipt.schema.json \
  schemas/published-release-inventory.schema.json; do
  jq empty "$schema"
done

bash -n \
  scripts/package-local.sh \
  scripts/test-standalone-install.sh \
  scripts/test-public-alpha-clean-install.sh \
  scripts/test-public-alpha-candidate.sh \
  scripts/test-public-alpha-release.sh \
  scripts/test-phase1.sh \
  scripts/lib/gate-result.sh \
  scripts/lib/lima-temp.sh \
  scripts/lib/public-alpha-cleanup.sh \
  scripts/lib/verified-runtime-cache.sh \
  scripts/test-doc-truth-smoke.sh
sh -n install.sh
grep -F 'https://github.com/$repository/releases/download/$tag/$package' install.sh >/dev/null
grep -F '"$package_root/install.sh" --prefix "$prefix" --store "$store" --skip-init' install.sh >/dev/null

(
  cache_fixture="$(mktemp -d "${TMPDIR:-/tmp}/hideout-runtime-cache-contract.XXXXXX")"
  trap 'rm -rf "$cache_fixture"' EXIT
  catalog="$cache_fixture/catalog.json"
  shared="$cache_fixture/shared"
  target="$cache_fixture/target"
  url="https://example.invalid/runtime.qcow2"
  payload="runtime-cache-contract-fixture"
  digest="$(printf '%s' "$payload" | shasum -a 256 | awk '{print $1}')"
  key="$(printf '%s' "$url" | shasum -a 256 | awk '{print $1}')"
  source="$shared/download/by-url-sha256/$key"
  mkdir -p "$source"
  printf '%s' "$payload" >"$source/data"
  printf '%s\n' "$url" >"$source/url"
  jq -n --arg url "$url" --arg digest "$digest" '
    {families:[{id:"developer-standard",currentRevision:"fixture",
      revisions:[{id:"fixture",artifacts:[{hostOS:"darwin",hostArch:"arm64",
        location:$url,sha256:$digest}]}]}]}
  ' >"$catalog"

  status="$(hideout_seed_verified_runtime_cache "$catalog" "$shared" "$target" 1)"
  [ "$status" = "verified-clone" ]
  cmp "$source/data" "$target/download/by-url-sha256/$key/data"
  printf 'changed-target' >"$target/download/by-url-sha256/$key/data"
  [ "$(cat "$source/data")" = "$payload" ]

  rm -rf "$target" "$source"
  if hideout_seed_verified_runtime_cache "$catalog" "$shared" "$target" 1 >/dev/null 2>&1; then
    echo "public-alpha-release: required missing runtime cache was accepted" >&2
    exit 1
  fi
  mkdir -p "$source"
  printf 'wrong-runtime' >"$source/data"
  if hideout_seed_verified_runtime_cache "$catalog" "$shared" "$target" 1 >/dev/null 2>&1; then
    echo "public-alpha-release: wrong runtime cache digest was accepted" >&2
    exit 1
  fi
)

(
  retained_fixture="$(mktemp -d "${TMPDIR:-/tmp}/hideout-retained-gate2-contract.XXXXXX")"
  trap 'rm -rf "$retained_fixture"' EXIT
  output="$retained_fixture/gate2.out"
  provenance="$retained_fixture/build-provenance.json"
  evidence="$retained_fixture/evidence"
  artifact="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  commit="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  printf '%s\n' \
    'runtime_contract=passed' \
    'runtime_family=developer-standard' \
    'runtime_revision=fixture' \
    "runtime_artifact_sha256=$artifact" \
    'runtime_environment_id=env_fixture' \
    'runtime_host_os=darwin' \
    'runtime_host_arch=arm64' \
    'runtime_guest_arch=aarch64' \
    "runtime_build_commit=$commit" \
    'runtime_build_dirty=false' \
    'gate2: passed' >"$output"
  jq -n --arg commit "$commit" --arg artifact "$artifact" '
    {schema:"hideout.runtime-build-provenance/v1",source:{commit:$commit,dirty:false},
     output:{sha256:$artifact}}
  ' >"$provenance"

  HIDEOUT_RELEASE_EVIDENCE_DIR="$evidence" \
    HIDEOUT_RUNTIME_BUILD_PROVENANCE="$provenance" \
    emit_retained_gate2_result "$output" "$output"
  jq -e --arg artifact "$artifact" --arg commit "$commit" '
    .id == "gate2-lima" and .backend == "lima" and .result == "passed" and
    .runtime.artifactSHA256 == $artifact and .runtime.buildCommit == $commit and
    .runtime.buildDirty == false
  ' "$evidence/gates/gate2-lima.json" >/dev/null

  printf '%s\n' 'runtime_contract=passed' 'gate2: failed' >"$retained_fixture/failed.out"
  if HIDEOUT_RELEASE_EVIDENCE_DIR="$evidence" \
    HIDEOUT_RUNTIME_BUILD_PROVENANCE="$provenance" \
    emit_retained_gate2_result "$retained_fixture/failed.out" >/dev/null 2>&1; then
    echo "public-alpha-release: failed Gate 2 output was reused" >&2
    exit 1
  fi
  jq --arg wrong "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" \
    '.output.sha256 = $wrong' "$provenance" >"$retained_fixture/wrong-provenance.json"
  if HIDEOUT_RELEASE_EVIDENCE_DIR="$evidence" \
    HIDEOUT_RUNTIME_BUILD_PROVENANCE="$retained_fixture/wrong-provenance.json" \
    emit_retained_gate2_result "$output" >/dev/null 2>&1; then
    echo "public-alpha-release: mismatched runtime provenance was reused" >&2
    exit 1
  fi

  jq -n --arg commit "$commit" --arg packageSHA "$artifact" \
    --arg goVersion "go$(awk '$1 == "go" { print $2; exit }' go.mod)" '
    {schema:"hideout.public-alpha-candidate/v1",version:"0.1.0-alpha.1",
     tag:"v0.1.0-alpha.1",sourceCommit:$commit,sourceDirty:false,
     workflowRunId:123,publicationStatus:"draft-only",packageSHA256:$packageSHA,
     goVersion:$goVersion}
  ' >"$retained_fixture/candidate.json"
  validate_retained_gate0_candidate "$retained_fixture/candidate.json" "$commit" "$artifact"
  jq 'del(.goVersion)' "$retained_fixture/candidate.json" \
    >"$retained_fixture/candidate-missing-go-version.json"
  if validate_retained_gate0_candidate "$retained_fixture/candidate-missing-go-version.json" \
    "$commit" "$artifact" >/dev/null 2>&1; then
    echo "public-alpha-release: Gate 0 receipt without toolchain identity was accepted" >&2
    exit 1
  fi
  if validate_retained_gate0_candidate "$retained_fixture/candidate.json" "$commit" \
    "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" >/dev/null 2>&1; then
    echo "public-alpha-release: Gate 0 receipt was reused for another package" >&2
    exit 1
  fi
)

for workflow in \
  .github/workflows/hideout-alpha-candidate.yml \
  .github/workflows/hideout-alpha-promote.yml \
  .github/workflows/hideout-alpha-public-truth.yml; do
  test -f "$workflow"
  if command -v ruby >/dev/null 2>&1; then
    ruby -e 'require "yaml"; YAML.load_file(ARGV.fetch(0))' "$workflow" >/dev/null
  fi
  if grep -E 'uses:[[:space:]]+[^[:space:]@]+@(main|master|v[0-9]+([.]|$))' "$workflow" >/dev/null; then
    echo "public-alpha-release: workflow action is not pinned by full commit: $workflow" >&2
    exit 1
  fi
done

# These guards are workflow authority, not documentation. Keep the no-publish
# rehearsal tied to their executable branches as well as to the typed models.
grep -F 'candidate: published tag already exists; replacement is forbidden' \
  .github/workflows/hideout-alpha-candidate.yml >/dev/null
grep -F 'private draft exists; retry requires replace_private_draft=true' \
  .github/workflows/hideout-alpha-candidate.yml >/dev/null
grep -F '.draft == true and .prerelease == true and .published_at == null and .tag_name == $tag' \
  .github/workflows/hideout-alpha-candidate.yml >/dev/null
grep -F 'HIDEOUT_ALPHA_REPLACE_DRAFT_ID' \
  .github/workflows/hideout-alpha-candidate.yml >/dev/null
grep -F 'markdownlint-cli2@0.22.1 markdownlint@0.40.0' \
  .github/workflows/hideout-alpha-candidate.yml >/dev/null
grep -F 'environment: public-alpha' .github/workflows/hideout-alpha-promote.yml >/dev/null
grep -F 'RELEASE_ADMIN_TOKEN: ${{ secrets.RELEASE_ADMIN_TOKEN }}' \
  .github/workflows/hideout-alpha-promote.yml >/dev/null
grep -F 'GH_TOKEN: ${{ secrets.RELEASE_ADMIN_TOKEN }}' \
  .github/workflows/hideout-alpha-promote.yml >/dev/null
grep -F 'runs-on: macos-15' \
  .github/workflows/hideout-alpha-public-truth.yml >/dev/null
test "$(grep -Fc 'GH_TOKEN="$RELEASE_ADMIN_TOKEN" gh api' \
  .github/workflows/hideout-alpha-promote.yml)" -eq 2
grep -F '.draft == true and .tag_name == $tag and .target_commitish == $commit' \
  .github/workflows/hideout-alpha-promote.yml >/dev/null
grep -F 'diff -u <(printf' .github/workflows/hideout-alpha-promote.yml >/dev/null
grep -F 'api_digest' .github/workflows/hideout-alpha-promote.yml >/dev/null
grep -F '.cleanup.status == "passed"' .github/workflows/hideout-alpha-promote.yml >/dev/null
grep -F 'License: Apache-2.0 for Hideout' .github/workflows/hideout-alpha-promote.yml >/dev/null
grep -F 'packaging/homebrew/hideout.rb' .github/workflows/hideout-alpha-promote.yml >/dev/null
grep -F 'releases/formulas/${PUBLIC_ALPHA_TAG}.rb' .github/workflows/hideout-alpha-promote.yml >/dev/null
grep -F 'notes=$(cat "$PUBLIC_ALPHA_ROOT/validated/release-notes.md")' \
  .github/workflows/hideout-alpha-promote.yml >/dev/null
test "$(grep -Fc '>"$PROMOTION_ROOT/validated/context.json"' \
  .github/workflows/hideout-alpha-promote.yml)" -eq 1
grep -F '.schema == "hideout.public-alpha-validation-context/v1" and' \
  .github/workflows/hideout-alpha-promote.yml >/dev/null
grep -F 'public_alpha_cleanup_root "$work" "$out/cleanup-report.json"' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'umask 077' scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'candidate_short_tmp="${HIDEOUT_RELEASE_SHORT_TMPDIR:-/tmp}"' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'export HIDEOUT_LIMA_SHORT_TMPDIR="$work"' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'local root="${HIDEOUT_036_SHORT_TMPDIR:-/tmp}"' \
  scripts/lib/gate2-resource-lifecycle-performance.sh >/dev/null
if grep -F 'local root="${HIDEOUT_036_SHORT_TMPDIR:-${TMPDIR:-/tmp}}"' \
  scripts/lib/gate2-resource-lifecycle-performance.sh >/dev/null; then
  echo "public-alpha-release: Gate 2 lifecycle short root regressed to ambient TMPDIR" >&2
  exit 1
fi
grep -F 'hideout-036-performance-candidate-workspace.XXXXXX' \
  scripts/lib/gate2-resource-lifecycle-performance.sh >/dev/null
grep -F 'hideout-036-performance-baseline-workspace.XXXXXX' \
  scripts/lib/gate2-resource-lifecycle-performance.sh >/dev/null
grep -F '[ "$(gate2_036_fixture_digest "$baseline_workspace")" = "$fixture_digest" ]' \
  scripts/lib/gate2-resource-lifecycle-performance.sh >/dev/null
if grep -F 'hideout-036-performance-workspace.XXXXXX' \
  scripts/lib/gate2-resource-lifecycle-performance.sh >/dev/null; then
  echo "public-alpha-release: Gate 2 lifecycle candidate and baseline share one physical workspace" >&2
  exit 1
fi
grep -F 'baseline_commit="322c3c6cc9561eea21d4ed20ab78172429654c54"' \
  scripts/test-lifecycle-lima-e2e.sh >/dev/null
grep -F 'schema:"hideout.attach-reservation-performance/v1"' \
  scripts/lib/gate2-resource-lifecycle-performance.sh >/dev/null
grep -F 'workspaceIsolation:"separate-physical-fixtures"' \
  scripts/lib/gate2-resource-lifecycle-performance.sh >/dev/null
if grep -F "<(proof_json '036.lifecycle.real-gate2.performance'" \
  scripts/test-lifecycle-lima-e2e.sh >/dev/null; then
  echo "public-alpha-release: current candidate reclaims the historical 036 feature-regression proof" >&2
  exit 1
fi
grep -F 'HIDEOUT_GATE2_EXTERNAL_HOST_APP_PACK="$ROOT/test/host-app-packs/gate2-external"' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'HIDEOUT_REQUIRE_RUNTIME_CACHE=1 scripts/test-public-alpha-clean-install.sh' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'hideout_seed_verified_runtime_cache' \
  scripts/test-public-alpha-clean-install.sh >/dev/null
grep -F 'ln -s "$1" "$path"' scripts/test-runtime-lima.sh >/dev/null
if grep -F 'echo hideout-runtime-drift' scripts/test-runtime-lima.sh >/dev/null; then
  echo "public-alpha-release: runtime drift fixture regressed to an executable chmod mutation" >&2
  exit 1
fi
grep -F 'umask 077' scripts/test-runtime-lima.sh >/dev/null
grep -F 'chmod 0700 "$HIDEOUT_STORE_ROOT"' scripts/test-env-image.sh >/dev/null
grep -F 'chmod 0700 "$drift_store"' scripts/test-runtime-lima.sh >/dev/null
if grep -F 'projection_runtime_args' scripts/lib/gate2-projection.sh >/dev/null; then
  echo "public-alpha-release: Gate 2 projection retained a Bash 3.2-unsafe optional array" >&2
  exit 1
fi
grep -F 'set -- --runtime "${HIDEOUT_PROJECTION_RUNTIME_FAMILY:-developer-standard}"' \
  scripts/lib/gate2-projection.sh >/dev/null
grep -F '"$@"' scripts/lib/gate2-projection.sh >/dev/null
grep -F 'local proxy_args=(--listen 127.0.0.1:0 --url-host 127.0.0.1)' \
  scripts/lib/gate2-projection.sh >/dev/null
if grep -F -- '--url-host host.lima.internal' scripts/lib/gate2-projection.sh >/dev/null; then
  echo "public-alpha-release: Gate 2 projection bypasses the host gateway proxy contract" >&2
  exit 1
fi
grep -F 'projection_assert_host_effect_preflight' \
  scripts/lib/gate2-projection.sh >/dev/null
grep -F '.action == "broker.readiness"' \
  scripts/lib/gate2-projection.sh >/dev/null
grep -F '.details.accepted >= 2' \
  scripts/lib/gate2-projection.sh >/dev/null
grep -F '.details.responseWritten >= 2' \
  scripts/lib/gate2-projection.sh >/dev/null
grep -F 'chmod 0700 "$store" "$home" "$lima_home"' \
  scripts/test-public-alpha-clean-install.sh >/dev/null
grep -F 'clean_install_tmp_parent="${HIDEOUT_LIMA_SHORT_TMPDIR:-/tmp}"' \
  scripts/test-public-alpha-clean-install.sh >/dev/null
grep -F 'tmp="$(mktemp -d "$clean_install_tmp_parent/hci.XXXXXX")"' \
  scripts/test-public-alpha-clean-install.sh >/dev/null
for consumer in test-hostfs-visibility-e2e.sh; do
  grep -F "$consumer --real-gate2 --require-real" \
    scripts/test-public-alpha-candidate.sh >/dev/null
done
grep -F -- '--gate2-evidence "$out/runtime-gate2/product-hardening-evidence.json"' \
  scripts/test-public-alpha-candidate.sh >/dev/null
for consumer in test-projection-readiness-lima-e2e.sh \
  test-shared-workspace-lima-e2e.sh test-lifecycle-lima-e2e.sh \
  test-first-run-e2e.sh test-workspace-executable-lima-e2e.sh \
  test-disposable-recovery-lima-e2e.sh; do
  grep -F "$consumer" scripts/test-public-alpha-candidate.sh >/dev/null
done
for package_binding in \
  '--package "$package" --out "$out/projection-readiness-gate2"' \
  '--package "$package" --out "$out/shared-workspace-gate2"' \
  '--package "$package" --out "$out/lifecycle-gate2"' \
  '--package "$package" --out "$out/setup-first-run-gate2"' \
  '--package "$package" --out "$out/workspace-executable-gate2"' \
  '--package "$package" --out "$out/disposable-recovery-gate2"'; do
  grep -F -- "$package_binding" scripts/test-public-alpha-candidate.sh >/dev/null
done
grep -F 'scripts/promote-projection-privacy.sh' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'HIDEOUT_PHASE1_RETAINED_GATE2_OUTPUT="$out/runtime-gate2/logs/gate2.out"' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'HIDEOUT_PHASE1_RETAINED_GATE0_CANDIDATE="$candidate_observation"' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'validate_retained_gate0_candidate "$retained_gate0_candidate"' \
  scripts/test-phase1.sh >/dev/null
grep -F 'emit_retained_gate2_result "$retained_gate2_output"' \
  scripts/test-phase1.sh >/dev/null
grep -F 'HIDEOUT_RELEASE_BINARY is not executable' \
  scripts/test-env-image.sh >/dev/null
grep -F 'HIDEOUT_LINUX_WORKSPACE_PORTAL_PATH="$work/package/hideout/bin/hideout-workspace-portal-linux-$arch"' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'gate_require_completion public-alpha-candidate' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'package-owned Linux tun2socks helper' \
  scripts/test-public-alpha-candidate.sh >/dev/null
for gate in scripts/test-gate2-lima.sh scripts/test-gate3-hidden-proxy.sh; do
  grep -F 'release evidence requires the packaged Linux Workspace Portal' "$gate" >/dev/null
  grep -F 'release evidence requires the packaged Linux shim' "$gate" >/dev/null
done
grep -F 'release evidence requires the packaged Linux hostfsd' \
  scripts/test-gate2-lima.sh >/dev/null
grep -F 'release evidence requires the packaged Linux DNS stub' \
  scripts/test-gate3-hidden-proxy.sh >/dev/null
grep -F 'gate3: using package-owned tun2socks helper' \
  scripts/test-gate3-hidden-proxy.sh >/dev/null
grep -F 'hideout="$HIDEOUT_RELEASE_BINARY"' \
  scripts/test-gate3-hidden-proxy.sh >/dev/null
if grep -F 'cp "$HIDEOUT_RELEASE_BINARY" "$hideout"' \
  scripts/test-gate3-hidden-proxy.sh >/dev/null; then
  echo "public-alpha-release: release Gate 3 must preserve package helper adjacency" >&2
  exit 1
fi
grep -F 'HIDEOUT_GATE4_SHORT_TMPDIR' \
  scripts/test-gate4-host-escape.sh >/dev/null
grep -F 'guest_workspace="/workspace"' \
  scripts/test-gate4-host-escape.sh >/dev/null
grep -F 'expect_open_denied "symlink-escape" "$guest_workspace/link-out" '\''resolves outside workspace'\''' \
  scripts/test-gate4-host-escape.sh >/dev/null
grep -F 'expect_open_denied "special-file" "$guest_workspace/pipe" '\''not a regular file or directory'\''' \
  scripts/test-gate4-host-escape.sh >/dev/null
if grep -F 'expect_open_denied "symlink-escape" "$workspace/link-out"' \
  scripts/test-gate4-host-escape.sh >/dev/null; then
  echo "public-alpha-release: Gate 4 unsafe-file fixtures must use guest-visible paths" >&2
  exit 1
fi
grep -F 'gate4_temp_roots' scripts/test-release-dogfood.sh >/dev/null
grep -F 'scripts/test-ordinary-user-release.sh --release-candidate' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'scripts/test-ui-e2e.sh --all --require-executed' \
  scripts/test-public-alpha-candidate.sh >/dev/null
grep -F 'work="$(mktemp -d "${TMPDIR:-/tmp}/hou.XXXXXX")"' \
  scripts/test-ordinary-user-release.sh >/dev/null
grep -F 'setup_store="$work/s"' \
  scripts/test-ordinary-user-release.sh >/dev/null
grep -F 'package_ui_raw="$work/package-ui.raw"' \
  scripts/test-ordinary-user-release.sh >/dev/null
grep -F 'redact-public-evidence --input "$package_ui_raw"' \
  scripts/test-ordinary-user-release.sh >/dev/null
grep -F -- '--input "$gate2_evidence" --out "$gate2_public"' \
  scripts/test-ordinary-user-release.sh >/dev/null
grep -F -- '--input "$gate3_evidence" --out "$gate3_public"' \
  scripts/test-ordinary-user-release.sh >/dev/null
grep -F -- '--input "$gate3_log" --out "$gate3_public_log"' \
  scripts/test-ordinary-user-release.sh >/dev/null
grep -F -- '--input "$work/package-tui.out" --out "$tui_public"' \
  scripts/test-ordinary-user-release.sh >/dev/null
grep -F 'rm -f "$package_ui_raw"' \
  scripts/test-ordinary-user-release.sh >/dev/null
if grep -F '"$installed_prefix/bin/hideout" ui --print-url >"$work/package-ui.out"' \
    scripts/test-ordinary-user-release.sh >/dev/null; then
  echo "public-alpha-release: raw WebUI operator token must not enter retained evidence" >&2
  exit 1
fi
if grep -F 'retain_artifact "$gate3_log" gate3.out' \
    scripts/test-ordinary-user-release.sh >/dev/null; then
  echo "public-alpha-release: raw Gate 3 log must not enter retained evidence" >&2
  exit 1
fi
if grep -E 'retain_artifact "\$(gate2_evidence|gate3_evidence)"|retain_artifact "\$work/package-tui\.out"' \
    scripts/test-ordinary-user-release.sh >/dev/null; then
  echo "public-alpha-release: raw gate or TUI artifact must not enter retained evidence" >&2
  exit 1
fi
disposal_stop_line="$(grep -n 'os.kill(daemon_pid, signal.SIGSTOP)' \
  scripts/test-disposable-recovery-lima-e2e.sh | cut -d: -f1)"
disposal_snapshot_line="$(grep -n 'with open(snapshot, "w", encoding="utf-8")' \
  scripts/test-disposable-recovery-lima-e2e.sh | cut -d: -f1)"
if [ -z "$disposal_stop_line" ] || [ -z "$disposal_snapshot_line" ] ||
    [ "$disposal_stop_line" -ge "$disposal_snapshot_line" ]; then
  echo "public-alpha-release: disposable checkpoint watcher must freeze before retaining evidence" >&2
  exit 1
fi
if grep -F 'hideout-ordinary-user-release.XXXXXX' \
  scripts/test-ordinary-user-release.sh >/dev/null; then
  echo "public-alpha-release: ordinary-user setup fixture must preserve the short daemon socket path" >&2
  exit 1
fi
grep -F -- '--url-host 127.0.0.1' scripts/test-gate3-hidden-proxy.sh >/dev/null
grep -F -- '--map-connect "$mediated_resolver:443=cloudflare-dns.com:443"' \
  scripts/test-gate3-hidden-proxy.sh >/dev/null
for gate in scripts/test-gate2-lima.sh scripts/test-gate3-hidden-proxy.sh \
  scripts/test-runtime-lima.sh scripts/test-env-image.sh scripts/test-dogfood-cli-smoke.sh; do
  grep -F 'hideout_mktemp_lima_home' "$gate" >/dev/null
done
grep -q 'docs_candidate_raw=' scripts/test-public-alpha-candidate.sh
if [ "$(grep -c 'support release redact-public-evidence' scripts/test-public-alpha-candidate.sh)" -ne 2 ]; then
  echo "public-alpha-release: candidate docs and readiness evidence must pass the Go-owned public redaction boundary" >&2
  exit 1
fi
grep -q 'readiness_raw=' scripts/test-public-alpha-candidate.sh
grep -F 'public_alpha_cleanup_workflow_state' \
  .github/workflows/hideout-alpha-candidate.yml >/dev/null
grep -F 'Retain bounded workflow cleanup receipt' \
  .github/workflows/hideout-alpha-candidate.yml >/dev/null
if grep -n 'set +e' .github/workflows/hideout-alpha-candidate.yml >/dev/null; then
  echo "public-alpha-release: hosted cleanup must remain fail closed" >&2
  exit 1
fi
grep -F 'HIDEOUT_SECRET_DEFAULT_PROXY=<operator-secret-ref>' \
  specs/033-public-alpha-release-channel/quickstart.md >/dev/null
grep -F 'scripts/test-public-alpha-release.sh --no-publish' \
  specs/033-public-alpha-release-channel/quickstart.md >/dev/null
if grep -En -- '--gate3-proxy-secret|--source-commit' \
    specs/033-public-alpha-release-channel/quickstart.md >/dev/null; then
  echo "public-alpha-release: quickstart uses a nonexistent release-script option" >&2
  exit 1
fi
notary_line="$(grep -n 'xcrun notarytool submit' .github/workflows/hideout-alpha-candidate.yml | cut -d: -f1)"
notary_observation_line="$(grep -n 'support release observe-notarization' .github/workflows/hideout-alpha-candidate.yml | cut -d: -f1)"
signing_observation_line="$(grep -n 'support release observe-signing' .github/workflows/hideout-alpha-candidate.yml | cut -d: -f1)"
if [ -z "$notary_line" ] || [ -z "$notary_observation_line" ] ||
  [ -z "$signing_observation_line" ] || [ "$notary_observation_line" -le "$notary_line" ] ||
  [ "$signing_observation_line" -le "$notary_observation_line" ]; then
  echo "public-alpha-release: accepted notarization must precede online-ticket signing observation" >&2
  exit 1
fi
if ! grep -q -- '--check-notarization' internal/releasechannel/signing_darwin.go; then
  echo "public-alpha-release: command-line Mach-O observation must check the online notarization ticket" >&2
  exit 1
fi
if grep -REn 'mktemp (-d )?"?/tmp/' scripts >/dev/null; then
  echo "public-alpha-release: test temporary roots must honor TMPDIR" >&2
  exit 1
fi

go test ./internal/releasechannel ./internal/packagekit ./internal/productevidence \
  ./internal/releasecompat ./internal/recovery >/dev/null

if [ "$mode" = "--contract-only" ]; then
  echo "public-alpha-release: contract-only passed"
  exit 0
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-public-alpha-rehearsal.XXXXXX")"
cleanup_report="$(mktemp "${TMPDIR:-/tmp}/hideout-public-alpha-cleanup.XXXXXX")"
rehearsal_report="$(mktemp "${TMPDIR:-/tmp}/hideout-public-alpha-rehearsal-report.XXXXXX")"
cleanup_complete=0
before_releases=""
cleanup() {
  local exit_status=$?
  trap - EXIT
  if [ "$cleanup_complete" -eq 0 ]; then
    public_alpha_cleanup_root "$tmp" "$cleanup_report" || exit_status=1
  fi
  rm -f "$cleanup_report" "$rehearsal_report"
  exit "$exit_status"
}
trap cleanup EXIT
touch "$tmp/limactl-Chromium-cleanup-fixture"
tail -f "$tmp/limactl-Chromium-cleanup-fixture" >/dev/null 2>&1 &
cleanup_fixture_pid=$!
disown "$cleanup_fixture_pid" >/dev/null 2>&1 || true

workflow_cleanup_root="$tmp/workflow-cleanup-fixture"
workflow_cleanup_report="$tmp/workflow-cleanup-report.json"
mkdir -p "$workflow_cleanup_root"
touch "$workflow_cleanup_root/secret-bearing-fixture"
public_alpha_cleanup_workflow_state "$workflow_cleanup_root" "" "$workflow_cleanup_report"
jq -e '
  .schema == "hideout.public-alpha-workflow-cleanup/v1" and
  .status == "passed" and
  .keychainDeleteFailures == 0 and
  .candidateKeychainsRetained == 0 and
  .candidateTemporaryRootsRetained == 0 and
  .candidateSecretBearingStateRetained == 0
' "$workflow_cleanup_report" >/dev/null

if command -v gh >/dev/null 2>&1 && [ -n "${GITHUB_TOKEN:-${GH_TOKEN:-}}" ]; then
  token="${GH_TOKEN:-$GITHUB_TOKEN}"
  before_releases="$(GH_TOKEN="$token" gh api --paginate repos/vibe-agi/hideout/releases --jq 'length' | awk '{s+=$1} END{print s+0}')"
  GH_TOKEN="$token" gh api -H 'X-GitHub-Api-Version: 2026-03-10' \
    repos/vibe-agi/hideout/immutable-releases --jq 'select(.enabled == true)' >/dev/null
  GH_TOKEN="$token" gh api -H 'X-GitHub-Api-Version: 2026-03-10' \
    repos/vibe-agi/hideout/private-vulnerability-reporting --jq 'select(.enabled == true)' >/dev/null
  GH_TOKEN="$token" gh api -H 'X-GitHub-Api-Version: 2026-03-10' \
    repos/vibe-agi/hideout/environments --jq '
      [.environments[] | select(.name == "public-alpha" or .name == "public-alpha-signing") |
       select(any(.protection_rules[]; .type == "required_reviewers"))] | length == 2
    ' >/dev/null
fi

fixture_source="$tmp/source"
mkdir -p "$fixture_source"
git ls-files --cached --others --exclude-standard -z |
  tar --null -T - -cf - |
  tar -C "$fixture_source" -xf -
git -C "$fixture_source" init -q
git -C "$fixture_source" config user.name "Hideout no-publish fixture"
git -C "$fixture_source" config user.email "no-publish@invalid.example"
git -C "$fixture_source" add -A
git -C "$fixture_source" -c core.hooksPath=/dev/null commit -qm \
  "Create non-promotable no-publish fixture"
fixture_commit="$(git -C "$fixture_source" rev-parse HEAD)"
if git cat-file -e "$fixture_commit^{commit}" >/dev/null 2>&1; then
  echo "public-alpha-release: fixture commit unexpectedly belongs to the product repository" >&2
  exit 1
fi

package="$tmp/hideout-v0.1.0-dev.0-darwin-arm64.tar.gz"
scripts/package-local.sh --source "$fixture_source" --out "$package" >/dev/null
go run ./cmd/hideout support release package-identity \
  --archive "$package" --out "$tmp/package-identity.json" >/dev/null

package_root="$tmp/package-root"
mkdir -p "$package_root"
tar -xzf "$package" -C "$package_root"
package_tree="$package_root/hideout"

evidence="$tmp/evidence"
mkdir -p "$evidence/proofs/contract" "$evidence/package" "$evidence/signing" \
  "$evidence/notarization" "$evidence/runtime" "$evidence/gates"
go run ./cmd/hideout support proof-registry --json >"$evidence/proof-registry.json"
cp "$tmp/package-identity.json" "$evidence/candidate-identity.json"
cp "$package_tree/package-manifest.json" "$evidence/package/package-manifest.json"
go run ./cmd/hideout support release observe-package-verification \
  --package-root "$package_tree" --package-identity "$tmp/package-identity.json" \
  --out "$evidence/package/verify.json" >/dev/null
package_manifest_sha="$(shasum -a 256 "$package_tree/package-manifest.json" | awk '{print $1}')"
runtime_revision="$(jq -r '.runtime.revision' "$package_tree/package-manifest.json")"
runtime_family="$(jq -r '.runtime.family' "$package_tree/package-manifest.json")"
runtime_sha="$(jq -r '.runtime.artifactSHA256' "$package_tree/package-manifest.json")"
runtime_build_commit="$(printf 'b%.0s' {1..40})"
observed_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
jq -n --arg observedAt "$observed_at" --arg manifestSHA "$package_manifest_sha" '
  {schema:"hideout.release-signing-observation/v1",status:"developer-id-verified",
   teamId:"FIXTURETEAM",commonName:"Developer ID Application: no-publish fixture",
   observedAt:$observedAt,hostOS:"darwin",packageManifestSHA256:$manifestSHA,
   binaries:[{path:"bin/hideout",identifier:"fixture.hideout",cdHash:"fixture",
     secureTimestamp:true,hardenedRuntime:true,strictVerified:true,onlineNotarizationValid:true}]}
' >"$evidence/signing/observation.json"
jq -n --arg observedAt "$observed_at" --arg manifestSHA "$package_manifest_sha" '
  {schema:"hideout.release-notarization-observation/v1",status:"accepted",
   submissionId:"no-publish-fixture",submissionSHA256:("f"*64),
   packageManifestSHA256:$manifestSHA,observedAt:$observedAt,ticketMode:"online",
   stapleStatus:"not-applicable-tar-gz"}
' >"$evidence/notarization/observation.json"
jq -n --arg revision "$runtime_revision" --arg runtimeSHA "$runtime_sha" \
  --arg startedAt "$observed_at" --arg completedAt "$observed_at" '
  {schema:"hideout.runtime-build-provenance/v1",revision:$revision,
   source:{commit:("b"*40),dirty:false,sourceLockSHA256:("c"*64)},
   builder:{observedIdentity:"fixture-builder@sha256:fixture",
     expectedIdentity:"fixture-builder@sha256:fixture",attestation:"no-publish-fixture",
     qemu:"fixture",libguestfs:"fixture",libguestfsBackend:"fixture",
     libguestfsBackendSettings:"fixture",libguestfsAcceleration:"fixture"},
   output:{file:"runtime.qcow2",sha256:$runtimeSHA,bytes:1},
   startedAt:$startedAt,completedAt:$completedAt,promoted:false}
' >"$evidence/runtime/build-provenance.json"
jq -n --arg family "$runtime_family" --arg revision "$runtime_revision" \
  --arg runtimeSHA "$runtime_sha" --arg buildCommit "$runtime_build_commit" '
  {id:"gate2-lima",backend:"lima",result:"passed",
   reason:"",boundarySummary:"boundary-summary:present",environmentName:"fixture-gate2",
   runtime:{schema:"hideout.runtime-evidence-binding/v1",family:$family,revision:$revision,
     artifactSHA256:$runtimeSHA,environmentId:"env_fixturegate2",hostOS:"darwin",
     hostArch:"arm64",guestArch:"aarch64",buildCommit:$buildCommit,buildDirty:false}}
' >"$evidence/gates/gate2.json"
jq -n --arg family "$runtime_family" --arg revision "$runtime_revision" \
  --arg runtimeSHA "$runtime_sha" --arg buildCommit "$runtime_build_commit" '
  {id:"gate3-hidden-proxy",backend:"lima",result:"passed",
   reason:"",boundarySummary:"boundary-summary:present",environmentName:"fixture-gate3",
   runtime:{schema:"hideout.runtime-evidence-binding/v1",family:$family,revision:$revision,
     artifactSHA256:$runtimeSHA,environmentId:"env_fixturegate3",hostOS:"darwin",
     hostArch:"arm64",guestArch:"aarch64",buildCommit:$buildCommit,buildDirty:false}}
' >"$evidence/gates/gate3.json"
jq -n --slurpfile package "$tmp/package-identity.json" '
  {schema:"hideout.release-readiness/v1",mode:"release-candidate",releaseReady:true,
   sourceCommit:$package[0].sourceCommit,packageIdentity:$package[0]}
' >"$evidence/release-readiness.json"
commit="$(jq -r '.sourceCommit' "$tmp/package-identity.json")"
jq -n \
  --arg generatedAt "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --arg commit "$commit" \
  --arg runtimeFamily "$runtime_family" \
  --arg runtimeRevision "$runtime_revision" \
  --arg runtimeSHA "$runtime_sha" \
  --arg runtimeBuildCommit "$runtime_build_commit" \
  --slurpfile package "$tmp/package-identity.json" \
  --slurpfile registry "$evidence/proof-registry.json" '
  def proof($r): {
    proofId:$r.proofId,
    featureId:$r.featureId,
    mode:(if $r.requiredMode != null then $r.requiredMode else "unit" end),
    evidenceClass:(if $r.requiredEvidenceClass != null then $r.requiredEvidenceClass else "no-publish-contract-fixture" end),
    status:"passed",
    commandSummary:"no-publish mechanics fixture; not release evidence",
    coveredClaims:[$r.claimIds[] | {claimId:.,source:"spec",description:"contract mechanics only"}],
    prerequisites:[],
    artifacts:[],
    redactionStatus:"passed"
  } + (if $r.runtimePolicy == "exact-real" then {runtime:{
    schema:"hideout.runtime-evidence-binding/v1",family:$runtimeFamily,revision:$runtimeRevision,
    artifactSHA256:$runtimeSHA,environmentId:("env_" + ($r.proofId|gsub("[^A-Za-z0-9]";""))),
    hostOS:"darwin",hostArch:"arm64",guestArch:"aarch64",buildCommit:$runtimeBuildCommit,buildDirty:false
  }} else {} end);
  {
    version:"hideout.product-hardening-evidence/v1",
    generatedAt:$generatedAt,
    commit:$commit,
    dirty:false,
    packageIdentity:$package[0],
    proofs:[$registry[0].requirements[] |
      select(.requiredFor == "release-candidate") | proof(.)]
  }' >"$evidence/proofs/contract/manifest.json"
go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
  "$evidence/proofs/contract/manifest.json" >/dev/null

evidence_archive="$tmp/hideout-v0.1.0-dev.0-evidence.tar.gz"
go run ./cmd/hideout support release build-evidence --root "$evidence" \
  --package-identity "$tmp/package-identity.json" --out "$evidence_archive" >/dev/null
go run ./cmd/hideout support release validate-evidence --archive "$evidence_archive" >/dev/null

missing_verification="$tmp/missing-verification"
cp -R "$evidence" "$missing_verification"
rm -f "$missing_verification/bundle-manifest.json" "$missing_verification/SHA256SUMS" \
  "$missing_verification/package/verify.json"
if go run ./cmd/hideout support release build-evidence --root "$missing_verification" \
    --package-identity "$tmp/package-identity.json" \
    --out "$tmp/missing-verification.tar.gz" >/dev/null 2>&1; then
  echo "public-alpha-release: missing package verification fixture passed" >&2
  exit 1
fi

runtime_drift="$tmp/runtime-drift"
cp -R "$evidence" "$runtime_drift"
rm -f "$runtime_drift/bundle-manifest.json" "$runtime_drift/SHA256SUMS"
jq '(.output.sha256) = ("0" * 64)' "$runtime_drift/runtime/build-provenance.json" \
  >"$runtime_drift/runtime/mutated.json"
mv "$runtime_drift/runtime/mutated.json" "$runtime_drift/runtime/build-provenance.json"
if go run ./cmd/hideout support release build-evidence --root "$runtime_drift" \
    --package-identity "$tmp/package-identity.json" \
    --out "$tmp/runtime-drift.tar.gz" >/dev/null 2>&1; then
  echo "public-alpha-release: runtime provenance drift fixture passed" >&2
  exit 1
fi

undeclared="$tmp/undeclared"
cp -R "$evidence" "$undeclared"
rm -f "$undeclared/bundle-manifest.json" "$undeclared/SHA256SUMS"
printf 'not part of the canonical evidence inventory\n' >"$undeclared/undeclared.txt"
if go run ./cmd/hideout support release build-evidence --root "$undeclared" \
    --package-identity "$tmp/package-identity.json" \
    --out "$tmp/undeclared.tar.gz" >/dev/null 2>&1; then
  echo "public-alpha-release: undeclared evidence fixture passed" >&2
  exit 1
fi

missing="$tmp/missing"
cp -R "$evidence" "$missing"
rm -f "$missing/bundle-manifest.json" "$missing/SHA256SUMS"
jq 'del(.proofs[0])' "$missing/proofs/contract/manifest.json" >"$missing/proofs/contract/mutated.json"
mv "$missing/proofs/contract/mutated.json" "$missing/proofs/contract/manifest.json"
if go run ./cmd/hideout support release build-evidence --root "$missing" \
    --package-identity "$tmp/package-identity.json" --out "$tmp/missing.tar.gz" >/dev/null 2>&1; then
  echo "public-alpha-release: missing proof fixture passed" >&2
  exit 1
fi

failed="$tmp/failed"
cp -R "$evidence" "$failed"
rm -f "$failed/bundle-manifest.json" "$failed/SHA256SUMS"
jq '(.proofs[0].status) = "failed"' "$failed/proofs/contract/manifest.json" \
  >"$failed/proofs/contract/mutated.json"
mv "$failed/proofs/contract/mutated.json" "$failed/proofs/contract/manifest.json"
if go run ./cmd/hideout support release build-evidence --root "$failed" \
    --package-identity "$tmp/package-identity.json" --out "$tmp/failed.tar.gz" >/dev/null 2>&1; then
  echo "public-alpha-release: failed gate fixture passed" >&2
  exit 1
fi

secret="$tmp/secret"
cp -R "$evidence" "$secret"
rm -f "$secret/bundle-manifest.json" "$secret/SHA256SUMS"
printf 'cap_0123456789abcdef0123456789abcdef\n' >"$secret/logs-secret.txt"
if go run ./cmd/hideout support release build-evidence --root "$secret" \
    --package-identity "$tmp/package-identity.json" --out "$tmp/secret.tar.gz" >/dev/null 2>&1; then
  echo "public-alpha-release: credential fixture passed" >&2
  exit 1
fi

outside="$tmp/outside"
printf 'outside\n' >"$outside"
linked="$tmp/linked"
cp -R "$evidence" "$linked"
rm -f "$linked/bundle-manifest.json" "$linked/SHA256SUMS"
ln -s "$outside" "$linked/escape"
if go run ./cmd/hideout support release build-evidence --root "$linked" \
    --package-identity "$tmp/package-identity.json" --out "$tmp/linked.tar.gz" >/dev/null 2>&1; then
  echo "public-alpha-release: symlink fixture passed" >&2
  exit 1
fi

if [ -n "$before_releases" ]; then
  after_releases="$(GH_TOKEN="${GH_TOKEN:-$GITHUB_TOKEN}" gh api --paginate repos/vibe-agi/hideout/releases --jq 'length' | awk '{s+=$1} END{print s+0}')"
  test "$before_releases" = "$after_releases"
fi

docs_root="$tmp/docs-root"
mkdir -p "$docs_root/docs" "$docs_root/packaging/homebrew" \
  "$docs_root/releases/formulas" "$docs_root/releases/receipts"
cp README.md README.zh-CN.md CHANGELOG.md "$docs_root/"
cp docs/STATUS.md docs/distribution-bootstrap.md docs/support-matrix.md "$docs_root/docs/"
cp packaging/homebrew/hideout.rb "$docs_root/packaging/homebrew/"
jq -n --slurpfile package "$tmp/package-identity.json" \
  --arg observedAt "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --arg commit "$(jq -r '.sourceCommit' "$tmp/package-identity.json")" '
  def asset($name;$digest): {name:$name,bytes:1,apiSHA256:$digest,downloadSHA256:$digest};
  {schema:"hideout.publication-receipt/v1",status:"public-verified",observedAt:$observedAt,
   version:$package[0].productVersion,tag:("v"+$package[0].productVersion),sourceCommit:$commit,
   releaseId:1,url:("https://github.com/vibe-agi/hideout/releases/tag/v"+$package[0].productVersion),
   prerelease:true,immutable:true,package:$package[0],evidenceSHA256:("d"*64),
   assets:[
     asset(("hideout-v"+$package[0].productVersion+"-darwin-arm64.tar.gz");("a"*64)),
     asset(("hideout-v"+$package[0].productVersion+"-evidence.tar.gz");("b"*64)),
     asset(("hideout-v"+$package[0].productVersion+"-release.json");("c"*64)),
     asset("SHA256SUMS";("e"*64))
   ],proofStatus:"satisfied"}
' >"$docs_root/releases/receipts/v0.1.0-dev.0.json"
receipt_sha="$(shasum -a 256 "$docs_root/releases/receipts/v0.1.0-dev.0.json" | awk '{print $1}')"
jq -n --slurpfile package "$tmp/package-identity.json" \
  --arg generatedAt "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" --arg receiptSHA "$receipt_sha" '
  {schema:"hideout.published-release-inventory/v1",generatedAt:$generatedAt,
   current:{version:$package[0].productVersion,tag:("v"+$package[0].productVersion),
    maturity:"public-supervised-alpha",platform:"darwin/arm64",backend:"lima",
    package:$package[0],releaseURL:("https://github.com/vibe-agi/hideout/releases/tag/v"+$package[0].productVersion),
    receiptSHA256:$receiptSHA,supportMatrix:"2026-07-alpha",
    nonClaims:["public-alpha-maturity","workspace-write-blocking"]}}
' >"$docs_root/releases/current.json"
HIDEOUT_DOC_ROOT="$docs_root" scripts/render-public-release-docs.sh \
  --inventory "$docs_root/releases/current.json" >/dev/null
first_render="$(find "$docs_root" -type f ! -path '*/releases/current.json' -exec shasum -a 256 {} \; | LC_ALL=C sort)"
HIDEOUT_DOC_ROOT="$docs_root" scripts/render-public-release-docs.sh \
  --inventory "$docs_root/releases/current.json" >/dev/null
second_render="$(find "$docs_root" -type f ! -path '*/releases/current.json' -exec shasum -a 256 {} \; | LC_ALL=C sort)"
test "$first_render" = "$second_render"
for file in README.md README.zh-CN.md docs/STATUS.md docs/support-matrix.md CHANGELOG.md; do
  grep -F "v0.1.0-dev.0" "$docs_root/$file" >/dev/null
  grep -F 'https://github.com/vibe-agi/hideout/releases/tag/v0.1.0-dev.0' "$docs_root/$file" >/dev/null
done
formula="$docs_root/packaging/homebrew/hideout.rb"
formula_snapshot="$docs_root/releases/formulas/v0.1.0-dev.0.rb"
grep -F 'releases/download/v0.1.0-dev.0/hideout-v0.1.0-dev.0-darwin-arm64.tar.gz' "$formula" >/dev/null
grep -F "sha256 \"$(jq -r '.artifactSHA256' "$tmp/package-identity.json")\"" "$formula" >/dev/null
cmp <(tail -n +3 "$formula") "$formula_snapshot"
formula_drift_root="$tmp/formula-drift-root"
cp -R "$docs_root" "$formula_drift_root"
printf '# injected teaching-surface drift\n' >>"$formula_drift_root/releases/formulas/v0.1.0-dev.0.rb"
if HIDEOUT_DOC_ROOT="$formula_drift_root" scripts/test-doc-truth-smoke.sh \
    --out "$tmp/formula-drift-evidence" >/dev/null 2>&1; then
  echo "public-alpha-release: formula snapshot drift fixture passed" >&2
  exit 1
fi
public_truth="$tmp/public-truth"
HIDEOUT_DOC_ROOT="$docs_root" scripts/test-doc-truth-smoke.sh --out "$public_truth" \
  --public-receipt "$docs_root/releases/receipts/v0.1.0-dev.0.json" >/dev/null
jq -e '
  ([.proofs[].proofId] | sort) ==
  (["033.release.docs-public-truth","033.release.public-download"] | sort)
' "$public_truth/public-release-evidence.json" >/dev/null

# Exercise the exact typed mutations used by the promotion validator. These
# prove partial assets and changed bytes fail before any GitHub publication.
go test ./internal/releasechannel \
  -run 'TestPublicReleaseRejectsIdentityAndAssetMutations|TestPublicReleaseRejectsChangedPackageBytes' \
  -count=1 >/dev/null

public_alpha_cleanup_root "$tmp" "$cleanup_report"
cleanup_complete=1
jq -e '
  .status == "passed" and
  .candidatePathProcessesTerminated >= 1 and
  .candidatePathProcessesRetained == 0 and
  .candidateLimaProcessesRetained == 0 and
  .candidateBrowserProcessesRetained == 0 and
  .candidateTemporaryRootsRetained == 0 and
  .candidateSecretBearingStateRetained == 0
' "$cleanup_report" >/dev/null
if kill -0 "$cleanup_fixture_pid" >/dev/null 2>&1; then
  echo "public-alpha-release: cleanup fixture process survived" >&2
  exit 1
fi
jq -n --slurpfile cleanup "$cleanup_report" '
  {schema:"hideout.public-alpha-rehearsal/v1",publicationStatus:"not-published",
   signing:"fixture-only",notarization:"fixture-only",cleanup:$cleanup[0],
   faultFixtures:{failedGate:"passed",absentApproval:"passed",partialAssetSet:"passed",
     digestDrift:"passed",rebuildAttempt:"passed"},status:"passed"}
' >"$rehearsal_report"
jq -e '.cleanup.status == "passed" and .publicationStatus == "not-published"' \
  "$rehearsal_report" >/dev/null
echo "public-alpha-release: no-publish passed"
