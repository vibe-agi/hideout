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
Session
HostFSRule
HostFSOverlay
NetworkPlan
AccessSensor
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
InstallTask, as an InitTask subtype for artifact placement
BundleSource
Bundle
BundleVersion
BundleReference
BundleEntrypoint
BundlePermission
Recipe
ProjectManifest
ProjectLock
ProjectApplyPlan
CompatibilityReport
VerificationReport
TrustPolicy
ExportPlan
RedactionRule
```

CapabilityAdapter means a Manager-visible script adapter reference: entrypoint,
bundle/version, declared permissions, required Core primitives, and risk labels
for a domain workflow. It is not a backend adapter and does not execute
authority directly. Backend adapters remain Go-owned backend substrate
integrations.

AccessSensor means the Later observation plane for guest filesystem, process, or
network probes. It is a reporting and warning resource, not an authorization
mechanism. Manager should use this term instead of defining separate
filesystem/network sensors unless a future design intentionally splits them.

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

PlanBundleEnable(profile, bundleRef) -> PolicyChangePlan
ApplyBundleEnable(planId) -> BundleReference

PlanProjectApply(projectPath) -> ProjectApplyPlan
ApplyProjectApply(planId) -> ProjectManifest

PlanBundleExport(profile, options) -> ExportPlan
ApplyBundleExport(planId) -> ExportResult
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
- Manager API exposes the minimal run surface: `POST /api/v1/run/plan`,
  `POST /api/v1/run/apply`, and `GET /api/v1/run/status`.
- Manager API exposes the minimal init/tool setup surface:
  `POST /api/v1/init/plan` and `POST /api/v1/init/apply`.
- Manager API exposes controlled reusable environment lifecycle actions:
  `POST /api/v1/environment/stop/plan`,
  `POST /api/v1/environment/stop/apply`,
  `POST /api/v1/environment/clean/plan`, and
  `POST /api/v1/environment/clean/apply`.
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
- compatibility and verification reports;
- init plans, init task status, and redacted init results;
- doctor remediation suggestions.

Current API v1 init and run resources:

```text
POST /api/v1/init/plan
  Input: InitAPIRequest with profile, backend, network, tool presets, and
  user-declared npm global tools.
  Output: InitPlan with typed InitTasks and structured nextSteps.
  Authority: planning only; no profile mutation, helper build, backend prepare,
  broker, HostFS service, package install, or host command execution.

POST /api/v1/init/apply
  Input: InitAPIRequest.
  Output: InitResult with the same plan/nextSteps shape.
  Authority: applies the same PlanInit -> ApplyInit chain as CLI init and
  doctor fix. It may create store/profile state, write install metadata, and
  update profile tool supply policy. It runs without an interactive prompt
  channel in API v1, so confirmation-required tasks fail closed.

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

The init API is intentionally not a generic profile-write endpoint. It exposes
only typed init tasks and generic tool-supply fields validated by the profile
schema. The run API is intentionally not a generic `host.exec` endpoint. Native
backend execution still requires explicit weak-isolation acknowledgement and is
audited as a backend selection decision.

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

Best for:

- first-run initialization;
- local monitoring;
- interactive doctor;
- session/environment overview;
- HostFS grants and recent denied paths;
- network status;
- install tasks.

Suggested command:

```bash
hideout tui
hideout doctor --interactive
hideout init
```

Bubble Tea is a good fit because it keeps the management experience inside the
terminal, uses Go, and can share process-level code with Hideout.

### WebUI

Best for:

- audit search and filtering;
- policy editing;
- larger visual explanations;
- OpenTarget and port topology;
- onboarding;
- session timeline.

WebUI should call Manager API and must not implement separate policy logic.

## Local API Security

Default API exposure:

```text
Unix socket on macOS/Linux
or 127.0.0.1 with random token for browser UI
```

Rules:

- no unauthenticated remote listener;
- short-lived browser UI tokens;
- audit all authority-changing operations;
- sensitive values are redacted before API response;
- manager socket path lives under Hideout runtime state.

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
- UI command exists as an initial surface.
- Manager overview exposes initial init, bundle, and project status summaries;
- InitTask has a minimal machine setup engine used by `hideout init` and
  `doctor --fix` through Manager Core.
- Init summary exposes init audit path and event count; the log itself remains a
  typed `hideout.init-audit/v1` JSONL file under the store logs directory.
- Manager API exposes minimal run plan/apply/status resources over the local
  token-protected server.

### Next Product Increment

- formal Manager resource schema;
- expand InitTask resource schema and plan/apply operations beyond machine
  setup;
- plan/apply operations for HostFS, network, OpenTarget, and profile/config
  mutations;
- plan/apply operations for bundle install, bundle enable, project apply, and
  bundle export;
- TUI first-run and monitoring surface;
- WebUI read-only audit/session/profile viewer;
- ensure CLI paths call Manager Core for shared operations.

### Later

- multi-user local policy;
- team policy sync;
- remote manager;
- prompt/approval workflows.

## Open Questions

- Should Manager run as an explicit daemon or in-process per command first?
- Which operations require plan/apply before product release?
- Should TUI be the default first-run experience?
- What is the smallest read-only WebUI worth shipping?
