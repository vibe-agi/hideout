# Quickstart: Documentation Truth Gate

<!-- markdownlint-disable MD013 -->

## Scenario 1: Run Docs Truth Smoke

```bash
scripts/test-doc-truth-smoke.sh --out /tmp/hideout-doc-truth
go run ./cmd/hideout-schema-validate \
  schemas/product-hardening-evidence.schema.json \
  /tmp/hideout-doc-truth/product-hardening-evidence.json
```

Expected:

- four 025 proof ids pass;
- claim-boundary registry includes 021-024 proof ids;
- banned overclaim scan reports zero findings;
- curated commands are recognized or intentionally not executed;
- localized README declares English README canonical.

## Scenario 2: Gate 0 Integration

```bash
scripts/test-gate0.sh
```

Expected:

- docs truth smoke runs after 021-024 product-hardening lanes;
- Gate 0 remains local docs truth evidence, not release readiness.
