#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

# Several app tests intentionally launch bounded helper processes. Unbounded
# package-level parallelism can starve those helpers on high-core hosts and
# turn their product timeouts into scheduler-load flakes. Keep enough package
# concurrency for useful throughput while making Gate 0 deterministic.
go_test_parallelism="${HIDEOUT_GATE0_GO_TEST_PARALLELISM:-4}"

# --quick is the inner-loop tier: vet, format, cached tests, lint, schema
# syntax. It allows Go test caching so only changed packages re-run, and it
# proves nothing about smokes, evidence, or packaging. Full gate0 (no flag)
# remains required before any commit or claim.
if [ "${1:-}" = "--quick" ]; then
  go vet ./...
  unformatted="$(gofmt -l cmd internal test)"
  if [ -n "$unformatted" ]; then
    echo "gate0 --quick: gofmt required for:" >&2
    echo "$unformatted" >&2
    exit 1
  fi
  go test -p "$go_test_parallelism" ./...
  markdownlint-cli2 'docs/*.md'
  jq empty schemas/*.json
  echo "gate0 --quick passed; run the full gate before commit"
  exit 0
fi

go build ./...
go vet ./...
unformatted="$(gofmt -l cmd internal test)"
if [ -n "$unformatted" ]; then
  echo "gate0: gofmt required for:" >&2
  echo "$unformatted" >&2
  exit 1
fi
go test -p "$go_test_parallelism" -count=1 ./...
scripts/test-formal-models.sh
scripts/test-install-smoke.sh
scripts/test-package-smoke.sh
scripts/test-standalone-install.sh
markdownlint-cli2 'docs/*.md'
jq empty schemas/*.json
test -f schemas/init-plan.schema.json
test -f schemas/init-audit-event.schema.json
test -f schemas/helper-manifest.schema.json
test -f schemas/run-plan.schema.json
test -f schemas/run-result.schema.json
test -f schemas/release-dogfood.schema.json
test -f schemas/export-artifact.schema.json
test -f schemas/daemon-status.schema.json
test -f schemas/daemon-event.schema.json
test -f schemas/live-console-seed.schema.json
test -f schemas/command-adapter.schema.json
test -f schemas/adapter-pack-manifest.schema.json
test -f schemas/adapter-pack-registry.schema.json
test -f schemas/guest-privilege-status.schema.json
test -f schemas/hostfs-write-decision.schema.json
test -f schemas/hostfs-write-event.schema.json
test -f schemas/decision-record.schema.json
test -f schemas/trusted-host-app-grant.schema.json
test -f schemas/notice-record.schema.json
test -f schemas/onboarding-evidence.schema.json
test -f schemas/doctor-report.schema.json
test -f schemas/support-matrix.schema.json
test -f schemas/release-readiness.schema.json
test -f schemas/product-hardening-evidence.schema.json
test -f schemas/public-release.schema.json
test -f schemas/public-evidence-bundle.schema.json
test -f schemas/release-package-verification.schema.json
test -f schemas/publication-receipt.schema.json
test -f schemas/published-release-inventory.schema.json
test -f schemas/hostfs-read-grants.schema.json
test -f schemas/runtime-catalog.schema.json
test -f schemas/runtime-verification.schema.json
test -f schemas/host-app-pack.schema.json
test -f schemas/host-app-pack-registry.schema.json
test -f schemas/host-app-enablement.schema.json
test -f schemas/host-app-inspection.schema.json
test -f schemas/active-session-summary.schema.json
test -f schemas/environment-activation-receipt.schema.json
test -f schemas/environment-service-state.schema.json
test -f schemas/lifecycle-journal.schema.json
test -f schemas/lifecycle-status.schema.json
test -f schemas/workspace-research-decision.schema.json
test -f schemas/workspace-attachment.schema.json
test -f schemas/environment-summary.schema.json
test -f cmd/hideout-workspace-probe/main.go
test -f test/fixtures/workspaceattach/generate.sh
test -f scripts/lib/workspace-research.sh
scripts/test-runtime-smoke.sh

# Test/evidence spine (026): one Go-owned proof registry feeds shell gates,
# docs truth, and release supporting evidence.
proof_registry_tmp="$(mktemp "${TMPDIR:-/tmp}/hideout-proof-registry.XXXXXX")"
go run ./cmd/hideout support proof-registry --json >"$proof_registry_tmp"
jq -e '
  .schema == "hideout.proof-registry/v1" and
  any(.requirements[]; .featureId == "021-ui-e2e-proof") and
  any(.requirements[]; .featureId == "025-documentation-truth-gate") and
  ([.requirements[] | select(.featureId == "029-hostfs-discoverable-namespace")] | length == 8) and
  ([.requirements[] | select(.featureId == "031-supported-cli-runtime")] | length == 8) and
  ([.requirements[] | select(.featureId == "032-community-host-app-recipes")] | length == 4) and
  ([.requirements[] | select(.featureId == "033-public-alpha-release-channel")] | length == 7) and
  ([.requirements[] | select(.featureId == "035-shared-default-vm-cross-workspace")] | length == 5) and
  ([.requirements[] | select(.featureId == "038-zero-friction-setup")] | length == 8) and
  ([.requirements[] | select(.featureId == "039-trusted-host-app-grant")] | length == 2) and
  ([.requirements[] | select(.featureId == "041-workspace-executable-support")] | length == 4) and
  ([.requirements[] | select(.featureId == "042-disposable-orphan-recovery")] | length == 5) and
  ([.requirements[] | select(.featureId == "043-projection-readiness-proof")] | length == 5) and
  ([.requirements[] | select(.featureId == "044-ordinary-user-release")] | length == 7)
' "$proof_registry_tmp" >/dev/null
rm -f "$proof_registry_tmp"

# Recovery codes (028): one Go-owned recovery registry feeds doctor/package/
# init/readiness surfaces and documentation truth.
recovery_registry_tmp="$(mktemp "${TMPDIR:-/tmp}/hideout-recovery-registry.XXXXXX")"
go run ./cmd/hideout support recovery-codes --json >"$recovery_registry_tmp"
jq -e '
  .schema == "hideout.recovery-codes/v1" and
  any(.codes[]; .code == "package.prerequisite.missing") and
  any(.codes[]; .code == "release.gate-evidence.missing") and
  any(.codes[]; .code == "release.package.identity-invalid") and
  any(.codes[]; .code == "release.signing.required") and
  any(.codes[]; .code == "release.notarization.required") and
  any(.codes[]; .code == "workspace.transport.unsupported") and
  any(.codes[]; .code == "workspace.root.unstable") and
  any(.codes[]; .code == "workspace.host-permission.denied") and
  any(.codes[]; .code == "workspace.capacity.exhausted") and
  any(.codes[]; .code == "workspace.cleanup.unproved") and
  any(.codes[]; .code == "workspace.external-metadata.unsupported")
' "$recovery_registry_tmp" >/dev/null
rm -f "$recovery_registry_tmp"

test -f packaging/homebrew/hideout.rb
if command -v ruby >/dev/null 2>&1; then
  ruby -c packaging/homebrew/hideout.rb >/dev/null
fi
published_tag="$(jq -er '.current.tag' releases/current.json)"
published_version="$(jq -er '.current.version' releases/current.json)"
published_sha="$(jq -er '.current.package.artifactSHA256' releases/current.json)"
test "$published_tag" = "v$published_version"
grep -Fq "url \"https://github.com/vibe-agi/hideout/releases/download/$published_tag/hideout-$published_tag-darwin-arm64.tar.gz\"" packaging/homebrew/hideout.rb
grep -Fq "sha256 \"$published_sha\"" packaging/homebrew/hideout.rb
grep -q 'depends_on "lima"' packaging/homebrew/hideout.rb
grep -q 'skip_clean "bin/hideout-dns-stub-linux-arm64"' packaging/homebrew/hideout.rb
grep -q 'system "/usr/bin/codesign", "--verify", "--strict"' packaging/homebrew/hideout.rb
grep -q '"--skip-init"' packaging/homebrew/hideout.rb
grep -q '"package", "verify", prefix' packaging/homebrew/hideout.rb
if grep -q 'depends_on "go"' packaging/homebrew/hideout.rb; then
  echo "gate0: Homebrew formula must consume the signed package, not rebuild source" >&2
  exit 1
fi
grep -q 'Initialization Is Planned, Not Scripted' docs/architecture-principles.md
grep -q 'bundle.installScript' docs/ecosystem-foundation-design.md
grep -q 'project.initScript' docs/ecosystem-foundation-design.md

phase1_required_plan="$(HIDEOUT_PHASE1_PRINT_PLAN=1 scripts/test-phase1.sh --required)"
printf '%s\n' "$phase1_required_plan" | grep -q 'Gate 0 static contract'
printf '%s\n' "$phase1_required_plan" | grep -q 'Gate 1 native smoke'
printf '%s\n' "$phase1_required_plan" | grep -q 'Gate 2 Lima E2E'
printf '%s\n' "$phase1_required_plan" | grep -q 'Gate 3 hidden proxy'
printf '%s\n' "$phase1_required_plan" | grep -q 'Gate 4 host escape boundary dry-run'
if printf '%s\n' "$phase1_required_plan" | grep -Eiq 'Capability probe|lab|Web UI|hideoutd|daemon'; then
  echo "gate0: required Phase 1 plan must not depend on lab commands, Web UI, or daemon" >&2
  printf '%s\n' "$phase1_required_plan" >&2
  exit 1
fi

release_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-evidence-smoke.XXXXXX")"
release_secret="socks5://user:pass@127.0.0.1:7890"
HIDEOUT_PHASE1_PRINT_PLAN=1 \
  HIDEOUT_SECRET_DEFAULT_PROXY="$release_secret" \
  HIDEOUT_RELEASE_EVIDENCE_DIR="$release_tmp/evidence" \
  scripts/test-release-dogfood.sh >"$release_tmp/stdout" 2>"$release_tmp/stderr"
test -f "$release_tmp/evidence/manifest.json"
test -f "$release_tmp/evidence/test-release-dogfood.log"
jq -e '
  .schema == "hideout.release-dogfood.v1" and
  .status == "passed" and
  .command == "scripts/test-phase1.sh --release-candidate" and
  .operatorProxy.provided == true and
  .operatorProxy.scheme == "socks5" and
  .operatorProxy.url == "redacted" and
  (.releaseArtifact.file | test("^hideout-[A-Za-z0-9_.-]+\\.tar\\.gz$")) and
  (.releaseArtifact.sha256 | test("^[a-f0-9]{64}$")) and
  (.releaseArtifact.bytes > 0) and
  (.releaseArtifact.hideoutVersion.version | length > 0) and
  (.releaseArtifact.hideoutVersion.commit == .git.commit) and
  (.releaseArtifact.hideoutVersion.builtAt | length > 0) and
  (.releaseArtifact.hideoutVersion.go | length > 0) and
  (.releaseArtifact.hideoutVersion.platform | test("^[^/]+/[^/]+$")) and
  .cleanup.gate4BrowserProcesses == 0 and
  .cleanup.gate4TempDirs == 0 and
  .cleanup.hideoutLimaInstances == 0
' "$release_tmp/evidence/manifest.json" >/dev/null
release_artifact_file="$(jq -r '.releaseArtifact.file' "$release_tmp/evidence/manifest.json")"
test -f "$release_tmp/evidence/$release_artifact_file"
if command -v shasum >/dev/null 2>&1; then
  release_artifact_sha="$(shasum -a 256 "$release_tmp/evidence/$release_artifact_file" | awk '{print $1}')"
elif command -v sha256sum >/dev/null 2>&1; then
  release_artifact_sha="$(sha256sum "$release_tmp/evidence/$release_artifact_file" | awk '{print $1}')"
else
  echo "gate0: missing shasum or sha256sum" >&2
  exit 127
fi
test "$release_artifact_sha" = "$(jq -r '.releaseArtifact.sha256' "$release_tmp/evidence/manifest.json")"
release_artifact_extract="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-artifact-extract.XXXXXX")"
tar -xzf "$release_tmp/evidence/$release_artifact_file" -C "$release_artifact_extract"
"$release_artifact_extract/hideout/bin/hideout" version >"$release_tmp/artifact-version.out"
grep -qx "hideout $(jq -r '.releaseArtifact.hideoutVersion.version' "$release_tmp/evidence/manifest.json")" "$release_tmp/artifact-version.out"
grep -qx "commit: $(jq -r '.releaseArtifact.hideoutVersion.commit' "$release_tmp/evidence/manifest.json")" "$release_tmp/artifact-version.out"
grep -qx "builtAt: $(jq -r '.releaseArtifact.hideoutVersion.builtAt' "$release_tmp/evidence/manifest.json")" "$release_tmp/artifact-version.out"
grep -qx "go: $(jq -r '.releaseArtifact.hideoutVersion.go' "$release_tmp/evidence/manifest.json")" "$release_tmp/artifact-version.out"
grep -qx "platform: $(jq -r '.releaseArtifact.hideoutVersion.platform' "$release_tmp/evidence/manifest.json")" "$release_tmp/artifact-version.out"
rm -rf "$release_artifact_extract"
grep -q 'phase1-plan: Gate 2 Lima E2E' "$release_tmp/evidence/test-release-dogfood.log"
grep -q 'phase1-plan: Gate 2 exact runtime family developer-standard' "$release_tmp/evidence/test-release-dogfood.log"
grep -q 'phase1-plan: Gate 3 exact runtime family developer-standard' "$release_tmp/evidence/test-release-dogfood.log"
if grep -R --fixed-strings "$release_secret" "$release_tmp" >/dev/null 2>&1; then
  echo "gate0: release dogfood evidence leaked operator proxy URL" >&2
  exit 1
fi
rm -rf "$release_tmp"

# Isolation-evidence machine-readable contract (no Lima): per-gate emission,
# manifest aggregation shape, and release-dogfood schema for isolationGates /
# environmentSnapshot.
scripts/test-isolation-evidence-smoke.sh

# Export/share redaction boundary (no Lima): three source surfaces, schema,
# control-plane cleanliness, user selection, and evidentiary fail-closed.
scripts/test-export-redaction-smoke.sh

# hideoutd local control-plane boundary (no Lima): lifecycle, guest-unreachable
# socket placement, token auth + audited refusals, Manager parity, event stream,
# and ordered stop.
scripts/test-daemon-smoke.sh

# Daemon-owned normal run path (034): exact bytes/exit, private session socket,
# real local PTY/resize/Ctrl-C, and two independent same-workspace workers.
scripts/test-daemon-session-smoke.sh
scripts/test-daemon-session-pty.sh
scripts/test-concurrent-sessions-e2e.sh
# Shared-default cross-workspace mechanics (035): two projects converge on one
# machine incarnation while daemon workers retain distinct immutable views.
# This local lane intentionally makes no real filesystem-isolation claim.
scripts/test-shared-workspace-smoke.sh
# Shared Workspace Portal executable mechanics (041): local flag semantics,
# Linux arm64 compile contract, strict evidence judge, and proof registration.
# Direct execution support still requires the clean packaged real Lima gate.
scripts/test-workspace-executable-smoke.sh

# Disposable orphan recovery (042): exact authority, durable intent, stable
# absence, record-last convergence, restart replay, strict evidence judge, and
# the dedicated TLC model. This local lane makes no real Lima recovery claim.
scripts/test-disposable-recovery-smoke.sh

# Daemon live operations console (no Lima): typed seed/event contracts and
# payload-driven UI proof. Initially a skeleton smoke, expanded by 007.
scripts/test-live-console-smoke.sh

# Operator decision center (no Lima): actionable decisions vs notices,
# share/export approval, redaction, and local UI/watch contracts.
scripts/test-decision-center-smoke.sh

# Command capability adapters (no isolation claim): strict adapter schema,
# local digest pinning, command-name routing, and root-sensitive intent wording.
scripts/test-command-adapter-smoke.sh
scripts/test-adapter-pack-smoke.sh

# Community host-app recipes (032): strict package/registry/enablement/
# inspection schemas, inert built-in recipe and Core safety-profile data, and
# artifact-backed proof ownership. Setup smoke grants no runtime authority.
scripts/test-host-app-pack-smoke.sh

# Projection readiness evidence judge (043): one exact-candidate positive
# fixture plus mandatory forged marker, package, sample, p95, external-pack,
# and persistent-grant false-green refusals. This does not claim a real gate.
scripts/test-projection-readiness-smoke.sh

# Guest privilege separation and risk audit (Lima proof added by 009 polish):
# status schema/classifier and no guest-root containment overclaim.
scripts/test-privilege-separation-smoke.sh

# HostFS write overlay (010): staged write/apply schema presence. Expanded by
# 010 implementation and real Gate 2 HostFS smoke.
scripts/test-hostfs-write-overlay-smoke.sh

# HostFS discoverable namespace (029): local policy, typed-error, decision, and
# injected-redaction proof. The local lane is intentionally unable to emit the
# two real Gate 2 proof IDs.
hostfs_visibility_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-hostfs-visibility-gate0.XXXXXX")"
scripts/test-hostfs-visibility-e2e.sh --local-fast --out "$hostfs_visibility_tmp"
jq -e '
  all(.proofs[];
    .proofId != "029.hostfs-visibility.real-gate2.namespace" and
    .proofId != "029.hostfs-visibility.real-gate2.live-grant"
  )
' "$hostfs_visibility_tmp/product-hardening-evidence.json" >/dev/null
rm -rf "$hostfs_visibility_tmp"

# HostFS/decision E2E proof (023): local-fast product-hardening evidence for
# staged overlay lifecycle, decision outcomes, model visibility, and redaction.
# Real Gate 2 HostFS data-plane proof remains explicit and prerequisite-gated.
hostfs_decision_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-hostfs-decision-gate0.XXXXXX")"
scripts/test-hostfs-decision-e2e.sh --local-fast --out "$hostfs_decision_tmp"
rm -rf "$hostfs_decision_tmp"

# Profile templates and first-run onboarding (014): built-in templates,
# hardened privilege honesty, no default HostFS/adapter authority, evidence
# schema, and docs commands.
scripts/test-onboarding-smoke.sh

# Doctor diagnostics, recovery, and stable error hints (015/028): local/light
# report, JSON schema, optional recovery code fields, explicit doctor evidence
# export, required failure, warning exit semantics, and safe recovery dry-run.
scripts/test-doctor-smoke.sh

# Doctor/package recovery E2E (024): existing package repair and doctor safe-fix
# paths with product-hardening evidence. This remains local recovery evidence,
# not release readiness or real backend proof.
recovery_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-recovery-gate0.XXXXXX")"
scripts/test-doctor-package-recovery-e2e.sh --local-fast --out "$recovery_tmp"
rm -rf "$recovery_tmp"

# Ordinary-user release convergence (044): targeted help, doctor, support,
# package-helper, upgrade, repair, uninstall, purge, redaction, docs, mutation,
# and negative-fixture evidence. This local lane cannot satisfy real Gate 2/3,
# required UI execution, signing/notarization, or public receipt requirements.
ordinary_user_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-ordinary-user-gate0.XXXXXX")"
scripts/test-ordinary-user-release.sh --local-fast \
  --out "$ordinary_user_tmp/product-hardening-evidence.json"
go run ./internal/productevidence/cmd/validate-044 \
  --target targeted-completion "$ordinary_user_tmp/product-hardening-evidence.json" >/dev/null
rm -rf "$ordinary_user_tmp"
scripts/test-release-readiness.sh --negative-fixtures

# Documentation truth gate (025): claim-boundary registry, known-overclaim scan,
# curated command examples, localized README canonicality, and Gate 0/docs
# consistency. This is local docs correctness evidence, not release readiness.
doc_truth_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-doc-truth-gate0.XXXXXX")"
scripts/test-doc-truth-smoke.sh --out "$doc_truth_tmp"
rm -rf "$doc_truth_tmp"

# Release hardening and compatibility matrix (016): support matrix, readiness
# artifact shape, local-fast honesty, release-candidate missing-evidence
# fail-closed, doctor/version alignment, and docs drift guard.
scripts/test-release-hardening-smoke.sh

# Public alpha channel (033): strict release/workflow contracts plus a package
# install in a fresh HOME with no source tree or Go on PATH. This local lane
# withholds Lima and requires doctor to report that missing prerequisite
# honestly. It does not sign, notarize, publish, create a Lima guest, or claim
# public release.
scripts/test-public-alpha-release.sh --contract-only
public_alpha_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-public-alpha-gate0.XXXXXX")"
if ! scripts/package-local.sh \
  --out "$public_alpha_tmp/hideout-v0.1.0-dev.0-darwin-arm64.tar.gz" >/dev/null; then
  echo "gate0: public-alpha package build failed" >&2
  exit 1
fi
if ! scripts/test-public-alpha-clean-install.sh \
  --package "$public_alpha_tmp/hideout-v0.1.0-dev.0-darwin-arm64.tar.gz" \
  --out "$public_alpha_tmp/clean-install.json" >/dev/null; then
  echo "gate0: public-alpha clean install failed" >&2
  exit 1
fi
if ! jq -e '
  .schema == "hideout.public-alpha-clean-install/v1" and
  .install.status == "passed" and
  .install.sourceCheckoutUsed == false and
  .install.goOnPATH == false and
  .install.profileCreated == false and
  .install.doctorLight == "prerequisite-missing" and
  .prerequisites.lima.status == "missing" and
  .realLima.status == "not-run"
' "$public_alpha_tmp/clean-install.json" >/dev/null; then
  echo "gate0: public-alpha clean-install evidence mismatch:" >&2
  cat "$public_alpha_tmp/clean-install.json" >&2 || true
  exit 1
fi
rm -rf "$public_alpha_tmp"

# First-run alpha path (020): package install docs, privacy/Lima default,
# native-only-as-harness wording, doctor recovery commands, and no stale go-run
# examples in the user-facing path.
scripts/test-first-run-docs-smoke.sh

# Alpha first-run E2E proof (022) and zero-friction setup (038) share one
# candidate package. The setup lane executes the installed binary in a real PTY
# and proves configuration-only behavior; neither local lane is Lima evidence.
first_run_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-first-run-gate0.XXXXXX")"
scripts/package-local.sh --out "$first_run_tmp/hideout.tar.gz" >"$first_run_tmp/package.out"
scripts/test-first-run-e2e.sh --local-fast --package "$first_run_tmp/hideout.tar.gz" --out "$first_run_tmp/022"
scripts/test-first-run-e2e.sh --setup-local-fast --package "$first_run_tmp/hideout.tar.gz" --out "$first_run_tmp/038"
rm -rf "$first_run_tmp"

# UI E2E product-hardening evidence (021): schema and not-run semantics only in
# Gate 0. Targeted browser/PTY completion requires --require-executed on a host
# with those prerequisites.
ui_e2e_tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-ui-e2e-gate0.XXXXXX")"
scripts/test-ui-e2e.sh --all --out "$ui_e2e_tmp"
rm -rf "$ui_e2e_tmp"

# Concurrent run sessions (034): schemas, ownership/transition models, shared
# service identity, namespace command construction, and Manager wiring. Real
# process/mount isolation remains a separate explicit macOS/Lima Gate 2.
scripts/test-concurrent-sessions-smoke.sh

# Resource lifecycle and final-session stop (036): closed catalog, pure stop
# reducer, strict journal, typed backend observation, and redaction. Real Lima
# stop behavior remains an explicit lifecycle Gate 2 lane.
scripts/test-lifecycle-smoke.sh
