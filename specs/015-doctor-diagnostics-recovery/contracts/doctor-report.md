# Contract: Doctor Report

<!-- markdownlint-disable MD013 -->

## Schema

Doctor JSON reports validate against `schemas/doctor-report.schema.json`.

Top-level fields:

- `schema`: `hideout.doctor-report/v1`.
- `generatedAt`: RFC 3339 timestamp.
- `profile`: profile name.
- `backend`: requested/resolved backend.
- `level`: `light` or `deep`.
- `features`: selected feature names.
- `summary`: counts and exit classification.
- `findings`: ordered list of findings.
- `redaction`: redaction summary.

## Finding Fields

Each finding includes:

- `checkId`: stable id.
- `category`: product category.
- `status`: `pass`, `warn`, `error`, `skipped`, or `unsupported`.
- `severity`: `info`, `warning`, or `error`.
- `required`: boolean.
- `summary`: concise message.
- `details`: structured redacted facts.
- `nextActions`: list of concrete next actions.
- `evidenceRefs`: list of local evidence/audit references or markers.

## Stability

- Check ids are stable across 015.
- JSON field names are stable enough for Gate 0/smoke assertions.
- 016 may freeze the compatibility matrix and longer-term report ABI.

## Redaction

The report must not contain:

- broker or UI token values;
- generated machine ids;
- raw `HIDEOUT_SECRET_*` backing values;
- proxy secret values;
- hidden runtime credential paths.

User/application data is not guessed away in local reports unless it crosses the export/share boundary.

## Export Selection

Doctor reports enter export/share artifacts only through explicit source
selection:

```text
hideout audit export --source doctor-report --doctor-report <report.json> --out <artifact.json>
```

Unrelated `audit`, `bundle`, and `boundary-summary` exports must not discover or
embed doctor reports implicitly.
