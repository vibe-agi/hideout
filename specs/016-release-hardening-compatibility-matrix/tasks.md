# Tasks: Release Hardening And Compatibility Matrix

<!-- markdownlint-disable MD013 MD052 -->

**Input**: Design artifacts from `/specs/016-release-hardening-compatibility-matrix/`

## Phase 1: Setup

- [X] T001 Add `internal/releasecompat` package skeleton with matrix/readiness/compat docs files.
- [X] T002 Add `schemas/support-matrix.schema.json` and `schemas/release-readiness.schema.json`.
- [X] T003 Register new schema files in `scripts/test-gate0.sh`.
- [X] T004 Add release-hardening smoke script placeholder in `scripts/test-release-hardening-smoke.sh`.

## Phase 2: Foundational

- [X] T005 [P] Add support-matrix model and validation tests covering closed levels, required rows, and non-first-class reason/guidance.
- [X] T006 [P] Add readiness artifact model and redaction tests covering control-plane-shaped values.
- [X] T007 [P] Add compatibility fixture inventory tests covering every FR-011 family with accepted and rejected unknown-version fixtures.
- [X] T008 Implement Go-owned built-in support matrix in `internal/releasecompat/matrix.go`.
- [X] T009 Implement readiness builder/redaction in `internal/releasecompat/readiness.go`.
- [X] T010 Implement docs drift helper in `internal/releasecompat/docs.go`.
- [X] T011 Implement compatibility fixture helper in `internal/releasecompat/compat.go`.

## Phase 3: User Story 1 - Inspect Supported Alpha Matrix

- [X] T012 [P][US1] Add app tests for `hideout support matrix --json`, support summary output, and invalid subcommand failure.
- [X] T013 [P][US1] Add doctor support-matrix finding tests for native degraded and current platform support.
- [X] T014 Add `hideout support matrix [--json]` CLI dispatch in `internal/app/app.go`.
- [X] T015 Extend `hideout version` with matrix schema/version and current platform support lines without changing existing lines.
- [X] T016 Add support-matrix doctor finding while preserving degraded warning semantics.
- [X] T017 Validate CLI matrix output against `schemas/support-matrix.schema.json` in smoke.

## Phase 4: User Story 2 - Run Release Gate With Real Evidence

- [X] T018 [P][US2] Add readiness tests for local-fast non-release status and release-candidate missing Gate 2/Gate 3 fail-closed status.
- [X] T019 [P][US2] Add script smoke checks for readiness schema, redaction, and candidate missing-evidence failure.
- [X] T020 Implement `scripts/test-release-readiness.sh` with `--local-fast`, `--release-candidate`, `--out`, and real-gate evidence env vars.
- [X] T021 Implement release-hardening smoke to exercise matrix, doctor, readiness local-fast, and missing-evidence candidate paths.
- [X] T022 Ensure readiness artifacts include commit, platform, matrix version, command summaries, gate summaries, status, and non-claims.
- [X] T023 Ensure release-candidate can accept existing Gate 2/Gate 3 evidence references without inlining raw logs.
- [X] T024 Ensure local-fast readiness artifacts always set `releaseReady=false`.

## Phase 5: User Story 3 - Verify Compatibility And Migration Contract

- [X] T025 [P][US3] Add compatibility fixture tests for profile/package/adapter/doctor/export/decision/notice/HostFS/onboarding/daemon/live-console/run/init families.
- [X] T026 [P][US3] Add unknown major version fail-closed tests with recreate/upgrade guidance.
- [X] T027 Implement fixture inventory and validators in `internal/releasecompat`.
- [X] T028 Include compatibility fixture smoke in Gate0 release-hardening smoke.
- [X] T029 Ensure unknown fixtures fail before mutation, enablement, or apply by keeping tests local and parser/schema-backed.

## Phase 6: User Story 4 - Remove Stale Shipped/Not-Yet Documentation

- [X] T030 [P][US4] Add docs drift tests for matrix platform/backend claims and required non-claims.
- [X] T031 [P][US4] Add stale-claim scan for shipped 008-016 current docs while allowing historical/superseded spec trail.
- [X] T032 Add `docs/support-matrix.md` generated-from-matrix style content.
- [X] T033 Update README with alpha support matrix, doctor, readiness, and release-candidate gate guidance.
- [X] T034 Update `docs/STATUS.md`, `docs/backend-capability-matrix.md`, `docs/privacy-run-test-plan.md`, and `docs/threat-model.md` to align with matrix.
- [X] T035 Wire docs drift checks into release-hardening smoke and Gate0.

## Phase 7: Polish

- [X] T036 Validate all 016 spec artifacts with markdownlint.
- [X] T037 Run `go build ./...`.
- [X] T038 Run `go vet ./...`.
- [X] T039 Run `gofmt -l internal cmd`.
- [X] T040 Run `git diff --check`.
- [X] T041 Run `go test ./...`.
- [X] T042 Run `scripts/test-release-hardening-smoke.sh`.
- [X] T043 Run `scripts/test-gate0.sh`.
- [X] T044 Update `tasks.md` to 44/44 complete after successful verification.
