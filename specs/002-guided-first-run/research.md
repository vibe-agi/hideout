<!-- markdownlint-disable MD013 -->

# Research: Tool Model Cleanup

## Decision: Remove legacy tool-supply compatibility instead of migrating it

**Rationale**: Hideout is not released, and the architecture has deliberately
moved away from package-manager/provider/preset tool installation. Keeping
compatibility would preserve an authority path that the constitution no longer
accepts. Explicit rejection is safer and clearer than silent migration because
old fields described installation intent, while the new model describes
diagnostic expectations only.

**Alternatives considered**:

- Silently migrate `npmGlobals` or presets into `expectedCommands`: rejected
  because it would turn an installer declaration into a diagnostic declaration
  and hide an authority change from the operator.
- Keep old fields but ignore them: rejected because it creates false readiness
  and makes stale profiles appear accepted.
- Keep provider execution for local users only: rejected because product code
  would still own package-manager behavior.

## Decision: `tools.expectedCommands` is diagnostic-only state

**Rationale**: Operators need a generic way to declare that a guest environment
is expected to contain commands such as `claude`, `codex`, or an internal CLI.
That declaration helps doctor/status output and environment fingerprints
without making Hideout responsible for acquiring the command. The field must
therefore carry command names and optional labels only, not packages, installers,
providers, URLs, scripts, or host paths.

**Alternatives considered**:

- Model expected commands as package specs: rejected because that recreates the
  removed provider model.
- Store shell snippets that test or install commands: rejected because it adds
  arbitrary execution and breaks typed authority.
- Defer expected commands entirely: rejected because the cleanup needs a
  replacement diagnostic concept so users can express command readiness without
  installer semantics.

## Decision: Missing expected commands report readiness failure but do not fix it

**Rationale**: A missing expected command is useful diagnostic information. If
the command is also the requested target command, the run must fail closed
before claiming readiness. But the repair path belongs to operator-controlled
environment setup, base images, or later named/global environment design, not
to this cleanup slice.

**Alternatives considered**:

- Auto-install missing commands during doctor: rejected because doctor would
  become a package manager.
- Allow run to fall back to a host command: rejected as a privacy-boundary
  violation.
- Treat missing expected commands as warnings only: rejected when the missing
  command is required for the target run.

## Decision: Validate command names conservatively

**Rationale**: Expected-command declarations name guest commands; they are not
argv arrays, scripts, file paths, or package coordinates. Validation should
reject empty names, path-like values, values containing whitespace or shell
metacharacters, and argument-bearing strings. This keeps the field from
becoming a disguised command proxy or installer recipe.

**Alternatives considered**:

- Accept arbitrary strings for flexibility: rejected because it blurs command
  names with scripts and arguments.
- Accept full argv arrays: rejected because argv intent belongs to command
  execution/policy, not diagnostic setup state.
- Maintain a fixed allowlist of known products: rejected because Hideout must
  remain product-generic.

## Decision: Validation evidence comes from the same product paths users hit

**Rationale**: This feature exists to remove drift between docs, specs, and
code. Tests should exercise actual profile/schema/app surfaces and repository
scans, not independently recompute what those surfaces "should" contain. This
matches the constitution's evidence rule.

**Alternatives considered**:

- Document the new model without tests: rejected because old tool surfaces
  already reappeared through stale artifacts.
- Rely only on broad text grep: rejected because schema/profile behavior must
  also reject legacy fields.
- Require Lima gates: rejected because this cleanup does not prove runtime
  isolation or environment materialization.
