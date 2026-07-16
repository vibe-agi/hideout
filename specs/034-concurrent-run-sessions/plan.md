# Implementation Plan: Concurrent Run Sessions

<!-- markdownlint-disable MD013 MD060 -->

**Branch**: `034-concurrent-run-sessions` | **Date**: 2026-07-16 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/034-concurrent-run-sessions/spec.md`

## Summary

Allow multiple `hideout run` processes to use one existing, workspace-pinned
reusable environment without sharing per-run authority. The existing static
workspace virtiofs mount remains unchanged. The environment runtime mount
becomes a transport root with environment-level `services/` and unique
`sessions/<session-id>/` children. Manager holds the environment transition
lock only while attaching, starting shared services, reconciling ownership, or
finishing. Each run holds a separate OS-backed owner lease for its lifetime.

For Lima, the existing root-control SSH identity starts each ordinary target
inside a mount and PID namespace, binds only that session child to
`/hideout/session`, mounts its HostFS view privately, then drops to the existing
non-root profile user. A second Core-owned channel on the same authenticated
SSH transport acts only as a connection-liveness guardian: the host sends a
fixed heartbeat and normal completion sends `done`; heartbeat timeout or
transport EOF validates the root-owned namespace parent PID, process start time,
and exact session source argument before killing that parent. It carries no
broker, HostFS, network, or daemon authority.
This is an A1/A2 process/control-view boundary, not a guest-root wall.
Environment-level privacy networking is shared only for an identical
secret-free configuration fingerprint and is cleaned only by the last proved
owner. Cross-workspace reuse, daemon adoption/automatic stop, and complete
dynamic terminal resize remain outside 034.

## Technical Context

**Language/Version**: Go 1.25; POSIX shell generated only by trusted Go backend code; JSON/JSON Schema for ownership and status contracts

**Primary Dependencies**: Existing Manager/backend/Lima/session/network/HostFS packages, `golang.org/x/sys/unix` flock, `golang.org/x/crypto/ssh`, Linux `unshare`, `mount`, `setpriv`, and private `/proc` from the supported guest runtime

**Storage**: Existing environment and session stores plus additive strict session-owner records and environment-service state under private environment directories; no database and no daemon-owned authority

**Testing**: Go unit, race, contract, Manager/CLI parity, cleanup-failure, terminal-stream, JSON Schema, docs truth, Gate 0 smoke, and real macOS arm64 Lima Gate 2 concurrency/isolation/performance evidence

**Target Platform**: Product claim requires macOS arm64 with Lima and the supported Linux guest runtime; native is a weak mechanics harness only

**Project Type**: Go CLI plus local Manager control plane, reusable VM backend, and brokered per-run data plane

**Performance Goals**: A second run joining an already-active environment reaches target execution in 2.0 seconds p95; Git/package metadata fixtures remain within 1.25x of the pre-feature static-workspace baseline

**Constraints**: Preserve the static workspace mount and environment identity; no dynamic cross-workspace mount, no daemon requirement, no automatic stop, no generic guest-root claim, no broad store mount, no hidden helper download, no ambient host fallback, and no per-session UID change that would break workspace ownership

**Scale/Scope**: One operator, one pinned workspace per environment, up to 16 concurrent sessions, one compatible environment network service, and bounded retained owner metadata

## Constitution Check

*GATE: Passed before research and re-checked after Phase 1 design.*

- **Privacy Boundary - PASS**: The feature changes lifecycle locking, runtime
  state projection, process visibility, HostFS mount scope, and shared network
  service lifetime. Unsupported namespace primitives, ambiguous owner state,
  incompatible service fingerprints, and cleanup errors fail before a target
  gains authority or before stop mutates the VM.
- **Typed Authority - PASS**: Existing Manager run and environment stop
  plan/apply operations remain the only product paths. Go owns owner leases,
  namespace command construction, shared-service validation, HostFS setup, and
  cleanup. No JavaScript or community artifact participates.
- **Workspace And Policy - PASS**: The existing workspace mapping remains the
  intentional shared read/write surface. Each namespace receives only its own
  `/hideout/session` and HostFS mount; existing deny, reserved-root, read,
  discover, decision, and staged-write policy remains authoritative.
- **Generality And Provider Scope - PASS**: Concurrent sessions are generic.
  Shells, Git, package tools, Claude, and Codex are fixtures, not command
  semantics. Lima owns the namespace provider; native makes no isolation claim.
- **Evidence And Redaction - PASS**: One owner registry feeds session status,
  environment summaries, stop refusal, audit, doctor, CLI, and Manager API.
  Public summaries expose IDs and states, never lock paths, tokens, proxy
  material, machine identity, or sibling runtime paths.
- **Backend And Distribution - PASS**: No new helper binary is required. Lima
  probes the fixed runtime primitives before activation and fails with typed
  recovery if absent. Runtime/package docs record the prerequisite; target code
  never receives root-control SSH.
- **Gates - PASS**: Gate 0 covers state machines, lock races, strict schemas,
  redaction, API parity, shared environment-network lifecycle/runtime-health,
  terminal-stream behavior, docs, and failure fixtures. Real Gate 2 covers
  namespaces, process/mount invisibility, HostFS isolation, forced
  interruption/teardown, sibling survival, and performance. The direct-network
  check in Gate 2 proves session non-interference; it does not stand in for a
  real shared `tun2socks` lifecycle claim.
- **Status And Docs - PASS**: Implementation updates `docs/STATUS.md`,
  `docs/architecture-principles.md`, `docs/privacy-run-design.md`,
  `docs/threat-model.md`, `docs/privacy-run-test-plan.md`,
  `docs/claim-boundaries.md`, support matrix, README concurrency wording, and
  command examples only after the relevant proof passes.

Post-design re-check: all eight items remain PASS. The design removes the
single-writer implementation without weakening the static workspace boundary,
uses existing Go-owned setup authority, and keeps automatic lifecycle
ownership and cross-workspace transport out of scope. No constitution waiver
is required.

## Project Structure

### Documentation (this feature)

```text
specs/034-concurrent-run-sessions/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── environment-service.md
│   ├── guest-session-view.md
│   └── session-ownership.md
├── checklists/
│   └── requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/
├── session/
│   ├── ownership.go             # OS-backed lease and strict summary model
│   └── session.go               # existing durable/ephemeral session layout
├── environment/
│   └── environment.go           # transition lock and runtime child layout
├── manager/
│   ├── run_apply.go             # attach/run/finish lock choreography
│   ├── run_environment.go       # owner-aware lifecycle state
│   ├── run_session.go           # unique environment runtime child per run
│   ├── run_network.go           # shared-service fingerprint/materialization
│   ├── environment_lifecycle.go # active-owner stop refusal
│   ├── manager.go               # authoritative summaries
│   └── api.go                   # existing run/status and overview surfaces
├── backend/
│   ├── backend.go               # activation/session-view contract
│   └── lima/
│       ├── lima.go              # runtime activation and existing run path
│       ├── session_view.go      # root-SSH namespace runner
│       ├── ssh_bridge.go        # bounded direct SSH stream/PTY reuse
│       └── setup.go             # existing separate setup identity
├── network/
│   └── network.go               # secret-free service fingerprint
├── recovery/
│   └── registry.go              # stable owner/isolation/service errors
└── doctor/
    └── doctor.go                # runtime primitive and stale-owner checks

schemas/
├── active-session-summary.schema.json
└── environment-service-state.schema.json

scripts/
├── test-concurrent-sessions-smoke.sh
├── test-concurrent-sessions-e2e.sh
├── test-gate0.sh
└── lib/gate2-concurrent-sessions.sh

docs/
├── architecture-principles.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
├── threat-model.md
├── claim-boundaries.md
├── support-matrix.md
└── STATUS.md
```

**Structure Decision**: Extend the existing session and environment ownership
boundaries rather than create a parallel scheduler or daemon. Add one Lima
backend file for the namespace runner because it is a backend primitive, while
Manager retains lifecycle and service-policy authority. No new executable or
third-party runtime dependency is introduced.

## Complexity Tracking

No constitution violations require justification. The additional session
ownership record is necessary because the old environment-wide lock cannot
both prove each live owner and permit concurrency. The Lima namespace runner
is a backend implementation of the existing per-run isolation contract, not a
new product authority.
