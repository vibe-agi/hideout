# Contract: Documentation Truth Gate

<!-- markdownlint-disable MD013 -->

## Command Shape

```bash
scripts/test-doc-truth-smoke.sh [--out <dir>]
```

## Required Behavior

- validate `docs/claim-boundaries.md`;
- validate `docs/command-examples.json`;
- scan current docs for banned overclaim patterns;
- verify README, localized README, STATUS, test plan, and Gate 0 cross-links;
- write `hideout.product-hardening-evidence/v1`;
- validate schema and public artifact redaction.

## Required Proof IDs

- `025.docs.claim-boundaries`
- `025.docs.overclaim-scan`
- `025.docs.command-examples`
- `025.docs.cross-doc-consistency`

## Fail-Closed Conditions

- required 021-024 proof id missing from claim registry;
- banned overclaim pattern found;
- curated safe command is not recognized;
- localized README lacks canonicality statement;
- test plan and Gate 0 disagree on product-hardening scripts;
- product evidence fails schema validation.
