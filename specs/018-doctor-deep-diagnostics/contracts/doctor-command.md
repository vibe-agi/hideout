# Contract: Doctor Command

<!-- markdownlint-disable MD013 -->

## Command Forms

```text
hideout doctor [--level light|deep] [--feature <name> ...] [--format human|json] [--evidence-out <path>]
hideout doctor --fix --dry-run
hideout doctor --fix --apply
```

## Deep Mode

`--level deep` includes all supported feature diagnostics in addition to the existing local light checks.

Deep mode MUST:

- read local facts only;
- avoid starting guests, running Gate 2/Gate 3, probing network, mutating HostFS, deleting files, or changing package state;
- include observed facts, candidate causes, next actions, and gate-required markers where applicable;
- preserve warning/degraded exit-zero semantics unless a local required error is present.

## Feature Mode

`--feature <name>` runs only selected feature diagnostics plus the existing light checks.

Supported names:

- `adapters`
- `cleanup`
- `daemon`
- `decisions`
- `dns`
- `export`
- `hostfs`
- `lima`
- `packaging`
- `privilege`

Unknown feature names MUST be rejected during option parsing or produce an error finding before side effects.

## Human And JSON Parity

Human output and JSON output MUST contain the same diagnostic facts:

- check id;
- status;
- severity;
- required marker;
- summary;
- next actions.

Human output MAY format details compactly, but it MUST NOT omit feature/deep findings that appear in JSON.

## Exit Semantics

- Required local errors: nonzero exit.
- Warnings/degraded states: zero exit by default.
- Gate-required proof markers: zero exit unless paired with a required local error.
- Unsafe recovery refusal: nonzero only when invoked as an apply operation that cannot be safely represented.

## Redaction

All command output modes MUST use the same deterministic control-plane redaction boundary as the JSON report contract.
