# Quickstart: Doctor Diagnostics And Recovery

<!-- markdownlint-disable MD013 -->

## Scenario 1: Default Light Doctor

```bash
hideout doctor
```

Expected:

- no guest start;
- no hidden network probe;
- human report lists package/store/profile/daemon/helper/template basics;
- warning/degraded states are visible;
- required local errors return nonzero.

Maps to FR-001, FR-003, FR-005, FR-006, FR-014, SC-001, SC-004, SC-005.

## Scenario 2: JSON Report

```bash
hideout doctor --format json > doctor.json
hideout-schema-validate schemas/doctor-report.schema.json doctor.json
```

Expected:

- JSON validates;
- every human-output finding has equivalent JSON fields;
- `summary.exitCode` matches CLI behavior.

Maps to FR-001, FR-002, FR-014, SC-002.

## Scenario 3: Feature-Scoped DNS Diagnostics

```bash
hideout doctor --feature dns --profile default --backend lima
```

Expected:

- local DNS mediation prerequisites or a gate-required marker are reported;
- connected-subnet resolver blocking is not claimed by the local doctor unless
  real gate evidence is present;
- missing prerequisites are errors or skipped/unsupported findings with hints,
  not weak fallback passes.

Maps to FR-004, FR-011, SC-003.

## Scenario 4: Feature-Scoped HostFS Diagnostics

```bash
hideout doctor --feature hostfs --profile default
```

Expected:

- HostFS local readiness or a gate-required marker is reported;
- reserved-root protection remains required when local state is present;
- missing backend/runtime prerequisites are not converted into weak fallback
  passes.

Maps to FR-004, FR-012, SC-003.

## Scenario 5: Privilege Degraded Status

Run doctor against a fixture whose privilege state is degraded.

Expected:

- report status is warning/degraded;
- exit is zero in default light mode;
- non-claim text remains visible.

Maps to FR-013, FR-014, SC-005.

## Scenario 6: Safe Recovery Dry-Run

```bash
hideout doctor --fix --dry-run
```

Expected:

- a recovery plan prints;
- no durable state is written;
- unsafe findings are listed as refused/manual.

Maps to FR-015, FR-016, SC-006, SC-008.

## Scenario 7: Safe Recovery Apply

```bash
hideout doctor --fix --apply
```

Expected:

- only safe typed repairs apply;
- audit evidence is written;
- refused findings remain refused.

Maps to FR-015, FR-016, SC-007, SC-008.

## Scenario 8: Doctor Report Evidence

```bash
hideout doctor --format json --evidence-out ./doctor-report.json
hideout audit export \
  --source doctor-report \
  --doctor-report ./doctor-report.json \
  --out ./doctor-export.json \
  --acknowledge-full-fidelity
```

Expected:

- report is redacted before save/export;
- unrelated exports do not include doctor reports unless selected.

Maps to FR-010, FR-017, FR-018, SC-009.

## Scenario 9: Gate 0 Smoke

```bash
scripts/test-doctor-smoke.sh
scripts/test-gate0.sh
```

Expected:

- smoke covers human output, JSON output, one required failure, one warning/degraded state, redaction, and dry-run recovery.

Maps to FR-019, SC-010.
