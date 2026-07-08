# Feature Specification: Command Capability Adapters

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `[008-command-capability-adapters]`

**Created**: 2026-07-08

**Status**: Draft

**Input**: User description: "Implement 008 from `.tmp/008-010-plan.md`: upgrade command proxy into a local, profile-scoped command capability adapter runtime. Deterministic JavaScript adapters may classify command intent, explain risk, deny, simulate, rewrite non-privileged guest commands, or propose Go Core-owned capabilities. Root-sensitive commands are the first built-in adapter, but this feature must not claim to block root escalation by itself; 009 owns privilege separation and 010 owns HostFS write apply."

## Clarifications

### Session 2026-07-08

- Q: How are overlapping adapter command matches resolved within one profile? → A: Reject duplicate command ownership.
- Q: What command context may adapters inspect? → A: Raw argv/cwd, env summary only.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Route Commands Through Adapters (Priority: P1)

As an operator running an untrusted CLI in Hideout, I want selected guest command invocations to be routed through explicit command adapters so that provider-specific behavior can be classified, denied, simulated, rewritten, or proposed without granting ambient host authority.

**Why this priority**: This is the minimum useful product slice. It turns command proxy from a small fixed surface into a bounded extension point while preserving Go Core ownership of authority.

**Independent Test**: Configure one local adapter for one command symbol, run a guest command that hits that symbol, and verify that the adapter outcome controls the command response, audit evidence is recorded, and existing unregistered commands behave as before.

**Acceptance Scenarios**:

1. **Given** a profile with an explicitly enabled local adapter for command `tool-x`, **When** the target invokes `tool-x --version`, **Then** Hideout routes the invocation to the adapter, applies the validated outcome, and records the adapter decision in audit.
2. **Given** an enabled adapter returns an invalid outcome, throws, times out, or proposes an undeclared capability, **When** the target invokes the registered command, **Then** Hideout denies the invocation before side effects and records the fail-closed reason.
3. **Given** a command symbol is not registered to an adapter or command proxy, **When** the target invokes that command, **Then** Hideout preserves the existing guest command behavior.

---

### User Story 2 - Capture Root-Sensitive Intent (Priority: P2)

As an operator reviewing untrusted tool behavior, I want common root-sensitive command attempts to be captured and explained so that I can see when a tool wants privileged guest mutation without confusing that signal for an enforced root boundary.

**Why this priority**: Root-sensitive commands are the highest-value first adapter because they reveal important intent, but the feature must be honest that command proxy alone cannot stop absolute-path or syscall-level root behavior.

**Independent Test**: Enable the built-in root-sensitive adapter, run representative commands such as `sudo apt install nodejs` and `iptables -F`, and verify that the adapter denies or proposes bounded intent, never simulates successful system mutation, and labels the result as intent capture rather than root containment.

**Acceptance Scenarios**:

1. **Given** the root-sensitive adapter is enabled, **When** the target invokes `sudo apt install nodejs` by command name, **Then** Hideout records a root-attempt intent with package-install details and either denies or creates a non-applied capability proposal.
2. **Given** the root-sensitive adapter is enabled, **When** the target invokes a destructive network mutation command such as `iptables -F`, **Then** Hideout denies the command and records the attempted network mutation intent.
3. **Given** root-sensitive adapter evidence is displayed or exported, **When** an operator reads it, **Then** it clearly states that the adapter captured command intent and does not claim that root escalation is impossible without 009 enforced privilege separation.

---

### User Story 3 - Enable Local Adapters Safely (Priority: P3)

As an operator or local adapter author, I want adapters to be enabled only after their identity and requested capabilities are explicit so that community-maintained logic can be useful without becoming authority.

**Why this priority**: Adapter supply is needed for a real ecosystem path, but v1 should stay local and profile-scoped rather than introducing marketplace trust too early.

**Independent Test**: Add a local adapter artifact to a profile, review its declared command matches and requested proposal capabilities, enable it, and verify that digest changes or undeclared capabilities fail closed.

**Acceptance Scenarios**:

1. **Given** a local adapter artifact has a recorded digest and declared command matches, **When** the operator enables it for a profile, **Then** Hideout records the adapter identity, digest, command matches, and allowed proposal capabilities.
2. **Given** an enabled adapter artifact changes after enablement, **When** a registered command tries to use it, **Then** Hideout refuses to run that adapter until the operator explicitly reviews and enables the changed artifact.
3. **Given** an adapter attempts to request host or privileged authority outside its declared proposal capabilities, **When** the adapter returns that outcome, **Then** Hideout denies the outcome and records the mismatch.

### Edge Cases

- Adapter entrypoint is missing, malformed, non-deterministic, or exceeds execution limits.
- Adapter output contains unknown fields, multiple JSON values, unknown outcomes, or a capability not declared for that adapter.
- Adapter receives command arguments that contain sensitive target data; local audit records the invocation but export/share redaction remains responsible for lossy sharing.
- Adapter attempts to include Hideout control-plane credentials, token-looking values, or backing secret names in resources, audit, suggestions, or simulated output.
- Multiple adapters claim the same command symbol for one profile; the profile configuration must be rejected before runtime.
- A command registered to an adapter is invoked through an absolute path that bypasses command proxy.
- A root-sensitive command asks for behavior that is not implemented as a Core-owned capability in 008.
- Existing `open` and `xdg-open` command proxy behavior must not change unless the operator explicitly reconfigures those symbols.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Command proxy, scripts, profile policy, audit evidence, Manager/Web/TUI visibility, and guest command execution. No new host authority provider is introduced in 008.
- **Fail-closed behavior**: Registered adapter commands deny before side effects when the adapter is missing, changed without review, invalid, over limit, requests undeclared capability, returns unsupported outcome, or cannot be represented as a typed Core-owned proposal.
- **User authority and policy**: Operators explicitly enable local profile-scoped adapters. Deny outcomes win over rewrite, simulate, or proposal. Adapter proposals are requests only; Go Core and later Manager plan/apply surfaces decide whether any capability can execute.
- **Generality and provider scope**: The runtime is generic. The root-sensitive adapter is the first built-in provider/example and must not turn package managers, service managers, firewalls, or distributions into Core semantics.
- **Evidence surface**: Adapter decisions appear in audit, Manager API summaries, TUI/WebUI surfaces that show command proxy activity, and export/share artifacts after redaction. Root-sensitive evidence states whether it is intent-only or backed by later privilege separation.
- **Secret/redaction boundary**: Hideout control-plane credentials, broker/UI tokens, `HIDEOUT_SECRET_*` backing material, and generated control-plane identifiers must not appear in adapter audit, simulated output, proposal resources, UI output, or exported artifacts.
- **Backend/gate expectation**: Gate 0 and unit tests are required for schema, validation, redaction, and command proxy behavior. Lima smoke is required only for the built-in root-sensitive command-proxy path as intent capture; no isolation claim is promoted by 008.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST allow a profile to declare local command adapters with adapter identity, artifact location, digest, command symbol matches, entrypoint, and allowed proposal capabilities.
- **FR-002**: System MUST require explicit operator enablement before any local adapter can affect a command invocation.
- **FR-003**: System MUST route a command symbol registered to an enabled adapter through that adapter before the target command can execute.
- **FR-004**: System MUST preserve existing behavior for unregistered command symbols.
- **FR-005**: System MUST reject profile configuration that assigns the same command symbol to more than one adapter or command-proxy owner.
- **FR-006**: System MUST provide adapters raw command arguments and working-directory context needed for classification, while exposing only environment summaries and never raw inherited environment values.
- **FR-007**: System MUST fail closed when an adapter is missing, digest-mismatched, disabled, malformed, over execution limits, or returns invalid output.
- **FR-008**: System MUST support adapter outcomes for deny, simulate, rewrite guest command, and propose capability.
- **FR-009**: System MUST validate every adapter outcome against a strict schema and reject unknown fields, unknown outcomes, unsupported routes, or undeclared proposal capabilities.
- **FR-010**: System MUST allow automatic guest-command rewrite only when the rewrite remains non-privileged guest execution and requests no new host, backend, network, filesystem, or privileged authority.
- **FR-011**: System MUST forbid root-sensitive adapters from simulating successful system mutation.
- **FR-012**: System MUST treat propose-capability outcomes as non-applied proposals in 008; no adapter outcome may directly execute privileged setup, HostFS write apply, host execution, endpoint exposure, or network mutation.
- **FR-013**: System MUST include a built-in root-sensitive adapter covering common escalation, package-manager, mount, network, resolver, service-manager, and system-management command symbols.
- **FR-014**: System MUST label root-sensitive adapter results as command-intent capture unless 009 later reports enforced privilege separation.
- **FR-015**: System MUST record adapter decisions with adapter identity, digest, profile, session, command symbol, argument summary, outcome, reason, proposed intent, and failure reason when applicable.
- **FR-016**: System MUST apply deterministic control-plane redaction to adapter audit, UI output, simulated output, proposal resources, and exported evidence.
- **FR-017**: System MUST prevent JavaScript adapters from reading arbitrary files, opening network connections, spawning processes, accessing raw Hideout tokens, or mutating profile state.
- **FR-018**: System MUST expose adapter configuration and recent adapter decisions through existing local management surfaces without adding a raw profile writer or arbitrary host execution API.
- **FR-019**: System MUST keep `host.open` command proxy behavior compatible unless the operator explicitly changes those command symbols.
- **FR-020**: System MUST document that command proxy does not intercept absolute paths, syscall-level behavior, or guest-root behavior outside command-name routing.

### Key Entities *(include if feature involves data)*

- **Command Adapter**: A local profile-scoped script artifact with identity, digest, entrypoint, command matches, declared proposal capabilities, and enabled state.
- **Adapter Invocation**: One routed command attempt, including profile, session, command symbol, arguments, working directory summary, adapter identity, and evaluation status.
- **Adapter Outcome**: The validated result of an adapter invocation: deny, simulate, rewrite guest command, or propose capability.
- **Capability Proposal**: A non-applied request for a Go Core-owned capability, with generic capability name, provider-specific intent payload, reason, and suggestions.
- **Root-Sensitive Intent**: A classified command attempt that indicates desired guest privilege or system mutation, recorded as intent capture rather than enforced containment.
- **Adapter Evidence**: Audit and UI/export records derived from adapter invocation and outcome, after deterministic control-plane redaction.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of registered adapter command invocations either produce a schema-valid outcome or fail closed before target-side command execution.
- **SC-002**: 100% of adapter digest mismatches, missing entrypoints, invalid outputs, undeclared capability proposals, and execution-limit failures are denied with audit evidence.
- **SC-003**: Existing `open` and `xdg-open` command proxy tests continue to pass without behavior changes in default profiles.
- **SC-004**: The built-in root-sensitive adapter classifies at least one escalation command, one package-manager command, one network mutation command, one resolver command, and one service-manager command in automated tests.
- **SC-005**: 0 automated tests or evidence outputs claim that 008 blocks root escalation; all root-sensitive proof text uses intent-capture wording unless an enforced 009 status is present.
- **SC-006**: 100% of adapter audit/export fixtures containing Hideout control-plane tokens, backing secret names, or token-shaped values are redacted before leaving local evidence.
- **SC-007**: Operators can review adapter identity, digest, command matches, requested proposal capabilities, and last decision status from a local management surface.
- **SC-008**: Unsupported or unimplemented capability proposals remain non-applied and produce a clear deny or proposal-unavailable result.

## Assumptions

- 008 implements the adapter runtime first; 009 privilege separation and 010 HostFS write apply are separate later features.
- v1 adapters are local, profile-scoped, digest-pinned, and explicitly enabled. Marketplace trust, remote distribution, signing, revocation, and publisher identity are later work.
- JavaScript adapters may classify and propose but never execute authority.
- Provider-specific details belong in adapter intent payloads and examples, not in Core action names.
- The built-in root-sensitive adapter is useful even before 009 because it captures intent and produces audit, but it is not a security boundary by itself.
