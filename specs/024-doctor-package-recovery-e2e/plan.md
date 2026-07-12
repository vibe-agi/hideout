# Implementation Plan: Doctor And Package Recovery E2E

<!-- markdownlint-disable MD013 -->

**Branch**: `024-doctor-package-recovery-e2e` | **Date**: 2026-07-09 |
**Spec**: [spec.md](spec.md)

## Summary

024 turns existing package repair and doctor recovery behavior into executable
product-hardening evidence. The implementation adds stable proof ids, a single
recovery E2E runner, local-fast evidence validation, and docs/Gate 0 wiring. It
does not add repair authority; it calls existing package and doctor paths and
records exactly what they prove.

## Technical Context

**Language/Version**: Go 1.24+ repository code, POSIX shell, jq, existing
package and doctor CLIs.

**Primary Dependencies**: Existing `internal/productevidence`,
`internal/packagekit`, `internal/doctor`, `internal/export`,
`scripts/test-package-smoke.sh`, `scripts/test-doctor-smoke.sh`, and
`scripts/test-gate0.sh`.

**Storage**: Temporary package extraction/install roots, temporary Hideout
stores, package/doctor logs, exported doctor report, and
`hideout.product-hardening-evidence/v1`.

**Testing**: `go test ./...`, targeted productevidence/packagekit/doctor/export
tests, `scripts/test-doctor-package-recovery-e2e.sh --local-fast`,
markdownlint, redaction scans, and Gate 0.

**Target Platform**: macOS/Linux local operator machines. Local-fast mode does
not require Lima and must not claim Gate 2/Gate 3 proof.

**Project Type**: Go CLI/local daemon project with shell E2E gates and JSON
evidence artifacts.

**Constraints**: No new package authority, doctor repair type, backend
provisioning, HostFS/network/script authority, or release-readiness claim.

## Constitution Check

- **Privacy Boundary**: PASS. The feature only proves existing local recovery
  paths and fails on redaction/schema/evidence mismatch.
- **Typed Authority**: PASS. Repairs remain Go-owned package repair and doctor
  InitTask fix apply; scripts orchestrate and collect evidence.
- **Workspace And Policy**: PASS. No workspace, profile policy, HostFS, or
  network authority changes.
- **Generality And Provider Scope**: PASS. Recovery fixtures are evidence
  scopes only.
- **Evidence And Redaction**: PASS. Product-hardening evidence and public
  artifact redaction scans are required.
- **Backend And Distribution**: PASS. Local recovery proof is not real backend
  proof or release readiness.
- **Gates**: PASS. Gate 0 gets local-fast recovery proof; real gates remain
  separate.
- **Status And Docs**: PASS. STATUS and test-plan updates are required.

## Project Structure

```text
specs/024-doctor-package-recovery-e2e/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── doctor-package-recovery-e2e.md
└── tasks.md

internal/productevidence/
├── claims.go
├── aggregate.go
└── doctor_package_recovery.go

scripts/
├── test-doctor-package-recovery-e2e.sh
├── test-package-smoke.sh
├── test-doctor-smoke.sh
└── test-gate0.sh
```

## Complexity Tracking

No constitution violations. One new script and one evidence helper wrap
existing behavior.

## Phase 0 Research Summary

See [research.md](research.md). Key decisions:

- Reuse existing package/doctor smokes as the authority path.
- Add product-hardening evidence rather than new ad hoc logs.
- Keep non-auto-fix doctor findings as guidance with gate-required markers.
- Treat local recovery proof as local usability evidence, not release readiness.

## Phase 1 Design Summary

Design artifacts:

- [data-model.md](data-model.md)
- [contracts/doctor-package-recovery-e2e.md](contracts/doctor-package-recovery-e2e.md)
- [quickstart.md](quickstart.md)

## Post-Design Constitution Check

All pre-design checks remain PASS. The contracts preserve existing authority
and require evidence/redaction validation before pass claims.
