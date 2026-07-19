# Tasks: Zero-Friction Setup

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/038-zero-friction-setup/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`,
`contracts/`, `quickstart.md`

**Tests**: Required. This feature changes profile authority, daemon routing,
init plan/apply binding, first-run UI, package guidance, and release evidence.
Every story starts with behavior that fails against the pre-038 implementation.

**Organization**: Tasks are grouped by user story so each product claim has an
independent falsification path.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel because it changes separate files and has no
  dependency on an incomplete task in the same phase.
- **[Story]**: User story from `spec.md`.
- Every task names its implementation or verification path.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Register 038 evidence and prepare reusable test seams without
changing product behavior.

- [X] T001 Add feature/proof constants, `Required038ProofIDs`, and FR/SC mappings for all eight proof IDs in `internal/productevidence/setup.go` (new file; not `claims.go`), `internal/productevidence/aggregate.go`, and `internal/productevidence/registry.go`
- [X] T002 [P] Add registry completeness, target/freshness, and claim mapping tests for 038 in `internal/productevidence/registry_test.go` and `internal/productevidence/aggregate_test.go`
- [X] T003 [P] Add reusable daemon init-client HTTP fixtures and call counters in `internal/app/app_test.go` without activating setup behavior
- [X] T004 [P] Add source-versus-published Homebrew formula parity fixture covering caveats and all packaged Linux helpers in `scripts/test-first-run-docs-smoke.sh`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Establish one Manager-owned init service, exact review binding,
cross-process serialization, authenticated API, and thin client.

**CRITICAL**: No setup story can apply state until this phase is complete.

- [X] T005 [P] Write failing prepared-init model, canonical digest sensitivity, generated-value stability, and confirmation tests in `internal/manager/init_service_test.go` covering FR-010, FR-030, SC-005
- [X] T006 [P] Write failing pure setup-state classification tests for fresh, ready, repairable, blocked, malformed, symlinked, and unprovable profiles in `internal/manager/init_service_test.go` covering FR-014 through FR-016
- [X] T007 [P] Write failing cross-instance race tests proving create/change after review is rejected before mutation and the store-rooted lock is retained through apply in `internal/manager/init_service_test.go` and `internal/manager/profile_lock_test.go`
- [X] T008 [P] Write failing Manager API contract tests for prepared `init/plan`, bound `init/apply`, unknown-field rejection, options-only apply refusal, and no route-count drift in `internal/manager/api_test.go` and `internal/manager/api_routes_test.go`
- [X] T009 Implement versioned `InitServiceRequest`, `InitReview`, `PreparedInit`, `InitConfirmation`, canonical semantic projection, and SHA-256 digest in `internal/manager/init_service.go`
- [X] T010 Implement pure `fresh|ready|repairable|blocked` setup observation using strict profile reads and explicit local prerequisite observations in `internal/manager/init_service.go` without calling `LoadOrInit`
- [X] T011 Implement one lock-owning init apply path with lock-contained re-observation, stale-plan rejection, confirmation validation, and a private lock-assuming InitTask helper in `internal/manager/init_service.go` and `internal/manager/manager.go`, proving no recursive lock acquisition
- [X] T012 Replace the current init API wire behavior with `InitService.Prepare` and bound `InitService.Apply` in `internal/manager/api.go`, `internal/manager/routes.go`, and `schemas/manager-api.schema.json` without adding a route or compatibility shim
- [X] T013 [P] Write failing authenticated daemon init-client tests for plan, apply, API errors, token failure, and cancellation (delivered as `TestInitDaemonRequest*` in `internal/app/setup_test.go`; no separate `init_client_test.go`)
- [X] T014 Implement the typed authenticated init client over `daemon.DialClient` in `internal/app/init_client.go` with no embedded Manager fallback
- [X] T015 Run the foundational suites and correct all regressions in `internal/manager`, `internal/inittask`, `internal/profile`, `internal/daemon`, and `internal/app`

**Checkpoint**: Both setup and advanced init can use one bound Manager service;
no setup UI is active yet.

---

## Phase 3: User Story 1 - Prepare A Fresh Installation (Priority: P1) MVP

**Goal**: Execute one fixed setup command, one review, one confirmation, and
one configuration-only apply.

**Independent Test**: From an installed package and empty store, run
`hideout setup` in a PTY, confirm once, verify one default profile, and prove
zero VM starts and zero runtime downloads.

### Tests For User Story 1

- [X] T016 [P] [US1] Write failing exact dispatch and fixed-projection parity tests for `operatorintent.KindSetup` in `internal/app/app_test.go` and `internal/operatorintent/intent_test.go` covering FR-001, FR-002, FR-031, SC-015
- [X] T017 [P] [US1] Write failing fresh setup integration tests that observe one plan, one apply, one profile, setup evidence, no backend start, and no runtime transfer in `internal/app/app_test.go` covering FR-003, FR-005, FR-012, FR-013, FR-017

### Implementation For User Story 1

- [X] T018 [US1] Dispatch exact `hideout setup` through the existing natural intent layer and reject all setup arguments in `internal/app/app.go` and `internal/operatorintent/intent.go`
- [X] T019 [US1] Implement fixed setup request construction, authoritative plan call, one local confirmation, bound apply call, and fresh success flow in `internal/app/app.go` using `internal/app/init_client.go`
- [X] T020 [US1] Render concise configuration-ready next steps, including doctor, first run, exact tested-agent install/run, and privacy follow-up in `internal/app/app.go` without internal IDs or raw host paths
- [X] T021 [US1] Preserve existing InitTask audit/onboarding evidence and add setup operation attribution without a new schema in `internal/inittask/inittask.go` and `internal/manager/init_service.go`
- [X] T022 [US1] Validate US1 independently with targeted Go tests and a local packaged PTY execution in `scripts/test-first-run-e2e.sh`

**Checkpoint**: A fresh operator can configure Hideout without starting Lima.

---

## Phase 4: User Story 2 - Understand The Boundary Before Accepting (Priority: P1)

**Goal**: Make the fixed posture and its non-claims understandable before
approval.

**Independent Test**: Capture the real setup review and assert all material
boundary facts are visible while injected control-plane values are absent.

### Tests For User Story 2

- [X] T023 [P] [US2] Write failing table-driven review tests for Lima, future `/workspace`, read/write scope, hidden outside files, direct-network non-privacy, runtime revision/size/preview, always-on audit, and no-download wording in `internal/app/app_test.go`
- [X] T024 [P] [US2] Write failing redaction and non-claim tests with real token, proxy, machine-ID, host-username, guest-root, private-network, write-filtering, and compatibility-shaped values in `internal/app/app_test.go`

### Implementation For User Story 2

- [X] T025 [US2] Implement the stable setup review model and renderer in `internal/app/app.go`, deriving runtime facts from the prepared Manager response rather than local assumptions
- [X] T026 [US2] Validate US2 independently through captured terminal output and deterministic redaction tests in `internal/app/app_test.go`

**Checkpoint**: The operator can understand what setup does and does not prove.

---

## Phase 5: User Story 3 - Cancel Or Automate Without Surprise (Priority: P1)

**Goal**: Make every negative path default-deny and keep automation on explicit
init.

**Independent Test**: Exercise negative, empty, EOF, Ctrl-C, control-byte, and
non-TTY input and compare profile/evidence/backend/runtime state before and
after.

### Tests For User Story 3

- [X] T027 [P] [US3] Write failing PTY/input tests for negative, empty, EOF, Ctrl-C, control-byte, and non-TTY setup behavior in `internal/app/app_test.go` covering FR-005 through FR-007, SC-003
- [X] T028 [P] [US3] Write failing cancellation side-effect tests that permit bounded daemon socket/token/lock state but reject profile, identity, onboarding evidence, passing 038 evidence, VM, runtime, or new authority in `internal/app/app_test.go` and `internal/daemon/autostart_test.go`

### Implementation For User Story 3

- [X] T029 [US3] Harden setup confirmation to default No, require local TTY, handle EOF/Ctrl-C/control input, and avoid constructing apply confirmation on cancellation in `internal/app/app.go`
- [X] T030 [US3] Keep explicit non-interactive `hideout init --no-input` as the only automation path and reject setup flags in CLI usage/help in `internal/app/app.go`
- [X] T031 [US3] Validate US3 independently and prove the old implementation would have mutated or accepted at least one tested negative path in `internal/app/app_test.go`

**Checkpoint**: Convenience never becomes implicit approval.

---

## Phase 6: User Story 4 - Re-run Setup Safely (Priority: P1)

**Goal**: Make repeated setup a strict read-only readiness view.

**Independent Test**: Re-run against valid and customized profiles and compare
bytes, metadata, identities, evidence, audit, and mtimes; exercise partial and
unsafe states separately.

### Tests For User Story 4

- [X] T032 [P] [US4] Write failing byte/digest/mtime/call-count tests proving valid and customized profiles use pure reads, send no apply, and create no metadata, identity, audit, or evidence in `internal/app/app_test.go` and `internal/manager/init_service_test.go` covering SC-004
- [X] T033 [P] [US4] Write failing typed recovery tests for repairable, malformed, unsafe, partial, and conflicting state in `internal/manager/init_service_test.go`

### Implementation For User Story 4

- [X] T034 [US4] Render terminal `Already set up` with effective current posture and next steps, without confirmation or default-comparison judgment, in `internal/app/app.go`
- [X] T035 [US4] Implement repairable/blocked setup guidance without writes in `internal/manager/init_service.go` and map covered conditions through `internal/recovery/registry.go`
- [X] T036 [US4] Validate US4 independently, including a concurrent creator after review and no `LoadOrInit` calls on existing state, in `internal/manager/init_service_test.go` and `internal/app/app_test.go`

**Checkpoint**: Re-running setup cannot normalize or steal an existing profile.

---

## Phase 7: User Story 5 - Complete A Real First Run (Priority: P1)

**Goal**: Prove the packaged product reaches a useful command in real Lima with
honest runtime waiting, identity, workspace, audit, reuse, and lifecycle.

**Independent Test**: Install the candidate package, setup, run Git in a real
macOS arm64 Lima fixture, and evaluate registered Gate 2 evidence.

### Tests For User Story 5

- [X] T037 [P] [US5] Write failing Lima startup presentation tests for exact runtime family/revision/declared size, possible first-use download, bounded heartbeat, ready message, and absence of fabricated byte/percentage progress in `internal/backend/lima/lima_test.go`
- [X] T038 [P] [US5] Extend first-run harness assertions for `/workspace`, `getpwuid` account home, target `HOME`, non-root identity, runtime provenance, audit, Boundary evidence, lifecycle, and exact environment reuse in `scripts/test-first-run-e2e.sh`

### Implementation For User Story 5

- [X] T039 [US5] Pass runtime presentation facts into Lima startup and render the honest wait notice/heartbeat in `internal/backend/lima/lima.go` and its Manager/backend call sites
- [X] T040 [US5] Add the additive direct/setup real macOS arm64 lane and `038.setup.real-gate2.first-run|not-run` artifacts to `scripts/test-first-run-e2e.sh` while retaining the privacy lane
- [X] T041 [US5] Validate US5 independently in `scripts/test-first-run-e2e.sh` with the distributed candidate binary and exact runtime cache/provenance, recording failed or `not-run` instead of native fallback

**Checkpoint**: Configuration success is backed by a real first command.

---

## Phase 8: User Story 6 - Install And Run A Tested Agent (Priority: P1)

**Goal**: Prove direct networking and persistent target state can install one
exact agent and execute it by name in a later session.

**Independent Test**: Install the exact fixture under `$HOME/.local` without
root, end the session, and run the pinned version in another session.

### Tests For User Story 6

- [X] T042 [P] [US6] Assert package integrity, target ownership, no `sudo`, persistent PATH lookup, separate-session execution, and absence of auth/proxy/control-plane material (delivered by reusing the unmodified 031 fixture `scripts/test-runtime-agent-install.sh` inside the guest and asserting the separate-session run in `scripts/test-first-run-e2e.sh`)

### Implementation For User Story 6

- [X] T043 [US6] Integrate the existing exact-version agent fixture into the direct/setup real lane without teaching setup to install or authenticate it in `scripts/test-first-run-e2e.sh` and `scripts/test-runtime-agent-install.sh`
- [X] T044 [US6] Emit and register `038.setup.real-gate2.agent-install-run` evidence with exact package/runtime provenance in `scripts/test-first-run-e2e.sh`
- [X] T045 [US6] Validate US6 independently through two real Lima sessions in `scripts/test-first-run-e2e.sh` and inspect host/guest credential locations for imported state

**Checkpoint**: The named fixture is a proved compatibility case, not a generic
agent guarantee.

---

## Phase 9: User Story 7 - Recover From A Failed First Run (Priority: P2)

**Goal**: Turn first-run failures into one bounded reason and runnable action
without fallback.

**Independent Test**: Inject daemon, package, profile, disk, runtime, network,
and backend failures and verify stable recovery output plus zero hidden success.

### Tests For User Story 7

- [X] T046 [P] [US7] Write failing daemon stale-socket, build-mismatch, cold-start, readiness, authentication, and mid-operation exit tests with zero embedded fallback (delivered in `internal/app/setup_test.go` and `internal/daemon/autostart_test.go`; no separate `init_client_test.go`)
- [X] T047 [P] [US7] Write failing profile conflict, stale plan, package corruption, insufficient disk, runtime mismatch, missing Lima, and real-backend failure recovery tests in `internal/app/app_test.go` and `internal/manager/init_service_test.go`

### Implementation For User Story 7

- [X] T048 [US7] Add or reuse stable recovery records for setup/first-run covered failures in `internal/recovery/registry.go` and render one concise reason and runnable next action in `internal/app/app.go`
- [X] T049 [US7] Validate US7 independently and prove real failures produce failed or `not-run` evidence rather than local/native success in `scripts/test-first-run-e2e.sh`

**Checkpoint**: The first-run path is actionable when it cannot proceed.

---

## Phase 10: User Story 8 - Keep Advanced Initialization Consistent (Priority: P2)

**Goal**: Preserve advanced choices while eliminating the embedded init writer.

**Independent Test**: Compare fixed setup with equivalent explicit init, then
exercise different valid advanced choices through the same daemon Manager API.

### Tests For User Story 8

- [X] T050 [P] [US8] Write failing semantic parity tests between setup and equivalent explicit init plus non-equivalent advanced template/backend/network/runtime/profile choices in `internal/manager/init_service_test.go` covering FR-008, FR-029, FR-031
- [X] T051 [P] [US8] Write failing CLI tests proving normal init uses authenticated daemon plan/apply, preserves interactive collection and dry-run, and never calls embedded `manager.New` authority in `internal/app/app_test.go`

### Implementation For User Story 8

- [X] T052 [US8] Migrate normal `hideout init` plan/dry-run/confirm/apply to `internal/app/init_client.go` and remove the embedded Manager mutation path from `internal/app/app.go`
- [X] T053 [US8] Preserve all advanced init options and existing InitTask effects through `InitService` mode `init`, and correct the stale dev/debug workspace-path comment, in `internal/manager/init_service.go`, `internal/manager/api.go`, and `internal/profiletemplate/template.go`
- [X] T054 [US8] Validate US8 independently across CLI and API with semantic parity, profile-lock concurrency, audit, evidence, and recovery assertions

**Checkpoint**: Setup and init are two presentations over one authority.

---

## Phase 11: Polish And Cross-Cutting Concerns

**Purpose**: Package synchronization, documentation truth, evidence gates, and
final adversarial review.

- [X] T055 [P] Update primary setup-first product path and direct-network/runtime disclosures in `README.md`, `README.zh-CN.md`, `docs/first-run-alpha.md`, and `docs/distribution-bootstrap.md`
- [X] T056 [P] Update implemented status, support/proof rows, Manager binding, and narrow claim/non-claim language in `docs/STATUS.md`, `docs/support-matrix.md`, `docs/manager-control-plane.md`, and `docs/claim-boundaries.md`
- [X] T057 [P] Update onboarding composition and Gate 0/Gate 2 evidence requirements in `docs/privacy-run-design.md` and `docs/privacy-run-test-plan.md`
- [X] T058 [P] Replace long init caveats with `hideout setup`, synchronize the complete helper list, and enforce source/tap parity in `packaging/homebrew/hideout.rb`, `/Users/null/Code/github/vibe-agi/homebrew-tap/Formula/hideout.rb`, and `scripts/test-first-run-docs-smoke.sh`
- [X] T059 [P] Update CLI usage, package smoke, docs-truth, and gate wiring in `internal/app/app.go`, `scripts/test-package-smoke.sh`, `scripts/test-first-run-docs-smoke.sh`, and `scripts/test-gate0.sh`
- [X] T060 Generate and validate all registered 038 local/product evidence manifests with `internal/productevidence` evaluators and keep real prerequisites as `not-run` when unavailable
- [X] T061 Run markdownlint over all changed Markdown, Ruby syntax/checks for both formulas, schema validation, and `git diff --check`
- [X] T062 Run `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, and `go test ./...`
- [X] T063 Run `scripts/test-gate0.sh`, package smoke, first-run local-fast, packaged PTY, and required real macOS arm64 setup/agent Gate 2 lanes from `quickstart.md`
- [X] T064 Perform a fresh-eyes adversarial review against `spec.md`, attempt to falsify no-download, no-overwrite, plan-binding, daemon-recovery, agent-install/run, exact-reuse, and real-first-run claims, then mark `spec.md` and `tasks.md` complete only if no open finding remains

---

## Dependencies And Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependency.
- **Foundational (Phase 2)**: Depends on Phase 1 and blocks all stories.
- **US1/US2/US3/US4**: Depend on Foundational. Their tests may be authored in
  parallel; shared `app.go` implementation is serialized US1 to US4.
- **US5**: Depends on US1 configuration behavior and can otherwise proceed in
  parallel with US2 to US4.
- **US6**: Depends on US5's real direct/setup lane.
- **US7**: Depends on US1 and Foundational error surfaces; can proceed before
  real US5 execution.
- **US8**: Depends on Foundational and should land after setup behavior is
  stable so parity has a fixed reference.
- **Polish**: Depends on all selected stories and evidence producers.

### User Story Dependencies

- **US1**: First deliverable after Foundational; product MVP.
- **US2**: Uses US1's prepared review but is independently output-testable.
- **US3**: Uses US1's confirmation but independently proves zero durable state.
- **US4**: Uses the foundational state classifier; independently proves
  read-only repetition.
- **US5**: Requires US1; proves runtime behavior omitted deliberately by setup.
- **US6**: Requires US5's real lane and persistent profile state.
- **US7**: Exercises failure branches from Foundational and US1.
- **US8**: Shares the service with US1 but preserves the independent advanced
  init contract.

### Parallel Opportunities

- T002-T004 can run in parallel.
- T005-T008 and T013 are independent failing-test tasks.
- T023-T024, T027-T028, T032-T033, T037-T038, T046-T047, and T050-T051 are
  parallel test pairs.
- T055-T059 touch separate documentation, formula, and gate surfaces.
- Real Gate 2 preparation can run while local Manager/CLI tests execute, but a
  real claim is accepted only after the exact candidate commit/package exists.

## Parallel Example: Foundational

```text
Task T005: prepared-init digest and confirmation tests
Task T006: pure state-classification tests
Task T007: cross-process lock/race tests
Task T008: Manager API contract and route tests
Task T013: authenticated thin-client tests
```

## Parallel Example: First-Run Proof

```text
Task T037: Lima runtime-wait unit tests
Task T038: real-lane identity/workspace/evidence assertions
Task T042: exact agent fixture ownership/privacy assertions
```

## Implementation Strategy

### MVP First

1. Complete evidence registration and Foundational `InitService`.
2. Complete US1 and prove setup configures without VM/download.
3. Complete US2-US4 so approval, cancellation, and repeat behavior are honest.
4. Stop and run the packaged PTY lane before touching documentation claims.

### Product Completion

1. Add US5 real first-run and exact-reuse evidence.
2. Add US6 agent install/separate-run proof.
3. Close US7 recovery and US8 advanced-init convergence.
4. Synchronize docs/formulas only after behavior evidence exists.
5. Run the complete local and real battery plus fresh-eyes review.

## Notes

- Tests for authority behavior must fail against the pre-implementation path.
- The worktree already contains uncommitted operator-intent, formal-model,
  Manager, profile, and documentation changes; preserve and extend them.
- Do not add compatibility shims for the unsafe options-only init apply API;
  there are no external users to preserve.
- Do not use source grep as the sole setup, UI, daemon, or evidence proof.
- A cached runtime is acceptable only after exact SHA-256 verification.

## Phase 12: Convergence

- [X] T065 Make setup confirmation cancellation context-aware and prove real
  SIGINT/PTY, EOF, empty, negative, control-byte, and non-terminal paths produce
  no apply or durable setup effect per FR-005, FR-006, SC-003
- [X] T066 Complete setup-state observation with strict single-value JSON,
  private owner/mode proof, bounded typed reasons, deterministic redaction, and
  fresh/ready/repairable/blocked fixtures per FR-014, FR-015, FR-016, FR-026,
  SC-004, SC-009
- [X] T067 Define and test the complete semantic prepared-init projection,
  including effect-field sensitivity, presentation/generated-value stability,
  runtime/catalog/prerequisite drift, and stale confirmation rejection per
  FR-010, FR-013, SC-005
- [X] T068 Prove setup is authority-equivalent to explicit dev/Lima/direct
  initialization while advanced profile/template/backend/network/runtime,
  interactive, dry-run, and no-input paths use only authenticated daemon
  plan/apply per FR-008, FR-029, FR-031, SC-015
- [X] T069 Replace or bound heuristic setup recovery classification and add
  stale-socket, build-mismatch, cold-start, auth, mid-operation exit, package,
  disk, runtime, and backend failure tests with zero embedded/native fallback
  per FR-009, FR-035, US7
- [X] T070 Execute the packaged direct/setup macOS arm64 Lima lane and the
  exact agent install/separate-session run using the verified cached runtime,
  then validate first-run, identity, reuse, lifecycle, audit, and real proof
  artifacts per FR-023 through FR-025, FR-032 through FR-034, SC-006 through
  SC-008, SC-011, SC-014, SC-016
- [X] T071 Add byte/digest/mtime and store-inventory snapshots around cancel,
  ready, customized, and concurrent-creator paths, covering profile, identity,
  audit, onboarding/product evidence, environment, runtime, and daemon-only
  permitted state per FR-006, FR-014, FR-015, FR-027, SC-004
- [X] T072 Re-run and evaluate the shared-package local setup PTY lane, verify
  all registered 038 proof mappings, package-install zero side effects, docs
  truth, and source/tap formula parity per FR-021, FR-022, FR-028, SC-010,
  SC-012, SC-013
