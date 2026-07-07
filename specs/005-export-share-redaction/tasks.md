<!-- markdownlint-disable MD013 -->

# Tasks: Export/Share Redaction Boundary

**Input**: Design documents from `specs/005-export-share-redaction/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/,
quickstart.md

**Tests**: REQUIRED — this feature touches Hideout authority (audit, redaction,
release evidence). Every story includes at least one positive and one
fail-closed/redaction test before implementation.

**Organization**: Grouped by user story (US1 P1 → US2 P2 → US3 P3) so each is an
independently testable increment.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: US1/US2/US3 for user-story tasks; none for Setup/Foundational/Polish
- Paths are repository-root Go packages per plan.md.

---

## Phase 1: Setup (Shared Infrastructure)

- [X] T001 Create the `internal/export` package skeleton with a doc comment stating it owns the export/share boundary, reuses `internal/audit` for the control-plane strip and the `audit.redact` evaluator for user data, and MUST NOT reuse the broker's `preserveBrokerAuditMetadata` restore, in `internal/export/doc.go`
- [X] T002 [P] Add the exported-artifact envelope + provenance JSON schema (`additionalProperties:false`, version `hideout.export/v1`, per contracts/export-artifact.md) in `schemas/export-artifact.schema.json`
- [X] T003 [P] Register `schemas/export-artifact.schema.json` in the schema-validate tooling and the Gate 0 schema list in `scripts/test-gate0.sh`

---

## Phase 2: Foundational (Blocking Prerequisites)

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T004 Define the Core-owned evidentiary key set (`requestId`, `subject`, `command`, `route`, `requestedAction`, `status`, `error`) once as an exported Go symbol in `internal/audit/evidentiary.go`, and refactor `preserveBrokerAuditMetadata` (`internal/broker/broker.go:947`) to consume it so broker (restore) and export (fail-closed) share one source of truth
- [X] T005 [P] Define the exported-artifact envelope + provenance Go types (version, provenance{source,commit,createdAt,redactionStages,decision}, recordCount, body, notice) matching the schema, in `internal/export/artifact.go`
- [X] T006 [P] Implement read-only source readers (audit slice via `manager.AuditEvents`; release-evidence bundle manifest+logs; `manager.BoundarySummary`) with no redaction yet, in `internal/export/source.go`
- [X] T007 Implement the deterministic control-plane strip re-assertion over every artifact field, reusing `internal/audit` `RedactDetails`/`RedactString`/`RedactValue`, in `internal/export/redact.go`
- [X] T008 Implement the local meta-audit summary emit (a local `audit.Event`; summary fields incl. `out` path; control-plane strip at `audit.Writer.Emit`; explicitly NO export stage 2 at emit) in `internal/export/metaaudit.go`
- [X] T009 Add the `hideout audit export` subcommand dispatch and flag-parsing skeleton beside `audit show` (`internal/app/app.go:3730`) in `internal/app/app.go`
- [X] T010 Define the Manager typed `evidence.export` plan/apply op + Go validator boundary (plan returns the pre-export review; apply performs the export) mirroring existing Manager plan/apply ops, in `internal/manager/export.go`

**Checkpoint**: Package, schema, shared evidentiary set, readers, control-plane strip, meta-audit, and command/Manager skeletons exist.

---

## Phase 3: User Story 1 - Share without leaking control-plane secrets (Priority: P1) 🎯 MVP

**Goal**: Produce a shareable artifact from any of the three sources that provably
carries no Hideout-minted control-plane secret and carries provenance.

**Independent Test**: Seed each source with known control-plane material and user
data, export with `--acknowledge-full-fidelity`, and assert the artifact is
control-plane-clean, well-formed, and references nothing un-exported.

### Tests for User Story 1

- [X] T011 [P] [US1] Unit: control-plane cleanliness across all three sources (seed `HIDEOUT_SECRET_*`, `cap_`/`ui_` token, `capabilityToken` field, `machineId=<32hex>`; assert absent) in `internal/export/redact_test.go` (SC-001, FR-002)
- [X] T012 [P] [US1] Unit: reference resolution — a Boundary Summary `auditPath` and bundle logs are inlined-and-redacted or refused, never a dangling local path, in `internal/export/source_test.go` (FR-006)
- [X] T013 [P] [US1] Unit: empty/filtered-to-nothing export yields a valid zero-record artifact + "0 records matched" notice, not an error, in `internal/export/export_test.go` (Edge Cases)
- [X] T014 [P] [US1] Unit: `--out` accepts only a local path and the command performs no send/upload, in `internal/export/export_test.go` (FR-011)

### Implementation for User Story 1

- [X] T015 [US1] Implement artifact assembly (envelope + provenance + control-plane-stripped body; write to a temp path and rename only on success so no partial file) in `internal/export/export.go` (depends on T005, T007)
- [X] T016 [US1] Implement the `--acknowledge-full-fidelity` decision path (produce artifact with residual user data verbatim, control-plane stripped) in `internal/export/export.go`
- [X] T017 [US1] Implement reference resolution (inline-and-redact, else refuse) for bundle logs and the Boundary Summary `auditPath` in `internal/export/source.go` (FR-006)
- [X] T018 [US1] Wire the CLI export happy path — `--source`, per-source selectors, `--out`, `--acknowledge-full-fidelity` — plus the Manager apply, and emit the meta-audit on success, in `internal/app/app.go` and `internal/manager/export.go`

**Checkpoint**: `hideout audit export ... --acknowledge-full-fidelity` produces a control-plane-clean artifact for all three sources.

---

## Phase 4: User Story 2 - Choose what user data is scrubbed (Priority: P2)

**Goal**: Apply operator-selected user-data redaction via the `audit.redact`
policy with export semantics, enforcing the Core-owned evidentiary floor Go-side.

**Independent Test**: With an `audit.redact` selection, export and assert the
selected user field is scrubbed while unselected user data is preserved and the
evidentiary set is intact; a selection targeting an evidentiary field fails closed.

### Tests for User Story 2

- [X] T019 [P] [US2] Unit: SC-006 — a `--redact`-selected user field is scrubbed and an unselected user field is preserved, in `internal/export/redact_test.go`
- [X] T020 [P] [US2] Unit: SC-007 — a `--redact` selector naming an evidentiary field is rejected at parse time (fail closed, no script run); and a policy that mutates an evidentiary key is caught by the post-script 7-key compare (fail closed), in `internal/export/redact_test.go`
- [X] T021 [P] [US2] Unit: SC-008 — `--acknowledge-full-fidelity` with a configured policy still redacts policy-selected fields (no bypass), in `internal/export/redact_test.go`
- [X] T022 [P] [US2] Unit: offline policy resolution — per-event by `audit.Event.Profile` for the audit source, and `--policy-profile` for bundle/boundary-summary, in `internal/export/redact_test.go`

### Implementation for User Story 2

- [X] T023 [US2] Implement offline `audit.redact` resolution — `store.Load(name).Policy.ScriptRefs` (entrypoint `redactAudit`) against `store.ProfileDir(name)` → `policy.Evaluator.RunAuditRedactScript`; per-event by `audit.Event.Profile`, else `--policy-profile` — in `internal/export/redact.go` (research.md offline-resolution decision)
- [X] T024 [US2] Implement the `--redact <selector>` grammar parse, parse-time rejection of any selector naming an evidentiary field, and injection as `ctx.Extra["exportRedaction"]`, in `internal/export/redact.go`
- [X] T025 [US2] Implement the post-script evidentiary compare (returned `details` vs input for the seven evidentiary keys → fail closed on any change) in `internal/export/redact.go`
- [X] T026 [US2] Implement residual computation (`details` keys minus control-plane minus evidentiary) and the acknowledge-covers-residual rule, in `internal/export/redact.go`
- [X] T027 [US2] Wire `--redact` and `--policy-profile` into the CLI and the Manager op, and record each applied policy's `id`+`sha256` into provenance `redactionStages`, in `internal/app/app.go` and `internal/manager/export.go`

**Checkpoint**: User-data selection works with export semantics; the evidentiary floor is Go-enforced.

---

## Phase 5: User Story 3 - Refuse to ship anything unredacted (Priority: P3)

**Goal**: The full fail-closed spine and the dual-track decision — no artifact is
ever produced without the control-plane strip and a conscious user-data decision.

**Independent Test**: Force each failure mode (missing decision, strip error,
policy error) and assert no artifact and no partial file; confirm a meta-audit
event records every outcome.

### Tests for User Story 3

- [X] T028 [P] [US3] Unit: user data present + no selection + no acknowledge + no TTY → export refused, no artifact, no partial file, specific diagnostic, in `internal/export/export_test.go` (FR-004, FR-012)
- [X] T029 [P] [US3] Unit: control-plane strip error and `audit.redact` policy error each fail closed with no artifact, in `internal/export/export_test.go`
- [X] T030 [P] [US3] Unit: a meta-audit event is emitted on both success and fail-closed (with reason), and passes only the control-plane strip (local summary; no export stage 2), in `internal/export/metaaudit_test.go` (SC-005, FR-007)
- [X] T031 [P] [US3] Unit: local full-fidelity surfaces unchanged — `hideout audit show`/local JSONL still present user data verbatim after export ships, in `internal/app/app_test.go` (FR-005, SC-004)
- [X] T032 [P] [US3] Unit: Manager typed `evidence.export` parity — plan returns the pre-export review; apply produces the same artifact as the CLI; policy failure and missing-decision each fail closed identically to the CLI; `--acknowledge-full-fidelity` does not bypass a configured policy, in `internal/manager/export_test.go` (contracts/export-command.md Manager parity; FR-004, SC-008)
- [X] T033 [P] [US3] Unit: pre-export review — built from authoritative export facts by a shared builder used by BOTH the CLI TTY path and the Manager plan; lists included vs redacted content and the decision required; and is itself control-plane redacted, in `internal/export/review_test.go` (FR-008, SC-003)

### Implementation for User Story 3

- [X] T034 [US3] Implement the fail-closed decision gate (residual user data present with no decision on any channel → refuse) and the no-partial-file guarantee across all failure modes, in `internal/export/export.go`
- [X] T035 [US3] Implement the shared pre-export review builder (authoritative export facts, control-plane redacted) plus the dual-track decision — non-interactive flags and an interactive TTY confirmation — consumed by BOTH `internal/app/app.go` and the Manager plan (`internal/manager/export.go`), in `internal/export/review.go` (FR-008, SC-003)
- [X] T036 [US3] Ensure the meta-audit event is emitted on every outcome including fail-closed (with the refusal reason), in `internal/export/export.go` and `internal/export/metaaudit.go`

**Checkpoint**: Every path either produces a provably-clean artifact or fails closed with a recorded reason.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T037 [P] Add the exported-artifact-cleanliness static check (seed known control-plane material + user data into a source, export, assert the artifact is clean and the selection took effect) and schema validation to `scripts/test-gate0.sh`
- [X] T038 [P] Add `scripts/test-export-redaction-smoke.sh` exercising the three sources end to end (control-plane clean, selection applied, evidentiary fail-closed)
- [X] T039 [P] Update `docs/STATUS.md` — promote the export/share redaction surface from design-ready to implemented
- [X] T040 [P] Update `docs/threat-model.md` — sharing under the boundary becomes a claim; raw hand-copy remains the unsafe path
- [X] T041 [P] Update `docs/privacy-run-design.md` — Audit and Explain: the export surface and its two-stage redaction contract
- [X] T042 [P] Update `docs/privacy-run-test-plan.md` — the export gate (Gate 0 cleanliness + unit)
- [X] T043 Run `go test ./...`, `scripts/test-gate0.sh`, and the quickstart.md validation; confirm green with no real-Lima requirement

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies.
- **Foundational (Phase 2)**: depends on Setup; BLOCKS all user stories. T004 (shared evidentiary set) blocks US2's evidentiary enforcement; T005–T010 block artifact/command work.
- **User Stories (Phase 3–5)**: depend on Foundational. US1 is the MVP. US2 depends on US1's export pipeline (artifact assembly, source readers). US3 hardens US1+US2 with the full fail-closed spine.
- **Polish (Phase 6)**: depends on the desired stories being complete.

### User Story Dependencies

- **US1 (P1)**: after Foundational. Independently testable (control-plane cleanliness with `--acknowledge-full-fidelity`).
- **US2 (P2)**: builds on US1's pipeline; independently testable (selection scrubs a field; evidentiary selection fails closed).
- **US3 (P3)**: builds on US1+US2; independently testable (each failure mode refuses; meta-audit on every outcome).

### Within Each User Story

- Tests (authority/redaction/fail-closed) written and failing before implementation.
- `internal/export` types/readers before assembly; assembly before CLI/Manager wiring.

### Parallel Opportunities

- Setup: T002, T003 in parallel.
- Foundational: T005, T006 in parallel (different files); T004 is standalone; T007/T008 after types.
- Each story's test tasks ([P]) run in parallel; the `redact.go` implementation tasks in US2 (T023–T026) touch one file and are sequential.
- Polish T037–T042 in parallel; T043 last.

---

## Parallel Example: User Story 1 tests

```bash
# Launch US1 tests together (they touch different test files):
Task: "T011 control-plane cleanliness across sources in internal/export/redact_test.go"
Task: "T012 reference resolution in internal/export/source_test.go"
Task: "T013 empty export in internal/export/export_test.go"
Task: "T014 local-only --out in internal/export/export_test.go"
```

---

## Implementation Strategy

### MVP First (User Story 1)

1. Phase 1 Setup → Phase 2 Foundational (CRITICAL) → Phase 3 US1.
2. STOP and VALIDATE: export produces a control-plane-clean artifact for all three
   sources with `--acknowledge-full-fidelity`.
3. Demo: a shareable artifact that provably carries no control-plane secret.

### Incremental Delivery

1. Setup + Foundational → foundation ready.
2. US1 → control-plane-clean export (MVP).
3. US2 → operator-selected user-data redaction with export semantics.
4. US3 → full fail-closed spine + dual-track decision.
5. Polish → Gate 0 cleanliness check, docs, final validation.

---

## Notes

- [P] = different files, no dependencies.
- This feature makes a data-handling/redaction claim, not an isolation claim:
  Gate 0 + `go test ./...` only, no real-Lima.
- The evidentiary floor is Go-owned (parse-time reject + post-script compare); the
  `audit.redact` policy is the only flexible decision point.
- The meta-audit event is a LOCAL summary — never assert export stage-2 user-data
  rules on it.
- Update `docs/STATUS.md` and `docs/threat-model.md` (Phase 6) since implemented
  status and claims change.
