# Contract: Doctor Report

<!-- markdownlint-disable MD013 -->

## Report Shape

The report remains compatible with the existing 015 `schemas/doctor-report.schema.json` shape. 018 adds structured detail conventions rather than a new schema version unless implementation requires a schema bump.

Required top-level fields:

- `schema`
- `generatedAt`
- `profile`
- `backend`
- `level`
- `summary`
- `findings`
- `redaction`

Optional top-level fields:

- `features`
- `evidence`

## Finding Detail Conventions

Feature/deep findings SHOULD use these optional detail keys:

```json
{
  "observedFacts": ["local fact"],
  "candidateCauses": ["possible explanation, not definitive root cause"],
  "gateRequired": ["Gate 3 DNS proof required for release claim"]
}
```

Rules:

- `observedFacts` contains facts read from local state.
- `candidateCauses` contains non-definitive explanations only.
- `gateRequired` contains proof that doctor does not attempt to run.
- `nextActions` remains the machine-readable command/guidance surface.

## Redaction Boundary

The following MUST be absent as raw values from report JSON, human rendering, evidence files, audit/recovery evidence, and export-selected doctor reports:

- broker/UI tokens;
- decision claim tokens;
- proxy secrets and credentialed proxy URLs;
- `HIDEOUT_SECRET_*` backing values;
- generated machine ids;
- hidden runtime credential paths;
- provider-private refs.

Non-secret user/application data remains visible unless the export/share boundary later applies user-selected redaction.

## Evidence Write Contract

Evidence write MUST:

- serialize the redacted report only;
- use temp-write plus rename;
- leave no partial file on serialization or write failure;
- redact the evidence path in the report itself if the path contains hidden control-plane material.
