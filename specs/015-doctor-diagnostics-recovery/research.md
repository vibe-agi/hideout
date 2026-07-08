# Research: Doctor Diagnostics And Recovery

<!-- markdownlint-disable MD013 -->

## Decision 1: Shared Doctor Report Model

**Decision**: Add a Go-owned `internal/doctor` package for check definitions, findings, reports, redaction, rendering, and recovery planning.

**Rationale**: Current doctor behavior is centered in `internal/app/app.go:1929` and writes human text directly through a local `report` closure. 015 needs the same facts in human output, JSON output, Gate 0 smoke, export-selected evidence, and future UI surfaces. A shared model keeps check ids/statuses stable and avoids parsing human text as an API.

**Alternatives considered**:

- Keep all doctor logic in `internal/app`: rejected because JSON/evidence would fork from CLI text.
- Add a Manager-only API first: rejected for v1 because the immediate product gap is CLI/package support; Manager can wrap the same package later.

## Decision 2: Light Default, Explicit Deep/Feature Probes

**Decision**: Default doctor runs local/light checks only. Deep or feature-specific probes require `--level deep` or `--feature <name>`.

**Rationale**: The roadmap and spec require no surprise guest starts, DNS probes, HostFS mutation, or hidden network calls. Existing `checkNetwork` can prepare dry-run network plans (`internal/app/app.go:2560`), but real DNS/connected-subnet proof belongs to explicit feature/deep selection and release gates.

**Alternatives considered**:

- Run all probes by default: rejected because it would make doctor slow, surprising, and potentially dependent on Lima/network availability.
- Keep separate scripts only: rejected because users need one product command with structured status.

## Decision 3: JSON Mirrors Human Findings

**Decision**: Human output and JSON output are two renderers over the same `DoctorReport`.

**Rationale**: SC-002 requires 100% correspondence between human and JSON findings. Separate code paths would repeat the 007 static-proof problem where one surface claims behavior not proven by the runtime path.

**Alternatives considered**:

- Generate JSON by scraping human text: rejected because it would be brittle and not schema-safe.
- Emit only JSON and format externally: rejected because external-alpha users need readable CLI output.

## Decision 4: Exit Classification Belongs To Report Summary

**Decision**: Each finding records whether it is required for the selected scope. Report summary computes exit classification: required errors fail, warning/degraded results do not fail unless selected as required.

**Rationale**: Spec FR-014 requires CI-friendly nonzero exits only for required failures. This must be deterministic from report fields, not hard-coded per CLI branch.

**Alternatives considered**:

- Fail on any warning: rejected because degraded-but-declared states are intentional and should not break default local checks.
- Always exit zero: rejected because CI/smoke needs a required-failure signal.

## Decision 5: Safe Recovery Reuses InitTask

**Decision**: Safe recovery planning delegates to existing Manager `PlanDoctorFix`/`ApplyDoctorFix` (`internal/manager/manager.go:267`) and only adds report linkage, dry-run/apply rendering, and unsafe-fix refusal.

**Rationale**: The constitution requires first-run and repair to use typed InitTask plans. Existing `doctorFix` already calls this path from `internal/app/app.go:2132`; 015 should structure it instead of adding a new repair authority.

**Alternatives considered**:

- Add doctor-specific repair functions: rejected because it would bypass typed InitTask ownership.
- Keep repair as human advice only: rejected because safe metadata repairs are useful and already exist.

## Decision 6: Package And Helper Checks Reuse Packagekit

**Decision**: Installed package and helper integrity checks use `packagekit.Verify` and installed manifest facts rather than independent checksum logic.

**Rationale**: `internal/packagekit/verify.go:22` already owns artifact/installed verification and checksum mismatch hints. Doctor should surface those facts and next actions, not recreate package validation.

**Alternatives considered**:

- Duplicate checksum scanning in doctor: rejected as drift-prone.
- Make package checks mandatory when no package manifest exists: rejected because source-tree development remains valid and should report package state as not-installed/optional.

## Decision 7: Subsystem Health Checks Read Existing Stores

**Decision**: Adapter, decision, HostFS overlay, daemon, environment, and export readiness checks read their existing stores and schemas.

**Rationale**: Current package surfaces already expose lists/state: adapter registry listing (`internal/adapterpack/registry.go:182`), HostFS overlay decisions (`internal/hostfs/overlay/store.go:428`), environment records (`internal/environment/environment.go:386`), daemon status structs, and export/redaction helpers. Doctor is an aggregator, not a new source of truth.

**Alternatives considered**:

- Persist a separate doctor cache: rejected because it would go stale and create a second evidence source.

## Decision 8: Redaction Before Render And Export

**Decision**: Doctor findings are redacted before human rendering, JSON rendering, audit, and export-selected evidence.

**Rationale**: The constitution requires deterministic redaction of Hideout control-plane material. Existing `audit.RedactDetails` (`internal/audit/audit.go:114`) is the baseline for map-like diagnostic details; doctor should centralize string/detail redaction so all renderers share the same protected fields.

**Alternatives considered**:

- Redact only export artifacts: rejected because CLI/JSON/audit could still leak.
- Heuristic user-data redaction: rejected because storage-time user/application data remains verbatim unless crossing the export/share boundary.

## Decision 9: Doctor Reports Are Explicit Export Inputs

**Decision**: Doctor reports are exportable only when the operator explicitly selects them.

**Rationale**: Spec FR-017 prevents doctor reports from silently expanding every export/share artifact. This aligns with 005 export boundaries: source evidence selection is explicit, control-plane redaction remains mandatory, and local full-fidelity audit is unchanged.

**Alternatives considered**:

- Automatically include latest doctor report in every export: rejected because it changes export scope unexpectedly.

## Decision 10: Gate 0 Owns Product Completion For 015

**Decision**: Gate 0 includes doctor schema validation, human/JSON smoke, required-failure exit, warning/degraded exit, redaction scan, and dry-run recovery. Real Gate 2/3 remains release-gate evidence for real HostFS/DNS claims.

**Rationale**: 015 is primarily local diagnostics/reporting/recovery. It must not claim real isolation proof from fast local checks, but it should make those checks visible and verifiable in Gate 0.

**Alternatives considered**:

- Require real Lima for every doctor change: rejected because default doctor must stay local/light.
