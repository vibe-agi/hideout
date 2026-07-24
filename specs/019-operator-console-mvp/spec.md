# Feature Specification: Operator Console MVP

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `019-operator-console-mvp`

**Created**: 2026-07-09

**Status**: Implemented — the exact claim surface and non-claims live in [docs/STATUS.md](../../docs/STATUS.md)

**Input**: User description: "Continue .tmp/017-020-internal-hardening-plan.md. 019 makes existing local control-plane capabilities easier to operate from TUI/WebUI: decisions, notices, doctor findings, package/support status, environments/background work, and HostFS write approvals. It must not add new authority."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See What Needs Action (Priority: P1)

A local operator opens the WebUI console or TUI compact dashboard and sees one coherent action/status view: pending decisions, unacknowledged notices, doctor status, package/support posture, environments, and background operations.

**Why this priority**: Existing capabilities are powerful but scattered across CLI commands. External alpha users need a single local surface that tells them what needs attention without inventing new operations.

**Independent Test**: Seed Manager with pending decisions, notices, doctor report data, support/package status, environment summaries, and background operations. Render WebUI and TUI outputs and verify each panel appears with empty/loading/error states as applicable.

**Acceptance Scenarios**:

1. **Given** pending decisions and notices exist, **When** the operator opens the console, **Then** decisions and notices appear in an "Action Required" area with counts, status, and next actions.
2. **Given** no decisions or notices exist, **When** the console renders, **Then** it shows a clear empty state rather than hiding the panels.
3. **Given** package/support/doctor status is degraded or warning, **When** the console renders, **Then** the status appears as read-only context with next commands, not as an automatic repair prompt.

---

### User Story 2 - Resolve Existing Decisions From the Console (Priority: P2)

An operator can claim and resolve existing actionable decisions, including HostFS write approvals, from the console using the existing Manager decision routes and claim-token semantics.

**Why this priority**: HostFS write overlay and export/share approvals already depend on the decision center. The console should make those existing decisions usable without a CLI-only workflow.

**Independent Test**: Seed a HostFS write decision and an export/share decision. Exercise WebUI JavaScript and TUI command handlers against the existing Manager claim/resolve API, verifying claim token handling, timeout/deny states, and redacted rendering.

**Acceptance Scenarios**:

1. **Given** a pending HostFS write decision, **When** the operator approves it through the console, **Then** the console uses the existing claim/resolve path and the provider applies or refuses through Manager as before.
2. **Given** a stale token, expired decision, or already claimed decision, **When** the operator acts, **Then** the console shows the failure and does not retry with ambient authority.
3. **Given** an informational notice, **When** the operator acknowledges it, **Then** it uses the existing notice ack route and does not create an actionable decision.

---

### User Story 3 - Refresh Without Hidden Polling (Priority: P3)

An operator leaves the console open while the daemon event stream is healthy, and the UI refreshes from event payloads or explicit operator actions without hidden steady-state polling.

**Why this priority**: 006/007 established an event-driven contract. 019 must preserve that honesty while grouping more panels.

**Independent Test**: Run the existing WebUI JavaScript reducer harness and TUI watch tests with a healthy event stream. Verify no steady-state overview/audit polling timer runs; when the stream closes, documented fallback behavior is used.

**Acceptance Scenarios**:

1. **Given** the daemon stream is healthy, **When** decisions/notices/background events arrive, **Then** the console updates visible counts and panels from event payloads or a documented explicit refresh.
2. **Given** the stream closes or the daemon is absent, **When** the console remains open, **Then** it enters a stale/fallback state and may use the documented fallback path.
3. **Given** credentials expire mid-stream, **When** the console receives the terminal event, **Then** it stops stream-driven refresh and shows a re-authentication/error state.

### Edge Cases

- The console must never invent a new approval, package repair, doctor repair, environment operation, or HostFS mutation route.
- WebUI and TUI may differ visually, but they must use the same Manager/daemon facts and authority routes.
- Doctor findings are shown from an explicit operator-triggered local light doctor run or cached report; page load must not automatically run doctor.
- Package/support status is read-only in 019; package repair remains a CLI/package command.
- HostFS write approvals must not expose staged file contents beyond existing decision preview fields.
- Claim tokens and provider-private refs must never be rendered.
- Stale-token, denied, timeout, empty, loading, and error states must be visible and tested.
- Native weak-isolation/degraded statuses must remain warnings, not hidden.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Existing Manager overview, decision/notice claim/resolve/ack routes, daemon event stream, doctor report/status rendering, package/support matrix status, environment/background summaries, WebUI/TUI presentation. No new host, network, backend, HostFS, package, or script authority is introduced.
- **Fail-closed behavior**: Stale tokens, expired decisions, denied resolves, missing daemon, unavailable Manager API, malformed events, and unknown decision kinds must show errors/refusals and must not fall back to ambient authority.
- **User authority and policy**: The operator explicitly clicks/commands claim, approve, deny, acknowledge, refresh, or run light doctor. Console actions reuse existing Manager plan/apply or decision routes.
- **Generality and provider scope**: Panels use generic product concepts: decisions, notices, doctor findings, package/support status, environments, background operations. Lima/native appear only as backend facts.
- **Evidence surface**: WebUI render tests, TUI render tests, daemon event stream tests, decision/notice API tests, console smoke, and docs. No new release-gate claim.
- **Secret/redaction boundary**: Console rendering must not expose claim tokens, provider-private refs, broker/UI tokens, proxy backing values, hidden runtime credential paths, or raw staged HostFS content.
- **Backend/gate expectation**: Gate 0 plus WebUI/TUI reducer/harness tests. No real Lima gate is required because 019 adds no isolation claim.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Console MUST present pending decisions, unacknowledged notices, doctor status, package/support status, environments, and background operations in coherent WebUI panels.
- **FR-002**: TUI MUST expose the same model in compact form, even if fewer details are visible on screen.
- **FR-003**: Decisions and notices MUST appear in an action-required grouping with counts and empty states.
- **FR-004**: Console decision actions MUST use the existing Manager decision claim/resolve routes and MUST NOT create new approval authority.
- **FR-005**: Console notice actions MUST use the existing notice acknowledge route and MUST NOT convert notices into decisions.
- **FR-006**: HostFS write approvals MUST render only existing redacted decision preview fields and MUST NOT expose staged content objects.
- **FR-007**: Package/support status MUST be read-only in 019, with package repair shown only as an explicit next command.
- **FR-008**: Doctor integration MUST be explicit operator-triggered local light doctor or cached report display; console page load MUST NOT automatically run doctor.
- **FR-009**: WebUI and TUI MUST preserve the healthy-stream no-hidden-polling contract from 007; fallback polling or refresh is allowed only after stream absence/closure or explicit operator action.
- **FR-010**: Console MUST show loading, empty, error, denied, stale-token, timeout, and credential-expired states.
- **FR-011**: Console MUST redact claim tokens, provider-private refs, broker/UI tokens, proxy backing values, hidden runtime credential paths, and raw HostFS staged content.
- **FR-012**: Console tests MUST execute the real WebUI JavaScript reducer/action logic or Go TUI rendering logic, not only static source greps.
- **FR-013**: Console MUST not add new package, doctor repair, environment, HostFS, network, script, or backend authority.
- **FR-014**: Docs MUST describe what the console can act on, what remains CLI/package/doctor-only, and how stream fallback behaves.

### Key Entities *(include if feature involves data)*

- **Console Model**: Aggregated read model for decisions, notices, doctor status, package/support status, environments, background operations, stream health, and error states.
- **Action Required Item**: Decision or notice that needs operator attention, with redacted summary, allowed actions, status, timeout/stale metadata, and next actions.
- **Console Panel**: WebUI/TUI section for one product area, including loading, empty, populated, error, and denied states.
- **Console Action**: Operator-triggered claim/resolve/ack/refresh/run-light-doctor operation mapped to existing Manager/daemon endpoints.
- **Stream Health State**: Live, stale, credential-expired, disconnected, or fallback state for event-driven refresh.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: WebUI render tests show all required panels and at least one empty state, one warning/degraded state, and one action-required state.
- **SC-002**: TUI render tests show decisions/notices/doctor/package/support/environment/background summaries from the same console model.
- **SC-003**: Decision approve/deny and notice ack tests use existing Manager routes and pass without new action names.
- **SC-004**: WebUI JavaScript tests execute real console reducer/action code and fail if hidden steady-state polling is reintroduced while stream is healthy.
- **SC-005**: TUI watch tests fail if a healthy stream runs an interval polling timer.
- **SC-006**: Redaction tests find zero raw claim tokens, provider-private refs, broker/UI tokens, proxy secret values, hidden runtime paths, or staged HostFS content in console output.
- **SC-007**: Doctor run from console is explicit and absent from initial page-load tests.
- **SC-008**: Gate 0 console smoke covers WebUI panel rendering, TUI compact rendering, decision/notice action visibility, stream fallback, and redaction scan.

## Assumptions

- WebUI is the primary richer layout for 019; TUI uses the same model in compact form.
- Doctor status can be cached or explicitly triggered; automatic page-load doctor execution is deferred.
- Package repair stays in `hideout package repair`; 019 only shows status and next command.
- Existing Manager/daemon endpoints are sufficient for MVP actions.
