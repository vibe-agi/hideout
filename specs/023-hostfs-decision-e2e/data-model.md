# Data Model: HostFS And Decision E2E

<!-- markdownlint-disable MD013 -->

## HostFS Write Proof

Represents one staged HostFS write operation and its observed host/guest state.

Fields:

- `proofId`: stable product-hardening proof id.
- `operation`: HostFS write class (`create`, `replace`, `append`,
  `truncate`, `mkdir`, `delete`, `rename`, `chmod`, `chown`).
- `coverage`: `local-fast`, `real-gate`, `representative`, or `not-run`.
- `backend`: backend mode used for the observation.
- `hostPathSummary`: redacted host path or fixture label.
- `guestReadBeforeApply`: whether guest/read view saw staged state.
- `hostLowerBeforeApply`: hash or summary proving host lower state before apply.
- `hostLowerAfterApply`: hash or summary after apply.
- `decisionId`: public decision id.
- `status`: `passed`, `failed`, or `not-run`.
- `artifacts`: referenced logs, JSON reports, or Gate 2 outputs.

Validation:

- A `real-gate` pass requires `guestReadBeforeApply=true` and a host lower
  before/after summary.
- A local-fast pass cannot set `coverage=real-gate`.
- Public fields must not contain claim tokens, provider-private refs, private
  overlay object paths, or control-plane material.

## Decision Proof

Represents claim/resolve/timeout behavior for one actionable decision.

Fields:

- `decisionId`: public decision id.
- `kind`: decision kind, including `hostfs.write` or a generic actionable
  decision kind.
- `initialState`: starting state.
- `claimSurface`: claimant surface label.
- `winningClaim`: redacted summary proving one claimant won.
- `losingClaim`: redacted summary proving a second claimant lost or saw claimed
  state.
- `resolution`: `approved`, `denied`, `timeout-denied`, or `not-run`.
- `auditRefs`: local audit references.
- `publicRecordClean`: whether public record redaction passed.

Validation:

- Exactly one winning claimant is allowed for a single pending decision.
- A denied/timeout decision must not execute provider apply side effects.
- Claim tokens are allowed only in private command output needed to continue the
  test; they must not appear in public records or final evidence.

## Operation Coverage Matrix

Declares what the E2E run actually covered.

Fields:

- `supportedOperations`: full supported HostFS write operation list.
- `coveredLocalFast`: operations covered in local-fast decision-state proof.
- `coveredRealGate`: operations covered in real Gate 2 guest proof.
- `uncovered`: supported operations not covered by the current run.
- `coverageNote`: human-readable scope statement.

Validation:

- Every supported operation must appear in exactly one of `coveredLocalFast`,
  `coveredRealGate`, or `uncovered`.
- If `uncovered` is non-empty, evidence and docs must say representative rather
  than complete.

## Visibility Proof

Represents whether pending/resolved decisions are visible through existing
operator surfaces.

Fields:

- `surface`: `cli`, `api`, `webui-model`, or `tui-model`.
- `decisionId`: public decision id.
- `state`: pending/claimed/applied/denied/expired.
- `summaryVisible`: whether redacted summary was visible.
- `privateMaterialAbsent`: whether claim tokens/provider refs were absent.

Validation:

- UI/TUI entries are model/state visibility proof, not browser click proof.
- Surface records must agree on decision id and state.

## Product-Hardening Evidence Manifest

Reuses `hideout.product-hardening-evidence/v1`.

Required 023 proof groups:

- local-fast HostFS/decision proof;
- local-fast decision concurrency proof;
- local-fast redaction proof;
- local-fast visibility proof;
- real Gate 2 pass or not-run proof.

Validation:

- Local-fast completeness must not require a real Gate 2 pass.
- Real Gate 2 not-run requires an explicit prerequisite reason.
- Manifests must validate against the existing schema and `productevidence`
  Go validation.
