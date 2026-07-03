# Ecosystem Foundation Design

<!-- markdownlint-disable MD013 -->

## Contract

This document defines the bottom-layer architecture needed before Hideout grows
a public ecosystem of bundles, project manifests, presets, scripts, and
community recipes.

It follows:

- [architecture-principles.md](architecture-principles.md)
- [init-task-architecture.md](init-task-architecture.md)
- [manager-control-plane.md](manager-control-plane.md)
- [policy-config-supply-chain.md](policy-config-supply-chain.md)

The purpose is to make ecosystem support a stable runtime contract, not a later
UI or marketplace feature. Bundles, Hideoutfile, TUI, WebUI, and future registry
features must compile into the same Manager resources and effective policy
model.

## Design Goal

Hideout should support a community ecosystem without allowing community
artifacts to become hidden authority.

The bottom layer must support:

- local and Git-installed bundles;
- project manifests that are safe to commit;
- permission review before authority changes;
- schema validation and compatibility checks;
- lock files and reproducible installs;
- constrained goja scripts;
- install, update, rollback, and audit;
- future TUI/WebUI views without duplicating policy logic.

The user-facing ecosystem can evolve later. The architecture cannot rely on a
future marketplace to enforce privacy boundaries.

## Non-Negotiable Ecosystem Principles

### 1. Artifacts Declare, Manager Applies

Ecosystem artifacts are declarations. They do not directly mutate runtime
authority.

```text
Bundle / Hideoutfile / Recipe / Template
        |
        | parse, validate, verify
        v
InstallPlan / ProjectPlan / PermissionDiff
        |
        | explicit apply
        v
Manager resources
        |
        v
Effective policy
```

A bundle install is not a policy enable. A project manifest discovery is not a
profile mutation. A script package is not an arbitrary plugin.

### 2. Install Is Separate From Enable

Install places a verified artifact into the local bundle store. Enable links a
bundle version or recipe into a profile or project scope.

This separation keeps update and rollback understandable:

```text
installed bundle version
  may be inspected, verified, updated, or removed

enabled bundle reference
  participates in effective policy composition
```

### 3. Project Manifests Are Git-Safe

`Hideoutfile` is a project-level suggestion and constraint file. It is not a
profile dump and must not contain local identity state.

Allowed:

- bundle references;
- required Hideout capability versions;
- workspace-relative policy suggestions;
- template input declarations;
- project-specific denied paths inside the workspace;
- network or OpenTarget recommendations.

Disallowed by default:

- profile identity material;
- local absolute host paths, except explicit examples;
- real proxy URLs with credentials;
- secret values;
- session state;
- audit history;
- HostFS overlay contents.

### 4. Permissions Are Typed And Reviewable

Every bundle permission must map to a known subsystem and action shape.

Examples:

```text
command.decide
command.fake.select
audit.redact
hostfs.template
opentarget.template
network.recommend
doctor.check
project.requirement
init.requirement
toolPreset.check
```

Disallowed:

```text
host.exec
host.shell
hostfs.raw_mount
network.raw_route
manager.raw_mutation
script.unrestricted
bundle.installScript
project.initScript
```

Permissions are capabilities to propose policy. They are not direct runtime
grants unless a Manager plan explicitly enables them.

### 5. Scripts Are Extension Points, Not Packages

Goja scripts inside bundles can only run through registered entrypoints.

The script runtime must provide:

- fixed input schema per entrypoint;
- fixed output schema per entrypoint;
- timeout and interrupt support;
- panic recovery;
- no filesystem, network, process, timer, or module APIs;
- no mutable global state shared across users or profiles;
- validator-owned final decisions.

Scripts may propose decisions. Builtin Go validators and subsystem policies
remain the authority.

### 6. Adapters Are Composition Units, Not Authority

Bundles may ship JavaScript adapters for domain workflows.

An adapter maps product intent into Hideout capability proposals. Examples:

- preview adapter for H5 dev servers;
- browser-control adapter for an isolated browser profile;
- adb adapter for controlled access to a host adb server endpoint;
- simulator adapter for mobile deep links;
- command compatibility adapter for a known tool.

Adapters may use only the constrained script SDK described in
[script-extension-architecture.md](script-extension-architecture.md). They may
classify input, choose templates, construct proposals, add audit tags, and
redact presentation fields. They must not read host state, open network
connections, spawn processes, mutate profiles, allocate ports, or execute
capabilities.

Persona recipes compose adapters with policy templates, environment hints,
doctor checks, and sensitive-path deny templates. They are the community-facing
unit for H5, Android, iOS-assisted, backend, and AI-agent workflows.

### 7. Runtime Evidence Cannot Be Deleted By Ecosystem Code

Bundles may provide audit redaction rules for export or presentation. They must
not remove core runtime evidence from local audit records.

Non-removable evidence includes:

- requested HostFS path;
- decision;
- rule ID;
- rule source;
- command proxy action;
- OpenTarget target type;
- PortBridge lifecycle event;
- network mode and leak check result;
- bundle version that contributed a policy decision.

### 8. Phase 1 Bundles Do Not Ship Executables

Phase 1 bundles may contain:

- JSON manifests;
- schemas;
- goja scripts;
- JavaScript adapters that use registered entrypoints;
- persona recipes;
- docs;
- test fixtures;
- examples.

Phase 1 bundles must not contain:

- executable binaries;
- dynamic libraries;
- shell install scripts;
- backend helper binaries;
- auto-run hooks outside registered goja entrypoints.

This can be revisited only with a new design contract, signature model, and
release gate.

### 9. Init Requirements Are Declarative

Bundles and project manifests may declare initialization requirements, setup
checks, package hints, or tool preset expectations. They must not ship
executable initialization scripts.

Allowed:

```text
requires helper hideout-shim-linux-arm64 >= 0.1.0
requires backend capability hostfs.v1
recommends network mode tun2socks
checks guest command git
suggests project Hideoutfile template
```

Disallowed:

```text
run this shell script on first install
run npm install -g
run brew install
run curl | sh
modify profile grants automatically
```

Manager compiles requirements into an InitPlan. The user applies the plan when
it changes authority.

## Layered Architecture

```text
Source Resolver
  local dir / tarball / git / github shorthand
        |
        v
Artifact Verifier
  schema / checksum / forbidden file scan / secret scan / compatibility
        |
        v
Bundle Store
  immutable installed versions
        |
        v
Permission Analyzer
  permissions / entrypoints / templates / risk labels
        |
        v
Manager Plan
  install / enable / project apply / init / update / rollback / export
        |
        v
Policy Compiler
  defaults + bundle refs + project + profile overrides + run flags - deny
        |
        v
Effective Policy
  HostFS / Command Proxy / Network / OpenTarget / Audit / Doctor
```

No ecosystem component can bypass the Policy Compiler or Manager plan/apply.

## Core Components

### Source Resolver

Resolves artifact sources into a local staged directory.

Supported early sources:

```text
local directory
tarball
git URL
github:owner/repo/path@ref
```

The resolver records:

- source string;
- resolved commit or digest;
- fetch time;
- source type;
- trust hint;
- local staging path.

The resolver must not apply policy.

### Artifact Verifier

Validates a staged artifact before it enters the bundle store.

Required checks:

- manifest schema;
- apiVersion compatibility;
- entrypoint file existence;
- script size limit;
- permission declaration completeness;
- forbidden file scan;
- secret-looking value scan;
- local absolute path scan;
- checksum verification when lock data exists.

The verifier produces `CompatibilityReport` and `VerificationReport`.

### Bundle Store

Stores immutable bundle versions.

```text
~/.hideout/bundles/<publisher>/<name>/<version>/
```

Rules:

- installed versions are content-addressed or checksum recorded;
- updates install a new version instead of mutating the old one;
- rollback changes enabled references, not stored content;
- local edits to installed bundle content make verification fail.

### Permission Analyzer

Turns bundle declarations into a permission diff that can be shown by CLI, TUI,
or WebUI.

Permission diff should show:

- new entrypoints;
- changed entrypoints;
- HostFS templates;
- OpenTarget templates;
- network recommendations;
- command proxy hooks;
- audit redaction hooks;
- doctor checks;
- declared inputs;
- risk labels.

### Policy Compiler

Compiles all policy sources into one effective policy.

Order:

```text
Hideout defaults
  + installed bundle defaults referenced by profile or project
  + project Hideoutfile requirements
  + profile local overrides
  + run flags
  - deny and hard-deny
  = effective policy
```

Deny and hard-deny are subtractive. No bundle can override them.

### Script Runtime

Runs constrained goja entrypoints.

The runtime must be owned by the Policy Engine, not by individual subsystems.
Subsystems ask the Policy Engine for decisions and receive validated results.
SDK classes, delivery phase, and runtime restrictions are defined in
[script-extension-architecture.md](script-extension-architecture.md). The
runtime must not expose raw Go standard library packages or subsystem handles.

### Trust Store

Tracks where installed artifacts came from and what verification was performed.

Trust levels:

```text
local
git-unverified
verified-checksum
signed
official
```

Trust affects warnings and review UX. It does not bypass runtime validation.

## Domain Resources

Manager must model ecosystem state as resources.

```text
BundleSource
Bundle
BundleVersion
BundleInstall
BundleReference
BundleEntrypoint
BundlePermission
Recipe
InitRequirement
InitPlan
ProjectManifest
ProjectLock
ProjectApplyPlan
CompatibilityReport
VerificationReport
TrustPolicy
ExportPlan
RedactionRule
```

Each resource needs:

```text
id
kind
apiVersion
status
createdAt
updatedAt
ownerProfile, when relevant
ownerProject, when relevant
sourceRef, when relevant
checksum, when relevant
capabilityBoundary, when relevant
```

## Hideoutfile Contract

`Hideoutfile` is the project manifest. It is safe to commit by design.

Preferred shape:

```json
{
  "apiVersion": "hideout.project/v1",
  "kind": "ProjectManifest",
  "requirements": {
    "hideout": ">=0.1.0 <0.2.0",
    "capabilities": [
      "hostfs.v1",
      "opentarget.host_open"
    ]
  },
  "initRequirements": [
    {
      "kind": "backendCapability",
      "id": "hostfs.v1"
    }
  ],
  "bundles": [
    {
      "source": "github:vibe-agi/hideout-bundles/web-agent-safe@1.0.0",
      "name": "web-agent-safe"
    }
  ],
  "projectPolicy": {
    "network": {
      "mode": "direct",
      "warning": "network identity visible"
    },
    "hostfsTemplates": [
      {
        "bundle": "web-agent-safe",
        "id": "project-docs",
        "inputs": {
          "path": "${workspace}/docs/*.md"
        }
      }
    ]
  }
}
```

Avoid a top-level `profile` field in project manifests. The project can request
policy, but it does not own the user's profile.

## Lock And Local State

Commit-safe project lock:

```text
hideout.lock.json
```

May contain:

- bundle source;
- resolved ref;
- digest;
- compatibility result;
- schema version.

Must not contain:

- install time;
- local cache path;
- user approval history;
- private trust override;
- local profile ID;
- local absolute paths unless part of the committed project.

Local install state belongs under the Hideout store, not the project.

```text
~/.hideout/install-state.json
```

## Sequence: Bundle Install

```mermaid
sequenceDiagram
  participant U as User
  participant M as Manager
  participant R as SourceResolver
  participant V as ArtifactVerifier
  participant S as BundleStore
  participant A as Audit

  U->>M: bundle install <source>
  M->>R: resolve source
  R-->>M: staged artifact + resolved digest
  M->>V: verify artifact
  V-->>M: verification report + permission summary
  M-->>U: install plan
  U->>M: apply install plan
  M->>S: store immutable version
  M->>A: audit bundle.install
```

Install does not enable the bundle for any profile unless the plan explicitly
contains an enable step.

## Sequence: Project Apply

```mermaid
sequenceDiagram
  participant U as User
  participant M as Manager
  participant P as ProjectManifest
  participant C as PolicyCompiler
  participant A as Audit

  U->>M: project diff
  M->>P: load Hideoutfile
  M->>C: compile proposed policy
  C-->>M: permission diff + conflicts
  M-->>U: project apply plan
  U->>M: apply project plan
  M->>A: audit project.apply
```

Project apply can link bundle references and project requirements. It must not
silently write local profile identity or secrets.

## Sequence: Init Requirement

```mermaid
sequenceDiagram
  participant U as User
  participant M as Manager
  participant P as ProjectManifest
  participant I as InitTaskEngine
  participant A as Audit

  U->>M: project diff
  M->>P: load initRequirements
  M->>I: plan typed InitTasks
  I-->>M: InitPlan + risk labels
  M-->>U: permission and setup diff
  U->>M: apply approved plan
  M->>A: audit init.apply
```

No executable init body is loaded from the project or bundle.

## Sequence: Runtime Policy Use

```mermaid
sequenceDiagram
  participant T as Target Command
  participant G as Guest Shim
  participant B as Broker
  participant P as Policy Engine
  participant A as Audit

  T->>G: open /host/path
  G->>B: HostFS request
  B->>P: decide with effective policy
  P-->>B: allow or deny + rule source
  B->>A: audit decision
  B-->>G: result
```

The bundle that contributed a decision is recorded as rule source. The target
command never talks to bundle scripts directly.

## Export Safety Matrix

`hideout bundle export --from-profile` must be conservative.

| Data | Default export | Reason |
| --- | --- | --- |
| Bundle references | allow | Shareable dependency graph |
| Command policy templates | allow | Reviewable policy |
| HostFS templates with variables | allow | No local path leak |
| Resolved local HostFS grants | reject | Host path leak |
| Local absolute paths | reject or example-only | Machine identity leak |
| Profile identity | reject | Private identity |
| SecretRef names | redact | May reveal setup |
| Secret values | reject | Credential leak |
| Audit logs | reject unless sanitized | Behavior leak |
| HostFS overlay contents | reject | Host file content leak |
| Usage-derived allow/deny | reject by default | Private behavior |

Export must produce a report describing rejected and redacted fields.

## Failure Behavior

Ecosystem operations fail closed when:

- schema is unknown;
- compatibility is outside supported range;
- permission is undeclared;
- init requirement contains an executable body;
- entrypoint file is missing;
- script exceeds limits;
- forbidden file type is present;
- lock checksum mismatches;
- project manifest attempts to mutate profile identity;
- a bundle asks for unsupported raw host authority.

Warnings are acceptable for trust level, not for missing privacy contracts.

## Release Gates

Ecosystem foundation needs tests before public bundle usage:

- schema validation for bundle and project manifests;
- forbidden file and secret scan fixtures;
- install plan/apply separation;
- enable plan/apply separation;
- lock checksum mismatch fail-closed;
- project manifest cannot mutate profile identity;
- export redaction matrix tests;
- permission diff contains every subsystem boundary change;
- scripts cannot access filesystem, network, process, timers, or modules;
- bundle and project init requirements compile into typed InitTasks, not
  scripts;
- effective policy records bundle source in audit decisions.

## Phase Plan

### Phase 1 Foundation

- document ecosystem resource model;
- define bundle and Hideoutfile schemas;
- keep bundles config/script/docs/tests only;
- keep install separate from enable;
- keep init requirements declarative;
- route all ecosystem state through Manager resources;
- define export safety matrix.

### First Product Increment

- `hideout bundle install/list/verify/remove`;
- local and Git source resolver;
- bundle store;
- project diff/apply;
- permission review output;
- lock file;
- basic TUI bundle/status views.

### Later

- signed bundles;
- official registry;
- team policy server;
- marketplace;
- binary extension model, only after a separate security design;
- automated compatibility farm.
