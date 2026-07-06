<!-- markdownlint-disable MD013 -->

# Tasks: Hideout Lima Real Run

**Input**: Design documents from `/specs/001-lima-real-run/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/lima-real-run.md](contracts/lima-real-run.md), [quickstart.md](quickstart.md)

**Tests**: Required. This feature touches backend selection, workspace safety, HostFS, host.open, endpoint exposure, network setup, lifecycle cleanup, audit, and Boundary Summary evidence.

**Organization**: One P1 user story. The implementation must remain a validation fixture and product-path smoke; it must not add a generic host success-check API, daemon, TUI/WebUI workflow, guest-to-host authority, release bundle, guided onboarding, or product-specific real-agent adapter.

## Phase 1: Setup

**Purpose**: Add the dedicated smoke entrypoint and keep the contract fixed before implementation work.

- [X] T001 Create `scripts/test-lima-real-run.sh` with strict shell mode, repository root detection, timeout helper, command preflight helper, temporary store/Lima/workspace directories, cleanup trap, and an initial fail-fast `main` scaffold.
- [X] T002 Add a fixed Boundary Action Set declaration to `scripts/test-lima-real-run.sh` containing host.open denial, HostFS deny/reserved-root denial, session lifecycle, network setup, and one preview.open/endpoint exposure event.

---

## Phase 2: Foundational

**Purpose**: Add reusable smoke helpers before wiring the full Lima run.

**⚠️ CRITICAL**: No Lima dogfood success can be claimed until this phase and the US1 evidence assertions are complete.

- [X] T003 Add `scripts/test-lima-real-run.sh` helpers to build local `hideout`, `hideout-gate-lab-target`, Linux `hideout-shim`, Linux `hideout-hostfsd`, and a Linux `hideout-test-cli` into the temporary test workspace/bin paths.
- [X] T004 Add `scripts/test-lima-real-run.sh` helpers to start and stop a host-side `hideout-gate-lab-target` HTTP endpoint for the declared network request, capturing only non-secret address and log files under the temporary directory.
- [X] T005 Add `scripts/test-lima-real-run.sh` assertion helpers for required stable markers, forbidden secret strings, audit JSON availability via `hideout audit show --session --json`, and Boundary Summary presence.

**Checkpoint**: The smoke script has the utilities needed to drive and verify a real Lima run.

---

## Phase 3: User Story 1 - Run One Target CLI In Lima (Priority: P1) 🎯 MVP

**Goal**: Run one generic target CLI through Lima against a sanitized workspace, complete the reference workload, reach the declared endpoint through the selected network policy, and produce non-secret audit plus Boundary Summary evidence.

**Independent Test**: Run `scripts/test-lima-real-run.sh` on a macOS host with Lima. It passes only if the target updates the expected workspace file, the guest-side success check passes, endpoint reachability succeeds through the selected network mode, unsafe workspace/native/missing-target negative paths fail closed, and the fixed Boundary Action Set is visible in authoritative run evidence.

### Tests for User Story 1

- [X] T006 [P] [US1] Create `cmd/hideout-test-cli/main_test.go` with failing tests for a new `workload` subcommand that writes the expected workspace output, performs its success check inside the guest process, requests a declared endpoint, and prints non-secret workload markers.
- [X] T007 [P] [US1] Add a direct-mode shell contract section in `scripts/test-lima-real-run.sh` that initially fails until `hideout run --backend lima` produces `workspace-updated`, `success-check`, `endpoint`, `session`, `environment`, `audit`, `boundary`, `evidence`, and `passed` markers.
- [X] T008 [P] [US1] Add a negative shell contract section in `scripts/test-lima-real-run.sh` for unsafe workspace rejection using `$HOME` and the temporary Hideout store, asserting rejection before backend preparation.
- [X] T009 [P] [US1] Add a negative shell contract section in `scripts/test-lima-real-run.sh` for missing target CLI failure, asserting no fallback to host execution, native backend, ambient host files, or ambient host networking.
- [X] T010 [P] [US1] Add a native-backend shell contract section in `scripts/test-lima-real-run.sh` that verifies native output is treated as weak wiring evidence and cannot satisfy the Lima dogfood success marker.
- [X] T011 [P] [US1] Add a fixed Boundary Action Set assertion in `scripts/test-lima-real-run.sh` for host.open localhost/private denial, HostFS store/denied access denial, network setup evidence, endpoint exposure/preview.open evidence, and session lifecycle events.
- [X] T012 [P] [US1] Add a privacy-mode branch in `scripts/test-lima-real-run.sh` guarded by `HIDEOUT_LIMA_REAL_RUN_NETWORK=privacy` that requires `HIDEOUT_SECRET_DEFAULT_PROXY`, uses existing tun2socks/proxy run options, and asserts the proxy URL is absent from stdout, stderr, audit, and summary evidence.

### Implementation for User Story 1

- [X] T013 [US1] Implement the `workload` subcommand in `cmd/hideout-test-cli/main.go` with structured flags for task file, output file, expected content, endpoint URL, expected HTTP status, and marker output; reject paths outside the current workspace and do not read host env secrets.
- [X] T014 [US1] Implement the default direct-mode reference run in `scripts/test-lima-real-run.sh`: initialize an isolated Lima profile/store, prepare helpers, create a sanitized workspace/task file, run `./hideout-test-cli workload` with `hideout run --backend lima --workspace`, capture target stdout/stderr separately from control evidence, and parse session/environment/audit details.
- [X] T015 [US1] Implement host-side read-only artifact verification in `scripts/test-lima-real-run.sh` by reading the expected workspace output file and comparing deterministic content without executing any operator-provided host command.
- [X] T016 [US1] Implement guest-side success-check verification in `cmd/hideout-test-cli/main.go` so the `workload` subcommand validates the file it wrote before printing `success-check=passed`.
- [X] T017 [US1] Implement endpoint reachability verification in `cmd/hideout-test-cli/main.go` so the `workload` subcommand requests the declared URL, validates the expected HTTP status, and prints a non-secret endpoint marker.
- [X] T018 [US1] Implement selected-network evidence in `scripts/test-lima-real-run.sh` so direct mode prints `lima-real-run: network=direct`, privacy mode prints `lima-real-run: network=privacy`, and failed network preparation exits before any `lima-real-run: passed` marker.
- [X] T019 [US1] Implement the fixed Boundary Action Set driver in `scripts/test-lima-real-run.sh` using existing product paths: one denied host.open localhost/private request, one HostFS store/reserved-root denial, one `--preview` guest-loopback endpoint exposure event, network setup evidence, and session lifecycle evidence.
- [X] T020 [US1] Implement audit and Boundary Summary checks in `scripts/test-lima-real-run.sh` that derive evidence from `hideout run --verbose` output and `hideout audit show --session --json`, not from independently recomputed assumptions.
- [X] T021 [US1] Implement negative-path checks in `scripts/test-lima-real-run.sh` for unsafe workspace, missing target command, unavailable helper/Lima diagnostics where feasible, and native backend weak-evidence labeling.
- [X] T022 [US1] Wire `scripts/test-phase1.sh` to accept `--lima-real-run`, print it in `HIDEOUT_PHASE1_PRINT_PLAN=1`, and run `scripts/test-lima-real-run.sh` after Gate 2 when the flag is selected.
- [X] T023 [US1] Keep `scripts/test-phase1.sh --release-candidate` unchanged unless release-candidate bundle scope is explicitly promoted in a later spec.

**Checkpoint**: `scripts/test-lima-real-run.sh` independently proves the Lima real-run slice with a deterministic useful-work artifact, selected network policy evidence, and the fixed Boundary Action Set.

---

## Phase 4: Polish & Cross-Cutting Concerns

**Purpose**: Update docs/status and run the minimum gates for this slice.

- [X] T024 [P] Update `docs/privacy-run-test-plan.md` with the Lima Real Run smoke command, its fixed Boundary Action Set, and the direct/privacy validation split.
- [X] T025 [P] Update `docs/STATUS.md` to reflect the implemented supervised dogfood reference smoke without claiming unattended daily use, GA readiness, release-candidate evidence, guided setup, TUI/WebUI observation, or product-specific agent support.
- [X] T026 [P] Update `specs/001-lima-real-run/quickstart.md` if implementation details changed, keeping it focused on the reference smoke rather than first-run onboarding.
- [X] T027 Run `gofmt` on `cmd/hideout-test-cli/main.go` and `cmd/hideout-test-cli/main_test.go`.
- [X] T028 Run `go test ./cmd/hideout-test-cli`.
- [X] T029 Run `go test ./...`.
- [X] T030 Run `scripts/test-phase1.sh --quick`.
- [X] T031 Run Gate 2 with `scripts/test-phase1.sh --lima` on a macOS host with Lima.
- [X] T032 Run the reference smoke with `scripts/test-lima-real-run.sh`.
- [X] T033 If privacy mode was implemented or changed, run `HIDEOUT_LIMA_REAL_RUN_NETWORK=privacy HIDEOUT_SECRET_DEFAULT_PROXY=<operator-supplied-socks-url> scripts/test-lima-real-run.sh` and `scripts/test-phase1.sh --proxy`.
- [X] T034 Run `git diff --check -- cmd/hideout-test-cli scripts docs specs/001-lima-real-run`.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 Setup**: No dependencies.
- **Phase 2 Foundational**: Depends on Phase 1; establishes shared smoke helpers.
- **Phase 3 US1**: Depends on Phase 2; implements the independent Lima real-run slice.
- **Phase 4 Polish**: Depends on US1 completion.

### User Story Dependencies

- **US1**: Only user story. It is independently testable through `scripts/test-lima-real-run.sh`.

### Within US1

- T006 must be written before T013, T016, and T017.
- T007-T012 define shell contract expectations before T014-T021 complete them.
- T014-T018 provide the reference workload and network markers before T019-T020 can assert the full Boundary Action Set.
- T022 depends on a working `scripts/test-lima-real-run.sh`.
- T023 must preserve scope by keeping release-candidate wiring out of this feature.

## Parallel Opportunities

- T006 can run in parallel with T001-T005 after the feature contract is understood.
- T007-T012 can be drafted in parallel because each covers a separate script assertion section.
- T024-T026 can run in parallel after implementation behavior is stable.
- T028-T030 can run independently after code compiles; T031-T033 require macOS/Lima/proxy prerequisites.

## Parallel Example: User Story 1

```bash
# Draft independent shell assertions in parallel:
Task: "T007 direct-mode shell contract section in scripts/test-lima-real-run.sh"
Task: "T008 unsafe workspace rejection in scripts/test-lima-real-run.sh"
Task: "T009 missing target CLI failure in scripts/test-lima-real-run.sh"
Task: "T011 fixed Boundary Action Set assertion in scripts/test-lima-real-run.sh"
Task: "T012 privacy-mode branch in scripts/test-lima-real-run.sh"
```

## Implementation Strategy

### MVP First

1. Complete Phase 1 and Phase 2.
2. Complete US1 direct-mode path through T020.
3. Run `go test ./cmd/hideout-test-cli`, `go test ./...`, `scripts/test-phase1.sh --quick`, and `scripts/test-lima-real-run.sh`.
4. Run Gate 2 on macOS/Lima before treating the smoke as dogfood isolation evidence.

### Incremental Delivery

1. Ship deterministic direct-mode Lima real-run smoke first.
2. Add privacy-mode validation only when operator proxy prerequisites are available or tun2socks/proxy handling changes.
3. Keep release bundles, guided setup, TUI/WebUI observation, daemon mode, and real-agent adapters for later specs.

### Notes

- Host-side verification in this feature is limited to read-only assertions over deterministic workspace artifacts and redacted evidence.
- The guest/workspace success check is part of the target workload and must not become a new host execution channel.
- Boundary evidence must come from authoritative runtime facts: run output, audit records, and Boundary Summary.

## Phase 5: Convergence

Convergence tasks were appended by a converge assessment. The trailing
parenthetical on each task records the gap status **at assessment time**
(`missing`/`partial`), not the completion state; the checkbox records
completion.

- [X] T035 Add a SIGINT/SIGTERM interruption cleanup smoke to `scripts/test-lima-real-run.sh` that proves normal-stop cases leave no active run-scoped HostFS, endpoint exposure, broker token, or proxy secret artifacts per SC-008 (missing)
- [X] T036 Add a reusable-environment two-run reference workflow to `scripts/test-lima-real-run.sh` using the generic `hideout-test-cli workload`, asserting both runs reuse the same Lima environment and preserve isolated profile state per SC-009 (partial)
- [X] T037 Extend unsafe-workspace rejection coverage in `scripts/test-lima-real-run.sh` with a macOS case-variant sensitive-root sample when the filesystem supports it, while retaining before-backend-prepare side-effect assertions, per SC-004 (partial)
- [X] T038 Add a reference workload duration assertion or explicit timing marker so the smoke verifies the 10-minute completion criterion per SC-001 (partial)
- [X] T039 Assert post-run resume, stop, and clean guidance in the reference run's authoritative output or evidence per FR-013 (partial)
- [X] T040 Add a deterministic unavailable-helper or unavailable-Lima negative path, where feasible, that fails closed without native, host, or ambient-network fallback per FR-007 and T021 (partial)
