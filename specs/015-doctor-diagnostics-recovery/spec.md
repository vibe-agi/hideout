# Feature Specification: Doctor Diagnostics And Recovery

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `015-doctor-diagnostics-recovery`

**Created**: 2026-07-09

**Status**: Draft

**Input**: User description: "Follow .tmp/011-016-plan.md using speckit-* skills; complete and commit one feature at a time. 015 turns gates, smoke scripts, schema checks, Lima probes, DNS checks, HostFS checks, privilege status checks, and packaging checks into user-facing doctor diagnostics and safe recovery."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Diagnose Local Readiness Quickly (Priority: P1)

An operator runs `hideout doctor` after install or first-run setup and receives a concise local readiness report that names each failed, warning, and passed check with a concrete next action.

**Why this priority**: External-alpha users need a self-service answer before they can trust profiles, package installs, daemon status, helper binaries, and local state.

**Independent Test**: Run doctor on a fresh package install and on fixtures with missing helper/profile/package state; verify human output, JSON output, exit code semantics, and redaction without invoking real guest or network probes.

**Acceptance Scenarios**:

1. **Given** a fresh package install with a valid store, **When** the operator runs `hideout doctor`, **Then** the report lists local store, package, helper, profile, daemon, template, and evidence checks with pass/warn/error status and next actions.
2. **Given** a missing or mismatched helper, **When** the operator runs doctor, **Then** the report marks the helper check as error, prints the expected repair command, and exits nonzero.
3. **Given** only declared degraded posture, **When** the operator runs doctor, **Then** the report marks it as warning/degraded, includes the evidence reason, and exits zero unless the check was required by the selected level or feature.

---

### User Story 2 - Run Explicit Deep Or Feature Diagnostics (Priority: P2)

An operator debugging a specific problem can opt into deeper local diagnostics for DNS, HostFS, Lima, privilege separation, adapter packs, packaging, daemon health, decision queue health, or export redaction without surprise guest or network probes in the default path. V1 feature checks are local inventory/status summaries plus explicit gate-required markers for facts that require real backend proof.

**Why this priority**: Deep diagnostics are useful but can be slow, require host tools, start guests, or touch network paths. They must be explicit to preserve operator control.

**Independent Test**: Run doctor with `--level deep` and individual `--feature` selectors against controlled fixtures and fake/available dependencies; verify selected scopes appear, unselected deep checks do not run, unavailable prerequisites produce actionable warnings/errors, and real-gate facts are not claimed from local placeholders.

**Acceptance Scenarios**:

1. **Given** the operator selects `--feature dns`, **When** doctor runs, **Then** local DNS mediation prerequisites or an explicit gate-required marker are included and unrelated deep checks are omitted.
2. **Given** the operator selects `--feature hostfs`, **When** doctor runs, **Then** HostFS read-only/write-overlay local readiness facts or an explicit gate-required marker are included.
3. **Given** a deep check needs a real backend prerequisite that is unavailable, **When** doctor runs, **Then** it reports the missing prerequisite and does not claim isolation evidence from a weak fallback.

---

### User Story 3 - Apply Safe Recovery Deliberately (Priority: P3)

An operator can ask doctor to apply low-risk repairs, preview those repairs first, and trust that recovery never deletes evidence or silently recreates environments.

**Why this priority**: Diagnostics without safe repair still leave the operator stuck, but repair must stay within Hideout's typed authority and fail-closed model.

**Independent Test**: Run `doctor --fix --dry-run` and `doctor --fix --apply` against fixtures with safe store/profile/schema/helper metadata issues; verify the plan, audit, no destructive behavior, and refusal for high-risk recovery.

**Acceptance Scenarios**:

1. **Given** a missing safe metadata item, **When** the operator runs `doctor --fix --dry-run`, **Then** doctor prints a repair plan and writes no durable state.
2. **Given** the same safe metadata item, **When** the operator runs `doctor --fix --apply`, **Then** doctor applies only the typed safe fix, writes audit evidence, and reports what changed.
3. **Given** a risky fix would delete evidence, purge durable state, recreate environments, or change isolation posture, **When** the operator asks doctor to recover, **Then** doctor refuses automatic apply and prints an explicit manual path or high-risk command.

---

### User Story 4 - Export Diagnostic Evidence On Demand (Priority: P4)

An operator preparing a bug report can intentionally include a doctor report as evidence while keeping control-plane secrets out and avoiding automatic bundling into unrelated exports.

**Why this priority**: External support needs reproducible facts, but diagnostic evidence must not become another leakage path or silently expand export scope.

**Independent Test**: Produce a doctor report and explicitly include it in an export/share flow; verify schema, redaction, provenance, and that normal exports omit doctor reports unless selected.

**Acceptance Scenarios**:

1. **Given** a doctor report exists, **When** the operator explicitly selects it for evidence export/share, **Then** the exported artifact includes the redacted report with provenance.
2. **Given** the operator exports unrelated audit evidence, **When** no doctor report is selected, **Then** the export does not silently include doctor reports.
3. **Given** diagnostic output contains proxy refs, tokens, generated machine ids, or hidden control-plane paths, **When** it is printed, saved, or exported, **Then** those values are deterministically redacted.

### Edge Cases

- Default doctor must not start a guest, run real DNS probes, mutate HostFS, or perform hidden network calls.
- Deep checks must be opt-in by level or feature and must report prerequisite failures rather than falling back to weak evidence.
- A warning/degraded finding must not produce a failing CI exit unless the selected level/feature declares it required.
- Required check failures must produce nonzero CI exit and structured JSON status.
- Recovery must not delete audit, evidence, profiles, adapter packs, or environments without an explicit purge/high-risk flow outside automatic doctor repair.
- Doctor must not leak control-plane secret values, generated machine ids, broker/UI tokens, backing `HIDEOUT_SECRET_*` values, or hidden runtime paths.
- JSON output must remain stable enough for Gate 0 and smoke scripts to assert check ids, statuses, severity, next actions, and evidence references.
- Unsupported platform/backend combinations must be reported as unsupported or degraded, not as passed.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Lifecycle diagnostics, package/install status, helper binary status, profile/schema status, daemon status, backend capability, DNS/HostFS/adapter/decision/export readiness, InitTask safe repair, local evidence report. Doctor observes many authorities but only applies repair through existing typed safe fixes.
- **Fail-closed behavior**: Missing prerequisites, unsupported deep checks, helper mismatches, invalid schema, unsafe recovery, ambiguous purge, unavailable backend, or redaction failure MUST report error/refusal before side effects. Deep checks must not replace real gate evidence with weak native fallbacks.
- **User authority and policy**: The operator selects level, feature scope, JSON/human output, evidence inclusion, and apply/dry-run. Doctor may recommend commands but must not broaden HostFS, adapter, network, privilege, daemon, or profile authority without a typed plan/apply path.
- **Generality and provider scope**: Doctor check ids are generic Hideout product checks. Named tools such as Lima or git are checked as dependencies, not turned into generic product semantics for unrelated backends.
- **Evidence surface**: Human report, JSON report, doctor audit entries, export-selected doctor report, Gate 0 smoke, and docs/status updates. Doctor reports are evidence only when explicitly selected for export/share.
- **Secret/redaction boundary**: Doctor output, JSON, audit, and export-selected reports must not expose control-plane secrets, backing secret env values, generated machine ids, broker/UI tokens, or hidden runtime credential paths.
- **Backend/gate expectation**: Gate 0 plus targeted diagnostics tests for local/light checks. Feature/deep checks that claim Lima, DNS, HostFS, or privilege facts must use real probes only when explicitly selected and must not claim release isolation unless the relevant real gate is run.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide a doctor report with stable check ids, title, category, status, severity, summary, next action, evidence references, and required/optional marker.
- **FR-002**: System MUST support human-readable output and structured JSON output for the same doctor facts.
- **FR-003**: Default doctor MUST run only light/local checks and MUST NOT start guests, run real DNS probes, mutate HostFS, or perform hidden network calls.
- **FR-004**: Doctor MUST support `--level light`, `--level deep`, and feature selectors for at least DNS, HostFS, Lima/backend, privilege, adapters, packaging, daemon, decisions, export/redaction, and cleanup.
- **FR-005**: Doctor MUST include package/install integrity and helper-binary presence/checksum checks when package state exists.
- **FR-006**: Doctor MUST include profile schema, template/onboarding metadata, migration, and store writability checks.
- **FR-007**: Doctor MUST include daemon status, daemon transport/auth status, and background operation health checks when daemon state exists.
- **FR-008**: Doctor MUST include local adapter-pack and command-adapter inventory/status checks and MUST NOT treat them as a substitute for adapter-pack smoke or digest-lock enforcement.
- **FR-009**: Doctor MUST include decision queue status checks for pending, claimed, terminal, timeout-risk, and notice counts without leaking claim tokens or provider-private refs.
- **FR-010**: Doctor MUST include export/audit availability and redaction-scope checks without automatically adding doctor reports to exports.
- **FR-011**: Doctor deep/feature DNS checks MUST distinguish local mediated-DNS prerequisite facts, missing proxy/resolver prerequisites, and real connected-subnet proof that is not available without the relevant gate.
- **FR-012**: Doctor deep/feature HostFS checks MUST distinguish local read-only/write-overlay readiness facts, unavailable local state, and real backend proof that is not available without the relevant gate.
- **FR-013**: Doctor privilege checks MUST report enforced, degraded, or unknown status and MUST preserve the non-claim when guest-root containment is not proven.
- **FR-014**: Required check failures MUST produce nonzero exit in CI/smoke use, while warnings and declared degraded states MUST remain exit zero unless selected as required by level/feature.
- **FR-015**: Doctor safe recovery MUST provide dry-run and apply modes, and apply MUST be limited to typed safe fixes that do not delete evidence, purge durable state, recreate environments, or change isolation posture.
- **FR-016**: Unsafe or high-risk recovery MUST be reported as advice or explicit manual command, not automatically applied by doctor.
- **FR-017**: Doctor reports MUST be explicitly selectable as export/share evidence and MUST NOT be silently bundled into unrelated exports.
- **FR-018**: Doctor output, JSON, audit, and export-selected reports MUST pass deterministic control-plane redaction.
- **FR-019**: Docs MUST describe default/light behavior, deep/feature opt-in behavior, CI exit semantics, safe recovery limits, and evidence export behavior.

### Key Entities *(include if feature involves data)*

- **Doctor Check**: Stable diagnostic definition with id, category, level, feature tags, required marker, prerequisite rules, and safe next actions.
- **Doctor Finding**: Result of a check with status, severity, summary, details, next action, evidence refs, and redacted fields.
- **Doctor Report**: Ordered collection of findings plus selected level/features, exit classification, generated timestamp, store/profile/backend facts, and redaction metadata.
- **Recovery Plan**: Typed safe repair plan generated from eligible findings, with dry-run/apply state and audit reference.
- **Diagnostic Evidence Selection**: Operator-selected inclusion of a doctor report in an export/share artifact.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Default doctor completes on a fresh local package install without starting a guest or performing hidden network probes.
- **SC-002**: JSON output covers 100% of human-output findings with the same check ids, statuses, severities, required markers, and next actions.
- **SC-003**: Fixture tests cover at least one pass, warning, or error result in package/helper, profile/schema, daemon, adapter inventory, decision status, privilege, DNS, HostFS, export/redaction, and cleanup categories.
- **SC-004**: Missing helper/package/profile required-check fixtures produce nonzero exit 100% of the time.
- **SC-005**: Declared warning/degraded fixtures produce zero exit unless the selected level/feature marks the check required.
- **SC-006**: `doctor --fix --dry-run` writes 0 durable state files in tests.
- **SC-007**: `doctor --fix --apply` applies only typed safe fixes and writes audit evidence for 100% of successful recovery tests.
- **SC-008**: Unsafe recovery fixtures are refused 100% of the time without deleting evidence, profiles, adapter packs, or environments.
- **SC-009**: Doctor report redaction tests find 0 control-plane secret matches in human, JSON, audit, and export-selected report output.
- **SC-010**: Gate 0 includes doctor smoke coverage for human output, JSON output, at least one required failure, at least one warning/degraded state, and safe dry-run recovery.

## Assumptions

- Default doctor is light/local; deep checks require explicit `--level deep` or `--feature`.
- JSON output is a product contract for smoke/CI, but not a public long-term API until 016 freezes the compatibility matrix.
- Safe recovery is limited to existing typed InitTask-style repair operations unless a later spec promotes additional typed repair plans.
- Doctor reports are exportable only when explicitly selected by the operator.
- Real Gate 2/Gate 3 evidence remains a release-gate concern; 015 local doctor marks such facts as gate-required rather than replacing real gates.
