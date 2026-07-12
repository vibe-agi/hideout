# Data Model: Doctor And Package Recovery E2E

<!-- markdownlint-disable MD013 -->

## Recovery Evidence Manifest

Reuses `hideout.product-hardening-evidence/v1`.

Required local-fast proof ids:

- `024.recovery.package.repair-loop`
- `024.recovery.doctor.safe-fix-loop`
- `024.recovery.doctor.guidance-only`
- `024.recovery.redaction`

Validation:

- All required local-fast proof ids must pass.
- Not-run proofs cannot satisfy local-fast completion.
- Proof entries must include artifact references and covered claims.

## Package Repair Loop

Fields:

- `installedPrefix`: redacted fixture label or temp path summary.
- `obsoleteFile`: package-relative path.
- `verifyBefore`: failed/warned status with repair hint.
- `dryRun`: planned removals and durable-state preservation.
- `apply`: removed files and durable-state preservation.
- `verifyAfter`: clean status.
- `unrelatedFilePreserved`: boolean.
- `durableStatePreserved`: boolean.

Validation:

- Dry-run must not remove the obsolete file.
- Apply must remove only package-owned obsolete files.
- Verify after repair must pass.

## Doctor Repair Loop

Fields:

- `deepReport`: doctor human or JSON artifact ref.
- `safeRepair`: dry-run/apply summaries.
- `recheck`: post-apply doctor or state artifact ref.
- `appliedRepairKinds`: typed safe repair ids.
- `unsafeRepairsApplied`: must be empty.

Validation:

- Dry-run must not create profile/install state.
- Apply must be explicit.
- Unsafe states remain guidance.

## Guidance Finding

Fields:

- `checkId`: doctor check id.
- `status`: warn/error/pass.
- `observedFacts`: public facts.
- `candidateCauses`: public candidate causes.
- `nextActions`: actionable commands or references.
- `gateRequired`: real gate markers when applicable.
- `fixed`: must be false for guidance-only findings.

Validation:

- Gate-required findings cannot be counted as release readiness.
- Missing prerequisites must name what is missing without leaking secrets.

## Recovery Redaction Scan

Fields:

- `scannedArtifacts`: public files scanned.
- `forbiddenPatterns`: categories, not raw secret values.
- `status`: passed/failed.

Validation:

- Fails on broker/UI tokens, raw proxy URL with credentials, `HIDEOUT_SECRET_*`
  values, generated machine-id material, provider refs, or private claim tokens.
