# Tasks: HostFS Discoverable Namespace

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/029-hostfs-discoverable-namespace/`

**Tests**: Required. This feature changes a filesystem information boundary,
broker errno ABI, session authority, decision lifecycle, and promoted real
backend claims.

**Organization**: Tasks are grouped by user story so each story is an
independently testable increment. Real Lima assertions are part of the story
that promotes the corresponding guest-visible claim.

## Phase 1: Setup

**Purpose**: Establish versioned artifacts and test entrypoints without adding
runtime authority.

- [X] T001 Add strict session read-grant JSON Schema in `schemas/hostfs-read-grants.schema.json`
- [X] T002 [P] Create the persistence package boundary and package documentation in `internal/hostfs/readgrant/doc.go`
- [X] T003 [P] Add 029 proof/claim constants without registering passing evidence in `internal/productevidence/claims.go`
- [X] T004 [P] Create local-fast/real-gate mode parsing and fail-closed prerequisite skeleton in `scripts/test-hostfs-visibility-e2e.sh`
- [X] T005 Register the new schema and non-passing smoke skeleton in `scripts/test-gate0.sh`

---

## Phase 2: Foundational

**Purpose**: Shared roots, session layout, and typed ABI primitives required by
all three stories.

**CRITICAL**: Complete before user-story implementation.

- [X] T006 [P] Add category/consumer matrix tests for the existing workspace-sensitive roots in `internal/hostpathrisk/catalog_test.go`
- [X] T007 Extract the single categorized sensitive-root source of truth and adapt workspace callers in `internal/hostpathrisk/catalog.go` and `internal/manager/run_plan.go`
- [X] T008 [P] Add session owner/read-state path, permissions, and cleanup tests in `internal/session/session_test.go`
- [X] T009 Add `HostFSReadDir`, owner/provider lock paths, and ephemeral cleanup ownership in `internal/session/session.go`
- [X] T010 [P] Add strict broker typed-error schema tests for every valid and invalid V1 code/errno pair in `internal/broker/broker_test.go`
- [X] T011 Add the additive broker error model and closed HostFS code/errno validator in `internal/broker/broker.go` and `internal/broker/hostfs_error.go`
- [X] T012 Update the broker envelope schema so HostFS failures require a valid typed error in `schemas/broker-envelope.schema.json`
- [X] T013 [P] Add `hostfs.read` kind/reopen action validation and redaction tests in `internal/decision/types_test.go` and `internal/decision/evidence_test.go`
- [X] T014 Extend the closed decision vocabulary and packaged decision schema with `hostfs.read` and provider-specific reopen action in `internal/decision/types.go` and `schemas/decision-record.schema.json`

**Checkpoint**: One root catalog, private session layout, and typed broker/
decision vocabulary are available; no discovery or read grant is active yet.

---

## Phase 3: User Story 1 - Navigate Visible Host Paths Without Reading (P1) MVP

**Goal**: Explicit selected names are visible with coarse metadata while
content, full metadata, symlink targets, writes, and all outside-domain names
remain unavailable.

**Independent Test**: Real fixture roots prove exact, one-level, and recursive
visibility; complete-or-error enumeration; three-state errno; and no behavior
change for a profile without `see*`.

### Tests For User Story 1

- [X] T015 [P] [US1] Add selector normalization, glob rejection, and new-`list:` rejection tests in `internal/hostfs/hostfs_test.go`
- [X] T016 [P] [US1] Add visibility precedence, explicit-domain, reserved-root, discover-deny versus exact-content-grant, and legacy-collapse tests in `internal/hostfs/hostfs_test.go`
- [X] T017 [P] [US1] Add coarse exact/one-level/tree, synthetic ancestor, and no-follow symlink tests in `internal/hostfs/service_test.go`
- [X] T018 [P] [US1] Add complete-or-error tests for exact-directory readdir, 4097 entries, depth 33, child inspection failure, and four-call concurrency in `internal/hostfs/service_test.go`
- [X] T019 [P] [US1] Add broker tests for all typed visibility errors plus bounded routine discovery aggregation by session/root/op/outcome in `internal/broker/broker_test.go`
- [X] T020 [P] [US1] Add Linux helper tests proving typed errno mapping, unknown-to-EIO fallback, stderr non-authority, and explicit FUSE TTLs in `cmd/hideout-hostfsd/main_linux_test.go`
- [X] T021 [P] [US1] Add regression tests for existing read/dir/tree/glob/staged overlay and grant-implied-only unauthorized behavior in `internal/hostfs/service_test.go` and `internal/manager/manager_test.go`

### Implementation For User Story 1

- [X] T022 [US1] Add `OpDiscover`, `see*` parsing, validation, ordering, summaries, and packaged profile schema support without stat normalization in `internal/hostfs/hostfs.go` and `schemas/profile.schema.json`
- [X] T023 [US1] Implement one Go-owned visibility evaluator with explicit-domain and enumeration-depth results in `internal/hostfs/visibility.go`
- [X] T024 [US1] Extend `NodeInfo`/`DirEntry` with locked coarse result semantics while retaining ordinary granted metadata in `internal/hostfs/service.go`
- [X] T025 [US1] Implement exact lookup and synthetic ancestor behavior using no-follow kind classification in `internal/hostfs/service.go`
- [X] T026 [US1] Implement lazy complete-or-error `see-dir`/`see-tree` listing, 4096-entry and depth-32 bounds, and per-session four-call semaphore in `internal/hostfs/service.go`
- [X] T027 [US1] Map service visibility/error outcomes to validated broker typed errors without creating a decision in `internal/broker/hostfs.go`
- [X] T028 [US1] Replace HostFS helper stderr matching with typed error validation and set entry/attr/negative TTLs in `cmd/hideout-hostfsd/main_linux.go`
- [X] T029 [US1] Treat discover-only policies as HostFS-active and materialize required grafts/helper delivery in `internal/manager/run_dataplane.go` and `internal/manager/manager.go`
- [X] T030 [US1] Extend profile/run rule output and Manager profile-hostfs plan/apply summaries for discover selectors in `internal/app/app.go`, `internal/manager/profile_hostfs.go`, and `internal/manager/api.go`
- [X] T031 [US1] Replace per-syscall routine discovery audit with bounded session/root/op/outcome aggregation and ordered close flush in `internal/broker/hostfs_audit.go` and `internal/manager/run_dataplane.go`
- [X] T032 [US1] Add local-fast namespace/typed-errno proof generation with real secret/content injection checks in `scripts/test-hostfs-visibility-e2e.sh`
- [X] T033 [US1] Add real Gate 2 namespace fixtures and machine-asserted markers for cases 1-8 and 14-17 in `scripts/test-gate2-lima.sh`
- [X] T034 [US1] Run and fix targeted US1 tests: `go test ./internal/hostfs ./internal/broker ./internal/manager ./cmd/hideout-hostfsd`

**Checkpoint**: US1 is usable independently. A target can navigate explicit
coarse names and receives truthful typed denials, but no read approval is
created yet.

---

## Phase 4: User Story 2 - Approve One Locked Read And Retry (P2)

**Goal**: One eligible locked read creates/coalesces a bounded decision;
authenticated approval gives the unchanged running session exact-file read
authority on its next retry.

**Independent Test**: A real running guest fails promptly, a separate host
process approves, the same target retries successfully, attrs converge within
one second, and deny/timeout/limit/dead-session/symlink cases remain closed.

### Tests For User Story 2

- [X] T035 [P] [US2] Add strict provider/grant schema, atomic generation, expiry, malformed-state, and no-private-path tests in `internal/hostfs/readgrant/store_test.go`
- [X] T036 [P] [US2] Add owner-lock live/dead/orphaned/unprovable and cleanup tests in `internal/hostfs/readgrant/owner_test.go` and `internal/session/session_test.go`
- [X] T037 [P] [US2] Add deterministic dedup, unchanged deadline, eight-pending, eight-per-minute, retry hint, and concurrent-process tests in `internal/manager/hostfs_read_test.go`
- [X] T038 [P] [US2] Add claim/approve/deny/timeout/reopen revision and dead-session refusal tests in `internal/manager/decisions_test.go`
- [X] T039 [P] [US2] Add broker proposal mapping tests proving references only for real pending/claimed decisions in `internal/broker/broker_test.go`
- [X] T040 [P] [US2] Add same-process and separate-process check-before-deny tests, including next-retry activation and no watcher, in `internal/manager/manager_test.go`
- [X] T041 [P] [US2] Add symlink-retarget, current-policy deny, expiry, and stale-generation fail-closed tests in `internal/hostfs/readgrant/store_test.go` and `internal/hostfs/service_test.go`
- [X] T042 [P] [US2] Add API route inventory/parity and auth tests for generic read decisions and reopen in `internal/manager/api_routes_test.go` and `internal/manager/api_test.go`
- [X] T043 [P] [US2] Add WebUI goja and TUI rendering tests for profile scope, plain untrusted reason, actions, and terminal reopen in `internal/manager/server_liveconsole_test.go` and `internal/app/app_liveconsole_test.go`

### Implementation For User Story 2

- [X] T044 [US2] Implement strict provider state, atomic grant manifest, shared/exclusive locks, generation, expiry, and validation in `internal/hostfs/readgrant/store.go`
- [X] T045 [US2] Implement owner-lock acquisition/probing and integrate its lifetime with data-plane close in `internal/hostfs/readgrant/owner.go` and `internal/manager/run_dataplane.go`
- [X] T046 [US2] Implement the Manager-owned read provider with opaque key, five-minute timeout, dedup, terminal memory, rate/pending limits, and aggregate suppression in `internal/manager/hostfs_read.go`
- [X] T047 [US2] Define and inject the broker proposal provider and session grant reader without manager import cycles in `internal/broker/hostfs.go` and `internal/manager/run_dataplane.go`
- [X] T048 [US2] Implement read/stat check-before-deny with per-operation recanonicalization, active exact grant validation, immediate content activation, and ordinary stat metadata within the one-second FUSE TTL in `internal/hostfs/service.go` and `internal/broker/hostfs.go`
- [X] T049 [US2] Promote `hostfs.read` claim/approve/deny/timeout through the generic decision center and publish the grant only after revalidation/audit in `internal/manager/decisions.go`
- [X] T050 [US2] Add generic store support for provider-validated terminal reopen and failed activation without weakening other kinds in `internal/decision/store.go`
- [X] T051 [US2] Add authenticated query/path reopen dispatch to the shared production route inventory in `internal/manager/routes.go` and `internal/manager/api.go`
- [X] T052 [US2] Add `hideout decision reopen` and typed recovery output in `internal/app/app.go`
- [X] T053 [US2] Render generic `hostfs.read` actions/reopen in WebUI and TUI using existing decision state/profile filtering in `internal/manager/server.go` and `internal/app/app.go`
- [X] T054 [US2] Emit redacted decision create/suppress/claim/apply/deny/timeout/reopen/activation-failure audit and live events in `internal/manager/hostfs_read.go` and `internal/manager/decisions.go`
- [X] T055 [US2] Remove session read authority during ordered/ephemeral cleanup and report unprovable leftovers without re-adoption in `internal/session/session.go` and `internal/manager/run_session.go`
- [X] T056 [US2] Add real Gate 2 live process orchestration and assertions for cases 7-13 and 18-20 in `scripts/test-gate2-lima.sh`
- [X] T057 [US2] Emit digest-backed namespace/live-grant or explicit supporting not-run proof in `scripts/test-hostfs-visibility-e2e.sh`
- [X] T058 [US2] Run and fix targeted US2 tests: `go test ./internal/hostfs/readgrant ./internal/decision ./internal/broker ./internal/manager ./internal/daemon ./internal/liveconsole ./internal/app`

**Checkpoint**: US2 proves a real, bounded cross-process authority path. An
approval that does not change the running target's next retry is a failure, not
a completed task.

---

## Phase 5: User Story 3 - Choose Visibility Without Overclaiming Privacy (P3)

**Goal**: Operators can keep full hiding, confirm useful landmarks, or
explicitly acknowledge broader name disclosure; migration, TCC diagnostics,
audit, evidence, and docs state the exact boundary.

**Independent Test**: No-choice/noninteractive remains none; confirmed presets
expand to ordinary rules; broad selection requires acknowledgement; legacy
list migration is atomic; TCC is unprobed until explicit access; docs/evidence
reject every forbidden claim and secret leak.

### Tests For User Story 3

- [X] T059 [P] [US3] Add raw legacy-profile detection, all-rule mapping, disclosure preview, drift, confirmation, and atomic migration tests in `internal/manager/profile_hostfs_test.go` and `internal/profile/profile_test.go`
- [X] T060 [P] [US3] Add none/landmarks/home-tree, cancellation, noninteractive omission, acknowledgement, and no-eager-scan tests in `internal/profiletemplate/template_test.go` and `internal/inittask/inittask_test.go`
- [X] T061 [P] [US3] Add categorized broad-discovery exclusion and explicit exact-content-grant compatibility tests in `internal/hostpathrisk/catalog_test.go` and `internal/hostfs/hostfs_test.go`
- [X] T062 [P] [US3] Add hostfs doctor unprobed/explicit-probe/warn-before-access/prerequisite-EIO tests in `internal/app/app_test.go` and `internal/doctor/report_test.go`
- [X] T063 [P] [US3] Add audit/Boundary/evidence redaction tests with real broker/claim tokens, machine ID, secret fields, content, symlink target, and grant paths in `internal/manager/hostfs_read_test.go` and `internal/app/app_test.go`
- [X] T064 [P] [US3] Add proof-registry tests for all eight 029 IDs, real artifact/digest policy, required-for targets, and not-run non-satisfaction in `internal/productevidence/registry_test.go` and `internal/productevidence/evaluate_test.go`
- [X] T065 [P] [US3] Add docs-truth rejection cases for all SC-014 overclaims in `scripts/test-doc-truth-smoke.sh`

### Implementation For User Story 3

- [X] T066 [US3] Add a strict raw profile migration-candidate loader that accepts only known legacy-list state in `internal/profile/profile.go`
- [X] T067 [US3] Add `migrate-list` mapping, disclosure review, confirmation, drift check, and atomic Manager plan/apply in `internal/manager/profile_hostfs.go` and `internal/manager/api.go`
- [X] T068 [US3] Add CLI parsing/output for all-rule legacy migration in `internal/app/app.go`
- [X] T069 [US3] Implement visibility preset expansion and categorized broad-discovery denies in `internal/profiletemplate/template.go`
- [X] T070 [US3] Carry visibility selection, explicit acknowledgement, generated rule IDs, warnings, non-claims, review lines, evidence, and packaged schemas through InitTask in `internal/inittask/inittask.go`, `internal/app/app.go`, `schemas/init-plan.schema.json`, and `schemas/onboarding-evidence.schema.json`
- [X] T071 [US3] Add explicit `--probe-hostfs-root` behavior and observed/unprobed HostFS findings without silent TCC claims in `internal/app/app.go` and `internal/doctor/report.go`
- [X] T072 [US3] Add visibility posture, counts, limits, and scoped non-claim to Manager overview and Boundary Summary in `internal/manager/manager.go` and `internal/app/app.go`
- [X] T073 [US3] Register all 029 proof requirements with local versus real-gate freshness/artifact policies in `internal/productevidence/registry.go` and `internal/productevidence/claims.go`
- [X] T074 [US3] Run and fix targeted US3 tests: `go test ./internal/profile ./internal/profiletemplate ./internal/inittask ./internal/hostpathrisk ./internal/doctor ./internal/productevidence ./internal/app`

**Checkpoint**: Product policy, setup, diagnostics, claims, and evidence now
describe exactly the implemented disclosure domain; no preset silently grants
visibility.

---

## Phase 6: Polish And Cross-Cutting Completion

**Purpose**: Distribution, documentation, evidence integration, and complete
verification after all stories work.

- [X] T075 [P] Update public workflow and name-disclosure warning in `README.md` and `README.zh-CN.md`
- [X] T076 [P] Replace the global HostFS denied/absent claim with scoped three-state claims and predictable-path non-claim in `docs/threat-model.md` and `docs/claim-boundaries.md`
- [X] T077 [P] Document discover/read/write separation, FUSE cache contract, and session grant lifecycle in `docs/hostfs-overlay-design.md`
- [X] T078 [P] Add explicit onboarding selection, TCC warning, and parseable CLI examples in `docs/first-run-alpha.md` and `docs/command-examples.json`
- [X] T079 [P] Add Gate 0/real Gate 2 coverage, 20 assertion inventory, and local-fast non-claim in `docs/privacy-run-test-plan.md`
- [X] T080 Verify changed `hideout-hostfsd` remains installed, packaged, checksummed, and smoke-validated in `internal/packagekit/manifest.go`, `scripts/install-local.sh`, `scripts/package-local.sh`, and `scripts/test-package-smoke.sh`
- [X] T081 Integrate the 029 local-fast lane and schema checks into Gate 0 without treating not-run/real claims as pass in `scripts/test-gate0.sh`
- [X] T082 Run quickstart scenarios 1-10 through `specs/029-hostfs-discoverable-namespace/quickstart.md` and record only valid local proof entries in the 029 evidence manifest
- [X] T083 Run the complete static battery against `internal/`, `cmd/`, `scripts/test-gate0.sh`, and `specs/029-hostfs-discoverable-namespace/`: build, vet, gofmt, diff-check, tests, markdownlint, and Gate 0
- [X] T084 Run real macOS arm64 Lima `scripts/test-hostfs-visibility-e2e.sh --real-gate2`, validate all 20 machine assertions, verify both required real proof IDs/artifact digests, and clean leaked sessions/Lima instances
- [X] T085 Update `docs/STATUS.md` to Implemented only after T084 produces validated real Gate 2 namespace and live-grant proof
- [X] T086 Re-run `speckit-analyze` and `speckit-converge`, rerun affected docs/static checks after status promotion, append any real residual work, and leave all completion tasks checked only when code, docs, evidence, and gates agree

---

## Dependencies And Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup and blocks every story.
- **US1 (Phase 3)**: Depends on Foundational and is the MVP.
- **US2 (Phase 4)**: Depends on US1's explicit visibility and typed errno path.
- **US3 (Phase 5)**: Depends on US1 policy vocabulary; provider-facing evidence
  also depends on US2.
- **Polish (Phase 6)**: Depends on all stories. Real status promotion T085
  depends on T084, not merely local green tests.

### User Story Dependency Graph

```text
Setup -> Foundational -> US1 (coarse namespace)
                             |
                             +-> US2 (read approval/live grant)
                             |
                             `-> US3 policy/onboarding base
                                  `-> US3 evidence uses US2 lifecycle

US1 + US2 + US3 -> docs/distribution -> local battery -> real Gate 2 -> converge
```

### Within Each User Story

- Write tests and schema assertions before implementation.
- Implement Core model and validators before broker/UI adapters.
- Implement provider persistence before authority apply.
- Add audit/redaction before marking a provider outcome complete.
- Run targeted tests before editing promoted status/docs claims.

## Parallel Opportunities

### User Story 1

```text
T015/T016 policy tests
T017/T018 service tests
T019 broker tests
T020 Linux helper tests
T021 regression tests
```

After T022-T023 establish vocabulary, service work T024-T026 and helper/schema
work T027-T028 can proceed in parallel, then converge in T029-T034.

### User Story 2

```text
T035 grant-store tests
T036 liveness tests
T037 provider-limit tests
T038 generic lifecycle tests
T039 broker mapping tests
T042 route tests
T043 UI tests
```

Persistence T044-T045 can proceed alongside decision/store T046/T049-T050,
then provider injection/check-before-deny T047-T048 joins them.

### User Story 3

```text
T059 migration tests
T060 onboarding tests
T061 catalog tests
T062 doctor tests
T063 redaction tests
T064 evidence tests
T065 docs-truth tests
```

Migration T066-T068, onboarding T069-T070, doctor T071, and evidence T073 use
different primary files and can proceed after shared US1 vocabulary exists.

## Implementation Strategy

### MVP First

Complete Setup, Foundational, and US1. At that checkpoint Hideout can expose an
explicit coarse namespace without broadening content authority. Do not claim
read approval until US2 proves same-running-session activation.

### Incremental Delivery

1. Deliver policy/namespace/typed errno with legacy compatibility.
2. Deliver exact-file read provider and real cross-process retry.
3. Deliver explicit presets, migration, diagnostics, and honest claims.
4. Promote status only after distribution checks, Gate 0, and real Gate 2.

### Completion Discipline

- A checked task means its named behavior and tests exist, not merely a file or
  scaffold.
- Local-fast/not-run evidence cannot check T084 or promote real proof IDs.
- UI source grep cannot replace runtime reducer/route tests.
- Real Gate 2 must assert output values and failure modes, not only exit zero.
- Any approval that leaves the unchanged running target denied is incomplete.

## Phase 7: Adversarial Review Convergence

**Purpose**: Close the production-path gaps found by post-implementation
adversarial probes before retaining the Implemented claim.

- [X] T087 [US1] Fix discover-deny precedence so parent enumeration omits a denied node even when a separate content/stat grant remains usable, and replace the fixation assertion with positive omission plus direct-content regression coverage in `internal/hostfs/service.go` and `internal/hostfs/service_test.go`
- [X] T088 [US1] Enforce the shared categorized broad-discovery hidden roots for every effective discover policy, including manually authored `see-tree:$HOME`, with no second root list and with exact content authority preserved in `internal/hostfs/hostfs.go`, `internal/hostfs/visibility.go`, and `internal/hostfs/service_test.go`
- [X] T089 [US3] Make ordinary profile loading return a typed guided migration error for stored legacy list-only rules while keeping strict raw batch migration available, and remove the load-success fixation in `internal/profile/profile.go`, `internal/profile/profile_test.go`, and `internal/manager/profile_hostfs_test.go`
- [X] T090 [US1] Return typed unauthorized `EACCES` for write attempts inside an explicit discover domain even when an overlay store is present, while preserving legacy collapse outside that domain, in `internal/hostfs/service.go` and HostFS write tests
- [X] T091 [US2] Strip terminal control/format characters from untrusted read reasons, replace the scattered byte limit with a named constant, and add missing terminal-memory, explicit-read-deny/no-proposal, hidden-stat, and broker hidden-ENOENT regression assertions
- [X] T092 [P] Align stale selector documentation, migration guidance, spec status, STATUS evidence provenance, contracts, and test inventory with the corrected production behavior in `README.md`, `README.zh-CN.md`, `docs/`, and `specs/029-hostfs-discoverable-namespace/`
- [X] T093 [US1] Extend the local and real evidence lanes so manual broad discovery hides catalog roots and discover-denied content grants stay absent from parent enumeration without revoking direct exact access in `scripts/test-hostfs-visibility-e2e.sh` and `scripts/test-gate2-lima.sh`
- [X] T094 Run targeted tests, full static/Gate 0 checks, real macOS arm64 Lima Gate 2, digest verification, docs truth, and a final analyze/converge pass; promote completion only if all review probes and existing 20 assertions pass
- [X] T095 [US1] Preserve the hidden-path ENOENT contract for direct directory enumeration by short-circuiting `List` before exact-visible not-enumerable handling, add unit and real Gate 2 regression assertions, rerun Gate 0 and real evidence, and update provenance
