# Tasks: Projection Readiness Proof

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/043-projection-readiness-proof/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`,
`contracts/`, `quickstart.md`

**Tests**: Required. This feature changes the filesystem/session-view,
backend/supervisor, lifecycle, broker, audit, schema, and release-evidence
boundaries. Every new assertion needs an observed implementation mutation, and
every new judge needs a false-green negative fixture.

**Organization**: Tasks are grouped by user story so the first-attempt fix,
authority closure, and clean promotion evidence can be validated separately.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel because it changes different files and has no
  dependency on another incomplete task in the same phase.
- **[Story]**: Maps to a user story in `spec.md`.
- Every task names the exact file or bounded file set it changes.

## Phase 1: Setup And Baseline

**Purpose**: Preserve the known-green baseline and create the required
adversarial ledger before changing the readiness boundary.

- [ ] T001 Run the focused baseline from `specs/043-projection-readiness-proof/quickstart.md` and record exact commands and the known first-run/schema gaps in `specs/043-projection-readiness-proof/adversarial-report.md`
- [ ] T002 [P] Add the four-item 030 starting disposition table, mutation inventory, judge-negative-fixture inventory, and real-gate evidence placeholders to `specs/043-projection-readiness-proof/adversarial-report.md`

---

## Phase 2: Foundational Catalog And Proof Types

**Purpose**: Define the backend-neutral immutable catalog, manifest, and proof
types used by every user story.

**CRITICAL**: No user story implementation begins until these types,
canonicalization rules, and schemas are green.

- [ ] T003 [P] Write failing strict validation and session-match tests for `ProjectionReadinessExpectation`, `ProjectionReadinessObservation`, and extended `SessionReadyProof` values in `internal/backend/backend_test.go`
- [ ] T004 [P] Write failing canonical ordering, duplicate/path rejection, digest stability, strict manifest decoding, and catalog-size-bound tests in `internal/manager/projection_readiness_test.go`
- [ ] T005 Implement backend-neutral readiness expectation, disposition, typed reason, clone, validation, and ready-proof comparison fields in `internal/backend/backend.go`
- [ ] T006 Implement Manager-owned catalog snapshot, entry digest, canonical catalog digest, strict manifest codec, and bounded catalog construction in `internal/manager/projection_readiness.go`
- [ ] T007 [P] Add the strict public readiness manifest/evidence contract and valid/unknown-field fixtures in `schemas/projection-readiness.schema.json` and `internal/productevidence/schema_test.go`

**Checkpoint**: One complete final registry can be represented, hashed, decoded,
and compared without granting authority or depending on Lima.

---

## Phase 3: User Story 1 - Open The Host App On The First Attempt (Priority: P1) MVP

**Goal**: Make the complete exact session projection visible and validated
before target commit on fresh, warm, dedicated, shared, and concurrent sessions.

**Independent Test**: A first projected target either receives an authenticated
ready proof for its exact catalog and starts once, or returns a typed bounded
failure with zero target, host effect, fallback, or cross-session access.
Ordinary non-projected commands retain their existing lookup behavior.

### Tests For User Story 1

- [ ] T008 [P] [US1] Write failing tests that the final built-in plus enabled external registry is snapshotted and that the readiness manifest is written only after every exact shim in `internal/manager/run_dataplane_host_app_test.go` and `internal/manager/projection_readiness_test.go`
- [ ] T009 [P] [US1] Write failing tests that Lima `Prepare` preserves the Manager expectation instead of reconstructing a profile-only catalog in `internal/backend/lima/lima_test.go`
- [ ] T010 [P] [US1] Write failing exact-session-view tests for missing/late files, regular non-symlink executables, wrong digest, foreign manifest, identity drift, two-second timeout, and disjoint catalogs in `internal/backend/lima/session_view_test.go` and `internal/backend/lima/activation_concurrent_test.go`
- [ ] T011 [P] [US1] Write failing strict wire and Linux supervisor tests for manifest decoding, complete entry hashing, catalog digest/count reporting, and refusal before commit in `internal/sessionwire/control_test.go`, `cmd/hideout-session-supervisor/model_test.go`, and `cmd/hideout-session-supervisor/wire_linux_test.go`
- [ ] T012 [P] [US1] Write failing stream tests for authenticated proof comparison, zero commit/output on mismatch, immediate pre-commit cancellation, and unchanged post-commit cancellation in `internal/backend/lima/session_stream_test.go`
- [ ] T013 [P] [US1] Write failing Manager and daemon lifecycle tests proving no lifecycle activation or `Started` frame precedes matching readiness, while ordinary guest commands retain command-not-found semantics in `internal/manager/run_lifecycle_test.go` and `internal/daemon/session_server_test.go`
- [ ] T014 [P] [US1] Write failing fresh/warm/concurrent data-plane tests for exact session catalog isolation and no ambient fallback in `internal/manager/projection_readiness_test.go` and `internal/manager/concurrent_run_test.go`

### Implementation For User Story 1

- [ ] T015 [US1] Build the final catalog from `RunDataPlane.Registry`, materialize dispatcher and shims, and atomically write the strict session manifest last in `internal/manager/run_dataplane.go` and `internal/manager/projection_readiness.go`
- [ ] T016 [US1] Carry and rebind the immutable Manager expectation through `RunSpec`, `Backend.Prepare`, and returned `Session`, and use its complete shim names for bootstrap in `internal/backend/backend.go`, `internal/backend/lima/lima.go`, and `internal/manager/run_session.go`
- [ ] T017 [US1] Extend the exact Lima session-view prerequisite barrier to the readiness manifest and fixed supervisor without adding a global command probe or target retry in `internal/backend/lima/session_view.go`
- [ ] T018 [US1] Strictly decode the bound manifest and validate every dispatcher/command file as regular, non-symlink, executable, and digest-matched before reporting ready in `cmd/hideout-session-supervisor/model.go`, `cmd/hideout-session-supervisor/main_linux.go`, and `cmd/hideout-session-supervisor/wire_linux.go`
- [ ] T019 [US1] Extend authenticated `SupervisorReady` and `SessionReadyProof` with readiness status, catalog digest, counts, and bounded duration while preserving strict unknown-field/frame validation in `internal/sessionwire/control.go` and `internal/backend/backend.go`
- [ ] T020 [US1] Validate the ready catalog before `SupervisorCommit`, close the owning SSH session immediately on pre-commit cancellation, and keep existing graceful post-commit termination in `internal/backend/lima/session_stream.go`
- [ ] T021 [US1] Gate Manager workspace/lifecycle activation and daemon `Started` publication on the matching proof, and emit one typed redacted readiness disposition in `internal/manager/run_lifecycle_effects.go`, `internal/manager/run_service.go`, and `internal/daemon/session_server.go`
- [ ] T022 [US1] Run and record red-then-green mutations for omitted catalog entry, early manifest, accepted symlink/digest mismatch, foreign ready digest, skipped lifecycle gate, and target retry in `specs/043-projection-readiness-proof/adversarial-report.md`

**Checkpoint**: User Story 1 is independently green under focused tests and
race tests; no real-backend promotion is claimed yet.

---

## Phase 4: User Story 2 - Keep Projection Authority Closed (Priority: P2)

**Goal**: Prove the synchronization fix does not broaden command, broker,
workspace, schema, or reviewed-plan authority, and dispose of all four 030 debt
observations individually.

**Independent Test**: Contradict registry membership, template posture,
workspace path mode, descriptor/intent schema, and reviewed catalog digest one
at a time. Each contradiction fails before a host effect, and every 030 debt
item names its direct current proof or remains explicitly deferred.

### Tests For User Story 2

- [ ] T023 [P] [US2] Add a deliberately inconsistent fixture where bindings resolve `code` but the exact command registry does not, and require broker denial plus unvalidated audit classification in `internal/broker/hostapp_test.go`
- [ ] T024 [P] [US2] Add direct table assertions that newly created privacy, hardened, development, and debug templates each use neutral alias workspace presentation in `internal/profiletemplate/template_test.go`
- [ ] T025 [P] [US2] Strengthen the existing alias-to-preserve pathMode flip proof to assert recreate impact, machine/session identity drift, and zero silent remap in `internal/manager/run_environment_mode_test.go` and `internal/manager/run_apply_test.go`
- [ ] T026 [P] [US2] Add strict tests that actual marshalled `CapabilityDescriptor` and open-resource intent values satisfy their public schemas while unknown, missing, incompatible, or trailing fields fail in `internal/hostcap/registry_test.go`
- [ ] T027 [P] [US2] Add stale-plan tests that change built-in/external command ownership after review and require apply/readiness refusal instead of ambient catalog substitution in `internal/manager/run_apply_test.go` and `internal/manager/run_dataplane_host_app_test.go`

### Implementation For User Story 2

- [ ] T028 [US2] Preserve exact command-registry lookup before binding/provider admission and derive audit command classification only from the validated registry result in `internal/broker/hostapp.go`
- [ ] T029 [US2] Add stable lower-camel JSON tags to `CapabilityDescriptor`, align `residualPolicy` and required fields in `schemas/capability-descriptor.schema.json`, and keep strict decoding in `internal/hostcap/descriptor.go`
- [ ] T030 [US2] Bind the complete projection catalog digest into reviewed run truth, recompile and compare it at apply, and refuse stale external-pack ownership in `internal/manager/run_plan.go`, `internal/manager/run_apply.go`, and `internal/manager/run_service.go`
- [ ] T031 [US2] Record named current proofs for broker registration, four-template alias defaults, pathMode recreation, descriptor parity, and unbound-intent schema in `specs/043-projection-readiness-proof/adversarial-report.md`
- [ ] T032 [US2] Run and record red-then-green mutations for skipped broker lookup, one non-alias template, removed pathMode recreate identity, omitted descriptor field/tag, permissive unknown fields, and skipped catalog-drift comparison in `specs/043-projection-readiness-proof/adversarial-report.md`
- [ ] T033 [US2] Narrow or close the four-item 030 acceptance ledger row item by item without deleting unresolved triggers in `docs/DEBT.md`

**Checkpoint**: User Story 2 is green with direct mutation-sensitive proofs for
all historical observations and no readiness-derived authority expansion.

---

## Phase 5: User Story 3 - Promote Projection Claims With Clean Provenance (Priority: P3)

**Goal**: Produce and independently judge one exact clean package/runtime
evidence set covering first-attempt readiness, built-in projection, external
pack, and durable grant/revoke, with privacy promotion kept conditional.

**Independent Test**: A valid exact-package fixture passes the production
evaluator; dirty, mismatched, reduced, edited, unknown-field, false-check,
altered-artifact, or `not-run` fixtures fail for the intended reason.

### Tests For User Story 3

- [ ] T034 [P] [US3] Write failing semantic evaluator tests for closed check maps, 10 fresh and 30 warm raw samples, nearest-rank p95, cancellation bound, zero retries/fallback/effects, artifact digests, redaction, and every mandatory false-green fixture in `internal/productevidence/projection_readiness_test.go`
- [ ] T035 [P] [US3] Write failing proof-registry tests for new 043 and 039 proofs plus strengthened 030/032 exact source/package/runtime requirements in `internal/productevidence/registry_test.go`
- [ ] T036 [P] [US3] Write failing release-readiness fixture tests for valid 043/039 semantic evidence and missing, mismatched, dirty, or `not-run` variants in `internal/releasecompat/readiness_test.go` and `internal/releasecompat/readiness_schema_test.go`
- [ ] T037 [P] [US3] Add shell-level false-green smoke fixtures for forged marker-only, wrong package, reduced samples, edited p95, and missing external/grant flows in `scripts/test-projection-readiness-smoke.sh`

### Implementation For User Story 3

- [ ] T038 [US3] Implement strict projection readiness/sample/flow/privacy artifact decoders, recomputation, inventory hashing, and redaction validation in `internal/productevidence/projection_readiness.go`
- [ ] T039 [US3] Register 043 readiness and 039 persistent-grant proof IDs, require exact candidate package/runtime semantics, and strengthen reused 030/032 proof policies in `internal/productevidence/registry.go`
- [ ] T040 [US3] Extend release compatibility matrices and semantic fixture builders for the 043/039 proof requirements in `internal/releasecompat/matrix.go` and `internal/releasecompat/readiness_test.go`
- [ ] T041 [US3] Implement the strict smoke producer/evaluator wrapper and add it to Gate 0 in `scripts/test-projection-readiness-smoke.sh` and `scripts/test-gate0.sh`
- [ ] T042 [US3] Implement the clean exact-package real producer for fresh, warm, concurrent, built-in, external-pack, durable-grant, no-fallback, and artifact-retention lanes in `scripts/test-projection-readiness-lima-e2e.sh` and `scripts/lib/gate2-projection.sh`
- [ ] T043 [US3] Require the strict 043 proof marker/artifacts from aggregate Gate 2 while retaining the existing 030/032/039 regression flows in `scripts/test-gate2-lima.sh`, `scripts/test-host-capability-projection-e2e.sh`, and `scripts/test-host-app-pack-e2e.sh`
- [ ] T044 [US3] Build one clean exact package and retain evaluator-passing 10-fresh/30-warm/concurrent Gate 2 evidence under `.hideout-release-evidence/043-projection-readiness-real-gate2` using `scripts/test-projection-readiness-lima-e2e.sh`
- [ ] T045 [US3] Run matching clean Gate 3 when prerequisites exist and either retain `artifacts/projection-privacy-gate3.json` or explicitly retain privacy as unpromoted in `specs/043-projection-readiness-proof/adversarial-report.md`

**Checkpoint**: Mechanical readiness, 032 external pack, and 039 durable grant
are promoted only if their strict artifacts pass. Alias privacy is promoted
only when the matching clean Gate 3 artifact also passes.

---

## Phase 6: Polish, Documentation, And Convergence

**Purpose**: Complete mutation evidence, local gates, product truth, and
cross-artifact convergence without overstating unavailable real evidence.

- [ ] T046 [P] Run focused Go tests, Linux supervisor tests, race suites, schema tests, and shell syntax checks from `specs/043-projection-readiness-proof/quickstart.md` and record results in `specs/043-projection-readiness-proof/adversarial-report.md`
- [ ] T047 Complete the implementation-mutation and judge-negative-fixture matrices with exact commands, observed red failures, restored-green results, and retained limitations in `specs/043-projection-readiness-proof/adversarial-report.md`
- [ ] T048 Run full `scripts/test-gate0.sh`, Markdown lint, docs-truth smoke, and `git diff --check`, then record exact results in `specs/043-projection-readiness-proof/adversarial-report.md`
- [ ] T049 [P] Update only evidence-supported status, claim, design, test-plan, subsystem, threat-model, 039 proof, and remaining-debt statements in `docs/STATUS.md`, `docs/claim-boundaries.md`, `docs/privacy-run-design.md`, `docs/privacy-run-test-plan.md`, `docs/host-capability-projection.md`, `docs/host-app-recipes.md`, `docs/threat-model.md`, `docs/DEBT.md`, and `specs/039-host-app-persistent-grants/spec.md`
- [ ] T050 Reconcile every FR, SC, acceptance scenario, and checklist item with implementation/evidence; append any real remaining work before marking 043 implemented in `specs/043-projection-readiness-proof/spec.md` and `specs/043-projection-readiness-proof/tasks.md`

---

## Dependencies And Execution Order

### Phase Dependencies

- **Phase 1**: No dependencies.
- **Phase 2**: Depends on Phase 1 and blocks all user stories.
- **User Story 1**: Depends on Phase 2 and is the implementation MVP.
- **User Story 2**: Depends on Phase 2; stale-catalog tasks T027/T030 also
  consume the catalog model from T006, but its broker/default/schema proofs are
  independently runnable.
- **User Story 3**: Evaluator and registry work can begin after Phase 2, but its
  real producer and evidence tasks T042-T045 require the completed US1/US2
  semantics.
- **Phase 6**: Depends on all implementation stories selected for the release;
  documentation promotes only proof families whose gates actually passed.

### Within Each User Story

- Failing contract and negative tests precede implementation.
- Domain and wire types precede backend and Manager integration.
- Materialization precedes guest observation; observation precedes ready proof;
  ready proof precedes lifecycle activation and target commit.
- Semantic evaluator negative fixtures precede producer acceptance.
- Every new green assertion is followed by its listed implementation mutation
  and restored-green run.

### User Story Dependency Graph

```text
Setup -> Foundational -> US1 first-attempt readiness
                    \-> US2 authority/debt closure
US1 + US2 -> US3 clean exact-package evidence
US1 + US2 + US3 -> documentation and convergence
```

### Parallel Opportunities

- T002 can run while the T001 baseline executes.
- T003, T004, and T007 touch independent type/model/schema test surfaces.
- T008-T014 are independent red-test fixtures until shared types stabilize.
- T023-T027 are independent 030 contract fixtures.
- T034-T037 are independent evaluator, registry, releasecompat, and shell
  negative-fixture lanes.
- T046 and evidence-supported portions of T049 can run after implementation
  semantics and retained proof status are fixed.

---

## Parallel Examples

### User Story 1

```text
Task T008: Manager complete-catalog/manifest-order red tests
Task T010: Lima exact-session-view file/identity red tests
Task T011: sessionwire and Linux supervisor red tests
Task T013: Manager/daemon lifecycle-boundary red tests
```

### User Story 2

```text
Task T023: Broker registry/binding mismatch fixture
Task T024: Four-template direct alias fixture
Task T025: pathMode recreate fixture
Task T026: Real descriptor/intent schema fixtures
```

### User Story 3

```text
Task T034: Production evidence semantic negative fixtures
Task T035: Proof registry requirement fixtures
Task T036: Release compatibility fixture builder
Task T037: Shell producer false-green fixtures
```

---

## Implementation Strategy

### MVP First

1. Complete setup and immutable catalog/proof foundations.
2. Deliver US1 through the authenticated ready/commit boundary.
3. Run focused and race tests plus US1 mutations.
4. Stop and validate that the first projected target starts once or fails
   before all effects; do not wait for release promotion to accept the
   reliability fix.

### Incremental Delivery

1. **US1** closes the user-visible fresh/warm first-attempt race.
2. **US2** proves the fix did not broaden authority and closes/narrows 030 debt.
3. **US3** turns those implemented paths into clean exact-package claims.
4. **Convergence** promotes only evidence-backed facts and retains unavailable
   Gate 3 or real-backend limitations explicitly.

## Notes

- `[P]` means separate files and no unmet semantic dependency, not permission
  to merge competing edits blindly.
- Native/fake tests prove mechanics only; Lima is required for guest visibility
  and host-effect claims.
- Gate 2 does not promote alias privacy without matching Gate 3.
- No task adds a CLI, config field, host capability, provider, fallback, target
  retry, workspace copy, or guest-to-host projection direction.
