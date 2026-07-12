# Contract: Proof Evaluation

<!-- markdownlint-disable MD013 -->

## Purpose

Evaluate one or more product-hardening manifests against the Go-owned proof
registry and a target context.

## Evaluation Inputs

- Registry requirements.
- One or more `hideout.product-hardening-evidence/v1` manifests.
- Target:
  - `local-dogfood`;
  - `targeted-completion`;
  - `release-candidate`.
- Expected commit and package identity when the target requires freshness.
- Artifact root for artifact existence and digest checks.

## Result Statuses

Evaluator statuses are separate from manifest proof statuses:

- `satisfied`
- `missing`
- `failed`
- `not-run`
- `stale`
- `redaction-failed`
- `artifact-missing`
- `artifact-digest-mismatch`
- `not-required`

`ProofEntry.status` remains `passed`, `failed`, or `not-run`.

## Required Behavior

- Missing proof results must include registry feature ID and proof ID.
- A proof with `status=passed` and `redactionStatus!=passed` is
  `redaction-failed`.
- A proof with `status=failed` is `failed`.
- A proof with `status=not-run` is not satisfied for targeted completion or
  release candidate.
- A proof whose commit/package identity violates its freshness policy is
  `stale`.
- Artifact checks are applied only when the requirement artifact policy requires
  them.
- Local product-hardening proof is supporting evidence for release readiness;
  it does not satisfy real Gate 2/Gate 3 requirements.

## Output Requirements

- Human diagnostics must name `featureId`, `proofId`, and the unsatisfied
  reason.
- JSON output must be deterministic enough for tests.
- Redaction failures prevent satisfaction.
