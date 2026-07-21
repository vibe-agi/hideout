# Research: Trusted Host-App Workspace Grant

<!-- markdownlint-disable MD013 -->

All decisions below are grounded in code inspected 2026-07-20 and the throwaway
real-Lima spike that proved path B end to end. No open NEEDS CLARIFICATION.

## D1 — Grant is durable profile policy, not a per-run decision

**Decision**: Store an approved trusted host-app grant as a per-profile JSON file
under `profiles/<p>/`, read every run, keyed by `(workspaceID, qualifiedAppRef,
bindingDigest)`. It is operator policy of the same shape as a HostFS profile
grant, not a per-run capability token.

**Rationale**: The decision mechanism (`hostfs/readgrant`, overlay write, the
current trusted decision) is built for *guest-initiated, operator-after-the-fact*
approval; `readgrant.Manifest.Validate` even binds `sessionID`. Trusted Host App is
the *operator's own up-front intent*, so a decision that dies with the run is a
category error and the cause of the deadlock. Durable profile policy read each
run is exactly how HostFS profile grants already work, and does not violate
constitution V (per-run authority still regenerates; this is policy the run
consults).

**Alternatives considered**: (A) make the approved per-run decision persist
across runs — rejected: complicates the decision lifecycle and strains the
per-run-authority principle. (C) document safe-only, never fix trusted —
rejected: leaves the headline `code .` native path permanently dead.

## D2 — Landing site is `runProjectionGrantChecker`, not `decisionHostAppGrantChecker`

**Decision**: Add the persistent-grant check inside
`runProjectionGrantChecker.TrustedGrantActive` (`run_dataplane.go:517`), before
the existing per-run decision lookup. Delete or explicitly document the
`decisionHostAppGrantChecker` twin (`hostcap_projection.go:335`).

**Rationale**: `StartRunDataPlane` wires only `runProjectionGrantChecker` into
the broker (`run_dataplane.go:178` → `HostApp: hostAppProjection`).
`decisionHostAppGrantChecker` has only test callers — the production path never runs
it. The spike edited exactly this function and the real `code .` behavior
changed, confirming the site. Editing the twin would change nothing real
(FR-011).

**Alternatives considered**: editing `decisionHostAppGrantChecker` (the earlier
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

## D4 — Grant command derives workspaceID but promotes the run-observed digest (revised after implementation probe)

**Decision (revised)**: `hideout allow host-app code` in the project directory
derives `workspaceID` itself (the deterministic path, proven equal to a run's by
T008a), but takes `qualifiedAppRef` and `bindingDigest` from a run-written
request rather than computing them independently. Flow: a trusted-mode run with
no grant fails closed AND records a request (workspaceID + qualifiedAppRef +
bindingDigest, all run-accurate) under `profiles/<p>/host-app-trust-request.json`;
`allow host-app code` derives the current workspaceID, reads the request, verifies
`request.workspaceID == derived workspaceID` (so it can only promote a request
for the project the operator is standing in), and writes the grant using the
derived workspaceID plus the request's app ref + digest. `deny host-app code`
removes the derived workspace's grant (FR-001, FR-006).

**Rationale — why not derive the digest too**: `ComputeBindingDigest` keeps
`ObservedIdentityDigest` in the digest UNLESS the binding is `IdentityDeferred`,
and `identityDeferred = (Access == safe)`. In trusted mode the built-in VS Code
binding's access is compiled to `ask-each-run`, so `identityDeferred` is false
and the digest depends on the run-time observed editor identity. A grant command
computing the digest independently would (a) need to observe the host editor
itself, with a different `forbiddenRoots` set than a run, and (b) be unverifiable
in unit tests (no real editor). Promoting the run-written digest keeps the grant
keyed on exactly what the run will present, with equality by construction, and it
is the path the spike already proved on real Lima. `workspaceID` stays derived
(T008a) so the promotion is bound to the current project, not just the most
recent request.

**Alternatives considered**: fully independent derivation of both workspaceID
and digest — rejected: the digest depends on run-time editor observation
(above), so independent computation risks silent non-match and cannot be unit-
verified. The small UX cost (run `code .` once to record the request, then
`allow host-app code`) is acceptable because the fail-closed refusal already names
the grant command (FR-003), making the sequence self-guiding.

## D5 — Fail-closed refusal names the grant command

**Decision**: When trusted mode has no matching grant, the projected open refuses
with no host launch and the message names `hideout allow host-app code` (run in the
project directory). No stale decision is left behind.

**Rationale**: FR-003 and the 2026-07-20 walkthrough finding — the current
dead-end refusal is the usability bug. The broker already carries a guest-visible
`Stderr` line for host-app outcomes (038/2ccdd40), so the refusal message has a
delivery channel.

## D6 — Drift and revocation

**Decision**: A grant matches only when all key fields equal the current run's
values; a changed `workspaceID` (different project) or `bindingDigest` (changed
editor build/binding) simply does not match, re-requiring a grant. Switching the
profile to safe mode deletes all trusted host-app grants for the profile (extend the
existing `invalidateProjectionGrantsForProfile` path); `deny host-app code` deletes
one. Grant existence is shown by `profile host-app-mode`.

**Rationale**: FR-006/FR-007/FR-008. Matching-by-equality gives drift
re-confirmation for free; no separate expiry timer is needed (out of scope per
spec). Safe-mode revocation reuses the existing invalidation hook.

## D7 — Security invariants (the 030 four seams)

**Decision**: (1) grant lives under `profiles/<p>/`, guest-unreachable, `0600`,
atomic write — a guest writing the workspace cannot mint/refresh/read it;
(2) keyed by Core-derived workspace identity, never a guest path;
(3) drift (workspace/app identity) re-requires a grant; (4) visible via
`host-app-mode`, revocable via safe-mode/`deny host-app code`, and every grant/reuse/
refuse/revoke is audited.

**Rationale**: Directly the spec's four security invariants; mirrors the
guest-unreachable placement already proven for `host-app-mode.json`. The residual
trusted-mode risk (guest-writable workspace may carry `.vscode` tasks) is
inherent to trusted mode, identical under the old per-run path, disclosed on
`host-app-mode trusted`, and mitigated by safe mode being the default.

## D8 — Evidence and delivery discipline

**Decision**: Emit typed audit for grant/reuse/refuse/revoke. Prove behavior
with Go unit/contract tests (match, miss, drift, guest-cannot-forge, safe
unaffected) plus a real-Lima lane (grant → separate-run reuse → refuse without
grant → revoke). Every new assertion gets a mutation proof; every new judge a
negative fixture (constitution 1.3.0).

**Rationale**: FR-008, SC-001..SC-006, and the project's mutation/negative-
fixture requirement. The spike already demonstrated the real-Lima loop manually;
this makes it a repeatable, asserted lane.

## D9 — Operator surface generalized from `ide` to `host-app`

**Decision**: The draft named the commands `hideout allow host-app` /
`profile host-app-mode <p> trusted-host-app`. On operator review this was rejected as
too concrete ("who is the host app?") for a Core capability that is deliberately
application-agnostic (`host.app.open-resource`). The shipped surface is fully
generic: the operator types `hideout allow host-app <command>` or
`hideout deny host-app <command>`, sets the mode with
`hideout profile host-app-mode`, and the launch/audit mode value is
`trusted-host-app`. Core carries no domain word like "ide": the internal Go
types, the grant store file (`host-app-trust-grants.json`), the request file
(`host-app-trust-request.json`), the schema, and this spec directory all use the
generic `TrustedHostAppGrant*` / `trusted-host-app` spelling.

**Rationale**: There is no compatibility burden (no external users yet), so the
rename was free, and the generic surface matches the projection's actual design
(a terminal editor, a browser, adb, etc. could all be host apps). The
appopen package's own doc already promised "no editor- or vendor-specific
vocabulary", which the old `trusted-host-app` value contradicted.

## D10 — Batch adversarial review (constitution 1.3.0)

**Decision**: Fresh-eyes falsification pass against the four security seams and
the FRs, run 2026-07-20 against the shipped code (not memory). Attempts and
outcomes:

- *Guest forges/reads a grant by writing `/workspace`* → refuted: the grant and
  request live under `storeRoot/profiles/<p>/`, never the workspace
  (`TestTrustedHostAppGrantGuestWorkspaceWriteCannotForge`).
- *A different workspace or drifted binding reuses a grant* → refuted: match
  requires exact `(workspaceId, qualifiedAppRef, bindingDigest)` equality and
  trusted mode (`TestTrustedHostAppGrantMatchRequiresTrustedModeAndExactKeys`,
  T017 drift cases); real-Lima confirmed `--workspace X` and `cd X && allow`
  derive the same workspaceID (grant reused, exit 0).
- *Trusted mode with no grant silently opens or falls back to safe* → refuted:
  fail-closed refusal, no host launch, exit 126, names `hideout allow host-app
  code` (real-Lima + `hostapp_test.go`).
- *Two production grant checkers* → refuted: `runProjectionGrantChecker` is the
  sole production wiring (`run_dataplane.go:178`); `decisionHostAppGrantChecker` has
  only test callers and is documented TEST-ONLY (FR-011).
- *A stale `trusted-host-app` literal still drives behavior* → refuted: zero
  occurrences remain in code/scripts/schema; the audit value is
  `trusted-host-app`, asserted in the gate.

**Outcome**: No open finding. Build, vet, gofmt, full `go test`, gate0, and
markdownlint all green; the persistent-grant real-Lima lane is codified and its
command sequence was verified end-to-end this session.
