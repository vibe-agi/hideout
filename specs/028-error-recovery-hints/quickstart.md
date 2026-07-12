# Quickstart: Error Codes And Recovery Hints

<!-- markdownlint-disable MD013 -->

## Scenario 1: Recovery Code Registry

Requirement coverage: FR-001, FR-002, SC-001.

```bash
go test ./internal/recovery
```

Expected result:

- all v1 codes exist once;
- registry JSON is deterministic;
- invalid duplicate/empty codes fail tests.

## Scenario 2: Doctor Human/JSON Parity

Requirement coverage: FR-003, FR-004, FR-005, SC-002, SC-003.

```bash
go test ./internal/doctor -run 'Recovery|Schema|Human'
```

Expected result:

- coded findings validate against schema;
- human and JSON surfaces show the same code;
- uncoded findings remain valid.

## Scenario 3: Selected CLI Codes

Requirement coverage: FR-006, FR-007, FR-008, FR-010, SC-004.

```bash
go test ./internal/app ./internal/releasecompat -run 'RecoveryCode|Package|Init|Readiness'
```

Expected result:

- obsolete package leftovers print `package.obsolete-leftover`;
- privacy init prerequisite errors print stable init codes;
- release-candidate missing/stale evidence prints stable release codes.

## Scenario 4: Docs Truth

Requirement coverage: FR-009, SC-005.

```bash
scripts/test-doc-truth-smoke.sh --out "$(mktemp -d)"
```

Expected result:

- user-facing recovery-code references exist in the Go registry;
- docs truth writes schema-valid product-hardening evidence.

## Scenario 5: Gate 0

Requirement coverage: FR-011, FR-012, SC-006.

```bash
scripts/test-gate0.sh
```

Expected result:

- selected fail-closed behavior remains unchanged;
- new code checks are part of normal local validation.
