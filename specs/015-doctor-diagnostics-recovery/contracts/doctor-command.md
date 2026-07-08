# Contract: Doctor Command

<!-- markdownlint-disable MD013 -->

## Command Shape

```text
hideout doctor [--profile <name>] [--backend <name>] [--workspace <path>]
               [--level light|deep] [--feature <name>]...
               [--format human|json] [--evidence-out <path>]

hideout doctor --fix --dry-run [same scope flags]
hideout doctor --fix --apply [same scope flags]
```

## Scope Rules

- No level flag means `light`.
- No feature flag means run the checks selected by the level.
- `--feature <name>` includes that feature's local checks or explicit
  gate-required marker even in light mode.
- `--level deep` includes deep checks, but checks with unavailable prerequisites
  report error, skipped, or unsupported; they do not silently fall back to weak
  evidence.
- Supported feature names for v1: `dns`, `hostfs`, `lima`, `privilege`, `adapters`, `packaging`, `daemon`, `decisions`, `export`, and `cleanup`.

## Output Rules

- Human output is the default.
- `--format json` prints a `hideout.doctor-report/v1` object.
- Human and JSON output must represent the same findings.
- Required errors exit nonzero.
- Warnings/degraded states exit zero unless the selected scope marks that check required.

## Recovery Rules

- `--fix --dry-run` prints a Recovery Plan and writes no durable state.
- `--fix --apply` applies only safe typed repairs.
- Unsafe fixes print guidance and remain refused.
- `--fix` without `--dry-run` or `--apply` is invalid.

## Evidence Rules

- `--evidence-out <path>` writes a redacted doctor report that may later be explicitly selected for export/share.
- Doctor reports are not automatically attached to unrelated exports.
- Evidence paths and report details must be control-plane redacted.
