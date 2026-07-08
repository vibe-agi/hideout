# Research: Command Capability Adapters

<!-- markdownlint-disable MD013 -->

## Decision 1: Reuse The Existing Goja Policy Runtime

**Decision**: Execute adapter JavaScript through the existing constrained Goja
runtime in `internal/policy`, extending it with a command-adapter entrypoint
instead of adding Node, WASI, subprocess plugins, or browser execution.

**Rationale**: The project already has deterministic script execution,
source/output limits, strict JSON decoding, and SDK isolation for
`decideCommand` and `redactAudit`. Reusing that path keeps JavaScript as a
classification/proposal layer and preserves Go Core as the authority boundary.

**Alternatives Rejected**:

- **Node subprocess**: adds process spawning, filesystem/network exposure, and
  installability burden.
- **WASI/plugin runtime**: useful later for broader ecosystems, but too large
  for local profile-scoped adapters.
- **Compiled Go adapters**: moves flexible provider logic into Core and breaks
  the constitution's preference for constrained JS decision points.

## Decision 2: Introduce A Dedicated Adapter Outcome Envelope

**Decision**: Define a new strict adapter outcome envelope instead of returning
the existing `policy.Proposal` directly.

**Rationale**: Current proposals model policy decisions for existing actions.
008 needs outcomes that are not all policy decisions: simulation, guest-command
rewrite, and non-applied capability proposals. A dedicated envelope lets Go
validate outcome-specific fields, reject unknown fields, and preserve the rule
that proposals do not execute in 008.

**Alternatives Rejected**:

- **Overload `policy.Proposal`**: would blur deny/allow decisions with
  non-applied proposals and make it easier for JS to appear to execute
  authority.
- **Free-form adapter JSON**: fails the strict schema requirement and creates
  fragile tests.

## Decision 3: Profile-Scoped, Digest-Pinned Local Artifacts

**Decision**: A command adapter is a profile-scoped local artifact with an ID,
path, digest, entrypoint, command matches, allowed proposal capabilities, and
enabled state.

**Rationale**: The user is a local professional operator, and there is no public
marketplace yet. Digest pinning catches changed local artifacts before runtime.
Profile scoping keeps adapter effects explicit and reviewable.

**Alternatives Rejected**:

- **Global adapters**: too easy to affect profiles unexpectedly.
- **Remote adapter URLs**: create marketplace trust, revocation, and publisher
  identity requirements that are out of scope.
- **Unsigned mutable script paths**: break fail-closed review when the artifact
  changes.

## Decision 4: Reject Duplicate Command Ownership

**Decision**: A command symbol may have exactly one owner per profile across
host-open command proxy registrations and command adapters.

**Rationale**: Routing precedence would otherwise become policy. Rejection is
clearer, easier to test, and prevents an adapter from shadowing `open` or
`xdg-open` unless the operator explicitly reconfigures ownership.

**Alternatives Rejected**:

- **Priority order**: makes command behavior depend on list order and is harder
  to audit.
- **Allow adapter override by default**: risks silently changing existing
  `host.open` behavior.

## Decision 5: Adapter Context Is Raw Argv/CWD Plus Environment Summary

**Decision**: Adapters receive the command symbol, raw argv, working directory,
profile/session metadata, workspace summary, network summary, and environment
summary only. They do not receive raw inherited environment values.

**Rationale**: Raw argv and cwd are needed to classify command intent. Raw env
values can include credentials, proxy secrets, and application secrets, so the
adapter receives only keys/classes/counts/safe metadata.

**Alternatives Rejected**:

- **Full env pass-through**: leaks secrets into JavaScript and evidence.
- **No argv**: prevents useful command classification and makes the adapter
  ecosystem mostly decorative.

## Decision 6: Guest Rewrite Is Non-Privileged Only

**Decision**: `rewriteGuest` outcomes may rewrite only to non-privileged guest
execution and must request no host, backend, filesystem, network, endpoint, or
privileged setup authority.

**Rationale**: Rewrite is an ergonomics feature, not an authority grant. Go can
validate that the outcome remains inside the existing guest execution route.

**Alternatives Rejected**:

- **Allow rewrite to any command**: root-sensitive adapters could simulate or
  route privileged mutation without Manager review.
- **Disallow rewrite entirely**: loses a useful adapter outcome for harmless
  command compatibility.

## Decision 7: Root-Sensitive Adapter Is Intent Capture Before 009

**Decision**: The built-in root-sensitive adapter classifies command-name
attempts such as escalation, package management, mount, network mutation,
resolver changes, service management, and system management. Before 009 reports
enforced privilege separation, its evidence must say intent capture, not root
containment.

**Rationale**: Existing command proxy cannot intercept absolute paths,
guest-root behavior, or syscalls. 009 owns the dual-identity privilege
separation required for a stronger boundary. 008 still adds value by surfacing
intent and making later capability proposals explicit.

**Alternatives Rejected**:

- **Claim root blocking in 008**: false; command-name routing is bypassable.
- **Omit root-sensitive adapter**: loses the highest-value first adapter and
  delays audit visibility unnecessarily.

## Decision 8: Capability Names Stay Generic

**Decision**: Core capability names remain generic, such as
`guest.privilege.plan` or later feature-owned primitives. Provider-specific
details such as apt packages, resolver commands, or service names live in the
adapter intent payload.

**Rationale**: Core should not learn package-manager or distro semantics. This
keeps provider logic in local adapters while allowing Go to validate generic
authority classes.

**Alternatives Rejected**:

- **Core action per package manager**: turns examples into product semantics.
- **Free-form capability strings**: lets JS invent authority.

## Decision 9: Manager Plan/Apply Owns Adapter Enablement

**Decision**: Adapter configuration changes use a typed Manager plan/apply
operation, with CLI as the first consumer and WebUI/TUI display support. No raw
profile writer or arbitrary script install API is added.

**Rationale**: Profile changes alter command routing and must be reviewable,
auditable, and parity-checked like other profile mutations.

**Alternatives Rejected**:

- **Edit profile JSON directly**: bypasses validation and audit.
- **CLI-only mutation**: violates the shared Manager product path.

## Decision 10: Evidence Uses Existing Redaction And Export Boundaries

**Decision**: Adapter audit is emitted through existing audit paths with
`RedactDetails`, surfaced through local UI/Manager summaries, and exported
through 005 export/share redaction.

**Rationale**: Adapter invocations can contain sensitive user data in argv, but
Hideout control-plane secrets must never leak. Reusing the established audit
and export model avoids inconsistent redaction behavior.

**Alternatives Rejected**:

- **Separate adapter logs**: creates a second evidence model and export gap.
- **Heuristic local redaction of user data**: contradicts the local-audit
  contract; export/share owns lossy user-data redaction.
