<!-- markdownlint-disable MD013 -->

# Tasks: Unified Named Environments With Declared Base Image

**Input**: Design documents from `/specs/003-unified-named-environments/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`,
`contracts/environment-model.md`, `quickstart.md`, `.specify/memory/constitution.md`

**Tests**: Required. This feature changes environment identity, run
selection, backend preparation input, destructive lifecycle commands, and
store record versioning — every boundary-relevant change needs positive and
fail-closed coverage per constitution Principle IV.

**Organization**: Tasks are grouped by user story. US1 (named environment +
declared image) is the MVP; US2 unifies resolution; US3 delivers drift and
safety semantics.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Shared fixtures for declaration validation and record
versioning used by every story.

- [X] T001 [P] Add image-declaration test fixtures (valid `template:` form, valid URL+`#sha256:` form, digest-less URL, credentialed URL, OCI-style ref, malformed digest) in `internal/environment/testdata/imagedecl/`
- [X] T002 [P] Add prior-version environment record fixtures (old version string, missing name/image fields, corrupt JSON) in `internal/environment/testdata/records/`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Record model, declaration validation, profile field, and
auto-naming — the vocabulary every story builds on.

**Critical**: No user story implementation before these are testable.

### Tests First

- [X] T003 [P] Add record model tests for `Name` charset validation, case-insensitive uniqueness, reserved `default` rejection, `autoNamed` flag, required `imageRef`, `toolsHash` absence, and version-bump rejection with clean-and-recreate guidance in `internal/environment/environment_test.go`
- [X] T004 [P] Add image-declaration parser tests covering both accepted forms and every rejection class from the contract (no digest, credentials, scheme, OCI, malformed digest) in `internal/environment/environment_test.go`
- [X] T005 [P] Add profile validation and schema tests for `environment.baseImage` (both forms accepted, rejections mirrored, default profile carries the explicit built-in template value) in `internal/profile/profile_test.go` and `schemas/profile.schema.json`
- [X] T006 [P] Add auto-name derivation tests (deterministic for profile+workspace, different after workspace move, sanitized basename, single namespace with explicit names) in `internal/environment/environment_test.go`

### Implementation

- [X] T007 Implement the image-declaration type, parser, and validator (both forms, all rejections) in `internal/environment/environment.go`
- [X] T008 Extend `Record`/`Spec` with `Name`, `AutoNamed`, `ImageRef`; remove `ToolsHash`; bump the record version constant; add case-insensitive name uniqueness and `LoadByName`; reject foreign-version records with model-changed guidance in `internal/environment/environment.go`
- [X] T009 Add `environment.baseImage` to the profile model and schema with declaration validation, set the explicit built-in template value in the shipped default profile, and resolve an absent profile field to the built-in template default at declaration resolution in `internal/profile/profile.go` and `schemas/profile.schema.json`
- [X] T010 Implement deterministic auto-name derivation (sanitized workspace basename + 8-hex hash of profile and cleaned path) in `internal/environment/environment.go`

**Checkpoint**: Record model, declaration validation, profile default, and
naming are testable; user stories can start.

---

## Phase 3: User Story 1 - Create And Use A Named Environment With A Declared Base Image (Priority: P1) MVP

**Goal**: `env create <name> --image <declaration>` followed by
`run --env <name>` boots the pinned image and runs the command; creation is
local-only (no boot, no network).

**Independent Test**: Quickstart steps 2 and 4 — create/inspect on a clean
store without network, then the real Lima boot from a declared image with the
run summary naming the environment.

### Tests for User Story 1

- [X] T011 [P] [US1] Add app tests for `env create`/`env inspect` covering success, reserved-name, collision, digest-less URL guidance, credentialed URL rejection, and no-boot/no-network behavior in `internal/app/app_test.go`
- [X] T012 [P] [US1] Add Manager tests for environment create/inspect/remove plan/apply shape, `--image` > profile default > built-in default precedence (including a profile without `environment.baseImage`), the remove running-guest refusal with stop hint, and `env.create` audit emission in `internal/manager/manager_test.go`
- [X] T013 [P] [US1] Add Lima config generation tests proving `template:` form maps to the base template reference, URL form generates an `images` entry with `location` and `digest`, and no hardcoded base image remains in `internal/backend/lima/lima_test.go`
- [X] T014 [P] [US1] Add run selection tests for `--env <name>` (found, unknown-name error naming `env list`, names-only contract, conflicting `--profile`/`--backend`/workspace inputs fail closed) in `internal/manager/run_environment_test.go`

### Implementation for User Story 1

- [X] T015 [US1] Implement Manager environment create/remove operations with declaration resolution (flag > profile default > built-in default), workspace pinning input, the running-guest refusal with stop hint for `env remove` (the `--force` override lands in US3), and `env.create`/`env.remove` audit in `internal/manager/environment_lifecycle.go`
- [X] T016 [US1] Implement the `hideout env create|inspect` commands and the `run --env <name>` flag in `internal/app/app.go`
- [X] T017 [US1] Compile the pinned declaration into Lima configuration (template form → base reference; URL form → `images` entry with digest) and delete the hardcoded base template in `internal/backend/lima/lima.go`
- [X] T018 [US1] Name the selected environment in the run summary and run audit evidence in `internal/app/app.go` and `internal/manager/run_dataplane.go`
- [X] T019 [US1] Add the real-backend gate variant: create an environment from a declared image URL, boot it, assert the guest matches, assert a wrong digest fails closed, in `scripts/test-env-image.sh` wired as an optional Gate 2 step in `scripts/test-phase1.sh`

**Checkpoint**: US1 delivers the dogfood path — reproducible named
environments from declared images.

---

## Phase 4: User Story 2 - One Environment Model For Every Run (Priority: P2)

**Goal**: Runs without `--env` resolve to the deterministic auto-named
environment; MRU selection and the top-level `list` command are gone; one
listing shows everything including unsupported-version records.

**Independent Test**: Quickstart steps 3 and 6 — auto-named create/reuse in a
fresh workspace, and planted old-version records surfacing as guided
`unsupported-version` entries.

### Tests for User Story 2

- [X] T020 [P] [US2] Add selection tests for auto-named resolution (first-use create with profile image default, reuse on rerun, `--rm` leaving no record, `--ephemeral` unchanged) in `internal/manager/run_environment_test.go`
- [X] T021 [P] [US2] Add app tests for `env list` (columns, auto-named marker, `unsupported-version` display) and for the removal of top-level `list` and of `--resume`/`--new` flags in `internal/app/app_test.go`
- [X] T022 [P] [US2] Add guidance tests proving list/run/stop/clean on prior-version records stop with clean-and-recreate guidance and never read through them in `internal/environment/environment_test.go` and `internal/manager/run_environment_test.go`
- [X] T023 [P] [US2] Add Manager summary/API tests exposing `name`, `imageRef`, and workspace per supported environment, and showing prior-version records only as `unsupported-version` rows keyed by id/path/version, in `internal/manager/manager_test.go` and `internal/manager/api_test.go`

### Implementation for User Story 2

- [X] T024 [US2] Rewrite `SelectRunEnvironment` to name-based resolution (explicit `--env`, else auto-name load-or-create), delete `Store.Latest` MRU selection and the `--resume`/`--new` option paths, keep `--rm`/`--ephemeral` semantics, and give native runs the same record path without VM lifecycle in `internal/manager/run_environment.go`
- [X] T025 [US2] Implement `env list`, remove the top-level `list` command and `--resume`/`--new` flags from parsing and help, and make `stop`/`clean` accept environment names in `internal/app/app.go`
- [X] T026 [US2] Expose `name`/`imageRef`/workspace in environment summaries, Manager API resources, TUI/WebUI environment rendering, and typed environment lifecycle plan/apply endpoints in `internal/manager/manager.go`, `internal/manager/api.go`, and `internal/manager/server.go`

**Checkpoint**: One model everywhere; no silent environment derivation
remains.

---

## Phase 5: User Story 3 - Drift Is Explicit, Never Silent (Priority: P3)

**Goal**: Backend/workspace drift fails closed with a recreate hint; destructive
commands respect running guests; workspace pinning and the shadowed-rule
warning land.

**Independent Test**: Quickstart steps 5 and 7 — per-axis drift matrix with
recreate recovery, forced/refused destructive commands, and the shadow
warning on an in-workspace HostFS rule.

### Tests for User Story 3

- [X] T027 [P] [US3] Add drift matrix tests: each use-time axis (backendConfigVersion, workspace-by-file-identity) fails closed naming the axis with pinned and current values plus a recreate hint; profile baseImage and expectedCommands changes produce no drift in `internal/manager/run_environment_test.go`
- [X] T028 [P] [US3] Add app/manager tests for `env recreate`/`env remove` refusing on a running guest with a copyable stop hint and proceeding with `--force` (stop-then-act, force recorded in audit) in `internal/app/app_test.go` and `internal/manager/manager_test.go`
- [X] T029 [P] [US3] Add workspace pinning tests: create defaults to the invoking directory, dangerous-root rejection with the existing override, and use-time comparison via real file identity (case/hardlink variants) in `internal/manager/run_environment_test.go`
- [X] T030 [P] [US3] Add shadowed-rule warning tests for run planning and doctor (warns once per rule, never blocks) in `internal/app/app_test.go`
- [X] T031 [P] [US3] Add audit tests for `env.drift.denied`, `env.recreate`, and forced-flag recording with verbatim ref/workspace values in `internal/manager/manager_test.go`

### Implementation for User Story 3

- [X] T032 [US3] Implement backend/workspace drift comparison, the drift report rendering, and `env.drift.denied` audit in `internal/manager/run_environment.go`
- [X] T033 [US3] Implement `env recreate` (teardown via existing stop/clean internals, rebuild from the pinned declaration under the same name) with the running-guest guard and `--force` on recreate/remove in `internal/app/app.go` and `internal/manager/environment_lifecycle.go`
- [X] T034 [US3] Pin the workspace at create (default invoking directory) through the existing dangerous-root guard, and store it for file-identity comparison in `internal/manager/environment_lifecycle.go`
- [X] T035 [US3] Implement the shadowed-rule warning (`os.SameFile`-grade containment of profile HostFS rules inside the pinned workspace) in run planning and doctor in `internal/hostfs/hostfs.go` and `internal/app/app.go`

**Checkpoint**: All three stories independently verifiable.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Documentation truth, gates, and end-to-end validation.

- [X] T036 [P] Update the environment chapter (named model implemented, MRU removed, auto-name resolution) and the identity sentence per clarification Q1 (pinned image declaration is immutable; drift comparison = backend config version + workspace; expectedCommands are live diagnostics) in `docs/privacy-run-design.md`
- [X] T037 [P] Update environment/tool rows and the evidence wording in `docs/STATUS.md`, and replace `hideout list` with the `env` command family in `README.md` and `README.zh-CN.md`
- [X] T038 [P] Add the change-to-gate row for environment model/image declarations and document `scripts/test-env-image.sh` as the Lima gate variant in `docs/privacy-run-test-plan.md`
- [X] T039 Run `go test ./...` from `/Users/null/Code/ym/nodejs/node-v26.4.0` and fix fallout in touched packages
- [X] T040 Run `scripts/test-gate0.sh` from `/Users/null/Code/ym/nodejs/node-v26.4.0` and fix schema/docs/smoke fallout
- [X] T041 Run `markdownlint-cli2 README.md README.zh-CN.md docs specs/003-unified-named-environments` from `/Users/null/Code/ym/nodejs/node-v26.4.0` and fix issues
- [X] T042 Walk quickstart steps 2, 3, 5, 6, and 7 locally (no-network create, auto-named resolution, drift matrix, old-record guidance, shadow warning) and record results in `specs/003-unified-named-environments/quickstart.md` notes

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: depends on Setup; blocks all stories.
- **US1 (Phase 3)**: depends on Foundational. MVP.
- **US2 (Phase 4)**: depends on Foundational; lands after US1 because the
  selection rewrite (T024) consumes the create path (T015/T016).
- **US3 (Phase 5)**: depends on Foundational; drift (T032) and recreate
  (T033) consume US1's lifecycle ops; the shadow warning (T035) is
  independent after Phase 2.
- **Polish (Phase 6)**: after the stories it documents.

### User Story Dependencies

- **US1**: none beyond Foundational. Independently shippable.
- **US2**: consumes US1's create path; independently testable via quickstart
  steps 3 and 6.
- **US3**: consumes US1's lifecycle ops; independently testable via
  quickstart steps 5 and 7.

### Within Each Story

- Tests first; confirm they fail against current behavior before
  implementing.
- Store/model changes before Manager operations; Manager before CLI; CLI
  before gate scripts.
- Destructive-path changes (recreate/remove) land only with their
  running-guest guard tests.

### Parallel Opportunities

- T001–T002 in parallel.
- T003–T006 in parallel (same package but distinct test functions; if churn
  conflicts, T003+T004 first).
- T011–T014 in parallel (separate packages).
- T020–T023 in parallel.
- T027–T031 in parallel.
- T036–T038 in parallel after implementation settles.

## Parallel Example: User Story 1

```bash
Task: "T011 [P] [US1] app tests for env create/inspect in internal/app/app_test.go"
Task: "T012 [P] [US1] Manager create/remove plan-apply tests in internal/manager/manager_test.go"
Task: "T013 [P] [US1] Lima image-compilation tests in internal/backend/lima/lima_test.go"
Task: "T014 [P] [US1] run --env selection tests in internal/manager/run_environment_test.go"
```

## Implementation Strategy

### MVP First (US1 Only)

1. Phases 1–2 (fixtures, record model, declaration, profile default,
   auto-name primitive).
2. US1: create/inspect, `run --env`, Lima compilation, gate variant.
3. Validate quickstart steps 2 and 4; stop and review before US2.

### Incremental Delivery

1. US1: named environments from declared images (dogfood value).
2. US2: unified resolution, MRU removal, single listing, old-record
   guidance.
3. US3: drift semantics, destructive-command guards, workspace safety,
   shadow warning.
4. Polish: docs, gates, quickstart walk.

### Scope Guard

- No shared `default` environment, dynamic mounts, daemon, image
  building/caching/credential handling, OCI references, ecosystem image
  intake, or onboarding in this feature.
- No record migration: prior-version records only ever produce guidance.
