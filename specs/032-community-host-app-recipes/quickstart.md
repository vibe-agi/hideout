# Quickstart: Community Host-App Recipes Verification

<!-- markdownlint-disable MD013 MD060 -->

This is the implementation verification guide for the current 032 v1 product
claim. Its real Gate 2 receipt is dirty private-alpha evidence, not clean release
provenance.

## 1. Built-In Genericity

Load two built-in test recipes and compile their command registrations. Prove
both use one strict grammar, immutable binding model, app resolver, safety
profile selector, provider, decision checker, inspection model, and audit path.
Remove either recipe and prove only its own commands disappear. Scan production
generic paths for application/command-specific branching.

Maps: FR-008, FR-018, FR-019, FR-020, FR-028; SC-003.

## 2. Source Snapshot And Digest Lock

Create local and exact-commit git packs. Plan and apply install, mutate the
original sources, and prove runtime reads the private snapshot. Reject symlink,
special-file, oversize, submodule, hook/filter, changing-local-source, and wrong
commit/digest fixtures. Exercise both source forms through the shared CLI and
Manager plan/apply surface, not only the source package. Prove update creates a
new revision.

Maps: FR-001, FR-002, FR-003, FR-004, FR-005; SC-002, SC-009.

## 3. Schema, Grammar, And No New Authority

Validate a pack and run representative grammar vectors. Reject unknown fields,
JavaScript/hooks, raw argv, undeclared capabilities/results, app overrides,
host paths, multiple resources, malformed locations, unknown flags, reserved
commands, and implicit conflicts. Prove package tests can fail without changing
Core validation and cannot certify security.

Maps: FR-006, FR-007, FR-017, FR-020, FR-031, FR-032; SC-007.

## 4. Application Root And Identity

Exercise signed, unsigned, absent, unsupported, owner-mismatch, writable-
ancestor, symlink-escape, workspace/HostFS/tmp/store overlap, package-self-
requirement, and launch-time replacement fixtures. Prove signed identity comes
from Core observation and unsigned trust is exact-digest, unverified, elevated,
and suspended on change. For unsigned apps, inject out-of-bundle links, special
files, count/byte limit overflow, and mutation during descriptor-relative digest
traversal; each must fail before trust or launch.

Maps: FR-009, FR-010, FR-011, FR-012; SC-004.

## 5. Core Safety Floor

Request a compatible reviewed safety profile and prove Core builds and checks
both argv and settings. Attempt the forbidden effect through a flag and through
an equivalent setting. Prove an unknown/unreviewed identity cannot become safe
and can proceed only through explicit ask-each-run when otherwise valid.

Maps: FR-013, FR-014, FR-025; SC-005.

## 6. Permission Fingerprint And Update Review

Mutate each command, alias, identity, app root/name, executable path,
expectation, launch flag, safety profile/version, resource class, grammar,
capability/result/access, and return declaration. Prove every mutation changes
the permission fingerprint, suspends trust, and appears in a bounded diff.
Change only docs/tests and prove source digest changes without an invented
permission broadening.

Maps: FR-015, FR-016; SC-006.

## 7. Atomic Product Add Flow

From a TTY, run one guided add for a valid local pack and one cancellation.
From a non-interactive client, omit acceptance and supply a wrong digest. Prove
only the confirmed exact plan stores/enables authority, cancellation and both
failures leave no binding, and install-only stores bytes without authority.
Compare CLI and Manager plan/apply byte-for-byte fields and outcomes.
Inject ANSI/OSC/control sequences into every package-provided display field and
hint; prove human and JSON surfaces contain only the shared bounded sanitized
value and retain an explicit untrusted label.

Maps: FR-004, FR-005, FR-026, FR-032; SC-001, SC-002, SC-012.

## 8. Immutable Runtime Binding And No Fallback

Compile a new run, inspect exact command-to-app binding, and submit forged
command/action/binding/app/capability/result/host-path/unknown-field requests.
Disable, revoke, drift, or remove the provider. Prove all requests fail before
host/guest fallback and audit facts come from the validated binding.

Maps: FR-018, FR-019, FR-020, FR-021, FR-029; SC-007, SC-008.

## 9. App-Scoped Decision Lifecycle

Create safe and elevated bindings for two apps/revisions. Claim/approve, deny,
timeout, revoke, update, and end owner/session state. Prove one approval applies
only to its exact app, package, binding, command, profile, workspace,
environment, identity, and run. Verify no persistent profile allowance exists.

Maps: FR-024, FR-025; SC-008.

## 10. HostFS Resource Consumption

Use an existing HostFS content grant to open a mapped resource. Reject see-only,
ungranted, denied, reserved, stale/ended portal, owner mismatch, policy expiry,
and symlink-retarget fixtures. Prove no recipe/intent/decision/public artifact
contains the resolved host path and revoking HostFS invalidates retry.

Maps: FR-022, FR-023; SC-010, SC-013.

## 11. Session Immutability And Lifecycle

Start a run, enable a pack, and prove the existing session receives no shim and
inspection reports the new-run action. Start the next run and prove it receives
exactly the enabled commands. Disable/revoke/remove and prove runtime requests
fail closed while historical audit survives and unrelated files/packs remain.

Maps: FR-027, FR-029, FR-032; SC-011, SC-012.

## 12. Real macOS Arm64 Lima Gate 2

Install an external local test pack that binds a second command to the already
installed VS Code app, without rebuilding Hideout. From a real guest, prove
workspace and authorized HostFS opens reach the same generic host provider;
safe and ask-each-run scope correctly; unsafe app roots and see-only portals
fail; old-session/new-run behavior is exact; disable/revoke has no fallback;
built-in `code .` and `code -g` still pass; evidence contains no host path,
username, raw argv, or control credentials.

Run this through the 032-owned `scripts/test-host-app-pack-e2e.sh`. It may reuse
030's low-level projection helper, but it must emit only 032 proof IDs and must
not change the command, claims, or artifact semantics of the retained 030
evidence entrypoint.

Maps: FR-010, FR-018, FR-021, FR-022, FR-023, FR-024, FR-028, FR-030, FR-033;
SC-003, SC-004, SC-005, SC-007, SC-008, SC-010, SC-011, SC-013, SC-014.

## Final Battery

Run formatting, build, vet, diff checks, all Go tests, schema/docs checks,
package smoke, host-app-pack smoke, Gate 0, and the full real Gate 2. Then run an
adversarial review focused on guest-writable app execution, package self-
attestation, safe-setting bypass, cross-binding dispatch, source TOCTOU,
permission-fingerprint omissions, HostFS authority fixation, fallback, and
evidence overclaim. Do not mark 032 Implemented until all Blocking/High/Medium
findings are resolved and the real external-pack proof is retained.
