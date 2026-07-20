# Research: Trusted Host-IDE Workspace Grant

<!-- markdownlint-disable MD013 -->

All decisions below are grounded in code inspected 2026-07-20 and the throwaway
real-Lima spike that proved path B end to end. No open NEEDS CLARIFICATION.

## D1 — Grant is durable profile policy, not a per-run decision

**Decision**: Store an approved trusted-IDE grant as a per-profile JSON file
under `profiles/<p>/`, read every run, keyed by `(workspaceID, qualifiedAppRef,
bindingDigest)`. It is operator policy of the same shape as a HostFS profile
grant, not a per-run capability token.

**Rationale**: The decision mechanism (`hostfs/readgrant`, overlay write, the
current trusted decision) is built for *guest-initiated, operator-after-the-fact*
approval; `readgrant.Manifest.Validate` even binds `sessionID`. Trusted IDE is
the *operator's own up-front intent*, so a decision that dies with the run is a
category error and the cause of the deadlock. Durable profile policy read each
run is exactly how HostFS profile grants already work, and does not violate
constitution V (per-run authority still regenerates; this is policy the run
consults).

**Alternatives considered**: (A) make the approved per-run decision persist
across runs — rejected: complicates the decision lifecycle and strains the
per-run-authority principle. (C) document safe-only, never fix trusted —
rejected: leaves the headline `code .` native path permanently dead.

## D2 — Landing site is `runProjectionGrantChecker`, not `decisionIdeGrantChecker`

**Decision**: Add the persistent-grant check inside
`runProjectionGrantChecker.TrustedGrantActive` (`run_dataplane.go:517`), before
the existing per-run decision lookup. Delete or explicitly document the
`decisionIdeGrantChecker` twin (`hostcap_projection.go:335`).

**Rationale**: `StartRunDataPlane` wires only `runProjectionGrantChecker` into
the broker (`run_dataplane.go:178` → `HostApp: hostAppProjection`).
`decisionIdeGrantChecker` has only test callers — the production path never runs
it. The spike edited exactly this function and the real `code .` behavior
changed, confirming the site. Editing the twin would change nothing real
(FR-011).

**Alternatives considered**: editing `decisionIdeGrantChecker` (the earlier
draft's implication) — rejected as dead code by probe.

## D3 — Grant key fields are all Core-derived and available at the check point

**Decision**: Key the grant by `scope.WorkspaceID`, `scope.QualifiedAppRef`,
`scope.BindingDigest` (all on `hostcap.GrantScope` at the check point), scoped to
`scope.Profile`. No guest-supplied value participates.

**Rationale**: `GrantScope` already carries these fields, populated from the
Core-derived `projectionGrantBinding` (`WorkspaceID: authority.WorkspaceID`,
plus the binding's app ref and digest). The spike wrote a grant with exactly
these three fields and matched it at open time. This satisfies FR-004 (keyed by
Core-derived identity) and FR-010 (no host path/secret in the key).

## D4 — Grant command derives the workspace identity the same way a run does

**Decision**: `hideout allow ide-trust`, run in the project directory, derives
`workspaceID` with the same functions the run uses (`CaptureRootIdentity(cwd)`,
`LoadOrCreateIdentityKey(store)`, `DeriveWorkspaceID(...)`), reads the built-in
VS Code binding's `qualifiedAppRef` and `bindingDigest` from
`CompileHostAppCatalog`, then writes the grant. `hideout deny ide-trust` revokes
the current workspace's grant. This reuses the `operatorintent` `allow`/`deny`
surface (FR-001, FR-006).

**Rationale**: Workspace-identity derivation inputs (store identity key, canonical
workspace root, root file identity) are all deterministic and available when the
operator is in the project directory, so the command reproduces the exact
`workspaceID` a run computes — verified reproducible in the run-workspace code
path. This gives the immediate `allow read <path>`-style authorization (no
"fail first" required), matching the established HostFS grant UX.

**Alternatives considered**: request-driven promotion (spike used this: run
records a request, operator promotes it). Rejected as the primary UX because it
forces a "fail first" step unlike other `allow` commands; the derived-in-place
command is more self-consistent. (The fail-closed refusal still names the
command, so an operator who does hit the refusal is guided — FR-003.)

## D5 — Fail-closed refusal names the grant command

**Decision**: When trusted mode has no matching grant, the projected open refuses
with no host launch and the message names `hideout allow ide-trust` (run in the
project directory). No stale decision is left behind.

**Rationale**: FR-003 and the 2026-07-20 walkthrough finding — the current
dead-end refusal is the usability bug. The broker already carries a guest-visible
`Stderr` line for host-app outcomes (038/2ccdd40), so the refusal message has a
delivery channel.

## D6 — Drift and revocation

**Decision**: A grant matches only when all key fields equal the current run's
values; a changed `workspaceID` (different project) or `bindingDigest` (changed
editor build/binding) simply does not match, re-requiring a grant. Switching the
profile to safe mode deletes all trusted-IDE grants for the profile (extend the
existing `invalidateProjectionGrantsForProfile` path); `deny ide-trust` deletes
one. Grant existence is shown by `profile ide-mode`.

**Rationale**: FR-006/FR-007/FR-008. Matching-by-equality gives drift
re-confirmation for free; no separate expiry timer is needed (out of scope per
spec). Safe-mode revocation reuses the existing invalidation hook.

## D7 — Security invariants (the 030 four seams)

**Decision**: (1) grant lives under `profiles/<p>/`, guest-unreachable, `0600`,
atomic write — a guest writing the workspace cannot mint/refresh/read it;
(2) keyed by Core-derived workspace identity, never a guest path;
(3) drift (workspace/app identity) re-requires a grant; (4) visible via
`ide-mode`, revocable via safe-mode/`deny ide-trust`, and every grant/reuse/
refuse/revoke is audited.

**Rationale**: Directly the spec's four security invariants; mirrors the
guest-unreachable placement already proven for `ide-mode.json`. The residual
trusted-mode risk (guest-writable workspace may carry `.vscode` tasks) is
inherent to trusted mode, identical under the old per-run path, disclosed on
`ide-mode trusted-host-ide`, and mitigated by safe mode being the default.

## D8 — Evidence and delivery discipline

**Decision**: Emit typed audit for grant/reuse/refuse/revoke. Prove behavior
with Go unit/contract tests (match, miss, drift, guest-cannot-forge, safe
unaffected) plus a real-Lima lane (grant → separate-run reuse → refuse without
grant → revoke). Every new assertion gets a mutation proof; every new judge a
negative fixture (constitution 1.3.0).

**Rationale**: FR-008, SC-001..SC-006, and the project's mutation/negative-
fixture requirement. The spike already demonstrated the real-Lima loop manually;
this makes it a repeatable, asserted lane.
