# Contract: 022 Product-Hardening Evidence

022 uses `hideout.product-hardening-evidence/v1` from
[021](../021-ui-e2e-proof/contracts/product-hardening-evidence.md).

## Feature ID

All 022 proof entries use:

```text
022-alpha-first-run-e2e
```

## Required Proof IDs

The local-fast lane must produce these proof IDs:

- `022.first-run.local-fast.install`: package install used `--skip-init` and
  installed the package into the temp prefix.
- `022.first-run.local-fast.verify`: installed package verification passed.
- `022.first-run.local-fast.init`: local-fast init created exactly one weak/dev
  profile.
- `022.first-run.local-fast.run`: installed binary ran one low-risk command.
- `022.first-run.local-fast.audit-boundary`: audit and Boundary evidence were
  observed.
- `022.first-run.docs.order`: docs and script agree on install/init ordering.
- `022.first-run.failure.fixtures`: representative fail-closed fixtures were
  executed or recorded.

The real-backend lane must produce one of:

- `022.first-run.real-backend`: passed real Lima/privacy first-run proof.
- `022.first-run.real-backend.not-run`: prerequisites absent or skipped.

## Claim Mapping

Each proof entry must include at least one `coveredClaims` item with:

- `source`: `spec`, `docs`, `status`, `test-plan`, or `quickstart`;
- `claimId`: stable requirement or scenario id;
- `description`: redacted claim summary.

## Mode Mapping

- Local-fast proof entries use `mode: "local-fast"`.
- Real backend proof entries use `mode: "real-gate"` when executed.
- Docs-only proof entries may use `mode: "docs"`.
- Unit/schema proof entries may use `mode: "unit"` or `mode: "schema"`.

## Redaction

The manifest and referenced artifacts must pass the same control-plane
redaction scan used by 021:

- no daemon token values;
- no `HIDEOUT_SECRET_*` backing values;
- no generated machine-id;
- no raw proxy credential;
- no capability secret;
- no broker/UI token.

User/application data from the first command may remain in local evidence.
