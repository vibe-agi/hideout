# Implementation Plan: Workspace Executable Support

<!-- markdownlint-disable MD013 -->

**Branch**: `041-workspace-executable-support` | **Date**: 2026-07-22 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/041-workspace-executable-support/spec.md`

## Summary

Promote direct execution of guest-compatible scripts, binaries, and
project-local launchers from the exact selected workspace on the existing
macOS arm64 Lima shared Workspace Portal. The Linux FUSE client will accept and
strip `FMODE_EXEC` as a non-semantic local hint while preserving the Portal's
closed wire flags and exact-root authority. Focused and feature-specific real gates,
a Go-owned artifact validator, mutation proof, negative evidence fixtures, and
explicit static/dedicated virtiofs non-claims establish the boundary.

## Technical Context

**Language/Version**: Go 1.25.0; POSIX shell and Python 3 for real-gate orchestration

**Primary Dependencies**: go-fuse v2.10.1, existing `internal/workspaceattach`, product-evidence registry/evaluator, Lima `vz`, Workspace Portal helper

**Storage**: no new product state; retained JSON/log gate artifacts only

**Testing**: Go unit/cross-build tests, mutation and negative fixtures, focused Portal Lima probe, full Gate 0, feature-specific shared-Portal Gate 2, and legacy aggregate Lima regressions

**Target Platform**: macOS arm64 host, Linux arm64 Lima guest, compatible automatic/shared sessions using Workspace Portal

**Project Type**: Go CLI/daemon/Manager monorepo with guest FUSE helper

**Performance Goals**: 30/30 launcher samples pass; warm first-output p95 remains at most 2 seconds and median regression remains at most 10%

**Constraints**: no protocol authority expansion, hidden copy, host fallback, chmod, mount/cache-mode change, or dedicated/static promotion; unknown flags still fail closed; evidence is redacted and exact-commit bound

**Scale/Scope**: one flag-encoding correction, existing research-probe repair, two real-gate execution lanes, one evidence validator, 30 performance samples, and 100 repeated/disjoint-workspace executions

## Constitution Check

*GATE: Passed before Phase 0 research and re-checked after Phase 1 design.*

- **Privacy Boundary**: The change touches only Workspace Portal open-flag
  validation and evidence. The exact root, attachment credential, session,
  environment, provider, incarnation, path, symlink, admission, and lifecycle
  checks remain authoritative. Unknown flags and stale/mismatched attachments
  still fail before host open; no host or copy fallback exists.
- **Typed Authority**: Existing Manager run planning/application and the
  Go-owned shared workspace provider establish the attachment. The Go Portal
  client validates local flags; the host Portal server independently validates
  the unchanged wire flags and root. No JavaScript or config participates.
- **Workspace And Policy**: No mount, HostFS, passthrough, environment, proxy,
  profile, deny, reserved-root, or high-risk-override semantics change. Execute
  permission is not added. Workspace writes remain direct host-checkout writes.
- **Generality And Provider Scope**: The kernel-hint rule is generic to Linux
  FUSE Portal execution. The product claim is deliberately limited to the
  promoted shared macOS arm64 Lima provider. Package-manager launchers are test
  fixtures, not Core policy.
- **Evidence And Redaction**: No new runtime audit authority is introduced.
  Gate evidence names only candidate/platform/mechanism, closed checks,
  aggregate timing, sample count, and non-claims. Paths, credentials, arguments,
  outputs, content, and secrets are forbidden.
- **Backend And Distribution**: The packaged Workspace Portal helper is
  required. Native is a weak mechanics harness; static/dedicated virtiofs is
  unpromoted. No first-run, repair, package, schema, or InitTask change occurs.
- **Gates**: Required checks are targeted unit/cross-build tests, mutation and
  negative evidence fixtures, full Gate 0, the focused Portal Lima correctness
  probe, a clean exact-package 041 Gate 2, and the aggregate Lima regression
  gate with its static-virtiofs non-claim intact.
- **Status And Docs**: Update `docs/STATUS.md`, `docs/DEBT.md`,
  `docs/claim-boundaries.md`, `docs/privacy-run-design.md`,
  `docs/privacy-run-test-plan.md`, and `docs/threat-model.md`.

### Post-Design Re-check

The design passes without exception. `FMODE_EXEC` remains kernel-local metadata
and creates no wire or host authority. A dedicated/static generalization was
rejected and recorded as a non-claim, so environment trust-domain and lifecycle
semantics are unchanged. The new validator is Go-owned, fails unknown evidence
fields closed, and has a negative fixture. No Complexity Tracking entry is
required.

## Project Structure

### Documentation (this feature)

```text
specs/041-workspace-executable-support/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── adversarial-report.md
├── checklists/
│   └── requirements.md
├── contracts/
│   ├── portal-executable-open.md
│   └── evidence.md
└── tasks.md
```

### Source Code (repository root)

```text
cmd/hideout-workspace-probe/
└── portal.go                         # focused real-probe server wiring

internal/
├── workspaceattach/
│   ├── portal_openflags.go           # shared closed flag encoder
│   ├── portal_openflags_linux.go     # Linux FUSE hint allowlist
│   └── portal_openflags*_test.go     # host and Linux contract tests
└── productevidence/
    ├── workspace_executable.go       # 041 proof IDs and artifact validator
    ├── workspace_executable_test.go  # registry and false-green fixtures
    ├── registry.go                   # proof/validator registration
    └── aggregate.go                  # required proof list

scripts/
├── test-workspace-portal-lima.sh          # focused transport correctness
├── test-workspace-executable-lima-e2e.sh  # exact product proof
├── test-gate2-lima.sh                     # aggregate static-topology regressions
└── test-gate0.sh                          # local aggregate

docs/
├── STATUS.md
├── DEBT.md
├── claim-boundaries.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
└── threat-model.md
```

**Structure Decision**: Extend the existing Workspace Portal flag encoder,
focused/feature-specific real gates, aggregate regressions, and the Go
product-evidence registry. No new runtime
service, configuration surface, durable model, protocol bit, or UI is added.

## Implementation Phases

### Phase 0: Diagnose And Bound

1. Reproduce direct script and binary execution on real macOS arm64 Lima.
2. Retain the FUSE open trace and identify the rejected `FMODE_EXEC` bit.
3. Decide the promoted shared-Portal scope and static/dedicated non-claim.

### Phase 1: Contract And Local Mechanics

1. Add an OS-neutral encoder seam and tests for accepted-hint/no-wire-change and
   unknown-flag rejection.
2. Add `FMODE_EXEC` only to the Linux local allowlist and bind it to go-fuse's
   exported constant.
3. Repair the focused probe's current admission identity and add direct script
   and binary operations.

### Phase 2: Product And Evidence

1. Keep legacy static-virtiofs Gate 2 copies explicit and unable to satisfy the
   shared-Portal claim.
2. Add a feature-specific exact-candidate real gate with launcher, checkout,
   isolation, negative, 30-sample, and no-fallback checks.
3. Register a strict Go validator and retain false-green negative fixtures.

### Phase 3: Closure

1. Record mutation proof, run targeted/race/full Gate 0, focused Portal, 041
   real Gate 2, and integrated Lima Gate 2.
2. Update support, design, threat, gate, claim, and debt documentation.
3. Converge every FR/SC/task and retain clean exact-commit evidence.

## Complexity Tracking

No constitution violations or justified exceptions.
