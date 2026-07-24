# Feature Specification: Test And Evidence Spine

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `026-test-evidence-spine`

**Created**: 2026-07-09

**Status**: Implemented — the exact claim surface and non-claims live in [docs/STATUS.md](../../docs/STATUS.md)

**Input**: User description: "Implement the 026 portion of `.tmp/026-028-internal-hardening-plan.md`: create a practical shared test and evidence spine so product-hardening, real-gate, and release-candidate proof handling is trustworthy without broad test-framework rewrites."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Evaluate Required Proofs From One Registry (Priority: P1)

A maintainer adding or reviewing a feature needs one Go-owned source of truth
for required proof IDs, feature ownership, proof layer, freshness expectations,
claim IDs, and artifact policy. Missing proof must be reported from that
registry, not inferred from a manifest entry that does not exist.

**Why this priority**: Recent features repeatedly hand-wrote required proof ID
lists. That creates drift and makes "missing proof" diagnostics weak.

**Independent Test**: Run product evidence registry/evaluator tests with
fixtures for missing, failed, not-run, and passed proofs. The missing-proof
case must name the feature ID and proof ID even though the manifest lacks that
proof entry.

**Acceptance Scenarios**:

1. **Given** a registry requirement for a proof ID, **When** a manifest omits
   that proof, **Then** evaluation fails and reports the registry feature ID,
   proof ID, layer, and required-for target.
2. **Given** an existing 021-025 manifest with all required proofs, **When**
   the evaluator runs, **Then** it passes without changing the manifest schema.
3. **Given** a proof entry with status `not-run`, **When** the requirement is
   required for targeted completion or release-candidate use, **Then** the
   evaluator reports it as not satisfying that target.

---

### User Story 2 - Detect Stale Or False Evidence Without New Proof Status (Priority: P2)

A release reviewer needs to know whether evidence is absent, failed, not-run,
stale, or backed by missing artifacts. `stale` must be an evaluation result, not
a new `ProofEntry.status` value.

**Why this priority**: Release readiness and product-hardening evidence already
distinguish proof classes, but stale/fake-green cases need a common evaluator.

**Independent Test**: Run evaluator fixture tests for stale-by-commit,
stale-by-package, missing artifact, and digest mismatch while verifying proof
entry status remains only `passed`, `failed`, or `not-run`.

**Acceptance Scenarios**:

1. **Given** a passed proof generated for a different commit than required,
   **When** the freshness policy is `same-commit`, **Then** evaluation reports
   `stale` without mutating the proof entry.
2. **Given** a passed proof that references a missing artifact, **When** the
   registry artifact policy requires existence, **Then** evaluation fails with
   an artifact diagnostic.
3. **Given** a passed proof with a supplied artifact digest that does not match
   the referenced file, **When** evaluation runs, **Then** it fails with a
   digest mismatch diagnostic.

---

### User Story 3 - Let Shell Gates And Docs Truth Consume The Same Registry (Priority: P3)

Shell gates, docs truth, and release-readiness checks need a deterministic view
of the Go-owned proof registry so they do not keep separate hard-coded proof
lists.

**Why this priority**: 025 proved docs can drift from implementation. A registry
that only Go tests can see would still leave shell scripts and docs truth with a
second truth source.

**Independent Test**: Run a registry JSON view check and one existing
product-hardening script compatibility check. Shell/docs truth consumers must
read the Go-owned registry view or Go helper output, not a separate list.

**Acceptance Scenarios**:

1. **Given** the Go registry contains 021-025 required proof IDs, **When** the
   registry JSON view is generated, **Then** it deterministically lists those
   proof IDs with feature ID, layer, required-for target, freshness policy,
   claim IDs, and artifact policy.
2. **Given** docs truth validates product-hardening proof IDs, **When** it
   runs, **Then** it consumes the registry JSON view or a Go helper derived from
   it instead of duplicating the list in shell.
3. **Given** release-readiness reports evidence state, **When** product
   hardening evidence is stale or local-only, **Then** the report preserves the
   distinction between supporting local proof and real Gate 2/Gate 3 evidence.

### Edge Cases

- A manifest may contain an unknown proof ID. The evaluator must report it as
  extra context without treating it as satisfying a registered requirement.
- A proof may be `passed` but redaction status failed. The requirement is not
  satisfied.
- A proof may be valid local product-hardening evidence but insufficient for a
  release-candidate target.
- Artifact refs may be intentionally absent for proof IDs whose artifact policy
  is `none`.
- Registry JSON generation must be deterministic so shell diffs and docs truth
  are stable.
- Historical gate evidence may remain in older formats; 026 does not require
  rewriting those manifests.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Test/evidence validation, docs truth, and release
  readiness reporting. No runtime host, filesystem, network, backend, profile,
  script, or UI authority is added.
- **Fail-closed behavior**: Missing registered proof, failed proof, disallowed
  not-run proof, stale freshness, missing required artifact, digest mismatch,
  redaction failure, or invalid registry JSON must fail the relevant validation
  target before any pass claim.
- **User authority and policy**: No user runtime policy changes. Existing local
  product-hardening proof remains support/regression evidence only.
- **Generality and provider scope**: Generic Hideout evidence infrastructure.
  No provider, agent, package manager, backend quirk, browser, or proxy port is
  promoted as product semantics.
- **Evidence surface**: Go-owned proof registry, deterministic registry JSON
  view, evaluator diagnostics, product-hardening evidence summaries, docs truth
  checks, and release-readiness supporting-evidence reports.
- **Secret/redaction boundary**: Registry/evaluator output must not contain raw
  token values, secret refs, machine IDs, or hidden control-plane paths. Proof
  entries with failed redaction do not satisfy requirements.
- **Backend/gate expectation**: Gate 0 covers local registry/evaluator/schema
  checks. This feature does not add real backend, DNS, HostFS, or release
  readiness claims; it preserves existing Gate 2/Gate 3 requirements.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST define a Go-owned proof requirement registry with
  at least `featureId`, `proofId`, `layer`, `requiredFor`, `freshnessPolicy`,
  `claimIds`, and `artifactPolicy` for registered product-hardening proofs.
- **FR-002**: The registry MUST cover existing 021-025 product-hardening proof
  IDs without requiring their manifests to change.
- **FR-003**: The evaluator MUST derive missing-proof diagnostics from the
  registry so absent proof entries still report feature ID, proof ID, layer,
  required-for target, and claim IDs.
- **FR-004**: `ProofEntry.status` MUST remain limited to existing proof status
  values; `stale` MUST be represented only as evaluator output.
- **FR-005**: The evaluator MUST report passed, failed, missing, not-run,
  stale-by-commit, stale-by-package, redaction failed, missing artifact, and
  digest mismatch outcomes.
- **FR-006**: Artifact policy MUST support `none`, `exists`, and
  `exists-and-digest-if-supplied`.
- **FR-007**: The registry MUST expose a deterministic JSON view generated from
  the Go-owned registry for shell gates, docs truth, and release-readiness
  checks.
- **FR-008**: Shell/docs truth/release-readiness checks MUST consume the
  registry JSON view or Go helpers derived from it; they MUST NOT maintain a
  separate required-proof list for 021-025.
- **FR-009**: Release-readiness reporting MUST keep local product-hardening
  proof separate from real Gate 2/Gate 3 evidence and MUST NOT treat local proof
  as release readiness by itself.
- **FR-010**: Existing 021-025 scripts and manifests MUST remain compatible.
- **FR-011**: Validation output MUST include actionable proof IDs and feature
  IDs so a maintainer can locate the failing requirement without reading all
  manifests.
- **FR-012**: Gate 0 MUST include the 026 registry/evaluator validation without
  materially increasing normal Gate 0 runtime.

### Key Entities *(include if feature involves data)*

- **Proof Requirement**: Registry row describing one expected proof ID, owning
  feature, evidence layer, required-for target, freshness policy, claim IDs,
  and artifact policy.
- **Proof Registry JSON View**: Deterministic JSON representation of the
  Go-owned registry for shell/docs/release consumers.
- **Proof Evaluation Result**: Evaluation output for one requirement, including
  satisfied/unsatisfied state and reason such as missing, failed, not-run,
  stale, redaction failed, missing artifact, or digest mismatch.
- **Evidence Target**: Validation context such as local dogfood, targeted
  completion, or release candidate.
- **Artifact Check**: Result of applying a requirement's artifact policy to
  proof artifact references.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of registered 021-025 required proof IDs appear exactly once
  in the registry JSON view.
- **SC-002**: 100% of missing-proof fixtures report both feature ID and proof
  ID from the registry.
- **SC-003**: 100% of stale-by-commit and stale-by-package fixtures are reported
  as evaluator results while manifest proof status remains unchanged.
- **SC-004**: 100% of missing artifact and digest mismatch fixtures fail
  evaluation when artifact policy requires them.
- **SC-005**: Existing 021-025 product-hardening scripts continue to pass their
  current local-fast validation.
- **SC-006**: Gate 0 includes 026 validation and completes successfully.
- **SC-007**: Docs truth and release-readiness checks no longer need a separate
  hand-written 021-025 required-proof list.

## Assumptions

- The existing `hideout.product-hardening-evidence/v1` manifest remains the
  proof entry format for 021-025.
- 026 may add a registry JSON view and evaluator output, but it should avoid a
  manifest schema change unless implementation proves one is necessary.
- Release-candidate proof may require product-hardening proof freshness as
  supporting context, but real Gate 2/Gate 3 evidence remains mandatory for
  corresponding isolation claims.
