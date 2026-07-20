# Quickstart / Validation: Trusted Host-IDE Workspace Grant

<!-- markdownlint-disable MD013 -->

Runnable validation scenarios proving the feature end to end. Detailed field and
command semantics are in [data-model.md](data-model.md) and
[contracts/](contracts/).

## Prerequisites

- macOS arm64 with Lima (real projection path); a supported, code-signed VS Code.
- A dedicated store and a Git project directory.
- `hideout setup` completed; profile host-app mode set to `trusted`.

## Scenario 1 — Grant once, reuse across runs (US1 / SC-001)

1. In the project directory: `hideout allow host-app code`.
2. Run a one-shot open: `hideout run -- code .`.
3. Expected: the full native editor opens (operator's own profile, extensions
   enabled), exit success, no prompt or approval step.
4. Run `hideout run -- code .` again (a separate run): still opens natively,
   still no prompt — the grant is reused across runs.

## Scenario 2 — Fail closed with guidance when ungranted (US2 / SC-002)

1. Fresh store/profile in trusted mode, no grant for the workspace.
2. Run `hideout run -- code .`.
3. Expected: refused, no editor launched, and the stderr names
   `hideout allow host-app code` as the way to allow it.
4. Run `hideout allow host-app code`, then rerun `hideout run -- code .`: now opens
   natively (refused → granted with no other change).

## Scenario 3 — Revoke and safe-mode return to guided/safe (US3 / SC-003)

1. With a grant present and `code .` opening natively, run
   `hideout deny host-app code`.
2. Run `hideout run -- code .`: back to the refused/guided path.
3. Grant again, then `hideout profile host-app-mode default safe`.
4. Run `hideout run -- code .`: opens in the safe isolated window (grant deleted
   by the safe switch; no grant needed for safe).

## Scenario 4 — Drift re-requires a grant (US3 / SC-004)

1. Grant trusted IDE in project A; confirm `code .` opens natively in A.
2. In a different project B (trusted mode), run `hideout run -- code .`:
   refused — A's grant does not authorize B.

## Scenario 5 — Guest cannot forge a grant (US1 edge / SC-005)

1. Trusted mode, no operator grant.
2. Have the guest write a plausible grant file into the workspace (e.g. via a
   run that creates `.hideout`-looking files in `/workspace`).
3. Run `hideout run -- code .`: still refused — grants live under the
   guest-unreachable `profiles/<p>/`, not the workspace.

## Automated coverage (Gate 0 + real Lima)

- Go unit/contract (`internal/manager`, `internal/operatorintent`,
  `internal/app`): grant match, miss, drift (workspace + bindingDigest), malformed
  manifest fail-closed, safe-mode ignores + deletes grants, guest-cannot-forge,
  single production check path (FR-011). Each new assertion carries a mutation
  proof; each new judge a negative fixture.
- Real-Lima projection lane: Scenarios 1–3 asserted end to end (grant → separate-
  run reuse → refuse without grant → revoke), recorded as projection evidence.

## Cleanup

- `hideout daemon stop`; `limactl stop <instance>` / `limactl delete <instance>`;
  remove the dedicated store.
