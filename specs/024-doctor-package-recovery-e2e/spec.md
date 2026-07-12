# Feature Specification: Doctor And Package Recovery E2E

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `024-doctor-package-recovery-e2e`

**Created**: 2026-07-09

**Status**: Draft

**Input**: User description: "Keep hardening existing product usability: prove
doctor and package recovery diagnose realistic broken states, repair only safe
states, and produce honest product-hardening evidence without adding new
authority."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Recover Stale Package State (Priority: P1)

An operator upgrades or verifies an installed package and needs Hideout to
detect obsolete package-owned files, show the exact repair command, preview the
cleanup, apply it only when explicitly requested, and verify clean afterward
without deleting durable store state or unrelated files.

**Why this priority**: Package repair is the first support path when a local
alpha install drifts. If it silently deletes the wrong file or cannot prove
repair ordering, the external alpha path is not trustworthy.

**Independent Test**: Run the recovery E2E in local-fast mode. It must create a
stale package-owned leftover through the existing package smoke path, observe
verify failure and repair guidance, run dry-run, run apply, rerun verify, and
write product-hardening evidence.

**Acceptance Scenarios**:

1. **Given** an installed package with one obsolete package-owned file, **When**
   package verify runs, **Then** it fails closed or warns with the exact
   obsolete file and repair hint.
2. **Given** the same stale package state, **When** package repair runs in
   dry-run mode, **Then** it reports the planned removal while preserving the
   stale file and durable store state.
3. **Given** the operator explicitly runs package repair apply, **When** verify
   is rerun, **Then** the obsolete package-owned file is gone, durable store
   evidence remains, unrelated files remain, and verify passes.

---

### User Story 2 - Recover Safe Doctor State (Priority: P2)

An operator with an incomplete or invalid local Hideout state needs doctor to
identify safe repairs, preview them, apply only typed safe repairs, and report
clean or improved state afterward.

**Why this priority**: `doctor --fix` is the operator's rescue path. It must
prove it is planned and typed, not arbitrary shell or silent mutation.

**Independent Test**: The recovery E2E creates an empty or incomplete store,
runs `doctor --level deep`, runs `doctor --fix --dry-run`, runs
`doctor --fix --apply`, and verifies the repaired state with schema-valid
doctor evidence.

**Acceptance Scenarios**:

1. **Given** a store missing safe InitTask material, **When** doctor deep runs,
   **Then** it reports observed facts, candidate causes, and typed next actions
   rather than claiming release readiness.
2. **Given** safe repair is available, **When** `doctor --fix --dry-run` runs,
   **Then** no profile or install state is created.
3. **Given** the operator runs `doctor --fix --apply`, **When** doctor reruns,
   **Then** the safe repair artifacts exist and no unsafe repair is reported as
   applied.

---

### User Story 3 - Preserve Guidance And Redaction Boundaries (Priority: P3)

An operator needs diagnostic output to distinguish fixable local states from
states that require external prerequisites or real gates, while public
evidence remains free of control-plane material.

**Why this priority**: Doctor and package recovery can easily overclaim by
treating warnings as fixes or leaking diagnostic secrets into support bundles.

**Independent Test**: The recovery E2E injects control-plane-looking values,
runs doctor/package outputs in human and JSON forms, exports the selected
doctor report, and scans public artifacts for forbidden material.

**Acceptance Scenarios**:

1. **Given** missing Lima, missing `tun2socks`, DNS gate-required, HostFS
   gate-required, or privilege degraded conditions, **When** doctor reports
   them, **Then** it provides guidance and gate-required markers without
   claiming it fixed or proved those states.
2. **Given** human and JSON doctor output for tested findings, **When** they are
   compared, **Then** they describe the same status and next actions.
3. **Given** public evidence and exported doctor report artifacts, **When** the
   redaction scanner runs, **Then** no broker/UI tokens, `HIDEOUT_SECRET_*`
   values, generated machine-id material, provider refs, or raw proxy URLs are
   present.

### Edge Cases

- Package repair must not delete files that were not package-owned.
- Package repair must not purge durable store state unless an explicit purge
  command is tested elsewhere; this E2E is repair, not uninstall.
- Doctor guidance for real Gate 2/Gate 3 prerequisites must remain guidance and
  must not satisfy release-readiness or isolation proof.
- Missing local prerequisites for the E2E must produce failed or not-run
  product-hardening evidence; skipped optional paths cannot be reported as
  passed.
- Source-tree fallback may help developers, but packaged recovery proof must be
  clearly labeled and cannot be replaced by `go run`.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Existing package verify/repair/uninstall logic,
  doctor report/fix logic, doctor export path, and product-hardening evidence.
  No new repair type, package authority, HostFS authority, script authority, or
  backend authority is added.
- **Fail-closed behavior**: Unknown package ownership, missing manifests,
  checksum mismatch, incompatible migration range, unsafe doctor repair,
  schema failure, redaction failure, or missing required evidence must fail or
  record not-run before a pass claim.
- **User authority and policy**: Mutation remains explicit operator action:
  package repair apply and doctor fix apply. Dry-runs must not mutate state.
- **Generality and provider scope**: This is recovery evidence over existing
  local product paths. It does not install dependencies, provision Lima, repair
  networks, or change guest state.
- **Evidence surface**: Product-hardening manifest, package verify/repair
  logs, doctor human/JSON reports, exported doctor report, and redaction scan
  reports.
- **Secret/redaction boundary**: Public artifacts must not expose control-plane
  values, raw proxy secrets, generated machine-id material, provider refs, or
  implementation-private tokens.
- **Backend/gate expectation**: Doctor can identify gate-required states, but
  local recovery E2E does not replace Gate 2/Gate 3 or release readiness.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST provide a recovery E2E runner that writes
  `hideout.product-hardening-evidence/v1` with stable 024 proof ids.
- **FR-002**: The runner MUST prove package verify detects obsolete
  package-owned leftovers and reports a repair hint.
- **FR-003**: The runner MUST prove package repair dry-run lists the planned
  removal without deleting the obsolete file or durable store state.
- **FR-004**: The runner MUST prove package repair apply removes only
  package-owned obsolete files and subsequent verify passes.
- **FR-005**: The runner MUST prove unrelated files and durable store evidence
  survive the package repair loop.
- **FR-006**: The runner MUST prove doctor deep emits observed facts, candidate
  causes, next actions, and gate-required markers for tested diagnostics.
- **FR-007**: The runner MUST prove doctor fix dry-run does not mutate profile
  or install state.
- **FR-008**: The runner MUST prove doctor fix apply performs only typed safe
  repairs and leaves unsafe states as guidance.
- **FR-009**: The runner MUST prove tested doctor human and JSON outputs are
  equivalent in status and next actions.
- **FR-010**: The runner MUST export a selected doctor report through the 005
  export path and validate the export schema.
- **FR-011**: The runner MUST scan public recovery artifacts for control-plane
  material and fail on leakage.
- **FR-012**: The runner MUST distinguish local recovery evidence from release
  readiness and real Gate 2/Gate 3 proof.
- **FR-013**: The runner MUST reuse or upgrade existing package and doctor smoke
  paths rather than creating a second implementation of package repair or
  doctor logic.

### Key Entities *(include if feature involves data)*

- **Recovery Evidence Manifest**: Product-hardening evidence file containing
  stable 024 proof entries, covered claims, artifact refs, prerequisite
  statuses, and redaction status.
- **Package Repair Loop**: Observed sequence of stale package state, verify
  failure, repair dry-run, repair apply, and verify pass.
- **Doctor Repair Loop**: Observed sequence of deep report, safe repair dry-run,
  safe repair apply, rerun, and export.
- **Guidance Finding**: Doctor finding that gives observed facts and next
  actions but explicitly remains unfixed and gate-required.
- **Recovery Redaction Scan**: Public artifact scan that rejects control-plane
  material while preserving useful local troubleshooting facts.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Local-fast recovery E2E produces schema-valid product-hardening
  evidence with passed package repair, doctor repair, guidance, and redaction
  proof ids.
- **SC-002**: Package repair proof demonstrates verify failure, dry-run
  non-mutation, apply mutation, verify pass, durable-store preservation, and
  unrelated-file preservation in one run.
- **SC-003**: Doctor repair proof demonstrates deep diagnostics, dry-run
  non-mutation, apply mutation limited to typed safe repairs, rerun success or
  improved state, and no release-readiness overclaim.
- **SC-004**: Guidance proof includes at least one non-auto-fix finding with a
  gate-required marker and no applied-fix claim.
- **SC-005**: Human/JSON/exported doctor artifacts and product-hardening
  evidence pass redaction scanning with zero forbidden control-plane values.
- **SC-006**: Gate 0 includes the recovery E2E local-fast lane or an explicit
  invocation of the same proof runner.

## Assumptions

- Existing `scripts/test-package-smoke.sh` and `scripts/test-doctor-smoke.sh`
  remain the primary smoke paths; 024 may call them or add evidence-producing
  modes, but must not fork their semantics.
- The E2E may use a packaged local artifact for package recovery and source
  fallback only for developer diagnostics; proof labels must state which path
  was used.
- Real Lima, Gate 2, Gate 3, dependency installation, and unsafe repair remain
  outside this feature.
