# Implementation Plan: Public Alpha Release Channel

**Branch**: `033-public-alpha-release-channel` | **Date**: 2026-07-13 |
**Spec**: [spec.md](spec.md)

**Input**: Feature specification from
`/specs/033-public-alpha-release-channel/spec.md`

## Summary

Turn the existing release-like package, product-evidence registry, real Gate 2
and Gate 3 evidence, and runtime retain/promote precedent into one supervised
macOS arm64 public alpha channel. The design introduces an explicit
product-version/source-commit/archive-digest identity, strict public release
and evidence contracts, one canonical package/install-state v1, a
build-retain-promote workflow, Developer ID and online-notarization
observation, clean-install and real-gate proof bound to the exact archive, and
two-phase documentation truth. No runtime capability or authority surface is
added.

## Technical Context

**Language/Version**: Go 1.25.0; POSIX shell/Bash; GitHub Actions YAML; JSON
Schema 2020-12; Markdown

**Primary Dependencies**: Existing `packagekit`, `productevidence`,
`releasecompat`, `runtimecatalog`, `recovery`, and schema validator packages;
macOS `codesign` (including online notarization-ticket checks) and `notarytool`;
`gh`, `jq`, SHA-256 tools, and `limactl`

**Storage**: Strict JSON manifests and receipts, `tar.gz` package/evidence
assets, package install state below the install prefix, durable operator state
below the existing store, GitHub draft/prerelease assets, and workflow evidence

**Testing**: Go unit/contract tests; schema adversarial fixtures; package,
doctor, docs, and release smoke scripts; `go build`, `go vet`, `gofmt`,
`git diff --check`, `go test ./...`; Gate 0; clean-machine install E2E; real
macOS Gate 2 and Gate 3; anonymous post-publication download verification

**Target Platform**: Public package for macOS arm64 with Linux arm64 guest;
GitHub-hosted macOS arm64 for package construction and signing; an independent
real macOS arm64 operator lane for nested Lima gates; Ubuntu for retention and
promotion validation

**Project Type**: Security-focused CLI product plus release/distribution
pipeline

**Performance Goals**: Add no steady-state runtime latency; keep public
evidence bounded to 64 MiB uncompressed with existing per-artifact limits;
retry public downloads before classifying channel verification failed

**Constraints**: Build once and publish the same archive bytes; clean source
and full 40-hex commit; no Go/source/developer `PATH` in clean install; no
release credential in public output; no native/local substitute for real
gates; GitHub-hosted arm64 runners cannot run nested Lima; `tar.gz` cannot be
submitted to or stapled by Apple's notarization service; public release assets
become immutable after publication

**Scale/Scope**: One `v0.1.0-alpha.1` macOS arm64 package, one separately
retained runtime family/revision, four allowlisted public assets, seven 033
proof IDs, six user journeys, and one supervised operator approval

## Constitution Check

*GATE: Passed before research and re-checked after Phase 1 design.*

- **Privacy Boundary**: Pre-research PASS because only release credentials and
  public artifacts are newly handled; unsupported identity, evidence, platform,
  or credentials fail closed. Post-design PASS because credentials stay in
  protected environments, public models reject control-plane material, and no
  product capability changes.
- **Typed Authority**: Pre-research PASS because existing Go package, evidence,
  and readiness validators remain authoritative. Post-design PASS because
  `internal/releasechannel` owns release, bundle, signing-observation, and
  receipt validation; scripts and workflows only orchestrate typed results.
- **Workspace And Policy**: Pre-research PASS because no workspace, HostFS,
  network, endpoint, profile, or proxy authority changes. Post-design PASS
  because clean-install and real-gate lanes exercise existing policy without
  modifying grants or fallback behavior.
- **Generality And Provider Scope**: Pre-research PASS because macOS signing and
  GitHub publication are scoped distribution providers. Post-design PASS
  because platform observations remain isolated, while the runtime and
  `tun2socks` remain separate prerequisites.
- **Evidence And Redaction**: Pre-research PASS because package digest, real
  gates, product proofs, signing, and public download are independently
  observed. Post-design PASS because the bounded bundle, canonical readiness,
  publication receipt, containment, and existing export boundary cover every
  outward evidence path.
- **Backend And Distribution**: Pre-research PASS because Lima is the release
  isolation backend and native remains a weak harness. Post-design PASS because
  clean install uses typed operations and hosted CI cannot substitute for real
  Lima evidence.
- **Gates**: Pre-research PASS because Gate 0, real Gate 2/3, package,
  clean-install, signing, and public-channel gates are required. Post-design
  PASS because quickstart maps every requirement to a proof layer and
  `not-run` never passes.
- **Status And Docs**: Pre-research PASS because all public status surfaces are
  in scope. Post-design PASS because candidate-local and post-public truth are
  separate, and one published inventory feeds human and machine surfaces.

## Project Structure

### Documentation (this feature)

```text
specs/033-public-alpha-release-channel/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── evidence-and-readiness.md
│   ├── operator-install-support.md
│   ├── package-identity.md
│   ├── public-release-artifacts.md
│   └── release-workflow.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/
├── app/app.go
├── packagekit/
├── productevidence/
├── recovery/
├── releasecompat/
└── releasechannel/

schemas/
├── package-manifest.schema.json
├── product-hardening-evidence.schema.json
├── publication-receipt.schema.json
├── public-evidence-bundle.schema.json
├── public-release.schema.json
├── published-release-inventory.schema.json
└── release-readiness.schema.json

scripts/
├── package-local.sh                   # stage-tree and finalize construction
├── test-public-alpha-candidate.sh     # exact draft package real-gate evidence
├── test-public-alpha-clean-install.sh
├── test-public-alpha-release.sh       # contract and no-publish rehearsal
└── test-gate0.sh

.github/
├── ISSUE_TEMPLATE/
├── pull_request_template.md
├── release-promotions/
└── workflows/
    ├── hideout-alpha-candidate.yml
    └── hideout-alpha-promote.yml

releases/current.json                  # post-public authoritative inventory
LICENSE
THIRD_PARTY_NOTICES.md
SECURITY.md
CONTRIBUTING.md
CHANGELOG.md
README.md
README.zh-CN.md
docs/
├── STATUS.md
├── claim-boundaries.md
├── distribution-bootstrap.md
├── first-run-alpha.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
└── support-matrix.md
```

**Structure Decision**: Keep existing ownership boundaries for package,
evidence, readiness, and recovery. Add one focused `internal/releasechannel`
package because public asset-set validation, notarization observations,
evidence-bundle containment, and publication receipts are a new cohesive data
domain and must not be implemented independently in shell. No new product
helper binary is introduced; `hideout support release ...` exposes the typed
validators needed by scripts and operators.

Release packaging is explicitly two-phase: `package-local.sh` first stages the
tree, then the signing lane finalizes the package manifest and archive over the
unchanged signed tree. Post-public truth is proposed through a receipt-bound
generated pull request rather than an unreviewed direct branch write.

## Complexity Tracking

No constitutional violations. The new release-channel package removes rather
than duplicates authority from workflow scripts; all runtime capabilities and
security boundaries remain unchanged.
