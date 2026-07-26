# Tasks: Ordinary User Release

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `specs/044-ordinary-user-release/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`,
`contracts/`, `quickstart.md`

**Tests**: Required. This feature touches network helper supply, package
ownership, diagnostic/export redaction, lifecycle, UI claims, and release
evidence. Tests precede implementation, new assertions require an observed red
mutation, and new judges require a firing negative fixture.

**Organization**: Tasks are grouped by user story so each journey remains
independently testable.

## Phase 1: Setup

**Purpose**: Establish traceability and the adversarial evidence ledger for the
implementation batch.

- [x] T001 Create the requirement-to-evidence and fresh-eyes report skeleton in `specs/044-ordinary-user-release/acceptance.md`
- [x] T002 [P] Add the local-fast/release-candidate runner argument and artifact contract skeleton in `scripts/test-ordinary-user-release.sh`

---

## Phase 2: Foundational

**Purpose**: Add shared validation scaffolding before user-facing behavior.

- [x] T003 Define 044 proof identifiers and claim mappings with registry tests in `internal/productevidence/ordinary_user_release.go`, `internal/productevidence/registry.go`, and `internal/productevidence/ordinary_user_release_test.go`
- [x] T004 Add the 044 product-evidence class to aggregate validation in `internal/productevidence/aggregate.go` and `internal/productevidence/ordinary_user_release_test.go`
- [x] T005 Add fail-closed completion-sentinel and candidate-identity helpers to `scripts/test-ordinary-user-release.sh` using `scripts/lib/gate-result.sh`

**Checkpoint**: Every delivered journey can now attach exact candidate identity,
mutation proof, negative fixture, and cleanup evidence to registered 044 proof
IDs.

---

## Phase 3: User Story 1 - Reach A First Useful Result (Priority: P1) 🎯 MVP

**Goal**: Prove the existing install/setup/direct-first-run journey using only
the exact package and commands printed by the product.

**Independent Test**: Install the candidate without Go/source state, complete
setup, readiness, and one real Lima project command, and verify honest boundary
and runtime progress wording.

### Tests for User Story 1

- [x] T006 [US1] Add exact-package clean-install, setup, no-VM/no-download, and printed-next-command assertions to `scripts/test-ordinary-user-release.sh`
- [x] T007 [US1] Add first-run runtime identity, declared-size, heartbeat, direct-network non-privacy, workspace, audit, and cleanup assertions to `scripts/test-first-run-e2e.sh`
- [x] T008 [US1] Record and execute a negative fixture that removes source/Go lookup and a mutation that corrupts one printed next command in `specs/044-ordinary-user-release/acceptance.md`

### Implementation for User Story 1

- [x] T009 [US1] Align setup success and first-run next-step presentation with the 044 CLI contract in `internal/app/setup.go`
- [x] T010 [US1] Align package caveats and canonical first-run commands in `packaging/homebrew/hideout.rb`, `README.md`, and `README.zh-CN.md`
- [x] T011 [US1] Document the exact four-command first-success path and runtime wait behavior in `docs/first-run-alpha.md`

**Checkpoint**: A clean package user can reach one real result without internal
architecture knowledge.

---

## Phase 4: User Story 2 - Find The Right Command Without Learning Internals (Priority: P1)

**Goal**: Provide concise primary help, successful contextual help, and a
complete explicit advanced index.

**Independent Test**: Locate setup, run, readiness, privacy, package lifecycle,
and support from primary/contextual help while verifying every old command
remains in `help --all`.

### Tests for User Story 2

- [x] T012 [US2] Add failing tests for concise primary help, complete `--all`, contextual topics, zero-write behavior, and `setup --help` exit 0 in `internal/app/help_test.go`
- [x] T013 [US2] Add a help-command inventory negative fixture and source-text mutation proof to `scripts/test-operator-cli-smoke.sh` and `specs/044-ordinary-user-release/acceptance.md`

### Implementation for User Story 2

- [x] T014 [US2] Implement primary, expanded, and contextual help routing in `internal/app/help.go`
- [x] T015 [US2] Replace monolithic top-level help dispatch with the new help provider and intercept setup help before operator-intent parsing in `internal/app/app.go`
- [x] T016 [US2] Add contextual privacy and package-manager lifecycle guidance in `internal/app/help.go`
- [x] T017 [US2] Synchronize help examples and boundary wording in `docs/first-run-alpha.md` and `docs/claim-boundaries.md`

**Checkpoint**: Primary help is short, contextual help is safe, and the full
command inventory is preserved.

---

## Phase 5: User Story 3 - Understand And Recover From Failure (Priority: P1)

**Goal**: Make default doctor output concise and actionable without weakening
the full report or repair boundary.

**Independent Test**: Compare healthy and representative failure summaries with
verbose/JSON output and verify all facts and recovery codes remain available.

### Tests for User Story 3

- [x] T018 [US3] Add failing unit tests for ready/attention/blocked summary projection, action filtering, line bounds, and source finding traceability in `internal/doctor/summary_test.go`
- [x] T019 [US3] Add failing CLI tests for default concise output, `--verbose`, deep/feature detail, unchanged JSON, and zero default `scripts/test-*` guidance in `internal/app/doctor_summary_test.go`
- [x] T020 [US3] Record an observed renderer mutation and maintainer-action negative fixture in `specs/044-ordinary-user-release/acceptance.md`

### Implementation for User Story 3

- [x] T021 [US3] Implement `ReadinessSummary` and concise rendering as a pure projection of `doctor.Report` in `internal/doctor/summary.go`
- [x] T022 [US3] Add explicit detailed-human selection and `--verbose` parsing/help while preserving JSON and repair behavior in `internal/app/app.go`
- [x] T023 [US3] Add ordinary-user recovery examples and detailed-mode escalation in `docs/first-run-alpha.md` and `docs/distribution-bootstrap.md`

**Checkpoint**: Default doctor answers “ready, attention, or blocked” with safe
next actions; full evidence is unchanged.

---

## Phase 6: User Story 4 - Create A Safe Support Report (Priority: P1)

**Goal**: Generate one bounded, inspectable, deterministically redacted support
report without raw audit or workspace data.

**Independent Test**: Generate and validate a report under healthy, damaged
package, missing setup, injected-secret, unsafe-path, overwrite, and oversized
fixtures.

### Tests for User Story 4

- [x] T024 [P] [US4] Add the strict support-report JSON Schema and positive/negative schema fixtures in `schemas/support-report.schema.json` and `internal/supportreport/schema_test.go`
- [x] T025 [P] [US4] Add failing model, size-bound, deterministic redaction, collection-failure, and no-raw-data tests in `internal/supportreport/report_test.go`
- [x] T026 [US4] Add failing safe-output tests for symlink, unsafe parent, existing file, atomic mode-0600 write, and cleanup in `internal/supportreport/write_test.go`
- [x] T027 [US4] Add failing CLI tests for help, explicit output, source/package modes, no upload, and no store mutation in `internal/app/support_report_test.go`
- [x] T028 [US4] Add injected token, proxy, host path, workspace content, and oversized negative fixtures plus one redaction mutation proof to `scripts/test-ordinary-user-release.sh` and `specs/044-ordinary-user-release/acceptance.md`

### Implementation for User Story 4

- [x] T029 [US4] Implement the strict bounded support-report model and validator in `internal/supportreport/report.go`
- [x] T030 [US4] Implement read-only binary/support/package/doctor/recovery collection in `internal/supportreport/collect.go`
- [x] T031 [US4] Implement safe explicit atomic output with no-overwrite default in `internal/supportreport/write.go`
- [x] T032 [US4] Implement `hideout support report --out <path>` in `internal/app/support_report.go` and route it from `internal/app/app.go`
- [x] T033 [US4] Document local-only collection, inspection, exclusions, and issue-report usage in `README.md`, `README.zh-CN.md`, and `docs/first-run-alpha.md`

**Checkpoint**: One command creates one bounded shareable report and adversarial
scans find no protected material.

---

## Phase 7: User Story 5 - Use Privacy Without Assembling Hidden Helpers (Priority: P1)

**Goal**: Build and distribute the tested Linux guest privacy helper as an
attributed, verified package-owned artifact.

**Independent Test**: Build/install the exact package with no ambient helper,
verify provenance/license/digest, run privacy through the package helper, and
fail closed for damage or invalid override.

### Tests for User Story 5

- [x] T034 [P] [US5] Add package manifest, helper manifest, license/notice, executable, removal, damage, and wrong-target tests in `scripts/test-package-smoke.sh`
- [x] T035 [P] [US5] Add resolver provenance and invalid-explicit-override fail-closed tests in `internal/helperbin/helperbin_test.go`
- [x] T036 [US5] Add package-owned privacy diagnostic tests and remove the external-prerequisite expectation in `internal/packagekit/packagekit_test.go` and `internal/app/app_test.go`
- [x] T037 [US5] Add exact-package Gate 3 provenance and no-ambient-helper assertions plus a digest mutation fixture in `scripts/test-gate3-hidden-proxy.sh`
- [x] T038 [US5] Record the helper-removal mutation, invalid-override negative fixture, and license review evidence in `specs/044-ordinary-user-release/acceptance.md`

### Implementation for User Story 5

- [x] T039 [US5] Add the pinned v2.6.0 isolated build graph in `tools/tun2socks-build/go.mod` and `tools/tun2socks-build/go.sum`
- [x] T040 [US5] Add the upstream MIT license and component/dependency attribution in `third_party/tun2socks/LICENSE` and `THIRD_PARTY_NOTICES.md`
- [x] T041 [US5] Build the Linux guest helper and write a provenance manifest during local/package staging in `scripts/install-local.sh`
- [x] T042 [US5] Require and inventory the helper and license during package finalization in `scripts/package-local.sh`
- [x] T043 [US5] Resolve installed package ownership and explicit development overrides with fail-closed provenance in `internal/helperbin/helperbin.go`
- [x] T044 [US5] Report package-owned, override, missing, damaged, and incompatible states in `internal/packagekit/prereq.go` and doctor packaging diagnostics in `internal/app/app.go`
- [x] T045 [US5] Remove the fulfilled helper-packaging debt and document the remaining operator proxy/resolver boundary in `docs/DEBT.md`, `docs/distribution-bootstrap.md`, and `docs/first-run-alpha.md`

**Checkpoint**: Privacy still requires an operator proxy, but no supported user
must find or compile the guest helper.

---

## Phase 8: User Story 6 - Upgrade Or Remove Without Losing Work (Priority: P2)

**Goal**: Make provider-owned update/repair/removal and durable-state behavior
obvious and re-prove it against the new candidate.

**Independent Test**: Upgrade a prior fixture, repair package damage, uninstall
normally, and explicitly purge while checking durable and unrelated files.

### Tests for User Story 6

- [x] T046 [US6] Extend package smoke with prior-version upgrade, helper addition migration, unrelated-file preservation, Homebrew guidance, uninstall preservation, and purge preview assertions in `scripts/test-package-smoke.sh`
- [x] T047 [US6] Add a destructive-scope negative fixture and installed-state preservation mutation proof to `scripts/test-doctor-package-recovery-e2e.sh` and `specs/044-ordinary-user-release/acceptance.md`

### Implementation for User Story 6

- [x] T048 [US6] Add installation-provider-specific update, repair, uninstall, and purge guidance to `internal/app/help.go` and package command help in `internal/app/app.go`
- [x] T049 [US6] Update Homebrew caveats and standalone lifecycle documentation in `packaging/homebrew/hideout.rb` and `docs/distribution-bootstrap.md`
- [x] T050 [US6] Align preserved-state and explicit-purge messaging in `README.md`, `README.zh-CN.md`, and `docs/claim-boundaries.md`

**Checkpoint**: Package ownership and durable-state outcomes are clear before
any lifecycle action.

---

## Phase 9: User Story 7 - Publish One Exact Self-Service Candidate (Priority: P1)

**Goal**: Bind every 044 journey and required release gate to one retained
candidate and publish only from a validated receipt.

**Independent Test**: Run local-fast negative fixtures, then freeze one clean
public package and complete exact-package Gate 2, Gate 3, UI, release readiness,
signing, notarization, anonymous download, and publication receipt.

### Tests for User Story 7

- [x] T051 [US7] Complete local-fast and release-candidate evidence generation, schema validation, redaction, cleanup inventory, and required-journey rejection in `scripts/test-ordinary-user-release.sh`
- [x] T052 [US7] Add the 044 runner to Gate 0 and release-candidate orchestration in `scripts/test-gate0.sh` and `scripts/test-public-alpha-candidate.sh`
- [x] T053 [US7] Add stale digest, dirty tree, private/unpushed commit, rebuilt bytes, missing UI, failed/not-run Gate 2/3, signing, notarization, and anonymous-download negative fixtures in `scripts/test-release-readiness.sh`
- [x] T054 [US7] Run and record all new assertion mutations, judge negative fixtures, fresh-eyes findings, and exact commands/artifacts in `specs/044-ordinary-user-release/acceptance.md`

### Implementation for User Story 7

- [x] T055 [US7] Integrate 044 registered proofs into release readiness and public evidence aggregation in `internal/releasecompat/readiness.go` and `internal/productevidence/ordinary_user_release.go`
- [x] T056 [US7] Update required ordinary-user gates and exact-package commands in `docs/privacy-run-test-plan.md` and `docs/support-matrix.md`
- [x] T057 [US7] Update implementation status, claims/non-claims, and deferred ledger after evidence exists in `docs/STATUS.md`, `docs/claim-boundaries.md`, `docs/threat-model.md`, and `docs/DEBT.md`
- [ ] T058 [US7] Run Gate 0, clean exact-package Gate 2, clean exact-package Gate 3, required UI E2E, and release-readiness; record retained evidence paths and identities in `specs/044-ordinary-user-release/acceptance.md`
- [ ] T059 [US7] Push the clean candidate commit and require public CI success before signing in `specs/044-ordinary-user-release/acceptance.md`
- [ ] T060 [US7] Sign, notarize, anonymously verify, and publish the retained candidate through the existing 033 workflow; validate the publication receipt in `specs/044-ordinary-user-release/acceptance.md`
- [ ] T061 [US7] Render the validated receipt into `releases/current.json`, `CHANGELOG.md`, `README.md`, `README.zh-CN.md`, `docs/STATUS.md`, `docs/support-matrix.md`, and `packaging/homebrew/hideout.rb`

**Checkpoint**: The public download is the exact candidate that passed every
ordinary-user and security gate.

---

## Phase 10: Polish & Cross-Cutting Concerns

- [x] T062 Run `gofmt`, shell syntax checks, schema validation, focused unit/race tests, and docs truth across all changed files
- [x] T063 Audit every 044 functional requirement and success criterion against authoritative code, test, package, gate, signature, notarization, download, cleanup, and receipt evidence in `specs/044-ordinary-user-release/acceptance.md`
- [ ] T064 Mark `specs/044-ordinary-user-release/spec.md` implemented and close all tasks only after T063 finds no missing, contradictory, weak, or indirect evidence

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Phase 1 and blocks evidence promotion.
- **US1 first result**: Depends on Phase 2; reuses existing 038 behavior.
- **US2 help**: Depends on Phase 2 and can proceed independently of US3-US6.
- **US3 doctor**: Depends on Phase 2 and the existing doctor report.
- **US4 support report**: Depends on Phase 2 and consumes the full doctor report;
  it does not depend on the concise renderer.
- **US5 privacy helper**: Depends on Phase 2 and can proceed independently of
  US2-US4.
- **US6 package lifecycle**: Depends on US5 because the upgrade fixture must
  prove addition/removal of the new package-owned helper.
- **US7 release**: Depends on US1-US6.
- **Polish**: Depends on all stories and real/public evidence.

### User Story Dependencies

```text
Foundation
├── US1 first result ───────────────┐
├── US2 help ──────────────────────┤
├── US3 doctor ────────────────────┤
├── US4 support report ────────────┤
└── US5 privacy helper -> US6 lifecycle
                                   └──> US7 exact candidate -> Polish
```

### Within Each User Story

- Write tests first and observe failure.
- Implement models/validators before CLI routing or shell integration.
- Run focused tests after each implementation task.
- Temporarily break every new assertion and record the red observation.
- Run every new judge against a negative fixture.
- Mark tasks complete only with a linked authoritative observation.

### Parallel Opportunities

- US2 help, US3 doctor, US4 support model/schema, and US5 isolated helper-build
  work touch separate files after Phase 2.
- Within US4, schema/model tests can be written independently from CLI tests.
- Within US5, isolated module/license work can proceed independently from
  resolver tests.
- Documentation files that overlap must be updated sequentially during final
  convergence.

## Parallel Example

```text
Task: T012 help contract tests in internal/app/help_test.go
Task: T018 doctor summary tests in internal/doctor/summary_test.go
Task: T024 support schema tests in internal/supportreport/schema_test.go
Task: T035 helper provenance tests in internal/helperbin/helperbin_test.go
```

## Implementation Strategy

### MVP First

1. Complete traceability and proof registration.
2. Re-prove the existing install/setup/direct-first-run path.
3. Deliver concise/contextual help.
4. Deliver concise doctor.
5. Validate a clean package user can reach one useful result.

### Self-Service Increment

1. Add the bounded support report.
2. Package the pinned privacy helper and close its debt.
3. Re-prove upgrade, repair, uninstall, and purge.
4. Run the local aggregate and all mutation/negative fixtures.

### Release Increment

1. Freeze one clean public candidate.
2. Run exact-package Gate 2, Gate 3, and required UI E2E.
3. Run release readiness.
4. Push and require public CI.
5. Sign, notarize, verify anonymous bytes, and publish.
6. Render only from the validated publication receipt.

## Notes

- `[P]` means different files with no incomplete dependency.
- A checked task requires evidence, not only an implementation diff.
- External publication tasks remain incomplete until their real public state is
  observed.
- Do not update `releases/current.json` or the published release blocks before a
  validated receipt exists.
- Do not mark 044 implemented from local-fast, native, dirty, or `not-run`
  evidence.
