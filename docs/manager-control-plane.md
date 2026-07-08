# Manager Control Plane

<!-- markdownlint-disable MD013 -->

## Contract

Manager Control Plane is the local coordination layer for Hideout. CLI, TUI,
and WebUI must use the same domain model and authority rules through Manager
Core and Manager API.

This document follows [architecture-principles.md](architecture-principles.md).

## Problem

Hideout has many powerful modules:

- profiles;
- identity stores;
- sessions;
- reusable environments;
- backends;
- HostFS grants;
- network plans;
- command proxy rules;
- OpenTargets;
- audit logs;
- policy scripts;
- secrets.

If each interface manages these directly, the product becomes fragmented and
unsafe. Manager Control Plane prevents that by owning orchestration and state
transitions.

## Architecture

```text
CLI
TUI
WebUI
Automation
   |
Manager API
   |
Manager Core
   |
Profile Store
Environment Store
Session Store
Backend Registry
Host Broker
HostFS Policy
Network Planner
Policy Engine
Audit Store
Secret Store
OpenTarget Registry
Bundle Store
Project Store
Trust Store
Init Task Engine
Run Planner
```

## Principles

- UI surfaces do not own authority.
- Manager Core owns transitions, validation, and explainable plans.
- Run, init, doctor remediation, and future UI actions must enter through
  Manager-owned planning models before they mutate state or start a backend.
- Manager API is local-only by default.
- Manager API must be safe for both TUI and WebUI.
- Manager state is observable and auditable.
- Long-running operations have IDs, status, logs, and cancellation.

## Domain Resources

Minimum resource model:

```text
Profile
Identity
Environment
GuestImageRef
Session
HostFSRule
HostFSOverlay
NetworkPlan
CommandProxyRule
OpenTarget
PortBridge
AuditEvent
SecretRef
Backend
DoctorCheck
PolicyScript
CapabilityAdapter
InitRequirement
InitPlan
InitTask
InitRun
InitResult
BundleSource
Bundle
BundleVersion
BundleReference
BundleEntrypoint
BundlePermission
ProjectManifest
ProjectLock
ProjectApplyPlan
```

This list covers implemented resources plus near-term increments, not an
implementation status table; [STATUS.md](STATUS.md) owns which resources exist
today, and referencing an unimplemented resource fails closed.

Environment covers the named environment model: the shared `default`
environment plus explicitly named environments, registered in one environment
registry.

GuestImageRef means a declarative guest base image reference: an image name
plus digest. It is guest-domain data consumed by backend prepare, it does not
pass the host trust gate, and its digest participates in the environment
fingerprint.

CapabilityAdapter means a Manager-visible script adapter reference: entrypoint,
bundle/version, declared permissions, required Core primitives, and risk labels
for a domain workflow. It is not a backend adapter and does not execute
authority directly. Backend adapters remain Go-owned backend substrate
integrations.

Each resource needs:

```text
id
kind
status
createdAt
updatedAt
ownerProfile, when relevant
ownerSession, when relevant
capabilityBoundary, when relevant
```

## Operations

Manager operations should use plan/apply semantics for authority-changing
actions.

Examples:

```text
PlanRun(command, profile, workspace, flags) -> RunPlan
ApplyRun(planId) -> RunResult

PlanHostFSRule(profile, rule) -> PolicyChangePlan
ApplyHostFSRule(planId) -> HostFSRule

PlanNetwork(profile, mode) -> NetworkPlan
ApplyNetwork(planId) -> ProfileChange

PlanOpenTarget(session, target) -> OpenTargetPlan
ApplyOpenTarget(planId) -> OpenTarget

PlanInit(scope, profile, project, backend, flags) -> InitPlan
ApplyInit(planId) -> InitRun
GetInitRun(runId) -> InitResult

PlanDoctorFix(checks, scope) -> InitPlan
ApplyDoctorFix(planId) -> InitRun

PlanBundleInstall(source) -> InstallPlan
ApplyBundleInstall(planId) -> BundleVersion

PlanProjectApply(projectPath) -> ProjectApplyPlan
ApplyProjectApply(planId) -> ProjectManifest
```

Plan output should be readable by CLI, TUI, and WebUI.

Current implementation status, summarized for Manager ownership only. The
cross-subsystem status source is [STATUS.md](STATUS.md).

- `PlanInit` and `ApplyInit` own first-run bootstrap tasks.
- `PlanDoctorFix` and `ApplyDoctorFix` reuse Init Task plans for repair.
- `PlanRun` owns run preflight decisions: command presence, profile
  load-or-init, temporary network overrides, ephemeral identity materialization,
  backend normalization, and workspace-to-guest mapping.
- Manager Core owns reusable environment selection, environment runtime
  preparation, and environment start/finish status transitions for `run`.
- Manager Core owns run session lifecycle setup and teardown: session layout,
  profile/runtime identity paths, env policy materialization, audit writer
  activation, audit close, and sensitive session cleanup.
- Manager Core owns run network setup: direct/tun2socks plan preparation,
  hidden proxy secret materialization, backend-specific runtime verification
  flags, and `network.setup` audit.
- Manager Core owns run data plane setup and teardown before backend execution:
  broker token and endpoint lifecycle, broker endpoint file, command proxy shim
  materialization, HostFS effective policy/service, HostFS guest helper
  materialization, broker env injection, and `session.start` audit.
- Manager Core owns `ApplyRun`: backend availability check, pending lightweight
  InitTask application before any session/backend prepare side effect, policy
  validation, backend prepare/run/cleanup, environment start/finish transitions,
  `session.end`, `backend.cleanup`, run result shaping, and cleanup error
  precedence.
- Manager Core owns `ExplainRun`: reusable environment selection, explain-only
  session creation, environment runtime non-preparation, and session cleanup.
- CLI `run` is now a thin caller over `PlanRun`, `ExplainRun`, and `ApplyRun`.
  TUI, WebUI, and automation must use the same Manager operation instead of
  reassembling the backend/data-plane sequence.
- CLI `stop` and `clean` are thin callers over Manager reusable environment
  lifecycle plan/apply operations. TUI, WebUI, and automation must use the same
  target/skipped/apply model instead of reimplementing environment store
  selection, backend stop, or cleanup logic.
- CLI `list` renders reusable environment records from Manager overview, so the
  terminal, TUI, WebUI, and API share one observation path for environment
  state.
- Manager API exposes the minimal run surface: `POST /api/v1/run/plan`,
  `POST /api/v1/run/apply`, and `GET /api/v1/run/status`.
- Manager API exposes the minimal init surface:
  `POST /api/v1/init/plan` and `POST /api/v1/init/apply`.
- Manager API exposes controlled reusable environment lifecycle actions:
  `POST /api/v1/environment/stop/plan`,
  `POST /api/v1/environment/stop/apply`,
  `POST /api/v1/environment/clean/plan`, and
  `POST /api/v1/environment/clean/apply`.
- Manager API exposes controlled command-proxy registration actions:
  `POST /api/v1/profile/command-proxy/plan` and
  `POST /api/v1/profile/command-proxy/apply`. These mutate only durable
  `profile.commandProxy.commands` entries for `host.open` symbols and must use
  the same profile validator as CLI `profile command-proxy`.
- Manager API exposes controlled profile HostFS rule actions:
  `POST /api/v1/profile/hostfs/plan` and
  `POST /api/v1/profile/hostfs/apply`. These mutate only durable
  `profile.hostfs.grants` and `profile.hostfs.deny` rules and must use the same
  HostFS rule grammar and profile validator as CLI `profile fs`.
- Manager API exposes controlled profile env policy actions:
  `POST /api/v1/profile/env/plan` and `POST /api/v1/profile/env/apply`. These
  mutate only durable `profile.env.public`, `profile.env.inherit`, and
  `profile.env.deny` policy, use the same profile validator as CLI
  `profile env`, and must not return public env values in plan/apply responses.
- The profile mutation endpoints above describe the current implemented
  surface accurately. As the API stabilizes they converge to a single
  `POST /api/v1/profile/policy/plan` and `POST /api/v1/profile/policy/apply`
  pair with a `kind` discriminator (`command-proxy`, `hostfs`, `env`).
  Convergence changes endpoint shape only; validators, plan/apply semantics,
  and authority bounds are unchanged.
- `run/apply` executes only through a configured `RunBackendFactory`. The local
  `hideout ui` server wires this factory to the same backend adapters used by
  CLI `run`; tests may install a fake backend. API handlers must not construct
  backend-specific command execution paths directly.
- `run/apply` also receives host-open behavior through a configured opener
  factory. The local `hideout ui` server wires this to the same isolated
  browser/file opener used by CLI `run`; API handlers must not invent a second
  host-open path or silently no-op registered command proxies.

## API Boundary

Manager API must not expose:

- broker tokens;
- proxy secret values;
- raw private key material;
- unrestricted host command execution;
- arbitrary file read/write endpoints;
- backend-specific privileged handles.
- raw bundle files before verification.
- executable init scripts from bundles or projects.

Manager API may expose:

- redacted SecretRef presence;
- audit events;
- profile policy;
- session status;
- environment status;
- backend capability status;
- HostFS requested paths and rule IDs;
- bundle permission diffs;
- project manifest status;
- bundle verification status;
- init plans, init task status, and redacted init results;
- doctor remediation suggestions.

Current API v1 init and run resources:

```text
POST /api/v1/init/plan
  Input: InitAPIRequest with profile, backend, network, machine-setup inputs,
  expected-command diagnostic declarations, and an optional guest base image
  declaration.
  Output: InitPlan with typed InitTasks and structured nextSteps.
  Authority: planning only; no profile mutation, helper build, backend prepare,
  broker, HostFS service, package install, or host command execution.

POST /api/v1/init/apply
  Input: InitAPIRequest.
  Output: InitResult with the same plan/nextSteps shape.
  Authority: applies the same PlanInit -> ApplyInit chain as CLI init and
  doctor fix. It may create store/profile state and write helper-artifact
  install metadata. It runs without an interactive prompt channel in API v1,
  so confirmation-required tasks fail closed.

POST /api/v1/run/plan
  Input: RunAPIRequest with profile, backend, workspace, network override,
  environment reuse flags, and command argv.
  Output: RunPlan.
  Authority: planning only; no backend prepare, no broker, no HostFS service.

POST /api/v1/run/apply
  Input: RunAPIRequest.
  Output: RunResult.
  Authority: executes the same PlanRun -> ApplyRun chain as CLI after the local
  Manager server supplies a backend factory. The response may include session,
  profile, backend, environment, instance, audit path, command, and redacted
  error text. It must not include broker tokens, broker socket paths, proxy
  secret values, raw helper search paths, or arbitrary host file contents.

GET /api/v1/run/status
  Input: optional session query filter.
  Output: session summaries from Manager overview.
  Authority: observation only. It may report audit/session paths and presence
  booleans such as `hasBrokerEndpoint` and `hasProxySecretFile`, but it must
  not return broker tokens, broker socket contents, proxy URLs, proxy secret
  values, or file-read handles.

POST /api/v1/environment/stop/plan
POST /api/v1/environment/clean/plan
  Input: optional environment IDs, idle filter, and stopped-only filter.
  Output: EnvironmentActionPlan with explicit targets and skipped entries.
  Authority: planning only; no backend stop, no instance deletion, no store
  mutation.

POST /api/v1/environment/stop/apply
POST /api/v1/environment/clean/apply
  Input: same as the matching plan endpoint.
  Output: EnvironmentActionResult with the applied plan, applied targets, and
  skipped entries.
  Authority: applies the same reusable environment store and Lima lifecycle
  operations as CLI stop/clean. Apply revalidates each target under the
  environment lock. It may stop a Lima instance, delete a Lima instance for
  clean, and update or remove environment records. It must not expose arbitrary
  VM commands, broker tokens, proxy secret values, or host file handles.
```

The environment lifecycle endpoints above also describe the current
implemented surface accurately. As the API stabilizes they converge to a
single environment lifecycle plan/apply pair carrying an `action` field
(`stop` or `clean`), with the same target/skipped/apply model and authority
bounds.

The init API is intentionally not a generic profile-write endpoint. It exposes
only typed init tasks and expected-command diagnostic fields validated by the
profile schema. The run API is intentionally not a generic `host.exec` endpoint. Native
backend execution still requires explicit weak-isolation acknowledgement and is
audited as a backend selection decision.

## Daemon Role

The steady-state architecture is daemon-first, in the Docker model: `hideoutd`
is the resident Manager runtime, and CLI, TUI, and WebUI are its clients.

```text
hideoutd
  resident Manager runtime, environment registry owner, and event hub

hideout CLI / TUI / WebUI
  authenticated clients over protected local transport
```

The daemon makes observation and control continuous across CLI invocations. It
owns:

- the environment registry: the shared `default` environment and explicitly
  named environments;
- live session and environment state;
- redacted event streams for TUI/WebUI;
- in-memory audit filtering for event streams (there is no separate indexed
  audit store; see
  [tui-webui-experience.md](tui-webui-experience.md));
- fail-closed handling for confirmation-required operations; interactive
  prompt channels are later work;
- background execution for existing typed environment stop/clean operations;
- local Manager API serving.

The daemon stays in single-operator form: one operator token with full access.
Read-only tokens, client role matrices, per-operation authorization grades,
delegated approval protocols, and per-subscriber redaction tiers are enterprise
shapes and are out of scope for the current implementation.
Approval means the operator confirming an action interactively on their own
machine, and every confirmation is recorded in audit.

The daemon is not a new authority layer. It must call the same Manager Core
plan/apply operations as CLI, TUI, and WebUI. It must not expose arbitrary host
execution, raw VM commands, broker tokens, proxy secret values, host file
handles, or a raw profile writer. Per-run authority remains session-scoped and
must be regenerated for each `hideout run` even when `hideoutd` is already
running.

The daemon introduces no tool installation channel: guest tools come from the
declared base image or from operator-authored setup run inside the boundary.

Daemon transport is security-sensitive:

- Preferred transport is a Unix domain socket under a runtime subdirectory of
  the effective Hideout store root, with `0700` ancestors. Anchoring the socket
  under the store lets the existing store-reserved HostFS guard and workspace
  mount safety guard enforce guest unreachability. The socket path must never
  live under the workspace, HostFS grants, passthrough mounts, or any path
  visible to the guest.
- Host loopback is not a sufficient trust boundary for the daemon API. A
  loopback HTTP listener may be used for a command-scoped browser UI only when
  protected by a short-lived token; it must not be the default long-lived
  daemon authority transport.
- Every client must authenticate with the operator token. OS peer credentials on
  the Unix socket are a useful additional check,
  but they are not sufficient by themselves for weak native-backend scenarios
  where the target and operator share the host UID; path unreachability and
  tokens still apply.
- The guest must not learn daemon socket paths, daemon tokens, or UI tokens.
  Endpoint exposure and PortBridge providers must not expose the daemon API
  back into the guest.

After a daemon restart, the daemon fails closed: live resources that cannot be
proved owned by the current instance are reported and audited as orphaned. The
daemon does not silently re-adopt or destroy them.

`hideoutd` is implemented as the resident steady-state Manager runtime, while
embedded CLI/TUI/WebUI paths remain supported when no daemon is running.

## CLI, TUI, and WebUI Roles

### CLI

Best for:

- scripting;
- direct command execution;
- quick profile commands;
- CI and tests;
- explicit flags.

CLI should remain thin over Manager Core for complex operations.

### TUI

Current TUI smoke surface is a read-only terminal dashboard over Manager
overview and redacted audit data. The product role is a persistent operator
window that can stay open while another terminal runs an agent or CLI. It is
best for:

- local monitoring;
- session/environment overview;
- recent denied paths;
- network status;
- init next steps.
- per-profile expected-command diagnostics and command-proxy state with CLI
  setup hints.

The TUI stays the lightweight pane: side-by-side panels with keyboard
shortcuts for audit observation and session management. Future TUI increments
can add keyboard navigation, selectable sessions and audit rows, first-run
initialization, interactive doctor, and helper repair apply flows; HostFS
rule management belongs to the WebUI. They must still call Manager Core operations instead of mutating
stores or backends directly.

Suggested command:

```bash
hideout tui
hideout tui --profile <name>
hideout tui --once
hideout doctor
```

Bubble Tea is a good fit because it keeps the management experience inside the
terminal, uses Go, and can share process-level code with Hideout.

### WebUI

Current WebUI smoke surface is a local Manager API client for overview,
audit/resource summaries, expected-command diagnostics, controlled run
plan/apply, and reusable environment stop/clean plan/apply. The WebUI is the
fuller management surface. It is best for:

- audit search and filtering;
- policy editing;
- environment management;
- basic operations that benefit from visual review;
- larger visual explanations;
- onboarding.

Future WebUI increments deepen that management role: full policy editing,
environment management, and richer audit search and session detail views.

WebUI should call Manager API and must not implement separate policy logic.

## Local API Security

Current implemented exposures:

```text
Unix socket under the Hideout runtime state directory for hideoutd authority.
127.0.0.1 with a short-lived random token for command-scoped or daemon-served WebUI.
```

Rules:

- no unauthenticated remote listener;
- short-lived browser UI tokens;
- audit all authority-changing operations;
- Hideout-minted control-plane credentials are never included in API
  responses; user/application data in local authenticated views follows the
  deterministic redaction contract in
  [privacy-run-design.md](privacy-run-design.md);
- manager socket path lives under Hideout runtime state.
- typed command-proxy mutation endpoints are limited to `host.open` command
  symbol registration and must not accept host command paths, provider code, or
  raw profile JSON.

## State Ownership

Profile Store owns durable user policy and identity defaults.

Environment Store owns reusable backend state and warm runtime metadata.

Session Store owns one command execution, audit file, broker endpoint metadata,
and temporary authority.

Bundle Store owns immutable installed bundle versions and verification metadata.

Project Store owns discovered project manifests, project lock state, and
project apply plans.

Trust Store owns source trust, checksums, future signatures, and local trust
overrides.

Init Task Engine owns first-run setup, `doctor --fix`, helper discovery or
installation, schema/version metadata repair, backend preparation, project
bootstrap, and other typed initialization tasks. It does not execute arbitrary
shell scripts.

Manager Core owns cross-resource consistency. Individual modules should not
mutate unrelated stores directly.

## Phase Plan

### Current

- Manager packages exist;
- CLI remains the primary user surface;
- `hideout tui` exists as a read-only persistent smoke dashboard over Manager
  overview, including per-profile expected-command diagnostic and
  command-proxy visibility with CLI setup hints. `--once` is the
  script/package-smoke snapshot mode.
- `hideout ui` exists as a local WebUI smoke/operations surface backed by
  Manager API.
- Manager overview exposes initial init, bundle, and project status summaries;
- InitTask has a minimal machine setup engine used by `hideout init` and
  `doctor --fix` through Manager Core.
- Init summary exposes init audit path and event count; the log itself remains a
  typed `hideout.init-audit/v1` JSONL file under the store logs directory.
- Manager API exposes minimal run plan/apply/status resources over the local
  token-protected server.
- Manager API and WebUI expose typed command-proxy plan/apply for `host.open`
  command symbols.
- Manager API and WebUI expose typed profile HostFS rule plan/apply for durable
  HostFS allow/deny rules.
- Manager API and WebUI expose typed profile env policy plan/apply for durable
  public/inherit/deny env policy without echoing env values.
- A resident `hideoutd` is implemented; CLI, TUI, and WebUI still support
  embedded Manager Core or the local WebUI server process when no daemon runs.

### Implemented Resident Runtime

- `hideoutd` is the steady-state resident Manager runtime (see
  [STATUS.md](STATUS.md)): it mounts the existing typed Manager API over a
  store-rooted, guest-unreachable Unix socket (parity by construction), serves a
  typed redacted event stream from real operation sources (environment stop/clean
  lifecycle, daemon-mediated run sessions, daemon background work, daemon audit
  tail, `evidence/export/apply`, and existing run session cleanup; no durable
  log; streams end on credential expiry), runs existing typed environment
  stop/clean as background work with queryable status, and fails closed after
  restart for live resources it cannot prove it owns. Its own status/event and
  background endpoints are a separate surface outside `/api/v1/`.
  Confirmation prompt channels remain a follow-on (the daemon fails closed for
  confirmation-required ops rather than prompting). It also serves the WebUI over
  a tokened loopback UI transport; WebUI and TUI seed once from Manager data and
  then apply `liveconsole.Event` payloads without steady-state overview/audit
  polling while the stream is healthy. CLI, TUI, and WebUI are all its clients.

### Next Product Increment

- formal Manager resource schema;
- expand InitTask resource schema and plan/apply operations beyond machine
  setup;
- plan/apply operations for HostFS, network, OpenTarget, and broader
  profile/config mutations beyond typed command-proxy registration;
- plan/apply operations for bundle install confirmation and project apply;
- TUI first-run wizard and interactive doctor;
- richer WebUI audit/session/profile views beyond the current smoke surface;
- ensure CLI paths call Manager Core for shared operations.

### Later

- richer interactive confirmation flows through the daemon prompt channel.

## Open Questions

- Which remaining CLI-direct operations require typed Manager plan/apply before
  product release?
