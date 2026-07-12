# Research: Test And Evidence Spine

<!-- markdownlint-disable MD013 -->

## Decision 1: Keep Product Evidence Manifest Schema Stable

**Decision**: Do not add `stale` to `ProofEntry.status`; retain the existing
manifest status set: `passed`, `failed`, and `not-run`.

**Rationale**: `internal/productevidence/manifest.go` validates proof status
against the current finite set and existing 021-025 scripts already produce
schema-valid manifests. `stale` is contextual: a proof can be stale for one
target and current for another, depending on commit/package freshness policy.

**Alternatives considered**:

- Add `stale` as a new status. Rejected because it pollutes source evidence
  with target-specific evaluation state and breaks the stated 026 boundary.
- Add a new manifest version. Rejected because existing manifest data is
  sufficient for v1 evaluation.

## Decision 2: Add A Go-Owned Proof Requirement Registry

**Decision**: Introduce a registry in `internal/productevidence` that records
`featureId`, `proofId`, `layer`, `requiredFor`, `freshnessPolicy`, `claimIds`,
and `artifactPolicy`.

**Rationale**: Current required proof IDs live in separate slices such as
`Required021ProofIDs` and `Required025ProofIDs` in
`internal/productevidence/aggregate.go`. Missing-proof diagnostics can only
name proof IDs from the caller's list; shell/docs consumers also risk copying
those lists. A registry gives missing proofs a feature owner and allows future
features to register proof metadata once.

**Alternatives considered**:

- Keep required proof slices and add better error messages. Rejected because it
  leaves shell/docs/release consumers with a second truth source.
- Generate registry from manifests. Rejected because a missing proof has no
  manifest entry and must still report feature metadata.

## Decision 3: Expose Deterministic Registry JSON

**Decision**: Provide a deterministic JSON view derived from the Go registry so
shell gates, docs truth, and release-readiness checks can consume the same proof
truth.

**Rationale**: 025 introduced docs truth, but shell scripts can drift if they
hard-code proof IDs. A JSON view keeps Go as the owner while allowing shell
checks to stay simple.

**Alternatives considered**:

- Shell imports constants by grepping Go. Rejected as fragile source parsing.
- Keep shell lists and test them against Go. Rejected because it preserves the
  second list 026 is meant to remove.

## Decision 4: Add Artifact Existence And Digest Evaluation

**Decision**: Keep `ArtifactRef.Validate` as schema/shape validation, and add
targeted evaluator checks for artifact existence and digest mismatch according
to registry artifact policy.

**Rationale**: Existing `ArtifactRef.Validate` checks relative paths and SHA
format, not whether artifacts exist or match hashes. That is correct for schema
validation, but insufficient for false-green evidence evaluation.

**Alternatives considered**:

- Make manifest validation always stat artifacts. Rejected because manifests
  may be validated away from their artifact directory and not every proof
  requires artifacts.
- Ignore artifacts in 026. Rejected because missing artifacts are a known
  false-green class.

## Decision 5: Release Readiness Consumes Product-Hardening As Supporting Evidence

**Decision**: Integrate 026 evaluator output into release-readiness reporting as
supporting local product-hardening context, while keeping real Gate 2/Gate 3
checks mandatory for release-candidate isolation claims.

**Rationale**: `internal/releasecompat/readiness.go` currently gates release
candidate readiness on local checks plus Gate 2/Gate 3 evidence. 026 should
improve diagnostics for product-hardening evidence without allowing local proof
to replace real gate evidence.

**Alternatives considered**:

- Make product-hardening proof sufficient for release readiness. Rejected by
  claim-boundary and constitution rules.
- Leave release readiness untouched. Rejected because stale local evidence would
  remain invisible to release reviewers.
