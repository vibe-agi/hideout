# Implementation Plan: Release Hardening And Compatibility Matrix

<!-- markdownlint-disable MD013 -->

**Branch**: `016-release-hardening-compatibility-matrix` | **Date**: 2026-07-09 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/016-release-hardening-compatibility-matrix/spec.md`

## Summary

Freeze the early-alpha release contract into a Go-owned support matrix, expose
that matrix through local CLI/doctor surfaces, and wrap existing validation
paths with release-readiness artifacts. 016 does not add new isolation
authority. It makes current platform/backend/schema/non-claim status explicit,
keeps local-fast checks distinct from release-candidate evidence, and gives Gate
0 a drift guard so shipped docs cannot silently diverge from the matrix.

## Technical Context

**Language/Version**: Go 1.25+ module, shell scripts for existing gates, JSON
Schema draft-compatible validation through `cmd/hideout-schema-validate`.

**Primary Dependencies**: Standard library only for new Go package; existing
`internal/audit`, `internal/doctor`, CLI app dispatch, schema validator,
`jq`, `markdownlint-cli2`, existing shell gate scripts.

**Storage**: Repository files only: `internal/releasecompat` embeds the matrix
source, `schemas/support-matrix.schema.json`, `schemas/release-readiness.schema.json`,
`docs/support-matrix.md`, and optional readiness JSON artifacts written by
scripts to operator-selected temp/evidence directories.

**Testing**: `go test ./...`, focused `internal/releasecompat` tests, schema
validation via `go run ./cmd/hideout-schema-validate`, `scripts/test-gate0.sh`,
new release-hardening smoke, and existing package/doctor/release dogfood
scripts.

**Target Platform**: macOS arm64 first-class alpha; Linux amd64/aarch64
supported with narrower smoke coverage; native backend explicitly
degraded/development-only; unsupported platforms are reported without claims.

**Project Type**: CLI/control-plane Go application with shell-driven release
gates and Markdown/JSON-schema documentation.

**Performance Goals**: Matrix lookup, doctor support finding, and version
summary complete in under 100 ms without network or Lima. Local-fast smoke
should stay Gate0-sized; full release-candidate readiness may be long-running.

**Constraints**: No new host/guest/network authority; no replacement for real
Gate 2/Gate 3; no public SLA; no Windows support claim; release-candidate mode
fails closed when required real-gate evidence is missing.

**Scale/Scope**: One authoritative support matrix, two new JSON schemas, one
small Go package, one CLI/status surface, one release-hardening smoke, one
readiness script/artifact, and docs/status alignment for 008-016.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches release lifecycle, docs/status, doctor/version
  surfaces, schema fixtures, and release evidence only. It grants no new
  filesystem, network, backend, browser, HostFS, or command authority. Missing
  matrix entries, unsupported platforms, unknown schemas, missing real-gate
  evidence, and stale docs fail closed for release-ready status.
- **Typed Authority**: Go Core owns the matrix and readiness artifact builder.
  Shell scripts compose existing typed gates and never invent isolation claims.
  JavaScript/config does not participate in 016 authority.
- **Workspace And Policy**: Does not alter workspace mounts, HostFS grants,
  adapter policy, proxy secrets, or profile state. It records and validates
  current claims, including native degraded status and HostFS/DNS/privilege
  gate dependencies.
- **Generality And Provider Scope**: macOS/Linux support levels are product
  support labels. Existing dogfood/probe commands remain test fixtures, not
  generic provider defaults.
- **Evidence And Redaction**: Evidence is the matrix, readiness artifact,
  doctor finding, version/support output, docs, Gate0, package smoke, doctor
  smoke, release dogfood, and real Gate2/Gate3 records. Readiness output uses
  deterministic control-plane redaction and never embeds raw secrets.
- **Backend And Distribution**: No new helper binary. Package/install status is
  reported from the matrix and existing package smoke; native remains a weak
  harness and cannot count as isolation evidence.
- **Gates**: Gate0 is required and gains schema/drift smoke. Release-candidate
  readiness requires real Gate2/Gate3 evidence when those claims are in scope.
  Local-fast readiness is explicitly non-release.
- **Status And Docs**: Update `README.md`, `docs/STATUS.md`,
  `docs/threat-model.md`, `docs/privacy-run-test-plan.md`,
  `docs/backend-capability-matrix.md`, and add `docs/support-matrix.md`.

**Post-design re-check**: PASS. Design keeps a single Go-owned matrix, typed
readiness artifacts, local-fast versus release-candidate separation, and docs
drift checks. No new authority, helper, backend, network, or prompt channel is
introduced.

## Project Structure

### Documentation (this feature)

```text
specs/016-release-hardening-compatibility-matrix/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── support-matrix.md
│   ├── release-readiness.md
│   └── compatibility-fixtures.md
├── checklists/
│   └── requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/releasecompat/
├── matrix.go
├── readiness.go
├── docs.go
├── compat.go
└── *_test.go

internal/app/app.go
internal/doctor/report.go

schemas/
├── support-matrix.schema.json
└── release-readiness.schema.json

scripts/
├── test-release-hardening-smoke.sh
├── test-release-readiness.sh
└── test-gate0.sh

docs/
├── support-matrix.md
├── STATUS.md
├── backend-capability-matrix.md
├── privacy-run-test-plan.md
└── threat-model.md
```

**Structure Decision**: Keep the release contract in a small Go package so CLI,
doctor, tests, and scripts read one matrix. Shell remains the orchestrator for
existing gates; Go owns typed JSON shapes and drift checks.

## Complexity Tracking

No constitution violations. The new package is justified because support-matrix
logic must be shared by CLI, doctor, tests, and release scripts without copying
tables into Markdown or shell.
