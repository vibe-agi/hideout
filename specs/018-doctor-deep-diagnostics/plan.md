# Implementation Plan: Doctor Deep Diagnostics

<!-- markdownlint-disable MD013 -->

**Branch**: `018-doctor-deep-diagnostics` | **Date**: 2026-07-09 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/018-doctor-deep-diagnostics/spec.md`

## Summary

Upgrade the existing 015 doctor surface from shallow feature inventory into useful local troubleshooting. The implementation keeps report mode read-only and local, but deep/feature diagnostics now attach structured observed facts, candidate causes, gate-required markers, and next actions to the same `internal/doctor.Report` model used for JSON/evidence. Human output is rendered from the same findings so it no longer drops feature diagnostics. Existing code anchors are `internal/app/app.go` `doctor`, `addDoctorFeatureDiagnostics`, package verification from `internal/packagekit`, decision status from Manager, adapter/daemon/cleanup overview facts, and deterministic redaction from `internal/doctor/report.go`.

## Technical Context

**Language/Version**: Go 1.25 module.

**Primary Dependencies**: Existing standard library plus current project packages only; no new diagnostic framework, telemetry, browser, or backend dependency.

**Storage**: Hideout store files, installed package state and obsolete-file metadata from 017, profile data, adapter registry, decision/notice store, daemon runtime/status files, HostFS overlay/profile state, doctor evidence JSON, and local audit.

**Testing**: `go test ./...`, targeted `internal/app` and `internal/doctor` tests, doctor smoke in Gate 0, markdownlint, gofmt, vet, diff-check, and existing package/doctor smoke scripts.

**Target Platform**: macOS and Linux hosts supported by the current CLI. Native remains a weak local harness and cannot prove backend isolation.

**Project Type**: Single Go CLI/local-control-plane application.

**Performance Goals**: Light doctor stays fast and local. Deep/feature doctor may read more local stores but must not start guests, run real Gate 2/3, probe network, or mutate HostFS/package state.

**Constraints**: Doctor findings must be deterministic, redacted, local, and non-mutating by default. Deep findings may mark proof as gate-required but must not claim root cause or release readiness. Human and JSON outputs must derive from one report model.

**Scale/Scope**: Single-operator local machine with dozens of local findings across adapters, decisions, packaging, DNS, HostFS, privilege, daemon, export/redaction, cleanup, and Lima/backend readiness.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Doctor observes package, adapter, decision, daemon, profile, backend, DNS, HostFS, privilege, export, and cleanup state. It does not execute new authority. Unsupported or ambiguous proof becomes warn/error or gate-required status before claims.
- **Typed Authority**: Report generation is Go-owned in `internal/doctor` and `internal/app`. Existing safe recovery remains typed InitTask-style repair only; this feature adds no Manager apply operation.
- **Workspace And Policy**: Doctor does not alter workspace mounts, HostFS grants, proxy secrets, profile policy, adapter enablement, decision ownership, or package files in report mode. Repair guidance is command text unless an existing typed safe fix is invoked.
- **Generality And Provider Scope**: Check ids and finding types are generic Hideout diagnostics. Lima, `tun2socks`, package helpers, git, and smoke fixtures appear only as provider/local prerequisite facts.
- **Evidence And Redaction**: Human output, JSON report, evidence file, local audit/recovery evidence, and export-selected doctor report use the same deterministic redaction boundary. Tests inject representative control-plane-looking values rather than relying on empty scans.
- **Backend And Distribution**: Deep diagnostics do not start or certify backends. Packaging diagnostics reuse 017 package state and explicitly distinguish package-owned helpers from external prerequisites.
- **Gates**: Gate 0 is required: doctor smoke for deep mode, feature selectors, warning/error exit semantics, redaction injection, and safe dry-run. Real Gate 2/3 remain outside doctor.
- **Status And Docs**: Update `docs/STATUS.md`, `docs/privacy-run-test-plan.md`, and README/docs command guidance for deep/feature doctor semantics.

## Project Structure

### Documentation (this feature)

```text
specs/018-doctor-deep-diagnostics/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── doctor-command.md
│   └── doctor-report.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/doctor/
├── report.go              # existing report/finding/redaction model, extended only if needed
├── render.go              # existing JSON rendering; human parity may use shared findings
└── *_test.go              # report, schema, redaction, parity tests

internal/app/app.go        # doctor CLI, feature diagnostics, human output parity
internal/app/app_test.go   # CLI/deep/feature/redaction/smoke-style tests
internal/packagekit/       # 017 package verification and obsolete-file facts reused
internal/manager/          # overview and decision status facts reused
scripts/test-doctor-smoke.sh
scripts/test-gate0.sh
docs/
```

**Structure Decision**: No new package is required. 015 already introduced `internal/doctor`, and 018 is a focused upgrade of the existing report model and app-level diagnostic assembly. Adding another diagnostic package would split one report contract and make parity harder.

## Phase 0 Research

See [research.md](research.md).

## Phase 1 Design

See [data-model.md](data-model.md), [contracts/doctor-command.md](contracts/doctor-command.md), [contracts/doctor-report.md](contracts/doctor-report.md), and [quickstart.md](quickstart.md).

## Constitution Check Post-Design

- **Privacy Boundary**: PASS. Report mode is read-only and all backend/network/HostFS proof beyond local facts is expressed as gate-required.
- **Typed Authority**: PASS. Diagnostic decisions and rendering are Go-owned; no JavaScript/config executes authority.
- **Workspace And Policy**: PASS. Doctor reads workspace/profile facts but does not broaden or mutate policy.
- **Generality And Provider Scope**: PASS. Named dependencies are local prerequisites, not generic product semantics.
- **Evidence And Redaction**: PASS. Contracts require one redaction boundary across human, JSON, evidence, audit, and export-selected reports.
- **Backend And Distribution**: PASS. Package-owned helper facts are separated from external prerequisites such as `tun2socks`.
- **Gates**: PASS. Gate 0 plus targeted unit tests are sufficient because doctor does not make new isolation claims.
- **Status And Docs**: PASS. Status/test-plan/readme updates are part of tasks.

## Complexity Tracking

No constitution violations. No new authority surface, package, daemon endpoint, backend probe, or external dependency is introduced.
