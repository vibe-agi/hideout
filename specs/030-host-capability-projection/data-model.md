# Phase 1 Data Model: Host Capability Projection

<!-- markdownlint-disable MD013 MD060 -->

Domain entities and their validation rules. Concrete Go types live in `internal/hostcap` (Core-owned) and `internal/cmdgrammar` (authority-free parsing). All fields that an untrusted layer can influence carry an explicit Go-side validator.

## CapabilityDescriptor (Core-owned, static)

The static description of one host capability. The registry of descriptors is the authority surface; it is not runtime-extensible.

| Field | Type | Rules |
|-------|------|-------|
| `ID` | string | Stable, dotted (`host.app.open-resource`). Unique in registry. |
| `RiskClass` | enum | `low` \| `elevated` \| `high`. Drives `DecisionPolicy`. `host.app.open-resource` safe mode = `low`; trusted mode = `high`. |
| `IntentSchema` | string | Schema id the provider consumes (`open-resource-intent/v1`). |
| `ResourceKinds` | []enum | Subset of `workspace` \| `hostfs` \| `guest-only` \| `url` \| `endpoint` \| `device`. v1 `host.app.open-resource` = `[workspace]`. |
| `ResultPolicy` | enum | `none` \| `bounded-typed` \| `stream` \| `lease`. `host.app.open-resource` = `none`. |
| `ProviderRef` | string | Names a Go provider function in the registry. Must resolve. |
| `DecisionPolicy` | enum | `default-allow-audited` \| `operator-grant`. Safe mode = `default-allow-audited`; trusted mode = `operator-grant`. |
| `LifecyclePolicy` | enum | `session` \| `run`. Grants/leases bound to and revoked at this lifecycle. |
| `Platforms` | []enum | Host platforms the descriptor is available on (`darwin`, later `linux`). |
| `Status` | enum | `implemented` \| `design-ready`. v1: `host.app.open-resource` = `implemented`; adb/applescript entries = `design-ready` (present in the model, refused at dispatch). |

**Validation**: registry load fails closed if any descriptor has an unknown enum, an unresolved `ProviderRef`, a duplicate `ID`, or a `design-ready` descriptor reached at dispatch.

## CommandBinding

Maps guest command name(s) to an adapter/grammar and the capabilities it may propose. Recipes are bindings, not new capabilities. Extends the existing `cmdproxy.Registration`.

| Field | Type | Rules |
|-------|------|-------|
| `Name` | string | Guest command name (`code`). |
| `Aliases` | []string | Optional (`code-insiders` NOT in v1). |
| `Action` | string | `host.app.open-resource`. |
| `GrammarID` | string | `code-open-v1` (declarative) or an adapter id (goja). |
| `AllowedCapabilities` | []string | Descriptor ids this binding may propose. Must be a subset of the registry. |
| `AllowedTargets` | []enum | `workspace-file`, `workspace-dir`. |
| `OwnerType` | string | `host-app-projection`. |

**Validation**: `Action`/`AllowedCapabilities` must exist in the registry; unknown command name at broker time → `projection.command.unbound` fail-closed.

## Command grammar (authority-free) — `code-open-v1`

Parses guest argv into an `OpenResourceIntent`. Zero host authority; unknown flags denied.

| Element | Mapping | Rules |
|---------|---------|-------|
| positional args | `resources[]` (kind `workspace`) | `.` → workspace root; relative/`/workspace/...` paths → workspace ResourceRef. Absolute non-workspace / guest-only → `no-host-mapping`. |
| `-g <file:line:col>` | `resources[0]` + `location{line,column}` | `file` parsed as workspace resource; `line`,`column` positive ints. |
| `-n`, `--new-window` | `windowMode = new` | |
| `-r`, `--reuse-window` | `windowMode = reuse` | default `reuse` |
| any other flag | — | **denied** (`projection.flag.unrecognized`) |

**Validation**: grammar output is a proposal only; Go re-decodes it (below). The grammar never emits a host path or raw argv.

## OpenResourceIntent (`open-resource-intent/v1`)

The app-agnostic structured request the Core capability consumes. Go strictly decodes (unknown fields rejected) and field-validates before the provider acts.

| Field | Type | Rules |
|-------|------|-------|
| `AppRef` | string | Stable app id; MUST resolve in the app-identity registry. No binary path / bundle id accepted here. |
| `Resources` | []ResourceRef | ≥1; each validated (below). |
| `Location` | {Line:int, Column:int}? | Optional; both ≥1 when present; applies to `Resources[0]`. |
| `WindowMode` | enum | `reuse` \| `new`. Default `reuse`. |

**Validation**: reject unknown fields; `AppRef` must resolve; every `ResourceRef` must be a workspace kind that maps to a host path within the session workspace root; `Location` ints positive; `WindowMode` in enum. Any failure → typed refusal, nothing opens.

## ResourceRef (workspace)

The guest/relative view of a workspace resource. Only Core maps it to a host path.

| Field | Type | Rules |
|-------|------|-------|
| `Kind` | enum | `workspace` (v1). `hostfs`/`guest-only`/`url`/`endpoint`/`device` reserved. |
| `GuestPath` | string | `/workspace/...` (alias) or the guest workspace path. |
| `RelativePath` | string | Path relative to the workspace root; no `..` escape. |

**Validation**: `RelativePath` must not escape the workspace root; Core resolves `HostRoot`/`RelativePath`, re-checks symlink escape (`EvalSymlinks`), and confirms existence. Escapes / guest-only → `projection.path.no-host-mapping`. Host path never serialized back to guest/adapter/event.

## AppIdentityEntry (Core/package-owned)

Maps a stable app id to the host application, per platform. Referenced by id only from untrusted layers.

| Field | Type | Rules |
|-------|------|-------|
| `AppRef` | string | Stable id (`vscode`). |
| `Platform` | enum | `darwin` (v1). |
| `Resolver` | descriptor | How Core resolves and verifies a fixed host application identity. For VS Code on macOS this is a package-owned bundle candidate set, executable-relative path, and designated code-signing requirement; ambient `PATH` is never consulted. Not operator/JS-supplied. |
| `LaunchProfile` | descriptor | Safe-mode launch parameters (isolated user-data-dir, disable-extensions, no auto-tasks, trust kept on). |

**Validation**: `AppRef` must exist; unresolved host app → `projection.app.absent`; resolver mismatch/drift → `projection.app.identity-drift`. Both fail closed.

## IdeMode (per profile, control-plane state)

| Field | Type | Rules |
|-------|------|-------|
| `Profile` | string | Owning profile. |
| `Mode` | enum | `safe` (default) \| `trusted-host-ide`. |
| `GrantRef` | string? | For `trusted-host-ide`: the decision-center grant record id. |

**Validation**: `trusted-host-ide` requires a live `GrantRef`; stored only in guest-unreachable control-plane state keyed by workspace/profile identity; never read from the workspace. Grant is revocable; revocation → next launch denied/safe. Profile/environment identity change invalidates or requires re-affirmation.

**State transitions**: requested mode `safe` → operator requests `trusted-host-ide` → run-bound decision pending/approved/denied. Revoke or identity change invalidates the grant but does not silently rewrite the requested mode; trusted opens remain denied until the operator explicitly selects `safe` or obtains a new run-bound grant. Target retries never change either state.

## ProjectionAuditRecord (`ide.open`)

Typed evidence of a projected open.

| Field | Type | Rules |
|-------|------|-------|
| `Command` | string | `code`. |
| `Capability` | string | `host.app.open-resource`. |
| `Mode` | enum | `safe` \| `trusted-host-ide`. |
| `WorkspaceIdentity` | string | Workspace name/identity, not host path. |
| `RelativeTarget` | string | Relative resource; no host absolute path. |
| `Outcome` | enum | `launched` \| `refused` (+ recovery code). |

**Validation**: MUST NOT contain host absolute path, host username, host home, decision/claim tokens, or raw guest argv. Passes existing control-plane redaction before persistence; export goes through the 005 boundary.

## ProjectionInspection (doctor / manager)

Read model for US4.

| Field | Type | Rules |
|-------|------|-------|
| `ProjectedCapabilities` | []descriptor summary | id, riskClass, resultPolicy, status. |
| `Bindings` | []binding summary | command → capability, mode. |
| `PathShadowOrder` | []entry | For each projected name, whether a real guest binary is shadowed and the resolution order. |
| `Mode` | enum | active safe/trusted mode. |

**Validation**: no host absolute path, token, or secret in the inspection output.
