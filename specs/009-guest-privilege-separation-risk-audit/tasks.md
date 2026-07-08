# Tasks: Guest Privilege Separation And Risk Audit

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/009-guest-privilege-separation-risk-audit/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Required. 009 touches backend identity, privileged guest setup, network/DNS/HostFS setup, audit, evidence, UI surfaces, and gate evidence.

**Organization**: Tasks are grouped by user story so each story can be implemented and tested independently.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create shared 009 files, schemas, and gate entry points.

- [X] T001 Create `internal/privilege/doc.go`, `internal/privilege/status.go`, `internal/privilege/checks.go`, and `internal/privilege/evidence.go` package skeletons for backend-neutral status, checks, and evidence.
- [X] T002 [P] Add `schemas/guest-privilege-status.schema.json` with `enforced`, `degraded`, and `unknown` status values and required evidence fields.
- [X] T003 [P] Add `scripts/test-privilege-separation-smoke.sh` skeleton with sections for unit tests, real Lima enforced proof, real Lima degraded proof, and overclaim scans.
- [X] T004 Register `schemas/guest-privilege-status.schema.json` and `scripts/test-privilege-separation-smoke.sh` in `scripts/test-gate0.sh`.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Shared status model, redaction rules, and proof vocabulary that all stories use.

**CRITICAL**: No user story work can begin until this phase is complete.

- [X] T005 [P] Add status classification table tests in `internal/privilege/status_test.go` covering enforced, degraded, unknown, passwordless sudo, shared sudo setup, missing checks, and enforced-only failures (FR-004, FR-005, FR-006, FR-007, FR-016, SC-002, SC-003).
- [X] T006 [P] Add setup-secret redaction tests in `internal/privilege/evidence_test.go` covering setup keys, setup tokens, broker/UI tokens, `HIDEOUT_SECRET_*`, generated machine-id, raw credential paths, audit evidence, and UI/export summaries (FR-011, SC-004).
- [X] T007 Implement status values, check result types, setup identity types, classification, enforced-only validation, and guidance generation in `internal/privilege/status.go` and `internal/privilege/checks.go`.
- [X] T008 Implement evidence detail builders, redaction helpers, non-claim text, and schema-compatible serialization in `internal/privilege/evidence.go`.
- [X] T009 Add schema validation tests for `schemas/guest-privilege-status.schema.json` in `internal/privilege/evidence_test.go`.
- [X] T010 Wire 009 evidence redaction through existing audit/export redaction paths in `internal/audit/audit.go` and `internal/export/` tests without weakening 005 export guarantees.

**Checkpoint**: Shared status, evidence, and schema logic are complete and can be consumed by Lima, Manager, and UI code.

---

## Phase 3: User Story 1 - Know The Guest Privilege State (Priority: P1) MVP

**Goal**: Every Lima run reports exactly one honest `enforced`, `degraded`, or `unknown` status before target command completion.

**Independent Test**: Run a Lima-backed command and inspect audit, explain, and Boundary Summary evidence showing one status; verify `enforced` appears only when target non-root and passwordless sudo checks fail.

### Tests for User Story 1

- [X] T011 [P] [US1] Add fake Lima privilege probe tests in `internal/backend/lima/privilege_test.go` for target `id -u`, `sudo -n true`, `/usr/bin/sudo -n true`, ambiguous output, command errors, and pre-009 metadata (FR-001, FR-002, FR-003, FR-007).
- [X] T012 [P] [US1] Add run evidence tests in `internal/manager/run_dataplane_test.go` proving exactly one `guest.privilege.status` event is emitted per run and appears before target completion (FR-018, SC-001).
- [X] T013 [P] [US1] Add Boundary Summary and explain/doctor tests in `internal/manager/boundary_summary_test.go` and `internal/app/app_test.go` for enforced, degraded, and unknown status wording (FR-008, FR-009, FR-018, SC-008).
- [X] T014 [P] [US1] Add Manager/TUI/WebUI status display tests in `internal/manager/manager_test.go`, `internal/manager/server_liveconsole_test.go`, and `internal/app/app_test.go` proving status, reason, blocking state, and guidance are visible without raw credentials (FR-018, SC-004).

### Implementation for User Story 1

- [X] T015 [US1] Implement Lima privilege probing in `internal/backend/lima/privilege.go` and call it from `internal/backend/lima/lima.go` before target launch (FR-001, FR-002, FR-003).
- [X] T016 [US1] Store per-run guest privilege status in Manager runtime/session state in `internal/manager/run_dataplane.go` and `internal/manager/manager.go` (FR-004, FR-018).
- [X] T017 [US1] Emit `guest.privilege.status` audit records from `internal/manager/run_dataplane.go` using `internal/privilege/evidence.go` (FR-018, SC-001).
- [X] T018 [US1] Add guest privilege status to Boundary Summary in `internal/manager/boundary_summary.go` with explicit non-claim text for `degraded` and `unknown` (FR-009, FR-018, SC-008).
- [X] T019 [US1] Add `hideout explain` and `hideout doctor` status output in `internal/app/app.go` with recreate/base-image guidance for degraded environments (FR-008, FR-018).
- [X] T020 [US1] Add Manager overview fields and JSON serialization for guest privilege status in `internal/manager/manager.go` and `internal/manager/server.go` (FR-018).
- [X] T021 [US1] Add TUI and WebUI rendering for guest privilege status in `internal/app/app.go` and `internal/manager/server.go` (FR-018).
- [X] T022 [US1] Update `scripts/test-privilege-separation-smoke.sh` to validate unit status tests and no-overclaim scans for degraded/unknown output (FR-009, SC-008).

**Checkpoint**: US1 is complete when status classification, audit, Boundary Summary, explain/doctor, Manager, TUI, WebUI, and no-overclaim scans work without changing privileged setup paths.

---

## Phase 4: User Story 2 - Keep Hideout Setup Separate From Target Intent (Priority: P2)

**Goal**: Privileged Lima setup for tun2socks, DNS mediation, HostFS, and cleanup uses a Hideout-owned setup path while target commands remain non-root and non-sudo.

**Independent Test**: On Lima, prove required setup succeeds through the Hideout setup identity while the target user cannot use passwordless sudo.

### Tests for User Story 2

- [X] T023 [P] [US2] Add Lima setup identity provisioning tests in `internal/backend/lima/privilege_test.go` for root/control SSH metadata, credential permissions, target unreadability, and pre-009 environment detection (FR-005, FR-010, FR-011).
- [X] T024 [P] [US2] Add fake setup runner tests in `internal/backend/lima/privilege_test.go` proving setup commands use the setup path, target commands use the target identity, and shared sudo reports degraded (FR-005, FR-006, FR-010).
- [X] T025 [P] [US2] Add network/DNS setup path tests in `internal/network/network_test.go` for setup-path execution, missing setup path fail-closed, and no fallback to target sudo (FR-015, FR-017, SC-007).
- [X] T026 [P] [US2] Add HostFS setup path tests in `internal/backend/lima/privilege_test.go` or `internal/backend/lima/lima_test.go` proving HostFS mount setup uses the setup identity and does not write setup material under target-writable session paths (FR-010, FR-011, FR-015).
- [X] T027 [P] [US2] Add privileged setup audit tests in `internal/manager/run_dataplane_test.go` for `hideout.privileged_setup` and `hideout.privileged_cleanup` events separated from target root attempts (FR-012, SC-004).

### Implementation for User Story 2

- [X] T028 [US2] Provision a Hideout setup identity for new Lima environments in `internal/backend/lima/lima.go` and `internal/backend/lima/privilege.go` using system provisioning, control-plane storage, and safe file permissions (FR-005, FR-010, FR-011).
- [X] T029 [US2] Add a Lima setup command runner in `internal/backend/lima/privilege.go` that uses the setup identity for fixed Go-owned setup categories only (FR-010, FR-017).
- [X] T030 [US2] Remove shared target-user `sudo -n` dependence from tun2socks/DNS bootstrap paths in `internal/network/network.go` when an allowed setup path is required (FR-010, FR-015, FR-017).
- [X] T031 [US2] Move HostFS mount setup and cleanup in `internal/backend/lima/lima.go` to the setup identity path and keep target-visible HostFS behavior unchanged (FR-010, FR-015).
- [X] T032 [US2] Move cleanup that needs guest privilege to the setup identity path in `internal/backend/lima/lima.go` and `internal/network/network.go` with fail-closed errors on setup identity failure (FR-010, FR-017).
- [X] T033 [US2] Emit `hideout.privileged_setup` and `hideout.privileged_cleanup` audit events from Manager/backend setup paths in `internal/manager/run_dataplane.go` (FR-012).
- [X] T034 [US2] Ensure setup identity credentials are never passed to target env, target-writable session directories, Manager overview, WebUI/TUI output, audit JSONL, or export artifacts in `internal/manager/run_dataplane.go`, `internal/manager/server.go`, and `internal/export/` (FR-011, SC-004).
- [X] T035 [US2] Update `scripts/test-gate3-hidden-proxy.sh` or its assertions only as needed to record privilege setup evidence while preserving DNS forward/reverse proof (FR-015, SC-007).

**Checkpoint**: US2 is complete when requested setup either runs through the Hideout setup identity or fails closed, and Gate 3 still passes after network/DNS setup changes.

---

## Phase 5: User Story 3 - Audit Root-Sensitive Attempts Honestly (Priority: P3)

**Goal**: Root-sensitive command-name intent is audited as target intent and never presented as absolute-path, syscall, setuid, or guest-root containment.

**Independent Test**: Enable the root-sensitive adapter, run command-name and absolute-path sudo attempts, and verify command-name attempts are denied/audited while absolute-path risk is covered by privilege status and non-claim text.

### Tests for User Story 3

- [X] T036 [P] [US3] Extend `internal/broker/broker_adapter_test.go` to assert root-sensitive adapter evidence includes `target.root_attempt`, current separation status, and non-claim wording for degraded/unknown runs (FR-013, FR-014, SC-008).
- [X] T037 [P] [US3] Extend `scripts/test-command-adapter-smoke.sh` to prove `sudo whoami` by command name is denied or produces a non-applied proposal and is labeled target intent, not setup activity (FR-013).
- [X] T038 [P] [US3] Add absolute-path sudo evidence tests in `internal/privilege/evidence_test.go` and `internal/app/app_test.go` proving `/usr/bin/sudo` bypass risk is documented and status-driven (FR-014, SC-008).

### Implementation for User Story 3

- [X] T039 [US3] Add target root attempt evidence builders in `internal/privilege/evidence.go` and wire them into 008 adapter audit output in `internal/broker/broker.go` (FR-013).
- [X] T040 [US3] Pass current guest privilege status into command adapter broker envelopes from `internal/manager/run_dataplane.go` and `internal/broker/broker.go` (FR-013).
- [X] T041 [US3] Update root-sensitive adapter messages in `internal/cmdadapter/rootsensitive.go` to distinguish intent capture from privilege containment and include absolute-path/syscall non-claims (FR-014).
- [X] T042 [US3] Update Boundary Summary, explain, doctor, Manager, TUI, and WebUI wording to show command proxy limitations and degraded base-image risk without claiming guest-root containment in `internal/manager/boundary_summary.go`, `internal/app/app.go`, and `internal/manager/server.go` (FR-009, FR-014, FR-018).

**Checkpoint**: US3 is complete when root-sensitive evidence is useful for review but cannot be mistaken for a root or kernel boundary.

---

## Phase 6: Real Lima Proof And Polish

**Purpose**: Real VM evidence, documentation, gates, and final cleanliness checks.

- [X] T043 [P] Add real Lima enforced proof mode to `scripts/test-privilege-separation-smoke.sh` covering target `id -u != 0`, `sudo -n true` failure, `/usr/bin/sudo -n true` failure, setup identity success, status `enforced`, and no setup secret leakage (FR-020, SC-005).
- [X] T044 [P] Add real Lima degraded proof mode to `scripts/test-privilege-separation-smoke.sh` covering passwordless sudo detection, `degraded` status, warning, audit reason, recreate/base-image guidance, and no guest-root containment claim (FR-008, FR-020, SC-006, SC-008).
- [X] T045 [P] Update `README.md`, `docs/README.md`, `docs/STATUS.md`, `docs/privacy-run-design.md`, `docs/privacy-run-test-plan.md`, `docs/threat-model.md`, `docs/backend-capability-matrix.md`, and `docs/network-privacy-architecture.md` for 009 status, setup identity, degraded risk, and A3 guest-root non-claims (FR-019, SC-008).
- [X] T046 [P] Update `specs/009-guest-privilege-separation-risk-audit/quickstart.md` with actual commands and evidence paths after implementation details settle (FR-020).
- [X] T047 Run real Lima enforced proof from `scripts/test-privilege-separation-smoke.sh` and attach evidence to `specs/009-guest-privilege-separation-risk-audit/tasks.md` or a linked evidence log before marking 009 complete (FR-020, SC-005).
- [X] T048 Run real Lima degraded proof from `scripts/test-privilege-separation-smoke.sh` and attach evidence to `specs/009-guest-privilege-separation-risk-audit/tasks.md` or a linked evidence log before marking 009 complete (FR-020, SC-006).
- [X] T049 Run `scripts/test-gate3-hidden-proxy.sh` after setup-path changes and record `gate3: passed` with DNS forward/reverse proof (FR-015, SC-007).
- [X] T050 Run final battery: `go build ./...`, `go vet ./...`, `test -z "$(gofmt -l internal cmd)"`, `git diff --check`, `npx --yes markdownlint-cli2 README.md docs/**/*.md specs/009-guest-privilege-separation-risk-audit/**/*.md`, `go test ./...`, `scripts/test-gate0.sh`, and `scripts/test-privilege-separation-smoke.sh`.

### Evidence Ledger

- Real Lima enforced proof: `scripts/test-privilege-separation-smoke.sh --real-enforced` printed `privilege-separation-smoke real-enforced: passed`. The smoke asserts target UID is non-zero, target `sudo -n true` and `/usr/bin/sudo -n true` fail, setup identity is `root-control-ssh`, HostFS privileged setup succeeds, and setup credential material is absent from audit/stdout/stderr.
- Real Lima degraded proof: `scripts/test-privilege-separation-smoke.sh --real-degraded` printed `privilege-separation-smoke real-degraded: passed`. The smoke creates a temporary managed Lima environment, externally restores target passwordless sudo to simulate a weak/pre-009 image, then verifies product output and audit report `degraded`, include recreate/base-image/non-claim guidance, and do not claim guest-root containment.
- Gate 3 setup-path regression proof: `scripts/test-gate3-hidden-proxy.sh` printed `proxy_env_absent=yes`, `dns_mediated=yes`, `connected_subnet_blocked=yes`, `https_request=ok`, `privilege_status=enforced`, `privileged_setup=network`, and `gate3: passed`.
- Final battery: `go build ./...`, `go vet ./...`, `test -z "$(gofmt -l internal cmd)"`, `git diff --check`, `npx --yes markdownlint-cli2 README.md docs/**/*.md specs/009-guest-privilege-separation-risk-audit/**/*.md`, `go test ./...`, `scripts/test-gate0.sh`, and `scripts/test-privilege-separation-smoke.sh` all exited 0.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup; blocks all user stories.
- **US1 (Phase 3)**: Depends on Foundational; MVP status/evidence surface.
- **US2 (Phase 4)**: Depends on Foundational and should consume US1 status/evidence types; required for real `enforced` Lima status with privileged setup.
- **US3 (Phase 5)**: Depends on Foundational and US1 status; can proceed in parallel with late US2 implementation after status APIs stabilize.
- **Polish (Phase 6)**: Depends on all desired stories, with real Lima proof after US2 setup-path changes.

### User Story Dependencies

- **US1 (P1)**: Start after Foundational; independently testable with fake Lima/unknown/degraded evidence even before setup-path replacement.
- **US2 (P2)**: Start after Foundational and status shape; makes enforced status meaningful for privileged setup.
- **US3 (P3)**: Start after US1 status shape and 008 adapter evidence; no dependency on setup-path internals except status input.

### Within Each User Story

- Tests for classification, fail-closed, redaction, and non-claim behavior first.
- Shared models before backend integration.
- Backend setup paths before Manager/UI claims.
- Real Lima proof before marking 009 complete.

## Parallel Opportunities

- T002 and T003 can run in parallel after T001.
- T005 and T006 can run in parallel before T007/T008.
- T011 through T014 can be written in parallel for US1.
- T023 through T027 can be written in parallel for US2.
- T036 through T038 can be written in parallel for US3.
- T043 through T046 can be prepared in parallel during polish, but T047 through T049 require a built implementation and real Lima.

## Parallel Example: User Story 2

```bash
Task: "Add Lima setup identity provisioning tests in internal/backend/lima/privilege_test.go"
Task: "Add network/DNS setup path tests in internal/network/network_test.go"
Task: "Add HostFS setup path tests in internal/backend/lima/privilege_test.go"
Task: "Add privileged setup audit tests in internal/manager/run_dataplane_test.go"
```

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1 and Phase 2.
2. Complete US1 status classification, probes, audit, Boundary Summary, explain/doctor, Manager, TUI, and WebUI.
3. Validate US1 with unit tests and fake Lima evidence.
4. Do not claim enforced setup separation for privileged setup until US2 and real Lima proof are complete.

### Incremental Delivery

1. US1 gives operators honest `enforced`/`degraded`/`unknown` visibility.
2. US2 turns Lima enforced status into real setup separation for tun2socks, DNS, HostFS, and cleanup.
3. US3 connects 008 root-sensitive intent evidence to 009 status without root-boundary overclaims.
4. Polish runs real Lima enforced/degraded proof, Gate 3, docs, and full battery.

### Non-Negotiable Gates

- `enforced` cannot be emitted without complete proof.
- Requested privileged setup cannot fall back to target sudo silently.
- Setup credentials cannot appear in target env, target-writable paths, audit, UI, or export.
- Real Lima enforced and degraded evidence is required before 009 is complete.
