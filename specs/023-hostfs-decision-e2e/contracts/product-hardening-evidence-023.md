# Contract: 023 Product-Hardening Evidence

<!-- markdownlint-disable MD013 -->

023 reuses `hideout.product-hardening-evidence/v1`.

## Required Local-Fast Completeness

A complete local-fast 023 manifest must contain passed proof entries for:

- `023.hostfs-decision.local-fast.lifecycle`
- `023.hostfs-decision.local-fast.claim-race`
- `023.hostfs-decision.local-fast.timeout`
- `023.hostfs-decision.local-fast.visibility`
- `023.hostfs-decision.local-fast.redaction`

The manifest may also contain `023.hostfs-decision.real-gate2.not-run`.

## Required Real Gate 2 Completion

A complete real Gate 2 023 manifest must contain:

- `023.hostfs-decision.real-gate2.lifecycle` with `status=passed`; or
- `023.hostfs-decision.real-gate2.not-run` with `status=not-run` and an
  explicit prerequisite reason when real proof is not required.

When `--require-real` is used, `not-run` is still written but the command exits
non-zero.

## Covered Claims

Proof entries must map to one or more of:

- `023.FR-001`: E2E proof lane records mode and coverage.
- `023.FR-002`: target reads reflect staged overlay state before apply.
- `023.FR-003`: host lower remains unchanged before apply.
- `023.FR-004`: apply mutates only planned host state.
- `023.FR-005`: stale/conflict apply fails closed.
- `023.FR-006`: decisions visible through local surfaces without private data.
- `023.FR-007`: exactly one claimant wins.
- `023.FR-008`: approve/deny/timeout outcomes are audited.
- `023.FR-010`: native/local-fast does not satisfy real HostFS claims.
- `023.FR-011`: control-plane redaction proof.
- `023.SC-007`: coverage matrix lists covered and uncovered operations.

## Redaction Rules

The evidence manifest and referenced public artifacts must not contain:

- claim tokens;
- provider-private refs;
- private overlay object paths or `hfwobj_` ids;
- broker/UI tokens;
- `HIDEOUT_SECRET_*` names or values;
- generated machine-id material;
- Core control-plane field names when surfaced as raw implementation details.

User-visible fixture path labels, operation names, and content summaries may be
preserved when they do not disclose control-plane material.
