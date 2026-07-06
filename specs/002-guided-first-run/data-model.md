<!-- markdownlint-disable MD013 -->

# Data Model: Tool Model Cleanup

## ExpectedCommandDeclaration

Operator-authored diagnostic state naming a command expected to exist inside a
guest environment.

Fields:

- value: required command-name string. It is a command symbol, not a path, argv
  string, package name, object, note, per-command required flag, or script.

Validation rules:

- `name` must be non-empty.
- `name` must not contain path separators, whitespace, shell metacharacters,
  URL schemes, package coordinates, or arguments.
- `name` must not identify a package manager action or installer provider.
- The declaration must not contain package source, version, registry,
  installer, script, host path, or secret fields.
- Declarations are operator-authored profile/setup state; imported declarations
  do not grant runtime authority without the normal trust/review path.
- Whether a missing expected command blocks a run is derived from the requested
  target command and diagnostic context, not stored as a per-command field.

Relationships:

- Belongs to a profile or setup state.
- May contribute to an environment readiness fingerprint.
- Produces `ToolDiagnostic` facts when checked.

## ToolDiagnostic

User-facing and test-facing result for an expected command.

Fields:

- `command`: command name from `ExpectedCommandDeclaration`.
- `status`: `present`, `missing`, `not-checkable`, or `blocked`.
- `backend`: selected backend or environment context, if known.
- `reason`: short diagnostic message safe for user-facing output.
- `blocksRequestedRun`: derived boolean indicating that absence blocks the
  requested target command in the current diagnostic context.

Validation rules:

- `present` requires evidence from the selected guest/environment context.
- `missing` must not trigger installation or repair.
- `not-checkable` is used when the selected environment cannot be inspected.
- `blocked` is used when legacy unsupported tool-supply state prevents
  diagnostics from being trusted.
- A missing command required for the target run causes fail-closed behavior.

Relationships:

- Derived from one expected command and one environment/check context.
- May appear in doctor/status output and tests.
- Must not claim that Hideout installed, repaired, or materialized a command.

## DeprecatedToolSupplySurface

Any old npm/provider/preset field or command that would make Hideout acquire or
materialize guest tools.

Examples:

- npm global package declarations.
- tool presets.
- package-manager providers.
- package hints intended to drive installation.
- provider execution hooks or setup tasks that download/install tools.

Validation rules:

- Deprecated surfaces are rejected, not migrated.
- Rejection must occur before package-manager execution, profile mutation,
  backend preparation, or environment setup.
- Diagnostics must point to expected-command declarations and external
  environment setup, not to another installer path inside Hideout.

Relationships:

- May be found in old profile fixtures, stale API requests, CLI flags, docs, or
  tests.
- May remain only as explicit negative-test input or migration/removal prose.

## EnvironmentReadinessEvidence

Evidence that records what commands were expected and what diagnostic status
Hideout observed.

Fields:

- `expectedCommands`: list of expected command names.
- `diagnostics`: list of `ToolDiagnostic` results.
- `fingerprintInput`: representation of declarations that affect readiness or
  environment identity.
- `source`: profile, setup state, or operator command that supplied the
  declaration.

Validation rules:

- Evidence labels declarations as expectations.
- Evidence labels observed command state as present/missing/not-checkable.
- Evidence must not include "installed", "downloaded", "repaired", or
  "provisioned" claims for expected commands.
- Evidence must derive from the same validation/diagnostic path used by
  product behavior.

## State Transitions

```text
legacy field present
  -> rejected unsupported legacy field
  -> no mutation / no backend prepare / no install

expected command declared
  -> validated
  -> diagnostic check attempted
  -> present | missing | not-checkable

missing expected command required for target run
  -> fail closed before readiness claim

missing expected command not required for current run
  -> diagnostic only, no repair
```
