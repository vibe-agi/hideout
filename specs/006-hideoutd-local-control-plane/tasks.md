<!-- markdownlint-disable MD013 -->

# Tasks: hideoutd Local Control-Plane Daemon

**Input**: Design documents from `specs/006-hideoutd-local-control-plane/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/,
quickstart.md

**Tests**: REQUIRED — this feature touches Hideout authority (control-plane
lifecycle, authentication, audit/redaction evidence). Every story has a positive
and a fail-closed/redaction test before implementation.

**Organization**: Grouped by user story (US1 P1 → US2 P2 → US3 P3), each an
independently testable increment. `internal/daemon` reuses `manager.API.Handler()`
and `internal/audit`; the only change inside `manager` is a nil-default observer
seam so embedded mode is provably unchanged.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: different files, no dependencies
- **[Story]**: US1/US2/US3 for user-story tasks; none for Setup/Foundational/Polish

---

## Phase 1: Setup (Shared Infrastructure)

- [X] T001 Create the `internal/daemon` package skeleton with a doc comment stating it owns the daemon lifecycle/transport/events/background, mounts `manager.API.Handler()` as a parity-locked subrouter (no new Manager operation class, raw profile write, or host execution), and adds only its own status/event endpoints outside `/api/v1/…`, in `internal/daemon/doc.go`
- [X] T002 [P] Add `schemas/daemon-status.schema.json` (`additionalProperties:false`) for the daemon status/inventory shape (serving state, transport, background-op inventory) per contracts/daemon-transport-auth.md
- [X] T003 [P] Add `schemas/daemon-event.schema.json` (`additionalProperties:false`) for the event envelope (`kind`, optional `phase`, `seq`, redacted `payload`) per contracts/event-stream.md
- [X] T004 [P] Register both daemon schemas in the Gate 0 schema list in `scripts/test-gate0.sh`

---

## Phase 2: Foundational (Blocking Prerequisites)

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T005 Implement the store-runtime private directory + placement guard (an operator-private subdirectory of the store; refuse if an ancestor is non-private or the path is guest-visible), reusing the existing store-reserved and workspace-safety guards rather than a new mechanism, in `internal/daemon/transport.go`
- [X] T006 Implement the persistent, session-unbound daemon-local audit log (append-only across stop/restart; reuse `audit.NewFile`/`audit.Event`/`RedactDetails`; record channel + reason; never client-supplied token material) in `internal/daemon/audit.go`

**Checkpoint**: Package, schemas, placement guard, and the persistent daemon audit log exist.

---

## Phase 3: User Story 1 - Run the local control plane as a daemon (Priority: P1) 🎯 MVP

**Goal**: A per-user daemon that serves the existing typed Manager API over a
guest-unreachable store-rooted socket, authenticates every request, and has a clean
start/status/stop lifecycle — with behavior parity to embedded mode.

**Independent Test**: Start the daemon on a temp store; confirm placement, auth
refusals, a plan/apply through it identical to embedded, then a clean stop.

### Tests for User Story 1

- [X] T007 [P] [US1] Unit: single-instance lifecycle — start, inspectable status, a second start reports the existing instance (no race), ordered stop leaves no socket/lock; a stale socket/lock is reclaimed safely or fails closed, in `internal/daemon/daemon_test.go` (FR-001, FR-012, SC-007 placement in T008)
- [X] T008 [P] [US1] Unit: transport placement fails closed — a guest-visible/non-private placement prevents start; no unauthenticated API is served; no non-local bind; the native-vs-real-backend split holds (placement excludes real backends; token defends native), in `internal/daemon/transport_test.go` (FR-002, FR-003, SC-007)
- [X] T009 [P] [US1] Unit: authentication + unauth audit — no/wrong/expired/valid operator token; the invalid ones (incl. an expired token) refuse with no state change; each refusal is recorded in the daemon audit log with channel + reason and no client-supplied token material, in `internal/daemon/server_test.go` (FR-004, SC-001)
- [X] T010 [P] [US1] Unit: Manager parity drift-guard — the daemon-served Manager route set equals the 32 routes (16 POST + 16 GET incl. `audit/events` and `run/status`); one plan/apply through the daemon matches embedded, in `internal/daemon/server_test.go` (FR-005, SC-002)
- [X] T011 [P] [US1] Unit: daemon-specific endpoints are a separate surface — status/inventory lives outside `/api/v1/…`, adds no Manager op class, and is subject to the same auth, in `internal/daemon/server_test.go` (FR-016)
- [X] T012 [P] [US1] Unit: confirmation-required operation fails closed — the daemon neither prompts nor treats a missing prompt channel as approval, in `internal/daemon/server_test.go` (FR-014, FR-015, SC-009)
- [X] T013 [P] [US1] Unit: daemon-less zero regression — existing CLI/WebUI flows pass unchanged with no daemon running, in `internal/app/app_test.go` (FR-006, SC-006)

### Implementation for User Story 1

- [X] T014 [US1] Implement the Unix-socket listen under the runtime dir plus the loopback UI transport reused from `manager.StartLocalServer`, in `internal/daemon/transport.go` (depends on T005)
- [X] T015 [US1] Implement operator-token mint (reuse `manager.NewUIToken`), 0600 persist under the runtime dir, and reload, in `internal/daemon/token.go`
- [X] T016 [US1] Mount `manager.API.Handler()` over the socket bound to the same `Core` (set the served API's allowed host/origin to the accepted loopback-equivalent), and add the daemon status endpoint outside `/api/v1/…`, in `internal/daemon/server.go` and `internal/daemon/status.go`
- [X] T017 [US1] Wrap the mounted handler with an auth-refusal recorder that writes 401 refusals to the daemon audit log (T006) without altering the response or reading token material, in `internal/daemon/server.go`
- [X] T018 [US1] Implement the single-instance lock plus stale-endpoint detection (socket connect probe) so a second start reports the existing instance, in `internal/daemon/daemon.go`
- [X] T019 [US1] Implement the daemon lifecycle — start, serve, ordered stop (in-flight requests finish or fail closed; socket/lock removed) — and the status inventory, in `internal/daemon/daemon.go`
- [X] T020 [US1] Implement the confirmation-required fail-closed behavior (no daemon prompt; require CLI/WebUI-supplied confirmation), in `internal/daemon/server.go`
- [X] T021 [US1] Wire the `hideout daemon start|status|stop` CLI subcommand dispatch, in `internal/app/app.go`

**Checkpoint**: `hideout daemon start` serves the parity-locked Manager API over the store socket with auth + lifecycle; `status`/`stop` work.

---

## Phase 4: User Story 2 - Live events instead of polling (Priority: P2)

**Goal**: CLI/TUI/WebUI clients subscribe to a live, redacted event stream (operation
lifecycle + audit tail) and render updates without polling.

**Independent Test**: Subscribe, drive an operation and an audit write, and observe
ordered events with no polling after a single overview seed; control-plane material
never appears.

### Tests for User Story 2

- [X] T022 [P] [US2] Unit: live events, no polling, redacted — after one `overview` seed a subscriber receives ordered operation/audit events with zero polling; seeded control-plane material never appears while local user data is verbatim; a restart replays no history, in `internal/daemon/events_test.go` (FR-007, FR-008, SC-003, SC-004, SC-010)
- [X] T023 [P] [US2] Unit: backpressure — a slow subscriber is disconnected with a terminal event; operations and other subscribers are unaffected and daemon memory stays bounded, in `internal/daemon/events_test.go` (FR-013)
- [X] T024 [P] [US2] Unit: the event-subscribe endpoint is a separate surface outside `/api/v1/…` with the same auth and redaction, in `internal/daemon/events_test.go` (FR-016)
- [X] T025 [P] [US2] Unit: the Core event-observer seam is nil in embedded construction and emits nothing (reinforces zero regression), in `internal/manager/event_observer_test.go` (FR-006)
- [X] T026 [P] [US2] Unit: mid-stream credential invalidation — an active subscription is terminated with a terminal event when its token expires, is revoked, or is rotated; a resubscribe with the stale token is refused and audited, in `internal/daemon/events_test.go` (FR-004, spec US2 scenario 4, spec Fail-closed behavior)

### Implementation for User Story 2

- [X] T027 [US2] Add the nil-default event-observer seam on `manager.Core` (a nil observer in embedded construction; the daemon sets it), in `internal/manager/event_observer.go`
- [X] T028 [US2] Emit operation-lifecycle events (start/progress/complete/failed) around operations the daemon executes, via the observer, in `internal/daemon/events.go`
- [X] T029 [US2] Implement the audit-tail fan-out reusing `readAuditEvents` + `audit.RedactDetails` so streamed audit events are control-plane-stripped by construction, in `internal/daemon/events.go`
- [X] T030 [US2] Implement the event-subscribe endpoint (separate surface), the single-`overview`-seed-then-events protocol, and mid-stream termination when the subscriber's credential expires/revokes/rotates (terminal event; stale-token resubscribe refused and audited), in `internal/daemon/events.go` and `internal/daemon/server.go`
- [X] T031 [US2] Implement per-subscriber bounded buffering with drop-and-terminal-event on a slow consumer, in `internal/daemon/events.go`
- [X] T032 [US2] Serve the WebUI over the daemon's tokened loopback UI transport and open an `EventSource` on `/daemon/events` so the panels refresh on events (event-triggered re-fetch; no polling timer); daemon-less fallback stays the existing behavior per T013; no console redesign, in `internal/daemon/uiweb.go` and `internal/manager/server.go` (FR-009, event-triggered scope; payload-driven zero-read + UX verification deferred)
- [X] T033 [US2] Wire the TUI to consume the daemon event stream via `daemon.SubscribeEvents`, refreshing on events (event-triggered re-render) with the interval as a fallback and daemon-less polling unchanged, in `internal/app/app.go` and `internal/daemon/client.go` (FR-009, event-triggered scope; payload-driven zero-read + UX verification deferred)

**Checkpoint**: Subscribed clients see live, ordered, redacted events with no polling, and streams terminate on credential invalidation.

---

## Phase 5: User Story 3 - Background ownership with fail-closed restart (Priority: P3)

**Goal**: The daemon runs existing typed env stop/clean apply as background work with
queryable status, and after restart fails closed for live resources it cannot prove
it owns.

**Independent Test**: Submit an env clean as background work, query status through
completion; restart with a fabricated stale live-resource record and assert it is
failed closed (reported/audited), not re-adopted.

### Tests for User Story 3

- [X] T034 [P] [US3] Unit: background status — an env stop/clean apply submitted as background work transitions status through completion under the same plan/apply semantics; daemon stop leaves zero headless work and zero live endpoints, in `internal/daemon/background_test.go` (FR-010, SC-008)
- [X] T035 [P] [US3] Unit: restart fails closed for orphans — a pre-restart live-resource record without current-instance ownership is reported and audited as an orphan, not re-adopted and not destroyed, in `internal/daemon/ownership_test.go` (FR-011, SC-005)
- [X] T036 [P] [US3] Unit: no new op class — background work is env stop/clean apply only; a session-cleanup or `run/status` submission is rejected as out of v1 background scope, in `internal/daemon/background_test.go` (FR-010)

### Implementation for User Story 3

- [X] T037 [US3] Implement the background-operation registry running `Core.ApplyEnvironmentStop`/`ApplyEnvironmentClean` in daemon goroutines with queryable status (queued/running/completed/failed), in `internal/daemon/background.go`
- [X] T038 [US3] Implement the in-memory live-resource ownership set tracked since the current start (`session.ValidID`-keyed), in `internal/daemon/ownership.go`
- [X] T039 [US3] Implement restart orphan detection → report + daemon-audit record (never re-adopt, never destroy), in `internal/daemon/ownership.go`
- [X] T040 [US3] Implement ordered shutdown of background work (finish or fail closed with a recorded terminal status) and include background ops in the status inventory, in `internal/daemon/daemon.go` and `internal/daemon/status.go`

**Checkpoint**: Background env work is queryable; restart fails closed for orphans.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T041 [P] Update `docs/STATUS.md` — `hideoutd` design-ready → implemented; TUI/WebUI rows gain daemon-backed live refresh
- [X] T042 [P] Update `docs/tui-webui-experience.md` — the daemon-first steady state is current for the existing panels
- [X] T043 [P] Update `docs/threat-model.md` — daemon contract wording from "when enabled" to shipped surface
- [X] T044 [P] Update `docs/manager-control-plane.md` — daemon serving + the event surface
- [X] T045 [P] Update `docs/privacy-run-test-plan.md` — daemon lifecycle/auth/stream gates
- [X] T046 [P] Add `scripts/test-daemon-smoke.sh` exercising start → auth refusal (audited) → subscribe/event → mid-stream credential invalidation → background status → ordered stop
- [X] T047 Run the full battery: `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, `go test ./...`, `scripts/test-gate0.sh`, and the quickstart.md validation; confirm green with no real-Lima requirement

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies.
- **Foundational (Phase 2)**: depends on Setup; BLOCKS all user stories. T005 (placement) and T006 (daemon audit log) are shared primitives.
- **User Stories (Phase 3–5)**: depend on Foundational. US1 is the MVP. US2 depends on US1's serving daemon (adds the observer seam + event fan-out). US3 depends on US1 (lifecycle) and reuses US2's daemon-audit path for orphan records.
- **Polish (Phase 6)**: depends on the desired stories being complete.

### User Story Dependencies

- **US1 (P1)**: after Foundational. Independently testable (serve + auth + parity + lifecycle).
- **US2 (P2)**: builds on US1's daemon; independently testable (subscribe → live redacted events, no polling).
- **US3 (P3)**: builds on US1; independently testable (background status; restart orphan fail-closed).

### Within Each User Story

- Tests (authority/auth/redaction/fail-closed) written and failing before implementation.
- `internal/daemon` primitives (transport, token, audit) before serving; serving before lifecycle/CLI.

### Parallel Opportunities

- Setup: T002, T003, T004 in parallel.
- Each story's test tasks ([P]) run in parallel (distinct test files).
- US1 impl: T014/T015 touch different files and can parallelize; T016/T017/T020 share `server.go` and are sequential.
- US2 impl: T027 (manager seam) is independent of the daemon event files; T028–T031 share `events.go` and are sequential; T032 (WebUI) and T033 (TUI) touch different surfaces and can parallelize.
- Polish T041–T046 in parallel; T047 last.

---

## Parallel Example: User Story 1 tests

```bash
# Launch US1 tests together (distinct test files):
Task: "T007 single-instance lifecycle in internal/daemon/daemon_test.go"
Task: "T008 transport placement fail-closed in internal/daemon/transport_test.go"
Task: "T009 auth + unauth audit in internal/daemon/server_test.go"
Task: "T013 daemon-less zero regression in internal/app/app_test.go"
```

---

## Implementation Strategy

### MVP First (User Story 1)

1. Phase 1 Setup → Phase 2 Foundational (CRITICAL) → Phase 3 US1.
2. STOP and VALIDATE: `hideout daemon start` serves the parity-locked Manager API
   over the store socket with auth + lifecycle; a plan/apply matches embedded.
3. Demo: a running local control plane, token-gated, guest-unreachable, with clean
   start/status/stop.

### Incremental Delivery

1. Setup + Foundational → foundation ready.
2. US1 → the daemon serves + authenticates + parity + lifecycle (MVP).
3. US2 → live redacted event streams; surfaces consume without polling.
4. US3 → background env work + restart fail-closed.
5. Polish → docs, smoke, final validation.

---

## Notes

- [P] = different files, no dependencies.
- This feature makes a local control-plane claim, not an isolation claim: Gate 0 +
  `go test ./...` only, no real-Lima.
- Parity is by construction (mount `api.Handler()`); the daemon adds no Manager op
  class. Daemon-specific status/event endpoints live outside `/api/v1/…`.
- The Manager change is only the nil-default observer seam — embedded mode stays
  byte-for-byte unchanged (FR-006).
- Restart fail-closed means report + audit orphans, never re-adopt and never destroy.
- Update `docs/STATUS.md`, `docs/threat-model.md`, and `docs/tui-webui-experience.md`
  (Phase 6) since implemented status and the daemon-first steady state change.
