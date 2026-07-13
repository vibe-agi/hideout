#!/usr/bin/env bash
set -euo pipefail

# 032 Gate 0 smoke. It proves local lifecycle and immutable binding mechanics;
# real host effects remain a separate macOS arm64 Lima Gate 2 obligation.

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

evidence_out="${HIDEOUT_HOST_APP_EVIDENCE_DIR:-}"
cleanup_evidence=0
if [ -z "$evidence_out" ]; then
  evidence_out="$(mktemp -d "${TMPDIR:-/tmp}/hideout-host-app-evidence.XXXXXX")"
  cleanup_evidence=1
fi
mkdir -p "$evidence_out/logs"
cleanup() {
  rm -f "${proof_registry_tmp:-}"
  rm -rf "${contributor_root:-}"
  if [ "$cleanup_evidence" -eq 1 ]; then rm -rf "$evidence_out"; fi
}
trap cleanup EXIT
sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

schemas=(
  schemas/host-app-pack.schema.json
  schemas/host-app-pack-registry.schema.json
  schemas/host-app-enablement.schema.json
  schemas/host-app-inspection.schema.json
  schemas/open-resource-intent.schema.json
)
recipes=(
  internal/hostcap/recipes/builtin-vscode.json
  internal/hostcap/recipes/safety-profiles.json
)

echo "host-app-pack-smoke: strict schemas and inert data scaffolds"
for path in "${schemas[@]}" "${recipes[@]}"; do
  test -f "$path"
done

if grep -E '(^|[[:space:]])(pkill|killall)([[:space:]]|$)' \
  scripts/test-host-app-pack-e2e.sh scripts/test-gate2-lima.sh scripts/lib/gate2-projection.sh >/dev/null; then
  echo "host-app-pack-smoke: GUI gates must not use broad process-name cleanup" >&2
  exit 1
fi
if grep -E 'name:[[:space:]]*"[^"]+"[[:space:]]*,[[:space:]]*status:[[:space:]]*"passed"' \
  scripts/test-host-app-pack-e2e.sh >/dev/null; then
  echo "host-app-pack-smoke: evidence prerequisites must use the schema availability enum" >&2
  exit 1
fi
jq empty "${schemas[@]}" "${recipes[@]}"
for path in "${schemas[@]}"; do
  jq -e '
    .["$schema"] == "https://json-schema.org/draft/2020-12/schema" and
    .type == "object" and
    .additionalProperties == false
  ' "$path" >/dev/null
done

jq -e '
  .schemaVersion == "hideout.host-app-pack/v1" and
  .id == "builtin.vscode" and
  (.apps | length == 1) and
  (.bindings | length == 1) and
  (.bindings[0].capabilityId == "host.app.open-resource") and
  (.bindings[0].resultPolicy == "none") and
  ([.. | objects | has("bundleCandidates") or has("designatedRequirement") or has("script") or has("hook") or has("argv")] | any | not)
' internal/hostcap/recipes/builtin-vscode.json >/dev/null

jq -e '
  .schemaVersion == "hideout.host-app-safety-profiles/v1" and
  (.profiles | length == 1) and
  (.profiles[0].id == "vscode-family-v1") and
  (.profiles[0].requiredArgv | index("--disable-extensions") != null) and
  (.profiles[0].forbiddenArgv | index("--disable-workspace-trust") != null) and
  (.profiles[0].requiredSettings["security.workspace.trust.enabled"] == true) and
  (.profiles[0].requiredSettings["task.allowAutomaticTasks"] == "off")
' internal/hostcap/recipes/safety-profiles.json >/dev/null

# T001 establishes package ownership without assuming later phases have not
# started in another worktree process.
test -f internal/packsnapshot/doc.go
test -f internal/hostapppack/doc.go
go list ./internal/packsnapshot ./internal/hostapppack >/dev/null

echo "host-app-pack-smoke: artifact-backed proof registry"
go test ./internal/productevidence -run 'TestHostAppPackProofRegistryRequiresArtifactBackedRealEvidence|TestProofRegistryValidatesAndIsDeterministic'
proof_registry_tmp="$(mktemp "${TMPDIR:-/tmp}/hideout-host-app-pack-proofs.XXXXXX")"
go run ./cmd/hideout support proof-registry --json >"$proof_registry_tmp"
jq -e '
  [.requirements[] | select(.featureId == "032-community-host-app-recipes")] as $rows |
  ($rows | length == 4) and
  ([$rows[] | select(.proofId | startswith("032.host-app-pack.gate0."))] | length == 3) and
  (all($rows[]; .artifactPolicy == "exists-and-digest-if-supplied")) and
  (any($rows[];
    .proofId == "032.host-app-pack.real-gate2.external" and
    .layer == "real-gate" and
    .requiredFor == "release-candidate"
  ))
' "$proof_registry_tmp" >/dev/null

echo "host-app-pack-smoke: immutable source, model, identity, safety, and grammar foundation"
go test \
  ./internal/packsnapshot \
  ./internal/adapterpack \
  ./internal/hostapppack \
  ./internal/cmdproxy \
  ./internal/recovery \
  ./internal/audit \
  ./internal/hostcap \
  ./internal/hostcap/appopen \
  ./internal/cmdgrammar

echo "host-app-pack-smoke: US1 lifecycle and immutable binding evidence"
go test ./internal/hostapppack ./internal/manager ./internal/app \
  -run 'Test(StoreGuidedInstall|HostApp|APIHostApp|CompileHostAppCatalog)' -count=1 \
  2>&1 | tee "$evidence_out/logs/lifecycle.out"
go test ./internal/hostcap ./internal/cmdproxy ./internal/broker ./cmd/hideout-shim \
  -run 'Test(Binding|OpenBound|Projection|GeneratedHostAppShim|NormalizeInvocation)' -count=1 \
  2>&1 | tee "$evidence_out/logs/binding.out"

echo "host-app-pack-smoke: US3 existing HostFS authority consumption"
go test ./internal/hostfs ./internal/manager ./internal/broker ./internal/hostcap ./internal/hostapppack \
  -run 'Test(HostAppResource|StartRunDataPlaneBindsCompleteForbiddenRoots|Projection.*HostFS|ProjectionDecisionBindsPathFreeResourceAuthorityClasses|OpenBoundHostFS|OpenResourceEvidence)' -count=1 \
  2>&1 | tee "$evidence_out/logs/hostfs-resource.out"

echo "host-app-pack-smoke: US2 identity, lifecycle, recovery, and redaction"
go test ./internal/hostcap ./internal/hostapppack ./internal/manager ./internal/broker ./internal/app ./internal/doctor ./internal/recovery \
  -run 'Test(BundleTree|UnverifiedApp|UnverifiedHostApp|HostAppDecision|HostAppUpdate|HostAppDisable|HostAppInspection|HostAppCLIUpdate|ProjectionAudit|RecoveryCode|UnknownRecovery)' -count=1 \
  2>&1 | tee "$evidence_out/logs/identity-safety.out"

echo "host-app-pack-smoke: US4 contributor workflow without Core edits"
contributor_root="$(mktemp -d "${TMPDIR:-/tmp}/hideout-host-app-contributor.XXXXXX")"
contributor_recipe="$contributor_root/recipe"
contributor_store="$contributor_root/store"
core_before="$({ find internal/hostapppack internal/hostcap internal/cmdgrammar internal/cmdproxy -type f -name '*.go' -print | LC_ALL=C sort | xargs shasum -a 256; } | shasum -a 256 | awk '{print $1}')"
go run ./cmd/hideout app init \
  --dir "$contributor_recipe" \
  --id community.smoke-editor \
  --app-id editor \
  --command smoke-editor \
  --bundle 'Smoke Editor.app' \
  --executable Contents/MacOS/SmokeEditor \
  >>"$evidence_out/logs/contributor.out"
HIDEOUT_STORE_ROOT="$contributor_store" go run ./cmd/hideout app validate \
  --path "$contributor_recipe" \
  >>"$evidence_out/logs/contributor.out"
HIDEOUT_STORE_ROOT="$contributor_store" go run ./cmd/hideout app test \
  --path "$contributor_recipe" \
  >>"$evidence_out/logs/contributor.out"
test "$(HIDEOUT_STORE_ROOT="$contributor_store" go run ./cmd/hideout app list --json | jq '[.hostAppPacks[] | select(.builtIn != true)] | length')" -eq 0
HIDEOUT_STORE_ROOT="$contributor_store" go run ./cmd/hideout app add \
  --path "$contributor_recipe" --install-only --yes \
  >>"$evidence_out/logs/contributor.out"
revision_id="$(HIDEOUT_STORE_ROOT="$contributor_store" go run ./cmd/hideout app list --json | jq -er '.hostAppPacks[] | select(.packId == "community.smoke-editor") | .activeRevisionId')"
HIDEOUT_STORE_ROOT="$contributor_store" go run ./cmd/hideout app validate --revision "$revision_id" community.smoke-editor \
  >>"$evidence_out/logs/contributor.out"
HIDEOUT_STORE_ROOT="$contributor_store" go run ./cmd/hideout app test --revision "$revision_id" community.smoke-editor \
  >>"$evidence_out/logs/contributor.out"
jq '.description = "mutated source after immutable install"' "$contributor_recipe/hideout.host-app-pack.json" >"$contributor_root/mutated.json"
mv "$contributor_root/mutated.json" "$contributor_recipe/hideout.host-app-pack.json"
HIDEOUT_STORE_ROOT="$contributor_store" go run ./cmd/hideout app validate --revision "$revision_id" community.smoke-editor \
  >>"$evidence_out/logs/contributor.out"
core_after="$({ find internal/hostapppack internal/hostcap internal/cmdgrammar internal/cmdproxy -type f -name '*.go' -print | LC_ALL=C sort | xargs shasum -a 256; } | shasum -a 256 | awk '{print $1}')"
test "$core_before" = "$core_after"
grep -q '"applied":true' "$evidence_out/logs/contributor.out"

commit="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then dirty=true; else dirty=false; fi
generated_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
lifecycle_sha="$(sha256_file "$evidence_out/logs/lifecycle.out")"
binding_sha="$(sha256_file "$evidence_out/logs/binding.out")"
hostfs_resource_sha="$(sha256_file "$evidence_out/logs/hostfs-resource.out")"
identity_safety_sha="$(sha256_file "$evidence_out/logs/identity-safety.out")"
contributor_sha="$(sha256_file "$evidence_out/logs/contributor.out")"
jq -n \
  --arg generatedAt "$generated_at" \
  --arg commit "$commit" \
  --argjson dirty "$dirty" \
  --arg lifecycleSHA "$lifecycle_sha" \
  --arg bindingSHA "$binding_sha" \
  --arg hostfsResourceSHA "$hostfs_resource_sha" \
  --arg identitySafetySHA "$identity_safety_sha" \
  --arg contributorSHA "$contributor_sha" \
  '{
    version: "hideout.product-hardening-evidence/v1",
    generatedAt: $generatedAt,
    commit: $commit,
    dirty: $dirty,
    proofs: [
      {
        proofId: "032.host-app-pack.gate0.lifecycle",
        featureId: "032-community-host-app-recipes",
        mode: "unit",
        evidenceClass: "gate0-test-log",
        status: "passed",
        commandSummary: "go test hostapppack manager app US1 lifecycle",
        coveredClaims: [{claimId:"032.FR-001",source:"spec",description:"Exact source lifecycle, review, confirmation, atomic add, and profile enablement"}],
        prerequisites: [],
        artifacts: [
          {kind:"log",path:"logs/lifecycle.out",sha256:$lifecycleSHA,redactionStatus:"passed",description:"US1 lifecycle test output"},
          {kind:"log",path:"logs/contributor.out",sha256:$contributorSHA,redactionStatus:"passed",description:"US4 scaffold, immutable install, source-mutation, and no-Core-edit workflow output"}
        ],
        redactionStatus: "passed"
      },
      {
        proofId: "032.host-app-pack.gate0.binding",
        featureId: "032-community-host-app-recipes",
        mode: "unit",
        evidenceClass: "gate0-test-log",
        status: "passed",
        commandSummary: "go test hostcap cmdproxy broker shim immutable binding",
        coveredClaims: [
          {claimId:"032.FR-010",source:"spec",description:"Initial and final app identity checks reject Manager-derived workspace, HostFS-writable, temporary, source, session, and control roots"},
          {claimId:"032.FR-018",source:"spec",description:"Immutable command binding, Core-derived app/resource authority, and no fallback"},
          {claimId:"032.FR-022",source:"spec",description:"Workspace and same-session HostFS resources require existing content authority"},
          {claimId:"032.FR-023",source:"spec",description:"HostFS authority and canonical identity are revalidated at final launch without lower-path output"}
        ],
        prerequisites: [],
        artifacts: [
          {kind:"log",path:"logs/binding.out",sha256:$bindingSHA,redactionStatus:"passed",description:"US1 binding test output"},
          {kind:"log",path:"logs/hostfs-resource.out",sha256:$hostfsResourceSHA,redactionStatus:"passed",description:"US3 HostFS authority consumption and final revalidation output"}
        ],
        redactionStatus: "passed"
      },
      {
        proofId: "032.host-app-pack.gate0.identity-safety",
        featureId: "032-community-host-app-recipes",
        mode: "unit",
        evidenceClass: "gate0-test-log",
        status: "passed",
        commandSummary: "go test hostcap hostapppack manager broker app doctor recovery US2 identity and safety",
        coveredClaims: [
          {claimId:"032.FR-011",source:"spec",description:"Core independently observes signed and unsigned application identity"},
          {claimId:"032.FR-012",source:"spec",description:"Unsigned app trust binds an exact bundle-tree digest and requires retrust after change"},
          {claimId:"032.FR-015",source:"spec",description:"Authority changes produce an exact permission difference and fresh acceptance"},
          {claimId:"032.FR-024",source:"spec",description:"Run approval binds exact app, package, command, run, and owner facts"},
          {claimId:"032.FR-029",source:"spec",description:"Lifecycle and runtime outcomes retain typed path-free audit facts"},
          {claimId:"032.FR-030",source:"spec",description:"Lifecycle and runtime evidence removes control credentials, paths, credentials, and raw argv"}
        ],
        prerequisites: [],
        artifacts: [{kind:"log",path:"logs/identity-safety.out",sha256:$identitySafetySHA,redactionStatus:"passed",description:"US2 identity, lifecycle, recovery, and redaction test output"}],
        redactionStatus: "passed"
      }
    ]
  }' >"$evidence_out/product-hardening-evidence.json"
go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json "$evidence_out/product-hardening-evidence.json"

echo "host-app-pack-smoke: 032 design markdown"
markdownlint-cli2 'specs/032-community-host-app-recipes/**/*.md'

if [ "$cleanup_evidence" -eq 0 ]; then
  echo "host-app-pack-smoke: evidence=$evidence_out/product-hardening-evidence.json"
fi
echo "host-app-pack-smoke: PASS (Gate 0 lifecycle/binding/HostFS authority; real host effect remains Gate 2)"
