# Feature Specification: Error Codes And Recovery Hints

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `028-error-recovery-hints`

**Created**: 2026-07-09

**Status**: Draft

**Input**: User description: "Implement the 028 portion of `.tmp/026-028-internal-hardening-plan.md`: give selected high-value fail-closed and degraded host-visible outcomes stable codes, reasons, hints, and next actions without changing authority or promising a full error-model rewrite."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Read A Stable Code In Doctor Output (Priority: P1)

An operator or support reviewer running `hideout doctor` needs a stable code for
selected findings so the output can be searched, documented, and tested without
parsing long prose.

**Why this priority**: Doctor is the primary local diagnostic surface. It
already has structured findings and next actions, so adding stable code fields
there gives the most value with the least blast radius.

**Independent Test**: Run doctor human/JSON tests that create selected findings
with codes and assert the same code, reason, hint, and next actions appear in
both human and JSON output while control-plane material remains redacted.

**Acceptance Scenarios**:

1. **Given** a doctor finding for missing `tun2socks`, **When** human and JSON
   reports are rendered, **Then** both surfaces include the same stable code,
   redacted reason, recovery hint, and next action.
2. **Given** a doctor report schema, **When** a finding includes code metadata,
   **Then** the schema accepts it and still rejects unknown accidental fields.
3. **Given** a finding with no public code, **When** it is rendered, **Then** it
   remains valid and is not forced into an unstable placeholder code.

---

### User Story 2 - Surface Codes On Selected CLI Failures (Priority: P2)

An operator seeing a package, init, or release-readiness failure should get a
short stable code plus a copyable next step for selected high-value cases.

**Why this priority**: Fail-closed is correct, but repeated support cases such
as obsolete package leftovers, missing proxy prerequisites, or missing real
release gate evidence should be recoverable without reading implementation
internals.

**Independent Test**: Run CLI fixture tests for the v1 selected host-observable
cases and assert code/reason/hint parity rather than exact prose.

**Acceptance Scenarios**:

1. **Given** package install or verify detects obsolete package-owned leftovers,
   **When** the CLI prints the failure or warning, **Then** it includes
   `package.obsolete-leftover` and a repair command.
2. **Given** init privacy mode is missing a proxy secret or mediated resolver,
   **When** non-interactive validation fails, **Then** the error includes the
   corresponding stable code and a concrete rerun hint.
3. **Given** release-candidate readiness lacks real Gate 2/Gate 3 evidence,
   **When** readiness is evaluated, **Then** the output includes a release
   evidence code and does not imply local proof is enough.

---

### User Story 3 - Keep Error Code Truth Central And Documented (Priority: P3)

Maintainers need one registry of public error codes so docs truth and tests can
verify referenced codes exist and new codes do not appear accidentally.

**Why this priority**: A code vocabulary is useful only if it remains stable and
central. Otherwise docs and tests will drift the same way route/proof lists did.

**Independent Test**: Run code-registry tests and docs truth smoke verifying
all referenced public codes exist in the Go registry.

**Acceptance Scenarios**:

1. **Given** a public code appears in docs, **When** docs truth runs, **Then**
   the code must exist in the Go-owned registry.
2. **Given** two registry entries share a code, **When** registry validation
   runs, **Then** it fails.
3. **Given** an unselected internal error, **When** tests run, **Then** it is
   allowed to remain uncoded rather than inventing an unstable public code.

### Edge Cases

- Warning/degraded states may carry codes without becoming hard failures.
- Guest bootstrap shell internals are not required to expose public codes in
  v1; the host-visible failure may carry the code when practical.
- Manager/daemon API error migration is not required unless the selected case
  already has a stable host-visible response.
- Copyable next actions must not include raw secrets or hidden control-plane
  paths.
- Codes must be subsystem-namespaced and stable, for example
  `package.obsolete-leftover`.
- Human text can evolve, but tests should assert code/reason/hint shape rather
  than long prose.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Error/report metadata, doctor output, selected CLI
  failure rendering, docs truth. No runtime host, filesystem, network, backend,
  profile, script, approval, or UI authority is added.
- **Fail-closed behavior**: Existing fail-closed decisions remain unchanged.
  Codes and hints explain denial; they do not approve, retry, repair, or bypass.
- **User authority and policy**: No policy change. Operator commands listed in
  `nextActions` are guidance only and still run through existing plan/apply or
  CLI validation when invoked.
- **Generality and provider scope**: Codes are generic Hideout support
  vocabulary. No named package manager, browser, proxy port, backend quirk, or
  agent becomes Core semantics.
- **Evidence surface**: Doctor human/JSON output, selected CLI output,
  release-readiness support output, docs truth code registry validation, and
  Gate 0 smoke.
- **Secret/redaction boundary**: `reason`, `hint`, `nextActions`, and
  `evidenceRefs` must pass existing deterministic control-plane redaction.
- **Backend/gate expectation**: Gate 0 covers the code registry and selected
  host-visible outputs. No real-gate or release-readiness claim is added.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST define a Go-owned registry for public recovery
  codes with code, reason, hint, next actions, severity, and owning subsystem.
- **FR-002**: Codes MUST be subsystem-namespaced, unique, deterministic, and
  testable.
- **FR-003**: Doctor `Finding` JSON MUST support optional `code`, `reason`,
  `hint`, `nextActions`, and `evidenceRefs` fields through the schema.
- **FR-004**: Doctor human output MUST render code, reason, hint, next actions,
  and evidence refs for coded findings.
- **FR-005**: Doctor human and JSON output MUST agree on the code for selected
  findings.
- **FR-006**: Selected package obsolete-leftover output MUST include
  `package.obsolete-leftover` and a repair hint.
- **FR-007**: Selected init privacy prerequisite failures MUST include
  `init.proxy-secret.missing` or `init.mediated-resolver.missing` when the
  failure is host-observable before profile creation.
- **FR-008**: Selected release-readiness missing/stale evidence output MUST
  include stable release evidence codes without treating local proof as real
  Gate 2/Gate 3 proof.
- **FR-009**: Docs truth MUST verify public codes referenced by user-facing docs
  exist in the Go registry.
- **FR-010**: Tests MUST assert codes and structured hints rather than long
  prose for selected cases.
- **FR-011**: Existing fail-closed behavior MUST remain unchanged.
- **FR-012**: Unselected internal errors MAY remain uncoded in v1.

### V1 Public Codes

- `package.obsolete-leftover`
- `package.prerequisite.missing`
- `init.proxy-secret.missing`
- `init.mediated-resolver.missing`
- `privilege.status.degraded`
- `release.gate-evidence.missing`
- `release.evidence.stale`
- `hostfs.reserved-root.denied`
- `decision.claim.expired`

### Key Entities *(include if feature involves data)*

- **Recovery Code**: Stable public identifier with subsystem, reason, hint,
  severity, and next actions.
- **Recovery Record**: Code plus redacted reason/hint/next actions attached to
  a finding or selected CLI output.
- **Coded Doctor Finding**: Doctor finding carrying optional recovery metadata.
- **Code Registry View**: Deterministic view used by tests and docs truth.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of v1 public codes are present exactly once in the registry.
- **SC-002**: 100% of coded doctor fixtures validate against
  `schemas/doctor-report.schema.json`.
- **SC-003**: 100% of coded doctor human/JSON parity tests show the same code.
- **SC-004**: Selected package/init/release fixtures assert stable codes rather
  than long prose.
- **SC-005**: Docs truth rejects a referenced public code missing from the
  registry.
- **SC-006**: Gate 0 includes 028 validation and completes successfully.

## Assumptions

- V1 starts with host-observable surfaces: doctor, package CLI, init CLI, and
  release-readiness support output.
- Manager API and daemon response shape can stay unchanged unless a selected
  code already appears through those surfaces.
- The code registry is additive metadata; it must not change denial/approval
  decisions.
