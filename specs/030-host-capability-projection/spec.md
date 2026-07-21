# Feature Specification: Host Capability Projection

<!-- markdownlint-disable MD013 MD036 MD060 -->

**Feature Branch**: `030-host-capability-projection`

**Created**: 2026-07-10

**Status**: Implemented

**Input**: Design draft `.tmp/030-host-capability-projection-draft.md` (reviewed across multiple rounds). Core product point: inside a strong-isolation guest, project authorized host capabilities as the commands a CLI already knows (`code .`, `open .`), even though those commands do not exist in the guest — "it just works" through typed, audited, fail-closed brokered routes. V1 implements exactly one capability family end-to-end (`host.app.open-resource` with the `code` recipe); adb, AppleScript templates, and result streaming are design-ready only.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Open a workspace resource in the host IDE from inside the guest (Priority: P1)

An operator runs an agent or shell inside the isolated guest. The guest image does not contain a `code` binary. The operator (or the agent) types `code .`, `code src/main.go`, or `code -g src/main.go:42:8`. The corresponding host workspace opens in the operator's VS Code, in a constrained safe mode, without the operator learning any host absolute path from inside the guest and without the guest gaining any new host authority.

**Why this priority**: This is the headline product demonstration of the whole feature — a command that does not exist in the guest "just works" through a typed brokered route. It is the single most user-visible proof that Hideout reconciles strong isolation with a native-feeling developer experience, and it is the smallest slice that stands alone as a viable MVP.

**Independent Test**: In a real guest with no `code` binary, run `code .` and observe the correct host workspace directory open in a constrained VS Code instance; run `code -g <file>:<line>:<column>` and observe the correct file and cursor location; confirm the guest process, its output, and the audit record never contain the host absolute workspace path or host username.

**Acceptance Scenarios**:

1. **Given** a guest with a projected `code` command and no real `code` binary, **When** the operator runs `code .` in the mounted workspace, **Then** the mapped host workspace directory opens in a constrained (safe-mode) VS Code instance and a typed `ide.open` audit record is written.
2. **Given** the same environment, **When** the operator runs `code -g src/app.ts:12:3`, **Then** VS Code opens the mapped host file at line 12 column 3, and the guest never receives the host absolute path.
3. **Given** a path outside the mounted workspace (for example a guest-only path or `../` escape), **When** the operator runs `code <that path>`, **Then** the command fails closed with a typed "no host mapping" result and nothing opens.
4. **Given** an argument or flag the `code` grammar does not recognize, **When** the operator runs it, **Then** the command is rejected (unknown flags denied) and no raw guest argument reaches the host.
5. **Given** a guest where a projected `code` shim is installed but the capability provider is unavailable or validation fails, **When** the operator runs `code .`, **Then** the command fails closed and never delegates to host execution or to any real same-named binary the shim shadows.

---

### User Story 2 - Use the guest without disclosing the host username or path shape (Priority: P2)

An operator creates a privacy or hardened profile. Inside the guest, the workspace appears at a stable neutral path (`/workspace`) rather than the operator's real home path, so the host username and host path shape are not synthesized into the guest's default workspace path, identity environment, generated Git identity, or verified guest-visible mount metadata. The operator understands, and the product states, exactly what this does and does not protect.

**Why this priority**: The projection model requires that the guest and untrusted adapters only ever see a relative/aliased workspace view while the real host path lives only in Core. Establishing that neutral path model is the foundation the `code` recipe safely sits on, and it closes an information-disclosure channel (host username) that the current `preserve` default leaves open. It is independently valuable and independently testable, but slightly less user-visible than US1.

**Independent Test**: Create a new privacy profile, run a command inside a real guest, and confirm `pwd` resolves under `/workspace`; assert the host username and host home path do not appear in the identity environment, workspace namespace, or mount metadata; run a `preserve`-mode control to prove the detectors actually catch the host path when it is present.

**Acceptance Scenarios**:

1. **Given** a newly created privacy or hardened profile, **When** an environment is created and a command runs, **Then** the guest sees the workspace at `/workspace` and the workspace mount resolves there.
2. **Given** alias mode is active, **When** the three disclosure channels (identity environment; workspace namespace including `pwd`/`realpath`/arguments/errors; mount metadata via `/proc/mounts`, `/proc/self/mountinfo`, `mount`, `findmnt`) are inspected on a real backend, **Then** none contains the host username, host home path, or host workspace root.
3. **Given** a `preserve`-mode control fixture, **When** the same inspection runs, **Then** the host path shape is deliberately exposed, proving the alias assertion cannot pass without exercising the real mapping.
4. **Given** an existing environment created before the default changed, **When** the profile default flips to alias, **Then** the existing environment is not silently changed and a `pathMode` change is reported as a workspace drift requiring explicit recreate.

---

### User Story 3 - Opt into the operator's full trusted host-app deliberately (Priority: P3)

An operator who wants their normal VS Code configuration (extensions, tasks, personal profile) explicitly opts into a `trusted-host-app` mode for a profile through the operator decision surface. The choice is remembered so `code .` stays low-friction, but it is recorded as a visible, revocable grant rather than a silent permanent flag, and the default remains the constrained safe mode.

**Why this priority**: The default safe mode is what preserves the boundary; the trusted-host-app escape hatch is a real convenience for some operators but carries higher authority (a guest-writable workspace opened in a fully trusted host-app that may auto-run workspace tasks and extensions). It must exist but must not be the default and must be governed like any other high-authority grant. It sits on top of US1 and is therefore lower priority.

**Independent Test**: With only the default safe mode, confirm `code .` opens without extensions/auto-tasks; request trusted mode without a grant and confirm the command is denied with no host launch; grant trusted mode through the decision surface and confirm one launch uses the operator profile; revoke the grant and confirm the next launch is denied until the operator explicitly selects safe mode again.

**Acceptance Scenarios**:

1. **Given** no trusted-mode grant, **When** the operator requests trusted-host-app, **Then** `code .` is denied with no host launch; it MUST NOT silently substitute safe mode for the requested mode.
2. **Given** the operator grants trusted-host-app for a profile through the decision surface, **When** `code .` runs, **Then** it opens with the operator's normal configuration and the grant is visible as an active, revocable record.
3. **Given** an active trusted-mode grant, **When** the operator revokes it, **Then** the next `code .` is denied until the operator explicitly selects safe mode or obtains a new run-bound trusted grant.
4. **Given** safe mode, **When** the workspace contains a folder-open task that would write a host marker, **Then** the marker is not written (auto-tasks and extensions did not run) and Workspace Trust protection was not disabled.

---

### User Story 4 - Inspect what is projected and how it maps (Priority: P3)

An operator can inspect, through existing diagnostic surfaces, which host capabilities are projected into a given environment, which command names are bound to which capabilities, the safe/trusted mode in effect, and the PATH shadow order that determines whether a projected shim overrides a real guest command.

**Why this priority**: Projection deliberately makes commands behave differently from a plain guest; operators must be able to see that behavior and its precedence to trust it and to debug "why did `code` do that." It is supporting evidence rather than the core flow, so it is lower priority than the projection itself.

**Independent Test**: Query the diagnostic surface for a projected environment and confirm it lists the projected capabilities, command bindings, active mode, and PATH shadow order, without exposing any host absolute path, token, or secret.

**Acceptance Scenarios**:

1. **Given** an environment with the `code` recipe projected, **When** the operator inspects it, **Then** the output lists the `code` binding, its capability, the active safe/trusted mode, and the PATH shadow order, with host paths kept internal to Core.
2. **Given** a projected command name that also exists as a real guest binary, **When** the operator inspects, **Then** the shadow order that determines which one runs is explicitly reported.

---

### Edge Cases

- The host application (VS Code) is not installed or not resolvable on the host: the command fails closed with a typed diagnostic, never a silent no-op or a fallback to another binary.
- The requested app identity resolves to a drifted or unregistered target: fail closed with a typed error, no fallback.
- A workspace symlink, rename, or replacement races between resolution and host launch: the host target is re-validated from the session-bound mapping; an escape outside the workspace is refused.
- The guest passes a path that maps to nothing on the host (guest-only path): refused with an explicit "no host mapping" result.
- Repeated rapid invocations against the same target (an agent spamming windows): de-duplicated or rate-limited so projection cannot flood the host.
- A projected command receives result-bearing subcommands in a future capability family: refused in v1 because the `code` family declares a `none` result policy; nothing streams host data back to the guest.
- `pathMode=alias` breaks cross-boundary absolute-path consistency (guest-written `/workspace/...` paths do not resolve on the host): this is a known, documented tradeoff, not a defect.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: A new typed host capability family (`host.app.open-resource`) reached through the existing command-proxy/broker path; the workspace path model (`pathMode` alias); the operator decision surface (for trusted-mode grants); profile, environment lifecycle, audit, and doctor. No new ambient host reach-back; the capability is a typed brokered route, not host command execution.
- **Fail-closed behavior**: An unbound projected name, a hit shim whose provider is unavailable or fails validation, an unknown flag, a path with no host mapping, an unregistered/absent app, a stale symlink target, or a missing trusted-mode grant all deny or stop before any host side effect. There is no generic fallback to host execution or to a shadowed guest binary.
- **User authority and policy**: Safe mode is a low-risk default-allowed capability (like `open`/`xdg-open` registration); trusted-host-app is an explicit operator grant through the decision surface, bound to session/profile/subject and revocable. Existing profiles and environments are never silently changed; a `pathMode` change enters the existing environment drift/recreate model.
- **Generality and provider scope**: Core owns a generic capability registry and a generic `host.app.open-resource` capability plus a stable app-identity registry. `code`/VS Code is a **recipe** (a command binding + adapter grammar + a registered app identity), not Core semantics. Command syntax (`-g file:line:column`, window mode) lives in a per-command adapter/grammar layer; the Core capability sees only a structured, app-agnostic intent. `cursor`, `zed`, and similar are future recipes over the same capability. adb, AppleScript templates, and result streaming are design-ready in the registry model but not implemented in v1.
- **Evidence surface**: A typed `ide.open` (projection) audit record; doctor/inspection surface for projected capabilities, bindings, mode, and PATH shadow order; the three-channel privacy verification is real-backend evidence. Positive tests plus fail-closed tests are required; the privacy claim is gated on real-backend proof with per-channel detector self-test and a preserve-mode positive control.
- **Secret/redaction boundary**: The host absolute path, host username, host home path, capability decision/claim tokens, and raw guest argv MUST never appear in guest responses, adapter context, capability intents, projection events, or exported evidence. Only Core resolves the host path from a session-bound mapping; the projected audit event does not store `hostPath` when the mapping can resolve it. Local full-fidelity operator audit remains governed by the existing export/redaction boundary.
- **Backend/gate expectation**: Gate 0 for registry/grammar/intent/no-fallback/redaction unit and mechanics coverage. Real macOS arm64 Lima proof for the guest-visible `code .` open, the safe-mode "auto-exec did not run" behavior, the trusted-mode grant/revoke lifecycle, and the three-channel username-hiding verification. Real Gate 3 privacy assertions must continue to pass with the projected/aliased environment. Native backend and local fixtures satisfy mechanics only, never the guest-visible or privacy claims.

## Requirements *(mandatory)*

### Functional Requirements

**Core projection model**

- **FR-001**: Core MUST own a static capability registry describing each host capability by stable id, risk class, intent schema, resource kinds, result policy, provider, decision policy, and lifecycle policy. The registry MUST be Core/package-owned and MUST NOT be a runtime interface through which scripts or ecosystem artifacts register providers.
- **FR-002**: Core MUST provide a generic `host.app.open-resource` capability that opens a validated host-appntified resource in a registered host application, consuming only a structured, app-agnostic intent (application id, resource references, optional location, window mode).
- **FR-003**: A projected command MUST be defined as a command binding (command name(s) → adapter → allowed capabilities) that is separate from the capability itself, so new command surfaces are added by binding/adapter without adding Core authority.
- **FR-004**: The guest, JS adapters, capability intents, broker receipts, and projection events MUST reference workspace resources by a workspace-scoped ResourceRef (kind, guest path, relative path) and MUST NOT contain the host absolute path; only Core resolves the host path from the session-bound workspace mapping.
- **FR-005**: A JS/declarative command adapter MUST only parse arguments, classify intent, and produce a structured proposal; it MUST NOT pass raw guest argv to the host. Go MUST strictly decode, reject unknown fields, normalize the ResourceRef, and re-validate every intent field before the provider acts.

**Fail-closed and no-fallback invariants**

- **FR-006**: When a projected command shim is invoked but the capability is unbound, the provider is unavailable, validation fails, the flag/argument is unrecognized, the path has no host mapping, or the app is absent/drifted, the command MUST fail closed with a typed recovery record and MUST NOT delegate to host execution or to a real same-named guest binary the shim shadows.
- **FR-007**: An installed projected command name MUST exclusively own that name in the guest; where a real same-named guest binary also exists, the PATH shadow order MUST be explicit and inspectable, and a failed projection MUST NOT hand off to the shadowed binary. When no projected shim is installed, a real same-named guest binary running normally is not a fallback.
- **FR-008**: Host identity resolution — application id → host binary/bundle, and any future script template id — MUST resolve only through a Core/package-owned registry. Untrusted layers (JS adapters, profiles, ecosystem artifacts) MUST reference a stable id only and MUST NOT supply a binary path, bundle id, or script source.

**Result channel and lifecycle**

- **FR-009**: Every capability MUST declare a typed result policy. The `code`/`host.app.open-resource` family MUST declare a `none` result policy: it is fire-and-forget and MUST NOT stream or return host data into the guest. Any host→guest result channel (design-ready families such as adb/AppleScript) MUST be a typed, bounded, audited channel and MUST NOT be delivered through ordinary command stdout.
- **FR-010**: Capability decisions, grants, and any lease MUST bind to the current session, profile, and subject, and MUST be revoked when that lifecycle ends; a projection grant MUST NOT survive as ambient standing authority.

**Safe / trusted host-app modes**

- **FR-011**: The `code` recipe MUST default to a Core-defined safe open mode: an isolated VS Code profile or user-data directory, extensions disabled, workspace auto-tasks not run, and Workspace Trust left enabled. The safe mode MUST NOT use `--disable-workspace-trust`. Safe mode MUST open the mapped host workspace without a per-invocation approval prompt.
- **FR-012**: A `trusted-host-app` mode that uses the operator's normal VS Code configuration MUST require an explicit operator grant obtained through the existing decision surface, MUST be denied without that grant, MUST be recorded as a visible revocable grant (not a silent permanent flag), and MUST be re-affirmable/invalidated on profile or environment identity change. The mode choice MUST be persisted only in guest-unreachable control-plane state keyed by workspace/profile identity, never in the guest-writable workspace.
- **FR-013**: The product MUST NOT claim to protect the host IDE from a malicious workspace; it MUST, however, disarm the obvious auto-execution vectors by default (safe mode) and MUST surface, in evidence, that a guest-writable workspace was opened in a host application.

**Privacy: workspace path and identity**

- **FR-014**: Newly created privacy and hardened profiles MUST default the workspace `pathMode` to `alias` so the guest sees `/workspace`. `dev` and `debug` MAY retain `preserve`. Recommended templates MUST NOT claim path privacy while silently inheriting `preserve`.
- **FR-015**: In alias mode Hideout MUST NOT synthesize the host username or host home path into the target's default workspace path, identity environment, generated Git identity/config, or verified guest-visible mount metadata. The verification MUST distinguish the guest account home from the target process HOME override and treat neither as host identity.
- **FR-016**: A `pathMode` change MUST be treated as an environment identity/drift change requiring explicit recreate; existing environments MUST NOT be silently remapped. This MUST use the existing persisted guest-workspace field and the existing drift comparator (workspace axis) rather than a new identity input or record-version bump.
- **FR-017**: The product MUST publish a scoped positive privacy claim (username/home not synthesized into the named guest surfaces in alias mode) together with an adjacent non-claim (Hideout does not inspect or remove identity data that operators, projects, dependencies, or tools place in workspace content or command output, and alias mode does not preserve general absolute-path identity). The product MUST NOT shorten this to a universal "Hideout hides your identity" claim.

**Evidence and inspection**

- **FR-018**: Projection MUST emit a typed audit record for each host-app open that records the projected command, capability, mode, and workspace identity, without host absolute path, host username, tokens, or raw guest argv.
- **FR-019**: An operator MUST be able to inspect, through an existing diagnostic surface, the capabilities projected into an environment, the command bindings, the active safe/trusted mode, and the PATH shadow order, with host paths kept internal to Core.

### Key Entities *(include if feature involves data)*

- **CapabilityDescriptor**: The Core-owned, static description of one host capability — id, risk class, intent schema, resource kinds, result policy, provider reference, decision policy, lifecycle policy. The registry of descriptors is the authority surface; it is not runtime-extensible.
- **CommandBinding**: A mapping from one or more guest command names to an adapter and the set of capabilities that adapter may propose. Recipes (e.g. `code`) are command bindings plus an adapter/grammar, not new capabilities.
- **Command adapter / grammar**: The per-command layer (declarative grammar for simple commands, constrained script for complex ones) that parses guest argv into a structured intent. Untrusted; carries zero host authority; unknown flags denied.
- **Projection intent (OpenResourceIntent)**: The app-agnostic structured request the Core capability consumes — application id, resource references, optional location (line/column), window mode. Re-validated field-by-field by Go.
- **Workspace ResourceRef**: The guest/relative view of a workspace resource (kind, guest path, relative path). Only Core maps it to a host path via the session-bound mapping.
- **App identity registry entry**: The Core/package-owned mapping from a stable application id (e.g. `vscode`) to the host application, per platform. Referenced by id only from untrusted layers.
- **IDE open mode**: The safe / trusted-host-app selection for a profile, persisted in control-plane state; trusted mode is a revocable operator grant.
- **Projection audit record / verification receipt**: The typed evidence of a projected open, and the bounded real-backend receipt of a privacy verification; neither is a permanent truth claim about mutable guest state.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a real guest that contains no `code` binary, an operator can run `code .`, `code <path>`, and `code -g <file>:<line>:<column>` and the correct host workspace resource opens in a constrained VS Code instance every time, with no per-invocation approval prompt.
- **SC-002**: 100% of tested out-of-scope or malformed invocations (guest-only path, workspace escape, unknown flag, absent app, unavailable provider) fail closed with a typed result and open nothing; none delegate to host execution or a shadowed guest binary.
- **SC-003**: 0 occurrences of the host absolute path, host username, host home path, decision/claim tokens, or raw guest argv appear in guest responses, adapter context, capability intents, projection events, or exported evidence across all tested flows.
- **SC-004**: In a new privacy/hardened profile on a real backend, the guest workspace resolves under `/workspace` and 0 of the three disclosure channels (identity environment, workspace namespace, mount metadata) contain the host username or home path; the preserve-mode control fixture and per-channel detector self-tests confirm the checks can actually fail.
- **SC-005**: 100% of tested `pathMode` changes on existing environments are reported as a workspace drift requiring explicit recreate, and 0 existing environments are silently remapped.
- **SC-006**: Trusted-host-app is denied without a grant in 100% of tested cases; after an explicit grant it launches with the operator configuration, and after revocation the next launch no longer uses it; in safe mode a folder-open task marker is never written.
- **SC-007**: An operator inspecting a projected environment sees the projected capabilities, command bindings, active mode, and PATH shadow order, with 0 host absolute paths or secrets exposed.
- **SC-008**: Real macOS arm64 Lima Gate 2 proves SC-001, SC-002, SC-004, SC-006 on the real backend; real Gate 3 privacy assertions still pass with the projected/aliased environment; native backend and local fixtures record mechanics-only and never satisfy the guest-visible or privacy claims.

## Assumptions

- The existing command-proxy/broker path, workspace mount, `host.open`/workspace-file opener (which already handles directories and re-checks symlink escape), `pathMode=alias` mapping (`/workspace`), synthetic identity (`developer` account, `HOME` override, generated Git identity), operator decision center, environment drift model, and command-adapter (008) proposal outcomes are available and are reused rather than rebuilt.
- V1 targets the macOS arm64 first-class path with VS Code as the single implemented recipe. Linux host, `cursor`/`zed`/other editors, adb, AppleScript templates, and result-streaming capabilities are design-ready in the registry model but explicitly out of scope for v1 implementation.
- The workspace is guest-writable and may contain agent-authored content; opening it in a host IDE is therefore treated as a host-execution surface to be disarmed by default, not a trusted-input path.
- The guest is a mutable reusable Linux VM; a successful projection or verification is bound to the current session/image/identity and is not a permanent attestation of guest state.
- Real backend evidence (Lima Gate 2/Gate 3) is produced by the operator; local fixtures and native backend runs prove mechanics only and are recorded honestly as not satisfying the guest-visible or privacy claims.
- The feature adds no new ambient host authority; a malicious image or workspace can still read and transmit what the target is already granted, and Hideout does not claim guest-root containment.
