# Feature Specification: Release Hardening And Compatibility Matrix

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `016-release-hardening-compatibility-matrix`

**Created**: 2026-07-09

**Status**: Draft

**Input**: User description: "Follow .tmp/011-016-plan.md using speckit-* skills; complete and commit one feature at a time. 016 freezes the early alpha release contract: supported platforms, backend modes, feature status, gates, migrations, docs, and known non-claims."

## Clarifications

### Session 2026-07-09

- Q: Is a local-fast release check allowed to claim release readiness when real Lima gates are unavailable? → A: No. Local-fast mode is useful evidence, but it must be labeled non-release and cannot substitute for release-candidate Gate 2/Gate 3 evidence.
- Q: What owns the compatibility/support truth source? → A: Go Core owns the machine-readable support matrix and readiness artifact generation; docs and scripts consume or verify that truth source rather than defining a parallel support table.
- Q: Should 016 create a new release system or compose existing gates? → A: Compose the existing Gate 0, package smoke, doctor smoke, release dogfood, and real Gate 2/Gate 3 paths; add drift guards and readiness artifacts around them.
- Q: How should unknown future schema/ABI fixtures behave? → A: Fail closed before mutation/enablement with recreate or migration guidance, not best-effort loading.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Inspect Supported Alpha Matrix (Priority: P1)

A technically inclined early-alpha operator can inspect one authoritative support matrix before installing or debugging Hideout. The matrix tells them which host platforms, backend modes, helper targets, feature areas, and isolation claims are supported, degraded, unsupported, or gate-required.

**Why this priority**: 011-015 added enough product surface that stale docs or vague support claims now create real install and privacy-risk confusion. The first external alpha needs a single truth source before any release automation matters.

**Independent Test**: Validate the matrix file/schema and compare README, STATUS, version/status output, and doctor references against it; unsupported or degraded entries must be visible without running Lima.

**Acceptance Scenarios**:

1. **Given** a supported macOS arm64 host, **When** the operator checks the support matrix, **Then** macOS arm64 is first-class alpha, Lima is the isolation backend, and native is explicitly degraded/development-only.
2. **Given** a Linux amd64 or arm64 host, **When** the operator checks the support matrix, **Then** Linux is supported with narrower smoke coverage and no macOS-only claims.
3. **Given** an unsupported platform/backend pair, **When** the matrix is consumed by docs, status, or doctor, **Then** the result is an explicit unsupported or degraded warning rather than an isolation claim.

---

### User Story 2 - Run Release Gate With Real Evidence (Priority: P2)

A maintainer preparing an alpha candidate can run one release checklist that records build, tests, Gate 0, package smoke, doctor smoke, markdown/schema checks, and real Gate 2/Gate 3 evidence when the local machine supports them.

**Why this priority**: Release readiness cannot be inferred from fast local tests. 004 taught that local checks must not replace real Lima/DNS/HostFS gates when the claim depends on those gates.

**Independent Test**: Run the release checklist in local-fast and real-gate-required modes against fixtures. Verify local-fast mode is labeled non-release evidence, and real-gate-required mode refuses to mark release-ready when required Gate 2/Gate 3 evidence is missing.

**Acceptance Scenarios**:

1. **Given** local-fast mode, **When** the release checklist runs, **Then** it may run build/vet/gofmt/diff-check, tests, Gate 0, package smoke, doctor smoke, markdown, and schema checks, but it labels the result as non-release evidence.
2. **Given** release-candidate mode on a host that supports Lima, **When** Gate 2 or Gate 3 evidence is absent, **Then** the checklist fails closed and records the missing gate.
3. **Given** real Gate 2 and Gate 3 evidence is present, **When** the checklist completes, **Then** it writes a machine-readable release readiness artifact with commit, platform, matrix version, gate results, and non-claims.

---

### User Story 3 - Verify Compatibility And Migration Contract (Priority: P3)

A maintainer changing schemas, package manifests, adapter ABIs, profile records, or report formats can verify that old supported fixtures either migrate or fail closed with explicit guidance.

**Why this priority**: External alpha users will carry profiles, audit, evidence, adapter locks, and package install state across builds. We do not need broad public backward compatibility yet, but the alpha contract must say what survives and how failures are surfaced.

**Independent Test**: Run compatibility fixtures for profile schema, package manifest, adapter-pack lock/ABI, doctor report schema, export artifact schema, decision/notice records, HostFS overlay decisions, and onboarding evidence. Verify accepted fixtures load, unsupported fixtures fail closed, and docs describe the outcome.

**Acceptance Scenarios**:

1. **Given** an older supported profile fixture, **When** compatibility validation runs, **Then** it either loads/migrates or emits explicit recreate/repair guidance without silent mutation.
2. **Given** an unsupported adapter-pack ABI or package manifest fixture, **When** compatibility validation runs, **Then** it fails closed before enable/apply.
3. **Given** a schema/report/export fixture that contains control-plane material, **When** compatibility validation exports or reports it, **Then** deterministic redaction still holds.

---

### User Story 4 - Remove Stale Shipped/Not-Yet Documentation (Priority: P4)

A maintainer or external tester can read README, STATUS, threat model, test plan, and specs without finding stale "not implemented" language for features shipped in 008-016 or overclaims for deferred areas.

**Why this priority**: Previous slices repeatedly found contract drift after implementation. 016 is the cleanup/freeze pass that makes the alpha story credible.

**Independent Test**: Run a stale-claim scan over README, docs, and specs for shipped 008-016 terms, unsupported backend/platform terms, and known non-claims; verify every hit is either current implementation status or an explicit deferred/non-claim statement.

**Acceptance Scenarios**:

1. **Given** a shipped 008-016 feature, **When** docs are scanned, **Then** no stale "not-yet" wording remains outside historical/spec trail or explicit deferred subfeatures.
2. **Given** guest-root, workspace writes, marketplace trust, browser security, DLP, or native backend isolation, **When** docs are scanned, **Then** they remain explicit non-claims unless a real gate closes them.
3. **Given** README examples, **When** they mention install, init, doctor, export, decisions, templates, adapters, or HostFS write, **Then** they use current packaged commands and explicit flags.

### Edge Cases

- A host supports Go tests and Gate 0 but not Lima; release-candidate mode must fail closed for missing real Gate 2/Gate 3 evidence while local-fast mode may pass as non-release evidence.
- A native-backend workflow passes all local checks; matrix consumers must still label native as degraded/development-only.
- Linux support is narrower than macOS support; matrix output must not imply equal gate coverage.
- A future schema appears in a fixture; compatibility validation must refuse unknown major versions before mutation.
- Documentation may contain historical spec trail; scans must distinguish historical/superseded text from current product claims.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Release lifecycle, compatibility reporting, docs/status, doctor/status/version surfaces, package/release evidence, schema fixtures, and release gate scripts. No new HostFS, network, backend, or host-open authority is granted.
- **Fail-closed behavior**: Unsupported platform/backend/matrix entries, missing required real gate evidence, unknown schema/ABI versions, stale status drift, or failed redaction checks must refuse release-ready status before publishing or claiming support.
- **User authority and policy**: Operators can inspect matrix status and run local-fast validation, but release-candidate status requires explicit real-gate evidence. Matrix reporting must not broaden profile, HostFS, adapter, network, or backend authority.
- **Generality and provider scope**: The matrix describes generic Hideout support posture, not a specific agent, package manager, browser, or marketplace provider. macOS/Linux support levels and backend labels are product support claims, not provider recipes.
- **Evidence surface**: Compatibility matrix file, release readiness artifact, `hideout version`/status/doctor report references, README, STATUS, threat model, test plan, Gate 0, package smoke, doctor smoke, and real Gate 2/Gate 3 records when release-candidate mode is used.
- **Secret/redaction boundary**: Release readiness artifacts and matrix-derived output must not include raw proxy URLs, broker/UI tokens, `HIDEOUT_SECRET_*` backing values, generated machine ids, hidden helper credential paths, or local claim tokens.
- **Backend/gate expectation**: Gate 0 is mandatory for matrix/docs/schema drift. Real Gate 2 is required for HostFS data-plane release evidence; real Gate 3 is required for DNS/network/privilege release evidence when a release candidate claims those areas.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide a machine-readable support matrix that covers host OS/architecture, backend modes, helper binary targets, package/install status, profile schema status, adapter-pack ABI, HostFS write overlay, DNS mediation, privilege separation, doctor/report schemas, export/share schemas, and known non-claims.
- **FR-002**: Matrix entries MUST classify support as one of `first-class`, `supported`, `degraded`, `unsupported`, or `gate-required`, and each non-first-class entry MUST include a concise reason and operator guidance.
- **FR-003**: Matrix MUST identify macOS arm64 as the first-class alpha host target and Linux amd64/aarch64 as supported with narrower smoke coverage.
- **FR-004**: Matrix MUST identify the native backend as degraded/development-only and MUST NOT treat native checks as isolation evidence.
- **FR-005**: `hideout version` or an equivalent local status surface MUST expose the matrix version/path or summarized platform support without leaking local paths that are not needed for operator action.
- **FR-006**: Doctor diagnostics MUST reference matrix support status for the current host/backend and preserve warning/degraded exit semantics.
- **FR-007**: Release gate MUST support local-fast mode and release-candidate mode, with local-fast mode explicitly labeled as non-release evidence.
- **FR-008**: Release-candidate mode MUST require real Gate 2 and Gate 3 evidence when the current host supports those gates, and MUST fail closed when required evidence is missing.
- **FR-009**: Release gate MUST write a machine-readable readiness artifact containing schema version, commit, platform, matrix version, command summary, gate results, package/doctor smoke results, docs/schema checks, status, and non-claims.
- **FR-010**: Release readiness artifacts MUST pass deterministic control-plane redaction.
- **FR-011**: Compatibility validation MUST cover current supported fixture versions for profile schema, package manifest, adapter-pack manifest/registry, doctor report, export artifact, decision/notice record, HostFS write decision/event, onboarding evidence, daemon status/event, live console seed, run result, init plan, and init audit.
- **FR-012**: Unknown or unsupported fixture versions MUST fail closed with clear migration/recreate guidance before mutation or enablement.
- **FR-013**: README, STATUS, threat model, architecture principles, privacy run design, and test plan MUST describe shipped 008-016 features consistently with the matrix.
- **FR-014**: Documentation scans MUST preserve explicit non-claims for guest-root containment, workspace write blocking/DLP, public marketplace trust, browser security, native backend isolation, and unsupported platforms.
- **FR-015**: Gate 0 MUST include matrix schema validation and stale-claim/matrix drift checks.
- **FR-016**: Release gate output MUST be export/share safe and eligible for inclusion in release dogfood evidence without dangling local-only references.

### Key Entities *(include if feature involves data)*

- **Support Matrix**: Versioned product support table for platforms, backends, feature areas, helper targets, schemas, ABIs, gates, and known non-claims.
- **Support Entry**: One matrix row with area, subject, support level, reason, guidance, required gates, and evidence source.
- **Release Readiness Run**: One execution of the release gate with mode, platform, commit, matrix version, command results, gate evidence, non-claims, and final status.
- **Compatibility Fixture**: Versioned sample data used to prove supported records load/migrate and unsupported records fail closed.
- **Stale Claim Finding**: Documentation or status mismatch found by drift scans, including current text, expected matrix state, and severity.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Matrix schema validation covers 100% of support entries and rejects entries missing support level, reason, guidance, or required-gate fields.
- **SC-002**: Gate 0 fails if the matrix file is missing, invalid, or out of sync with documented first-class/supported/degraded platform and backend claims.
- **SC-003**: Release-candidate mode fails 100% of runs missing required Gate 2/Gate 3 evidence on hosts where those gates are available.
- **SC-004**: Local-fast mode completes without real Lima gates but marks 100% of readiness artifacts as non-release evidence.
- **SC-005**: Compatibility fixture tests cover every schema/ABI family named in FR-011 with at least one accepted fixture and one rejected unknown-version fixture.
- **SC-006**: Stale-claim scan reports 0 current-doc overclaims for shipped 008-016 features and 0 missing required non-claims.
- **SC-007**: Release readiness artifacts contain 0 raw control-plane secret matches in automated redaction scans.
- **SC-008**: README, STATUS, doctor/status output, and the matrix agree on macOS arm64, Linux amd64/aarch64, and native backend status in automated checks.

## Assumptions

- 016 is an alpha release-hardening feature, not a public SLA or long-term compatibility promise.
- The first-class alpha platform is macOS arm64; Linux amd64/aarch64 are supported with narrower smoke coverage.
- Real Gate 2/Gate 3 execution may be unavailable on some local machines; such machines can produce local-fast evidence but not release-candidate readiness.
- Existing Gate 0, package smoke, doctor smoke, release dogfood, and real Lima gate scripts remain the authoritative test commands and should be composed rather than reimplemented.
- Windows, enterprise support matrix, public signing/notarization, and public marketplace trust remain out of scope for 016.
