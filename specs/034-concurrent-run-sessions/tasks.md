# Tasks: Daemon-Owned Concurrent Run Sessions

<!-- markdownlint-disable MD013 MD060 -->

**Input**: Design documents from `specs/034-concurrent-run-sessions/`

**Tests**: Required. This feature crosses authentication, lifecycle, terminal,
backend, process, HostFS, network, audit, and evidence boundaries. Story tests
precede implementation, and real PTY/Lima evidence is mandatory.

**Organization**: Tasks are grouped by user story. The old 034 implementation
is a baseline, not completion evidence for the daemon-owned contract.

**Implementation note**: The final implementation consolidates the planned
`terminal_client.go` into `internal/app/run_client.go`, the planned Lima
`supervisor.go` into `internal/backend/lima/session_stream.go`, and renewal,
ownership, and end-to-end fixtures into the corresponding session client,
lifecycle, Manager, and real Gate 2 test files. Task completion follows the
delivered behavior and tests rather than requiring placeholder filenames.

## Phase 1: Setup

**Purpose**: Add the protocol/helper structure and strict schemas without
changing executable run ownership.

- [X] T001 Add the `internal/sessionwire/` package skeleton and closed frame/payload type catalog in `internal/sessionwire/frame.go` and `internal/sessionwire/control.go`
- [X] T002 [P] Add Linux/unsupported command skeletons for the fixed guest helper in `cmd/hideout-session-supervisor/main_linux.go` and `cmd/hideout-session-supervisor/main_other.go`
- [X] T003 [P] Extend helper path/build/manifest fixtures for `hideout-session-supervisor` in `internal/helperbin/helperbin.go` and `internal/helperbin/helperbin_test.go`
- [X] T004 [P] Add daemon session inventory fields to `schemas/daemon-status.schema.json` and active-session summary constraints to `schemas/active-session-summary.schema.json`
- [X] T005 Add a non-HTTP daemon transport inventory for the private session socket in `internal/daemon/session_transport.go`, `internal/daemon/session_transport_test.go`, and `docs/manager-control-plane.md`; do not add it to the HTTP route catalog

---

## Phase 2: Foundational

**Purpose**: Implement shared protocol, auth, request, and helper distribution
primitives required by every story.

**Critical**: No user-story implementation starts before this phase is green.

- [X] T006 [P] Add malformed, oversized, wrong-direction, duplicate-terminal, unknown-mandatory, high-bit-extension, and arbitrary-binary frame tests in `internal/sessionwire/frame_test.go` and `internal/sessionwire/stream_test.go`
- [X] T007 Implement bounded length-prefixed framing, strict JSON controls, serialized writes, and direction validation in `internal/sessionwire/frame.go`, `internal/sessionwire/control.go`, and `internal/sessionwire/stream.go`
- [X] T008 [P] Add rotating-current/prior-grace/atomic-file/auth-callback tests in `internal/daemon/credential_test.go` and `internal/manager/api_test.go`
- [X] T009 Add callback-based Manager authentication while preserving static API fixtures in `internal/manager/api.go`, then implement atomic credential rotation in `internal/daemon/credential.go` and `internal/daemon/token.go`
- [X] T010 [P] Add strict full-option run request validation and CLI/API parity tests in `internal/manager/run_service_test.go` and `internal/manager/api_test.go`
- [X] T011 Define the canonical structured run request, terminal descriptor, confirmation binding, and stream-neutral run dependencies in `internal/manager/run_service.go` and extend `internal/manager/api.go`
- [X] T012 Materialize and verify the supervisor helper with no workspace/profile fallback in `internal/manager/run_dataplane.go`, `internal/helperbin/helperbin.go`, and their tests
- [X] T013 Add supervisor helper build/install/package/checksum entries in `scripts/install-local.sh`, `scripts/package-local.sh`, `scripts/test-install-smoke.sh`, and `packaging/homebrew/hideout.rb`
- [X] T014 Run `go mod tidy` after introducing the proven PTY dependency and verify no unrelated module churn in `go.mod` and `go.sum`

**Checkpoint**: Wire, credentials, canonical request shape, and verified guest
helper distribution are independently green.

---

## Phase 3: User Story 1 - Run Through One Resident Control Plane (Priority: P1)

**Goal**: Every executable run auto-connects to one daemon owner with no
embedded fallback while preserving Manager planning/confirmation and exact
non-interactive behavior.

**Independent Test**: Start with no daemon, run two concurrent non-interactive
commands, and prove one daemon owns both while stdout/stderr/exit and
confirmation remain correct.

### Tests For User Story 1

- [X] T015 [P] [US1] Add concurrent daemon auto-start, stale socket, readiness timeout, auth refusal, and no-embedded-fallback tests in `internal/daemon/autostart_test.go` and `internal/app/app_test.go`
- [X] T016 [P] [US1] Add one-connection/one-run handshake, strict request, and disconnect-before-start tests in `internal/daemon/session_server_test.go` and `internal/daemon/session_client_test.go`
- [X] T017 [P] [US1] Add byte-exact stdout/stderr, zero/nonzero/signal exit, cleanup-error override, Manager HTTP parity, and HTTP stale-credential cancellation tests in `internal/daemon/session_e2e_test.go` and `internal/manager/run_service_test.go`
- [X] T018 [P] [US1] Add review/digest/accept/stale-plan/non-interactive-default-deny tests in `internal/manager/run_service_test.go` and `internal/daemon/session_server_test.go`

### Implementation For User Story 1

- [X] T019 [US1] Implement race-safe detached daemon auto-start and bounded readiness in `internal/daemon/autostart.go`, with the main executable entering an internal serving role through `internal/app/app.go`
- [X] T020 [US1] Bind a second 0600 Unix listener and ordered close/remove behavior in `internal/daemon/session_transport.go`, `internal/daemon/transport.go`, and `internal/daemon/daemon.go`
- [X] T021 [US1] Implement authenticated one-connection/one-run server state and bounded stream pumps in `internal/daemon/session_server.go` and `internal/daemon/sessions.go`
- [X] T022 [US1] Implement the host-local session client handshake, request, stream, completion, and typed remote exit mapping in `internal/daemon/session_client.go`
- [X] T023 [US1] Move plan/apply/confirmation orchestration into the canonical Manager service and delegate HTTP run apply to it in `internal/manager/run_service.go`, `internal/manager/api.go`, and `internal/manager/run_apply.go`
- [X] T024 [US1] Convert executable `hideout run` into a daemon client while keeping `explain` non-executable and preserving every existing run flag in `internal/app/app.go`
- [X] T025 [US1] Return exact target exit codes from `app.Main` and keep control-plane failures distinct in `internal/app/app.go` and `internal/app/app_test.go`
- [X] T026 [US1] Update explicit daemon start/status/stop behavior for background serving and one-store ownership in `internal/app/app.go`, `internal/daemon/client.go`, and `internal/daemon/daemon.go`

**Checkpoint**: Non-interactive executable runs are daemon-only, parity-locked,
and independently usable before PTY work.

---

## Phase 4: User Story 2 - Use Interactive Tools Like Local Commands (Priority: P1)

**Goal**: A real PTY is owned inside the guest supervisor with correct initial
size, dynamic resize, signals/input/EOF, terminal restoration, and warm p95 at
or below two seconds.

**Independent Test**: Run a real PTY fixture and full-screen application,
resize it, interrupt it, and verify dimensions, single delivery, exact exit,
terminal restoration, and latency.

### Tests For User Story 2

- [X] T027 [P] [US2] Add strict supervisor start, PTY/non-PTY, resize, signal, EOF, process-group, transport-loss, and reaping tests in `cmd/hideout-session-supervisor/main_linux_test.go`
- [X] T028 [P] [US2] Add non-PTY SSH launch, no `RequestPty`, supervisor protocol, cleanup proof, and command-not-found tests in `internal/backend/lima/supervisor_test.go` and `internal/backend/lima/session_view_test.go`
- [X] T029 [P] [US2] Add terminal auto/always/never, raw-mode restore, SIGWINCH, Ctrl-C de-duplication, daemon loss, and non-file fallback tests in `internal/app/terminal_client_test.go`
- [X] T030 [P] [US2] Add a real local PTY harness that fails the old pipe timing shortcut in `scripts/test-daemon-session-pty.sh`

### Implementation For User Story 2

- [X] T031 [US2] Implement the fixed Linux supervisor with PTY allocation, initial/dynamic size, non-root target exec, process-group signals, pipe mode, and typed completion in `cmd/hideout-session-supervisor/main_linux.go`
- [X] T032 [US2] Replace the isolated SSH `RequestPty`/guardian target path with one non-PTY supervisor bridge in `internal/backend/lima/supervisor.go` and `internal/backend/lima/session_view.go`
- [X] T033 [US2] Add backend stream/control abstractions without embedding terminal file descriptors in Manager in `internal/backend/backend.go`, `internal/backend/lima/lima.go`, and `internal/backend/native/native.go`
- [X] T034 [US2] Implement client terminal mode resolution, raw state, stdin/EOF, SIGWINCH resize, non-duplicate signal handling, and guaranteed restore in `internal/app/terminal_client.go`
- [X] T035 [US2] Wire daemon client/server terminal and supervisor frame pumps with bounded backpressure in `internal/daemon/session_client.go`, `internal/daemon/session_server.go`, and `internal/backend/lima/supervisor.go`
- [X] T036 [US2] Preserve validated TERM only, reject ambient terminal identity forwarding, and document the 037 theme/OSC boundary in `internal/sessionwire/control.go` and `docs/claim-boundaries.md`

**Checkpoint**: Bash and a full-screen fixture are usable through the complete
C/S path; non-PTY automation remains exact.

---

## Phase 5: User Story 3 - Run Multiple Sessions In One Workspace (Priority: P1)

**Goal**: Multiple daemon workers reuse one existing workspace-pinned
environment while retaining distinct runtime/process/terminal state.

**Independent Test**: Run a shell, agent-like fixture, and one-shot command in
one environment, exchange a workspace file, and close one without affecting
the others.

### Tests For User Story 3

- [X] T037 [P] [US3] Add daemon worker-registry capacity, distinct connection/session identity, concurrent startup, and sibling survival tests in `internal/daemon/sessions_test.go`
- [X] T038 [P] [US3] Extend Manager same-workspace concurrent activation/transition/finish race tests for one daemon Core in `internal/manager/concurrent_run_test.go` and `internal/manager/environment_lifecycle_concurrent_test.go`
- [X] T039 [P] [US3] Add a three-client workspace collaboration and independent terminal stream smoke in `scripts/test-concurrent-sessions-e2e.sh`

### Implementation For User Story 3

- [X] T040 [US3] Make the daemon worker registry the only live run scheduler and enforce bounded session capacity in `internal/daemon/sessions.go` and `internal/daemon/daemon.go`
- [X] T041 [US3] Route all worker attach/start/finish operations through one daemon Core while retaining short environment transition locks in `internal/manager/run_service.go`, `internal/manager/run_apply.go`, and `internal/manager/run_environment.go`
- [X] T042 [US3] Preserve unique runtime children and owner locks without CLI-process ownership assumptions in `internal/manager/run_session.go`, `internal/environment/environment.go`, and `internal/session/ownership.go`
- [X] T043 [US3] Preserve shared direct workspace transport and compatible environment network service lifetime across siblings in `internal/backend/lima/lima.go` and `internal/manager/run_network.go`

**Checkpoint**: Three simultaneous sessions use one environment and shared
workspace without stream or lifecycle interference.

---

## Phase 6: User Story 4 - Keep Session Authority Separate (Priority: P1)

**Goal**: Daemon residency and VM reuse do not turn per-run authority into
daemon-global, sibling-visible, or guest-ambient state.

**Independent Test**: Give authority only to session A, probe from session B,
and prove zero sibling credentials/control state and no staged host mutation.

### Tests For User Story 4

- [X] T044 [P] [US4] Add cross-session broker/token/HostFS/read-decision/staged-write/host-app/network isolation tests in `internal/manager/run_concurrency_redaction_test.go`, `internal/manager/hostfs_concurrent_test.go`, and `internal/manager/run_dataplane_host_app_test.go`
- [X] T045 [P] [US4] Add ordinary-target sibling mount/PID/proc/descriptor/control-path probes and guest-root non-claim fixtures in `internal/backend/lima/session_view_test.go` and `scripts/lib/gate2-concurrent-sessions.sh`
- [X] T046 [P] [US4] Inject real token/proxy/setup/path fixtures and assert absence from frames/status/events/audit/evidence in `internal/daemon/session_server_test.go`, `internal/daemon/daemon_test.go`, and `internal/manager/run_concurrency_redaction_test.go`

### Implementation For User Story 4

- [X] T047 [US4] Keep every broker, HostFS provider, staged-write store, endpoint lease, host-app grant, audit writer, and runtime child inside one worker lifecycle in `internal/manager/run_dataplane.go` and `internal/daemon/session_server.go`
- [X] T048 [US4] Validate fixed session source, boot identity, target user, namespace view, and sibling-invisible `/proc` before authority in `internal/backend/lima/session_view.go` and `cmd/hideout-session-supervisor/main_linux.go`
- [X] T049 [US4] Ensure public session/status/event models expose no raw control paths or reusable credentials in `internal/daemon/status.go`, `internal/manager/manager.go`, and the strict schemas
- [X] T050 [US4] Update threat and claim boundaries for ordinary-target isolation, shared workspace, and guest-root non-containment in `docs/threat-model.md` and `docs/claim-boundaries.md`

**Checkpoint**: Sibling authority probes are zero and the only shared mutation
surface is the selected workspace.

---

## Phase 7: User Story 5 - Recover Ownership Truthfully (Priority: P2)

**Goal**: Status, stop refusal, client/daemon loss, credential renewal, and
restart recovery derive from one truthful owner model.

**Independent Test**: Observe two sessions, refuse stop, kill a client, rotate
credentials, kill the daemon, restart, and verify bounded cleanup plus
fail-closed orphan reporting.

### Tests For User Story 5

- [X] T051 [P] [US5] Add current/prior/stale session renewal and multi-rotation clock tests in `internal/daemon/session_renewal_test.go`
- [X] T052 [P] [US5] Add stop-vs-register, client EOF, daemon ordered stop, daemon crash socket loss, and bounded worker drain tests in `internal/daemon/session_lifecycle_test.go`
- [X] T053 [P] [US5] Add restart stale/unproved-owner, no-adopt/no-delete, explicit recovery, and exact-session cleanup tests in `internal/daemon/ownership_test.go` and `internal/session/ownership_test.go`
- [X] T054 [P] [US5] Add CLI/Manager/daemon-event/doctor status parity and redaction tests in `internal/daemon/status_test.go`, `internal/manager/api_test.go`, and `internal/app/app_test.go`

### Implementation For User Story 5

- [X] T055 [US5] Add session lease deadlines and renewal frames tied to rotating operator credentials in `internal/daemon/session_server.go`, `internal/daemon/session_client.go`, and `internal/daemon/credential.go`
- [X] T056 [US5] Cancel and drain workers in strict order on client loss and daemon stop, with supervisor transport closure and bounded waits, in `internal/daemon/sessions.go` and `internal/daemon/daemon.go`
- [X] T057 [US5] Reconcile durable owner records on daemon start without automatic adoption/deletion in `internal/daemon/ownership.go`, `internal/session/ownership.go`, and `internal/manager/run_environment.go`
- [X] T058 [US5] Serialize active-session registration against environment stop and keep final-session auto-stop forbidden in `internal/manager/environment_lifecycle.go`, `internal/manager/run_service.go`, and `internal/daemon/background.go`
- [X] T059 [US5] Publish redacted active worker/session/recovery state through daemon status/events, Manager overview/run status, and doctor in `internal/daemon/status.go`, `internal/daemon/events.go`, `internal/manager/manager.go`, and `internal/app/app.go`

**Checkpoint**: Loss and rotation are bounded, stop is truthful, and restart
never turns ambiguous metadata into authority or deletion proof.

---

## Phase 8: Polish And Cross-Cutting Verification

**Purpose**: Product documentation, packaging, real evidence, and adversarial
review after all stories are independently green.

- [X] T060 [P] Update architecture and lifecycle documentation for the thin client/daemon/supervisor split in `docs/architecture-principles.md`, `docs/privacy-run-design.md`, and `docs/manager-control-plane.md`
- [X] T061 [P] Update README/status/support matrix with only proved 034 behavior and explicit 035/036/037 boundaries in `README.md`, `README.zh-CN.md`, `docs/STATUS.md`, and `docs/support-matrix.md`
- [X] T062 [P] Update terminal/concurrency/crash test contracts and evidence mapping in `docs/privacy-run-test-plan.md` and `specs/034-concurrent-run-sessions/quickstart.md`
- [X] T063 Add daemon session, helper package, and local real-PTY smoke to `scripts/test-daemon-session-smoke.sh`, `scripts/test-gate0.sh`, and release package fixtures
- [X] T064 Extend real macOS arm64 Lima Gate 2 to prove 20-sample p95, resize, two PTYs, non-PTY separation, sibling authority, client kill, daemon kill, and terminal restoration in `scripts/lib/gate2-concurrent-sessions.sh`
- [X] T065 Register stable 034 evidence IDs and docs-truth claim mappings in `internal/productevidence/registry.go`, `internal/productevidence/registry_test.go`, and `scripts/test-doc-truth-smoke.sh`
- [X] T066 Run `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, `go test -race` on session/daemon/manager/backend packages, `go test ./...`, markdownlint, package smoke, Gate 0, and local PTY smoke; record exact exits in the 034 evidence output
- [X] T067 Run the real macOS arm64 Lima/PTY lane, validate evidence digests and dirty provenance, then perform an adversarial code/doc review for embedded fallbacks, output loss, secret exposure, fixation tests, and overclaims before marking `spec.md` Implemented

---

## Dependencies And Execution Order

### Phase Dependencies

- Setup (Phase 1) has no dependencies.
- Foundational (Phase 2) depends on Setup and blocks every story.
- US1 depends on Foundational and establishes the mandatory daemon path.
- US2 depends on US1 session transport; its helper tests can begin after
  Foundational.
- US3 depends on US1; it can proceed in parallel with late US2 work except for
  real PTY integration.
- US4 depends on US1 and the session-view portion of US2; model tests can run in
  parallel with US3.
- US5 depends on US1 worker ownership and integrates all completed stories.
- Polish/evidence depends on all stories.

### User Story Dependency Graph

```text
Setup -> Foundational -> US1 -> US2 -> US4 -> US5 -> Polish
                         |      |
                         +----> US3 -----------+
```

### Parallel Opportunities

- Setup tasks T002-T004 touch independent helper/schema files.
- Foundational protocol, credential, and run-request tests T006/T008/T010 can
  proceed independently.
- Each story's `[P]` tests can be written concurrently before implementation.
- US3 Manager race tests and US4 authority fixture work can overlap after US1.
- Documentation tasks T060-T062 can proceed in parallel after behavior freezes.

## Parallel Examples

### User Story 1

```text
T015 auto-start/no-fallback tests
T016 session handshake tests
T017 stream/exit parity tests
T018 confirmation binding tests
```

### User Story 2

```text
T027 guest supervisor tests
T028 Lima non-PTY bridge tests
T029 host terminal client tests
T030 real PTY harness
```

### User Story 3

```text
T037 daemon registry races
T038 Manager environment races
T039 three-client E2E
```

### User Story 4

```text
T044 provider isolation tests
T045 guest sibling probes
T046 redaction injection tests
```

### User Story 5

```text
T051 credential renewal tests
T052 lifecycle loss/race tests
T053 restart ownership tests
T054 status parity tests
```

## Implementation Strategy

### MVP

Phases 1-3 deliver the first independently usable increment: every
non-interactive executable run uses one auto-started daemon with exact streams,
exit status, confirmation binding, and no embedded fallback.

### Product Completion

1. Add guest-owned PTY/resize and meet the real-terminal latency gate (US2).
2. Prove multiple same-workspace workers and sibling survival (US3).
3. Prove per-run authority separation (US4).
4. Add rotation, crash recovery, stop refusal, and status truth (US5).
5. Run packaging, Gate 0, real PTY/Lima, docs truth, and adversarial review.

### Completion Rule

Task checkboxes become `[X]` only after the named assertion or implementation
exists and its targeted test passes. `go test ./...` alone does not prove PTY,
Lima, latency, crash, redaction, or user-visible claims.
