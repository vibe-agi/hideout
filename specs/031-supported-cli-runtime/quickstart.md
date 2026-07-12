# Quickstart: Supported CLI Runtime Verification

<!-- markdownlint-disable MD013 MD060 -->

This is an implementation verification guide, not current product truth until
031 is marked Implemented and real evidence is attached.

## 1. Catalog And Package Integrity

Build the package and verify the embedded/source/packaged catalog and contract
are byte-identical, schema-valid, checksummed, and inspectable.

Maps: FR-001, FR-002, FR-006, SC-002, SC-013.

## 2. Selection Is Explicit And Pinned

Initialize one privacy profile with `--runtime developer-standard`, inspect the
plan, and create an environment. Assert the plan resolves one macOS arm64
artifact, profile/environment provenance matches it, and no image URL is typed
by the user. Reject `--runtime` plus `--image`, unsupported architecture,
withdrawn revision, catalog drift between plan/apply, and digest-invalid input.

Maps: FR-003, FR-004, FR-005, SC-001, SC-002.

## 3. Existing And Custom Environments Stay Honest

Load legacy records and create an explicit custom-image environment. Change the
test catalog. Assert no record is mutated or rebuilt and status remains
`custom/unverified`, unmanaged, or pinned to its recorded revision.

Maps: FR-003, FR-005, FR-011, SC-010.

## 4. Runtime Contract Is Data, Not Provisioning

Validate the complete baseline and reject shell commands, paths, `-c`, env
assignments, remote scripts, install instructions, duplicate IDs, unknown
fields, and output over bounds. Compare effective authority before/after
selection and assert no new host/network/root grant.

Maps: FR-006, FR-007, FR-017, FR-020, SC-011, SC-013.

## 5. Real Candidate Image Spike

On a clean macOS arm64 host, download the exact retained URL, verify SHA-256,
boot with Lima, and record download bytes, virtual/expanded size, first boot,
base/source retention, license review, inventory, SBOM status, and target
privilege. Reject wrong digest and insufficient disk before false readiness.

Maps: FR-001, FR-002, FR-004, FR-012, FR-019, SC-003, SC-004, SC-012.

## 6. Actual Guest Baseline

Run all boundary and baseline observations in the real guest as target UID
1000. Assert every command/version matches, target sudo is unavailable, the
receipt is host-only, and CLI/doctor/Manager/Boundary Summary agree on
`preview-ready`.

Maps: FR-007, FR-008, FR-010, FR-011, FR-012, SC-003, SC-005.

## 7. Drift And Exact Command Failure

In a disposable candidate environment, make one baseline command unavailable
and run a different present command. Assert visible `preview-failed`, persisted
failure, and successful unrelated command. Then request the missing exact
command and assert it never runs and returns `runtime.command.missing`. Remove a
boundary prerequisite in a fixture and assert all target execution stops.

Maps: FR-008, FR-009, FR-010, FR-011, SC-005, SC-007.

## 8. Durable User Prefix

Assert target PATH starts with run shims, then includes
`/hideout/profile/home/.local/bin`, followed by system paths. Write a fixture
tool there, stop/start the environment, and execute it without sudo. Assert no
host PATH entry or host global prefix is present.

Maps: FR-013, FR-016, FR-020, SC-001, SC-011.

## 9. Real Agent Install Through Privacy Network

Clear target npm caches and Codex install state. In the real tun2socks/DoH lane,
install `@openai/codex@0.144.1` into `$HOME/.local` and run `codex --version`.
Assert target ownership, exact package integrity/version, HTTPS traversal,
mediated DNS forward proof, connected-subnet reverse block, and zero proxy
credential in target/public evidence.

Maps: FR-013, FR-014, FR-016, FR-017, FR-018, SC-006, SC-008, SC-009.

## 10. Failure Recovery Matrix

Use deterministic fixtures for artifact unavailable/digest mismatch/disk low,
network denied, DNS mediation failed, registry/package failed, prefix
unwritable, baseline missing, boundary missing, and exact command missing.
Assert distinct registered recovery codes, executable next actions, no guessed
package for an unknown tool, and no target side effect after a blocking error.

Maps: FR-004, FR-008, FR-009, FR-015, SC-002, SC-005, SC-007.

## 11. Existing Boundary Regression

Run the full real Gate 2 against the exact runtime digest: workspace alias,
identity, non-root/no-sudo, HostFS discover/read/write, decisions, projection,
cleanup, and environment lifecycle remain green. Run Gate 3 with the same
revision/digest and assert privacy-network and privilege evidence.

Maps: FR-012, FR-017, FR-018, FR-020, SC-003, SC-008, SC-009, SC-011.

## 12. Claim And Documentation Truth

Map every README/runtime/status/threat-model claim to a stable proof ID. Assert
all surfaces say preview, interactive login and automatic updates are out of
scope, native/local evidence cannot satisfy real readiness, and missing SBOM or
dirty candidate state is visible.

Maps: FR-010, FR-011, FR-016, FR-018, FR-019, SC-008, SC-012, SC-013.

## Final Battery

Run formatting, build, vet, diff checks, all Go tests, schema/docs checks,
package smoke, runtime smoke, Gate 0, the exact-image Gate 2, and privacy Gate 3.
Do not mark the feature Implemented while the retained runtime asset, clean
candidate provenance, or either real gate is missing.
