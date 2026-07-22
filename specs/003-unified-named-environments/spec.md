<!-- markdownlint-disable MD013 -->

# Feature Specification: Unified Named Environments With Declared Base Image

**Feature Branch**: `003-unified-named-environments`

**Created**: 2026-07-06

**Status**: Draft

**Input**: User description: "Unified named environments + declared base image。环境统一为唯一模型：一切环境皆命名，env create/list/inspect/recreate/remove + `run --env <name>`；不带 --env 的 run 解析到从 (profile, workspace) 确定性派生的自动命名环境，与显式命名环境同一套生命周期与语义；MRU 指纹选择机制删除。每个环境都有声明的 base image（env create --image 显式 > profile environment.baseImage 默认，default profile 显式携带内置模板引用，lima.go 硬编码删除）；image 以 ref+digest 固化进环境身份，Lima 从声明生成 images 条目并校验 digest，拉取失败 fail closed。所有环境的配置在创建时固化，任何漂移（image digest / expectedCommands / backendConfigVersion / workspace）一律 fail closed 并给 env recreate 指引——不再静默派生新环境。workspace 在 create 时 pin（SameFile 判定+危险根检查），HostFS 规则被 workspace 遮蔽时告警；default 为保留名（留给 004 共享环境）；顶层 hideout list 删除，env list 是唯一清单命令；存量环境记录不迁移，版本跳变后提示 clean 重建。不做：共享 default 环境、动态挂载、daemon、镜像构建/缓存/凭据管理、生态 image 分享接入面、onboarding。"

The raw input above is historical context. The clarifications, requirements,
contracts, and tasks below are normative where they narrow or correct it.

## Current Status Context

This feature follows `002-guided-first-run` (tool model cleanup). After 002,
Hideout ships no package-installation providers: guest tools come from the
declared base image plus operator-authored in-boundary setup, and
`tools.expectedCommands` is the diagnostic-only declaration model. The
architecture documents and constitution (v1.2.0) already define the target
direction this feature implements: environments as named, user-selected
runtime boxes; the base image reference as declarative guest-domain data pinned
into the environment record; and backend/workspace drift as a fail-closed
condition rather than a silent switch. Today the implementation still derives
environments implicitly from a fingerprint with most-recently-used selection,
and the Lima base image is a hardcoded template.

Because the product is unreleased, this feature applies the clean-change
principle: one environment model replaces the old one outright, with no
compatibility layer, no dual model, and no store migration.

The shared `default` environment (any-directory instant attach, dynamic
workspace transport, session view isolation) is the next slice, not this one.
This feature reserves the vocabulary and keeps the model ready for it: the
follow-up changes only the default resolution rule, not the environment model.

## Clarifications

### Session 2026-07-06

- Q: Should 003 deliver the full named+shared environment experience or split
  it? → A: Split. 003 delivers the unified named-environment model plus
  declared base images; the shared `default` environment (dynamic workspace
  attach, session view isolation, performance gate) is a separate follow-up
  slice.
- Q: Should the existing fingerprint-derived anonymous environments coexist
  with named environments? → A: No. The product is unreleased; the model is
  unified cleanly. Every reusable environment is named; non-disposable runs
  without `--env` resolve to a deterministically auto-named per-workspace
  environment with the same
  lifecycle and drift semantics; the most-recently-used fingerprint selection
  machinery is removed, and existing on-disk environment records are
  invalidated by a record-version bump with clean-and-recreate guidance
  instead of migration.
- Q: What image reference forms does this slice accept? → A: Lima-native
  forms only: a disk-image URL that MUST carry an explicit sha256 digest
  supplied by the operator (from the distributor's published checksums), or a
  built-in template name (the default profile's explicit default). Creation
  performs format validation, not network resolution; a URL reference without
  a digest is rejected with guidance. OCI registry references are deferred to
  container-class backends.
- Q: What do `env recreate` and `env remove` do when the environment's guest
  is running? → A: Fail closed by default: refuse, name the running state,
  and print a copyable stop command. An explicit force flag stops the guest
  first and then proceeds. Destructive lifecycle actions are never implicit.
- Q: Are expected-command declarations or image changes drift axes? → A:
  Expected-command declarations are diagnostic-only state evaluated live at
  each run/doctor: changing declarations never marks an environment as drifted
  and never forces a recreate; a missing declared command that is required for
  the requested target still fails closed at readiness. The base image
  declaration is pinned and immutable for the environment: profile default
  changes do not drift existing environments, and changing an environment's
  image requires remove/create or a later explicit update feature. URL digest
  mismatch is a boot-time verification failure, not an environment drift
  report. Drift comparison in this slice has two axes: backend configuration
  version and pinned workspace. The design document's identity wording is
  updated to match during implementation.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Create And Use A Named Environment With A Declared Base Image (Priority: P1)

An operator creates a named environment that declares which base image the
guest boots from, then runs their CLI inside it. The image declaration — a
disk-image URL with an explicit digest, or a built-in template name — is
validated and pinned when the environment is created, so every later run of
that environment boots the exact same guest baseline. This restores
reproducible guest tool supply after the 002 provider removal: the operator
bakes or picks an image with the tools they need and names the environment
once.

**Why this priority**: This is the dogfood-critical slice. After 002, guest
tools come from the base image or manual in-boundary setup; without a declared
image the daily `hideout run -- <agent CLI>` flow has no reproducible supply
path.

**Independent Test**: With a clean store, create a named environment with an
explicit digest-carrying image URL, run a command in it through the real
backend, and verify the guest booted from the pinned image and the
environment records the pinned digest. Deliverable value stands alone even if
nothing else in this feature ships.

**Acceptance Scenarios**:

1. **Given** a clean store and a digest-carrying image URL, **When** the
   operator runs `hideout env create work --image <url>#sha256:<digest>` (or
   equivalent declaration form) and then `hideout run --env work -- <command>`,
   **Then** the environment is created with the reference and digest recorded
   verbatim, the guest boots from that image, and the command runs inside it.
2. **Given** a URL image reference without a digest, **When** the operator
   creates an environment with it, **Then** creation fails closed with
   guidance to supply the distributor-published sha256 checksum; no network
   resolution is attempted.
3. **Given** an environment created without an explicit `--image`, **When**
   the environment is created, **Then** the image declaration is filled from
   the selected profile's base image default, and the default profile carries
   an explicit built-in image reference so no environment ever exists without
   a declared image.
4. **Given** a pinned image that cannot be pulled or fails digest
   verification, **When** the environment first boots, **Then** setup fails
   closed with a diagnostic naming the image reference and digest, and no run
   is claimed ready.

---

### User Story 2 - One Environment Model For Every Run (Priority: P2)

An operator who runs Hideout without naming an environment still gets a named
environment: the run resolves to an automatically named environment derived
deterministically from the profile and workspace. Auto-named environments
appear in the environment list, can be inspected, recreated, and removed, and
follow exactly the same rules as explicitly created ones. There is no second,
implicit environment model.

**Why this priority**: Unifying the model removes the silent
fingerprint-derived environment sprawl and gives the follow-up shared-default
slice a stable foundation where only the resolution rule changes.

**Independent Test**: Run a command in a fresh workspace without `--env`,
confirm an auto-named environment is created and listed; rerun and confirm the
same environment is reused; inspect it and confirm it has the same recorded
shape (name, image declaration, pinned workspace) as an explicitly created
environment.

**Acceptance Scenarios**:

1. **Given** a fresh workspace and no `--env` flag, **When** the operator runs
   a command, **Then** Hideout resolves to a deterministic auto-named
   environment for that profile and workspace, creating it on first use and
   reusing it afterward.
2. **Given** any mix of auto-named and explicitly named environments, **When**
   the operator lists environments, **Then** one listing command shows all of
them with name, image declaration, workspace, status, and disk usage, and no
   other top-level listing command exists.
3. **Given** the name `default`, **When** an operator tries to create an
   environment with it, **Then** creation is rejected because the name is
   reserved for the follow-up shared environment.
4. **Given** a store containing environment records from the previous model,
   **When** any environment operation touches them, **Then** Hideout reports
   that the environment model changed and directs the operator to clean and
   recreate; no silent migration or reuse occurs.

---

### User Story 3 - Drift Is Explicit, Never Silent (Priority: P3)

An operator who changes a runtime input that is compared at use time — the
backend configuration version or the pinned workspace — is told about it the
next time they use that environment. The run fails closed with a summary of
what drifted and a copyable recreate command. Environments never silently
switch, never silently rebuild, and never silently multiply. The base image is
pinned immutable environment data: boot-time digest verification failures are
reported as image verification errors, not drift reports. Expected-command
declarations are not identity: they are evaluated live as diagnostics on every
use and never force a rebuild.

**Why this priority**: Drift semantics are what make named environments
trustworthy: the name always means the configuration the operator confirmed.
This story also carries the workspace-safety hardening (pinning, dangerous
root guard, shadowed-rule warning).

**Independent Test**: Create an environment, change each use-time drift input
one at a time (backend config version, run from a different directory), and
verify each change produces a fail-closed drift report naming the changed
input plus a recreate hint; verify `env recreate` rebuilds the guest in place
under the same name from its pinned configuration; verify that changing the
profile base image default or expected-command declarations alone produces no
drift and no recreate requirement.

**Acceptance Scenarios**:

1. **Given** an environment whose profile later declares different expected
   commands, **When** the operator runs in that environment, **Then** the
   environment is not drifted and no recreate is required; the new
   declarations are evaluated live, and a missing declared command that is
   required for the requested target fails closed at readiness with a
   diagnostic, not a drift report.
2. **Given** an environment with a pinned workspace, **When** the operator
   uses it from a different directory, **Then** the run fails closed and
   reports the workspace mismatch using real file identity, not string
   comparison.
3. **Given** a drifted environment, **When** the operator runs the recreate
   command, **Then** the guest is destroyed and rebuilt from the environment's
   pinned declared configuration under the same name, and the next run
   succeeds.
4. **Given** an environment whose guest is running, **When** the operator
   invokes `env recreate` or `env remove` without the force flag, **Then** the
   command fails closed naming the running state and printing a copyable stop
   command; **When** invoked with the explicit force flag, **Then** the guest
   is stopped first and the operation proceeds.
5. **Given** a profile HostFS rule that overlaps the pinned workspace,
   **When** plans or doctor evaluate it, **Then** the operator is warned that
   the rule is shadowed by the workspace; the run itself is not blocked.
6. **Given** a workspace path that is the host home, the Hideout store, a
   credential root, or a parent of those, **When** an environment is created
   with it, **Then** creation is rejected under the existing dangerous
   workspace-root policy unless the explicit high-risk override is used.

---

### Edge Cases

- The image reference is malformed, names an unsupported scheme, or carries
  embedded credentials.
- The image reference is a URL without a digest, or with a malformed digest
  string.
- The downloaded image does not match the pinned digest at boot time.
- The environment name is malformed, empty, path-like, or collides with an
  existing environment or with the deterministic auto-name of a workspace.
- The reserved name `default` is requested, in any letter case.
- `env recreate` is invoked while the environment's guest is running.
- `env remove` is invoked while the environment's guest is running.
- The pinned workspace is renamed, moved, or replaced by a same-path different
  file identity after creation.
- The selected profile changes its base image default after an environment was
  created (no drift: the environment keeps its own pinned value).
- Old-version environment records exist in the store, including partially
  written or corrupt records.
- Two invocations race to create the same environment name.
- A disposable run is requested and must not leave a reusable environment
  behind.
- The native backend is selected: the same model applies with no VM boot, and
  native environments carry the same declared-image identity field for
  consistency even though no image is booted.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Environment lifecycle (create/list/inspect/recreate/
  remove), backend preparation input (base image declaration), profile
  defaults (`environment.baseImage`), run selection, workspace mount safety,
  and Manager plan/apply surfaces for the new lifecycle operations. No new
  host reach-back capability is introduced.
- **Fail-closed behavior**: Malformed or unsupported image references at create;
  digest mismatch or unpullable image at boot; backend configuration or
  workspace drift at run; missing declared commands
  required for the requested target at readiness; reserved or
  colliding names at create; dangerous workspace roots at create; old-version
  store records on any touch. All stop before backend preparation or before a
  run-ready claim, with operator-facing diagnostics.
- **User authority and policy**: Explicit operator commands (`env create`,
  `env recreate`, `env remove`) are user-authoritative. The base image
  declaration is guest-domain data under the constitution's declarative image
  carve-out: it does not pass a host trust gate, and a bad image degrades to
  in-boundary adversary positions already covered by the threat model.
  Project- or ecosystem-supplied image suggestions are out of scope for this
  feature; only operator commands and profile defaults declare images. Deny
  rules and the dangerous-workspace-root policy keep precedence.
- **Generality and provider scope**: The image declaration is a generic
  reference (name plus digest). No registry product, image builder, or
  distribution channel becomes Core semantics. The built-in default image is
  an explicit profile-carried reference, not a hardcoded backend special case.
- **Evidence surface**: `env list`/`env inspect` show name, image declaration,
  workspace, status, and disk usage; create/recreate/remove and drift
  rejections are audited; run output names the selected environment; Manager
  API exposes the same environment resources for TUI/WebUI read views.
- **Secret/redaction boundary**: Image references and digests are
  operator-declared user data and appear verbatim in local evidence.
  References must not carry embedded credentials (rejected at validation).
  Registry authentication material is never stored, displayed, or exported by
  Hideout. Control-plane redaction rules are unchanged.
- **Backend/gate expectation**: Unit and schema tests plus Gate 0 for model,
  naming, drift, and validation behavior; a real Lima gate variant proves an
  environment created from a declared image boots that image, that wrong URL
  digests fail closed, and `env recreate` rebuilds from the pinned declaration.
  Native harness covers CLI wiring only.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST have exactly one reusable environment model:
  every reusable environment is named, and explicitly created and automatically
  created environments share the same record shape, lifecycle operations,
  identity rules, and drift semantics. Disposable `--rm` runs own a per-run
  dedicated record marked disposable, removed by the proved teardown so no
  reusable record survives. `--ephemeral` changes only session identity state and
  resolves the same reusable environment as the corresponding normal run.
- **FR-002**: The system MUST provide environment lifecycle commands to
  create, list, inspect, recreate, and remove named environments, and a run
  selector to execute a command inside a named environment.
- **FR-003**: A non-disposable run invoked without an explicit environment
  MUST resolve to an automatically named environment derived deterministically
  from the selected profile and the current workspace, creating it on first
  use and reusing it on later use. The previous most-recently-used fingerprint
  selection MUST be removed.
- **FR-004**: Environment names MUST pass conservative validation (no paths,
  whitespace, or shell metacharacters), MUST be unique, and the name `default`
  MUST be rejected as reserved, case-insensitively.
- **FR-005**: Every reusable environment MUST carry a declared base image reference.
  An explicit reference given at creation wins over the profile's base image
  default; the shipped default profile MUST carry an explicit built-in image
  reference so that no environment can exist without a declared image and no
  backend hardcodes one. A profile without `environment.baseImage` MUST
  resolve to the built-in template default at declaration resolution — the
  field is a default, not identity, so filling it is not record migration.
- **FR-006**: The image declaration MUST be pinned at creation by validation,
  not network resolution. Accepted forms are a disk-image URL carrying an
  explicit sha256 digest, or a built-in template name. A URL reference
  without a digest MUST be rejected with guidance to supply the
  distributor-published checksum. References carrying embedded credentials
  MUST be rejected. OCI registry references are out of scope for this slice.
- **FR-007**: Backend preparation MUST boot the guest from the environment's
  pinned image declaration. For URL image declarations it MUST verify the
  pinned digest; pull or verification failure fails closed with a diagnostic
  naming the reference and digest. For built-in template declarations, the
  backend template mapping is governed by the backend configuration version.
- **FR-008**: Environment identity MUST be fixed at creation and MUST include
  the pinned image declaration, backend configuration version, pinned
  workspace, backend, profile name, and profile identity fields. Use-time drift
  comparison in this slice covers backend configuration version and pinned
  workspace only. The pinned image declaration is immutable environment data:
  profile default changes do not drift existing environments, and URL digest
  mismatch is a boot-time verification failure rather than an image drift
  report. Expected-command declarations are not identity: they MUST be
  evaluated live as diagnostics at each use, and changing them MUST NOT mark
  an environment as drifted or force a recreate.
- **FR-009**: Any backend-configuration or workspace drift detected at use
  MUST fail closed with a summary naming each drifted input and a copyable
  recreate command. The system MUST NOT silently reuse a stale guest, silently
  rebuild, or silently create a replacement environment.
- **FR-010**: Recreating an environment MUST destroy its guest and rebuild it
  from the environment's pinned declared configuration under the same name;
  removing an environment MUST tear down its guest and record. Changing an
  environment's image declaration is out of scope and requires remove/create
  or a future explicit update feature. When the
  environment's guest is running, both commands MUST fail closed with a
  copyable stop command unless an explicit force flag is given, in which case
  they stop the guest first and then proceed.
- **FR-011**: The workspace MUST be pinned at environment creation, default to
  the invoking directory, be compared by real file identity rather than string
  paths, and pass the existing dangerous-workspace-root policy including its
  explicit high-risk override.
- **FR-012**: Planning and doctor surfaces MUST warn when a profile HostFS
  rule is shadowed by the pinned workspace; the warning MUST NOT block the
  run.
- **FR-013**: One listing command MUST show all reusable environments —
  auto-named and explicit — with name, image declaration, workspace, status,
  and disk usage; the
  previous top-level listing command MUST be removed from the public command
  surface.
- **FR-014**: Disposable runs MUST remain possible without leaving a reusable
  environment record behind.
- **FR-015**: Environment records MUST carry a new record version; records
  from the previous model MUST be rejected with clean-and-recreate guidance,
  and the system MUST NOT migrate them.
- **FR-016**: Environment create, recreate, remove, and drift rejections MUST
  be audited, and run evidence MUST name the environment used.
- **FR-017**: Manager plan/apply operations for the new lifecycle MUST expose
  the same model to CLI, TUI, and WebUI read surfaces; no surface may bypass
  the shared model.
- **FR-018**: This feature MUST NOT introduce image building, image caching
  services, registry credential management, ecosystem image sharing intake,
  dynamic workspace attachment, a shared `default` environment, or a daemon.
- **FR-019**: When `run --env <name>` is used, the environment record supplies
  the profile, backend, image, and pinned workspace binding. Conflicting
  `--profile`, `--backend`, or workspace inputs MUST fail closed; a
  non-conflicting workspace input is compared to the pinned workspace by real
  file identity.

### Key Entities

- **Environment**: A named, reusable guest machine bound to one profile and
  one pinned workspace, with identity fixed at creation. Attributes: name
  (explicit or deterministically auto-derived), pinned base image reference
  and digest, backend configuration version, pinned workspace identity,
  status, disk usage, record version. Expected-command diagnostics are read
  live from the profile at use time and are not stored as identity.
- **Base Image Declaration**: A guest-domain reference stating what the guest
  boots from: a built-in template name or a URL plus digest. Declared
  explicitly at creation or filled from the profile default; pinned at
  creation; carries no
  credentials, build steps, or provider logic.
- **Drift Report**: The fail-closed result produced when an environment's
  current inputs no longer match its creation-time identity. Names each
  drifted input and the recreate command; never triggers automatic rebuild.
- **Environment Record (versioned)**: The stored representation of an
  environment. The version bump in this feature invalidates prior-model
  records, which are rejected with guidance rather than migrated.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator with a valid image declaration (digest-carrying URL
  or built-in template) can go from a clean store to a command running inside
  a named environment booted from that image in at most three commands
  (create, run, plus optional inspect).
- **SC-002**: 100% of tested use-time drift cases (backend configuration
  version, workspace) fail closed with a drift summary and recreate hint, 0%
  of them silently create, switch, or rebuild an environment, and 100% of
  tested image-default and expected-command changes produce no drift.
- **SC-003**: After the change, the repository contains no live
  most-recently-used fingerprint selection path and no backend-hardcoded base
  image; both are verified by automated scans or tests.
- **SC-004**: One listing command accounts for 100% of environments on the
  machine, including auto-named ones; `env inspect` output for an auto-named
  environment is indistinguishable in shape from an explicit one.
- **SC-005**: 100% of tested prior-model store records produce the
  clean-and-recreate guidance, and none are silently reused or migrated.
- **SC-006**: The real-backend gate proves end to end that a declared URL image
  is what the guest boots (pinned digest verified), that a wrong digest fails
  closed, and that recreate rebuilds from the pinned declaration on the primary
  macOS backend.
- **SC-007**: 100% of environment create/recreate/remove operations and drift
  rejections appear in audit with the environment name.

## Assumptions

- Creating an environment records the declaration but does not boot the guest;
  the first run boots it. Creation failures are therefore about validation and
  digest string shape, and boot failures surface on first use.
- `env create` without `--workspace` pins the invoking directory as the
  workspace.
- Auto-derived environment names are deterministic for (profile, workspace)
  and include a short stable suffix so that renamed or moved workspaces do not
  silently alias; explicit names and auto-names share one namespace and
  collisions are rejected.
- Operators obtain the sha256 digest for a URL image from the distributor's
  published checksums (for example a SHA256SUMS file); Hideout never computes
  or fetches digests over the network at create time.
- Profiles that predate `environment.baseImage` (every existing store today)
  resolve the absent field to the built-in template default; first runs on
  existing stores therefore keep working without profile edits.
- The built-in default image reference points at the same guest baseline the
  backend uses today, expressed as an explicit profile value instead of a
  backend hardcode; its underlying template mapping is tracked through the
  backend configuration version.
- The existing per-environment locking, stop/clean lifecycle, idle filters,
  and SIGINT/SIGTERM cleanup behavior are reused; this feature renames and
  re-keys selection, it does not redesign runtime cleanup.
- Privately hosted image URLs are the operator's own hosting concern;
  Hideout neither stores nor forwards image-hosting credentials in this
  feature, and references embedding credentials are rejected.
- The single-operator model applies: concurrent same-name creation is
  resolved by existing store locking, not by a coordination service.
- Shared `default` environment work (dynamic attach, session view isolation,
  transport performance gate) is the next slice and will only change the
  no-flag resolution rule from auto-named-per-workspace to the shared
  environment.
