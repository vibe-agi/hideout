# Data Model: Doctor Deep Diagnostics

<!-- markdownlint-disable MD013 -->

## Doctor Finding

Represents one diagnostic result.

Fields:

- `checkId`: stable lowercase id, for example `feature-packaging`.
- `category`: stable category such as `packaging`, `decisions`, or `dns`.
- `feature`: implied by category for feature diagnostics.
- `status`: `pass`, `warn`, `error`, `skipped`, or `unsupported`.
- `severity`: `info`, `warning`, or `error`.
- `required`: whether this finding contributes to nonzero exit.
- `summary`: redacted one-line summary.
- `details.observedFacts`: optional array of factual local observations.
- `details.candidateCauses`: optional array of non-definitive candidate causes.
- `details.gateRequired`: optional array of proof items that require Gate 2/Gate 3 or another explicit product gate.
- `nextActions`: redacted operator commands or guidance.
- `evidenceRefs`: redacted local evidence references.

Validation:

- All strings pass deterministic control-plane redaction before render/write.
- Candidate causes must not be phrased as definitive root cause.
- Gate-required details must not be marked as passed release evidence.

## Feature Diagnostic

Represents a selected feature area.

Supported features:

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

Validation:

- Every supported selector emits at least one finding.
- Single-feature mode emits no unrelated feature-only findings.
- Deep mode emits all supported feature diagnostics.
- Each feature emits either a local observed fact or a gate-required marker.

## Doctor Report

Represents the renderable report.

Fields:

- `schema`
- `generatedAt`
- `profile`
- `backend`
- `level`
- `features`
- `summary`
- `findings`
- `redaction`
- `evidence`

Validation:

- Human and JSON output use the same ordered findings.
- Summary exit code derives from required error findings.
- Evidence writes are temp-write plus rename and leave no partial report on failure.

## Recovery Guidance

Represents the next action attached to a finding.

Types:

- `typed-safe-fix`: existing `doctor --fix` InitTask-style recovery.
- `command-guidance`: explicit command such as `hideout package repair --prefix ...`.
- `gate-required`: real proof path such as Gate 3 for DNS mediation.
- `manual-high-risk`: action doctor refuses to apply automatically.

Validation:

- 018 adds no new automatic repair type.
- Guidance must not delete evidence, purge store state, recreate environments, run gates, or mutate HostFS by default.

## Redaction Probe

Represents synthetic secret-like values used only by tests.

Probe classes:

- Broker/UI/claim token shapes.
- `HIDEOUT_SECRET_*` backing values.
- Proxy credentials and URLs.
- Generated machine id shapes.
- Hidden runtime credential paths.
- Provider-private refs.

Validation:

- Raw probe values are absent from human output, JSON, evidence files, audit/recovery evidence, and export-selected doctor reports.
- Non-secret user data in the same finding remains visible.
