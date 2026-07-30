# Feature 045 negative judge fixtures

`run-negative-fixtures.sh` exercises the release claim-receipt boundary for
every claim family in `docs/release/045-claim-matrix.md`.

Each case has two digest-bound raw observation artifacts:

- `negative`: one named semantic fact is false while the receipt, contract
  digest, matrix digest, and evidence digest remain internally consistent;
- `restored`: the same observation is restored to the contract value and the
  identical judge accepts it.

Run:

```sh
scripts/mutation/045/run-negative-fixtures.sh
```

Evidence is written with private permissions below
`.artifacts/045/local/mutations/judge-negative-fixtures/`. The root summary
points to an immutable run-specific summary.

This lane proves the `N045-*` judge-fixture half only. It deliberately records
`implementationMutationProofs.accepted=false` and `claimAcceptance=false`.
Production `M045-*` mutants, real-Lima behavior, package identity, performance,
and full release readiness remain separate evidence.
