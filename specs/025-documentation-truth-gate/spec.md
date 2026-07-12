# Feature Specification: Documentation Truth Gate

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `025-documentation-truth-gate`

**Created**: 2026-07-09

**Status**: Draft

**Input**: User description: "Prevent README/docs/specs/status from drifting
ahead of implementation. Map current product claims to proof ids or gate
references, check command examples, and fail on known overclaim patterns."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Map Claims To Proof (Priority: P1)

An operator or maintainer needs to know which current product claims are backed
by which proof, gate, or non-claim document. Documentation should not say a
capability is real unless there is a named evidence source.

**Why this priority**: The project has repeatedly found green tests hiding
overclaims. A claim registry makes proof references visible and reviewable.

**Independent Test**: Run the documentation truth smoke and verify it validates
the claim-boundary registry against current docs and product-hardening proof
ids.

**Acceptance Scenarios**:

1. **Given** a current product claim in README, STATUS, or core docs, **When**
   the truth gate runs, **Then** it finds a registry entry that points to
   STATUS, support matrix, product-hardening proof ids, or real gate evidence.
2. **Given** a local-fast proof claim, **When** the truth gate validates it,
   **Then** it remains labeled local-fast and cannot become release readiness.

---

### User Story 2 - Catch Known Overclaims (Priority: P2)

A maintainer needs automated checks for the exact classes of documentation
drift that have caused previous review failures.

**Why this priority**: Known false claims are cheaper to prevent than to
rediscover by manual review.

**Independent Test**: Seed or scan current docs with banned overclaim patterns
and verify the truth smoke fails with file/line diagnostics; current docs pass.

**Acceptance Scenarios**:

1. **Given** docs claim native backend is isolation, **When** the scanner runs,
   **Then** it fails and reports the line.
2. **Given** docs claim doctor/local-fast/Gate 0 replaces real Gate 2/Gate 3 or
   release readiness, **When** the scanner runs, **Then** it fails.
3. **Given** docs describe reducer tests as browser E2E or HostFS overlay as
   the only host mutation path, **When** the scanner runs, **Then** it fails
   unless the line is explicitly a non-claim.

---

### User Story 3 - Keep Commands And Localized Entrypoints Honest (Priority: P3)

A new user following README or first-run docs needs current commands to be
recognized and localized docs to be either current or explicitly non-canonical.

**Why this priority**: Stale commands and stale translations break usability
even when implementation is correct.

**Independent Test**: Run the command fixture check and cross-doc consistency
checks. They must prove curated examples are recognized or intentionally
non-executed, and that localized docs declare their canonical source.

**Acceptance Scenarios**:

1. **Given** a curated user-facing command example, **When** the truth gate
   checks it, **Then** the command is recognized under a safe temp store or is
   explicitly classified as intentionally not executed.
2. **Given** `README.zh-CN.md` is not kept in strict parity, **When** the truth
   gate runs, **Then** it requires a visible statement that `README.md` is
   canonical.
3. **Given** `.tmp` discussion drafts exist, **When** the truth gate scans
   docs, **Then** it excludes those drafts from current product truth checks.

### Edge Cases

- Historical spec directories may contain decision trails. The truth gate
  should scan current/latest specs strictly and old specs only for banned
  current-product overclaims.
- Commands that mutate state must run only under a temporary store or be
  classified parse-only/not-executed.
- Markdown extraction is deferred; curated command fixtures are authoritative
  for v1 to avoid false positives.
- The truth gate must not rewrite docs automatically.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Documentation, docs smoke, product-hardening evidence,
  and command parsing checks. No product authority or runtime permission is
  added.
- **Fail-closed behavior**: Missing proof mapping, banned overclaim, stale
  canonical command, stale localized canonicality, schema failure, or redaction
  failure must fail the gate.
- **User authority and policy**: Safe command checks use temp stores and do not
  mutate operator state.
- **Evidence surface**: Claim-boundary doc, command fixture report, overclaim
  scan report, cross-doc consistency report, and product-hardening evidence.
- **Backend/gate expectation**: The truth gate is local docs evidence. It does
  not satisfy real Gate 2/Gate 3 or release-candidate proof.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST define a current claim-boundary registry mapping
  product claims to proof ids, gate references, STATUS/support docs, or explicit
  non-claims.
- **FR-002**: The truth gate MUST scan README, localized README, docs, and
  current feature specs for known overclaim patterns.
- **FR-003**: The truth gate MUST exclude `.tmp` discussion drafts from current
  product truth checks.
- **FR-004**: The truth gate MUST validate that 021-024 product-hardening proof
  ids are represented in the claim-boundary registry.
- **FR-005**: The truth gate MUST validate curated command examples as
  `execute-temp-store`, `parse-only`, `real-gate`, or
  `intentionally-not-executed`.
- **FR-006**: Commands that execute MUST use a temporary store or be blocked by
  the fixture classification.
- **FR-007**: The truth gate MUST require `README.md` to link the first-run
  alpha document and support matrix.
- **FR-008**: The truth gate MUST require `README.zh-CN.md` to either pass
  strict parity checks or visibly declare `README.md` canonical.
- **FR-009**: The truth gate MUST validate privacy-run-test-plan and Gate 0
  script agree on 021-024 product-hardening scripts.
- **FR-010**: The truth gate MUST write schema-valid
  `hideout.product-hardening-evidence/v1` with stable 025 proof ids.
- **FR-011**: The truth gate MUST report exact file/line diagnostics for
  overclaim or stale-doc failures.

### Key Entities *(include if feature involves data)*

- **Claim Boundary**: Human-readable row mapping a product claim to owner doc,
  proof ids, gates, and non-claim boundaries.
- **Command Example Fixture**: Curated JSON list of user-facing commands,
  safety classification, expected recognition method, and rationale.
- **Overclaim Finding**: File/line/pattern classification for a current-product
  statement that exceeds implemented evidence.
- **Docs Truth Evidence Manifest**: Product-hardening evidence aggregating
  claim mapping, overclaim scan, command checks, and cross-doc checks.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The truth smoke passes on current docs and writes four passed 025
  product-hardening proofs.
- **SC-002**: Removing any required 021-024 proof id from the claim registry
  causes the truth smoke to fail.
- **SC-003**: Adding a banned overclaim phrase to a scanned current doc causes
  the truth smoke to fail with file and line.
- **SC-004**: Curated commands are either recognized under temp-store/parse-only
  checks or explicitly documented as non-executed/real-gate.
- **SC-005**: Gate 0 includes the docs truth smoke.

## Assumptions

- V1 uses a curated command fixture rather than extracting every Markdown code
  fence.
- `.tmp` remains planning material and is not current product truth.
- English `README.md` is canonical unless localized docs are maintained in
  strict parity.
