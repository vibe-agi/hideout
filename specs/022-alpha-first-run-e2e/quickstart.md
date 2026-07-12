# Quickstart: Alpha First-Run E2E

## Scenario 1: Local-Fast First Run Passes

Purpose: prove the package-to-first-command path without claiming real
isolation.

Command:

```bash
scripts/test-first-run-e2e.sh --local-fast --out /tmp/hideout-first-run
go run ./cmd/hideout-schema-validate \
  schemas/product-hardening-evidence.schema.json \
  /tmp/hideout-first-run/product-hardening-evidence.json
```

Expected:

- package is installed with `--skip-init`;
- installed `hideout package verify <prefix>` passes;
- a weak/dev local-fast profile is initialized once;
- one low-risk command runs through installed `hideout`;
- audit and Boundary evidence are present;
- evidence marks the proof as local-fast/native weak/dev-only.

## Scenario 2: Docs And Script Install Order Match

Purpose: prevent the install/init collision from returning.

Command:

```bash
scripts/test-first-run-docs-smoke.sh
scripts/test-first-run-e2e.sh --local-fast --out /tmp/hideout-first-run-docs
```

Expected:

- `docs/first-run-alpha.md` instructs `./install.sh --skip-init` for the
  canonical real-backend path;
- the script uses the same order;
- no duplicate `default` init occurs.

## Scenario 3: Package Verification Failure Fails Closed

Purpose: prove stale or corrupted package state cannot pass first-run proof.

Command:

```bash
scripts/test-first-run-e2e.sh \
  --local-fast \
  --fixture bad-checksum \
  --out /tmp/hideout-first-run-bad-checksum
```

Expected:

- command exits non-zero or records failed proof;
- evidence contains the package checksum finding;
- no local-fast first-run pass proof is present for the failed claim.

## Scenario 4: Duplicate Profile Fails Closed

Purpose: prove first-run init is exactly-once.

Command:

```bash
scripts/test-first-run-e2e.sh \
  --local-fast \
  --fixture duplicate-profile \
  --out /tmp/hideout-first-run-duplicate-profile
```

Expected:

- the script detects existing `default` profile state before clean init;
- no overwrite occurs;
- evidence records failed duplicate-profile finding.

## Scenario 5: Missing Real Backend Is Not-Run

Purpose: prove missing Lima/privacy prerequisites do not become native pass
claims.

Command:

```bash
scripts/test-first-run-e2e.sh \
  --real-backend \
  --out /tmp/hideout-first-run-real
```

Expected when prerequisites are absent:

- evidence includes `022.first-run.real-backend.not-run`;
- missing prerequisites are named;
- script exits zero only when not-run is allowed.

Expected when prerequisites are present:

- the real Lima/privacy path executes;
- evidence includes `022.first-run.real-backend`;
- no native fallback is used.

## Scenario 6: Real Backend Required

Purpose: support manual release-style runs that require real proof.

Command:

```bash
scripts/test-first-run-e2e.sh \
  --real-backend \
  --require-real \
  --out /tmp/hideout-first-run-real-required
```

Expected:

- missing real prerequisites exit non-zero;
- a passing result requires actual real backend execution.

## Scenario 7: Redaction Check

Purpose: ensure evidence is shareable as local diagnostic proof.

Command:

```bash
scripts/test-first-run-e2e.sh --local-fast --out /tmp/hideout-first-run-redact
```

Expected:

- `product-hardening-evidence.json` validates against schema;
- logs and manifest contain no raw control-plane material;
- evidence includes `redactionStatus: "passed"` for passing proof entries.

## Scenario 8: Gate 0 Integration

Purpose: ensure the local first-run proof runs with normal project gates.

Command:

```bash
scripts/test-gate0.sh
```

Expected:

- schema validation passes;
- first-run local-fast proof passes or records allowed local prerequisites;
- docs smoke passes;
- real backend proof is not required by Gate 0.
