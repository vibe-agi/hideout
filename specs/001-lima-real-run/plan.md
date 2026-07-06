<!-- markdownlint-disable MD013 -->

# Implementation Plan: Hideout Lima Real Run

**Branch**: `001-lima-real-run` | **Date**: 2026-07-05 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/001-lima-real-run/spec.md`

## Summary

Deliver the first independently verifiable dogfood slice: one generic target
CLI run in Lima against a sanitized workspace, completing a concrete reference
workload that modifies a workspace file, reaches a declared network endpoint
through the selected network policy, and leaves audit plus Boundary Summary
evidence. The approach is to add a focused Lima real-run validation fixture and
script around existing product paths rather than creating a new Core success
check API. The success check executes inside the guest/workspace context; host
test code only verifies artifacts and redacted evidence.

Out of scope for this plan: release-candidate evidence bundle changes, guided
first-run onboarding, full-screen TUI/WebUI observation, public ecosystem trust,
daemon mode, guest-to-host exposure, or product-specific real-agent adapters.

## Technical Context

**Language/Version**: Go 1.25.0 plus POSIX shell scripts for gates and smoke
entrypoints.

**Primary Dependencies**: Standard library CLI/runtime, Lima/`limactl`,
`github.com/hanwen/go-fuse/v2`, `github.com/dop251/goja`,
`github.com/santhosh-tekuri/jsonschema/v6`, `golang.org/x/sys`,
`golang.org/x/crypto`, `gopkg.in/yaml.v3`.

**Storage**: Local Hideout store under `HIDEOUT_STORE_ROOT` or default store;
profile files, session audit JSONL, reusable environment records, and temporary
test workspaces.

**Testing**: `go test ./...`, targeted package tests for app/manager/hostfs as
needed, `scripts/test-phase1.sh --quick`, Gate 2 Lima E2E, and the new
reference-run smoke entrypoint.

**Target Platform**: macOS operator host with Lima Linux guest for product
dogfood evidence. Native backend remains a weak wiring harness only.

**Project Type**: Local privacy-runner CLI with backend adapters, Manager Core,
browser/host capability shims, and shell-based release gates.

**Performance Goals**: Existing spec target: a prepared operator can complete
one reference workload run in 10 minutes or less. No new latency/SLO target for
normal command execution.

**Constraints**: Must not introduce arbitrary host execution. Must not hardcode
third-party product CLI names in Core. Must fail closed instead of falling back
to native/backend ambient host authority. Must preserve target stdout/stderr
shape while surfacing evidence separately. Success check must run inside the
guest/workspace context or be expressed as host-side artifact verification by
the test harness, not as a generic product host exec channel.

**Scale/Scope**: One supervised run slice, one sanitized workspace, one generic
target CLI, direct or privacy network mode, one known boundary-triggering test
set. Release bundles and generalized onboarding are later specs.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches workspace mount, HostFS, host.open, network,
  Lima backend, tool supply, audit/Boundary Summary, environment lifecycle, and
  command execution. Missing Lima/helper/target/network support fails before
  claiming dogfood success. Unsafe workspaces fail before backend prepare.
- **Typed Authority**: Uses existing `Core.PlanRun`/`Core.ApplyRun`, Lima
  backend provider, HostFS policy, host.open broker, endpoint exposure providers
  when the boundary fixture uses preview, and existing Go validators. No
  JavaScript/config gains authority in this slice.
- **Workspace And Policy**: Workspace is shared and writable by design.
  Workspaces covering home/store/credential/browser roots remain rejected unless
  explicit high-risk override is used. HostFS store roots remain reserved.
  Non-operator-authored grants are not enabled by this slice.
- **Evidence And Redaction**: Required evidence is run completion output,
  audit path, Boundary Summary, backend evidence label, network mode, reference
  workload result, and known boundary-decision set. Evidence must not leak
  proxy secrets, broker tokens, endpoint internals, browser automation secrets,
  callback/open URL secrets, or raw host file contents beyond declared
  artifacts.
- **Backend And Distribution**: Lima is the product evidence backend. Native is
  wiring-only. Existing helper discovery/build paths and tool supply are reused;
  this plan must not introduce arbitrary setup scripts as product authority.
- **Gates**: Minimum before merge: `go test ./...`, `scripts/test-phase1.sh
  --quick`, Gate 2 Lima E2E, and the new Lima real-run reference smoke. Gate 3
  is required when validating privacy network mode or changing tun2socks/proxy
  handling. Release-candidate bundle is explicitly out of scope.
- **Status And Docs**: Update `docs/STATUS.md` and `docs/privacy-run-test-plan.md`
  if this slice becomes an implemented product path or adds a new smoke command.
  Update threat/design docs only if claims or authority shape change.

**Pre-design result**: PASS. No constitution violation is required. The plan
must keep the success check out of host ambient execution and must fix SC-006
as a deterministic boundary fixture.

## Project Structure

### Documentation (this feature)

```text
specs/001-lima-real-run/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── lima-real-run.md
└── tasks.md
```

### Source Code (repository root)

```text
cmd/
├── hideout/
├── hideout-test-cli/
└── hideout-gate-lab-target/

internal/
├── app/
├── backend/
│   ├── lima/
│   └── native/
├── manager/
├── hostfs/
└── network/

scripts/
├── test-phase1.sh
├── test-gate2-lima.sh
├── test-gate3-hidden-proxy.sh
├── test-dogfood-cli-smoke.sh
└── test-lima-real-run.sh

docs/
├── STATUS.md
└── privacy-run-test-plan.md
```

**Structure Decision**: Keep the feature inside the existing CLI/gate layout.
The likely implementation adds a reference workload subcommand to
`cmd/hideout-test-cli`, a dedicated smoke script under `scripts/`, and targeted
tests/docs updates. No new application package, daemon, UI, or public API is
introduced.

## Phase 0: Research

See [research.md](research.md).

## Phase 1: Design

See [data-model.md](data-model.md), [contracts/lima-real-run.md](contracts/lima-real-run.md), and [quickstart.md](quickstart.md).

## Post-Design Constitution Check

- **Privacy Boundary**: PASS. The design uses Lima, rejects unsafe workspaces,
  keeps host-open/HostFS/network failures fail-closed, and does not allow
  fallback to native/host execution.
- **Typed Authority**: PASS. The success check is a guest/workspace workload
  concern, not a new host authority. Existing Manager/Core run paths remain the
  authority path.
- **Workspace And Policy**: PASS. The reference workload mutates only the
  selected workspace and tests store/credential-root denial through existing
  guards.
- **Evidence And Redaction**: PASS. The smoke contract defines exact non-secret
  evidence fields and a known boundary-decision set for SC-006.
- **Backend And Distribution**: PASS. Lima is required for dogfood evidence;
  native remains wiring-only. Helper/tool supply uses existing typed setup.
- **Gates**: PASS. Required validation is scoped to quick checks, Gate 2, and
  the new reference smoke; Gate 3 only when privacy network mode is selected or
  changed.
- **Status And Docs**: PASS. Docs/status updates are limited to the new
  implemented smoke/product-slice status when implementation lands.

## Complexity Tracking

No constitution violations or exceptional complexity are required.
