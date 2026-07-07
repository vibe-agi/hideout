<!-- markdownlint-disable MD013 -->

# Feature Specification: Export/Share Redaction Boundary

**Feature Branch**: `005-export-share-redaction`

**Created**: 2026-07-07

**Status**: Draft

**Input**: User description: "Export/share redaction boundary — a safety valve for taking Hideout evidence off the local machine."

## Clarifications

### Session 2026-07-07

- Q: Does v1 cover all three export surfaces (audit slice, release-evidence bundle, Boundary Summary) at once, or only audit export with the others as follow-on? → A: All three surfaces in v1; each is a MUST in FR-001.
- Q: When user/application data is present and no `audit.redact` selection resolves it, does export include it by default or fail closed? → A: Fail closed absent an explicit operator decision (redact a selection, or explicitly acknowledge full-fidelity inclusion). The control-plane strip is always applied regardless.
- Q: How does export apply `audit.redact`, given the broker record path restores evidentiary metadata after the script runs (`internal/broker/broker.go:947`)? → A: Export reuses the same domain policy but with export-specific application semantics distinct from the broker record path: a defined set of user-redactable fields versus non-redactable evidentiary metadata (`requestId`, `subject`, `command`, `route`, `requestedAction`, `status`, `error`). A selection targeting a non-redactable evidentiary field fails closed, never silently restored or retained.
- Q: Does 005 send/upload the artifact, or only produce a local shareable copy? → A: Local shareable artifact only; the operator performs the actual send/upload. 005 owns no network/transport authority.
- Q: What does an empty or filtered-to-nothing export produce? → A: A valid artifact with zero evidence records plus a clear "0 records matched" notice — not an error, not a silent misleading file.
- Q: How does the operator make the required user-data decision on export? → A: Dual-track — a non-interactive flag/selection suitable for scripting/CI, and an interactive confirmation when a terminal is present. If neither provides a decision and the source carries user/application data, the export fails closed.
- Q: Who owns the split between user-redactable fields and non-redactable evidentiary metadata? → A: Go Core owns a fixed evidentiary set; the `audit.redact` policy cannot expand it (to dodge user-data redaction) or shrink it (to strip evidence). The split is deterministic, not policy-configurable.
- Q: When the operator acknowledges full-fidelity inclusion, does a configured `audit.redact` policy still apply? → A: Yes — the policy always runs; "acknowledge full-fidelity" covers only the residual unselected user data shipping verbatim and never bypasses configured scrubbing.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Share an audit or evidence artifact without leaking control-plane secrets (Priority: P1)

An operator wants to attach an audit slice or a release-evidence bundle to a bug
report, a support thread, or a message to a colleague — to show that a boundary
held, or to reproduce a problem. Today the only way to do that is to copy the raw
local audit JSONL or the release-evidence directory by hand, which carries
whatever full-fidelity content those files hold. The operator needs a single
mediated way to produce a shareable copy that provably carries no Hideout-minted
control-plane secret.

**Why this priority**: This is the core value and the reason the boundary
exists. 004 made the evidence bundle worth sharing to prove a boundary held; the
raw-copy path is the leak this feature closes. Without P1 there is no safe way to
take evidence off the box at all.

**Independent Test**: Seed an audit/evidence source with known control-plane
material (a `HIDEOUT_SECRET_*` value, a `cap_`/`ui_` token value, a control-plane
field, a generated machine-id) and user/application data, run the export, and
assert the produced artifact contains none of the control-plane material while
remaining a useful evidence artifact.

**Acceptance Scenarios**:

1. **Given** a local audit slice that contains a Hideout-minted proxy secret and a control-plane token, **When** the operator exports it for sharing, **Then** the exported artifact contains neither value and is still a readable audit of the same events.
2. **Given** a release-evidence bundle referencing an audit path, **When** the operator exports the bundle, **Then** the exported bundle carries the isolation-gate results and environment snapshot with all control-plane material stripped and no dangling reference to un-exported local files.
3. **Given** an export has completed, **When** the operator inspects the artifact, **Then** it records its own provenance (source, commit, which redaction stages were applied) so the recipient knows what was and was not scrubbed.

---

### User Story 2 - Choose what user/application data is scrubbed on the way out (Priority: P2)

The operator's own user/application data (target URLs, callback values, command
arguments, file contents surfaced in audit) is recorded verbatim locally on
purpose. When sharing, some of it may be sensitive to the operator or to third
parties. The operator needs to select what user/application data is redacted on
export, driven by the same `audit.redact` policy that owns user-data redaction —
not by Hideout guessing which values are secret.

**Why this priority**: Control-plane cleanliness (P1) is guaranteed and
automatic; user-data handling is a judgment the operator owns. This story makes
the export honest about the second redaction stage without turning Hideout into
a heuristic data-loss-prevention system.

**Independent Test**: Configure an `audit.redact` selection that scrubs a chosen
user field, export an artifact containing that field, and assert the field is
redacted in the artifact while a non-selected user field is preserved and all
control-plane material is still stripped.

**Acceptance Scenarios**:

1. **Given** an `audit.redact` policy that selects a user/application field for redaction, **When** the operator exports, **Then** that field is scrubbed in the artifact and unselected user data is preserved verbatim.
2. **Given** no user-data redaction selection is configured, **When** the operator exports an artifact that carries user/application data, **Then** the operator is required to make an explicit decision about that user data before the artifact can leave the box (see US3).

---

### User Story 3 - Refuse to ship anything unredacted (fail closed) (Priority: P3)

If the mandatory control-plane strip cannot be applied, or the operator has not
made the required decision about user data, the export must be refused with a
clear diagnostic — never a partial or unredacted artifact left on disk or sent.

**Why this priority**: Fail-closed is the non-waivable spine. An export path that
can silently emit unredacted evidence is worse than no export path. It depends on
P1/P2 existing, so it is validated last but is a hard requirement on both.

**Independent Test**: Force each failure mode (control-plane strip errors; a
required user-data decision is missing; the redaction policy errors) and assert
that no shareable artifact is produced and the operator sees a specific reason.

**Acceptance Scenarios**:

1. **Given** the control-plane strip cannot be applied to a source record, **When** export runs, **Then** the export is refused, no artifact is produced, and the diagnostic names the failure.
2. **Given** an artifact carries user/application data and no redaction decision has been made, **When** export runs, **Then** the export is refused rather than defaulting to include the data silently.
3. **Given** an export is refused, **When** the operator inspects the working directory, **Then** there is no partial artifact containing unredacted content.

---

### Edge Cases

- What happens when the source audit references files (an `auditPath`, an evidence log) that are themselves local and full-fidelity? The export must resolve and redact referenced content, or refuse — never emit a reference the recipient cannot open or that points back at un-exported local data.
- How does the system handle a Boundary Summary, which is already a lossy view designed to hide sensitive target values? It still passes the control-plane strip; it is the lightest case because it carries little raw user data.
- What happens when the `audit.redact` policy is present but errors or times out during export? Fail closed; no artifact.
- What happens when the operator exports an empty or filtered-to-nothing slice? Produce a valid artifact with zero evidence records plus a clear "0 records matched" notice — not an error and not a silent misleading file.
- What happens on re-export of an already-exported artifact? It is already redacted; re-export is a no-op-safe copy, not a second guessing pass that could differ.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: A new export/share evidence surface (audit export, release-evidence bundle export, Boundary Summary export); redaction; CLI and Manager/UI. No new host-capability authority (filesystem/network/host-open/endpoint) is granted — this governs what leaves the box, not what the target can reach.
- **Fail-closed behavior**: Export is refused when the deterministic control-plane strip cannot be applied, when a required user-data decision is missing, when the user-data redaction policy errors, or when a redaction selection targets a non-redactable evidentiary field. No partial or unredacted artifact is produced. Constitution Principle I and IV fail-closed behavior is non-waivable here.
- **User authority and policy**: The deterministic control-plane strip is Go-owned and always applied (the guaranteed floor). User/application-data redaction is owned by the operator through the `audit.redact` policy; deny/selection precedence follows the existing policy model. Go Core also owns a fixed non-redactable evidentiary set the policy cannot expand or shrink. The operator makes a conscious decision per export, expressible non-interactively (flag/selection) or as an interactive confirmation on a terminal; a configured policy always applies and acknowledgment covers only residual data.
- **Generality and provider scope**: This is a generic export/share boundary for any evidence artifact, expressed over the existing evidence and redaction model. It MUST NOT hard-code a specific bug tracker, transport, file format quirk, or destination as Core semantics.
- **Evidence surface**: The export itself is auditable — each export records a local meta-audit summary (source, redaction stages applied, operator decision) that embeds no source evidence content and, like any local audit event, passes only the deterministic control-plane strip (not the export user-data stage). `explain`/`doctor` may surface export readiness.
- **Secret/redaction boundary**: No Hideout-minted control-plane secret (`HIDEOUT_SECRET_*` backing material, broker/UI token values, control-plane field names, generated machine-id) may appear in any exported artifact — the same deterministic guarantee as local evidence. User/application data leaves only as the operator selected; Hideout does not guess user secrets.
- **Backend/gate expectation**: This is a data-handling/redaction claim, not an isolation claim, so it does not require a real-Lima isolation gate. Gate 0 (static: an exported artifact is provably clean) plus unit tests over the two-stage redaction and fail-closed paths are the evidence. The native harness is acceptable for exercising the CLI/Manager wiring because no isolation is being claimed.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Hideout MUST provide a single mediated export/share surface that produces a shareable copy of an evidence artifact (an audit slice, a release-evidence bundle, or a Boundary Summary), so that taking evidence off the box does not require hand-copying raw local files.
- **FR-002**: Every exported artifact MUST have the deterministic control-plane strip applied — the same contract as local evidence — so that no Hideout-minted control-plane secret (`HIDEOUT_SECRET_*` backing material, broker/UI token values, control-plane field names, generated machine-id) can appear in a shared artifact. This stage MUST be Go-owned and unconditional.
- **FR-003**: User/application data in an exported artifact MUST be redacted only according to the operator's selection, whose logic the `audit.redact` policy owns. Hideout MUST NOT apply heuristic or storage-time guessing to user data on export.
- **FR-004**: Export MUST fail closed: if the control-plane strip cannot be applied, if a required user-data decision is missing, or if the user-data redaction policy errors, the export MUST be refused with a specific diagnostic and MUST NOT leave any partial or unredacted artifact on disk.
- **FR-005**: Local full-fidelity surfaces (local audit JSONL, `hideout audit show`, authenticated Manager/WebUI views, and the `command.decide`/`audit.redact` script context) MUST remain unchanged; the export boundary governs only artifacts leaving the local trust zone.
- **FR-006**: When an exported artifact references other content (for example an `auditPath` or an evidence log), the export MUST either resolve and redact that referenced content into the artifact or refuse — it MUST NOT emit a reference that points back at un-exported local, full-fidelity data.
- **FR-007**: Each export MUST record a local meta-audit event capturing the source, the redaction stages applied, and the operator's user-data decision. As a local audit summary it MUST embed no source evidence content and passes the deterministic control-plane strip (not the export user-data stage); its summary fields (for example the `out` path) are local user/application data kept verbatim. If the meta-audit event is itself later exported, it passes export stage 2 like any audit event.
- **FR-008**: Before an artifact leaves the box, the operator MUST be able to see what will be included and what will be redacted (a pre-export review/summary), so sharing is a conscious act rather than a silent copy.
- **FR-009**: The exported artifact MUST carry provenance (source identity, commit where applicable, and which redaction stages were applied) so a recipient can tell what was and was not scrubbed.
- **FR-010**: The export MUST reuse the same `audit.redact` domain policy as the local record path but apply it with export-specific application semantics distinct from that path. Go Core MUST own a fixed set of non-redactable evidentiary metadata fields (`requestId`, `subject`, `command`, `route`, `requestedAction`, `status`, `error` — the set the local broker record path restores after the script runs); the `audit.redact` policy MUST NOT expand this set (to dodge user-data redaction) or shrink it (to strip evidence). When an operator/policy selection targets a non-redactable evidentiary field, the export MUST fail closed rather than silently restoring or retaining that field; it MUST NOT emit the field un-redacted after the operator asked to redact it.
- **FR-011**: The export MUST produce only a local shareable artifact; the operator performs any actual send or upload. 005 MUST NOT own or acquire network, transport, or destination authority — moving the artifact off the machine is outside this boundary.
- **FR-012**: The required user-data decision MUST be expressible non-interactively (a flag or selection suitable for scripting/CI) and, when an interactive terminal is present, MUST also be confirmable interactively. If neither a non-interactive decision nor an interactive confirmation is provided and the source carries user/application data, the export MUST fail closed.
- **FR-013**: A configured `audit.redact` policy MUST always be applied on export. An operator's acknowledgment of full-fidelity inclusion MUST cover only the residual user/application data the policy did not scrub; it MUST NOT bypass, disable, or override a configured policy.

### Key Entities *(include if feature involves data)*

- **Export/Share Boundary**: The single mediated path an evidence artifact crosses to leave the local trust zone. Owns the two-stage redaction and the fail-closed decision.
- **Evidence Source**: A local, full-fidelity artifact eligible for export — an audit slice, a release-evidence bundle, or a Boundary Summary — unchanged by the export.
- **Exported Artifact**: The redacted, shareable copy produced by the boundary, carrying provenance and no control-plane secret.
- **Deterministic Control-Plane Strip**: The Go-owned, unconditional first redaction stage (reuses the existing deterministic redaction contract) — the guaranteed floor.
- **User-Data Redaction Selection**: The operator-owned second stage, expressed through the `audit.redact` policy, choosing which user/application values are scrubbed on export. It operates only within the Core-owned user-redactable field space; it cannot reach the fixed non-redactable evidentiary set.
- **Non-Redactable Evidentiary Set**: The Core-owned, fixed set of evidentiary metadata fields (for example `requestId`, `subject`, `command`, `route`, `status`, `error`) that a redaction selection cannot delete; a selection targeting one drives the fail-closed refusal.
- **Export Decision**: The operator's conscious, recorded choice governing user data for a given export, expressible non-interactively (flag/selection) or as an interactive confirmation on a terminal; its absence, when user data is present, drives the fail-closed refusal. An acknowledgment of full-fidelity inclusion covers only residual data a configured policy did not scrub.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of exported artifacts contain zero Hideout-minted control-plane secrets, verified by seeding known control-plane material into the source and asserting its absence in the artifact.
- **SC-002**: 100% of exports where a required redaction stage cannot be applied are refused, producing no shareable artifact and no partial file.
- **SC-003**: An operator can produce a shareable audit or evidence artifact in a single command/flow that includes a pre-export review of what is included versus redacted.
- **SC-004**: 0 regressions to local full-fidelity surfaces — local audit, `hideout audit show`, and authenticated Manager/WebUI views still present user/application data verbatim after this feature ships.
- **SC-005**: 100% of exports produce a meta-audit event recording the redaction stages applied and the operator's user-data decision.
- **SC-006**: A user-redactable field selected by the `audit.redact` policy is redacted in 100% of exported artifacts that carry it, while unselected user data is preserved.
- **SC-007**: When a selection targets a non-redactable evidentiary field, 100% of such exports fail closed — no artifact that retains the targeted field un-redacted is produced.
- **SC-008**: When the operator acknowledges full-fidelity inclusion with a configured `audit.redact` policy present, 100% of policy-selected user fields are still redacted — acknowledgment covers only residual unselected data and never bypasses the policy.

## Assumptions

- **Reused mechanisms**: The deterministic control-plane strip reuses the existing `internal/audit` redaction contract (`RedactString`/`RedactArgv`/`RedactValue`), and user-data redaction reuses the existing `audit.redact`/`redactAudit` Goja policy already wired in the broker audit path. No new redaction engine is introduced.
- **Export destinations are operator-driven and off-box** (a file to attach, a bug report, a colleague), not the public marketplace. Ecosystem trust machinery (signing, revocation, publisher identity, namespace protection) is a marketplace-launch concern and is explicitly out of scope, per the constitution's prosumer/MVP constraint.
- **Fail-closed for user data (resolved, Clarifications 2026-07-07)**: When user/application data is present and no `audit.redact` selection resolves it, the export requires an explicit operator decision (redact a selection, or explicitly acknowledge full-fidelity inclusion) and refuses absent that decision. The control-plane strip is always applied regardless. This is the constitution-consistent (non-waivable fail-closed) reading.
- **v1 surface set (resolved, Clarifications 2026-07-07)**: v1 covers all three surfaces — audit slice export, release-evidence bundle export, and Boundary Summary export — each a MUST in FR-001; none is deferred out of v1. Boundary Summary is the lightest because it is already lossy. (The P1/P2/P3 story priorities describe the three redaction properties — control-plane cleanliness, user-data selection, fail-closed — not a per-surface ordering; surface delivery order within v1 is a plan concern.)
- **No isolation claim**: This feature makes a data-handling/redaction claim, not an isolation claim, so it is provable by Gate 0 plus unit tests without a real-Lima gate; the native harness is acceptable for CLI/Manager wiring.
- **Docs to update on implementation**: `docs/STATUS.md` (promote the export/share redaction surface from design-ready to implemented), `docs/threat-model.md` (sharing under the boundary becomes a claim; the raw-copy path remains the unsafe path), and the relevant design contract and test plan, per the constitution's Development Workflow.
