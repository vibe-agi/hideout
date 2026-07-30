#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
cd "$repo_root"

out="$repo_root/.artifacts/045/dependencies"

usage() {
  printf '%s\n' \
    "Usage: scripts/gates/dependencies.sh [--out DIR]" \
    "" \
    "Verifies every production/tool module checksum, direct and isolated-helper" \
    "license inventory, embedded BPF source/object/generated digests and" \
    "licenses, and pinned symbol-level vulnerability policy. Evidence is local."
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out)
      if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
        printf 'dependencies-gate: --out requires a directory\n' >&2
        exit 2
      fi
      out="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'dependencies-gate: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

for command in awk diff git go jq; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'dependencies-gate: missing required command: %s\n' "$command" >&2
    exit 1
  fi
done

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  printf 'dependencies-gate: missing shasum or sha256sum\n' >&2
  return 127
}

file_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
    return
  fi
  stat -c '%a' "$1"
}

source_commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
  source_dirty=true
else
  source_dirty=false
fi

run_id="run-$(date -u +'%Y%m%dT%H%M%SZ')-$$"
run_dir="$out/$run_id"
vulnerability_dir="$run_dir/vulnerability"
mkdir -p "$vulnerability_dir"
chmod 0700 "$out" "$run_dir" "$vulnerability_dir"

# Dependency inspection is read-only. In the default module mode, `go list -m
# all` may append checksums for modules outside the tidy build graph even though
# the source did not change. That both dirties a candidate and makes later tidy
# checks order-dependent.
export GOFLAGS="-mod=readonly"
module_inputs=(
  go.mod
  go.sum
  tools/govulncheck/go.mod
  tools/govulncheck/go.sum
  tools/tun2socks-build/go.mod
  tools/tun2socks-build/go.sum
)
write_module_input_manifest() {
  local destination="$1" input
  : >"$destination"
  for input in "${module_inputs[@]}"; do
    if [ ! -f "$input" ] || [ -L "$input" ]; then
      printf 'dependencies-gate: module input is missing or unsafe: %s\n' \
        "$input" >&2
      return 1
    fi
    printf '%s  %s\n' "$(sha256_file "$input")" "$input" \
      >>"$destination"
  done
}
write_module_input_manifest "$run_dir/module-inputs-before.sha256"

module_verify_log="$run_dir/module-verify.log"
{
  printf 'root: '
  GOWORK=off go mod verify
  printf 'tools/govulncheck: '
  GOWORK=off go -C tools/govulncheck mod verify
  printf 'tools/tun2socks-build: '
  GOWORK=off go -C tools/tun2socks-build mod verify
} 2>&1 | tee "$module_verify_log"

GOWORK=off go list -m -json all |
  jq -s 'sort_by(.Path, .Version // "")' >"$run_dir/modules-root.json"
GOWORK=off go -C tools/govulncheck list -m -json all |
  jq -s 'sort_by(.Path, .Version // "")' \
    >"$run_dir/modules-govulncheck-tool.json"
GOWORK=off go -C tools/tun2socks-build list -m -json all |
  jq -s 'sort_by(.Path, .Version // "")' \
    >"$run_dir/modules-tun2socks-helper.json"

license_log="$run_dir/licenses.log"
scripts/gates/dependency-licenses.sh 2>&1 | tee "$license_log"

bpf_test_log="$run_dir/bpf-manifest-tests.log"
go test ./internal/workloadobs/collector/bpf \
  -run 'TestEmbedded.*ArtifactManifestAndObjectAreExact|TestObserverArtifactManifestRejectsUnknownFieldsAndDigestDrift' \
  -count=1 -v 2>&1 | tee "$bpf_test_log"

jq -s \
  --arg noticeSHA256 "$(sha256_file THIRD_PARTY_NOTICES.md)" \
  --arg gplTextSHA256 "$(sha256_file LICENSES/GPL-2.0-only.txt)" \
  '{
    schema: "hideout.bpf-dependency-provenance/v1",
    manifests: sort_by(.source),
    noticeSHA256: $noticeSHA256,
    gplTextSHA256: $gplTextSHA256
  }' \
  internal/workloadobs/collector/bpf/observer.generated.json \
  internal/workloadobs/collector/bpf/file_observer.generated.json \
  internal/workloadobs/collector/bpf/network_observer.generated.json \
  >"$run_dir/bpf-provenance.json"

vulnerability_log="$run_dir/vulnerability.log"
scripts/test-vulnerability-gate.sh \
  --self-test \
  --source \
  --evidence-dir "$vulnerability_dir" 2>&1 |
  tee "$vulnerability_log"

write_module_input_manifest "$run_dir/module-inputs-after.sha256"
if [ "$(cat "$run_dir/module-inputs-before.sha256")" != \
  "$(cat "$run_dir/module-inputs-after.sha256")" ]; then
  printf 'dependencies-gate: dependency inspection modified module inputs\n' \
    >&2
  diff -u \
    "$run_dir/module-inputs-before.sha256" \
    "$run_dir/module-inputs-after.sha256" >&2 || true
  exit 1
fi

for evidence_file in \
  "$run_dir/module-inputs-before.sha256" \
  "$run_dir/module-inputs-after.sha256" \
  "$module_verify_log" \
  "$run_dir/modules-root.json" \
  "$run_dir/modules-govulncheck-tool.json" \
  "$run_dir/modules-tun2socks-helper.json" \
  "$license_log" \
  "$bpf_test_log" \
  "$run_dir/bpf-provenance.json" \
  "$vulnerability_log" \
  "$vulnerability_dir/summary.json"; do
  if [ ! -s "$evidence_file" ]; then
    printf \
      'dependencies-gate: required evidence is missing or empty: %s\n' \
      "$evidence_file" >&2
    exit 1
  fi
done

if ! jq -e '
  .schema == "hideout.bpf-dependency-provenance/v1" and
  (.manifests | length) == 3 and
  all(.manifests[];
    .schema == "hideout.generated-bpf/v2" and
    .target == "bpfel" and
    .compiler == "clang" and
    .compilerVersion == "19.1.7" and
    .goVersion == "go1.25.12" and
    .bpf2goVersion == "v0.22.0" and
    .license == "Apache-2.0 OR GPL-2.0-only" and
    .kernelProgramLicense == "GPL" and
    (.sourceSHA256 | test("^[a-f0-9]{64}$")) and
    (.objectSHA256 | test("^[a-f0-9]{64}$")) and
    (.generatedGoSHA256 | test("^[a-f0-9]{64}$"))
  ) and
  (.noticeSHA256 | test("^[a-f0-9]{64}$")) and
  (.gplTextSHA256 | test("^[a-f0-9]{64}$"))
' "$run_dir/bpf-provenance.json" >/dev/null; then
  printf 'dependencies-gate: BPF provenance failed validation\n' >&2
  exit 1
fi

if ! jq -e '
  .schema == "hideout.vulnerability-gate-evidence/v1" and
  .result == "passed" and
  .executed.policySelfTest == true and
  .executed.source == true and
  .scanner.scanLevel == "symbol" and
  (
    .policy.allowedModuleOnlyAdvisory as $allowed
    | all(.sourceFindings[];
        .reachability == "module-only-unreachable-by-import-graph" and
        .id == $allowed.id and
        .module == $allowed.module and
        .version == $allowed.selectedVersion and
        .fixedVersion == null and
        (.importedPackages | length) == 0
      )
  )
' "$vulnerability_dir/summary.json" >/dev/null; then
  printf 'dependencies-gate: vulnerability evidence failed validation\n' >&2
  exit 1
fi

if ! jq -e '
  any(.[]; .Path == "github.com/cilium/ebpf" and .Version == "v0.22.0")
' "$run_dir/modules-root.json" >/dev/null ||
  ! jq -e '
    any(.[];
      .Path == "github.com/xjasonlyu/tun2socks/v2" and
      .Version == "v2.6.0"
    ) and
    any(.[];
      .Path == "github.com/go-chi/chi/v5" and .Version == "v5.3.1"
    ) and
    any(.[];
      .Path == "golang.org/x/crypto" and .Version == "v0.54.0"
    )
  ' "$run_dir/modules-tun2socks-helper.json" >/dev/null ||
  ! jq -e '
    any(.[]; .Path == "golang.org/x/vuln" and .Version == "v1.6.0")
  ' "$run_dir/modules-govulncheck-tool.json" >/dev/null; then
  printf 'dependencies-gate: pinned dependency inventory drifted\n' >&2
  exit 1
fi

find "$run_dir" -type f -exec chmod 0600 {} +
while IFS= read -r evidence_file; do
  if [ "$(file_mode "$evidence_file")" != "600" ]; then
    printf \
      'dependencies-gate: evidence mode is not 0600: %s\n' \
      "$evidence_file" >&2
    exit 1
  fi
done < <(find "$run_dir" -type f | LC_ALL=C sort)

artifacts='[]'
while IFS= read -r evidence_file; do
  relative_path="${evidence_file#"$out"/}"
  artifacts="$(
    jq -c \
      --arg path "$relative_path" \
      --arg sha256 "$(sha256_file "$evidence_file")" \
      '. + [{path: $path, sha256: $sha256}]' <<<"$artifacts"
  )"
done < <(find "$run_dir" -type f | LC_ALL=C sort)

summary="$out/summary.json"
jq -n \
  --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
  --arg commit "$source_commit" \
  --argjson dirty "$source_dirty" \
  --arg run "$run_id" \
  --arg goVersion "$(GOWORK=off go env GOVERSION)" \
  --argjson vulnerability "$(cat "$vulnerability_dir/summary.json")" \
  --argjson bpf "$(cat "$run_dir/bpf-provenance.json")" \
  --argjson rootModules "$(cat "$run_dir/modules-root.json")" \
  --argjson helperModules "$(cat "$run_dir/modules-tun2socks-helper.json")" \
  --argjson scannerModules "$(cat "$run_dir/modules-govulncheck-tool.json")" \
  --argjson artifacts "$artifacts" \
  '{
    schema: "hideout.dependencies-gate/v1",
    generatedAt: $generatedAt,
    source: {commit: $commit, dirty: $dirty},
    result: "passed",
    run: $run,
    goVersion: $goVersion,
    checks: {
      moduleInputsReadOnly: "passed",
      moduleChecksums: "passed",
      directAndIsolatedLicenses: "passed",
      bpfSourceObjectGeneratedDigests: "passed",
      bpfSourceAndKernelLicenses: "passed",
      symbolVulnerabilityScan: "passed",
      vulnerabilityPolicySelfTest: "passed"
    },
    dependencyCounts: {
      root: ($rootModules | length),
      tun2socksHelper: ($helperModules | length),
      scannerTool: ($scannerModules | length)
    },
    advisories: {
      findings: $vulnerability.sourceFindings,
      allowedModuleOnlyPolicy:
        $vulnerability.policy.allowedModuleOnlyAdvisory,
      reachableImportedPackageFindings: 0,
      explanation:
        "Every package/symbol trace is rejected. The only permitted module-only record is rebound on every scan to its exact no-fix openpgp-only OSV scope and selected module version."
    },
    embeddedBPF: {
      count: ($bpf.manifests | length),
      manifests: $bpf.manifests,
      noticeSHA256: $bpf.noticeSHA256,
      gplTextSHA256: $bpf.gplTextSHA256
    },
    artifacts: $artifacts,
    packageBinaryBoundary:
      "This source gate does not substitute for scanning every final package binary; T158/T159 and the exact-candidate gate must run govulncheck -mode=binary over all manifest-listed Go binaries.",
    claimBoundary:
      "Module-only/unreachable is a reproducible import-graph classification for this exact symbol scan, not deletion of the advisory, dependency, or license obligation."
  }' >"$summary"
chmod 0600 "$summary"

if ! jq -e '
  .schema == "hideout.dependencies-gate/v1" and
  .result == "passed" and
  all(.checks[]; . == "passed") and
  .advisories.reachableImportedPackageFindings == 0 and
  (
    .advisories.allowedModuleOnlyPolicy as $allowed
    | all(.advisories.findings[];
        .reachability == "module-only-unreachable-by-import-graph" and
        .id == $allowed.id and
        .fixedVersion == null and
        (.importedPackages | length) == 0
      )
  ) and
  .embeddedBPF.count == 3 and
  (.artifacts | length) >= 12
' "$summary" >/dev/null; then
  printf 'dependencies-gate: generated summary failed validation\n' >&2
  exit 1
fi

printf \
  'dependencies-gate: passed evidence=%s run=%s\n' \
  "$summary" "$run_id"
