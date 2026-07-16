# Tasks: Concurrent Run Sessions

<!-- markdownlint-disable MD013 MD060 -->

**Input**: Design documents from `specs/034-concurrent-run-sessions/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`, `contracts/`, `quickstart.md`

**Tests**: This feature changes backend, lifecycle, filesystem, HostFS,
network, terminal, and evidence boundaries. Tests are mandatory and precede
the corresponding implementation.

**Organization**: Tasks are grouped by user story. The formal MVP is
same-workspace concurrency; cross-workspace reuse, daemon-owned auto-stop, and
complete dynamic terminal fidelity are not tasks in this file.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish strict public/state contracts and gate entry points.

- [X] T001 [P] Add strict active-session and environment-service schemas in `schemas/active-session-summary.schema.json` and `schemas/environment-service-state.schema.json`
- [X] T002 [P] Add 034 schema validation fixtures and registration tests in `internal/session/ownership_schema_test.go`, `internal/network/service_schema_test.go`, and `scripts/test-concurrent-sessions-smoke.sh`
- [X] T003 [P] Add the 034 Gate 0 and real Gate 2 script skeletons with explicit not-run evidence in `scripts/test-concurrent-sessions-smoke.sh`, `scripts/test-concurrent-sessions-e2e.sh`, and `scripts/lib/gate2-concurrent-sessions.sh`
- [X] T004 Register 034 smoke in `scripts/test-gate0.sh` without running the real Lima lane from Gate 0
- [X] T005 [P] Add stable 034 recovery-code declarations and registry coverage tests in `internal/recovery/registry.go` and `internal/recovery/registry_test.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Build the owner, lock, runtime-layout, and shared-service primitives
used by every story.

**CRITICAL**: No story implementation begins until these fail-closed models are
tested and complete.

- [X] T006 [P] Write failing live/stale/unprovable/process-death/concurrent-probe owner tests in `internal/session/ownership_test.go` (FR-010, FR-017; SC-007)
- [X] T007 Implement strict session owner records, OS-backed lease acquisition/probe/update/close, bounded errors, and redacted summaries in `internal/session/ownership.go`
- [X] T008 [P] Write failing context-cancelled transition-lock and per-session runtime-child tests in `internal/environment/environment_test.go` (FR-003, FR-004)
- [X] T009 Implement cancellable environment transition locking plus `runtime/services` and `runtime/sessions/<id>` lifecycle helpers in `internal/environment/environment.go`
- [X] T010 [P] Write failing canonical network fingerprint, secret-drift, status-state, and no-secret-serialization tests in `internal/network/network_test.go` (FR-014, SC-010)
- [X] T011 Implement a secret-free environment network-service fingerprint and strict state builder in `internal/network/network.go`
- [X] T012 [P] Write failing Lima namespace-command validation tests for session IDs, target users, workdirs, env, argv, required primitives, and generic-root-command rejection in `internal/backend/lima/session_view_test.go` (FR-007, FR-017)
- [X] T013 Add backend activation/session-view fields and lifecycle seams without changing native behavior in `internal/backend/backend.go`, `internal/backend/native/native.go`, and their tests
- [X] T014 Implement the fixed root-control SSH namespace command builder, initial PTY/non-PTY stream setup, and runtime primitive probe in `internal/backend/lima/session_view.go` and `internal/backend/lima/ssh_bridge.go`
- [X] T015 Add strict schema model tests and one shared authoritative session-summary builder in `internal/manager/manager_test.go` and `internal/manager/manager.go`

**Checkpoint**: OS liveness, runtime child layout, network identity, namespace
command construction, and summary contracts exist independently of run wiring.

---

## Phase 3: User Story 1 - Run Multiple Commands In One Workspace (Priority: P1) MVP

**Goal**: Multiple commands use one existing pinned environment and direct
workspace concurrently without the environment-busy failure.

**Independent Test**: Start three overlapping runs through Manager Core with
one static workspace/environment, prove one backend instance, distinct session
runtime children, shared file effects, exact exits, and sibling survival when
one ends.

### Tests for User Story 1

- [X] T016 [P] [US1] Write failing tests that reusable sessions use unique runtime/shim children and never clear siblings in `internal/manager/run_session_test.go` (FR-004; SC-002)
- [X] T017 [P] [US1] Write a blocking fake-backend integration test for three overlapping `ApplyRun` calls, one startup, exact exits, and no busy error in `internal/manager/manager_test.go` (FR-001, FR-003; SC-001, SC-009)
- [X] T018 [P] [US1] Write stopped-environment simultaneous-start and attach-versus-finish race tests in `internal/manager/run_environment_test.go` and `internal/manager/environment_lifecycle_test.go` (FR-003; SC-006)
- [X] T019 [P] [US1] Write shared direct/tun2socks service first-owner, matching-reuse, mismatch-deny, last-owner-cleanup, and crash-reconcile tests in `internal/manager/run_network_test.go` (FR-014; SC-004)
- [X] T020 [P] [US1] Write Lima activation tests proving full first-owner verification, live-receipt warm attach, authenticated SSH proof, and zero unsafe running-instance fallback in `internal/backend/lima/lima_test.go` (FR-017; SC-012)

### Implementation for User Story 1

- [X] T021 [US1] Replace environment-global session-dir reuse with `runtime/sessions/<session-id>` materialization while preserving environment identity and the static runtime-root/workspace mounts in `internal/manager/run_session.go`, `internal/manager/run_apply.go`, and `internal/backend/lima/lima.go` (FR-002, FR-005; SC-008)
- [X] T022 [US1] Refactor backend startup into bounded activation followed by target execution so Manager can release the transition lock before target lifetime in `internal/backend/backend.go`, `internal/backend/lima/lima.go`, and `internal/backend/native/native.go`
- [X] T023 [US1] Acquire the owner under the transition lock, wait contextually for startup races, release before target, and reacquire for finish in `internal/manager/run_apply.go`
- [X] T024 [US1] Make environment status and runtime cleanup owner-aware so one exit preserves siblings and only its runtime child is removed in `internal/manager/run_environment.go` and `internal/environment/environment.go`
- [X] T025 [US1] Materialize, fingerprint, activate, reuse, and last-owner-clean the existing environment network service without a mutable refcount in `internal/manager/run_network.go`, `internal/manager/run_apply.go`, and `internal/backend/lima/lima.go`
- [X] T026 [US1] Implement the Lima mount/PID/private-proc target path with per-session `/hideout/session`, profile-user drop, command check, exact argv, and context cancellation in `internal/backend/lima/session_view.go`
- [X] T027 [US1] Implement live activation receipts and the proved-owner warm-attach fast path while retaining full checks with zero owners in `internal/backend/lima/runtime.go`, `internal/backend/lima/lima.go`, and `internal/manager/run_apply.go`
- [X] T028 [US1] Preserve independent non-TTY stdout/stderr/exit status and initial PTY dimensions/terminal restoration on the namespace path in `internal/backend/lima/session_view.go` and `internal/backend/lima/session_view_test.go`
- [X] T029 [US1] Add CLI-level overlapping-run and shared-workspace tests using the existing run application path in `internal/app/app_test.go`
- [X] T030 [US1] Complete the local 034 smoke for overlapping direct-mode sessions and no auto-stop in `scripts/test-concurrent-sessions-smoke.sh`
- [X] T031 [US1] Run the US1 package/smoke suite and fix all race, cleanup, and timing failures before starting US2

**Checkpoint**: Same-workspace concurrent direct and matching-network sessions
are usable as an independent MVP; no cross-workspace claim exists.

---

## Phase 4: User Story 2 - Keep Session Authority Separate (Priority: P1)

**Goal**: Concurrent targets share workspace files but cannot consume sibling
process/control, broker, HostFS, staged-write, network-secret, or terminal
authority.

**Independent Test**: Give authority only to A, adversarially probe from B, end
A, and prove zero sibling material plus continued B functionality on real Lima.

### Tests for User Story 2

- [X] T032 [P] [US2] Write failing namespace-view tests for private `/proc`, mount identities, sibling runtime paths, descriptors, env, and process command lines in `internal/backend/lima/session_view_test.go` (FR-007, FR-008; SC-003)
- [X] T033 [P] [US2] Write failing concurrent HostFS read/discover/staged-write isolation and sibling-cleanup tests in `internal/manager/hostfs_concurrent_test.go` (FR-006, FR-013; SC-004, SC-005)
- [X] T034 [P] [US2] Write failing broker token, endpoint, proxy secret, machine ID, claim-token, raw lock/path, and sibling-audit redaction tests in `internal/manager/run_concurrency_redaction_test.go` (FR-008; SC-010)
- [X] T035 [P] [US2] Write an explicit guest-root non-claim fixture that distinguishes ordinary-target proof from root reachability in `internal/backend/lima/session_view_test.go` and `scripts/lib/gate2-concurrent-sessions.sh` (FR-009)
- [X] T036 [P] [US2] Write sibling interruption tests proving one cancel/host disconnect cannot signal, unmount, close a bridge, or consume the other stream in `internal/manager/manager_test.go` and `internal/backend/lima/session_view_test.go` (FR-013, FR-015; SC-004, SC-009)

### Implementation for User Story 2

- [X] T037 [US2] Start HostFS only inside the owning mount namespace and rely on namespace/process teardown rather than global `/hideout/hostfs` cleanup in `internal/backend/lima/session_view.go` and `internal/backend/lima/lima.go`
- [X] T038 [US2] Ensure every broker, shim, HostFS grant/stage, endpoint, preview, and decision remains keyed to the owning session runtime/data plane in `internal/manager/run_dataplane.go` and `internal/manager/run_apply.go`
- [X] T039 [US2] Make ordered cleanup aggregate per-session failures without removing sibling authority or falsely reporting environment readiness in `internal/manager/run_apply.go`, `internal/manager/run_session.go`, and `internal/backend/lima/session_view.go`
- [X] T040 [US2] Apply deterministic redaction and bounded untrusted fields to owner/service/session audit and status in `internal/manager/manager.go`, `internal/audit/redaction.go`, and `internal/manager/run_apply.go`
- [X] T041 [US2] Complete real Gate 2 cases for ordinary-target invisibility, HostFS/staged-write separation, direct-network non-interference, sibling interruption, and guest-root non-claim in `scripts/lib/gate2-concurrent-sessions.sh`; shared `tun2socks` lifecycle remains a Gate 0/backend-integration claim
- [X] T042 [US2] Run the US2 adversarial package tests plus race detector and fix every false-positive, false-negative, or test-only producer before US3

**Checkpoint**: Ordinary-target session authority separation has real Lima
evidence; guest-root remains visibly outside the claim.

---

## Phase 5: User Story 3 - Observe And Control Active Ownership (Priority: P2)

**Goal**: Operators see authoritative active owners, stop refuses live work,
stale metadata reconciles, and explicit stop succeeds after all owners exit.

**Independent Test**: Observe two owners through CLI/Manager, fail stop, kill
one host process, reconcile within one second, close the last owner, and stop
successfully without automatic stop.

### Tests for User Story 3

- [X] T043 [P] [US3] Write failing CLI/Manager/API parity tests for active owner count, identity, state, terminal mode, and no raw path/PID in `internal/manager/api_test.go` and `internal/app/app_test.go` (FR-016)
- [X] T044 [P] [US3] Write failing plan/apply tests for live-owner stop refusal, unprovable-owner refusal, stale reconciliation, attach-versus-stop race, and later success in `internal/manager/environment_lifecycle_test.go` (FR-011, FR-017; SC-006, SC-007)
- [X] T045 [P] [US3] Write doctor tests for missing namespace primitives, stale/failed owners, service conflict, and copyable recovery in `internal/doctor/doctor_test.go` (FR-016, FR-017)

### Implementation for User Story 3

- [X] T046 [US3] Extend authoritative session/environment summaries with owner state and derived active count while removing raw implementation paths from the active view in `internal/manager/manager.go`
- [X] T047 [US3] Serve the same owner model through overview and `/api/v1/run/status`, including profile/session filters and schemas, in `internal/manager/api.go` and `schemas/manager-api.schema.json`
- [X] T048 [US3] Refuse explicit stop/clean against live or unprovable owners under the transition lock and reconcile stale unlocked records in `internal/manager/environment_lifecycle.go`
- [X] T049 [US3] Render active owners and stable recovery through existing CLI/TUI/WebUI event-driven surfaces without adding polling in `internal/app/app.go` and `internal/manager/server.go`
- [X] T050 [US3] Add namespace-prerequisite, owner-health, and environment-service doctor findings using central recovery codes in `internal/doctor/doctor.go` and `internal/app/app.go`
- [X] T051 [US3] Complete smoke/API tests for explicit-stop success after final exit and positive proof that 034 never auto-stops in `scripts/test-concurrent-sessions-smoke.sh` (FR-012)

**Checkpoint**: Ownership is observable and lifecycle-safe through every
existing operator surface, while stop remains explicit.

---

## Phase 6: Polish And Cross-Cutting Concerns

**Purpose**: Bind claims to real evidence, update product truth, and verify the
complete repository.

- [X] T052 [P] Register 034 Gate 0, real-isolation, performance, and docs proof IDs/claims in `internal/productevidence/registry.go`, `internal/productevidence/registry_test.go`, and `docs/claim-boundaries.md`
- [X] T053 [P] Update concurrent-environment design and remove the obsolete single-writer claim in `docs/architecture-principles.md` and `docs/privacy-run-design.md`
- [X] T054 [P] Update A1/A2 session isolation, guest-root non-claim, shared-network scope, and stop behavior in `docs/threat-model.md` and `docs/support-matrix.md`
- [X] T055 [P] Add the exact 034 Gate 0/Gate 2 procedures, performance methodology, docs-truth rejection, and false-green refusal cases to `docs/privacy-run-test-plan.md` (SC-011)
- [X] T056 [P] Update operator status, README concurrency example, command inventory, and explicit cross-workspace/auto-stop/resize/root non-claims in `docs/STATUS.md`, `README.md`, and `docs/command-examples.json` (FR-018, SC-011)
- [X] T057 Add `unshare`, `mount`, `setpriv`, and private-proc observations to the supported runtime contract and retained-image verification in `internal/runtimecatalog/contract.json`, `internal/runtimecatalog/catalog.json`, and `runtime/developer-standard/verify-image.sh`; do not rewrite the published revision's historical `packages.txt` build input
- [X] T058 Run `scripts/test-concurrent-sessions-e2e.sh` on real macOS arm64 Lima, compare a separately built pre-034 commit and the candidate on the same ready-marker/static-workspace fixture for at least 30 measured samples, require all authority/performance assertions, and emit exact-commit/digest/dirty-aware evidence under the ignored evidence root
- [X] T059 Evaluate the 034 evidence through the product proof registry and release-readiness path; reject missing, stale, synthetic, or wrong-runtime artifacts in `internal/productevidence/` and `internal/releasecompat/`
- [X] T060 Run all 12 `quickstart.md` scenarios and an adversarial fresh-eyes audit for owner fixation, sibling cleanup, false-green namespace tests, and documentation overclaim; append any gap as a task before completion
- [X] T061 Run `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, `go test -race` for changed concurrency packages, `go test ./...`, schema/markdown/docs truth, Gate 0, 034 smoke, and the retained real Gate 2 verification
- [X] T062 Make stale-owner reconciliation fixation-resistant and two-phase: bind directory identity, reject non-directory owner entries, clean only the exact runtime child before removing proof, retain failed evidence, and block attach on unresolved owners in `internal/session/ownership.go`, `internal/manager/run_apply.go`, and adversarial tests
- [X] T063 Bind reusable environment-network state to the observed guest boot, verify live route/link/helper/resolver health before reuse, invalidate runtime service state on stop, and scrub partially prepared secret material on every early failure in `internal/network/`, `internal/backend/lima/`, and `internal/manager/run_network.go`
- [X] T064 Make session teardown kill descendants and prove it: combine kill-child namespace semantics with an exact-session, Core-owned SSH transport guardian; verify the session disappears from guest `/proc` before owner removal, strengthen the three-owner real Gate 2 namespace/forced-interruption lane, and preserve sibling runtime in `internal/backend/lima/` and `scripts/lib/gate2-concurrent-sessions.sh`
- [X] T065 Reject evidence-shaped theater by adding strict semantic validators for the exact 16 isolation assertions and recomputed performance samples, require distinct clean candidate/baseline commits and stable fixture digest, and bind 034 into the package-candidate path in `internal/productevidence/`, `internal/releasecompat/`, and release scripts
- [X] T066 Add an executable 12-scenario quickstart mapping and align docs with the actual proof partition: Gate 0 owns network/terminal integration, while real Gate 2 owns ordinary-target namespace, cleanup, sibling survival, and performance claims in `scripts/test-concurrent-sessions-quickstart.sh`, `quickstart.md`, and product docs

---

## Dependencies And Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: Starts immediately.
- **Foundational (Phase 2)**: Depends on Setup and blocks all stories.
- **US1 (Phase 3)**: Depends on Foundational and is the independently usable
  MVP.
- **US2 (Phase 4)**: Depends on the US1 namespace/runtime path but has an
  independent adversarial acceptance test.
- **US3 (Phase 5)**: Depends on the owner model and can proceed after US1;
  final UI/status integration also consumes US2 cleanup states.
- **Polish (Phase 6)**: Depends on all selected stories.

### User Story Dependencies

- **US1**: No story dependency after Foundation.
- **US2**: Uses US1's running session namespace; does not depend on US3 UI.
- **US3**: Uses foundational owner leases and US1 lifecycle; its stop tests do
  not require HostFS authority.

### Parallel Opportunities

- T001-T003 and T005 touch separate setup files.
- T006, T008, T010, and T012 are independent failing-test tasks.
- US1 test tasks T016-T020 can be written in parallel.
- US2 test tasks T032-T036 can be written in parallel.
- US3 test tasks T043-T045 can be written in parallel.
- Documentation tasks T052-T056 can proceed in parallel after behavior and
  evidence wording are stable.

## Parallel Examples

### User Story 1

```text
Task T016: unique runtime-child tests
Task T017: overlapping ApplyRun integration test
Task T018: startup/finish race tests
Task T019: shared network-service tests
Task T020: Lima activation/fast-path tests
```

### User Story 2

```text
Task T032: process/mount visibility tests
Task T033: HostFS/staged-write isolation tests
Task T034: control-plane redaction tests
Task T035: guest-root non-claim fixture
Task T036: interruption and stream isolation tests
```

### User Story 3

```text
Task T043: CLI/Manager/API summary parity
Task T044: stop and owner-race contract tests
Task T045: doctor/recovery tests
```

## Implementation Strategy

### MVP First

1. Complete Setup and Foundation.
2. Complete US1 with direct network and matching privacy-network service.
3. Run the independent three-session test and warm-attach performance gate.
4. Do not describe session authority isolation as complete until US2 real Gate
   2 passes.

### Incremental Completion

1. US1 removes the daily busy failure without changing workspace identity.
2. US2 proves per-run authority and cleanup boundaries.
3. US3 makes ownership and stop behavior understandable.
4. Polish binds the claim to exact real evidence and updates product truth.

## Notes

- Tests for authority-changing behavior must fail against the pre-034 code.
- A test that only inspects generated shell text cannot prove a namespace;
  real Gate 2 must inspect target-visible process and mount state.
- Do not add dynamic workspace mounts, daemon adoption, last-session auto-stop,
  SIGWINCH forwarding, or guest-root containment to this task list.
- If namespace primitives are absent, fail closed with recovery; never silently
  use the old global target path.
