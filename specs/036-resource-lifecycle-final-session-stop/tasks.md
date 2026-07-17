# Tasks: Resource Lifecycle And Final-Session Stop

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `specs/036-resource-lifecycle-final-session-stop/`

**Tests**: Required because this feature changes daemon, session, backend,
lifecycle, evidence, and cleanup boundaries.

## Phase 1: Setup

**Purpose**: Establish the package, schemas, and gate entry points without
enabling automatic stop.

- [X] T001 Create the lifecycle package skeleton in `internal/lifecycle/doc.go`
- [X] T002 [P] Add strict journal and public status schemas in `schemas/lifecycle-journal.schema.json` and `schemas/lifecycle-status.schema.json`
- [X] T003 [P] Add the local lifecycle smoke entry point in `scripts/test-lifecycle-smoke.sh`
- [X] T004 Register lifecycle schema/static checks without auto-stop behavior in `scripts/test-gate0.sh`

---

## Phase 2: Foundational Model And Backend Facts

**Purpose**: Build the shared model and fact sources required by every story.

**Critical**: No user-story implementation starts until this phase passes.

- [X] T005 [P] Write exhaustive reducer and invalid-graph tests in `internal/lifecycle/reducer_test.go`
- [X] T006 [P] Write catalog producer/drift and close-policy tests in `internal/lifecycle/catalog_test.go`
- [X] T007 [P] Write symlink, bounds, atomicity, malformed-state, and redaction tests in `internal/lifecycle/journal_test.go`
- [X] T008 [P] Write backend observation contract tests in `internal/backend/lifecycle_test.go` and `internal/backend/lima/lifecycle_test.go`
- [X] T009 Implement resource identities, states, edges, transitions, and the pure stop reducer in `internal/lifecycle/model.go` and `internal/lifecycle/reducer.go`
- [X] T010 Implement the closed production resource descriptor catalog in `internal/lifecycle/catalog.go`
- [X] T011 Implement the private atomic bounded discovery journal in `internal/lifecycle/journal.go`
- [X] T012 Implement the backend-neutral lifecycle observation contract in `internal/backend/lifecycle.go`
- [X] T013 Implement Lima inventory and boot-ID lifecycle observation independent of runtime verification in `internal/backend/lima/lifecycle.go`
- [X] T014 Implement the redacted derived lifecycle status model in `internal/lifecycle/status.go`

**Checkpoint**: The production reducer is exhaustively testable, the catalog is
closed, journal JSON is not authority, and Lima facts are independently typed.

---

## Phase 3: User Story 1 - Stop An Unused VM Without Deleting State (P1)

**Goal**: Stop a proved-idle preserved Lima environment after a visible
15-second grace while retaining all stable state.

**Independent Test**: the ordered real topology in
`scripts/test-lifecycle-lima-e2e.sh --real-gate2 --require-real` observes stop,
retained state, and a new boot generation on the next run.

### Tests For User Story 1

- [X] T015 [P] [US1] Add registrar generation and attach-versus-stop contract tests in `internal/lifecycle/registry_test.go`
- [X] T016 [P] [US1] Add idle deadline coalescing, cancellation, stale-timer, and 35-second stop-observation-bound tests in `internal/lifecycle/coordinator_test.go`
- [X] T017 [P] [US1] Add Manager planned-dependency-before-authority tests in `internal/manager/run_lifecycle_test.go`
- [X] T018 [P] [US1] Add one-shot retained-environment integration fixtures in `scripts/test-lifecycle-lima-e2e.sh`

### Implementation For User Story 1

- [X] T019 [US1] Implement the lifecycle registrar/handle contract in `internal/lifecycle/registry.go`
- [X] T020 [US1] Implement daemon-owned registry serialization and snapshot evaluation in `internal/lifecycle/registry.go`
- [X] T021 [US1] Implement environment start-generation and observed-incarnation journal operations under the existing transition lock in `internal/lifecycle/incarnation.go`
- [X] T022 [US1] Pass the daemon registrar through `RunServiceDependencies`, observe/reconcile attach under the environment transition, reject stopping/unknown roots, and commit each complete provider dependency subgraph before that provider's authority in `internal/manager/run_service.go`, `internal/manager/run_environment.go`, and `internal/manager/run_apply.go`
- [X] T023 [US1] Implement the 15-second incarnation-bound grace and revalidation transaction in `internal/lifecycle/coordinator.go`
- [X] T024 [US1] Implement the coordinator-owned stop callback as an observed non-destructive 35-second Lima transaction with `stopping-unknown` handling in `internal/manager/environment_lifecycle.go`
- [X] T025 [US1] Preserve retained environment, HostFS, decision, and evidence state across automatic stop in `internal/manager/run_environment.go`
- [X] T026 [US1] Compose the lifecycle coordinator, registrar, attach path, and Manager stop callback in `internal/daemon/lifecycle.go`, `internal/daemon/daemon.go`, and `internal/daemon/session_server.go`
- [X] T027 [US1] Add warm-command lifecycle overhead benchmarks in `internal/manager/run_lifecycle_benchmark_test.go`
- [X] T028 [US1] Add observation-only and shadow-predicate parity before enabling timers in `internal/daemon/lifecycle.go` and `internal/daemon/lifecycle_test.go`

**Checkpoint**: A final ordinary command stops only the exact observed VM after
grace; attach is serialized; no clean/delete/discard occurs.

---

## Phase 4: User Story 2 - Keep Independent Host Effects Alive (P1)

**Goal**: Classify host handoffs and retained objects accurately so VM stop
does not terminate or falsely pin them.

**Independent Test**: A real test-owned VS Code safe-mode process survives VM
stop, its disposable host resource remains intact, and a staged HostFS object
remains byte-identical.

### Tests For User Story 2

- [X] T029 [P] [US2] Add host-handoff, retained-overlay, decision, and evidence classification tests in `internal/lifecycle/effects_test.go`
- [X] T030 [P] [US2] Add real retained HostFS and isolated VS Code safe-mode scenarios in `scripts/test-lifecycle-lima-e2e.sh`

### Implementation For User Story 2

- [X] T031 [US2] Register direct host-app launch as bounded external-unmanaged handoff history in `internal/manager/run_dataplane.go` and `internal/lifecycle/catalog.go`
- [X] T032 [US2] Register HostFS staged objects and decision records as retained non-pinning state in `internal/manager/run_lifecycle_effects.go` and `internal/lifecycle/catalog.go`
- [X] T033 [US2] Enforce the no-managed-residual rule for result-none providers in `internal/hostcap/registry.go` and `internal/manager/run_dataplane.go`

**Checkpoint**: Origin does not imply VM dependency; independent host effects
and retained state remain visible without fictional close ownership.

---

## Phase 5: User Story 3 - Keep Required Guest Resources Alive (P1)

**Goal**: Register every current production VM dependency and drain so no live
session, bridge, provider, or support service is interrupted.

**Independent Test**: Two sessions and one run-scoped bridge keep the VM alive;
closing them one by one starts grace only after final ordered cleanup.

### Tests For User Story 3

- [X] T034 [P] [US3] Add sibling-session pin and release tests in `internal/daemon/lifecycle_sessions_test.go`
- [X] T035 [P] [US3] Add broker, HostFS read provider/grant, endpoint bridge, and network drain contract tests in `internal/manager/run_lifecycle_providers_test.go`
- [X] T036 [P] [US3] Add sibling, run-bridge, and network-drain real scenarios in `scripts/test-lifecycle-lima-e2e.sh`

### Implementation For User Story 3

- [X] T037 [US3] Register session worker, guest supervisor, and target dependencies through the production registrar in `internal/daemon/sessions.go` and `internal/manager/run_apply.go`
- [X] T038 [US3] Register and release broker, HostFS read provider, and live read-grant drains in `internal/manager/run_dataplane.go` and `internal/manager/run_lifecycle_effects.go`
- [X] T039 [US3] Register current run-scoped endpoint bridges as VM pins without promoting detached lifetime in `internal/manager/run_portbridge.go`
- [X] T040 [US3] Register the environment network service as a pre-stop drain and preserve final-owner cleanup behavior in `internal/manager/run_network.go` and `internal/manager/run_environment.go`

**Checkpoint**: Every current production VM dependency has a registrar and
probe; sibling resources survive; support services do not self-pin.

---

## Phase 6: User Story 4 - Refuse Unsafe Stop Under Uncertainty (P1)

**Goal**: Reconcile crashes and backend identity changes without re-adopting or
destroying ambiguous resources.

**Independent Test**: Daemon restart, external boot change, unknown stop, stale
owner recovery, and bounded shutdown all preserve fail-closed truth.

### Tests For User Story 4

- [X] T041 [P] [US4] Add daemon-restart journal reconciliation and fresh-grace tests in `internal/lifecycle/reconcile_test.go`
- [X] T042 [P] [US4] Add current session failed-to-orphaned migration and explicit recovery tests in `internal/manager/environment_lifecycle_concurrent_test.go` and `internal/daemon/lifecycle_test.go`
- [X] T043 [P] [US4] Add stop-success/observation-unknown tests in `internal/manager/environment_lifecycle_observed_test.go` and external boot-change tests in `internal/lifecycle/reconcile_test.go`
- [X] T044 [P] [US4] Add bounded daemon shutdown/deferred-stop tests in `internal/lifecycle/coordinator_test.go` and `internal/daemon/lifecycle_test.go`
- [X] T045 [P] [US4] Add daemon-restart, orphan-recovery, real stopped-to-new-generation, and production-path injected external-boot-change scenarios in `scripts/test-lifecycle-lima-e2e.sh` and lifecycle tests

### Implementation For User Story 4

- [X] T046 [US4] Implement provider probe dispatch and conservative restart classification in `internal/lifecycle/reconcile.go`
- [X] T047 [US4] Reconcile journal, owner locks, network state, and backend incarnation before enabling stop in `internal/daemon/lifecycle.go`
- [X] T048 [US4] Route explicit stop through the same daemon coordinator/observation serialization while preserving catalog-bounded recovery and automatic orphan rejection in `internal/daemon/lifecycle.go` and `internal/manager/environment_lifecycle.go`
- [X] T049 [US4] Implement bounded daemon shutdown that leaves the VM warm on insufficient proof/time in `internal/daemon/daemon.go` and `internal/lifecycle/coordinator.go`

**Checkpoint**: Old generations/timers/attempts have no authority; ambiguous
state blocks auto-stop; explicit recovery remains narrow and observed.

---

## Phase 7: User Story 5 - Explain Why An Environment Is Running (P2)

**Goal**: Present one redacted lifecycle classification consistently across
operator surfaces.

**Independent Test**: `scripts/test-lifecycle-smoke.sh --surfaces` constructs
each state and proves surface parity and control-material absence.

### Tests For User Story 5

- [X] T050 [P] [US5] Add strict schema and catalog-to-schema drift tests in `internal/lifecycle/status_schema_test.go`
- [X] T051 [P] [US5] Add CLI/Manager/daemon/doctor/UI parity and injected-secret redaction tests in `internal/lifecycle/status_parity_test.go` and `internal/app/app_test.go`

### Implementation For User Story 5

- [X] T052 [US5] Expose lifecycle inventory and typed events from daemon status in `internal/daemon/status.go`, `internal/daemon/server.go`, and `schemas/daemon-status.schema.json`
- [X] T053 [US5] Add compact human and machine lifecycle rendering plus doctor recovery findings through `internal/app/app.go` and the existing typed doctor report builder
- [X] T054 [US5] Add lifecycle event kinds and reducer state to the operations console in `internal/liveconsole/catalog.go` and `internal/liveconsole/reducer.go`
- [X] T055 [US5] Render pins, grace, retained state, handoffs, and orphans separately in `internal/manager/server.go` and the TUI rendering path in `internal/app/app.go`

**Checkpoint**: All surfaces agree; unknown is never displayed as stopped;
retained and independent effects are not counted as active sessions.

---

## Phase 8: Polish And Cross-Cutting Proof

**Purpose**: Close documentation, evidence, migration, and full-gate work.

- [X] T056 [P] Update lifecycle architecture and current behavior in `docs/architecture-principles.md`, `docs/privacy-run-design.md`, and `docs/STATUS.md`
- [X] T057 [P] Update lifecycle claims/non-claims and gate requirements in `docs/threat-model.md`, `docs/claim-boundaries.md`, and `docs/privacy-run-test-plan.md`
- [X] T058 [P] Update daemon/TUI/WebUI lifecycle UX in `docs/tui-webui-experience.md`
- [X] T059 Register 036 local and real-Lima proof IDs in `internal/productevidence/registry.go` and emit evidence from `scripts/test-lifecycle-smoke.sh` and `scripts/test-lifecycle-lima-e2e.sh`
- [X] T060 Add fail-closed missing-journal and current owner-state reconciliation in `internal/daemon/lifecycle_test.go`, catalog-to-schema/no-second-list checks in `internal/lifecycle/status_schema_test.go`, and docs-truth inputs in `scripts/test-lifecycle-smoke.sh`
- [X] T061 Run `go mod tidy` if imports changed, then run build, vet, gofmt, diff-check, full tests, markdownlint, and Gate 0 from `specs/036-resource-lifecycle-final-session-stop/quickstart.md`
- [X] T062 Run all real macOS arm64 Lima lifecycle scenarios, bind evidence to the exact candidate commit/runtime, and update `docs/STATUS.md` only after every required marker passes

---

## Dependencies And Execution Order

### Phase Dependencies

- Setup has no dependencies.
- Foundational depends on Setup and blocks every story.
- US1 depends on Foundational and supplies the coordinator/root transaction.
- US2 depends on Foundational and US1 registry wiring.
- US3 depends on US1 and completes production provider coverage.
- US4 depends on US1 and US3 because reconciliation must know every producer.
- US5 depends on the stable reducer and classifications from US1-US4.
- Polish depends on all stories.

### User Story Order

```text
Foundational -> US1 -> US2
                    -> US3 -> US4 -> US5 -> Polish
```

US2 and early US3 tests may proceed in parallel after US1 registry contracts
stabilize. Automatic stop remains shadow-only until US3 and US4 proof pass.

## Parallel Examples

- Foundational: T005-T008 can run in parallel before T009-T014.
- US1: T015-T018 can run in parallel; T027 can run after T022 independently of
  UI/status work.
- US2: T029 and T030 can run in parallel before T031-T033.
- US3: T034-T036 can run in parallel before provider migration T037-T040.
- US4: T041-T045 can run in parallel before reconciliation T046-T049.
- US5: T050 and T051 can run in parallel; rendering follows schema stability.
- Polish: T056-T058 can run in parallel before final docs-truth and gates.

## Implementation Strategy

1. Land model/catalog/journal/observer with no automatic side effect.
2. Add one-shot US1 registration and stop in shadow mode.
3. Classify host/retained effects, then migrate all live VM providers.
4. Prove restart/orphan/unknown behavior before enabling the 15-second timer.
5. Connect status/UI only to the stable production reducer.
6. Promote the claim only after exact-commit real Lima evidence passes.

**MVP**: US1 is the first demonstrable slice, but automatic stop is not
product-enabled until US3 provider completeness and US4 recovery proof pass.

---

## Phase 9: Code-Confronted Convergence

**Purpose**: Close readiness, retry, model-shape, evidence, and artifact gaps
found by the production-path review before claiming 036 complete.

- [X] T063 [P] Add a daemon startup test with multiple environments and one
  provider blocked beyond the readiness bound; assert authenticated status is
  available within three seconds while attach for the unreconciled environment
  cannot enter authority, per FR-029/SC-019 (partial)
- [X] T064 [P] Add coordinator/daemon tests for an authenticated same-epoch
  reconciliation retry from transient unknown to complete, including concurrent
  retry coalescing and stop/mutation refusal while reconciliation is in flight,
  per FR-030/SC-020 (missing)
- [X] T065 [P] Add catalog fixtures for host-only snapshot and guest-live
  materialization, requiring the latter's backend pin and rejecting both from
  production registration, per FR-031/SC-021 (missing)
- [X] T066 Add a current-epoch pending/reconciling fence and bounded wait surface
  to `internal/lifecycle`, persisting recovery reasons and preventing attach,
  stop, timer, or destructive mutation from bypassing it, per FR-029/FR-030
  (missing)
- [X] T067 Refactor daemon startup to publish authenticated status before
  backend probes finish and reconcile environments with bounded parallelism,
  while making each attach wait only for its environment, per FR-029/SC-019
  (contradicts)
- [X] T068 Add the parity-inventoried authenticated
  `POST /daemon/lifecycle/reconcile` endpoint and `hideout daemon reconcile`
  operator command using the same startup probe path, per FR-030/SC-020
  (missing)
- [X] T069 Split the ambiguous `host.materialization` descriptor into distinct
  closed design-ready snapshot and live-projection kinds and update schemas and
  contracts, per FR-031/SC-021 (contradicts)
- [X] T070 Replace predicate-only enumeration with a replayable bounded
  two-client/two-incarnation event-sequence explorer consuming production
  transition code, per FR-023/SC-001 (partial)
- [X] T071 Add randomized persisted-journal failure seeds and race replay output
  for attach/release/expiry/stop/restart/shutdown interleavings, per
  FR-023/SC-002 (partial)
- [X] T072 Record before/after warm `hideout run -- git status --short` latency
  with the real user-visible command and enforce the 5% or 10 ms threshold, per
  SC-010 (partial)
- [X] T073 Implement `scripts/test-lifecycle-lima-e2e.sh` with real boot-ID,
  sibling-session, bridge-close, retained disk/cache/audit/overlay,
  attach/stop-race, restart, stop-unknown, and exact observed-stop scenarios;
  keep failed-cleanup and environment-service drain claims on their
  production-path local tests, per SC-002-SC-009 and SC-013-SC-018 (missing)
- [X] T074 Register 036 local/race/real-Lima proof IDs and emit strict exact-
  commit/runtime evidence with artifact digest verification, per T059/T062
  (missing)
- [X] T075 Update current product docs and claim boundaries only from the
  resulting evidence, preserving explicit-stop truth until promotion, per
  FR-024 and T056-T058 (missing)
- [X] T076 Reconcile T015-T055 against concrete source/tests, marking only
  independently evidenced tasks complete and rewriting no historical task IDs,
  per the implementation tracking contract (partial)
- [X] T077 Run `go mod tidy`, build, vet, gofmt, diff-check, full tests,
  randomized/race lanes, markdownlint, Gate 0, and the real Lima gate; retain
  exact outputs for completion audit, per T061/T062 (missing)
- [X] T078 Keep production automatic stop disabled while T063-T077 are
  incomplete, with a test proving a configured lifecycle backend alone cannot
  enable side effects, per Decision 13 and FR-023 (contradicts)
- [X] T079 Enable production automatic stop only after T077 evidence passes,
  rerun the exact candidate gates, and bind the promoted behavior to
  `docs/STATUS.md`, per Decision 13 and FR-023 (missing)

## Requirement Coverage

- US1: FR-002, FR-006, FR-007, FR-009, FR-011-FR-014, FR-017, FR-024,
  FR-027; SC-002-SC-004, SC-009, SC-010.
- US2: FR-008, FR-014, FR-016; SC-005, SC-007.
- US3: FR-004-FR-009, FR-015, FR-025, FR-027; SC-004, SC-006, SC-013.
- US4: FR-010-FR-013, FR-018, FR-022-FR-028; SC-008, SC-009,
  SC-014-SC-018.
- US5: FR-018-FR-021; SC-011, SC-012.
- Foundational and Polish: FR-001, FR-003-FR-005, FR-019-FR-021 and all
  cross-surface/gate obligations.
