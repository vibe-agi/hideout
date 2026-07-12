# Data Model: Test And Evidence Spine

<!-- markdownlint-disable MD013 -->

## Proof Requirement

Registry row owned by Go.

Fields:

- `featureId`: stable feature identifier, for example `021-ui-e2e-proof`.
- `proofId`: stable proof identifier.
- `layer`: one of `unit`, `gate0`, `product-hardening`, `real-gate`, or
  `release-candidate`.
- `requiredFor`: one of `local-dogfood`, `targeted-completion`,
  `release-candidate`, or `supporting-only`.
- `freshnessPolicy`: one of `same-commit`, `same-package`,
  `same-commit-and-package`, or `none`.
- `claimIds`: claim IDs covered by the proof requirement.
- `artifactPolicy`: one of `none`, `exists`, or
  `exists-and-digest-if-supplied`.

Validation:

- `featureId` and `proofId` must be non-empty.
- `proofId` must be unique.
- Enum fields must use known values.
- `claimIds` must be non-empty for proof IDs that satisfy product claims.
- Registry JSON output must be deterministic.

## Proof Registry JSON View

Read-only JSON generated from the Go registry for shell and docs consumers.

Fields:

- `schema`: `hideout.proof-registry/v1`.
- `generatedAt`: generation timestamp, if emitted for human use.
- `requirements`: sorted proof requirements.

Rules:

- Sorting is by `featureId`, then `proofId`.
- Consumers must treat the JSON view as read-only.
- Shell scripts must not maintain a separate 021-025 required-proof list.

## Evidence Target

Evaluation context.

Fields:

- `name`: `local-dogfood`, `targeted-completion`, or `release-candidate`.
- `commit`: expected commit, when freshness requires it.
- `packageIdentity`: expected package identity, when freshness requires it.
- `artifactRoot`: base directory for relative artifact refs.

Rules:

- Missing commit/package values fail only when the selected freshness policy
  requires them.
- Real Gate 2/Gate 3 requirements are not satisfied by local product-hardening
  proof.

## Proof Evaluation Result

One result for one proof requirement.

Fields:

- `featureId`
- `proofId`
- `claimIds`
- `layer`
- `requiredFor`
- `status`: evaluator status such as `satisfied`, `missing`, `failed`,
  `not-run`, `stale`, `redaction-failed`, `artifact-missing`, or
  `artifact-digest-mismatch`.
- `summary`
- `artifactRefs`

Rules:

- `stale` is not a manifest proof status.
- A passed proof with failed redaction is not satisfied.
- A missing proof result must still include feature and claim metadata from the
  registry.

## Artifact Check

Result of evaluating a proof's artifacts against the requirement policy.

Fields:

- `policy`: `none`, `exists`, or `exists-and-digest-if-supplied`.
- `path`
- `sha256`
- `status`
- `summary`

Rules:

- `none` does not require artifact refs.
- `exists` requires every referenced artifact to exist under the artifact root.
- `exists-and-digest-if-supplied` additionally verifies the digest when present.
- Absolute or escaping artifact paths remain invalid through manifest
  validation.
