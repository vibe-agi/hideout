# Quickstart: Doctor Deep Diagnostics

<!-- markdownlint-disable MD013 -->

## Prerequisites

- Local repository checkout.
- Go toolchain used by this project.
- No real Lima instance, DNS probe, HostFS mutation, or network probe is required.

## Scenario 1: Deep Adds Feature Findings

Requirement coverage: FR-001, FR-002, FR-005, SC-001, SC-002.

Run:

```sh
hideout doctor --format json > light.json
hideout doctor --level deep --format json > deep.json
```

Expected:

- `deep.json` includes all supported feature diagnostics.
- Each feature has observed facts or a gate-required marker.
- Light output omits feature-only deep findings.

## Scenario 2: Single Feature Scope

Requirement coverage: FR-004, FR-006, SC-002, SC-003, SC-004.

Run each selector:

```sh
hideout doctor --feature adapters --format json
hideout doctor --feature decisions --format json
hideout doctor --feature packaging --format json
hideout doctor --feature dns --format json
hideout doctor --feature hostfs --format json
hideout doctor --feature privilege --format json
hideout doctor --feature daemon --format json
hideout doctor --feature export --format json
hideout doctor --feature cleanup --format json
```

Expected:

- Selected feature finding appears.
- Unselected feature-only findings are absent.
- DNS/HostFS/privilege/Lima release proof is marked gate-required when only local facts exist.

## Scenario 3: Human And JSON Parity

Requirement coverage: FR-012, SC-005.

Run:

```sh
hideout doctor --level deep > doctor.txt
hideout doctor --level deep --format json > doctor.json
```

Expected:

- Every JSON finding check id appears in human output.
- Status, severity, required marker, and next actions match.

## Scenario 4: Redaction Injection

Requirement coverage: FR-013, FR-014, SC-006.

Run targeted tests that inject control-plane-looking values into summaries, details, next actions, and evidence refs.

Expected:

- Human output contains zero raw probe values.
- JSON output contains zero raw probe values.
- Evidence file contains zero raw probe values.
- Audit/recovery evidence contains zero raw probe values.
- Non-secret user values remain visible.

## Scenario 5: Exit Semantics

Requirement coverage: FR-016, SC-007.

Run fixtures for warning/degraded and required local error states.

Expected:

- Warning/degraded state exits 0.
- Required local error exits nonzero.
- Gate-required marker alone exits 0.

## Scenario 6: No Hidden Probes

Requirement coverage: FR-011, SC-008.

Run deep doctor in tests with fake backend/network hooks that fail if invoked.

Expected:

- No backend start.
- No Gate 2/Gate 3 invocation.
- No hidden network probe.
- No HostFS mutation.

## Scenario 7: Gate 0 Smoke

Requirement coverage: FR-017, SC-009.

Run:

```sh
scripts/test-gate0.sh
```

Expected:

- Doctor smoke covers deep mode.
- At least three feature selectors are exercised.
- Warning/error paths and safe dry-run are covered.
- Redaction injection check passes.
