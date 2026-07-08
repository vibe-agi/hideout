# Implementation Plan: Command Capability Adapters

<!-- markdownlint-disable MD013 -->

**Branch**: `008-command-capability-adapters` | **Date**: 2026-07-08 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/008-command-capability-adapters/spec.md`

## Summary

Upgrade the fixed command-proxy surface into a local, profile-scoped command
adapter runtime. Registered command symbols still enter through the existing
`hideout-shim` and broker path, but profile configuration can now bind selected
symbols to digest-pinned JavaScript adapters. Adapters can classify intent and
return a strict Go-validated outcome: deny, simulate, rewrite a non-privileged
guest command, or propose a non-applied Go Core capability.

008 intentionally does not claim a root boundary. The built-in root-sensitive
adapter captures and audits command-name intent before 009 privilege separation
exists; absolute paths, guest-root behavior, and syscall-level behavior remain
outside this feature.

## Technical Context

**Language/Version**: Go 1.25.8 for product code; constrained JavaScript
entrypoints executed by the existing Goja policy runtime.

**Primary Dependencies**: Existing `github.com/dop251/goja` policy execution,
standard-library JSON/crypto/filesystem packages, current `internal/cmdproxy`,
`internal/broker`, `internal/policy`, `internal/profile`, and
`internal/manager` packages. No new external runtime or helper binary.

**Storage**: Profile JSON in the existing profile store; local adapter script
artifacts under profile-owned or operator-specified local paths; audit JSONL
through existing audit writers; optional schema files under `schemas/`.

**Testing**: `go test ./...`, targeted unit/contract tests for adapter profile
validation, Goja ABI, broker routing, redaction, and Manager plan/apply; Gate 0
for schemas/docs/static smoke; a lightweight Lima command-name smoke only for
root-sensitive intent capture, not for isolation evidence.

**Target Platform**: Hideout CLI/daemon on macOS and Linux hosts, with Lima as
the primary guest backend. Native remains a weak development harness and is not
isolation evidence.

**Project Type**: Go CLI/local manager/daemon product with guest command shims
and constrained JavaScript extension points.

**Performance Goals**: Registered command adapter evaluation must stay within
the existing policy script limits. Unregistered commands must not add command
proxy overhead beyond current shim lookup behavior. Adapter digest verification
must be deterministic and local.

**Constraints**: Fail closed for any registered adapter command when the adapter
is missing, digest-mismatched, malformed, disabled, over limit, or returns an
invalid outcome. JavaScript cannot read files, open network connections, spawn
processes, access raw tokens, mutate profile state, or execute authority.
`host.open` defaults remain compatible. Adapter output cannot invent Core
actions or bypass Go validators.

**Scale/Scope**: Local, profile-scoped adapters only. No public marketplace,
publisher identity, signing, revocation, remote distribution, organization
policy, delegated approval, or role model in 008.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches command proxy, broker routing, script
  execution, profile state, audit evidence, Manager/TUI/WebUI visibility, and
  guest command execution. It fails closed for unsupported adapter state or
  invalid outcomes before target-side command execution.
- **Typed Authority**: JavaScript adapters only classify and propose. Go Core
  validates profile declarations, command ownership, adapter digest, ABI,
  allowed capabilities, rewrite safety, root-sensitive simulation restrictions,
  redaction, and final broker response. Manager plan/apply owns profile changes.
- **Workspace And Policy**: No workspace mount, HostFS, passthrough mount, env
  policy, proxy secret, or HostFS grant is broadened. Profile state expands with
  explicit adapter config. Deny outcomes win. Proposals are non-applied in 008.
- **Generality And Provider Scope**: The runtime is generic. The
  root-sensitive adapter is a built-in provider/example, not Core semantics for
  apt, sudo, mount, iptables, resolvectl, systemctl, or a specific distro.
- **Evidence And Redaction**: Adapter decisions are recorded in audit,
  Manager state, TUI/WebUI summaries, and export/share artifacts after
  deterministic control-plane redaction. Evidence distinguishes intent capture
  from enforced 009 privilege separation.
- **Backend And Distribution**: Reuses existing `hideout-shim`; no new product
  helper binary. Lima smoke may prove command-name routing, but 008 promotes no
  backend isolation claim.
- **Gates**: Gate 0 is required. Product completion requires unit/contract
  tests for profile schema, Goja ABI, broker fail-closed behavior, Manager
  plan/apply parity, redaction, and default `host.open` compatibility. Lima
  smoke is required only for the built-in root-sensitive command-name path.
- **Status And Docs**: Update `docs/STATUS.md`, `docs/privacy-run-design.md`,
  `docs/threat-model.md`, `docs/privacy-run-test-plan.md`,
  `docs/script-extension-architecture.md`, `docs/manager-control-plane.md`,
  `docs/tui-webui-experience.md`, and `docs/README.md`.

**Initial Result**: PASS. No constitution violation is required.

## Project Structure

### Documentation (this feature)

```text
specs/008-command-capability-adapters/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── adapter-abi.md
│   ├── adapter-profile.md
│   ├── manager-command-adapters.md
│   └── root-sensitive-adapter.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/
├── cmdadapter/
│   ├── doc.go
│   ├── profile.go
│   ├── outcome.go
│   ├── evaluator.go
│   ├── rootsensitive.go
│   └── evidence.go
├── cmdproxy/
│   └── cmdproxy.go
├── broker/
│   └── broker.go
├── policy/
│   └── policy.go
├── profile/
│   └── profile.go
├── manager/
│   ├── command_adapters.go
│   └── api.go
└── app/
    └── app.go

cmd/
└── hideout-shim/
    └── main.go

schemas/
├── command-adapter.schema.json
└── profile.schema.json

scripts/
├── test-gate0.sh
└── test-command-adapter-smoke.sh

docs/
├── README.md
├── STATUS.md
├── manager-control-plane.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
├── script-extension-architecture.md
├── threat-model.md
└── tui-webui-experience.md
```

**Structure Decision**: Add a small `internal/cmdadapter` package for adapter
profile validation, ABI/outcome validation, root-sensitive classification, and
redacted evidence shaping. Keep shim materialization in `internal/cmdproxy` and
`internal/manager/run_dataplane.go`; extend the registry so command ownership
can point to host-open or adapter routes. Reuse `internal/policy` for Goja
execution limits and strict JSON decoding. Profile mutations use Manager
plan/apply plus a CLI wrapper in `internal/app/app.go`; `cmd/hideout/main.go`
remains a thin wrapper.

## Phase 0: Research

See [research.md](research.md).

Resolved questions:

- Adapter runtime uses existing Goja constraints rather than Node, WASI, or an
  external process.
- Adapter result uses a new strict envelope, not the existing
  `policy.Proposal` directly.
- Duplicate command ownership is rejected at profile validation time.
- Built-in root-sensitive handling is routed through the same adapter outcome
  validator while keeping provider-specific intent out of Core action names.
- 008 never turns a proposal into apply; 009 and 010 own later authority.

## Phase 1: Design

See [data-model.md](data-model.md), [quickstart.md](quickstart.md), and the
contracts under [contracts/](contracts/).

Design outputs:

- Adapter profile contract with digest pinning, local artifact rules, command
  ownership, and allowed proposal capability lists.
- Adapter ABI contract defining context shape, outcome shape, strict validation,
  and redaction requirements.
- Manager plan/apply contract for enabling, disabling, refreshing digest pins,
  listing, and showing recent decisions.
- Root-sensitive adapter contract with intent taxonomy and explicit non-claims.

## Post-Design Constitution Check

- **Privacy Boundary**: PASS. Contracts require registered adapter failures to
  deny before target execution; unregistered commands remain unchanged.
- **Typed Authority**: PASS. JavaScript returns an envelope; Go validates and
  executes only deny/simulate/rewrite-response/proposal bookkeeping. No JS path
  executes privileged host, filesystem, network, backend, or setup authority.
- **Workspace And Policy**: PASS. No new HostFS/workspace/env/network grant is
  created. Profile changes use typed plan/apply and duplicate owner rejection.
- **Generality And Provider Scope**: PASS. Root-sensitive command handling is
  documented as a provider/example and intent layer before 009.
- **Evidence And Redaction**: PASS. Audit/export/UI fields are defined and
  control-plane redaction is required at emit and export boundaries.
- **Backend And Distribution**: PASS. Existing shim helper is reused; no new
  helper artifact.
- **Gates**: PASS. Gate 0, full Go tests, command-adapter smoke, and limited
  Lima intent smoke are sufficient for 008's claims.
- **Status And Docs**: PASS. Required docs are enumerated for tasks.

## Complexity Tracking

No constitution violations.
