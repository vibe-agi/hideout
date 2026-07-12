# Data Model: Documentation Truth Gate

<!-- markdownlint-disable MD013 -->

## Claim Boundary

Fields:

- `claimId`: stable row id.
- `claim`: current product claim.
- `owner`: canonical doc or STATUS row.
- `proofRefs`: proof ids, gates, or test scripts.
- `nonClaims`: explicit limits.

Validation:

- Required 021-024 proof ids must appear.
- Every row must point to an owner and at least one proof or non-claim.

## Command Example Fixture

Fields:

- `id`: stable command id.
- `classification`: `execute-temp-store`, `parse-only`, `real-gate`, or
  `intentionally-not-executed`.
- `command`: argv array.
- `reason`: why this classification is safe.

Validation:

- `execute-temp-store` commands run with a temporary store.
- `parse-only` commands must return success.
- `real-gate` and `intentionally-not-executed` require a reason and are not run.

## Overclaim Finding

Fields:

- `file`
- `line`
- `category`
- `text`

Validation:

- Any finding fails the truth gate.

## Docs Truth Evidence Manifest

Required proof ids:

- `025.docs.claim-boundaries`
- `025.docs.overclaim-scan`
- `025.docs.command-examples`
- `025.docs.cross-doc-consistency`
