# Implementation Plan: Trusted Host-IDE Workspace Grant

<!-- markdownlint-disable MD013 -->

**Branch**: `039-trusted-ide-grant` | **Date**: 2026-07-20 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/039-trusted-ide-grant/spec.md`

## Summary

Replace the per-live-run trusted-IDE decision (which deadlocks one-shot `code .`)
with a durable trusted-IDE grant scoped to profile + workspace + app binding, the
same policy shape as a HostFS profile grant. The open-time check
(`runProjectionGrantChecker.TrustedGrantActive`) consults the persistent grant
before any per-run decision; trusted mode with no grant fails closed and names
the grant command. Path B was proven end-to-end on real Lima by a throwaway
spike (2026-07-20). Safe mode is unchanged and remains the default.

## Technical Context

**Language/Version**: Go 1.25 (existing Hideout module).

**Primary Dependencies**: existing internal packages —
`internal/manager` (run data plane, projection grant checker, profile IDE mode),
`internal/hostcap` (grant scope, open-resource binding),
`internal/workspaceattach` (workspace identity derivation),
`internal/operatorintent` (natural `allow`/`deny` grammar),
`internal/audit`, `internal/profile`.

**Storage**: one new per-profile JSON policy file under the reserved,
guest-unreachable store (`profiles/<p>/`), beside the existing `ide-mode.json`.
Atomic write, `0600`, strict schema. No new store subsystem.

**Testing**: `go test` (unit/contract in `internal/manager`), the existing
projection/host-app test suites, and a real-Lima end-to-end lane extending the
projection/`code .` walkthrough (grant → reuse → refuse → revoke).

**Target Platform**: macOS arm64 + Lima guest (the projection path's platform).

**Project Type**: single Go project (CLI + daemon-hosted Manager).

**Performance Goals**: N/A — the grant check is a single small-file read on an
already-slow VM-boot path; no throughput concern.

**Constraints**: the grant record and its audit must carry only Core-derived
identifiers (no host path, username, token, machine id, or raw argv); the guest
must not be able to create/refresh/read a grant; fail closed on any mismatch.

**Scale/Scope**: single-operator MVP; one built-in VS Code binding consumer; a
handful of workspaces per profile.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches host-app projection authority
  (`host.app.open-resource`) and profile policy. Fails closed: trusted mode with
  no matching grant, a malformed grant, or a workspace/app-identity mismatch all
  refuse the open with no host launch. Safe mode (default) is unaffected. No new
  host capability; no HostFS data-plane, network, or backend authority changes.
- **Typed Authority**: The grant is durable profile policy written by a Manager
  plan/apply-style operation under the profile mutation lock, read by Go at open
  time in `runProjectionGrantChecker`. No JavaScript/config participates; the
  guest supplies nothing — the workspace identity and app binding are Core-
  derived. This matches the existing HostFS profile-grant authority shape.
- **Workspace And Policy**: Adds a per-profile trusted-IDE grant keyed by the
  Core-derived workspace identity + app binding. It does not change workspace
  mounts, HostFS rules, passthrough mounts, env policy, or proxy secrets.
  Revocation: switching the profile to safe mode drops all trusted-IDE grants
  (extends existing safe-mode invalidation); an explicit revoke drops one.
  Drift in workspace identity or binding digest fails the match (re-grant).
- **Generality And Provider Scope**: The grant model is generic to host-app
  projection bindings (keyed by binding, not by "VS Code"). The built-in VS Code
  binding is the first and only consumer in this slice; no editor-specific
  semantics enter Core. `code .`/`trusted-host-ide`/`ide-mode` are existing
  product names, not new provider coupling.
- **Evidence And Redaction**: New audit events for grant / reuse / refuse /
  revoke, carrying only Core-derived identifiers. Grant existence surfaces
  through the profile `ide-mode` inspection output. The broker already discloses
  safe-vs-trusted posture on launch (038/2ccdd40). No host path/username/token/
  machine-id/argv in the grant record, audit, or guest-visible response.
- **Backend And Distribution**: Native harness proves unit/contract behavior;
  real Lima proves the end-to-end `code .` grant/reuse/refuse/revoke loop. No new
  helper artifact. No first-run/InitTask change.
- **Gates**: Gate 0 (Go tests, schema, docs-truth, markdownlint) for the
  contract and policy behavior; a real-Lima projection lane for the end-to-end
  claim (extends the existing host-capability projection evidence, not a new
  gate family). Mutation proofs + negative fixtures per constitution 1.3.0.
- **Status And Docs**: Update `docs/STATUS.md` (host-capability projection row),
  `docs/host-capability-projection.md` (trusted grant lifecycle),
  `docs/first-run-alpha.md` (replace the "trusted not usable for one-shot"
  limitation with the grant flow), `docs/claim-boundaries.md` (grant claim +
  proof id), and `docs/DEBT.md` (close the trusted-ide one-shot and two-checker
  entries). `docs/threat-model.md` reviewed: no new non-claim (this tightens an
  existing high-authority path, does not add one).

No constitution violations — Complexity Tracking not required.

## Project Structure

### Documentation (this feature)

```text
specs/039-trusted-ide-grant/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── trusted-ide-grant-record.md
│   └── operator-grant-commands.md
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```text
internal/manager/
├── hostcap_projection.go        # ide-mode read/write; trusted grant store
│                                #   read/write/revoke; delete or document the
│                                #   test-only decisionIdeGrantChecker (FR-011)
├── run_dataplane.go             # runProjectionGrantChecker.TrustedGrantActive
│                                #   consults the persistent grant first (FR-002/003)
├── hostcap_projection_test.go   # grant match/miss/drift/guest-cannot-forge
└── run_dataplane_host_app_test.go # open-time check unit coverage

internal/operatorintent/
├── intent.go                    # `allow ide-trust` / `deny ide-trust` grammar
└── intent_test.go               # grammar parse tests

internal/app/
├── operator_access.go           # wire ide-trust intent to Manager grant/revoke
├── operator_access_test.go      # command behavior + audit
└── app.go                       # usage line

schemas/
└── trusted-ide-grant.schema.json  # grant record schema (if a schema file fits
                                    #   the existing schema-gate pattern)

scripts/                          # real-Lima projection lane extension
docs/                             # STATUS, host-capability-projection,
                                  #   first-run-alpha, claim-boundaries, DEBT
```

**Structure Decision**: Single Go project. The change is concentrated in
`internal/manager` (grant storage + the one open-time check), with a thin
operator command surface in `internal/operatorintent` + `internal/app`. It
reuses the existing profile-store, mutation-lock, workspace-identity, and
projection-binding machinery rather than adding a subsystem.

## Complexity Tracking

No constitution violations; no entries required.
