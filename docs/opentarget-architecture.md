# OpenTarget Architecture

<!-- markdownlint-disable MD013 -->

## Contract

OpenTarget is the typed host or guest application escape model for Hideout. It
turns user intents such as "open a browser", "preview this dev server", or
"launch a simulator" into explicit policy, broker, port bridge, and audit
decisions.

This document follows [architecture-principles.md](architecture-principles.md).
It does not promote every OpenTarget type to Phase 1. Product status is decided
by [privacy-run-design.md](privacy-run-design.md).

## Problem

Developer tools and agents need controlled host interaction:

- open a URL in a host browser;
- reveal a generated file;
- preview a guest web server in the host browser;
- attach browser automation to an isolated profile;
- work with mobile simulators;
- open an IDE or system app for a project-local file.

If these are implemented as ad hoc command proxies, Hideout becomes a generic
host execution system. OpenTarget prevents that by giving every host escape a
typed model.

## Principles

- Every target has a type, owner session, policy proposal, audit record, and
  lifecycle.
- OpenTarget may use PortBridge, but PortBridge must not decide product policy.
- Browser control is a Browser OpenTarget, not an extension of `open`.
- OpenTarget Core defines target authority and lifecycle; adapters define
  protocol-specific intent such as browser control, preview, adb, simulator, or
  IDE behavior.
- User experience presets may group targets by workflow, but the lower layer is
  target-type based.
- Real host profiles and private host-local network targets are denied by
  default.

## Domain Model

```text
OpenTarget
  id
  type
  ownerSessionId
  profile
  request
  policyDecision
  resources
  portBridges
  auditContext
  lifecycle
```

Target types:

```text
host.open.url
host.open.file
browser.launch
browser.control
preview.open
app.launch
mobile.simulator.open
ide.open
```

OpenTarget lifecycle:

```text
requested -> policy-approved -> materialized -> active -> closed
requested -> denied
active -> expired
active -> failed
```

## Adapter Boundary

OpenTarget is a core primitive. It must not absorb every domain protocol into
the broker or backend layer.

The layering is:

```text
OpenTarget / PortBridge / Command Proxy
  -> Go capability provider
    -> JavaScript adapter
      -> persona recipe
```

PortBridge remains a generic transport primitive, not an adb, browser, or
preview-specific target type. `endpoint.expose.host-to-guest` is the first
direction productized and uses run-scoped, audited PortBridge mappings owned by
a typed capability. The first consumer is `preview.open` over declared or manual
guest-loopback TCP candidates. `endpoint.expose.guest-to-host` remains
design-ready/lab until a separate product design promotes it. Any bridge still
needs an owning OpenTarget or explicit product design.

Adapters may understand product protocols and developer workflows. For example:

- a preview adapter may recognize a guest dev server and propose
  `preview.open`;
- a browser-control adapter may propose an isolated browser target and a
  guest-to-host control bridge;
- an adb adapter may propose a constrained bridge to a host adb server endpoint;
- a simulator adapter may propose a typed simulator open or deep link.

Adapters must not execute host commands, open host ports, read host state, or
create capabilities directly. They produce structured proposals. The Go
validator and capability provider decide whether the proposal is legal and
materialize it only through Broker or Manager.

## Workflow Presets

Presets are product composition, not low-level authority.

### Daily Basics

Targets:

- `host.open.url`
- `host.open.file`
- `app.launch` for safe host apps such as Finder or system file reveal

Use cases:

- open documentation;
- reveal an output file;
- open a workspace-local file with a host app.

### Web Development

Targets:

- `preview.open`
- `browser.launch`
- `browser.control`
- `endpoint.expose.host-to-guest` for preview services
- `endpoint.expose.guest-to-host` for future browser control

Use cases:

- guest dev server is previewed in a host browser;
- agent controls isolated browser profile;
- loopback mapping is explicit and auditable.

Implementation shape:

- preview and browser-control behavior should live in adapters and recipes;
- Core owns OpenTarget, PortBridge, browser profile isolation, and audit;
- framework-specific knowledge such as Vite, Next.js, Playwright, or DevTools
  compatibility must not become broker policy unless needed for a security
  invariant.

### Android

Targets:

- `mobile.simulator.open`
- `app.launch` for emulator tooling;
- explicit `endpoint.expose.guest-to-host` proposals when an adb adapter is
  enabled.

Use cases:

- launch emulator;
- open app or deep link;
- forward or reverse a specific debug port.

adb is high-authority. The first product design should treat it as an adapter
over PortBridge and audit, not as a generic host port escape. A later
protocol-aware adapter may classify adb subcommands, but the Core primitive is
still the typed bridge and lifecycle.

adb adapters require the higher-risk `endpoint.expose.guest-to-host` primitive
to be designed and promoted from lab to product path. Until that promotion,
adapter proposals for host service reachability fail closed at the Go validator.

### iOS

Targets:

- `mobile.simulator.open`
- `app.launch` for simulator tooling;
- browser or WebView preview through explicit target rules.

Use cases:

- launch iOS simulator;
- open a URL in simulator Safari;
- preview local development pages through controlled mapping.

Linux guest backends cannot run the full Xcode toolchain. iOS support should be
documented as assisted workflow unless a macOS-native backend provides a
reviewed confinement model. Host-side `xcodebuild` must not be represented as a
generic host execution target.

### AI Agent

Targets:

- `browser.launch`
- `browser.control`
- `preview.open`
- `host.open.file`
- `endpoint.expose.host-to-guest` for previews and local callbacks
- `endpoint.expose.guest-to-host` for future browser control

Use cases:

- agent previews app;
- agent controls isolated browser profile;
- host file open remains audited and scoped.

## PortBridge Relationship

PortBridge is a transport primitive. It must always be owned by an OpenTarget or
an explicit lab probe.

```text
OpenTarget preview.open
  -> requests endpoint.expose.host-to-guest by candidateId
  -> Manager allocates endpoint
  -> policy validates target and exposure
  -> audit records mapping
  -> endpoint closes with OpenTarget/session
```

PortBridge does not decide:

- whether browser control is allowed;
- whether a target is safe;
- whether localhost/private network exposure is acceptable;
- whether the guest should receive a host endpoint.

Those decisions belong to the owning OpenTarget policy.

## Browser Rules

Browser targets must use isolated browser profile state by default.

Required defaults:

- no real browser profile;
- no remote-debugging port exposed to guest unless `browser.control` is approved;
- no host-local or private network URL open unless target policy explicitly
  allows it;
- audit records browser profile location, target type, and exposed endpoint
  category without leaking tokens.

## Policy Shape

OpenTarget requests compile into capability proposals:

```json
{
  "subject": "session:<id>",
  "action": "browser.control",
  "resources": ["opentarget:<id>", "browser-profile:<profile>"],
  "route": "host-broker",
  "decision": "allow"
}
```

Policy may allow, deny, or require a future prompt. Until prompts exist,
ambiguous requests deny.

## Audit Shape

OpenTarget audit events include:

```text
targetId
targetType
decision
ownerSessionId
policyEffect
resources
portBridgeId, when any
endpointCategory, when any
```

Audit must not include broker tokens, browser automation secrets, proxy secrets,
or arbitrary host command lines.

## Phase Plan

### Phase 1 Product Path

- `host.open.url`
- `host.open.file` for workspace-mapped files
- isolated browser profile for URL open
- Command Proxy for `open` and `xdg-open`

### Capability Probe

- browser-control probe;
- preview-open probe;
- loopback PortBridge probes.

### Next Product Increment

- harden `preview.open` beyond declared/manual candidates, including callback
  and readiness behavior;
- promote `browser.launch`;
- stabilize the adapter ABI for preview and browser-control recipes;
- define browser-control policy and endpoint lifecycle;
- expose OpenTarget state through Manager API and TUI/WebUI.

### Later

- Android and iOS target presets;
- IDE integration;
- Docker or local service targets;
- user prompts.

## Open Questions

- Which browser engines are first supported?
- Should preview endpoints be stable per session or per target?
- How should target presets be stored in profile policy?
- What is the first minimal browser-control handshake that is useful but safe?
