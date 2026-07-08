# Implementation Plan: Adapter Pack Lifecycle And Local Registry

<!-- markdownlint-disable MD013 -->

**Branch**: `011-adapter-pack-lifecycle` | **Date**: 2026-07-08 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/011-adapter-pack-lifecycle/spec.md`

## Summary

Promote 008 command adapters from profile-scoped script entries into a
store-wide local adapter pack lifecycle. Packs can be installed from local
directories or exact-commit git sources, locked by digest, tested
deterministically, and then explicitly enabled per profile through a binding
that pins an exact pack lock/revision. Runtime authority still flows through
the existing 008 adapter compiler and broker path; JavaScript remains
proposal-only and cannot apply HostFS writes, privilege operations, or any
other authority.

The implementation adds a Go-owned pack registry and Manager plan/apply surface
around existing `internal/cmdadapter` and `internal/manager/command_adapters.go`.
Existing profile-scoped local adapters and built-in root-sensitive behavior
remain compatible; 011 adds registry-backed bindings, pack lifecycle evidence,
mandatory pack tests, and export/status documentation.

## Technical Context

**Language/Version**: Go 1.25.8 for product code; constrained JavaScript
entrypoints continue to run through the existing Goja policy runtime.

**Primary Dependencies**: Existing standard-library JSON/crypto/filesystem
packages, `github.com/dop251/goja`, `github.com/santhosh-tekuri/jsonschema/v6`,
`internal/cmdadapter`, `internal/profile`, `internal/manager`, `internal/broker`,
`internal/policy`, `internal/audit`, and the existing export/redaction boundary.
Use the system `git` executable for exact-commit source intake; do not add a
Go git library in 011.

**Storage**: Hideout store files under an adapter-pack registry directory,
profile JSON bindings, audit JSONL, schemas under `schemas/`, and exported
evidence through the existing 005 export boundary.

**Testing**: `go test ./...`, targeted unit/contract tests for registry locking,
manifest validation, deterministic test harness, profile binding, Manager
plan/apply, runtime digest drift, redaction/export evidence, and Gate 0. A
focused local smoke script is sufficient for 011 claims.

**Target Platform**: Hideout CLI/daemon on macOS and Linux hosts. Native is
adequate for 011 tests because this feature changes local registry/profile
lifecycle, not guest isolation. Real Lima is not required unless implementation
changes backend setup or command shim behavior beyond existing 008 coverage.

**Project Type**: Go CLI/local Manager/daemon product with constrained
JavaScript extension points and profile-backed runtime policy.

**Performance Goals**: Listing installed packs should be local and sub-second
for a typical single-operator store. Enabling or runtime compiling an adapter
must remain deterministic and bounded by existing adapter script limits.
Registry digest checks should be proportional to pack size and occur at
install/test/enable/runtime verification points, not as unbounded background
work.

**Constraints**: Fail closed if source is unpinned, a digest mismatches, a pack
test fails, Core validation fails, a command binding conflicts, a pack is
disabled/revoked, built-in metadata is mutated, evidence cannot be recorded, or
the registry/profile write cannot be completed safely. Pack tests are mandatory
quality evidence but not the security boundary. Public marketplace, publisher
identity, signing, remote revocation, npm/node, recursive submodule trust, and
adapter-applied authority are out of scope.

**Scale/Scope**: Single-operator local registry. Store-wide installed packs,
profile-pinned enable bindings, local path and exact-commit git sources,
built-in metadata, install/test/enable/disable/upgrade/revoke lifecycle, and
exportable evidence. No multi-tenant delegation or organization policy.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches script adapter lifecycle, profile command
  routing, local git/path source intake, Manager lifecycle operations, audit,
  export evidence, and local UI/daemon inspection surfaces. It fails closed
  before profile authority changes or runtime adapter execution whenever source,
  digest, tests, Core validation, profile binding, registry state, or evidence
  is unverifiable.
- **Typed Authority**: Manager/Core owns pack install/test/enable/disable/
  upgrade/revoke plan/apply and profile mutation. JavaScript remains limited to
  008 adapter outcomes after Go validation. Pack tests and manifests are data,
  not authority.
- **Workspace And Policy**: No workspace mount, HostFS grant, passthrough
  mount, env policy, proxy secret, or network route is broadened. Profile state
  changes only through typed plan/apply. A store-wide registry entry grants no
  runtime authority until a profile binding pins a pack lock/revision.
- **Generality And Provider Scope**: This is a generic adapter pack lifecycle.
  Specific tools, package managers, editors, and agents may appear in fixtures
  only; they do not become Core semantics.
- **Evidence And Redaction**: Install, lock, test, enable, disable, upgrade,
  revoke, validation failure, digest mismatch, built-in metadata inspection, and
  runtime selection must be auditable and exportable after deterministic
  control-plane redaction.
- **Backend And Distribution**: No new helper binary. Uses existing CLI/Manager
  and optional system `git` as an operator-local prerequisite that doctor can
  report later. First-run repair is not changed.
- **Gates**: Gate 0 plus full Go tests and a focused adapter-pack smoke are
  required. Real Lima gates are not required for 011 unless implementation
  changes backend/guest behavior.
- **Status And Docs**: Update `docs/STATUS.md`,
  `docs/script-extension-architecture.md`, `docs/privacy-run-design.md`,
  `docs/privacy-run-test-plan.md`, `docs/manager-control-plane.md`,
  `docs/tui-webui-experience.md`, and `docs/README.md`.

**Initial Result**: PASS. No constitution violation is required.

## Project Structure

### Documentation (this feature)

```text
specs/011-adapter-pack-lifecycle/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── adapter-pack-manifest.md
│   ├── adapter-pack-registry.md
│   ├── adapter-pack-test.md
│   └── manager-adapter-pack-lifecycle.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/
├── adapterpack/
│   ├── doc.go
│   ├── manifest.go
│   ├── source.go
│   ├── registry.go
│   ├── lock.go
│   ├── test.go
│   └── evidence.go
├── cmdadapter/
│   ├── profile.go
│   ├── evaluator.go
│   ├── outcome.go
│   └── evidence.go
├── profile/
│   └── profile.go
├── manager/
│   ├── adapter_packs.go
│   ├── command_adapters.go
│   ├── api.go
│   └── server.go
├── app/
│   └── app.go
└── export/
    └── ...

schemas/
├── adapter-pack-manifest.schema.json
├── adapter-pack-registry.schema.json
├── command-adapter.schema.json
└── profile.schema.json

scripts/
├── test-gate0.sh
└── test-adapter-pack-smoke.sh

docs/
├── README.md
├── STATUS.md
├── manager-control-plane.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
├── script-extension-architecture.md
└── tui-webui-experience.md
```

**Structure Decision**: Add `internal/adapterpack` for pack manifests, source
intake, digest locks, registry state, deterministic pack tests, and evidence.
Extend the existing 008 profile/manager/runtime path rather than replacing it:
`internal/profile.Profile.CommandAdapters` gains pack lock/revision binding
fields; `internal/cmdadapter.Compile` resolves registry-backed bindings into
the same `RuntimeAdapter` shape that broker already executes; existing local
path and built-in adapters remain compatible. Manager lifecycle operations live
in a new `internal/manager/adapter_packs.go` file, while existing
`command_adapters.go` keeps profile binding plan/apply semantics and learns to
pin exact pack revisions.

## Phase 0: Research

See [research.md](research.md).

Resolved decisions:

- Registry is store-wide; profile binding is the authority edge.
- Profile binding pins exact pack lock/revision; upgrades create candidates and
  require explicit profile re-enable.
- Built-ins are Core-owned entries with pack-compatible metadata, not mutable
  registry artifacts.
- System `git` is used for exact-commit source intake in 011; no new Go git
  dependency.
- Core validation is the primary enable gate; pack-authored tests are mandatory
  but secondary.
- Registry state is local file-backed JSON with atomic write and schema
  validation, matching current profile/store patterns.

## Phase 1: Design

See [data-model.md](data-model.md), [quickstart.md](quickstart.md), and the
contracts under [contracts/](contracts/).

Design outputs:

- Adapter pack manifest contract for local and git-pinned sources.
- Store-wide registry/lock contract with candidate revision and lifecycle
  states.
- Pack test contract with deterministic fixtures and Core validation
  separation.
- Manager lifecycle contract for install, test, enable, disable, upgrade,
  revoke, list, inspect, and export evidence.

## Post-Design Constitution Check

- **Privacy Boundary**: PASS. The design keeps store-wide pack install
  authority-free until an explicit profile binding pins a verified revision.
  Missing or changed state denies before runtime routing.
- **Typed Authority**: PASS. Manager/Core owns lifecycle and profile mutation;
  JavaScript receives no new APIs and remains constrained to 008 outcomes.
- **Workspace And Policy**: PASS. No workspace, HostFS, env, network, or proxy
  authority is broadened. Profile bindings remain explicit and fail closed on
  conflicts.
- **Generality And Provider Scope**: PASS. Pack lifecycle is generic; built-in
  root-sensitive remains a Core-owned example with non-claim wording.
- **Evidence And Redaction**: PASS. Contracts define evidence and redaction for
  lifecycle events, test results, runtime selection, and export references.
- **Backend And Distribution**: PASS. No new helper binary or backend
  capability. System `git` is a local prerequisite for git-source installs only.
- **Gates**: PASS. Gate 0, full Go tests, adapter-pack smoke, schema checks, and
  docs markdownlint are sufficient for 011's claims.
- **Status And Docs**: PASS. Required docs are enumerated for tasks.

## Complexity Tracking

No constitution violations.
