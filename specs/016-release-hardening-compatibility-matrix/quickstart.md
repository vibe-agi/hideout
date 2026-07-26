# Quickstart: Release Hardening And Compatibility Matrix

<!-- markdownlint-disable MD013 -->

## 1. Inspect Matrix JSON

```bash
hideout support matrix --json > /tmp/hideout-support-matrix.json
go run ./cmd/hideout-schema-validate schemas/support-matrix.schema.json /tmp/hideout-support-matrix.json
```

Validates: FR-001, FR-002, FR-003, FR-004, SC-001, SC-008.

## 2. Inspect Version Support Summary

```bash
hideout version
```

Expected:

- Existing version, commit, builtAt, Go, and platform lines remain.
- Output includes support matrix schema/version.
- Current platform/backend summary does not leak local paths.

Validates: FR-005, SC-008.

## 3. Doctor Includes Matrix Finding

```bash
hideout doctor --backend native --workspace "$PWD" --json > /tmp/hideout-doctor.json
go run ./cmd/hideout-schema-validate schemas/doctor-report.schema.json /tmp/hideout-doctor.json
```

Expected:

- A `support-matrix` finding is present.
- Native backend is degraded/warn, not isolation evidence.

Validates: FR-006, FR-004, SC-008.

## 4. Local-Fast Readiness Is Not Release Evidence

```bash
scripts/test-release-readiness.sh --local-fast --out /tmp/hideout-readiness.json
go run ./cmd/hideout-schema-validate schemas/release-readiness.schema.json /tmp/hideout-readiness.json
```

Expected:

- `mode` is `local-fast`.
- `evidenceClass` is `local-fast`.
- `releaseReady` is `false`.
- `status` is `not-release`.

Validates: FR-007, FR-009, FR-010, SC-004, SC-007.

## 5. Release-Candidate Fails Closed Without Real Gates

```bash
if scripts/test-release-readiness.sh --release-candidate --out /tmp/hideout-rc.json; then
  echo "unexpected release readiness without real gate evidence" >&2
  exit 1
fi
```

Expected:

- Gate 2 and Gate 3 are marked missing.
- Exit is non-zero.
- Artifact is still redacted and schema-valid when `--out` was provided.

Validates: FR-008, FR-009, SC-003.

## 6. Release-Candidate Accepts Real Gate Evidence

```bash
HIDEOUT_GATE2_EVIDENCE=/path/to/gate2-evidence.json \
HIDEOUT_GATE3_EVIDENCE=/path/to/gate3-evidence.json \
  scripts/test-release-readiness.sh --release-candidate --out /tmp/hideout-rc-ready.json
```

Expected:

- Required gates are present and passed.
- `evidenceClass` is `real-gate`.
- `releaseReady` is true only when all required checks pass.

Validates: FR-008, FR-009, SC-003.

## 7. Compatibility Fixture Smoke

```bash
go test ./internal/releasecompat -run TestCompatibilityFixtures
```

Expected:

- Every FR-011 family has accepted and rejected coverage.
- Unknown major versions fail closed.

Validates: FR-011, FR-012, SC-005.

## 8. Gate0 Drift Guard

```bash
scripts/test-gate0.sh
```

Expected:

- Matrix schemas exist and validate.
- Release-hardening smoke runs.
- README/docs/status match the matrix for platform/backend claims.
- Required non-claims are still present.

Validates: FR-013, FR-014, FR-015, SC-002, SC-006, SC-008.

## 9. Release Dogfood Compatibility

```bash
HIDEOUT_LINUX_TUN2SOCKS_PATH=/path/to/tun2socks-linux-arm64 \
HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:7890 \
  scripts/test-release-dogfood.sh
```

Expected:

- Existing release dogfood remains the heavy real-gate path.
- New readiness/matrix work does not weaken dogfood redaction or manifest
  checks.

Validates: FR-008, FR-016.

## 10. Export/Share Safety

```bash
scripts/test-release-readiness.sh --local-fast --out /tmp/hideout-readiness.json
hideout audit export --source doctor-report --input /tmp/hideout-readiness.json --out /tmp/hideout-readiness.export.json --acknowledge-full-fidelity
```

Expected:

- Readiness output is safe to include in release dogfood or export/share
  evidence.
- No raw control-plane secret pattern appears.

Validates: FR-010, FR-016, SC-007.
