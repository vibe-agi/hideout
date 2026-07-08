# Data Model: Command Capability Adapters

<!-- markdownlint-disable MD013 -->

## Command Adapter

A local profile-scoped adapter artifact.

Fields:

- `id`: stable adapter identifier unique within the profile.
- `path`: local script artifact path, resolved relative to the profile
  directory unless absolute.
- `digest`: expected SHA-256 digest of the artifact bytes.
- `entrypoint`: JavaScript function name, v1 default `decideCommandAdapter`.
- `enabled`: boolean operator-controlled enablement flag.
- `commands`: non-empty list of simple command symbols owned by this adapter.
- `allowedProposalCapabilities`: generic capability names the adapter may
  propose. Empty means the adapter may deny, simulate, or rewrite only.
- `description`: optional local display text.

Rules:

- `id` and command symbols are normalized simple names.
- The same command symbol cannot be owned by another adapter or by a host-open
  command-proxy registration in the same profile.
- The artifact digest is verified before invocation.
- Disabled adapters do not affect command routing.

## Command Owner

The compiled routing record for one command symbol.

Fields:

- `command`: simple command name.
- `ownerType`: `host-open` or `adapter`.
- `adapterId`: present only for adapter owners.
- `route`: existing host-broker route or adapter route.
- `action`: current action for host-open, or adapter invocation marker.

Rules:

- Command ownership is compiled during profile validation.
- Duplicate ownership is a profile error, not runtime precedence.
- Default `open` and `xdg-open` remain host-open unless explicitly changed.

## Adapter Invocation

One registered command attempt routed to an adapter.

Fields:

- `profile`: profile name.
- `session`: session ID.
- `adapterId`: adapter identifier.
- `adapterDigest`: verified digest used for this invocation.
- `command`: command symbol.
- `argv`: raw argv vector.
- `cwd`: guest working directory string.
- `envSummary`: key/class/count metadata without raw values.
- `workspaceSummary`: guest root and workspace mode metadata.
- `networkSummary`: current network mode metadata.
- `startedAt` and `durationMs`: local timing metadata.

Rules:

- Invocation context must not contain raw broker tokens, UI tokens,
  `HIDEOUT_SECRET_*` values, generated machine IDs, or raw inherited env values.
- Invocation failure before script execution still emits evidence.

## Adapter Outcome

The strict Go-validated result returned by an adapter.

Common fields:

- `outcome`: one of `deny`, `simulate`, `rewriteGuest`, `proposeCapability`.
- `reason`: human-readable explanation.
- `audit`: optional JSON object, redacted by Go before evidence leaves local
  audit.

Outcome-specific fields:

- `deny`: optional `exitCode`, `stderr`, and `intent`.
- `simulate`: required `exitCode`, optional `stdout` and `stderr`, and no system
  mutation claim for root-sensitive commands.
- `rewriteGuest`: required replacement `argv` and optional `cwd`, validated as
  non-privileged guest execution only.
- `proposeCapability`: required `capability`, `intent`, and optional
  `suggestions`; non-applied in 008.

Rules:

- Unknown fields are rejected.
- Unknown outcomes are rejected.
- Unsupported routes/actions are rejected.
- Undeclared proposal capabilities are rejected.

## Capability Proposal

A non-applied request for a Go Core-owned capability.

Fields:

- `capability`: generic capability name declared by the adapter profile entry.
- `intent`: provider-specific JSON payload.
- `reason`: explanation shown in evidence and local management surfaces.
- `suggestions`: optional operator guidance.
- `status`: `proposed`, `unavailable`, or `denied`.

Rules:

- 008 never applies a capability proposal.
- Provider-specific details stay in `intent`; Core capability names remain
  generic.
- Unsupported proposals produce a clear unavailable/denied result.

## Root-Sensitive Intent

A classified command-name attempt indicating desired guest privilege or system
mutation.

Fields:

- `category`: `escalation`, `package-manager`, `mount`, `network-mutation`,
  `resolver`, `service-manager`, or `system-management`.
- `command`: command symbol.
- `argvSummary`: bounded argument summary.
- `classification`: provider-specific detail, such as package install or
  firewall mutation.
- `separationStatus`: `intent-only`, `enforced-009`, `degraded-009`, or
  `unknown`.

Rules:

- Before 009 enforced status exists, evidence must say intent capture.
- Root-sensitive adapters cannot simulate successful system mutation.
- Absolute-path and syscall bypasses are documented non-claims.

## Adapter Evidence

Audit/UI/export data derived from invocation and outcome.

Fields:

- `version`: evidence schema version.
- `profile`, `session`, `adapterId`, `adapterDigest`.
- `command`, `argvSummary`, `cwdSummary`.
- `outcome`, `reason`, `failureReason`.
- `intent`, `proposal`, `rewriteSummary`, `simulationSummary` where
  applicable.
- `separationStatus` for root-sensitive evidence.

Rules:

- Deterministic control-plane redaction applies before evidence leaves the
  trusted Go path.
- Local audit records user command data verbatim after control-plane strip;
  export/share owns lossy user-data redaction.
- Evidence must not claim root containment in 008.

## Manager Adapter Plan

Typed plan for profile adapter mutation.

Operations:

- `add-local`: add a local adapter declaration after digest calculation.
- `enable`: enable a configured adapter.
- `disable`: disable a configured adapter.
- `refresh-digest`: update the digest after explicit operator review.
- `remove`: remove an adapter and release its command symbols.

Rules:

- Plan output includes before/after command ownership, digest, commands,
  allowed proposal capabilities, and risk warnings.
- Apply recomputes the plan under profile mutation lock.
- Apply rejects drift that changes command ownership or artifact digest unless
  the operation explicitly refreshes the digest.
