# Feature Specification: Doctor Deep Diagnostics

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `018-doctor-deep-diagnostics`

**Created**: 2026-07-09

**Status**: Implemented — the exact claim surface and non-claims live in [docs/STATUS.md](../../docs/STATUS.md)

**Input**: User description: "Implement .tmp/017-020-internal-hardening-plan.md. 018 makes hideout doctor useful for real troubleshooting: deep and feature-scoped diagnostics report observed facts, candidate causes, and next actions without pretending to determine root cause or replace real gates."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Get Useful Deep Diagnostics (Priority: P1)

An external alpha operator runs `hideout doctor --level deep` and receives more useful local facts than light mode: package state, helper/prerequisite status, adapter pack status, decision status, daemon/local lifecycle status, DNS/HostFS/privilege gate requirements, and concrete next actions.

**Why this priority**: 015 made doctor honest but shallow. External alpha users need local troubleshooting that narrows the problem without requiring them to know which subsystem owns each symptom.

**Independent Test**: Run light and deep doctor on the same controlled store. Deep output must include additional feature findings, observed facts, candidate causes where appropriate, next actions, and gate-required markers; light output must remain local and smaller.

**Acceptance Scenarios**:

1. **Given** a valid local store, **When** the operator runs `doctor --level deep`, **Then** the report includes additional diagnostics beyond light mode and labels each as observed fact, candidate cause, or gate-required proof.
2. **Given** a missing package helper or obsolete package-owned leftover, **When** deep doctor runs, **Then** the packaging finding names the artifact/state and suggests `hideout package verify` or `hideout package repair` without mutating files.
3. **Given** a DNS, HostFS, Lima, or privilege fact that requires real backend evidence, **When** deep doctor runs without the real gate, **Then** the report marks the proof as gate-required and does not claim release evidence.

---

### User Story 2 - Diagnose One Feature Without Noise (Priority: P2)

An operator debugging one area can run `hideout doctor --feature <name>` and receive focused diagnostics for that feature without unrelated deep checks or hidden probes.

**Why this priority**: Focused diagnostics are the practical support path for alpha users. They should expose real local facts for a feature and avoid both false certainty and noisy all-system reports.

**Independent Test**: Run doctor with individual feature selectors for adapters, decisions, packaging, DNS, HostFS, privilege, daemon, export, and cleanup. Each selected feature must produce at least one real local finding or an explicit gate-required marker; unselected feature-only findings must be absent.

**Acceptance Scenarios**:

1. **Given** the operator selects `--feature adapters`, **When** doctor runs, **Then** it reports adapter pack inventory, enabled adapter references, conflict/routing facts where locally available, and adapter-pack test guidance.
2. **Given** the operator selects `--feature decisions`, **When** doctor runs, **Then** it reports pending, claimed, terminal, timeout-risk, stale-claim, and notice counts without claim tokens or provider-private refs.
3. **Given** the operator selects `--feature packaging`, **When** doctor runs, **Then** it reports installed package verification status, obsolete package-owned state from 017, helper mismatch facts, and external prerequisite status.
4. **Given** the operator selects `--feature dns`, `hostfs`, or `privilege`, **When** local proof is insufficient, **Then** the finding identifies the real gate needed for proof and does not fall back to native harness claims.

---

### User Story 3 - Trust Redacted Reports Across Output Modes (Priority: P3)

An operator can print, save, audit, and export a doctor report knowing every surface uses the same deterministic control-plane redaction boundary.

**Why this priority**: Doctor is now a support artifact. If human output, JSON, audit, and export-selected reports diverge, support evidence becomes either unsafe or untrustworthy.

**Independent Test**: Inject representative control-plane-looking values into diagnostic summaries/details/next actions and verify human output, JSON output, saved evidence, local audit, and export-selected doctor report contain zero raw secret matches while preserving non-secret user data.

**Acceptance Scenarios**:

1. **Given** diagnostic facts contain proxy URLs, generated machine ids, broker/UI tokens, claim tokens, `HIDEOUT_SECRET_*` backing values, or hidden runtime credential paths, **When** doctor renders or saves them, **Then** every output surface strips or redacts the control-plane material deterministically.
2. **Given** a doctor JSON report and equivalent human output, **When** they are compared, **Then** they contain the same check ids, statuses, severities, required markers, and next actions.
3. **Given** a doctor report is explicitly selected for export/share, **When** export runs, **Then** the exported artifact includes only the redacted report and does not silently add unrelated doctor reports.

---

### User Story 4 - Receive Safe Recovery Guidance (Priority: P4)

An operator who cannot auto-repair a problem still receives high-confidence next commands, while automatic apply remains limited to typed safe fixes.

**Why this priority**: Diagnostics should move users forward, but doctor must not become an unsafe package manager, environment provisioner, evidence deleter, or release gate.

**Independent Test**: Run doctor against fixtures with safe InitTask repairs, stale decisions, package leftovers, gate-required DNS/HostFS proof, and unsafe recovery needs. Verify safe fixes remain typed and unsafe cases produce guidance only.

**Acceptance Scenarios**:

1. **Given** a safe InitTask repair is available, **When** the operator runs `doctor --fix --dry-run`, **Then** the plan is shown and no durable state is written.
2. **Given** a non-auto-fix finding such as stale decisions or package leftovers, **When** doctor reports it, **Then** the report includes a high-confidence command such as `hideout decision ...` or `hideout package repair ...` without deleting anything.
3. **Given** a finding would require deleting evidence, purging durable state, recreating environments, starting Lima, or running Gate 2/Gate 3, **When** doctor reports it, **Then** it refuses automatic recovery and labels the required manual or gate action.

### Edge Cases

- Deep doctor must not start Lima, run Gate 2/Gate 3, mutate HostFS, or perform hidden network probes by default.
- Candidate causes must be labeled as candidates and must not be phrased as definitive root cause unless directly observed.
- Feature selection must not accidentally run every deep check.
- Warning/degraded findings exit zero unless a selected check is explicitly required.
- Required local errors exit nonzero and appear consistently in human and JSON output.
- Stale decisions must never reveal claim tokens or provider-private refs.
- Package diagnostics must not claim package checksum coverage for external `tun2socks`.
- Doctor report export remains explicit; unrelated exports must not silently include doctor reports.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Doctor diagnostics, package/install status, helper/prerequisite status, adapter registry, decision/notice status, daemon/local lifecycle status, DNS/HostFS/privilege local facts, report evidence, and existing safe InitTask recovery. No new HostFS, network, backend, package, or decision authority is added.
- **Fail-closed behavior**: Missing required local facts, invalid reports, redaction failure, unsafe recovery, unknown package state, and ambiguous gate proof MUST produce error/refusal or gate-required status before side effects or claims.
- **User authority and policy**: The operator chooses light/deep level, feature scope, output format, evidence path, export selection, and repair dry-run/apply. Doctor may recommend typed commands but must not broaden profile, HostFS, adapter, network, package, or environment authority.
- **Generality and provider scope**: Check ids remain generic Hideout product diagnostics. Lima, `tun2socks`, package manifests, adapter packs, and helper binaries appear as local facts or prerequisites, not as generic semantics for unrelated providers.
- **Evidence surface**: Human doctor output, structured doctor report, saved evidence file, doctor audit/recovery evidence, export-selected doctor report, Gate 0 doctor smoke, and docs/test-plan updates.
- **Secret/redaction boundary**: Human output, JSON report, saved evidence, audit, and export-selected doctor report MUST NOT expose broker/UI/claim tokens, proxy secret values, generated machine ids, `HIDEOUT_SECRET_*` backing material, or hidden runtime credential paths.
- **Backend/gate expectation**: Gate 0 plus targeted doctor tests. Real Gate 2/Gate 3 are not run by doctor; doctor may point to them as required proof but must not replace them.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST make `doctor --level deep` produce additional local diagnostics beyond light mode for every supported doctor feature.
- **FR-002**: Deep and feature findings MUST classify diagnostic statements as observed facts, candidate causes, next actions, or gate-required proof.
- **FR-003**: Candidate causes MUST be labeled as candidates and MUST NOT be phrased as definitive root cause unless directly observed.
- **FR-004**: System MUST support focused feature diagnostics for adapters, decisions, packaging, DNS, HostFS, privilege, daemon, export/redaction, and cleanup.
- **FR-005**: Each supported feature diagnostic MUST produce at least one real local finding or an explicit gate-required marker.
- **FR-006**: Feature selection MUST NOT run unrelated feature-only findings unless `--level deep` is selected.
- **FR-007**: Packaging diagnostics MUST reuse package verification facts where available, report obsolete package-owned state from 017, and distinguish packaged helper failures from external prerequisites such as `tun2socks`.
- **FR-008**: Adapter diagnostics MUST report local adapter pack inventory, enabled adapter references, missing/revoked/disabled pack state, command conflicts when locally detectable, and adapter-pack test next actions.
- **FR-009**: Decision diagnostics MUST report pending, claimed, terminal, timeout-risk, stale-claim, and notice counts without leaking claim tokens or provider-private refs.
- **FR-010**: DNS, HostFS, Lima, and privilege diagnostics MUST distinguish local prerequisites from proof that requires real Gate 2/Gate 3 evidence.
- **FR-011**: Doctor MUST NOT start Lima, run Gate 2/Gate 3, mutate HostFS, delete state, or perform hidden network probes as part of default or deep local diagnostics.
- **FR-012**: Human output and JSON output MUST contain the same check ids, statuses, severities, required markers, and next actions.
- **FR-013**: Doctor human output, JSON output, saved evidence, audit/recovery evidence, and export-selected reports MUST share the same deterministic control-plane redaction boundary.
- **FR-014**: Redaction tests MUST inject representative control-plane-looking values and prove zero raw matches across doctor output modes.
- **FR-015**: Safe recovery MUST remain limited to existing typed safe fixes; non-auto-fix findings MUST provide guidance or explicit commands without applying them.
- **FR-016**: Warning/degraded findings MUST exit zero unless selected as required; required local errors MUST exit nonzero.
- **FR-017**: Docs MUST explain light versus deep behavior, feature-scope behavior, warning/error exit semantics, gate-required markers, redaction guarantees, and safe recovery limits.

### Key Entities *(include if feature involves data)*

- **Doctor Finding**: One diagnostic result with check id, category, feature, status, severity, required marker, observed facts, candidate causes, gate-required markers, next actions, evidence refs, and redacted details.
- **Feature Diagnostic**: A focused diagnostic bundle for one feature area, with local fact sources, gate-required boundaries, and next actions.
- **Doctor Report**: Ordered set of findings with selected level/features, output mode, summary, redaction metadata, and export eligibility.
- **Recovery Guidance**: Safe repair plan or non-auto-fix command advice attached to a finding.
- **Redaction Probe**: Representative synthetic control-plane-looking values used to verify every output mode shares the same redaction boundary.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On the same fixture store, deep doctor emits at least one additional finding for every supported feature compared with light doctor.
- **SC-002**: 100% of supported feature selectors produce either a real local finding or a gate-required marker.
- **SC-003**: 0 feature-only findings from unselected features appear when running a single `--feature` selector without `--level deep`.
- **SC-004**: Fixture tests cover adapters, decisions, packaging, DNS, HostFS, privilege, daemon, export/redaction, and cleanup categories.
- **SC-005**: Human and JSON output parity tests match 100% of check ids, statuses, severities, required markers, and next actions.
- **SC-006**: Redaction tests find 0 raw control-plane secret matches across human, JSON, evidence file, audit/recovery evidence, and export-selected doctor report output.
- **SC-007**: Warning/degraded fixtures exit 0, while required local error fixtures exit nonzero.
- **SC-008**: Deep doctor starts 0 backend instances, runs 0 real Gate 2/Gate 3 probes, and performs 0 hidden network probes in automated tests.
- **SC-009**: Doctor smoke in Gate 0 covers deep mode, at least three feature selectors, warning/error paths, redaction injection, and safe recovery dry-run.

## Assumptions

- Deep diagnostics are local and explicit; they improve troubleshooting but do not certify release readiness.
- Doctor reads current executable/package state when installed; explicit package prefix diagnostics can be added later if needed.
- Stale decisions warn by default; stricter CI policy can be introduced later.
- Automatic recovery remains limited to existing typed safe repairs.
- Gate 2/Gate 3 proof remains owned by release/test gates, not by doctor.
