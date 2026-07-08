# Implementation Plan: Doctor Diagnostics And Recovery

<!-- markdownlint-disable MD013 -->

**Branch**: `015-doctor-diagnostics-recovery` | **Date**: 2026-07-09 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/015-doctor-diagnostics-recovery/spec.md`

## Summary

Promote `hideout doctor` from ad hoc CLI-only checks into a structured diagnostic and recovery product surface. The implementation adds a Go-owned doctor check/report model with stable check ids, human and JSON renderers, level/feature selection, redacted report evidence, and safe recovery plans that continue to use typed InitTask repair. Existing local checks in `internal/app/app.go:1929`, `doctorFix` at `internal/app/app.go:2132`, package verification in `internal/packagekit/verify.go:22`, adapter registry listing in `internal/adapterpack/registry.go:182`, HostFS overlay state in `internal/hostfs/overlay/store.go:373`, and deterministic redaction in `internal/audit/audit.go:114` become inputs to the shared report model instead of being recomputed by separate CLI branches.

## Technical Context

**Language/Version**: Go 1.25 module.

**Primary Dependencies**: Existing standard library plus current project dependencies only; no new diagnostic framework or external telemetry dependency.

**Storage**: Hideout store files, package manifests, profile files, daemon runtime metadata, adapter registry, decision/HostFS overlay state, and export-selected doctor reports.

**Testing**: `go test ./...`, targeted doctor package/app/manager tests, schema validation, doctor smoke in Gate 0, markdownlint, gofmt, vet, and diff-check.

**Target Platform**: macOS and Linux hosts supported by the current CLI/package paths. Native backend remains a weak local harness; Lima/deep checks are explicit.

**Project Type**: Single Go CLI/local-control-plane application.

**Performance Goals**: Default light doctor should complete quickly on a normal local store without starting a guest or network probe; deep/feature checks may be slower because they are explicitly selected.

**Constraints**: Default doctor must be offline/local, redacted, deterministic, and non-mutating. `--fix --dry-run` writes no durable state. `--fix --apply` only applies typed safe repairs. JSON output must be stable enough for Gate 0 and smoke assertions.

**Scale/Scope**: Single-operator local machine. Dozens of checks across package, store, profile, daemon, adapters, decisions, HostFS, DNS/network, privilege, export/redaction, and cleanup.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Doctor observes package, profile, daemon, backend, network, HostFS, adapter, decision, export, and cleanup state. It fails closed by reporting error/refusal when facts are unavailable, prerequisites are missing, recovery is unsafe, or redaction fails; default doctor does not start guests or run hidden probes.
- **Typed Authority**: Diagnostics are Go-owned checks. Safe repair uses existing Manager `PlanDoctorFix`/`ApplyDoctorFix` (`internal/manager/manager.go:267`) and InitTask apply only. JavaScript/config may be inspected as data (adapter packs/redaction policy refs) but never executes repair authority.
- **Workspace And Policy**: Doctor does not alter workspace mounts, HostFS grants, proxy secrets, or profile authority in report mode. Safe repair cannot broaden HostFS, adapter, network, or profile authority; unsafe fixes remain advice/manual high-risk flows.
- **Generality And Provider Scope**: Check ids are generic Hideout product categories. Lima, git, package manifests, and helper binaries appear as dependency/backend facts, not generic semantics for unrelated backends.
- **Evidence And Redaction**: Doctor emits human and JSON reports, doctor audit/recovery evidence, and explicit export-selected doctor report artifacts. Reports use deterministic control-plane redaction and share the export boundary when selected.
- **Backend And Distribution**: Package/helper checks reuse 013 package manifests. Backend/deep checks are opt-in. First-run/repair continues through typed InitTasks, not hidden scripts.
- **Gates**: Gate 0 required: doctor report schema, human/JSON smoke, required-failure exit, warning/degraded exit, safe dry-run recovery, redaction scan, markdown/schema/doc checks. Real Gate 2/3 only if deep checks are promoted to release evidence in 016.
- **Status And Docs**: Update `docs/STATUS.md`, `docs/privacy-run-design.md`, `docs/privacy-run-test-plan.md`, and relevant manager/TUI docs for doctor report JSON, level/feature selection, safe recovery, and evidence export behavior.

## Project Structure

### Documentation (this feature)

```text
specs/015-doctor-diagnostics-recovery/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── doctor-command.md
│   ├── doctor-report.md
│   └── recovery-contract.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/doctor/
├── doc.go                 # report/check package
├── report.go              # finding/report data model and redaction
├── runner.go              # level/feature selection and check execution
├── checks.go              # local/light checks and deep-check hooks
├── render.go              # human and JSON rendering
└── recovery.go            # safe recovery planning facade

internal/app/app.go        # doctor CLI parsing/output delegates to doctor package
internal/manager/manager.go # existing PlanDoctorFix/ApplyDoctorFix reused
internal/export/           # optional doctor report source when explicitly selected
schemas/doctor-report.schema.json
scripts/test-doctor-smoke.sh
scripts/test-gate0.sh
docs/
```

**Structure Decision**: A new `internal/doctor` package is justified because the current doctor command is CLI-local (`internal/app/app.go:1929`) and mixes checks, formatting, exit classification, and repair invocation. 015 needs the same facts for CLI, JSON, smoke, export-selected evidence, and later UI surfaces; keeping the model in `internal/app` would duplicate authority and make Manager/TUI reuse harder.

## Phase 0 Research

See [research.md](research.md).

## Phase 1 Design

See [data-model.md](data-model.md), [contracts/doctor-command.md](contracts/doctor-command.md), [contracts/doctor-report.md](contracts/doctor-report.md), [contracts/recovery-contract.md](contracts/recovery-contract.md), and [quickstart.md](quickstart.md).

## Constitution Check Post-Design

- **Privacy Boundary**: PASS. Report mode is read-only and default checks are local/light. Deep checks are selected by level/feature. Recovery refuses unsafe fixes.
- **Typed Authority**: PASS. `internal/doctor` owns report/check modeling, while actual repair remains existing InitTask plan/apply. No script or UI authority is introduced.
- **Workspace And Policy**: PASS. Doctor does not modify HostFS/workspace/profile policy except typed safe repairs already owned by InitTask.
- **Generality And Provider Scope**: PASS. Contracts define generic check categories and mark backend-specific probes as feature/deep checks.
- **Evidence And Redaction**: PASS. Doctor report schema and export-selection contract require deterministic redaction before print/save/export.
- **Backend And Distribution**: PASS. Package/helper checks reuse installed manifest facts; real backend probes remain opt-in.
- **Gates**: PASS. Tasks will add schema validation, doctor smoke, Gate 0 integration, and focused unit tests.
- **Status And Docs**: PASS. Docs/status/test-plan updates are in scope.

## Complexity Tracking

- **New `internal/doctor` package**: Needed because CLI JSON, smoke,
  export-selected evidence, and future UI need the same report/check/recovery
  model. Keeping logic in `internal/app` would preserve the current CLI-only
  ad hoc shape and make stable JSON/evidence contracts fragile.
