# Quickstart: Doctor And Package Recovery E2E

<!-- markdownlint-disable MD013 -->

## Scenario 1: Local-Fast Recovery Proof

Purpose: prove package repair and doctor recovery loops without claiming real
backend readiness.

Command:

```bash
scripts/test-doctor-package-recovery-e2e.sh \
  --local-fast \
  --out /tmp/hideout-recovery-e2e
go run ./cmd/hideout-schema-validate \
  schemas/product-hardening-evidence.schema.json \
  /tmp/hideout-recovery-e2e/product-hardening-evidence.json
```

Expected:

- evidence contains all four required 024 proof ids;
- package repair loop proves verify failure, dry-run, apply, verify pass, and
  preservation of durable/unrelated files;
- doctor loop proves deep guidance, dry-run non-mutation, explicit safe apply,
  and post-apply evidence;
- doctor report export validates through the export schema;
- public artifacts pass redaction scanning.

## Scenario 2: Gate 0 Integration

Purpose: keep the recovery proof in the local hardening gate.

Command:

```bash
scripts/test-gate0.sh
```

Expected:

- package smoke still covers the canonical package lifecycle;
- doctor smoke still covers the canonical doctor behavior;
- recovery E2E writes product-hardening evidence that references those
  behaviors instead of replacing them.

## Scenario 3: Documentation Boundary

Purpose: prevent doctor/package recovery evidence from becoming a release claim.

Expected:

- docs describe recovery E2E as local product-hardening evidence;
- docs continue to require real Gate 2/Gate 3 and release-readiness artifacts
  for supported privacy-path release claims.
