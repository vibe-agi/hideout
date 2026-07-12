# Implementation Plan: Error Codes And Recovery Hints

<!-- markdownlint-disable MD013 -->

**Branch**: `028-error-recovery-hints` | **Date**: 2026-07-09 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/028-error-recovery-hints/spec.md`

## Summary

Add a small Go-owned public recovery-code registry and wire it into selected
host-observable surfaces: doctor findings, package obsolete/prerequisite output,
init privacy prerequisite failures, release-readiness evidence failures, and
docs truth. The registry provides stable code, reason, hint, next actions, and
subsystem ownership. Existing fail-closed behavior and Manager/daemon response
shape remain unchanged unless a selected host-visible case already passes
through them.

## Technical Context

**Language/Version**: Go 1.x.

**Primary Dependencies**: Standard library, existing `internal/doctor`,
`internal/packagekit`, `internal/releasecompat`, and shell smoke tests.

**Storage**: N/A. Registry is Go constants/data; doctor reports are existing
JSON artifacts.

**Testing**: Unit tests for registry/schema/rendering; selected CLI tests in
`internal/app`; smoke checks through doctor/docs/Gate 0.

**Target Platform**: Local host-visible CLI/report surfaces.

**Project Type**: Go CLI/local control plane.

**Performance Goals**: No measurable runtime cost beyond formatting selected
errors and validating a small registry.

**Constraints**: No full error-model rewrite, no localization, no automatic
repair center, no new prompt/approval flow, no blanket Manager API migration.

**Scale/Scope**: Nine v1 public codes, selected host-observable outputs.

## Constitution Check

*GATE: Pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Only error/report metadata and selected output text are
  touched. Existing denials remain denials.
- **Typed Authority**: No new authority executor. Hints point to existing typed
  CLI/Manager paths when applicable.
- **Workspace And Policy**: No workspace, HostFS grant, proxy secret, profile,
  or policy mutation.
- **Generality And Provider Scope**: Generic Hideout support vocabulary. No
  provider or backend-specific behavior becomes Core semantics.
- **Evidence And Redaction**: Doctor JSON/human output, CLI output, docs truth,
  and Gate 0 prove codes. Recovery text passes existing redaction paths.
- **Backend And Distribution**: No helper or backend requirement added.
- **Gates**: Gate 0 is required; no real-gate or release-readiness claim is
  added.
- **Status And Docs**: Update `docs/STATUS.md`, `docs/privacy-run-test-plan.md`,
  `docs/first-run-alpha.md`, and docs truth smoke as needed.

**Post-design re-check**: PASS. The design adds structured metadata and tests
only; it does not expand product authority.

## Project Structure

### Documentation (this feature)

```text
specs/028-error-recovery-hints/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── recovery-code-registry.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/recovery/
├── registry.go
└── registry_test.go

internal/doctor/
├── report.go
├── render.go
├── report_test.go
└── report_schema_test.go

internal/app/
└── app_test.go

internal/releasecompat/
├── readiness.go
└── readiness_test.go

schemas/
└── doctor-report.schema.json

scripts/
├── test-doctor-smoke.sh
└── test-doc-truth-smoke.sh
```

**Structure Decision**: Put public code vocabulary in `internal/recovery` so
doctor, app, releasecompat, tests, and docs truth can consume one registry.

## Complexity Tracking

No constitutional violations.
