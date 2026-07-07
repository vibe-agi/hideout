<!-- markdownlint-disable MD013 -->

# Security & Isolation Requirements Quality Checklist

**Purpose**: Unit-test the requirements themselves — completeness, clarity,
consistency, measurability, coverage — for the DNS leak closure, isolation
evidence, and redaction work. This validates how the requirements are written,
not whether the implementation works.
**Created**: 2026-07-07
**Feature**: [spec.md](../spec.md)

**Focus (inferred)**: security/network correctness of the DNS-mediation model
and the isolation-evidence requirements; reviewer/gate depth. No clarifying
questions asked — the domain and rigor are unambiguous from the spec.

## Requirement Completeness

- [ ] CHK001 - Is the mediated-resolver precondition fully specified — what makes a resolver count as "verified mediated" — rather than only stating that one is required? [Completeness, Spec §FR-003a/003b]
- [ ] CHK002 - Are requirements defined for the multi-resolver case (guest exposes several resolvers, only some bypassing), or only the single-resolver case? [Completeness, Edge Case]
- [ ] CHK003 - Is the full set of `not-run` conditions enumerated per isolation gate (Gate 4 without browser, env-image without URL, and any others)? [Completeness, Spec §FR-010]
- [ ] CHK004 - Are the exact fields the evidence artifact must record specified per gate (id, backend, environment name, result, reason, audit path, boundary summary)? [Completeness, Spec §FR-009]
- [ ] CHK005 - Is the environment snapshot's content specified — which dimensions are held fixed for equivalence versus recorded-only? [Completeness, Spec §SC-005]
- [ ] CHK006 - Are the success conditions for both redaction fixes defined (machine-id non-derivability; InitTask audit pass-through)? [Completeness, Spec §FR-011/012]
- [ ] CHK007 - Is the required rollback of the bypass-route block specified for the cleanup path? [Completeness, Spec §FR-003a]

## Requirement Clarity

- [ ] CHK008 - Is "the privacy path" defined unambiguously (proxy, TUN, or both) so forward/reverse proofs reference the same thing? [Clarity, Ambiguity]
- [ ] CHK009 - Is "connected-subnet-only environment" defined precisely enough to decide, without judgment, when to fail closed? [Clarity, Spec §FR-003a]
- [ ] CHK010 - Is "target-style resolution through the normal system resolver path" concrete enough to be unambiguous, or open to a synthetic-probe interpretation? [Clarity, Spec §FR-003b]
- [ ] CHK011 - Is "observable proof" specified with enough precision that neither half can be satisfied by a static route-table check? [Clarity, Spec §FR-003b]
- [ ] CHK012 - Is the failure diagnostic content specified (names the resolver/route, redacts secrets) rather than left as "a clear diagnostic"? [Clarity, Spec §FR-002]

## Requirement Consistency

- [ ] CHK013 - Do spec, plan, research, data-model, and contract now describe the same pure-block + separate-mediated-resolver model, with no residual "block or redirect" / "any resolver works" wording? [Consistency, Conflict]
- [ ] CHK014 - Is "structural, not a point-in-time check" consistent with the "single observable verification at target execution" backstop, or do they read as conflicting? [Consistency, Spec §FR-003a]
- [ ] CHK015 - Are the no-silent-fallback rules stated consistently across DNS-block failure, proxy failure, and Lima failure? [Consistency, Spec §FR-004]
- [ ] CHK016 - Is the US1 "fully-mediated configuration proceeds and resolves" scenario reconciled explicitly with "004 does not build a turnkey mediated resolver"? [Consistency, Conflict, Spec §US1]

## Acceptance Criteria Quality

- [ ] CHK017 - Can "the check is not theater" be objectively measured (known-bad configuration fails, known-good passes, both halves asserted)? [Measurability, Spec §SC-002]
- [ ] CHK018 - Is "equivalent artifact" for repeatability defined measurably — exactly which fields must match and which are snapshot-only? [Measurability, Spec §SC-005]
- [ ] CHK019 - Is "no deterministic derivation yields the raw machine-id" objectively verifiable as written, rather than aspirational? [Measurability, Spec §FR-011]
- [ ] CHK020 - Are the isolation gates' pass/fail/not-run results defined as machine-readable outputs, or only as human-readable pass strings? [Measurability, Spec §FR-010]

## Scenario & Edge Case Coverage

- [ ] CHK021 - Are requirements defined for "enforcement cannot be established" — fail closed, no fallback to direct/native/ambient? [Coverage, Spec §FR-003/004]
- [x] CHK022 - Is the residual of the structural block addressed — can a guest-root process re-add the bypass route after the bootstrap block, and is that in or out of scope? [Coverage, Edge Case, Gap] — RESOLVED: recorded as an A3 guest-root non-claim (out of scope), Spec §Constitutional Alignment + Edge Cases + contract.
- [ ] CHK023 - Is it required that the Gate 3 known-good path use a controlled mediated resolver rather than relying on the leak for resolution? [Coverage, Spec §Gate 3]
- [ ] CHK024 - Are requirements defined for a gate whose prerequisites are partially present (e.g., env-image URL set but unreachable)? [Coverage, Edge Case, Gap]

## Non-Functional & Backend Scope

- [ ] CHK025 - Is the backend-capability framing (a backend must establish the block and verify a mediated resolver before serving privacy mode) stated as a requirement, not only narrative? [Completeness, Clarity]
- [ ] CHK026 - Are redaction requirements defined for every new evidence and diagnostic field, not just existing ones? [Coverage, Spec §FR-014]
- [ ] CHK027 - Is it required that native never appear as the backend for a passed isolation claim? [Completeness, Spec §FR-004]

## Dependencies & Assumptions

- [ ] CHK028 - Is the assumption that a mediated resolver is reachable through the proxy documented, with its absence handled as fail-closed? [Assumption]
- [ ] CHK029 - Is the dependency on tun2socks DNS behavior captured, and the Phase-0 empirical check (does current Gate 3 resolve via the leak) recorded as a required de-risking step? [Assumption, Dependency, Spec Research]
- [ ] CHK030 - Is the out-of-scope boundary (no turnkey mediated resolver, no daemon/shared-env/credential work) stated so tasks cannot drift into building it? [Assumption, Spec §FR-015]

## Notes

- These items test whether the requirements are well-written and ready for
  `/speckit-tasks`, not whether any code behaves correctly.
- Highest-risk cluster: the block-vs-mediated distinction (CHK001, CHK009,
  CHK013, CHK016) — the exact ambiguity that took two review rounds to
  converge. If any of these fail, tasks will diverge between "blackhole DNS"
  and "real mediated DNS."
- CHK022 (guest-root re-adds the bypass route) is a genuine potential gap: the
  structural block runs in the root bootstrap before the target; whether a
  later guest-root process can undo it, and whether that is in scope, may not
  be pinned.
