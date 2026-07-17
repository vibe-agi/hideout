<!-- markdownlint-disable MD013 -->

# Feature Specification: hideoutd Local Control-Plane Daemon

**Feature Branch**: `006-hideoutd-local-control-plane`

**Created**: 2026-07-07

**Status**: Draft

**Input**: User description: "006 = hideoutd local control-plane daemon. Hideout should provide a per-user local daemon that serves the existing typed Manager API over a guest-unreachable local transport, emits live operation/audit/environment event streams to CLI/TUI/WebUI clients under the existing local redaction rules, and owns background cleanup/status for long-running typed operations — failing closed after restart for resources it cannot prove belong to an active session, without adding new target authority, raw host execution, raw profile writes, or public network exposure."

## Clarifications

### Session 2026-07-07

- Q: What does FR-009 (surfaces consume events) deliver in 006, and what is deferred? → A: 006 delivers event-triggered refresh — the daemon exposes the stream over an authenticated transport (WebUI over a tokened loopback `EventSource`; TUI via a daemon subscription) and the surfaces re-fetch/re-render on events with no polling timer, verified at the plumbing level. Deferred to a follow-on product-UI slice: payload-driven panel updates with zero further overview reads after the seed, and end-to-end user-visible live-refresh verification (browser-driven WebUI test; TUI event-driven-render test). FR-009/SC-003/T032/T033 are scoped to the event-triggered-refresh deliverable, not "from events alone / zero further reads".
- Q: Does "unreachable from backend guests by construction" cover weak native-backend targets? → A: No — the structural claim is split. Transport placement (store-rooted, private ancestors) structurally excludes real backend guests (for example Lima VMs). A weak native-backend target shares the operator's UID, so placement is NOT an isolation boundary for it; token authentication is the sole defense there, which is the threat model's stated reason tokens exist.
- Q: How does the daemon handle confirmation-required operations, given prompt channels are out of scope? → A: The daemon API v1 fails closed for any confirmation-required operation unless the confirmation is provided by the existing CLI/WebUI flow outside any daemon prompt channel; there is no daemon-mediated prompting.
- Q: What Manager API route scope does the daemon serve in v1 (FR-005)? → A: The daemon mounts the full existing typed Manager API handler — every currently implemented plan/apply/read route — so parity is by construction; the plan enumerates a route inventory and parity matrix.
- Q: What is the source of truth for the event stream (FR-007)? → A: Live fan-out derived from Manager operation hooks plus the audit tail; no durable daemon event log. A (re)connecting client seeds initial state with one overview read then consumes events (zero steady-state polling); a restart does not replay history.
- Q: Where are unauthenticated/invalid-token refusals audited (FR-004)? → A: A daemon-local audit log (same audit format and deterministic redaction) under the daemon runtime area, not bound to any profile or session, recording the channel and refusal reason but never any client-supplied token material.
- Q: How is the daemon adopted? → A: Explicit opt-in start; no surface auto-spawns a background daemon. Daemon-less operation is unchanged.
- Q: Does the read-only token ship in v1? → A: No — v1 ships the operator token only; the read-only token is designed but deferred (the threat model marks it optional).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Run the local control plane as a daemon (Priority: P1)

Today every CLI command, TUI panel, and WebUI session starts its own embedded
Manager Core or a command-scoped local server, and tears it down when the
command ends. An operator who works with Hideout through the day wants one
per-user local control plane they can start, inspect, and stop — one that serves
the same typed Manager operations the embedded mode serves, is reachable only by
the operator on this machine (never by a guest, never by the network), and
refuses every request that does not carry a valid operator credential.

**Why this priority**: This is the substrate. The project's own UI experience
contract already names the steady state as daemon-first — surfaces consume live
state from the daemon instead of polling — and the daemon's trust contract is
already ratified in the threat model. Without the daemon there is nothing for
US2/US3 to stream from or hand background work to.

**Independent Test**: Start the daemon, confirm its transport is placed under
the operator-private runtime area and refuses unauthenticated and wrong-token
requests, complete an existing typed operation (plan and apply) through it with
a result identical to embedded mode, then stop it and confirm clean shutdown.

**Acceptance Scenarios**:

1. **Given** no daemon is running, **When** the operator starts it, **Then** exactly one daemon instance serves the local store, its status is inspectable, and a second start attempt reports the existing instance instead of racing it.
2. **Given** a running daemon, **When** a client presents no credential or a wrong credential, **Then** the request is refused, the refusal is auditable, and no operation state changes.
3. **Given** a running daemon, **When** the operator performs an existing typed operation through it (for example an environment stop/clean plan and apply), **Then** the plan, apply, and result are the same as the embedded-mode equivalent.
4. **Given** a running daemon, **When** the operator stops it, **Then** in-flight operations finish or fail closed, the transport is removed, and no orphaned control-plane process remains.

---

### User Story 2 - Live operation and audit events instead of polling (Priority: P2)

An operator watching a run, a cleanup, or an export today sees state only as
often as a surface re-polls the overview. With the daemon present, CLI/TUI/WebUI
clients subscribe to a live event stream — operation progress, environment
lifecycle, audit tail, export outcomes — and render updates as they happen. The
stream is a local, authenticated evidence surface: it follows the same local
redaction rules as local audit (control-plane material never appears; the
operator's own user/application data is shown verbatim locally).

**Why this priority**: Freshness is the visible payoff of the daemon and the
declared reason the UI contract is daemon-first ("the live tail comes from
daemon event streams"). It upgrades the existing TUI/WebUI smoke surfaces from
static snapshots to live views without redesigning them.

**Independent Test**: Subscribe a client to the stream, drive a state change
(start/finish an operation, emit an audit event), and assert the client observes
the corresponding events without issuing any polling reads; seed control-plane
material and assert it never appears in any streamed event.

**Acceptance Scenarios**:

1. **Given** a subscribed client, **When** an operation starts, progresses, and completes, **Then** the client receives corresponding events in order without polling any overview resource.
2. **Given** a subscribed client, **When** an audit event is recorded, **Then** the streamed copy carries no Hideout-minted control-plane secret while local user/application data remains verbatim.
3. **Given** the TUI or WebUI smoke surface connected to the daemon, **When** environment or run state changes, **Then** the surface refreshes on the resulting event (event-triggered re-fetch of its existing panels; no polling timer; no console redesign). Panel state driven directly from event payloads with zero further reads is deferred (FR-009 scope note).
4. **Given** a subscriber whose credential expires or is revoked mid-stream, **Then** the stream is terminated and re-subscription requires a valid credential.

---

### User Story 3 - Background ownership with fail-closed restart (Priority: P3)

Long-running typed environment operations — environment stop and environment
clean apply — today live and die with the foreground command that started them
(session cleanup is CLI-direct and not a typed Manager route, and `run/status` is
a read; both are out of v1 background scope). The operator hands the in-scope
env stop/clean work to the daemon: the daemon runs those existing typed
operations in the background, reports their status on demand, and — after a
daemon restart — refuses to silently re-adopt any live resource it cannot prove
still belongs to an active session, failing closed instead.

**Why this priority**: It completes the control plane (status and hygiene
survive individual commands) and carries the one hard trust obligation the
threat model places on restart. It depends on US1 for the daemon and US2 for
status visibility, so it lands last.

**Independent Test**: Start a background cleanup through the daemon, query its
status while running and after completion; then restart the daemon with a
fabricated stale live-resource record and assert it is failed closed (reported,
not re-adopted) rather than silently resumed.

**Acceptance Scenarios**:

1. **Given** a running daemon, **When** the operator submits an existing typed cleanup operation as background work, **Then** the operation runs under the same plan/apply semantics as its foreground equivalent and its status is queryable until completion.
2. **Given** a daemon restart, **When** the daemon finds records of live resources it cannot prove belong to an active session, **Then** those resources are failed closed with a diagnostic and an audit record — never silently re-adopted.
3. **Given** background work in progress, **When** the daemon is stopped, **Then** the work finishes or fails closed and its final status is recorded; nothing continues headless.

---

### Edge Cases

- What happens when a stale transport endpoint from a crashed daemon exists? A new start detects staleness, refuses to serve on an endpoint it cannot prove is dead, and either recovers it safely or fails closed with a diagnostic — never two live daemons on one store.
- What happens when the transport's placement preconditions are not met (world-readable ancestor, endpoint under a workspace/guest-visible path)? The daemon refuses to start; the placement rule reuses the existing store-reserved and workspace-safety guards, and is never downgraded to an unauthenticated local port.
- What happens when the daemon is absent? Every existing surface keeps working exactly as today (embedded Manager Core / command-scoped server); daemon-less operation is not degraded.
- What happens when a guest (including a weak native-backend target sharing the operator's UID) attempts to use the transport? OS peer identity alone is never sufficient — a valid token is required for every request, which is precisely why tokens exist.
- What happens to event subscribers when the daemon shuts down? Streams end with a terminal event; clients fall back to their daemon-less behavior rather than hanging.
- What happens when a stream consumer is too slow? The daemon bounds buffering per subscriber and drops/ends that subscription with an explicit signal rather than stalling operations or exhausting memory.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Local management control plane only — daemon lifecycle, a local operator-private transport, operator credentials, live event streams, and background execution of existing typed Manager operations. No new target-facing authority: no new host capability, no raw host execution, no raw profile writes, no new network exposure for guests.
- **Fail-closed behavior**: Refuse to start when transport placement preconditions fail; refuse every unauthenticated/invalid-token request; terminate streams on credential expiry; fail closed after restart for live resources not provably owned by an active session; stop means finish-or-fail-closed, never headless continuation. Daemon absence degrades to today's embedded behavior, not to an unauthenticated fallback.
- **User authority and policy**: The daemon serves the full existing Manager typed plan/apply handler as embedded mode — CLI, TUI, and WebUI remain intent-describing clients. Authentication follows the ratified daemon trust contract: an operator token (full access) in v1 (the optional read-only token is designed but deferred), no role matrices, no delegated approval. Interactive operator confirmation stays the approval mechanism, recorded in audit against the Manager-computed canonical request; the daemon fails closed for confirmation-required operations rather than prompting itself (FR-015).
- **Generality and provider scope**: A generic local control plane over the existing Manager model. No backend, agent, browser, or transport-vendor specifics become product semantics.
- **Evidence surface**: Daemon lifecycle (start/stop/auth refusal/restart fail-closed) is audited. The event stream is a new local, authenticated evidence surface governed by the existing local redaction rules — deterministic control-plane strip, operator's user/application data verbatim locally; anything leaving the machine still goes through the 005 export/share boundary.
- **Secret/redaction boundary**: Operator/read-only tokens are Hideout-minted control-plane credentials: never in the target environment, never in audit/evidence/streams/logs, and covered by the deterministic redaction contract. No streamed event may carry any control-plane secret.
- **Backend/gate expectation**: No isolation claim — this is local control-plane machinery. Gate 0 plus unit/integration tests over lifecycle, transport placement, authentication, stream redaction, parity with embedded mode, and restart fail-closed. The native harness is acceptable; no real-Lima gate is required.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Hideout MUST provide a per-user local control-plane daemon with explicit lifecycle operations (start, status, stop), serving exactly one daemon instance per store at a time; a concurrent start MUST report the existing instance rather than race it.
- **FR-002**: The daemon's primary transport MUST be an operator-private local endpoint under the store's runtime area with private ancestors, validated by the same store-reserved and workspace-safety guards that already protect the store; if placement or permission preconditions fail, the daemon MUST refuse to start. This placement structurally excludes real backend guests (for example Lima VMs) by construction. It MUST NOT be relied on as an isolation boundary against a weak native-backend target that shares the operator's UID: for that case token authentication (FR-004) is the sole defense, consistent with native being a weak harness.
- **FR-003**: Local loopback HTTP MAY be used only as a short-lived, explicitly tokened UI transport; the daemon MUST NOT expose any unauthenticated API on loopback and MUST NOT bind any non-local address.
- **FR-004**: Every daemon request MUST be authenticated with an operator token; OS peer identity alone MUST NOT be sufficient. Unauthenticated or invalid-token requests MUST be refused and MUST be recorded in a daemon-local audit log — the same audit format and deterministic redaction as session audit, bound to no profile or session — capturing the channel and refusal reason and never any client-supplied token material. Because the reused authentication path refuses without auditing, the daemon MUST record refusals through a wrapper around the served handler that observes the refusal outcome without altering the response or reading token material. (An optional read-only token is designed but out of v1; see Assumptions.)
- **FR-005**: The daemon MUST serve the full existing typed Manager API handler as a parity-locked subrouter — every currently implemented plan/apply/read route — so behavior parity (operations, validators, results) with embedded mode holds by construction. Within that Manager subrouter it MUST NOT add, rename, or gate routes, add new operation classes, raw profile writes, or host execution. The plan MUST enumerate the served route inventory and its parity matrix.
- **FR-016**: The daemon's own lifecycle/observability/control endpoints — status/inventory, ordered daemon stop, event subscription, background-work submission, and later typed lifecycle stop/mutation/reconciliation extensions — are a separate surface outside the Manager subrouter (all under `/daemon/…`) and MUST be inventoried explicitly. They MUST expose no raw profile write, raw VM operation, or host execution, and are subject to the same authentication and redaction rules.
- **FR-006**: When the daemon is absent, every existing surface MUST behave exactly as today (embedded Manager Core or command-scoped server); introducing the daemon MUST NOT regress or gate daemon-less operation.
- **FR-007**: The daemon MUST emit live event streams covering operation progress, environment lifecycle, the audit tail, export outcomes, and cleanup results, ordered per subscriber, so clients can render state changes without polling. Events are a live fan-out derived from Manager operation hooks plus the audit tail; the daemon MUST NOT maintain a durable event log. A (re)connecting client seeds initial state with a single overview read and then consumes events (zero steady-state polling); a restart does not replay historical events.
- **FR-008**: Streamed events MUST follow the existing local redaction rules: Hideout-minted control-plane material MUST never appear in any event; the operator's user/application data is presented verbatim on this local authenticated surface. Stream content leaving the machine remains governed by the export/share boundary.
- **FR-009**: The existing TUI and WebUI smoke surfaces MUST refresh their current panels on daemon events — event-triggered refresh, replacing timer-based polling — without a console redesign; TUI scope stays lightweight parity. The daemon exposes the stream over an authenticated local transport (the WebUI over a tokened loopback UI transport with an `EventSource`; the TUI via a daemon event subscription). Scope note: 006 delivers event-triggered refresh (on each event the surface re-fetches and re-renders; no polling timer). Updating panel state directly from event payloads with zero further overview reads after the seed, and end-to-end user-visible live-refresh verification (a browser-driven WebUI refresh; a TUI event-driven-render test), are explicitly DEFERRED to a follow-on product-UI slice (see Assumptions).
- **FR-010**: The daemon MUST accept the existing typed long-running environment operations — environment stop and environment clean apply — as background work, exposing queryable status from submission to completion with the same plan/apply semantics as foreground execution. Adding any new typed operation class (for example a typed session-cleanup operation, which is CLI-direct today and not a Manager route) is OUT of v1 scope; `run/status` is a read served through the parity-locked surface, not background work.
- **FR-011**: After a restart, the daemon MUST fail closed for any live resource it cannot prove still belongs to an active session: report and audit the orphan, never silently re-adopt or resume it.
- **FR-012**: Daemon shutdown MUST be ordered: in-flight and background operations finish or fail closed with recorded status, subscribers receive a terminal event, and the transport endpoint is removed; a stale endpoint from a crash MUST be safely recoverable or fail closed on the next start.
- **FR-013**: Subscriber buffering MUST be bounded: a slow or stuck consumer is disconnected with an explicit signal rather than stalling operations or growing memory without limit.
- **FR-014**: Interactive prompt/approval channels through the daemon are OUT of this feature's scope; operator approval remains the existing interactive confirmation recorded in audit against the Manager-computed canonical request.
- **FR-015**: The daemon API v1 MUST fail closed for any confirmation-required operation unless the confirmation is supplied by the existing CLI/WebUI flow outside any daemon prompt channel. The daemon MUST NOT prompt for or mediate approval itself, and MUST NOT treat the absence of a prompt channel as implicit approval.

### Key Entities *(include if feature involves data)*

- **Daemon Instance**: The single per-store local control-plane process; owns the transport, credentials, event fan-out, and background work; explicit lifecycle; part of the local management trusted computing base when enabled.
- **Local Transport**: The operator-private endpoint (store-runtime placement with private ancestors) — structurally guest-unreachable for real backends (for example Lima) and token-protected for a weak native target that shares the operator's UID — plus the optional short-lived tokened loopback UI channel. Placement preconditions are validated by existing store guards.
- **Operator Credential**: The operator token (full access), required on every request — a Hideout-minted control-plane secret under the deterministic redaction contract. The optional read-only token is designed but out of v1.
- **Daemon Audit Log**: A daemon-local audit surface under the daemon runtime area, bound to no profile or session, using the same audit format and deterministic redaction. It records lifecycle and authentication events (including unauthenticated/invalid-token refusals with channel and reason) and never any client-supplied token material.
- **Event Stream**: The ordered, per-subscriber live feed of operation/environment/audit/export/cleanup events; a local authenticated evidence surface under local redaction rules; bounded buffering per subscriber.
- **Background Operation**: An existing typed Manager operation submitted to the daemon for background execution; carries queryable status from submission to terminal state.
- **Live Resource Record**: The daemon's knowledge of a resource tied to an active session; after restart, any record not provably tied to an active session is failed closed.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of daemon requests without a valid operator token are refused with no state change, and each refusal is recorded in the daemon-local audit log with its channel and reason and with no client-supplied token material present.
- **SC-002**: An operator can start the daemon, complete an existing typed operation through it, and stop it in a single flow; the operation's plan, apply, and result are identical to the embedded-mode equivalent (behavior parity).
- **SC-003**: With the daemon present, a subscribed client receives 100% of tested operation/environment state-change events over the stream without polling for them, and the surfaces refresh on those events with no polling timer. (Panel state driven directly from event payloads with zero further overview reads is deferred — see FR-009 scope note.)
- **SC-004**: 100% of seeded control-plane material is absent from streamed events, while local user/application data remains verbatim.
- **SC-005**: 100% of restart scenarios with unprovable live resources fail closed with a diagnostic and audit record; zero silent re-adoptions.
- **SC-006**: 0 regressions to daemon-less operation: the existing CLI/TUI/WebUI flows pass unchanged with no daemon running.
- **SC-007**: 100% of tested transport-placement violations (guest-visible path, non-private ancestor) prevent the daemon from starting.
- **SC-008**: A background cleanup submitted to the daemon reports status transitions through completion, and daemon shutdown leaves zero headless work and zero live transport endpoints.
- **SC-009**: 100% of tested confirmation-required operations submitted to the daemon without CLI/WebUI-supplied confirmation fail closed — the daemon neither prompts nor treats a missing prompt channel as approval.
- **SC-010**: A (re)connecting stream client reaches a correct current view using at most one overview read followed by events, with zero further polling during the steady-state window, and a restart replays no historical events.

## Assumptions

- **Ratified trust contract reused**: The daemon's security shape is already fixed by the threat model's daemon contract (store-rooted transport with private ancestors reusing existing store guards — structurally guest-unreachable for real backends, token-protected for a weak native target that shares the operator's UID; loopback only as short-lived tokened UI transport; operator token + optional read-only token with no role matrices; OS peer identity insufficient alone; restart fail-closed for unprovable live resources). This spec productizes that contract; it does not renegotiate it.
- **Daemon-first is the declared steady state**: The TUI/WebUI experience contract states surfaces consume daemon event streams for live state instead of polling. 006 delivers the daemon substrate, the event stream, and the surfaces' consumption wiring at the event-triggered-refresh level (verified at the plumbing level). A fuller operations console is a follow-on (007 candidate), not part of 006.
- **Deferred to a follow-on product-UI slice (resolved, Clarifications 2026-07-07)**: two things are explicitly OUT of 006 scope and NOT claimed complete: (1) payload-driven panel updates — surfaces building panel state from event payloads with zero further overview reads after the seed (006 does event-triggered re-fetch instead); and (2) end-to-end, user-visible live-refresh verification — a browser-driven WebUI refresh test and a TUI event-driven-render test. 006 verifies the consumption wiring at the plumbing level (transport reachability, `EventSource` present in the served WebUI, `SubscribeEvents` signal delivery), not the delivered UX.
- **Daemon is opt-in (resolved, Clarifications 2026-07-07)**: The operator starts it explicitly; no surface auto-spawns a background daemon, and daemon-less operation is unchanged. Rationale: prosumer single-operator scope, no surprise persistent processes, fail-closed bias.
- **Read-only token deferred (resolved, Clarifications 2026-07-07)**: v1 ships the operator token only; the read-only token is designed but out of v1 (the threat model marks it optional). This narrows the v1 auth surface to a single credential class.
- **Event catalog v1**: operation progress, environment lifecycle, audit tail, export outcomes, cleanup results — matching what the existing surfaces already render; the exact event schema is a plan-level concern.
- **Prompt channels out of scope**: STATUS lists prompt channels among eventual daemon uses; 006 explicitly defers them (FR-014).
- **No isolation claim**: control-plane machinery only — provable by Gate 0 plus unit/integration tests (lifecycle, placement, auth, stream redaction, parity, restart fail-closed) with no real-Lima gate.
- **Docs to update on implementation**: `docs/STATUS.md` (hideoutd from design-ready to implemented, TUI/WebUI rows gain daemon-backed freshness), `docs/threat-model.md` (daemon moves from "when enabled" design text to shipped surface if wording changes), `docs/tui-webui-experience.md` (steady-state daemon-first becomes current), `docs/manager-control-plane.md` and `docs/privacy-run-test-plan.md` (daemon lifecycle/auth/stream gates), per the constitution's Development Workflow.
