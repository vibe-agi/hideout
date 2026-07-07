<!-- markdownlint-disable MD013 -->

# Contract: Exported Artifact

Contract for the single local file an export produces. A JSON schema
(`schemas/export-artifact.schema.json`, `additionalProperties:false`) fixes the
envelope so Gate 0 can validate it and the exported-artifact-cleanliness check can
assert it.

## Envelope

- `version`: string, the artifact schema version (for example
  `hideout.export/v1`).
- `provenance`: object —
  - `source`: `audit` | `bundle` | `boundary-summary`.
  - `commit`: string, present when the exporter knows the Hideout build commit
    that produced the artifact. For bundle exports, the source bundle's own
    `git.commit` remains in the redacted body as source evidence.
  - `createdAt`: timestamp.
  - `redactionStages`: array; MUST include `control-plane` (always) and, when a
    user-data policy ran, one entry per applied policy with its `id` and `sha256`
    (mirroring how the broker records `policyScripts` in audit details). A
    cross-profile `audit` export MAY record several policies (one per profile
    whose events were redacted).
  - `decision`: `redact` | `acknowledge-full-fidelity`.
- `recordCount`: integer ≥ 0.
- `body`: the redacted evidence payload (shape depends on `source`).
- `notice`: optional string; carries the "0 records matched" notice for an empty
  export.

## Body By Source

- `audit`: an array of redacted `audit.Event` records — control-plane stripped
  (re-asserted) and user-data redacted per the decision; the Non-Redactable
  Evidentiary Set is preserved (not deletable by a selection).
- `bundle`: the redacted `hideout.release-dogfood.v1` manifest with its referenced
  logs inlined-and-redacted; no `auditPath`/log reference points at un-exported
  local data (FR-006).
- `boundary-summary`: the redacted `BoundarySummary`; its `auditPath` is either
  resolved-and-redacted into the body or removed — never emitted as a dangling
  local path.

## Guarantees

- No Hideout-minted control-plane secret appears in any field (`HIDEOUT_SECRET_*`
  backing material, `cap_`/`ui_` token values, control-plane detail field names,
  generated machine-id) — SC-001.
- No user/application data appears except what the decision permits: a
  policy-scrubbed selection is absent; residual appears only under
  `acknowledge-full-fidelity` — SC-006, SC-008.
- A field in the Non-Redactable Evidentiary Set is never emitted un-redacted after
  a selection targeted it; that case fails closed and no artifact is written —
  SC-007.
- The artifact validates against `schemas/export-artifact.schema.json`.

## Failure (no artifact)

- On any fail-closed condition (stage 1 error, missing decision with user data,
  policy error, evidentiary-field selection), NO artifact file is written and the
  export exits non-zero with a specific diagnostic (FR-004). Partial files MUST
  NOT remain (write to a temp path and rename only on success).
