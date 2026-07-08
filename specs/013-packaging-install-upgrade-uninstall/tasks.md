# Tasks: Packaging, Install, Upgrade, And Uninstall

<!-- markdownlint-disable MD013 -->

**Input**: Design artifacts from `specs/013-packaging-install-upgrade-uninstall/`

## Phase 1: Setup

- [X] T001 Create `internal/packagekit/doc.go` package skeleton for package artifact, install state, verify, install, and uninstall logic
- [X] T002 Update `schemas/package-manifest.schema.json` to include migration metadata, file executable flags, expanded file kinds, and installed-state manifest shape
- [X] T003 [P] Add package lifecycle smoke placeholders in `scripts/test-package-smoke.sh` for install verify, upgrade preservation, uninstall dry-run, uninstall preserve, and purge assertions
- [X] T004 [P] Add 013 status placeholders to `docs/STATUS.md` and package docs references for alpha packaging scope

## Phase 2: Foundational

- [X] T005 Implement package manifest and installed-state data types in `internal/packagekit/manifest.go`
- [X] T006 Implement package-relative and prefix-relative path validation in `internal/packagekit/path.go`
- [X] T007 [P] Add manifest/path validation tests in `internal/packagekit/manifest_test.go`
- [X] T008 Implement SHA-256, regular-file, executable-bit, and symlink rejection helpers in `internal/packagekit/verify.go`
- [X] T009 [P] Add checksum, missing-file, symlink, executable-bit, and path-escape tests in `internal/packagekit/verify_test.go`
- [X] T010 Implement package audit event writer for install, upgrade, uninstall, dry-run, and purge summaries in `internal/packagekit/audit.go`
- [X] T011 Add `hideout package install|verify|uninstall` command parsing and usage in `internal/app/app.go`
- [X] T012 Add CLI parsing tests for package subcommands in `internal/app/app_test.go`

## Phase 3: User Story 1 - Install From An Alpha Package (P1)

**Goal**: Install an extracted alpha package into an operator-selected prefix and verify the installed layout without source checkout paths.

**Independent Test**: Build a package, install to a temporary prefix, run installed `hideout package verify`, and prove helpers/schemas/scripts are discoverable.

- [X] T013 [P] [US1] Add package artifact verification unit tests in `internal/packagekit/install_test.go` for FR-001/FR-004/SC-001
- [X] T014 [P] [US1] Add installed-state manifest tests in `internal/packagekit/install_test.go` for actual prefix/store recording and non-relocatable verify behavior for FR-003
- [X] T015 [US1] Implement artifact manifest loading and artifact verification in `internal/packagekit/verify.go`
- [X] T016 [US1] Implement install copy plan and installed-state manifest writer in `internal/packagekit/install.go`
- [X] T017 [US1] Implement installed prefix verification and relocation mismatch denial in `internal/packagekit/verify.go` for FR-003
- [X] T018 [US1] Wire `hideout package install` and enhanced `hideout package verify` to `internal/packagekit` in `internal/app/app.go`
- [X] T019 [US1] Convert `packaging/install-package.sh` into a thin wrapper around packaged `hideout package install`
- [X] T020 [US1] Expand `scripts/package-local.sh` to emit complete artifact manifest metadata for helpers, schemas, scripts, docs, migration range, and executable flags
- [X] T021 [US1] Expand `scripts/test-package-smoke.sh` fresh-install section to verify installed prefix without source checkout paths
- [X] T022 [US1] Add failure smoke for missing helper and checksum mismatch before copy in `scripts/test-package-smoke.sh`

## Phase 4: User Story 2 - Upgrade Without Losing State (P2)

**Goal**: Installing over an existing prefix upgrades package-owned files while preserving durable store state.

**Independent Test**: Install version A fixture, create durable store files, install compatible version B, and verify durable files remain; incompatible migration fails before mutation.

- [X] T023 [P] [US2] Add compatible upgrade preservation tests in `internal/packagekit/install_test.go` for FR-006/SC-004
- [X] T024 [P] [US2] Add incompatible migration fail-before-mutation tests in `internal/packagekit/install_test.go` for FR-007/SC-005
- [X] T025 [US2] Implement existing installed-state detection and compatible upgrade validation in `internal/packagekit/install.go`
- [X] T026 [US2] Implement idempotent reinstall result reporting in `internal/packagekit/install.go`
- [X] T027 [US2] Add CLI output tests for install vs upgrade vs idempotent reinstall in `internal/app/app_test.go`
- [X] T028 [US2] Expand `scripts/test-package-smoke.sh` upgrade fixture assertions for durable store preservation and incompatible migration denial

## Phase 5: User Story 3 - Uninstall Safely (P3)

**Goal**: Uninstall package-owned files precisely, preserve durable state by default, and require explicit purge for durable state deletion.

**Independent Test**: Install package, run dry-run, uninstall without purge, then uninstall with purge and inspect removal boundary.

- [X] T029 [P] [US3] Add uninstall dry-run tests in `internal/packagekit/uninstall_test.go` for FR-009/SC-006
- [X] T030 [P] [US3] Add uninstall preserve and purge tests in `internal/packagekit/uninstall_test.go` for FR-010/FR-011/SC-007/SC-008
- [X] T031 [US3] Implement uninstall planning from installed-state manifest in `internal/packagekit/uninstall.go`
- [X] T032 [US3] Implement dry-run, package-owned file removal, empty owned directory cleanup, and unrelated-file preservation in `internal/packagekit/uninstall.go`
- [X] T033 [US3] Implement explicit purge of durable store state and package audit evidence in `internal/packagekit/uninstall.go`
- [X] T034 [US3] Wire `hideout package uninstall --prefix [--store] [--dry-run] [--purge]` in `internal/app/app.go`
- [X] T035 [US3] Add CLI uninstall output tests in `internal/app/app_test.go`
- [X] T036 [US3] Expand `scripts/test-package-smoke.sh` uninstall dry-run, preserve, unrelated-file, and purge assertions

## Phase 6: User Story 4 - Documentation Uses Packaged Paths (P4)

**Goal**: README/docs present installed package commands as the main alpha path and label source checkout commands as development-only.

**Independent Test**: Scan docs and run package smoke to ensure primary examples use package commands.

- [X] T037 [P] [US4] Update `README.md` installation and first-run sections to use package tarball and installed commands
- [X] T038 [P] [US4] Update `docs/README.md` and `docs/STATUS.md` for 013 packaging status and deferred packaging surfaces
- [X] T039 [P] [US4] Update `docs/privacy-run-test-plan.md` to describe Gate 0 package smoke and installed-layout proof
- [X] T040 [US4] Add docs smoke assertions in `scripts/test-package-smoke.sh` or `scripts/test-gate0.sh` that primary install docs avoid repo-only commands

## Phase 7: Polish And Cross-Cutting

- [X] T041 [P] Update `internal/app/app_test.go` package verify tests to cover installed-prefix relocation mismatch and doctor-style hints
- [X] T042 [P] Validate `schemas/package-manifest.schema.json` with package artifact and installed-state fixtures in `scripts/test-package-smoke.sh`
- [X] T043 Update `scripts/test-gate0.sh` to keep package smoke as a required Gate 0 check and include 013 schema/doc checks
- [X] T044 Run markdownlint for README, docs, and `specs/013-packaging-install-upgrade-uninstall/**/*.md`
- [X] T045 Run `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, and `go test ./...`
- [X] T046 Run `scripts/test-package-smoke.sh` and `scripts/test-gate0.sh`
- [X] T047 Mark all completed 013 tasks as checked in `specs/013-packaging-install-upgrade-uninstall/tasks.md`

## Dependencies

- Setup and Foundational tasks block all user stories.
- US1 is the MVP and must land before US2/US3 because upgrade and uninstall need installed-state manifests.
- US2 depends on US1 installed-state behavior.
- US3 depends on US1 installed-state behavior and can proceed independently of US2 after US1.
- US4 can run after command names and package layout stabilize.
- Polish runs after US1-US4.

## Parallel Opportunities

- T003/T004 can run after T001/T002.
- T007 and T009 can run in parallel after data types and path helpers are clear.
- T013/T014 can run in parallel because they cover different install dimensions.
- T023/T024 can run in parallel.
- T029/T030 can run in parallel.
- T037/T038/T039 can run in parallel after CLI contract stabilizes.
- T041/T042 can run in parallel with final docs work.

## Implementation Strategy

MVP first: complete US1 so an alpha package installs and verifies from a
temporary prefix without source checkout paths. Then add upgrade preservation
and uninstall safety. Finish by updating docs and Gate 0 so external alpha
users see the packaged path as the product path.
