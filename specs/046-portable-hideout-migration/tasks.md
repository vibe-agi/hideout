<!-- markdownlint-disable MD013 -->

# Tasks: Portable Hideout Migration

**Input**: Design documents from
`/specs/046-portable-hideout-migration/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`,
`contracts/`, and `quickstart.md`

**Tests**: Required. This feature handles complete guest disks, host/backend
lifecycle, secrets, paths, authority proposals, identity, daemon recovery, and
release evidence. Each boundary needs positive, fail-closed, redaction, mutation,
and interruption coverage before implementation is accepted.

**Organization**: Tasks are grouped by user story. Setup and Foundation establish
the format, formal invariants, and Manager/provider authority boundary used by all
stories.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: May run in parallel after its stated prerequisites because it edits
  different files and does not depend on an unfinished sibling task.
- **[Story]**: Maps the task to a user story in `spec.md`.
- Every task names its concrete file path.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish versioned artifacts and the one new production dependency.

- [x] T001 Pin `github.com/klauspost/compress/zstd` and record its checksums in `go.mod` and `go.sum`
- [x] T002 [P] Define the strict v1 encrypted inventory schema in `schemas/migration-manifest.schema.json`
- [x] T003 [P] Define export/import request and immutable plan schemas in `schemas/migration-plan.schema.json`
- [x] T004 [P] Define the data-only adoption request/receipt schema in `schemas/migration-receipt.schema.json`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Prove the state model and implement bounded, backend-neutral
infrastructure before any authority-bearing journey.

**⚠️ CRITICAL**: No user story implementation starts until this phase passes
Gate 0.

### Formal preflight

- [x] T005 [P] Model stopped proof, snapshot ownership, authenticated checkpoints, sealing, crash, cancel, resume, and source immutability in `formal/MigrationBundle.tla`
- [x] T006 [P] Model two-plus imports, claims, Safe Clone, Exact Guest Restore, authority review, adoption, commit, rollback, crash, and recovery in `formal/MigrationAdoption.tla`
- [x] T007 Add focused safety/liveness configs for both models in `formal/MigrationBundle.cfg`, `formal/MigrationAdoption.cfg`, and `formal/cfg/Migration*Liveness.cfg`
- [x] T008 Register every new TLC config and invariant in `formal/inventory.json` and update counts/claims in `docs/formal-models.md`
- [x] T009 Implement production-shaped pure transition types plus TLA+ refinement tests in `internal/migration/state.go` and `internal/migration/state_refinement_test.go`

### Portable core and authority boundary

- [x] T010 Define bundle IDs, records, manifests, limits, identity policies, drafts, plans, checkpoints, and stable errors in `internal/migration/types.go`, `internal/migration/limits.go`, and `internal/migration/errors.go`
- [x] T011 [P] Write known-answer, wrong-key, nonce, KDF-bound, and redaction tests in `internal/migration/crypto_test.go`
- [x] T012 Implement Argon2id key wrapping, HKDF separation, XChaCha20-Poly1305 records, and replaceable-buffer cleanup in `internal/migration/crypto.go`
- [x] T013 [P] Write framing, footer, sparse extent, hostile length, ordering, and exact decompression tests in `internal/migration/format_test.go`
- [x] T014 Implement the bounded append-only reader/writer and canonical logical digest in `internal/migration/format.go`, `internal/migration/reader.go`, and `internal/migration/writer.go`
- [x] T015 Add hostile-input fuzz targets and seed corpus for header, frame, manifest, footer, and zstd parsing in `internal/migration/fuzz_test.go` and `internal/migration/testdata/fuzz/`
- [x] T016 Define the optional typed migration capability interface in `internal/backend/migration.go` without enlarging the base backend interface
- [x] T017 Define durable export/import operations, effect bindings, claims, commit decision, and validation in `internal/manager/migration_operation.go` and `internal/manager/migration_operation_test.go`
- [x] T018 Implement the operation store and deterministic claim acquisition/release in `internal/manager/migration_store.go` and `internal/manager/migration_store_test.go`
- [x] T019 Implement bounded one-shot migration secret-input handles in `internal/manager/migration_secret_input.go` and `internal/manager/migration_secret_input_test.go`
- [x] T020 Define shared progress, audit events, receipts, API redaction, and stable error projection in `internal/manager/migration_projection.go` and `internal/manager/migration_projection_test.go`
- [x] T021 Add schema, fuzz, formal, refinement, and portable-core checks to `scripts/gates/migration.sh` and invoke them from `scripts/test-gate0.sh`
- [x] T022 Run `scripts/gates/migration.sh` and `scripts/gates/formal.sh`, recording zero model, refinement, schema, fuzz-smoke, or redaction failures in `docs/STATUS.md`

**Checkpoint**: The hostile-input parser, formal invariants, durable operation
contract, and optional provider boundary are ready. No VM migration is claimed.

---

## Phase 3: User Story 1 — Continue A Setup On Another Computer (P1) 🎯 MVP

**Goal**: Export one compatible stopped Lima environment with all persistent
disks, import the unchanged bundle under a fresh destination identity, and run it
with its guest files intact.

**Independent Test**: Use independent source/destination stores, persist root and
attached-disk fixtures, export/import under Safe Clone, and prove file fidelity,
fresh Hideout/backend/guest identities, source immutability, readiness, and no
automatic start.

### Tests for User Story 1

- [x] T023 [P] [US1] Add Manager export/import plan/apply contract tests for the compatible no-conflict journey in `internal/manager/migration_api_test.go`
- [x] T024 [P] [US1] Add Safe Clone and Exact Guest Restore helper fixtures and policy tests in `cmd/hideout-migration-adopt/main_test.go`
- [x] T025 [P] [US1] Add source immutability, stopped-state, shared-disk closure, and idempotency provider tests in `internal/backend/lima/migration_test.go`
- [x] T026 [P] [US1] Add a package-candidate two-store full-state scenario with persistent root/attached-disk hashes in `scripts/gates/migration-lima.sh`

### Implementation for User Story 1

- [x] T027 [US1] Implement runtime Lima version/layout/helper capability probing in `internal/backend/lima/migration_capability.go`
- [x] T028 [US1] Implement exact stopped-incarnation and attached-disk graph inspection in `internal/backend/lima/migration_source.go`
- [x] T029 [US1] Implement operation-owned COW root and attached-disk snapshots without source writes or boots in `internal/backend/lima/migration_snapshot.go`
- [x] T030 [US1] Implement bounded sparse extent iteration for provider snapshot components in `internal/backend/lima/migration_reader.go`
- [x] T031 [US1] Implement fresh opaque destination staging, disk materialization, digest proof, and normalized config generation in `internal/backend/lima/migration_stage.go`
- [x] T032 [US1] Implement the strict Linux Safe Clone/Exact Guest Restore helper in `cmd/hideout-migration-adopt/main.go`
- [x] T033 [US1] Package and checksum the Linux adoption helper in `internal/helperbin/manifest.go` and the existing package manifests/scripts
- [x] T034 [US1] Package and probe a no-network Lima/VZ adoption executor, then implement nonce-bound request/receipt transport, shutdown proof, and channel removal in `internal/backend/lima/migration_adopt.go`; stock Lima's default user network MUST keep full import disabled
- [x] T035 [US1] Implement destination disk/config/identity/adoption verification in `internal/backend/lima/migration_verify.go`
- [x] T036 [US1] Implement ownership-proved staged-object and snapshot cleanup in `internal/backend/lima/migration_cleanup.go`
- [x] T037 [US1] Implement Manager export planning/apply and source snapshot orchestration in `internal/manager/migration_export.go`
- [x] T038 [US1] Implement Manager import planning/apply, fresh control/backend identity generation, and visibility commit in `internal/manager/migration_import.go`
- [x] T039 [US1] Implement daemon-owned long-running export/import workers and durable event publication in `internal/daemon/migration.go`
- [x] T040 [US1] Add authenticated `migration/*/plan`, `migration/*/apply`, capability, and operation routes in `internal/manager/routes.go` and `internal/manager/api.go`
- [x] T041 [US1] Add basic `migrate export`, `inspect`, `import`, and `status` dispatch/client behavior in `internal/app/migrate.go` and `internal/app/terminal_client.go`
- [x] T042 [US1] Implement the backend-independent config-only provider path in `internal/backend/native/migration.go`
- [x] T043 [US1] Complete the Lima gate's same-bundle two-destination Safe Clone and Exact Guest Restore assertions in `scripts/gates/migration-lima.sh`

**Checkpoint**: A supported package candidate can complete the core full-state
journey. Unsupported providers/layouts offer only explicit config-only behavior.

---

## Phase 4: User Story 2 — Inspect And Rebind Before Changes (P1)

**Goal**: Inspect without mutation, resolve destination conflicts/mappings, and
confirm an immutable plan whose imported authority remains disabled by default.

**Independent Test**: Inspect a valid bundle against a destination with a name
collision, missing workspace path, locked secret provider, and unavailable host
prerequisite; prove zero mutation and exact blockers/proposals/remediation.

### Tests for User Story 2

- [x] T044 [P] [US2] Add read-only inspection and zero-side-effect assertions in `internal/manager/migration_inspect_test.go`
- [x] T045 [P] [US2] Add traversal, reserved-root, alias, name-race, stale-plan, and disabled-authority tests in `internal/manager/migration_plan_test.go`
- [x] T046 [P] [US2] Add incompatible backend/version/architecture/space fixtures in `internal/backend/lima/migration_compatibility_test.go`

### Implementation for User Story 2

- [x] T047 [US2] Implement sealed bundle inspection and redacted inventory projection in `internal/migration/inspect.go` and `internal/manager/migration_inspect.go`
- [x] T048 [US2] Implement mutable import drafts and immutable digest-bound plans in `internal/manager/migration_plan.go`
- [x] T049 [US2] Implement compatibility, staging, rollback, and final-space preflight in `internal/manager/migration_compatibility.go`
- [x] T050 [US2] Implement canonical workspace/HostFS mapping with real-identity and reserved-root checks in `internal/manager/migration_paths.go`
- [x] T051 [US2] Implement secret availability/rebind/import proposals without value projection in `internal/manager/migration_secrets.go`
- [x] T052 [US2] Implement disabled-by-default host-app, endpoint, network, mount, command, script, and pack proposals in `internal/manager/migration_authority.go`
- [x] T053 [US2] Implement refuse-by-default rename/replace conflict plans and separate destructive replacement recovery in `internal/manager/migration_conflicts.go`
- [x] T054 [US2] Add inspect/import-plan request decoding and redacted responses in `internal/manager/migration_api.go` and `internal/manager/routes.go`
- [x] T055 [US2] Add CLI preview, name/path/secret/policy mapping, proposal approval, and typed acknowledgement flags in `internal/app/migrate.go`
- [x] T056 [US2] Add the shared inventory/plan/blocker projection to `internal/liveconsole/migration_projection.go`
- [x] T057 [US2] Add mutation tests proving inspect/preview create no Manager, Keychain, backend, profile, or path effects in `scripts/gates/migration.sh`

**Checkpoint**: Inspection is safe on hostile inputs; every destination-specific
decision is visible, validated, and bound before any effect.

---

## Phase 5: User Story 3 — Clear Privacy And Portability Scope (P2)

**Goal**: Make config/full scope, explicit selection, exclusions, secret handling,
and opaque guest-disk sensitivity truthful and testable.

**Independent Test**: Export config-only and full bundles from the same source and
prove their authenticated inventories contain exactly the selected portable
categories and none of the named exclusions/default secret values.

### Tests for User Story 3

- [x] T058 [P] [US3] Add normalized profile/config round-trip and exclusion tests in `internal/migration/config_test.go`
- [x] T059 [P] [US3] Add default-ref-only, selected-value, nonexportable-provider, and sentinel-redaction tests in `internal/manager/migration_secret_transfer_test.go`
- [x] T060 [P] [US3] Add explicit `--all` inventory and noninteractive ambiguity refusal tests in `internal/app/migrate_test.go`

### Implementation for User Story 3

- [x] T061 [US3] Implement strict portable profile/environment normalization and schema upgrade rules in `internal/migration/config.go`
- [x] T062 [US3] Build authenticated included/excluded category inventory and size estimates in `internal/manager/migration_inventory.go`
- [x] T063 [US3] Implement reference-only default and explicitly selected encrypted secret records in `internal/manager/migration_secret_export.go`
- [x] T064 [US3] Write selected values directly to fresh operation-owned Keychain refs on import in `internal/manager/migration_secret_import.go`
- [x] T065 [US3] Add opaque guest-content sensitivity acknowledgement to export plans and receipts in `internal/manager/migration_export.go`
- [x] T066 [US3] Implement config-only export/import independent of persistent-disk capability in `internal/manager/migration_config_only.go`
- [x] T067 [US3] Render concrete selection and named exclusions for interactive and JSON CLI in `internal/app/migrate.go`
- [x] T068 [US3] Add config/full inventory, secret sentinel, and excluded-host-data checks to `scripts/gates/migration.sh`

**Checkpoint**: “Full” and “all” are concrete inventories, not implied authority;
config-only is a useful independently testable product slice.

---

## Phase 6: User Story 4 — Resume Or Recover Safely (P2)

**Goal**: Resume verified work after interruption and deterministically finish or
roll back without a runnable partial environment or duplicated durable effect.

**Independent Test**: Crash after every durable export/import phase, restart the
daemon boundary, resume/recover the same operation, and prove exactly one terminal
result and zero duplicate environments, secrets, approvals, or provider objects.

### Tests for User Story 4

- [X] T069 [P] [US4] Add record-boundary tear, checkpoint substitution, resume cursor, and sealed immutability tests in `internal/migration/resume_test.go`
- [x] T070 [P] [US4] Add effect-level crash/restart/commit/rollback table tests in `internal/manager/migration_recovery_test.go`
- [x] T071 [P] [US4] Add provisional Keychain and provider cleanup fault-injection tests in `internal/manager/migration_cleanup_test.go`

### Implementation for User Story 4

- [X] T072 [US4] Implement encrypted checkpoint emission, tail authentication/truncation, and export resume in `internal/migration/resume.go`
- [x] T073 [US4] Implement durable component cursors and digest-safe import resume in `internal/manager/migration_import.go`
- [x] T074 [US4] Implement one-way activation decision and at-most-once effect reconciliation in `internal/manager/migration_recovery.go`
- [x] T075 [US4] Reconcile nonterminal migration operations during daemon startup in `internal/daemon/migration_recovery.go`
- [x] T076 [US4] Implement safe-boundary cancellation and explicit retain/remove choices in `internal/manager/migration_cancel.go`
- [x] T077 [US4] Implement reverse-order secret/provider/staging compensation and retained-object evidence in `internal/manager/migration_cleanup.go`
- [x] T078 [US4] Add status/resume/cancel/recover Manager routes and current-revision action validation in `internal/manager/migration_api.go`
- [x] T079 [US4] Add `migrate resume`, `cancel`, and `recover` commands with protected re-unlock in `internal/app/migrate.go`
- [x] T080 [US4] Add every durable crash cut, daemon restart, and model-refinement trace to `scripts/gates/migration.sh` and `scripts/gates/migration-lima.sh`

**Checkpoint**: Laptop/daemon interruption is an ordinary recoverable state, not a
reason to restart large verified work or expose partial state.

---

## Phase 7: User Story 5 — Same Understandable Flow Everywhere (P3)

**Goal**: Deliver one beginner-readable Manager-backed workflow through CLI, TUI,
WebUI, and automation with equivalent plans and concrete progress.

**Independent Test**: Enter identical choices through every surface and assert the
same plan digest, blockers, effects, confirmations, progress classification, and
terminal receipt.

### Tests for User Story 5

- [x] T081 [P] [US5] Add shared command-help examples, flag relationships, and nonterminal refusal tests in `internal/app/migrate_help_test.go`
- [x] T082 [P] [US5] Add TUI keyboard/modal/narrow-layout migration tests in `internal/tui/migration_test.go`
- [x] T083 [P] [US5] Add WebUI no-local-storage, dialog, redaction, and Manager-parity tests in `internal/daemon/uiweb_migration_test.go`

### Implementation for User Story 5

- [x] T084 [US5] Add concise and expanded migration entries/examples to `internal/app/command_catalog.go`, `internal/app/help.go`, and `internal/operatorhelp/`
- [x] T085 [US5] Add typed migration actions and time-series progress to `internal/liveconsole/migration_actions.go` and `internal/liveconsole/migration_projection.go`
- [x] T086 [US5] Implement the three-pane/collapsed TUI migration HUD in `internal/tui/migration.go`
- [x] T087 [US5] Implement Enter-edit path/secret/identity/authority modals in `internal/tui/modal/migration.go`
- [x] T088 [US5] Add embedded WebUI migration inventory, review, progress, and recovery presentation in `internal/daemon/uiweb_assets/migration.js`, `internal/daemon/uiweb_assets/index.html`, and `internal/daemon/uiweb_assets/style.css`
- [x] T089 [US5] Add the WebUI migration API client without URL/storage secret persistence in `internal/daemon/uiweb_assets/client.js`
- [x] T090 [US5] Enforce two-second progress refresh, explicit unknown totals/ETA, and plain-language next actions in `internal/manager/migration_projection.go`
- [x] T091 [US5] Add cross-surface golden plan/receipt parity checks in `internal/manager/migration_surface_parity_test.go`

**Checkpoint**: A first-time user and an automation client use the same meanings
and authority; the TUI looks and behaves like an actionable HUD.

---

## Phase 8: Polish & Cross-Cutting Release Evidence

**Purpose**: Harden the complete boundary and establish honest release status.

- [x] T092 [P] Update architecture, current status, and operator migration guide in `docs/privacy-run-design.md`, `docs/STATUS.md`, and `docs/migration.md`
- [x] T093 [P] Document bundle/guest-data threats, exact-identity nonclaims, passphrase limits, and local-owner assumptions in `docs/threat-model.md`
- [x] T094 [P] Document Gate 0, native, Lima, crash-cut, mutation, physical-host, and performance evidence in `docs/privacy-run-test-plan.md`
- [x] T095 [P] Update formal inventory counts, invariants, liveness properties, and refinement mappings in `docs/formal-models.md`
- [x] T096 Integrate the adoption helper, schemas, and embedded WebUI assets into package manifests, license inventories, signing, repair, and package-candidate verification under `internal/packagekit/` and `scripts/`
- [x] T097 Expand fuzz corpus and mutation tests for wrong key, truncation, duplication, reordering, sparse abuse, traversal, special files, expansion, and trailing content in `internal/migration/testdata/` and `scripts/gates/migration.sh`
- [x] T098 Run targeted race tests for Manager migration services, daemon workers, projections, and secret handles and fix failures in `internal/manager/`, `internal/daemon/`, and `internal/liveconsole/`
- [ ] T099 Run Gate 0 and package-candidate real Lima migration gates and record exact artifact/helper digests in `docs/STATUS.md` (Gate 0 passed on the current dirty diagnostic tree; exact clean-candidate Lima evidence remains pending)
- [ ] T100 Execute `quickstart.md` on three independent stores and two physical destination Macs, recording file/identity/bundle-digest evidence in the release evidence directory named by `docs/privacy-run-test-plan.md`
- [ ] T101 On a quiet host, measure migration-process CPU, I/O, peak memory, throughput, sparse preservation, and host-noise evidence in `scripts/gates/migration-performance.sh`
- [x] T102 Review stable error/help/redaction output for credentials and raw guest data, adding sentinels to `internal/manager/migration_projection_test.go` and `internal/app/migrate_help_test.go`
- [x] T103 Run `gofmt`, `go vet`, targeted/full `go test`, shellcheck, markdownlint, formal inventory, schema generation, and `git diff --check`; record the final supported/non-supported matrix in `docs/STATUS.md`

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: Starts immediately. T002–T004 may run in parallel.
- **Foundational (Phase 2)**: Depends on Setup. T005 and T006 may run in
  parallel; T007–T009 depend on both. Bundle crypto/format tests precede their
  implementations. Phase 2 blocks all user stories.
- **US1 (Phase 3)**: Starts after Foundation and delivers the compatible
  no-conflict full-state MVP.
- **US2 (Phase 4)**: Starts after Foundation. Its inspection/planning core is
  independently testable; T054–T057 integrate with US1 routes and workers.
- **US3 (Phase 5)**: Starts after Foundation. Config normalization and scope tests
  can proceed alongside US1/US2; secret apply integration uses US1 operations.
- **US4 (Phase 6)**: Formal recovery is foundational, but executable recovery
  depends on US1 workers/provider effects and US3 provisional secrets.
- **US5 (Phase 7)**: Help and presentation tests can start after Manager schemas
  stabilize; final parity depends on US1–US4 projections/actions.
- **Polish (Phase 8)**: Depends on every story included in the release claim.

### User story dependencies

```text
Foundation
├── US1 Core full-state journey (MVP)
├── US2 Read-only inspection and rebinding
└── US3 Scope, exclusions, and optional secrets

US1 + US3 ──> US4 Crash recovery and cleanup
US1 + US2 + US3 + US4 ──> US5 Cross-surface parity
All selected stories ──> Release evidence
```

US2's read-only inspect slice and US3's config-only slice can ship behind an
explicit experimental capability before Lima full-state promotion. Neither may be
described as proof that VM disks are portable.

### Within each user story

- Write positive and fail-closed/redaction tests first and observe the intended
  failure.
- Implement pure models/validators before Manager effects.
- Implement Manager plan/review before apply.
- Implement provider staging before adoption/activation.
- Add API projection before CLI/TUI/WebUI integration.
- Finish the story's independent gate before marking its tasks complete.

## Parallel Opportunities

- T002, T003, and T004 can run in parallel.
- T005 and T006 can run in parallel, then converge at T007–T009.
- T011 and T013 can prepare crypto/format tests in parallel before T012/T014.
- US1 helper work T024/T032/T033 can run alongside Lima source capture
  T025/T027–T030 after the provider contract exists.
- US2 inspection T044/T047 and destination planning T045/T048–T053 can run in
  parallel before API/CLI integration.
- US3 config T058/T061 and secret T059/T063–T064 tracks can run in parallel.
- US4 bundle resume T069/T072 and Manager recovery T070–T071/T073–T077 can run in
  parallel before crash-gate convergence.
- US5 CLI help, TUI, and WebUI tests/implementation are separate file tracks and
  converge in T091.

## Parallel Example: Formal Preflight

```text
Task T005: Model immutable export/snapshot/seal/recovery in MigrationBundle.tla
Task T006: Model multi-destination identity/adoption/recovery in MigrationAdoption.tla

Then:
Task T007: Add safety/liveness configurations
Task T008: Register inventory/docs
Task T009: Add production-shaped Go refinement
```

## Parallel Example: User Story 1

```text
Track A: T025 -> T027 -> T028 -> T029 -> T030
Track B: T024 -> T032 -> T033
Track C: T023 -> T037 -> T038 -> T039 -> T040 -> T041

Converge: T031 -> T034 -> T035 -> T036 -> T043
```

## Implementation Strategy

### Formal-first foundation

1. Finish Setup.
2. Run both TLA+ models before product effects exist.
3. Implement pure Go transition/refinement code and make Gate 0 compare it with
   the modeled invariants.
4. Implement bundle crypto/parser and Manager/provider contracts.
5. Stop if any invariant requires an authority or identity claim the backend
   cannot prove.

### MVP first

1. Complete Phases 1 and 2.
2. Complete US1 for one compatible stopped Lima environment and Safe Clone.
3. Validate file fidelity, source immutability, fresh identity, and package shape.
4. Keep the capability experimental until US2/US4 fail-closed/recovery work and
   physical-host evidence are complete.

### Incremental delivery

1. Foundation → hostile-input-safe, modeled format and operations.
2. US1 → compatible no-conflict full-state path.
3. US2 → safe inspection, mappings, conflicts, and authority review.
4. US3 → truthful config/full scopes and opt-in secrets.
5. US4 → ordinary interruption/recovery semantics.
6. US5 → CLI/TUI/WebUI parity and beginner-grade operation.
7. Polish → package, physical-host, performance, and release evidence.

## Notes

- `[P]` never means two tasks may edit the same file concurrently.
- Full-state support remains unavailable until the Lima runtime probe and real
  package-candidate gate prove the exact provider version/layout.
- Native tests prove only config/operation mechanics.
- The sealed bundle is never a cleanup target for import.
- Exact Guest Restore preserves guest identity but never preserves Hideout control
  identity and never claims disconnected-source retirement.
- Performance work waits for a stable host and reports migration-process evidence
  separately from unrelated host noise.

## Phase 9: Convergence

- [x] T104 CRITICAL: Record the explicitly deferred migration performance qualification, release non-claim, stable-host trigger, and closure evidence in `docs/DEBT.md`, `docs/STATUS.md`, and `docs/privacy-run-test-plan.md` per Constitution Development Workflow (missing)
- [x] T105 Implement backend-independent configuration-only export/import, including the native provider harness, authenticated inventory, CLI flow, and tests in `internal/backend/native/`, `internal/manager/`, `internal/app/`, and `scripts/gates/migration.sh` per FR-002 and US3/AC1 (missing)
- [x] T106 Add a migration-owned review/confirm/apply action that stops eligible selected environments, proves exact quiescence again before snapshotting, and reports lifecycle races without requiring daemon shutdown per US1/AC2 and FR-015–017 (partial)
- [x] T107 Complete import-time secret rebinding, class-specific disabled authority proposals and approvals, canonical path revalidation, full staging/rollback/final capacity preflight, and refuse/rename/separately-confirmed replace conflict handling in `internal/manager/` and `internal/app/migrate.go` per US2/AC2–5 and FR-024–033 (partial)
- [x] T108 Implement reference-only default secret portability, explicitly selected encrypted Hideout-managed secret transfer, direct destination-provider import, sentinel redaction, and a separate opaque guest-content sensitivity acknowledgement bound into plans and receipts per US3/AC2–4 and FR-026–029 (partial)
- [x] T109 Persist durable per-component import cursors, resume only authenticated completed work, and add effect-level crash/restart/commit/rollback/cleanup fault-injection coverage across Manager and daemon workers per US4 and FR-035–036 (partial)
- [x] T110 Align retained export-partial lifecycle across Manager state, recovery projection, CLI, cleanup, and `formal/MigrationBundle.tla`, including an explicit post-cancellation remove action with model/refinement tests per US4/AC4 and FR-045 (contradicts)
- [x] T111 Persist and publish the standard secret-free terminal `MigrationReceipt` and typed `MigrationAuditEvent`, expose them through status/doctor, and prove exactly-once terminal publication across restart per FR-044 (partial)
- [x] T112 Implement Manager-backed migration inventory/actions/progress in liveconsole, the actionable TUI HUD and Enter-edit modals, the embedded WebUI guided flow, and cross-surface golden parity/redaction tests per FR-039–041 and US5 (missing)
- [ ] T113 Add `scripts/gates/migration-lima.sh` with installed package-candidate, independent source/destination stores, root and attached-disk fidelity, same-bundle multi-destination Safe Clone, Exact Guest Restore, identity separation, source immutability, crash cuts, and fail-closed compatibility evidence per SC-014–016 and FR-048 (implementation and semantic preflight complete; exact clean-candidate execution pending)
- [x] T114 Expand the non-performance migration gate to cover CLI/API/daemon workflows, mutation proofs, negative fixtures, hostile input, no-side-effect inspection, terminal redaction, restart reconciliation, and targeted race tests per Constitution IV and FR-048 (partial)
- [x] T115 Finish beginner-grade migration help and copyable configuration/full examples, enforce every flag relationship and non-interactive refusal in tests, and connect preview output to all mapping/approval remediation commands per FR-042–043 and SC-010–011 (partial)
- [x] T116 Create `docs/migration.md` and update architecture/status/threat/test/formal documentation with the implemented support matrix, exact identity policies, encryption/recovery behavior, gates, evidence digests, and honest non-claims per plan: docs/status (missing)

## Phase 10: Convergence

- [ ] T117 CRITICAL: Replace the migration gate's permissive test-name regexes with an explicit fail-on-drift inventory covering CLI, Manager API, daemon recovery, TUI, WebUI, redaction, and targeted races, then add the installed package-candidate `scripts/gates/migration-lima.sh` evidence required before any full-state claim per Constitution IV and FR-048 (repository-wide active-source discovery and the 231-test/19-package inventory are complete; exact clean-candidate execution pending)
- [x] T118 Add authenticated included-category, per-environment/component, and logical-size estimates to the immutable full-export Manager plan and render the same concrete inventory in CLI, TUI, WebUI, and JSON review per FR-003 and US3/AC1 (partial)
- [x] T119 Refuse non-interactive import apply when environment scope was omitted, require explicit source refs for `--yes`/automation, and return a copyable inspection/preview remediation command without changing interactive safe-review behavior per FR-043 and US5/AC4 (contradicts)
- [x] T120 Add a golden cross-surface harness that submits equivalent CLI, TUI, WebUI, and automation choices to Manager and asserts the same plan digest, selections, blockers, effects, risk acknowledgements, confirmations, and terminal classification per FR-039 and SC-013 (partial)
- [x] T121 Close imported-Lima first-start prerequisites before another real gate: inventory-valid fail-closed image sentinel, authenticated runtime-image provenance, exact root size, destination-only mount reconciliation, longest socket-path preflight, and zero-VM installed-Lima regressions per US1/AC1, US1/AC4, FR-018–019, and FR-025
- [x] T122 Retain a structured migration gate execution review with candidate/start mode, reused work, stages, elapsed and byte metrics, failure layer, checkpoint availability, and minimum rerun scope; document the release-wide post-run efficiency protocol
- [x] T123 Bind imported Lima first attach to the destination's durable control key: initialize or validate the exact key pair under Lima's directory lock, install it for target/root during isolated adoption, preserve the adopted SSH host identity/root control across Lima's changing cloud-init instance IDs, receipt the action, and cover mismatch, alias, idempotence, preservation, and failure without a VM
- [x] T124 Make migration closure evidence and guest control-key installation fail closed: reconcile the recorded gate result with every nonzero exit and reject hard-linked adoption targets before any permission, ownership, or content mutation
- [x] T125 Isolate release verification from ambient Go module-write policy by pinning Gate 0, the local release aggregate, and migration tests to read-only module mode and proving a representative run leaves the tidy module ledger unchanged
- [x] T126 Extend the structured execution-review protocol to the local release aggregate with a pre-run start/reuse declaration, boot-session continuity, per-lane timing, first-diagnostic and post-diagnostic cost, repeated-lane and rerun-amplification metrics, and explicit diagnostic versus acceptance rerun scopes
- [x] T127 Bind the real-Lima migration result into the authoritative product-evidence registry with exact commit/package freshness, strict identity/fidelity/crash/compatibility semantics, the canonical eight-artifact inventory, and an exact signed-package public-candidate rerun
- [x] T128 Require the public migration proof to originate from the gate-owned exact signed Darwin/arm64 package and reject caller-supplied unsigned 046 evidence before VM work

## Phase 11: Convergence

- [x] T129 CRITICAL: Add bounded, deterministic, encrypted full-migration components for persistent profile application state under `home/`, `config/`, `data/`, and `browser/`, while excluding cache and regenerating Hideout/profile machine identity, with source-stability and no-plaintext-intermediate tests per US1/AC1 and FR-018 (contradicts)
- [x] T130 Extend authenticated inventory, logical-size estimates, plans, receipts, CLI/TUI/WebUI review, and the sensitivity acknowledgement to name included profile application state and its credential risk while keeping config-only scope identity-free per US3/AC1, US3/AC4, FR-003, and FR-029 (partial)
- [x] T131 Stage and atomically publish profile application state only with its fresh destination profile, add exact-owner rollback/resume checkpoints, and prove traversal, alias, symlink, special-file, expansion, substitution, and limit failures with negative fixtures and mutation proofs per FR-033–036 and Constitution I, II, and IV (missing)
- [x] T132 Repair `scripts/gates/migration-lima.sh` to seed and verify distinct root-disk (`/var/lib`), attached-disk, and projected profile-home sentinels across same-bundle Safe Clone/Exact Restore imports, add a negative fixture and mutation proof for each judge, and retain reusable authenticated checkpoints after post-export failures per SC-014, FR-048, and Constitution IV (partial)
- [x] T133 Update `docs/migration.md`, `docs/STATUS.md`, `docs/threat-model.md`, `docs/privacy-run-design.md`, `docs/privacy-run-test-plan.md`, and the release adversarial report with the profile-state include/exclude, identity-reset, sensitivity, recovery, and gate evidence boundaries per Development Workflow (partial)
- [x] T134 Preserve attached-disk filesystem type and original Lima guest path, emit explicit `format: false` for every fresh imported disk handle, receipt the isolated-guest mount rebind, require disk-fidelity proof before activation in TLA+/Go refinement, and make the real gate reject broad-path or formatting-default false greens
- [x] T135 Bind adoption request/receipt and disk-edge JSON Schema acceptance to actual production struct serialization, including destination SSH user, destination-key installation, mount bindings, and mount-dependent action order, so cheap schema validation detects protocol drift before candidate or VM work

## Phase 12: Release-candidate refinement closure

- [x] T136 Hoist migration terminal polling across fresh and authenticated checkpoint-resume paths, and make zero-VM semantic preflight execute completed and recoverable-failure polling before any expensive restore or VM work
- [x] T137 Bind every authenticated staged attached disk into the zero-network VZ adoption machine with a deterministic block-device identifier and fixed no-format guest mount, replacing the obsolete root-plus-CIDATA-only invariant with stage-to-device-to-mount refinement tests
- [x] T138 Treat a bound failed guest adoption receipt as a stable terminal provider failure before configuration materialization, control removal, or evidence writing, and replay that durable failure without a second guest boot
- [x] T139 Remove temporary adoption write authority from imported attached disks, require read-only VZ and guest mount proofs plus an unchanged host file identity before success evidence, and run the shared migration inventory preflight before every real-Lima attempt
- [x] T140 Close the adoption-to-ordinary-runtime mount refinement: on every imported cold start, prove the fresh attached-disk mount's exact path/filesystem/read-write state and idempotently restore the authenticated source-path alias through root control before RuntimeReady or any target command; model stop/boot/rebind/target ordering in TLA+, retain fail-closed diagnostics, and inventory the regressions per FR-053
