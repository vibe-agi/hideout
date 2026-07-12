# Data Model: Error Codes And Recovery Hints

<!-- markdownlint-disable MD013 -->

## Recovery Code

- `code`: subsystem-namespaced stable id.
- `subsystem`: package, init, privilege, release, HostFS, decision, or support.
- `severity`: info, warning, or error.
- `reason`: short redacted explanation.
- `hint`: one-line recovery hint.
- `nextActions`: optional copyable commands or docs refs.
- `docsRefs`: optional docs paths for user-facing explanation.

Validation:

- Codes are unique.
- Codes use lowercase subsystem namespaces.
- Reason and hint are non-empty for v1 public codes.
- Next actions must not contain control-plane secret material.

## Recovery Record

Rendered instance of a code on a host-visible surface.

- `code`: registry code.
- `reason`: redacted reason for this occurrence.
- `hint`: redacted hint.
- `nextActions`: redacted actions.
- `evidenceRefs`: existing report/evidence refs where available.

Validation:

- Code must exist in registry.
- Redaction applies before JSON/human output.

## Coded Doctor Finding

Existing doctor finding plus optional recovery fields.

- Existing fields remain valid.
- `code`, `reason`, and `hint` are optional.
- `nextActions` and `evidenceRefs` keep existing semantics.

Validation:

- JSON schema accepts coded findings.
- Human render prints code, reason, hint, next actions, and evidence refs.
- Uncoded findings remain valid.

## Code Registry View

Deterministic JSON view used by docs truth and tests.

- `schema`: `hideout.recovery-codes/v1`
- `codes`: sorted recovery code entries.
