# Script Extension Architecture

<!-- markdownlint-disable MD013 -->

## Contract

Hideout supports JavaScript extension points through constrained `goja`
entrypoints. Scripts are policy and composition tools. They are not plugins with
ambient authority, not backend drivers, and not isolation boundaries.

This document follows [architecture-principles.md](architecture-principles.md)
and is subordinate to [privacy-run-design.md](privacy-run-design.md).

Status:

```text
Script runtime for command policy and audit redaction: Required Phase 1.
Adapter ABI beyond command.decide and audit.redact: Design-ready.
Safe context queries: Later.
Endpoint exposure proposal builders: Later. Each builder is gated by the
corresponding direction-specific exposure primitive being promoted to a product
path.
```

## Product Reasoning

Hideout should avoid baking tool-specific workflows into Core. Browser preview,
browser control, adb access, simulator launch, IDE open, MCP integration, and
tool compatibility rules are important product workflows, but they should be
implemented as adapters and recipes over stable primitives.

The business goal is a small, durable Core with a broad ecosystem surface:

```text
Core primitive
  -> Capability provider
    -> JavaScript policy / adapter
      -> Persona recipe or bundle
```

This lets Hideout support H5, Android, iOS-assisted, backend, and AI-agent
workflows without turning the core broker into a collection of business
protocol implementations.

## Terminology

Adapter has two meanings in Hideout. Use the qualified term when ambiguity is
possible.

```text
Script adapter
  JavaScript policy code that maps a domain intent into a capability proposal.

Backend adapter
  Go code that integrates a backend substrate such as Lima, native, Docker, or
  SSH with Hideout's run contract.
```

This document is about script adapters. Backend adapters remain Go-owned
substrate integrations and must not execute script-defined authority.

## Layer Responsibilities

### Core Primitive

Core primitives define authority types and invariants.

Examples:

- HostFS grants and HostFS audit;
- Command Proxy request normalization;
- OpenTarget typed host or guest application intents;
- PortBridge endpoint allocation, direction, lifetime, and cleanup;
- Network policy and proxy bootstrap;
- Audit event schema and redaction boundaries;
- Policy Engine validation;
- Manager plan/apply lifecycle.

Core primitives must be implemented in Go and covered by product gates before
they become default run-path authority.

### Capability Provider

A capability provider is the Go-owned executor for a primitive. It receives a
validated request from Broker or Manager and materializes the capability.

Examples:

- Host Broker opening an approved isolated browser URL;
- HostFS daemon serving an approved host file read;
- PortBridge provider allocating one explicit endpoint mapping;
- Manager applying an approved OpenTarget plan.

Capability providers must not trust JavaScript output directly. They execute
only after the final Go validator accepts the proposal and effective policy.

### JavaScript Policy / Adapter

A JavaScript adapter understands domain intent and maps it into proposals.

Allowed responsibilities:

- classify command arguments or target shape;
- choose among declared target templates;
- construct capability proposals;
- attach reason strings and audit tags;
- redact presentation fields for audit export;
- encode protocol-specific intent without executing that protocol.

Disallowed responsibilities:

- reading host files;
- reading host or guest environment variables;
- opening network connections;
- spawning processes;
- mutating profile, session, or Environment state;
- creating undeclared action names;
- obtaining broker tokens or backend handles;
- bypassing Manager plan/apply;
- changing immutable audit evidence.

An adapter may know that `adb` uses a host TCP endpoint or that Chromium DevTools
uses a loopback port. It may propose a typed endpoint exposure or OpenTarget. It
must not open the port itself.

Runtime decision context is allowed to be rich. Hideout should give adapters the
structured facts needed for real local policy decisions: full URLs, parsed
redirect URIs, endpoint candidate metadata, ports, process argv, cwd class, and
declared endpoint metadata when those facts are part of the current decision
snapshot. This context is local runtime input to a constrained goja VM; sharing
the adapter code does not share the user's runtime context.

Context richness is not authority. Scripts may see facts, but they cannot use
those facts to materialize capabilities. For endpoint exposure, a script may
reference a Go-minted `candidateId` and request a category, TTL, close policy,
reason, or audit tag. It must not provide raw host addresses, guest addresses,
bridge direction, owner IDs, backend endpoints, provider handles, or a PID as
authority. Go resolves the candidate from the immutable snapshot and performs
the final validation.

Hideout control-plane secrets are never policy context. Broker tokens, proxy
secrets, hidden-env backing values, manager tokens, and PortBridge provider
handles must not be exposed to scripts. User/application secrets embedded in
arbitrary runtime facts cannot be perfectly identified by Core; policy context
therefore must not be treated as a public record. Audit, Boundary Summary,
exported fixtures, and UI views must redact or summarize sensitive values even
when the runtime policy script saw full context.

### Persona Recipe

A persona recipe is a product composition for a workflow. It combines adapters,
policy templates, environment requirements, doctor checks, and user-facing
defaults.

Examples:

```text
h5-dev
  preview adapter
  node/pnpm environment hints
  workspace secret deny templates

android-dev
  adb adapter
  Android SDK environment hints
  keystore and adbkey deny templates

ios-assist
  code-edit and dependency-analysis policy
  simulator URL-open templates
  explicit non-claim for xcodebuild inside Linux guests
```

Recipes are shareable ecosystem artifacts. They are not direct runtime
authority.

## Script SDK Classes

This section is the normative source for SDK classes. Other architecture
documents should refer here instead of duplicating the detailed list.

The script runtime may expose only three classes of SDK functions.

### Pure Helpers

Pure helpers transform supplied data and do not touch the host.

Examples:

- URL parsing;
- path normalization on sanitized paths;
- glob matching;
- CIDR containment checks;
- command argument parsing;
- semver checks;
- stable JSON formatting.

### Proposal Builders

Proposal builders return structured proposals for the Go validator.

Examples:

```javascript
hideout.decision.allow(...)
hideout.decision.deny(...)
hideout.decision.ask(...)
hideout.decision.auditOnly(...)
```

Future builders may construct structured HostFS, OpenTarget, or
`endpoint.expose.*` proposals, but they still return data. They do not
materialize capabilities or request raw transport mappings.

### Safe Context Queries

Safe queries are a future SDK class. They may inspect sanitized effective policy
or backend capability facts after their ABI, replay model, and audit fields are
designed. They are separate from the decision context passed into a policy
entrypoint. Decision context is a Go-minted immutable snapshot for that one
evaluation; safe queries are additional APIs scripts may call.

Safe queries must not reveal host file existence, host env, secret values,
broker tokens, or raw backend state.

Examples:

```javascript
hideout.context.hasCapability("endpoint.expose.guest-to-host")
hideout.context.backendSupports("host-to-guest-provider")
hideout.context.policyHas("hostfs.read")
```

Safe queries are optional and not required for Phase 1 command policy. If a
query would leak host reality, the runtime must not expose it.

Replay has two layers. The local decision snapshot used for policy evaluation
may contain rich runtime facts and can be retained in a protected local store or
bound by hash for debugging. Audit, Boundary Summary, exported fixtures, and UI
views are redacted presentation surfaces and are not required to contain enough
data to fully replay a decision. A replay tool that needs full fidelity must use
the protected decision snapshot, not the redacted audit view.

The default for rich snapshots should be hash-bound, not retained. If a full
snapshot is retained for debugging, it becomes a secret-bearing session artifact:
store it under the Hideout control-plane store with owner-only permissions,
include it in store-reserved-root protection, expire it with the session unless
the user explicitly preserves it, and never export it through audit, fixtures, or
UI views by accident.

## Runtime Restrictions

The runtime must not expose raw Go standard library packages or host objects.

Disallowed injected APIs include:

- filesystem APIs such as `os.Open`, `ReadFile`, or directory listing;
- environment APIs such as `os.Environ` or `Getenv`;
- network clients or transports;
- process execution APIs;
- backend driver handles;
- broker tokens or socket paths;
- mutable Manager, Profile, Session, or Environment stores;
- timers, module loaders, or host-global caches.

Scripts must be bounded by source size, input size, output size, entrypoint
count, timeout, and profile script count limits. Script failures, invalid output,
unknown fields, and timeouts fail closed.

Policy decisions must be replayable from sanitized input and script source. A
script must not depend on ambient host state for authorization. The runtime must
use deterministic time and random sources for policy evaluation, or reject APIs
that introduce nondeterminism.

## Examples

### Browser Preview

```text
H5 tool asks to open http://localhost:5173
  -> declared endpoint candidate exists in profile or approved project policy
  -> preview adapter classifies candidate as a web preview
  -> adapter proposes endpoint.expose.host-to-guest by candidateId
  -> Go validator checks owner, source, backend, address, lifetime, and audit
  -> PortBridge provider materializes one endpoint
  -> optional host.open opens isolated browser URL pointing at the mapped endpoint
```

Core does not understand Vite, Next.js, or the browser's DevTools protocol. It
understands OpenTarget, PortBridge, browser profile isolation, and audit.

### Browser Control

```text
Recipe enables browser-control adapter
  -> adapter proposes browser.control for an isolated profile
  -> Go validator checks target policy and endpoint exposure
  -> provider launches browser with a host-loopback DevTools endpoint
  -> PortBridge exposes only the approved endpoint to guest
```

The adapter may speak in browser-control terms. Core owns endpoint allocation,
secret handling, exposure scope, and cleanup.

### Android adb

```text
Recipe enables adb adapter
  -> adapter references a declared or device-discovered host adb endpoint
  -> adapter proposes endpoint.expose.guest-to-host by candidateId
  -> Go validator checks higher-risk guest-to-host policy and audit requirements
  -> PortBridge provider exposes only the approved mapping
```

Phase 1 should treat adb as high-authority and coarse-grained unless a later
protocol-aware adapter design introduces per-command policy. It must not be
implemented as a generic host port escape.

adb adapters require the higher-risk `endpoint.expose.guest-to-host` primitive
to be designed and promoted from lab to product path. Until that promotion,
adapter proposals for host service reachability fail closed at the Go validator.

### iOS-assisted Workflow

Linux guest backends cannot run the full Xcode toolchain. The iOS recipe should
be explicit: code editing, dependency analysis, and controlled simulator URL
opens may be supported; host-side `xcodebuild` is not a Core capability and must
not be disguised as `host.exec`.

## Entrypoint Mapping

This table lists script adapter entrypoints. Other Later policy entrypoints,
such as environment, broker, network, or profile materialization hooks, remain
owned by [privacy-run-design.md](privacy-run-design.md) until they receive a
dedicated adapter contract.

Current entrypoints:

| Entrypoint | Phase | Owner | Adapter use |
| --- | --- | --- | --- |
| `decideCommand(ctx)` | Required Phase 1 | Policy Engine | Command Proxy decisions for registered commands such as `open` and `xdg-open`. |
| `redactAudit(ctx)` | Required Phase 1 | Policy Engine | Presentation redaction for exported or viewed audit details. |

Design-ready and Later entrypoints:

| Entrypoint | Phase | Purpose |
| --- | --- | --- |
| `command.normalize(ctx)` | Design-ready | User-scripted normalization for future command families. |
| `opentarget.decide(ctx)` | Later | Direct OpenTarget proposal hook after OpenTarget product path is promoted. |
| `endpoint.expose.decide(ctx)` | Later | Direct endpoint exposure proposal hook after a direction-specific exposure product path is promoted. |

Adapters for adb, browser control, preview, IDE, or simulator workflows must use
an entrypoint that exists in the current effective policy. Today, that usually
means classification inside `decideCommand(ctx)` for a registered command shim.
Future entrypoints must be added to the profile schema, validator, audit shape,
and Gate 0 contract before bundles can depend on them.

## Development Rules

- Add a Core primitive only when a new authority type is needed.
- Add an adapter when a domain protocol maps onto existing primitives.
- Add a recipe when a developer persona needs a curated composition.
- Do not put protocol-specific business logic into Broker, HostFS, PortBridge,
  or backend adapters unless it is required to enforce the primitive's security
  invariant.
- Do not add a JavaScript API unless its output can be validated by Go without
  trusting script behavior.
- Do not promote a lab capability until the primitive, adapter contract, audit
  fields, and failure behavior are documented.

## Phase Plan

### Phase 1 Product Path

- `decideCommand(ctx)` and `redactAudit(ctx)` goja ABI.
- Deterministic time and random sources for policy evaluation.
- Pure helper SDK and decision proposal builders needed by required command
  policy.
- Final Go validation for every script proposal.
- Gate 0 checks for forbidden runtime globals and raw host capability exposure.

### Design-Ready

- Script adapter packaging as bundle entries.
- `command.normalize(ctx)` contract for future command families.
- Persona recipe references to adapters without granting authority directly.
- Adapter permission diff in Manager and UI surfaces.

### Later

- Safe context query SDK backed by immutable per-evaluation snapshots.
- Structured OpenTarget and endpoint exposure proposal builders.
- Direct `opentarget.decide(ctx)` and `endpoint.expose.decide(ctx)` entrypoints.
- Author tooling such as script fixture tests and policy evaluation CLI.

## Failure Behavior

- Missing entrypoint: fail closed.
- Parse error, runtime error, timeout, panic, oversized input, oversized output,
  or invalid return shape: fail closed.
- Unknown output field: fail closed.
- Proposal outside profile max capabilities or action/route vocabulary: fail
  closed.
- Adapter proposal for an unpromoted Core primitive: fail closed at the Go
  validator.

## Open Questions

- What is the first product-grade structured endpoint exposure proposal schema?
- Should future OpenTarget adapters share one `opentarget.decide(ctx)`
  entrypoint or use target-specific entrypoints?
- How should Manager present adapter permission diffs without implying the
  adapter has authority before apply?
- Which authoring tools are required before public bundle contribution?
