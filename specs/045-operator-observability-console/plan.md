# Implementation Plan: Operator Observability Console

**Branch**: `045-operator-observability-console` | **Date**: 2026-07-28 |
**Spec**: [spec.md](./spec.md)

**Input**: Feature specification from
`/specs/045-operator-observability-console/spec.md`

## Summary

Turn Hideout's current command dispatcher and text snapshot into one coherent,
approachable operator product without weakening the existing Manager authority
boundary. The implementation adds a declarative help catalog, a Bubble Tea HUD,
shared typed Manager projections and mutation transactions, daemon-owned
Keychain secrets, online connection reconfiguration, and a host-private
workload activity plane. A per-session Linux cgroup defines the observed
workload; a packaged guest observer attributes process, file, DNS, and network
activity and reports explicit coverage and loss. CLI, TUI, and WebUI consume the
same snapshots, event stream, plans, and terminal operation records.

Configuration and secret changes use
`Draft -> Plan -> Review -> Confirm -> Apply`, bind to a profile revision, and
are idempotent by operation ID. Existing sessions keep immutable snapshots.
Network changes stage, validate, activate, and either prove success or roll
back without restarting the daemon. New TLA+ modules and Go refinement tests
cover stale clients, retries, crashes, event loss, cleanup, and progress.

## Technical Context

**Language/Version**: Go 1.25.12; TLA+; POSIX shell for gates; existing
HTML/CSS/JavaScript WebUI

**Primary Dependencies**: existing Manager/daemon/Lima core;
`charm.land/bubbletea/v2` v2.0.8, `charm.land/bubbles/v2` v2.1.1,
`charm.land/lipgloss/v2` v2.0.5; `github.com/cilium/ebpf` v0.22.0;
macOS Security.framework Keychain Services

**Storage**: existing profile/environment JSON and append-only audit; new
0600 host-private, checksummed, rotated NDJSON activity segments and compact
indexes owned by one exact environment incarnation or disposable session;
bounded operation-result ledger

**Testing**: `go test`, `go test -race`, package/static/advisory gates, TLA+/TLC,
PTY and golden rendering tests, browser tests, mutation proofs, and real Lima
end-to-end/performance/recovery tests

**Target Platform**: macOS arm64 host; supported Debian 13 Linux guest on Lima;
native/unsupported backends expose explicit reduced coverage

**Project Type**: local CLI plus daemon, terminal UI, browser UI, and packaged
Linux guest helpers

**Performance Goals**: 95% of healthy-stream changes visible within two
seconds; no more than 10% median reference-workload elapsed-time overhead;
interactive input and attach budgets remain at or below existing limits;
bounded activity storage never exceeds one active segment beyond quota

**Constraints**: local single operator; offline-capable after runtime assets are
installed; no secret value in API, audit, process arguments, activity storage,
or UI; no file contents, environment values, terminal input/full output, or
packet payloads; stale clients are read-only; unsupported authority fails
closed; remote release requires separate authorization

**Scale/Scope**: one host operator, bounded concurrent sessions on reusable or
disposable VMs, tens of thousands of raw kernel observations per second
collapsed into human-scale activity records, 71 functional requirements, and
three operator surfaces sharing one domain contract

## Constitution Check

*GATE: Passed before Phase 0 research and re-checked after Phase 1 design.*

- **Privacy Boundary**: The feature touches profile configuration, proxy/DNS
  state, macOS Keychain items, guest cgroups and kernel observation, the
  host-private Manager store, local APIs, lifecycle cleanup, and local UI
  rendering. Capability probes produce `Available`, `Partial`, or `Unavailable`
  with reason and interval. Unknown revisions, ambiguous ownership, unsupported
  hooks, stale streams, failed redaction, and unproved cleanup never become a
  success or full-coverage claim.
- **Typed Authority**: Go Manager services own profile plans, operation
  application, secret metadata, activity queries, and lifecycle cleanup.
  Provider interfaces own Keychain, network transition, cgroup, observer, and
  storage effects. TUI and browser code submit typed drafts and operation IDs;
  they cannot write profile files, secret stores, VM state, or activity
  segments directly. Go revalidates every request and canonicalizes every plan.
- **Workspace And Policy**: Existing workspace attachment, HostFS, env policy,
  deny, reserved-root, and high-risk grant behavior is unchanged. File
  observation is evidence-only and inherits the immutable run workspace
  authority. Network plans reuse current policy and snapshot semantics.
  Secret references are non-secret profile values; secret material is resolved
  only at the daemon/provider boundary.
- **Generality And Provider Scope**: Bubble Tea is a terminal presentation
  provider; macOS Keychain is a host secret provider; cgroup/eBPF/fanotify are
  Linux observation providers; Lima/Debian is the supported reference backend.
  Claude, local SOCKS proxies, shells, package managers, and test servers are
  fixtures, not hard-coded product identities or defaults.
- **Evidence And Redaction**: Plans, transitions, terminal operations, coverage
  intervals, drop counters, cleanup proofs, and risk rule IDs flow through the
  Manager snapshot/event schema. Deterministic redaction removes known secret
  values and auth-shaped data before persistence. Boundary Summary,
  `explain`, `doctor`, TUI, WebUI, support exports, and audit expose only
  non-secret refs, generations, reasons, effects, and evidence.
- **Backend And Distribution**: Full attribution requires a supported Linux
  cgroup v2 guest and packaged observer helper. Native is a weak harness only.
  Observer and supervisor binaries are Go-owned, digest-manifested helper
  artifacts built into runtime images. Installation and repair stay in typed
  InitTasks; no downloadable runtime compiler or privileged script is added.
- **Gates**: Gate 0 plus lifecycle, network, browser, terminal, privacy,
  packaging, real-backend, mutation, and performance gates are required.
  Assertions about exact attribution, cleanup, online proxy rotation, and
  availability require both positive reference workloads and negative/drop
  fixtures. Race, recovery, and adversarial concurrent-client suites are
  mandatory.
- **Status And Docs**: Update `docs/STATUS.md`,
  `docs/privacy-run-design.md`, `docs/threat-model.md`,
  `docs/privacy-run-test-plan.md`, `docs/formal-models.md`, CLI/TUI/Web help,
  privacy/retention/export documentation, connection recovery, package
  lifecycle, and release evidence.

### Post-design re-check

The Phase 1 contracts preserve one authority route: surfaces never receive
secret values and never perform mutation directly. The workload observer is
outside the target cgroup and produces evidence only. Its inability to observe
does not silently grant authority or claim coverage. Storage ownership uses
the exact incarnation identity already required by environment lifecycle
proofs. No constitution exception is required.

## Project Structure

### Documentation (this feature)

```text
specs/045-operator-observability-console/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── manager-api.md
│   ├── activity-stream.md
│   └── tui-interaction.md
└── tasks.md
```

### Source Code (repository root)

```text
cmd/
├── hideout/
├── hideout-observer/
├── hideout-root-control/
└── hideout-session-supervisor/

internal/
├── app/
│   ├── command_catalog.go
│   ├── help.go
│   └── tui.go
├── daemon/
│   ├── activity_service.go
│   ├── events.go
│   └── sessions.go
├── helperbin/
├── liveconsole/
├── manager/
│   ├── activity_service.go
│   ├── operation_service.go
│   ├── profile_transaction.go
│   ├── secret_service.go
│   └── routes.go
├── secrets/
│   ├── store.go
│   ├── keychain_darwin.go
│   └── unsupported.go
├── sessionwire/
├── tui/
│   ├── model.go
│   ├── components/
│   ├── modal/
│   └── render/
└── workloadobs/
    ├── aggregate/
    ├── collector/
    ├── coverage/
    ├── query/
    ├── redact/
    ├── risk/
    ├── store/
    └── types/

web/

formal/
├── OperatorConfiguration.tla
├── SecretTransition.tla
├── WorkloadObservation.tla
└── cfg/

scripts/
├── gates/
├── lima-real-smoke.sh
└── release/

docs/
```

Tests remain beside Go packages, with cross-package product fixtures under
existing `internal/*_test.go`, `scripts/gates/`, browser, PTY, and Lima test
locations. Generated eBPF object files live with their source under
`internal/workloadobs/collector/bpf/` and are embedded in the packaged observer.

**Structure Decision**: Extend the existing Go monorepo and Manager/daemon
architecture. New authority is separated into typed services and provider
packages. The large command dispatcher is not replaced wholesale in this
feature; a declarative catalog generates discoverability while existing
parsers are migrated incrementally. The TUI becomes its own package and
consumes `liveconsole` projections rather than embedding domain logic.

## Delivery Phases

1. **Contracts and formal baseline**: land domain types, API schemas,
   redaction fixtures, TLA+ modules, failing refinement tests, and declarative
   command catalog.
2. **Transaction and secret foundation**: implement profile revision/CAS,
   canonical plans, idempotent operations, Keychain storage, online network
   stage/activate/rollback, and CLI parity.
3. **Workload boundary and evidence plane**: create per-session cgroups,
   observer handshake, process/network/DNS/file providers, aggregation,
   coverage intervals, bounded storage, risk rules, and lifecycle deletion.
4. **Operator surfaces**: build Bubble Tea HUD and modals; extend browser
   history; connect both to the same snapshot/events/plans/operations; rewrite
   primary and contextual help.
5. **Adversarial verification and release candidate**: run mutation,
   concurrency, crash/retry/drop, redaction, PTY/browser, real Lima,
   performance, clean install/upgrade/uninstall, dependency, and advisory
   gates; review code and produce local candidate evidence only.

Each phase must keep existing commands usable and tests green. New claims stay
hidden or explicitly experimental until their supporting capability and
negative-fixture gates pass.

## Complexity Tracking

No constitution violation is required. The two new platform providers are
necessary because host secret custody and guest workload attribution are
different operating-system authorities; both remain behind narrow Go
interfaces and explicit capability probes.
