# Research: Alpha First-Run E2E

## Decision 1: Canonical Install/Init Order

**Decision**: Every 022 lane installs with `--skip-init`, then runs exactly one
explicit init step. Local-fast uses a native/dev profile because the privacy
template requires `tun2socks`. Real-backend mode uses the documented
Lima/privacy init when prerequisites are present.

**Rationale**: `package install` and `install.sh` can initialize `default` when
`--skip-init` is absent. The current first-run docs then run `hideout init`
again, which can collide with the already-created profile. The privacy template
cannot run in native/direct local-fast mode, so 022 splits proof semantics:
local-fast proves package mechanics and first command behavior, while
real-backend proves the documented privacy profile or records `not-run`.

**Alternatives considered**:

- Let installer create `default` and remove the documented init step. Rejected
  because the first-run page should show the operator the exact profile and
  privacy posture being selected.
- Keep both init paths and tolerate pre-existing profiles. Rejected because it
  hides a first-run footgun and weakens fail-closed profile lifecycle behavior.

## Decision 2: Reuse Product-Hardening Evidence V1

**Decision**: 022 writes `hideout.product-hardening-evidence/v1` entries with
feature-specific proof IDs, using existing modes such as `local-fast`,
`real-gate`, `docs`, `schema`, and `unit`.

**Rationale**: 021 introduced the evidence manifest specifically for 021-025.
The existing schema already represents pass/fail/not-run, prerequisites,
artifacts, redaction status, package identity, and claim mapping. Adding a
parallel first-run schema would increase release-readiness integration work
without adding user value.

**Alternatives considered**:

- Extend `hideout.release-readiness/v1` directly. Rejected because local-fast
  first-run proof is not release readiness.
- Add `first-run-e2e` as a new manifest mode. Deferred until implementation
  proves the existing `local-fast` and `real-gate` modes are insufficient.

## Decision 3: Installed Binary Only After Packaging

**Decision**: After package creation, all verify/init/run/audit evidence steps
execute the installed `hideout` from the temp prefix, not `go run` or the
source-tree binary.

**Rationale**: 022 exists to close the gap between source-tree smoke tests and a
real operator path. Running source commands after installation would miss PATH,
packaged helper, package manifest, and installed-state failures.

**Alternatives considered**:

- Reuse package smoke as-is. Rejected because it proves many package details
  but does not produce product-hardening first-run evidence or align docs.

## Decision 4: Local-Fast Is Weak And Real Backend Is Explicit

**Decision**: Local-fast mode uses native/dev harness proof and is always
labeled weak/native/dev-only. It does not attempt to initialize the privacy
template with native/direct. Real-backend mode is a separate explicit run that
passes only when the actual Lima/privacy path executes; otherwise it records
`not-run` or failure.

**Rationale**: The constitution and existing support matrix treat native as a
weak harness. 022 must not repeat the release-readiness mistake of converting a
local check into real isolation evidence.

**Alternatives considered**:

- Infer real-backend proof from docs or support matrix. Rejected because real
  proof requires runtime evidence, not declared support.

## Decision 5: Package Verification Before Success

**Decision**: The script runs package verification after install and before any
first-run pass claim. Stale package, missing helper, manifest, checksum, or
obsolete file findings prevent pass evidence.

**Rationale**: `internal/packagekit.Verify` already checks package artifact and
installed-state integrity, including obsolete package-owned files. 022 should
reuse that behavior instead of reimplementing package checks in shell.

**Alternatives considered**:

- Trust successful `install.sh`. Rejected because 013/017 package work already
  showed install can leave stale state that verify must catch.

## Decision 6: Failure Fixtures Are First-Class Evidence

**Decision**: 022 includes fixture modes for duplicate profile, stale package,
missing helper/manifest/checksum, unsafe path, and missing real-backend
prerequisites. These write failed or `not-run` proof entries, never passed
entries.

**Rationale**: The useful first-run contract is not just happy path. Operators
need actionable failure output, and previous review cycles showed tests can
green over overclaim if negative paths are absent.

**Alternatives considered**:

- Cover only the happy path in Gate 0. Rejected because it would not prove the
  fail-closed behavior in FR-010 and FR-011.

## Decision 7: Audit And Boundary Proof

**Decision**: The first command is run with the installed binary and must
surface audit presence plus `Hideout boundary:` output or a structured
Boundary equivalent. Missing evidence blocks the corresponding proof entry.

**Rationale**: A first run that cannot show audit and Boundary evidence does
not demonstrate the product's core visibility model. The proof should observe
the real CLI output and audit store instead of manufacturing a summary.

**Alternatives considered**:

- Count command exit zero as sufficient. Rejected because it proves execution
  but not Hideout's evidence boundary.

## Decision 8: Docs Are Part Of The Contract

**Decision**: `docs/first-run-alpha.md` and the E2E script must agree on the
install/init order, proof mode labels, and local-fast vs real-backend boundary.

**Rationale**: 022 is an operator first-run feature. If the docs instruct a
different sequence than the script proves, the proof is not useful.

**Alternatives considered**:

- Keep docs smoke separate. Rejected because the install/init collision is a
  docs-vs-implementation mismatch.

## Decision 9: External Prerequisites Stay Explicit

**Decision**: `tun2socks` remains an external prerequisite in 022. The proof
records whether it is missing or skipped, but does not install it or claim the
package owns it.

**Rationale**: Current package verification reports `tun2socks` as
`packageOwned=false`. 022 is hardening the first-run path, not expanding the
package's helper ownership.

**Alternatives considered**:

- Package `tun2socks` now. Rejected as new distribution scope outside 022.

## Decision 10: Gate Integration

**Decision**: Gate 0 runs local-fast first-run evidence and docs checks. Real
backend mode remains manually or release invoked, and `not-run` is acceptable
when prerequisites are absent.

**Rationale**: Gate 0 must remain reasonably fast and available on normal dev
machines. Real Lima/privacy proof is valuable but belongs to explicit backend
gates or release evidence.

**Alternatives considered**:

- Require real Lima in Gate 0. Rejected because it would make local static/docs
  gates host-dependent and slow.
