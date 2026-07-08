# Data Model: Doctor Diagnostics And Recovery

<!-- markdownlint-disable MD013 -->

## Doctor Request

Operator-selected scope for a doctor run.

Fields:

- `profile`: profile name, defaulting to `default`.
- `backend`: requested backend or auto/default.
- `workspace`: host workspace path for workspace safety checks.
- `level`: `light` or `deep`; default `light`.
- `features`: zero or more feature selectors such as `dns`, `hostfs`, `lima`, `privilege`, `adapters`, `packaging`, `daemon`, `decisions`, `export`, or `cleanup`.
- `format`: `human` or `json`.
- `fixMode`: empty, `dry-run`, or `apply`.
- `evidence`: whether to save a local doctor report for explicit export/share selection.

Validation:

- Unknown level or feature selector fails before checks run.
- `fixMode=apply` must be explicit; report mode is read-only.
- Deep checks run only when selected by level or feature.

## Doctor Check

Static definition of one diagnostic check.

Fields:

- `id`: stable kebab-case id.
- `category`: package, helper, store, profile, daemon, backend, privilege, dns, hostfs, adapters, decisions, export, cleanup, or evidence.
- `title`: user-facing check name.
- `level`: minimum level where the check runs by default.
- `features`: feature selectors that include the check.
- `required`: whether failure contributes to nonzero exit for the selected request.
- `prerequisites`: facts needed before the check can run.
- `safeActions`: zero or more safe recovery hints.

Validation:

- IDs are unique.
- Every finding references a known check id.
- Deep-only checks are not included in default light runs unless selected by feature.

## Doctor Finding

Runtime result of one check.

Fields:

- `checkId`: stable check id.
- `category`: copied from check definition.
- `status`: `pass`, `warn`, `error`, `skipped`, or `unsupported`.
- `severity`: `info`, `warning`, or `error`.
- `required`: whether this finding affects exit status.
- `summary`: concise human summary.
- `details`: structured redacted facts.
- `nextActions`: concrete commands or manual guidance.
- `evidenceRefs`: local audit/evidence paths or markers after redaction.

Validation:

- `error` plus `required=true` causes report failure.
- `warn` never causes failure unless a selected deep/feature scope marks it required.
- Details are redacted before rendering or writing.

## Doctor Report

Complete diagnostic output.

Fields:

- `schema`: `hideout.doctor-report/v1`.
- `generatedAt`: timestamp.
- `profile`: profile name.
- `backend`: selected/resolved backend.
- `level`: selected level.
- `features`: selected feature list.
- `summary`: counts by status and exit classification.
- `findings`: ordered list of Doctor Findings.
- `redaction`: redaction version/summary.
- `evidence`: optional saved report path/provenance when explicitly requested.

Validation:

- Human and JSON renderers consume this same object.
- JSON validates against `schemas/doctor-report.schema.json`.
- Report contains no control-plane secret values.

## Recovery Plan

Safe repair plan derived from eligible findings.

Fields:

- `mode`: `dry-run` or `apply`.
- `sourceReport`: doctor report id or generated timestamp.
- `initPlan`: typed InitTask plan where applicable.
- `eligibleFindings`: finding ids/check ids that can be repaired safely.
- `refusedFindings`: finding ids/check ids that require manual/high-risk action.
- `auditPath`: local audit path for applied repairs.
- `result`: pending, applied, refused, or failed.

Validation:

- Dry-run writes no durable state.
- Apply delegates to typed safe repairs only.
- Unsafe fixes remain refused with guidance.

## Diagnostic Evidence Selection

Explicit choice to include a doctor report in an export/share artifact.

Fields:

- `reportPath`: local saved doctor report.
- `selectedBy`: local operator surface.
- `exportId`: export/share artifact id when produced.
- `provenance`: report schema, generated time, profile, and checksum.

Validation:

- Doctor reports are never included in exports unless selected.
- Export-selected reports pass the same control-plane redaction checks.
