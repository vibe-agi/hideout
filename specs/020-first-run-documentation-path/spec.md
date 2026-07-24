# Feature Specification: First-Run Documentation Path

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `020-first-run-documentation-path`
**Created**: 2026-07-09
**Status**: Implemented as documentation plus local package E2E coverage — the exact claim surface and non-claims live in [docs/STATUS.md](../../docs/STATUS.md)
**Input**: `.tmp/017-020-internal-hardening-plan.md`

## Clarifications

### Session 2026-07-09

- Q: What is the primary first-run posture? A: Privacy on Lima is the primary path; native appears only as a clearly labeled fast development harness.
- Q: Does 020 add new behavior? A: No. 020 is documentation, smoke checks, and error-path copy alignment over existing package/init/doctor/run/console behavior.
- Q: Does completing the walkthrough imply release readiness? A: No. Release readiness still requires explicit readiness evidence and real Gate 2/Gate 3 proof for the release artifact.

## User Scenarios & Testing

### User Story 1 - Follow One First-Run Path (Priority: P1)

An external alpha operator can follow one page from package extraction to a first
successful Lima privacy run without reading maintainer-only docs.

**Independent Test**: A docs smoke script validates the canonical commands,
privacy/Lima defaults, doctor recovery commands, and absence of stale `go run`
examples.

### User Story 2 - Recover From Common Setup Failures (Priority: P2)

When install, helper, proxy, Lima, or package verification fails, the operator
sees the next command and which diagnostics to run.

**Independent Test**: Documentation smoke checks for package repair, doctor
deep/feature commands, missing `tun2socks` wording, and gate-required language.

### User Story 3 - Avoid Security Overclaims (Priority: P3)

The first-run docs state native is a weak harness, workspace writes are
operator-visible user risk, and local doctor is not release/Gate 2/Gate 3 proof.

**Independent Test**: Smoke scans fail on native-as-isolation wording, stale
`go run` user paths, or release readiness claims from the walkthrough alone.

## Requirements

### Functional Requirements

- **FR-001**: The repo MUST provide one canonical external-alpha first-run page.
- **FR-002**: The canonical path MUST use packaged `hideout` commands, not `go run`.
- **FR-003**: The primary path MUST default to `privacy` on `lima` with
  `tun2socks`, a proxy secret, and a mediated resolver.
- **FR-004**: Native backend wording MUST be limited to weak development harness.
- **FR-005**: Common failure guidance MUST include package verify/repair, doctor
  deep, feature-scoped doctor, missing `tun2socks`, missing proxy/resolver, and
  stale package install.
- **FR-006**: HostFS write onboarding MUST point to overlay grants and explicit
  decision visibility, not silent host mutation.
- **FR-007**: TUI/WebUI operator console guidance MUST say it organizes existing
  state/actions and does not add authority or auto-run doctor.
- **FR-008**: Docs MUST preserve release/gate non-claims: first-run success and
  local doctor output are not release readiness or real Gate 2/Gate 3 evidence.

### Success Criteria

- **SC-001**: `scripts/test-first-run-docs-smoke.sh` passes in Gate 0.
- **SC-002**: README links to the canonical first-run page.
- **SC-003**: Docs index and STATUS point at the canonical first-run page.
- **SC-004**: Markdown lint passes for README, docs, and this spec.

## Assumptions

- 020 intentionally does not change package, init, doctor, run, daemon, or UI
  behavior.
- `privacy` on Lima remains the external-alpha primary path.
- Hardened remains an advanced path until enforced-capable image posture is
  smoother.
