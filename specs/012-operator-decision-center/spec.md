# Feature Specification: Operator Decision Center

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `[012-operator-decision-center]`

**Created**: 2026-07-08

**Status**: Draft

**Input**: User description: "Implement 012 from `.tmp/011-016-plan.md`: create one local operator center for actionable decisions and informational notices. Migrate HostFS write decisions onto the common model, add adapter capability proposals and share/leaving-machine export confirmations, keep pure local export synchronous, separate notices from approvals, support CLI/TUI/WebUI claim/resolve/watch flows, enforce timeout default-deny for actionable decisions, and preserve typed Manager/Core authority."

## Clarifications

### Session 2026-07-08

- Q: Are all operator-facing events approvals? -> A: No. 012 separates actionable decisions from informational notices. Claim, approve/apply, deny/discard, lease, and timeout/default-deny apply only to actionable decisions; notices use broadcast, acknowledgement, severity, and evidence.
- Q: Which decisions enter v1? -> A: HostFS write decisions from 010, adapter capability proposals from 008/011, and export/share confirmations only when evidence is leaving the machine or being prepared for sharing.
- Q: Does pure local export enter the decision inbox? -> A: No. Pure local export remains synchronous under the 005 contract unless a share/leaving-machine mode is explicitly requested.
- Q: Who may resolve decisions? -> A: Any authenticated local operator surface may claim and resolve an actionable decision; the first valid claim wins and every loser receives a stale/claimed response.
- Q: What is the default timeout behavior? -> A: Actionable decisions default to deny/discard on timeout, with a global default and optional per-kind overrides. Notices do not time out into denial because they are facts, not pending authority.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Resolve Local Actionable Decisions In One Place (Priority: P1)

As a Hideout operator, I want pending local authority decisions to appear in one queue so HostFS writes, adapter capability proposals, and share/export confirmations are handled consistently instead of each feature inventing its own approval surface.

**Why this priority**: This is the MVP. 010 already introduced HostFS write decisions, and 008/011 proposals need a shared local operator path before the ecosystem feels coherent.

**Independent Test**: Stage one HostFS write decision and one adapter capability proposal, list them through the operator center, claim one from a surface, approve or deny it, and verify the underlying Manager/Core provider applies only the claimed decision while the other remains pending.

**Acceptance Scenarios**:

1. **Given** a pending HostFS write, **When** the operator lists decisions, **Then** the queue shows a redacted preview, risk facts, allowed terminal actions, timeout/default outcome, and audit reference for that write.
2. **Given** an adapter returns a non-applied capability proposal, **When** the proposal is admitted to the decision center, **Then** it remains non-executed until an authenticated operator claims and resolves it through the typed Manager path.
3. **Given** two authenticated surfaces try to claim the same decision, **When** the first claim succeeds, **Then** every later claim or apply attempt fails closed as stale/claimed and records evidence.

---

### User Story 2 - Treat Warnings And Status As Notices, Not Fake Approvals (Priority: P2)

As an operator monitoring Hideout, I want privilege degradation and background status to appear beside decisions without being forced into approve/deny semantics that do not make sense for facts that already happened.

**Why this priority**: Mixing notices into decision leases would create false "deny" actions for status facts and repeat the overclaim pattern from earlier features.

**Independent Test**: Emit a privilege degraded notice and a background operation status notice, verify they are listed separately from actionable decisions, acknowledge each notice, and verify no claim token, approval route, timeout-deny, or provider apply path exists for either notice.

**Acceptance Scenarios**:

1. **Given** a run reports privilege status `degraded` or `unknown`, **When** the operator opens the center, **Then** the notice is visible with severity, source, acknowledgement state, and evidence reference but no approve/deny controls.
2. **Given** a daemon background operation changes state, **When** the operator watches notices, **Then** the status update is broadcast and can be acknowledged without altering the operation outcome.
3. **Given** a notice is acknowledged, **When** export/share evidence is later produced, **Then** the acknowledgement and original fact remain auditable without implying any authority was granted.

---

### User Story 3 - Share Evidence Only After Explicit Local Decision (Priority: P3)

As an operator preparing evidence for another person, I want sharing/leaving-machine exports to go through the same decision center so I can review redaction, approve or deny the share, and keep local-only exports fast.

**Why this priority**: 005 already made local export safe. 012 should add a consistent decision path only when evidence leaves the machine, not degrade the local workflow.

**Independent Test**: Run a pure local export and verify it remains synchronous; then request a share/leaving-machine export and verify it creates an actionable decision with redacted preview, claim/approve/deny behavior, timeout default-deny, and export evidence.

**Acceptance Scenarios**:

1. **Given** an operator runs a pure local export, **When** the export is not marked for sharing or leaving the machine, **Then** it completes under the existing 005 contract without entering the decision queue.
2. **Given** an operator requests a share/leaving-machine export, **When** redaction review succeeds, **Then** the export waits as an actionable decision until an authenticated operator approves or denies it.
3. **Given** a share/export decision times out or is denied, **When** the workflow ends, **Then** no share artifact is released and the denial/timeout is recorded as evidence.

---

### User Story 4 - Watch Decisions From CLI, TUI, And WebUI (Priority: P4)

As a local operator using multiple surfaces, I want CLI, TUI, and WebUI to show the same decision/notice state and resolve changes without polling or drifting from the Manager truth.

**Why this priority**: 006/007 created daemon event plumbing. 012 should reuse that substrate so the decision center is not CLI-only.

**Independent Test**: Open two local surfaces, create a decision and a notice, claim and resolve the decision from one surface, acknowledge the notice from another, and verify every surface updates to the same final state and shows no control-plane secret material.

**Acceptance Scenarios**:

1. **Given** a decision is created while WebUI and TUI are watching, **When** one surface claims and resolves it, **Then** both surfaces show the same terminal state and the losing surface cannot apply stale authority.
2. **Given** notices are broadcast, **When** a surface acknowledges one notice, **Then** every subscribed surface reflects the acknowledgement state.
3. **Given** decision previews include user-supplied paths, URLs, command names, or export metadata, **When** any local surface renders them, **Then** Hideout control-plane credentials and hidden implementation paths are absent.

### Edge Cases

- A decision source disappears or becomes stale between listing, claim, and apply.
- Two surfaces claim or resolve the same decision concurrently.
- A claim lease expires while an operator is reviewing the preview.
- A decision reaches timeout while a surface is offline.
- A HostFS write compatibility route and the generic decision route target the same staged operation.
- An adapter proposal asks for a capability that has no promoted provider.
- A share/leaving-machine export has redaction errors or residual user data with no explicit decision.
- A notice is acknowledged before its backing audit event is readable.
- A notice source repeats the same fact many times.
- A decision or notice contains malformed preview data, unsupported kind, unknown version, or control-plane secret-looking content.
- The decision store cannot persist state or audit evidence.
- The daemon is not running, a token expires mid-watch, or the WebUI/TUI reconnects after decisions have changed.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: HostFS write decisions, adapter capability proposals, export/share boundary, privilege status notices, daemon background status notices, Manager API, CLI/TUI/WebUI/daemon event surfaces, audit/export evidence, and local decision lifecycle.
- **Fail-closed behavior**: Actionable decisions fail closed on missing provider, unsupported kind, missing or expired claim, stale version, timeout, denied decision, invalid preview, redaction failure, persistence failure, audit failure, missing source, incompatible compatibility route, or unpromoted capability. Notices never grant authority and cannot be converted into approvals.
- **User authority and policy**: Operator-authored local decisions remain user-authoritative after claim/approval, subject to existing provider validation and deny precedence. Adapter proposals and shared exports are requests, not authority. HostFS writes still require explicit overlay grants and Core apply validation.
- **Generality and provider scope**: This is a generic local decision/notice center. HostFS write, adapter proposal, export/share, privilege status, and background operation are current kinds; provider-specific tools or package managers must remain payload examples, not Core semantics.
- **Evidence surface**: Decision create, claim, approve/apply, deny/discard, timeout, stale-claim refusal, notice create, notice acknowledgement, compatibility-route usage, and redaction failure must be visible through audit, Manager API, TUI/WebUI, and export/share evidence.
- **Secret/redaction boundary**: Decision previews, notice previews, audit, UI, logs, watch events, and export artifacts must not expose broker tokens, UI tokens, claim tokens, `HIDEOUT_SECRET_*` backing values, generated machine IDs, hidden store paths, overlay object paths, backend handles, proxy values, or raw control-plane field names.
- **Backend/gate expectation**: Gate 0 plus focused Manager/daemon/UI tests are required. Real Lima is not required unless implementation changes HostFS guest data-plane behavior, backend setup, DNS, or privilege separation claims.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST maintain one local operator center containing two distinct record classes: actionable decisions and informational notices.
- **FR-002**: System MUST NOT expose claim, approve/apply, deny/discard, lease, or timeout/default-deny controls for informational notices.
- **FR-003**: System MUST represent actionable decisions with stable id, kind, source profile/session/backend where applicable, risk facts, proposed action, current state, timeout/default outcome, claimant/lease metadata, redacted preview, audit reference, allowed terminal actions, and version.
- **FR-004**: System MUST represent informational notices with stable id, kind, source profile/session/backend where applicable, severity, current fact/status payload, acknowledgement state, redacted preview, audit reference, and version.
- **FR-005**: System MUST migrate HostFS write decisions onto the generic actionable decision model or provide compatibility routes backed by the same decision record, without creating a second source of truth.
- **FR-006**: System MUST admit adapter capability proposals as actionable decisions only when the requested capability is declared, known, and backed by a promoted provider; otherwise the proposal fails closed as non-applied.
- **FR-007**: System MUST admit export/share confirmations as actionable decisions only for evidence leaving the machine or being prepared for sharing.
- **FR-008**: System MUST keep pure local export synchronous under the existing export contract when no share/leaving-machine action is requested.
- **FR-009**: System MUST allow authenticated local CLI, TUI, WebUI, and Manager API surfaces to list, inspect, watch, claim, approve/apply, deny/discard, and observe terminal states for actionable decisions.
- **FR-010**: System MUST allow authenticated local surfaces to list, inspect, watch, and acknowledge informational notices without creating provider authority.
- **FR-011**: System MUST ensure exactly one valid claimant can own an actionable decision at a time; stale, duplicate, expired, or losing claims MUST fail closed.
- **FR-012**: System MUST support one global default timeout plus per-kind timeout overrides for actionable decisions, and timeout MUST resolve to the declared default denial/discard outcome with evidence.
- **FR-013**: System MUST preserve provider-specific validation at apply time; a claimed decision MUST still fail closed if the underlying HostFS, adapter, export/share, or future provider validation rejects the action.
- **FR-014**: System MUST emit decision and notice state changes to local watch/event surfaces so TUI/WebUI/CLI can update without owning authority.
- **FR-015**: System MUST redact decision and notice previews before storage, API responses, watch events, UI rendering, and export/share artifacts.
- **FR-016**: System MUST record audit evidence for decision creation, claim, approve/apply, deny/discard, timeout, stale-claim refusal, notice creation, notice acknowledgement, persistence failure, and redaction failure.
- **FR-017**: System MUST expose machine-readable decision/notice status for doctor and future diagnostics without requiring direct reads of provider-private state.
- **FR-018**: System MUST preserve compatibility for existing 010 HostFS write CLI/API workflows either as stable shims or as documented versioned responses backed by the generic decision store.
- **FR-019**: System MUST prevent daemon-mediated implicit prompt approval: daemon and watch surfaces may broadcast state and authenticate requests, but missing UI prompt or disconnected surfaces MUST NOT approve a decision.
- **FR-020**: System MUST fail closed without partial authority changes when the decision store, provider apply path, audit writer, redaction path, or watch-event emission cannot complete safely.
- **FR-021**: System MUST update docs/status/test plan so decisions and notices are described separately and no status warning is described as something an operator can deny.

### Key Entities *(include if feature involves data)*

- **Operator Center**: Local collection of actionable decisions and informational notices visible through authenticated local surfaces.
- **Actionable Decision**: Pending or terminal authority request that can be claimed and resolved by an authenticated operator.
- **Informational Notice**: Local fact or status update that can be viewed and acknowledged but never approved into authority.
- **Decision Claim**: Exclusive time-bounded ownership record for one actionable decision.
- **Decision Resolution**: Terminal outcome such as approved/applied, denied/discarded, timed out, failed, or stale.
- **Notice Acknowledgement**: Local acknowledgement state for one notice and surface/operator context.
- **Decision Preview**: Redacted summary of paths, commands, export metadata, risk facts, or provider-specific payload shown before resolution.
- **Decision Evidence**: Audit/export facts for decision and notice lifecycle events.
- **Compatibility Route**: Existing feature-specific route that remains available while using the generic decision store as the source of truth.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of HostFS write decisions in tests are visible through the generic decision center and the existing HostFS write route without state divergence.
- **SC-002**: 100% of adapter proposal fixtures for unsupported, undeclared, or unpromoted capabilities fail closed without provider side effects.
- **SC-003**: 100% of share/leaving-machine export fixtures require an actionable decision, while 100% of pure local export fixtures remain synchronous.
- **SC-004**: Concurrent claim tests produce exactly one winning claimant and all losing apply attempts fail closed with stale/claimed evidence.
- **SC-005**: Timeout tests resolve 100% of actionable decisions to the configured denial/discard outcome and record audit evidence.
- **SC-006**: Notice tests prove 100% of privilege/background notices have no approve/deny/claim fields or routes.
- **SC-007**: Redaction tests prove 100% of decision previews, notice previews, API responses, watch events, UI renderings, and exported evidence contain no Hideout control-plane credentials or hidden implementation paths.
- **SC-008**: CLI, TUI, and WebUI/watch tests each observe decision creation and terminal resolution without mutating provider authority outside Manager/Core.
- **SC-009**: Persistence failure tests prove no partial provider authority change occurs when decision, audit, or redaction writes fail.
- **SC-010**: Gate 0 or equivalent local smoke covers list/inspect/watch/claim/approve/deny/timeout/acknowledge plus HostFS compatibility before 012 is marked complete.

## Assumptions

- 012 is local single-operator coordination, not remote approval, organizational policy, roles, compliance workflow, or multi-tenant delegation.
- CLI is the first complete management surface; TUI and WebUI should consume the same Manager/Core model and daemon event stream rather than implementing separate authority.
- HostFS write decisions from 010 are the first migrated provider and may keep compatibility routes while the generic model becomes canonical.
- Adapter proposals remain non-applied until a promoted Go-owned provider exists for the requested capability.
- Export/share means evidence leaves the machine or is prepared for sharing. Pure local export does not require an inbox decision.
- Notices are facts and status records. Acknowledging a notice records operator awareness, not approval or denial of authority.
- Decision and notice state is local store evidence and follows the deterministic control-plane redaction contract.
