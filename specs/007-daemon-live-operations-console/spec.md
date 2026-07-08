<!-- markdownlint-disable MD013 -->

# Feature Specification: Daemon Live Operations Console

**Feature Branch**: `007-daemon-live-operations-console`

**Created**: 2026-07-08

**Status**: Draft

**Input**: User description: "007 should follow 006 by finishing the product UI slice that 006 deliberately deferred: WebUI and TUI surfaces should use the daemon event stream as real live state, not merely as a trigger to re-fetch. After one initial seed, panels should update from typed event payloads with zero steady-state overview polling, and the feature should add end-to-end user-visible proof for both browser WebUI and terminal TUI. This is not a UI framework migration; those choices belong in plan."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Watch WebUI update from live daemon payloads (Priority: P1)

An operator opens the WebUI while `hideoutd` is running and watches the same
operational panels the current WebUI already exposes: environments, runs,
background work, audit/export/cleanup outcomes, and stream health. The WebUI
gets one initial current-state seed, then keeps those panels current from typed
daemon event payloads for daemon-mediated operation sources. While the stream is
healthy, the WebUI does not poll or re-fetch overview/audit resources to discover
changes.

**Why this priority**: This closes the most visible 006 deferred item. 006 made
the browser reachable and event-triggered, but it still re-fetched on each
event. The main product value of 007 is turning the WebUI into a live
operations console with proof that the visible state came from event payloads.

**Independent Test**: Load the served WebUI JavaScript through the deterministic
reducer harness, record the single initial seed, drive representative daemon
events, and assert that visible panel state changes from event payloads with no
further overview/audit reads during a healthy-stream window.

**Acceptance Scenarios**:

1. **Given** the daemon is running and the WebUI has loaded its initial seed, **When** an environment stop/clean operation changes status, a daemon-mediated run changes status, a background operation changes status, an audit record arrives, an evidence export apply completes, or an existing run session cleanup completes, **Then** the corresponding visible panels update from daemon event payloads without another overview/audit read.
2. **Given** the WebUI stream is healthy, **When** no events arrive, **Then** the WebUI performs no timer-based refresh and no steady-state overview/audit polling.
3. **Given** the stream ends, the credential expires, or a required event field is missing, **When** the WebUI can no longer prove current state from the seed plus events, **Then** it marks the view stale/disconnected before presenting further state as live.
4. **Given** seeded control-plane material exists in test fixtures, **When** the WebUI renders live event payloads, **Then** no control-plane secret appears in the page, logs, event payloads, or test artifacts.

---

### User Story 2 - Watch TUI update from live daemon payloads (Priority: P2)

An operator uses the TUI/watch dashboard while `hideoutd` is running. The TUI
gets one seed, then applies daemon event payloads to the terminal view. While
the stream is healthy it does not run an interval poll in parallel. If the stream
closes, the terminal clearly shows that live mode is degraded before falling
back to any daemon-less behavior.

**Why this priority**: The TUI is the fastest local operational surface and the
place where hidden polling regressions are easiest to miss. 006 already had to
fix a parallel polling timer; 007 should make the terminal live-state contract
explicit and testable.

**Independent Test**: Start the TUI/watch dashboard against a daemon stream,
drive events, and assert the rendered terminal state changes exactly because of
those events while no interval overview polling runs during the healthy-stream
window.

**Acceptance Scenarios**:

1. **Given** the TUI has received its initial seed, **When** daemon events change environment/run/background/audit state, **Then** the terminal display updates from event payloads without interval polling.
2. **Given** the daemon stream is healthy but idle, **When** the watch interval would previously have fired, **Then** no render caused by timer polling occurs.
3. **Given** the stream closes or the credential expires, **When** the TUI cannot prove live state, **Then** it marks live mode stale/disconnected and only then uses an explicit daemon-less fallback.
4. **Given** duplicate, old, or out-of-order events, **When** the TUI applies them, **Then** the visible state remains deterministic and no completed or failed operation is shown as active again.

---

### User Story 3 - Trust the live view and diagnose stream health (Priority: P3)

An operator or maintainer needs to trust that the live console is current and
safe to share locally. Each visible state transition can be traced to a typed
event payload or the initial seed; event schema drift is caught; unknown future
events do not crash the UI; and stream health is visible. If the console cannot
prove it is live, it says so instead of silently mixing stale state, hidden
polling, and live updates.

**Why this priority**: Payload-driven UI is only valuable if it is auditable and
recoverable. This story makes live-state correctness, schema coverage, and
degraded-state behavior first-class instead of incidental UI behavior.

**Independent Test**: Validate the event catalog against the current panels,
drive event gaps/schema mismatches/duplicates/unknown event kinds, and assert
the WebUI and TUI either update deterministically or enter a visible stale state
without leaking control-plane material.

**Acceptance Scenarios**:

1. **Given** the event catalog version supported by the clients, **When** every event kind needed by the current WebUI/TUI panels is emitted, **Then** each event contains sufficient redacted payload data for the panels to update without an overview re-fetch.
2. **Given** an unknown future event kind, **When** a client receives it, **Then** the client ignores or records it without crashing and without marking known state current from unknown data.
3. **Given** a known event kind missing a required field, **When** a client receives it, **Then** the client marks the affected live view stale rather than inventing state.
4. **Given** a client reconnects after daemon restart, **When** historical events are unavailable, **Then** the client performs a new explicit seed and resumes from that point without claiming replayed history.

---

### Edge Cases

- What happens when an event arrives before the initial seed completes? The client must not apply it to an unseeded view as if it were complete current state; it either buffers safely within limits or re-seeds explicitly.
- What happens when events arrive out of order, are duplicated, or refer to an unknown entity? The reducer must be deterministic: ignore duplicates/older events, surface unknown references as stale or pending, and never revive terminal operations incorrectly.
- What happens when a required event field is absent or has an unsupported schema version? The affected panel enters a stale/schema-mismatch state rather than falling back to hidden polling or displaying invented state.
- What happens when the stream is idle for a long time? The UI remains live without timer polling, showing stream health and last known state; idle is not an error by itself.
- What happens when the daemon stream closes, credentials expire, or the daemon restarts? The UI marks disconnected/stale, stops claiming live state, and may recover only through an explicit new seed plus a new authenticated stream.
- What happens when multiple WebUI tabs and TUI sessions subscribe? Each subscriber gets consistent seed-plus-event semantics; a slow subscriber cannot stall the daemon or other subscribers.
- What happens when event payloads contain local user/application data? Local views may show that data verbatim, but Hideout-minted control-plane material must be stripped from payloads, UI, logs, and test artifacts.
- What happens when the daemon is absent? Existing daemon-less behavior remains available, but it is clearly a fallback and must not be described as daemon-backed live state.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: UI/daemon/evidence only. 007 consumes the daemon event stream and existing Manager read/plan/apply surfaces; it adds no target authority, raw host execution, raw profile writes, prompt channels, public network exposure, or new Manager operation class.
- **Fail-closed behavior**: If the client cannot prove live state from one seed plus typed events — missing required payload fields, schema mismatch, stream termination, credential expiry, event gap, or unknown entity reference — the affected view is marked stale/disconnected and stops claiming live state. Hidden polling is not an acceptable fallback while the daemon stream is healthy.
- **User authority and policy**: UI actions continue to go through the existing typed Manager plan/apply flow and existing confirmation/audit behavior. Event reducers are read-model logic only; they must not execute authority or mutate profile/run state directly.
- **Generality and provider scope**: Generic Hideout operations-console behavior over the daemon event contract. The spec does not choose any UI framework, backend provider, or transport vendor as Core semantics.
- **Evidence surface**: The visible WebUI and TUI render state becomes evidence of daemon event consumption. The feature must include deterministic WebUI reducer execution for served browser JavaScript, terminal-output verification, and schema/drift checks for the event catalog. Full headless-browser UX automation is not required for 007.
- **Secret/redaction boundary**: Daemon tokens, profile secrets, machine identifiers, proxy credentials, and other Hideout-minted control-plane material must not appear in event payloads, rendered UI, logs, screenshots, terminal captures, or generated test artifacts. Local user/application data remains local and may be shown verbatim unless it crosses the 005 export/share boundary.
- **Backend/gate expectation**: No new isolation claim. Gate 0 plus unit/integration/UI-harness tests are sufficient. Real-Lima is not required unless plan introduces a backend-specific assertion, which this spec does not.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The WebUI live console MUST render its current operational panels from one initial seed followed by typed daemon event payloads while the daemon stream is healthy.
- **FR-002**: While the daemon stream is healthy, the WebUI MUST NOT perform steady-state overview/audit re-fetches or timer-based polling to discover panel changes after the initial seed.
- **FR-003**: The TUI live/watch dashboard MUST render its current operational panels from one initial seed followed by typed daemon event payloads while the daemon stream is healthy.
- **FR-004**: While the daemon stream is healthy, the TUI MUST NOT perform interval overview/audit polling or render cycles caused by a polling timer after the initial seed.
- **FR-005**: The daemon event catalog MUST provide typed payloads sufficient for the current WebUI and TUI panels to update environment stop/clean lifecycle, daemon-mediated run/session, background operation, audit, export, cleanup, and stream-health state without an overview/audit re-fetch.
- **FR-006**: Every event payload used by the live console MUST carry enough identity, state, ordering, and redacted display data for deterministic client-side reduction; clients MUST NOT invent missing state.
- **FR-007**: WebUI and TUI reducers MUST handle duplicate, old, out-of-order, unknown, and unknown-entity events deterministically without crashing and without reviving terminal operations incorrectly.
- **FR-008**: A known event kind missing a required field, an unsupported schema version, an event gap, stream termination, or credential expiry MUST put the affected view into a visible stale/disconnected state before any further state is presented as live.
- **FR-009**: Clients MAY recover from stale/disconnected state only through an explicit new seed plus an authenticated stream; they MUST NOT silently continue claiming daemon-backed live state from stale data.
- **FR-010**: Event payloads, rendered UI, logs, screenshots, terminal captures, and test artifacts MUST contain zero Hideout-minted control-plane material.
- **FR-011**: UI actions from the live console MUST continue to use the existing Manager plan/apply/read contracts; event reducers MUST remain read-only and MUST NOT execute authority, bypass validation, or write profile/run state.
- **FR-012**: Daemon-less WebUI/TUI behavior MUST remain available and unchanged, but surfaces MUST distinguish daemon-backed live state from daemon-less fallback.
- **FR-013**: The WebUI MUST have a deterministic served-JavaScript reducer verification path proving visible panel state changes from daemon event payloads without manual refresh and without steady-state overview/audit polling after seed.
- **FR-014**: The TUI MUST have an end-to-end user-visible verification path proving terminal output changes from a daemon event payload without interval polling while the stream is healthy.
- **FR-015**: Multiple concurrent subscribers MUST receive consistent seed-plus-event semantics; a slow or stuck subscriber MUST NOT stall daemon operation, other subscribers, or UI state reduction.
- **FR-016**: The event catalog and client reducers MUST have drift guards so that adding/removing/renaming required event fields or panel states fails tests rather than silently falling back to hidden polling.
- **FR-017**: Scope is limited to the existing WebUI/TUI operational panels and their live-state contract; full UI redesign, prompt channels, plugin marketplaces, mobile/desktop app packaging, and framework migration are OUT of 007.

### Key Entities *(include if feature involves data)*

- **Live State Seed**: The one current-state snapshot a client reads before consuming events; establishes baseline state and schema version for a live view.
- **Daemon Event Payload**: A typed, redacted state-change record emitted by `hideoutd` with identity, ordering, state, display fields, and schema information sufficient for reducer updates.
- **Live State Reducer**: Client-side read-model logic that applies seed plus event payloads to produce WebUI/TUI panel state. It is read-only and cannot execute authority.
- **Operations Panel State**: The visible state shown by WebUI/TUI for environments, runs/sessions, background operations, audit/export/cleanup outcomes, and stream health.
- **Stream Health State**: The client-visible connection status: seeding, live, idle-live, stale, disconnected, schema-mismatch, or daemon-less fallback.
- **Event Catalog Version**: The supported set of event kinds, required fields, ordering rules, and redaction guarantees used by daemon emitters and UI reducers.
- **User-Visible Live Proof**: Deterministic browser-side reducer and terminal verification artifacts proving panel text/state changes because of daemon event payloads rather than hidden polling.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In the WebUI deterministic reducer test, every current operational panel represented in the event catalog updates visible panel state from daemon event payloads after one seed, with zero overview/audit reads after that seed during the healthy-stream window.
- **SC-002**: In the TUI end-to-end test, terminal output updates visibly from daemon event payloads after one seed, with zero interval overview/audit polling while the stream is healthy.
- **SC-003**: 100% of tested seeded control-plane material is absent from event payloads, rendered browser output, terminal captures, logs, screenshots, and generated artifacts.
- **SC-004**: 100% of tested stream termination, credential expiry, event-gap, and schema-mismatch cases mark the affected view stale/disconnected before any further state is claimed live.
- **SC-005**: 100% of tested duplicate, old, out-of-order, unknown, and unknown-entity events produce deterministic reducer outcomes and do not crash WebUI or TUI clients.
- **SC-006**: Multiple concurrent subscribers can receive and apply the same representative event sequence without one slow subscriber blocking another or causing daemon operation failure.
- **SC-007**: Existing daemon-less WebUI/TUI smoke paths continue to pass, and surfaces clearly distinguish daemon-backed live state from daemon-less fallback.
- **SC-008**: Event catalog drift is caught by tests: removing or renaming any required field for a current panel causes a failing test rather than a silent re-fetch/polling fallback.

## Assumptions

- 007 is the follow-on product-UI slice explicitly deferred by 006: payload-driven panel state and end-to-end user-visible live-refresh verification. It does not reopen 006 daemon lifecycle/auth/background contracts unless a bug blocks the live console.
- The first release targets the current WebUI and TUI operational panels, not a full console redesign. Layout polish may happen, but the acceptance contract is live-state correctness and evidence.
- Framework choices are plan-level. No browser, terminal, desktop, or mobile UI technology is a spec requirement.
- The daemon remains explicit opt-in. Surfaces may connect to an existing daemon and may fall back when absent, but 007 does not introduce automatic daemon spawning.
- Typed session and cleanup live events require the daemon-mediated Manager
  path where `Core.Observer` is set. Standalone CLI invocations keep embedded
  `Core.Observer=nil`; they still appear through audit-tail events, but not as
  typed session/cleanup live rows.
- Environment live events in 007 cover the existing Manager environment
  stop/clean lifecycle. Environment create/recreate and run-start environment
  changes remain visible through the initial seed, manual refresh, or a later
  environment-emitter expansion.
- Export live outcomes are sourced from the existing `evidence/export/apply`
  Manager operation. Cleanup live outcomes are sourced from existing run session
  cleanup; 007 does not add a daemon session-cleanup operation class.
- Browser and terminal verification can use local deterministic harnesses. No real-Lima gate is required because 007 makes no new guest-isolation claim.
- The daemon event stream is live fan-out, not a durable event log. After restart or reconnect, clients use a new seed and resume from that point; historical replay is not part of 007.
- Local user/application data may remain visible in local UI. Anything exported or shared outside the machine still goes through the 005 export/share redaction boundary.
