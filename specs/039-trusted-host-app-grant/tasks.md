---
description: "Task list for Trusted Host-App Workspace Grant (039)"
---

# Tasks: Trusted Host-App Workspace Grant

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `/specs/039-trusted-host-app-grant/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: REQUIRED. This feature changes host-app projection authorization and
profile policy. Every new authority assertion ships with a mutation proof (break
the guard, observe red, restore) and every new judge with a negative fixture
(constitution 1.3.0).

**Organization**: Tasks grouped by user story. US1 + US2 are the P1 MVP; US3
(revoke/drift/visibility) is required before the grant is safe to ship.

## Format: `[ID] [P?] [Story] Description`

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Define the grant record type and schema so all later phases build
on one shape. No behavior change yet.

- [X] T001 [P] Add the trusted host-app grant manifest type and schema-version constant (`workspaceId`, `qualifiedAppRef`, `bindingDigest`, `grantedAt`; manifest carries version + profile) in `internal/manager/trusted_host_app_grant.go`, per [data-model.md](data-model.md).
- [X] T002 [P] Add `schemas/trusted-host-app-grant.schema.json` for the grant manifest and wire its existence check into `scripts/test-gate0.sh` (mirror the existing schema-file checks).
- [X] T003 [P] Write failing type/round-trip tests (strict decode rejects unknown fields; malformed manifest decodes as no-grants, never implicit allow) in `internal/manager/trusted_host_app_grant_test.go`.

**Checkpoint**: grant record shape exists and is schema-validated; no read/write
or check path yet.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Persistent grant storage and the match predicate that every story
depends on. No user story can pass until this is done.

**CRITICAL**: Complete before Phase 3.

- [X] T004 Implement grant manifest read/write in `internal/manager/trusted_host_app_grant.go`: path `profiles/<p>/host-app-trust-grants.json`, `0600`, atomic temp+rename, guest-unreachable (mirror `host-app-mode.json` placement); fail-closed on malformed/unreadable.
- [X] T005 Implement the grant match predicate `(workspaceId, qualifiedAppRef, bindingDigest)` equality plus `ReadProjectionHostAppMode == trusted`, and add/refresh/remove-by-key helpers, in `internal/manager/trusted_host_app_grant.go`.
- [X] T006 [P] Write failing foundational tests in `internal/manager/trusted_host_app_grant_test.go`: exact match, each-field mismatch (workspace, appRef, digest), safe-mode never matches, atomic write leaves no partial file, `0600` mode, duplicate-key collapse. Each match/deny assertion includes a mutation proof.

**Checkpoint**: grant storage + match predicate proven in isolation.

---

## Phase 3: User Story 1 - Grant once, open natively thereafter (Priority: P1)

**Goal**: An operator grants trusted host app once; later one-shot `code .` opens the
native editor with no per-run approval.

**Independent Test**: real Lima, trusted mode, granted workspace → one-shot
`hideout run -- code .` opens native, exit 0, no prompt; a separate run reuses.

### Tests for User Story 1

- [X] T007 [P] [US1] Write failing test: `runProjectionGrantChecker.TrustedGrantActive` returns true when a matching persistent grant exists (before any per-run decision), in `internal/manager/run_dataplane_host_app_test.go`. Include a mutation proof (remove the grant lookup → red).
- [X] T008 [P] [US1] Write failing grammar test for `allow host-app code` in `internal/operatorintent/intent_test.go` (parses to a typed host-app trust intent; unknown trailing words rejected; `--for-profile` scope).

### Implementation for User Story 1

- [X] T008a [US1] **Derivation-equality gate (analyze U1)**: prove the grant command's independently derived `workspaceID` equals the run-derived one — a shared-env unit test comparing the two derivations for the same project, plus a real-Lima check. The whole grant command (T011) depends on this equality; if it cannot be proven, fall back to request-driven promotion (the spike-proven path: the run records a request, the grant command promotes it) and note it in research.md. Do this before T011.
- [X] T009 [US1] Add the persistent-grant check to `runProjectionGrantChecker.TrustedGrantActive` in `internal/manager/run_dataplane.go`, before the per-run decision lookup, per [contracts/trusted-host-app-grant-record.md](contracts/trusted-host-app-grant-record.md).
- [X] T010 [US1] Add `allow host-app code` (and reserve `deny host-app code`) to the operator grammar in `internal/operatorintent/intent.go`.
- [X] T011 [US1] Implement the grant command in `internal/app/operator_access.go`: derive `workspaceID` from the project dir (same functions a run uses), read the built-in VS Code binding `qualifiedAppRef` + `bindingDigest` from the compiled catalog, write the grant under the profile mutation lock; require host-app-mode trusted (else name `host-app-mode trusted`). Wire dispatch in `internal/app/app.go` and add the usage line.
- [X] T012 [US1] Emit a typed `host-app.trust` grant audit event and a trusted-launch reuse audit (Core-derived identifiers only) in `internal/manager/trusted_host_app_grant.go` / projection audit path.
- [X] T013 [US1] Add app-level test in `internal/app/operator_access_test.go`: `allow host-app code` writes a matching grant, is idempotent, refuses when host-app-mode is safe, and leaks no host path/secret; includes a mutation proof.

**Checkpoint**: grant + cross-run reuse works end to end at unit level.

---

## Phase 4: User Story 2 - Fail closed with a clear path (Priority: P1)

**Goal**: Trusted mode with no grant refuses `code .` with no host launch and
names the grant command.

**Independent Test**: trusted mode, no grant → `code .` refused, no editor, and
the refusal names `hideout allow host-app code`.

### Tests for User Story 2

- [X] T014 [P] [US2] Write failing test: trusted mode with no matching grant → open refused (no launch) and the guest-visible message names the grant command, in `internal/broker/hostapp_test.go` (or the projection open test). Include a mutation proof on the message/branch.

### Implementation for User Story 2

- [X] T015 [US2] Ensure the no-grant trusted refusal carries a guest-visible stderr naming `hideout allow host-app code`, reusing the broker host-app response channel, in `internal/broker/hostapp.go`; keep the refusal fail-closed (no host launch), per [contracts/operator-grant-commands.md](contracts/operator-grant-commands.md).
- [X] T016 [US2] Confirm the refusal audit (`host.app.open-resource` outcome refused) records the trusted-denied reason with Core-derived identifiers only.

**Checkpoint**: refused → named path → grant → native is a complete guided loop.

---

## Phase 5: User Story 3 - Revoke and drift return to safe/re-confirmed (Priority: P2)

**Goal**: Grants are revocable and self-invalidate on drift; existence is
visible.

**Independent Test**: revoke (or safe mode) returns `code .` to guided/safe;
different workspace or changed binding digest does not reuse a grant; `host-app-mode`
shows grant existence.

### Tests for User Story 3

- [X] T017 [P] [US3] Write failing tests in `internal/manager/trusted_host_app_grant_test.go`: `deny host-app code` removes the grant; switching profile to safe deletes all profile grants; workspace-identity drift and bindingDigest drift both fail the match. Mutation proofs on each.
- [X] T018 [P] [US3] Write failing adversarial test: a guest writing the workspace (including a forged grant-looking file in `/workspace`) cannot create/refresh/read a grant, in `internal/manager/trusted_host_app_grant_test.go` (grant lives under `profiles/<p>/`). Negative fixture required.

### Implementation for User Story 3

- [X] T019 [US3] Implement `deny host-app code` in `internal/operatorintent/intent.go` + `internal/app/operator_access.go` (remove the current workspace's grant under the mutation lock; no-op if absent); emit a `host-app.trust` revoke audit.
- [X] T020 [US3] Extend the safe-mode switch in `internal/manager/hostcap_projection.go` (`SetProjectionHostAppMode` → safe) to delete the profile's trusted host-app grants alongside the existing decision invalidation; emit a revoke=all audit.
- [X] T021 [US3] Show trusted host-app grant existence in `hideout profile host-app-mode <p>` output (`internal/app/app.go` / projection inspection), without leaking host paths.

**Checkpoint**: full lifecycle (grant/reuse/refuse/revoke/drift) proven at unit
level with adversarial coverage.

---

## Phase 6: Polish & Cross-Cutting

- [X] T022 [P] Remove `decisionHostAppGrantChecker` or explicitly rename/comment it test-only, so exactly one production trusted-grant check path exists (FR-011, SC-006), in `internal/manager/hostcap_projection.go`; update its tests.
- [X] T023 Extend the real-Lima projection lane (`scripts/` host-capability projection E2E) to assert Scenarios 1–3 from [quickstart.md](quickstart.md): grant → separate-run native reuse → refuse without grant → revoke; record projection evidence with stable proof id.
- [X] T024 [P] Update docs: `docs/host-capability-projection.md` (trusted grant lifecycle), `docs/first-run-alpha.md` (replace the "trusted not usable for one-shot" limitation with the grant flow), `docs/STATUS.md` (projection row), `docs/claim-boundaries.md` (grant claim + proof id).
- [X] T025 [P] Update `docs/DEBT.md`: close the trusted-host-app one-shot deadlock entry and the two-checker entry; keep the first-boot shim flake entry.
- [X] T026 Full verification: `go build ./...`, `go vet ./...`, `gofmt -l`, `go test ./...`, `scripts/test-gate0.sh`, markdownlint; confirm every new authority assertion has a recorded mutation proof and every judge a negative fixture; ship the batch adversarial report (constitution 1.3.0).

---

## Dependencies & Execution Order

- **Setup (P1–T003)** → **Foundational (T004–T006)** block everything.
- **US1 (T007–T013)** depends on Foundational; it is the core grant/reuse path.
- **US2 (T014–T016)** depends on US1's check path (refusal is the no-grant branch
  of the same check).
- **US3 (T017–T021)** depends on Foundational + US1 (needs a grant to revoke/drift).
- **Polish (T022–T026)** last; T022 (twin removal) after the production path
  (T009) is in place; T023 real-Lima after US1–US3.

## Parallel Opportunities

- Setup: T001, T002, T003 in parallel (distinct files).
- Foundational tests T006 parallel to storage impl review.
- US1 tests T007, T008 in parallel (distinct files) before their impl.
- US3 tests T017, T018 in parallel.
- Docs T024, T025 in parallel.

## Implementation Strategy

- **MVP**: US1 + US2 (grant/reuse + fail-closed guidance) — the headline
  `code .` native path becomes usable.
- **Ship-safe**: US3 (revoke/drift/visibility) is required before the grant is
  promoted as the native-editor path; do not mark 039 done without it.
- Real-Lima (T023) is the end-to-end proof; native harness covers unit/contract.
