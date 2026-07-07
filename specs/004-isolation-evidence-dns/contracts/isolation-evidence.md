<!-- markdownlint-disable MD013 -->

# Contract: Isolation Evidence Artifact

Contract for recording isolation-sensitive gate results into the existing
release-evidence bundle. Not a new bundle format.

## Extension, Not Replacement

- The artifact MUST extend the existing `hideout.release-dogfood.v1` manifest
  and its schema, not introduce a separate format.
- Because the schema is `additionalProperties:false` throughout, every new
  field MUST be added to `schemas/release-dogfood.schema.json` in lockstep, and
  the manifest MUST validate against the updated schema.

## Per-Gate Results

- The manifest MUST record, per isolation gate (`gate2-lima`,
  `gate3-hidden-proxy`, `gate4-host-escape`, `env-image`): id, backend,
  environment name (when applicable), result (`passed` | `failed` | `not-run`),
  reason (required for `not-run`), audit path reference, and Boundary Summary
  reference. The isolation gates run against ephemeral per-gate stores, so the
  durable references are: `environmentName` captured from the gate's run output,
  `auditPath` pointing at the retained release-evidence log, and
  `boundarySummary` a marker that the run's Boundary Summary was present. The
  orchestrator populates these from each gate's captured output.
- A gate that did not run MUST be recorded as `not-run` with a reason (for
  example Gate 4 without host prerequisites, env-image without a declared image
  URL), never omitted or marked passed.
- Native MUST NOT appear as the backend for a passed isolation claim.

## Environment Snapshot

- The manifest MUST record an environment snapshot: proxy mode and operator
  proxy identity, host prerequisites present, and the uncontrolled
  external-network / DNS-upstream context.
- The snapshot scopes repeatability: two runs with the same commit, backend,
  proxy mode, and host prerequisites MUST produce an equivalent artifact; the
  external context is recorded but excluded from the equivalence judgment.

## Per-Gate Emission

- When `HIDEOUT_RELEASE_EVIDENCE_DIR` is set, each isolation gate MUST write a
  machine-readable result to `$HIDEOUT_RELEASE_EVIDENCE_DIR/gates/<gate>.json`
  with `{id, result, reason, auditPath, boundarySummary, environmentName}`.
- The manifest writer MUST aggregate those files; each gate's human-readable
  output remains unchanged.
- `env-image` MUST run in the real gate sequence (not only the print-plan path)
  and MUST record `not-run` when no image URL is declared rather than
  hard-exiting.

## Redaction

- Every field in the artifact MUST pass the deterministic redaction contract:
  only Hideout-minted control-plane material is stripped; user/application data
  is preserved verbatim on local surfaces; the proxy stays redacted as today.
