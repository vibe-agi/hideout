# Hideout Architecture Principles

<!-- markdownlint-disable MD013 -->

## Contract

This document defines the architecture principles for Hideout after the HostFS
V1 foundation. It does not replace [privacy-run-design.md](privacy-run-design.md).
If a principle here conflicts with the Phase 1 design, the Phase 1 design wins
until both documents are intentionally revised.

[threat-model.md](threat-model.md) defines the Phase 1 Lite threat model, TCB,
claims, non-claims, user-authoritative HostFS grant model, loopback boundary,
and PortBridge invariants used when applying these principles to new host
reach-back capabilities.

The purpose of this document is to prevent future features from growing as
isolated patches. OpenTarget, Network Privacy, Backend support, Manager Control
Plane, Distribution, and HostFS Overlay must all follow the same product and
engineering rules.

## Product North Star

Hideout runs an observed command inside a backend boundary and mediates every
host capability it needs — environment variables, host files, network egress,
command interception, and typed host actions — through gates that are
configurable, dynamically decidable by constrained JS policy, audited, and
fail-closed.

Identity synthesis (fake home, env, machine identity) is how a guest is
initialized — a virtualenv for machine identity. The boundary and the mediated
gates are the product; privacy outcomes are benefits delivered by those gates,
not a separate mechanism. Shared policy, scripts, and recipes grow an ecosystem
of boundary policy plus declarative guest environment selection (base image
references), not of imperative environment building.

The target user should experience:

- the command runs like it is local;
- the workspace behaves like the real project;
- host identity, credentials, browser state, proxy secrets, and host files stay
  controlled unless explicitly granted;
- host escape hatches are typed, audited, and reversible;
- the product explains what happened without requiring the user to understand
  containers, FUSE, routing tables, or broker tokens.

## Non-Negotiable Principles

### 1. Privacy Boundary Wins

Compatibility must never silently weaken a privacy boundary. If an operation
cannot be represented by an explicit policy, capability, audit record, and
backend implementation, it fails closed.

Examples:

- no host fallback when a guest command is missing;
- no direct host filesystem mount for HostFS grants;
- no real browser profile by default;
- no proxy secret in target env;
- no generic host execution through Command Proxy.

### 2. Workspace Is Shared, Everything Else Is Granted

The workspace is the intentional collaboration surface. It remains read/write
and may expose project-local secrets.

The workspace must still be a project boundary, not a disguised host home,
credential root, browser profile, or Hideout control-plane store mount. Run
planning must reject those dangerous workspace roots before backend prepare,
with an explicit high-risk override for intentional use.

Everything outside the workspace is hidden by default and must enter through a
typed authority:

- HostFS grant;
- Command Proxy action;
- Host Broker action;
- OpenTarget action;
- Network setup;
- SecretRef used by Hideout setup, not by the target env.

Inside the workspace subtree the workspace authority wins: the workspace is a
uniform read/write zone, and HostFS rules do not apply to paths inside it.
Planning and doctor surfaces must warn loudly when a HostFS rule is shadowed
by the workspace, and workspace membership is decided by real file identity
(`os.SameFile`-level normalization), never by string prefix.

### 3. Every Host Escape Is Typed

Hideout must not grow a generic "run this on the host" mechanism. Host escapes
must be modeled as typed domain actions with their own policy and audit shape.

Allowed direction:

```text
open URL -> host.open (resource type: url)
open file -> host.open (resource type: workspace-file)
launch browser -> browser.launch
control browser -> browser.control
preview service -> preview.open + portbridge
mobile simulator -> mobile.simulator.open
```

Disallowed direction:

```text
host.exec arbitrary command
host.shell arbitrary script
host.fs passthrough mount without policy
```

### 4. UI And Daemon Are Not Authority

CLI, TUI, WebUI, and the `hideoutd` daemon are interaction or transport
surfaces over the same Manager Control Plane. They must not own policy
semantics, backend authority, or filesystem mutation rules.

All user-facing surfaces must call the same Manager Control Plane:

```text
CLI / TUI / WebUI / hideoutd clients
        |
Manager API
        |
Manager Core
        |
Profile / Session / Environment / Backend / Broker / HostFS / Network / Policy / Audit
```

If the WebUI and TUI disagree, the domain model is wrong.

The steady-state architecture is daemon-first, in the Docker model: `hideoutd`
hosts the Manager API and owns cross-invocation state — the environment
registry, session state, typed event streams, daemon-local audit, and background
execution for existing environment stop/clean operations — and CLI, TUI, and
WebUI are its clients. The daemon stays in single-operator form: an operator
token with full access. Read-only tokens, client role matrices, delegated
approval channels, per-subscriber redaction tiers, and replay-protection
protocols are enterprise shapes and are out of scope. Confirmation-required
daemon operations fail closed until an explicit prompt channel exists.

The daemon must not become a generic host execution service, a long-lived
bearer of per-run capability tokens, or a path around Manager plan/apply
validation. Per-run authority still belongs to the session, broker, and typed
capability records described below. The daemon also must not grow tool
installation channels: guest tools come from the declared base image or from
operator-authored setup run inside the boundary.

Surface division: the TUI is the lightweight pane — panels and shortcuts for
watching audit and managing sessions; the WebUI is the fuller management
surface. Both read the same Manager data model; neither owns policy semantics,
and the page sets are not required to mirror each other.

### 5. Session Authority Is Ephemeral

Reusable environments may keep tools, caches, and guest state. They must not own
per-run authority.

Per-run authority includes:

- broker tokens;
- shim directories;
- proxy secret files;
- network route bootstrap files;
- active HostFS grant materialization;
- audit file handles;
- OpenTarget and PortBridge lifetimes.

Every `hideout run` refreshes these even when it resumes a warm environment.

Environments are named, user-selected runtime boxes. A shared `default`
environment gives zero-configuration runs from any directory; a named
environment created explicitly gives isolation and a statically mounted
workspace. Inside a shared environment each session sees only its own
workspace plus HostFS grants: the session mount namespace scopes that view
for ordinary target processes, but it is not a wall against guest root — a
guest-root target in a shared environment may reach other attached
workspaces. Operators who need the VM-level wall between projects create a
dedicated environment. Changing an environment's pinned
configuration (base image digest, backend, profile binding) is drift: the
product fails closed and offers recreation, never a silent switch.

### 6. Policy Is Composed, Deny Wins

Profile, environment, run flags, and scripts may add authority only through the
same effective policy model. User-configured deny rules reduce authority and
win over allow rules.

No layer may implement "last flag wins" for privacy authority.

Policy is live: grant and deny changes, script updates, and env policy edits
take effect on the next evaluated request without restarting the environment.
Only physical mounts follow the environment lifecycle.

### 7. Observable By Default

Every material boundary decision must be visible in at least one of:

- `explain`;
- audit;
- `doctor`;
- Manager API state;
- TUI/WebUI summary.

Audit must be useful to the user. For HostFS, requested paths are recorded so
the user can inspect what the target program probed. Redaction is
deterministic, not heuristic: Hideout-minted control-plane credentials (broker
tokens, UI tokens, `HIDEOUT_SECRET_*` backing values under their self-known
names, generated machine-id) must never be recorded, and user-provided rule
reasons and extra resolved implementation paths stay out of audit. Raw proxy
URLs stay out because the Hideout-managed proxy secret flow must not emit
them and the redactor strips their `HIDEOUT_SECRET_*`-labeled forms; the
redactor does not scan for proxy-shaped user strings. User/application data
inside requests is recorded verbatim as host-local evidence, because Core cannot
reliably identify which user values are secrets; redacting user data belongs to
the export/share boundary and user-owned `audit.redact` policy. Read-only
HostFS write attempts are recorded as audit events too, but must not mutate the
host filesystem.

### 8. Backend Is A Substrate, Not The Product

Backend names such as Lima, SSH, Docker, Apple container, or Linux container do
not define product semantics. They expose capabilities that Hideout maps into a
consistent runtime contract.

The product asks:

```text
Can this backend provide the required capability safely?
```

not:

```text
Can we call this backend-specific feature directly from product code?
```

### 9. Scripts Are Constrained Extensions

Goja policy hooks are for user-specific decisions within a bounded ABI. Scripts
must not become arbitrary plugins.

Scripts may decide or redact only within supplied context. They must not access
filesystem, network, process APIs, Node APIs, timers, or mutable Hideout state.
The builtin validator remains the final authority.

Script capability grows along the expressiveness axis, never the authority
axis: strengthen what scripts can see, decide, and propose; never what they can
execute. Rich decision context and bounded factual queries are welcome;
execution paths, tokens, and raw authority-bearing outputs are not. Scripts
carry zero authority precisely so they can be shared.

### 10. Core Owns Capabilities, Scripts Compose Them

Hideout Core provides capability primitives, not product-specific workflows.
Domain workflows such as browser preview, browser control, adb access, simulator
launch, IDE open, or tool-specific compatibility behavior must be expressed as
constrained policy, adapters, and recipes above those primitives.

The layering is:

```text
Core primitive
  -> Capability provider
    -> JavaScript policy / adapter
      -> Persona recipe or bundle
```

Core primitives include HostFS, Command Proxy, OpenTarget, PortBridge, Network
Policy, Audit, Policy Engine, and Manager plan/apply. Capability providers are
Go-owned executors behind the broker or Manager. JavaScript may classify an
intent, construct a proposal, choose among declared recipes, or redact
presentation fields; it must not execute the capability or become the security
boundary.

Core supplies facts and authority boundaries, not product risk conclusions. A
workflow adapter may decide that a command, path, endpoint, or tool marker is
risky, but that classification belongs to policy. Core should expose bounded
facts and query APIs, validate proposals, and execute Go-owned providers.

Responsibility rules:

```text
Names bind; they do not authorize.
Configuration limits; it does not execute.
JavaScript interprets; it does not hold authority.
Core supplies facts; it does not own product risk logic.
Core validates proposals; scripts do not bypass validators.
Go providers execute capabilities; bundles do not ship providers.
Outcomes are typed; guest rewrites are not host invocation.
Audit records the authority decision; UI only presents it.
```

These rules apply to Command Proxy, OpenTarget, Endpoint Exposure, Network,
HostFS, and future host reach-back features. A feature that needs a
product-specific judgment should put that judgment in an adapter or recipe. A
feature that touches host authority must end in a typed Go capability provider.

The default is positive as well as restrictive: a new flexible product
judgment is built as a Go primitive plus a constrained JS decision point.
Hard-coding such a judgment into compiled Go requires a recorded reason.
Capability execution, validators, redaction, and transport remain Go-owned
always.

Tool supply follows the same split. A target command is just a guest process,
and an expected-command declaration is diagnostic and environment-fingerprint
input, not an installation request. Hideout does not materialize guest tools:
they come from the declared base image or from operator-authored setup run
inside the boundary. A package manager, editor, browser, or agent CLI name
must not become Core product semantics.

Hideout does not own tool installation as a product domain; it owns the
boundary once a tool runs. Guest tools come from two paths: the declared base
image, and operator-authored setup executed as an ordinary in-boundary
`hideout run` (see principle 14). Hideout ships no package-installation
providers.

The base image is declarative guest-domain data, not host authority. An
ecosystem artifact may declare a base image reference — a name plus digest,
nothing more — and backends consume that reference to start the guest. A bad
image is contained by the boundary, so image references do not pass the host
trust gate; the image digest participates in the environment fingerprint, and
changing it means a new environment. The dividing line is data consumed
versus steps executed: referencing an existing image is data;
ecosystem-shared preparation steps that Hideout would execute (Dockerfile
RUN, install scripts, first-boot hooks) remain prohibited until a dedicated
trust design promotes them
([ecosystem-foundation-design.md](ecosystem-foundation-design.md)). Backend
configuration — mounts, port forwards, network, provisioning fragments — is
host domain and is always generated by Hideout, never injected by ecosystem
artifacts.

The normative script SDK classes and runtime restrictions are defined in
[script-extension-architecture.md](script-extension-architecture.md). The SDK
must not expose raw Go standard library objects, host filesystem handles, HTTP
clients, process APIs, environment APIs, broker tokens, mutable profile stores,
or backend driver handles.

Examples:

```text
PortBridge
  -> browser-control adapter
    -> web-agent recipe

PortBridge
  -> adb adapter
    -> android-dev recipe

OpenTarget + PortBridge
  -> preview adapter
    -> h5-dev recipe
```

Extensibility must never move the security boundary from Go Core into user
scripts.

### 11. Installability Is A Core Architecture Concern

Hideout depends on multiple binaries and backend prerequisites. Distribution is
not a packaging afterthought.

The product must own:

- first-run initialization;
- typed initialization tasks;
- guest helper binary discovery or installation;
- backend prerequisite checks;
- schema/version metadata repair;
- `doctor --fix` style remediation;
- release gate verification.

If a feature requires the user to manually assemble hidden runtime parts, it is
not product-complete.

### 12. Share Bundles, Not Local Identity

Hideout should support a community ecosystem of shareable policy bundles,
recipes, presets, and project manifests. These artifacts must be reviewable in
Git, versioned, and installable.

Local profile instances are not the shareable unit. They may contain identity
material, host paths, secret refs, local overrides, and usage-derived decisions.
Shared artifacts must express templates, defaults, scripts, recipes, base
image references, and declared inputs instead of exporting private local
state. Secrets are parameterized through SecretRef declared inputs: the shared
artifact names what it needs, the user supplies the value locally, and the
artifact body never contains sensitive material.

### 13. Ecosystem Artifacts Are Not Authority

Bundles, recipes, project manifests, and scripts declare intent. They do not
directly gain runtime authority.

Every ecosystem artifact must pass through:

```text
parse -> validate -> verify -> plan -> apply -> compile effective policy
```

No bundle, project file, or goja script may silently mutate a profile, enable a
HostFS grant, add a passthrough mount, launch a host app, bridge a port, change a
network route, or apply a HostFS overlay without an explicit Manager operation.

Installing an artifact never silently grants effect: taking effect requires an
explicit confirmed step, which may be presented inside the same install flow
as a single review-and-confirm. Project discovery is separate from project
apply. Export is separate from share.

### 14. Initialization Is Planned, Not Scripted

Hideout supports initialization tasks, not arbitrary initialization scripts.

First-run setup, `doctor --fix`, helper installation, schema metadata repair,
backend preparation, project bootstrap, and bundle enablement must compile into typed
`InitTask` plans owned by Manager.

Allowed direction:

```text
PlanInit -> InitTask -> ApplyInit -> Verify -> Audit
```

Disallowed direction:

```text
bundle install script
project init script
host shell bootstrap
guest shell bootstrap supplied by ecosystem code
```

Session bootstrap files are generated by Hideout for a specific run. They are
not a user or ecosystem extension point.

A declared base image reference is not scripted initialization: it is data
compiled into the backend prepare plan (principle 10). The prohibition above
targets executable steps supplied by ecosystem code, not declarative inputs.

Preparing a reusable environment by running setup commands as an ordinary
`hideout run` target is not initialization and is not restricted by this
principle. That path already carries the full boundary: selected network
policy, no ambient host authority, audit, and fail-closed behavior. The
prohibition above is about initialization mechanisms and ecosystem extension
points, not about what an operator deliberately executes inside the boundary.

### 15. One Operator, MVP First

Hideout is built for a professional individual operator on their own machine.
Every security mechanism must map to a threat that operator actually faces:
an untrusted CLI inside the boundary, and occasionally a third-party artifact
pulled from Git. Machinery that defends threats which only exist at
organization scale — role systems, approval workflows, policy distribution,
compliance evidence, supply-chain infrastructure — is not built, and designs
must not accrete it speculatively. Ecosystem trust machinery (signing,
revocation, publisher identity, namespace protection) becomes a day-1
requirement when a public marketplace launches and is not designed ahead of
that launch.

The user-facing concept set stays small: environment, workspace, permission,
audit. New features that force a new noun into the first-run path need a
recorded reason.

## Capability Lifecycle

New capabilities move through four stages:

```text
Probe -> Design Contract -> Product Path -> Release Gate
```

### Probe

Smallest executable experiment. It may live behind `hideout lab`, tests, or an
internal package. It must not be part of default `hideout run`.

### Design Contract

The domain model, policy shape, audit fields, backend capability requirements,
and failure behavior are written down.

### Product Path

CLI, Manager API, TUI/WebUI, `explain`, `doctor`, and audit use the same model.
No separate UI-only or CLI-only policy exists.

### Release Gate

The capability is covered by local tests and, when it crosses backend or privacy
boundaries, an end-to-end gate.

## Architecture Documents

Start with [README.md](README.md) for the reading order and
[STATUS.md](STATUS.md) for current implementation status. The following
documents extend this principle layer:

- [privacy-run-design.md](privacy-run-design.md)
- [threat-model.md](threat-model.md)
- [privacy-run-test-plan.md](privacy-run-test-plan.md)
- [opentarget-architecture.md](opentarget-architecture.md)
- [network-privacy-architecture.md](network-privacy-architecture.md)
- [backend-capability-matrix.md](backend-capability-matrix.md)
- [manager-control-plane.md](manager-control-plane.md)
- [distribution-bootstrap.md](distribution-bootstrap.md)
- [init-task-architecture.md](init-task-architecture.md)
- [script-extension-architecture.md](script-extension-architecture.md)
- [ecosystem-foundation-design.md](ecosystem-foundation-design.md)
- [policy-config-supply-chain.md](policy-config-supply-chain.md)
- [hostfs-overlay-design.md](hostfs-overlay-design.md)
- [tui-webui-experience.md](tui-webui-experience.md)

These documents must not introduce authority that violates this file or the
Phase 1 design contract.
