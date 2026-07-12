# Quickstart & Validation: Host Capability Projection

<!-- markdownlint-disable MD013 MD060 -->

Runnable validation scenarios that prove the `code` recipe works end-to-end and stays fail-closed. Gate 0 scenarios use `go test` and a local smoke; the guest-visible and privacy scenarios require real macOS arm64 Lima (Gate 2/Gate 3).

## Prerequisites

- Built `hideout` from this tree; an officially signed VS Code bundle installed in
  a package-owned supported Applications location. Ambient `PATH` is not an app
  identity source.
- For guest-visible scenarios: Lima available (`limactl`), a sanitized dedicated workspace under the operator home (used to detect the real username/home host-side only).

## Gate 0 (mechanics, no backend)

```bash
go test ./internal/hostcap/... ./internal/cmdgrammar/... ./internal/cmdproxy/... ./internal/broker/... ./internal/manager/... ./internal/profiletemplate/... ./internal/recovery/...
scripts/test-host-capability-projection-smoke.sh
```

Expected: registry validation passes; `code-open-v1` grammar maps each accepted form and denies unknown flags; intent strict-decode rejects unknown fields and non-workspace/escape resources; broker refuses out-of-allowlist args and provider-unavailable without fallback; no host path/username/token appears in any guest-facing or event payload; `projection.*` recovery codes render in human and JSON.

## Scenario 1 — `code .` opens the host workspace (US1, real Lima)

```bash
cd /path/to/sanitized/project
hideout run --profile privacy --backend lima -- code .
```

Expected: the host workspace `/path/to/sanitized/project` opens in a constrained (safe-mode) VS Code; the guest process and output never printed a host absolute path; an `ide.open` audit record exists with mode `safe`, no host path.

```bash
hideout run --profile privacy --backend lima -- code -g src/app.ts:12:3
```

Expected: VS Code opens `src/app.ts` at line 12 column 3 (mapped to the host file); guest never received the host path.

## Scenario 2 — fail-closed (US1, Gate 0 + real)

```bash
hideout run --profile privacy --backend lima -- code /etc/hosts       # non-workspace
hideout run --profile privacy --backend lima -- code ../../secret      # escape
hideout run --profile privacy --backend lima -- code --unknown-flag .  # unknown flag
```

Expected: each refuses with a typed `projection.*` code and opens nothing; none delegate to host execution or to a shadowed guest `code` binary.

## Scenario 3 — privacy three-channel verification (US2, real Lima)

```bash
scripts/test-gate2-lima.sh   # includes the projection + privacy lane
```

Expected (alias mode, privacy profile), with host username/home patterns recorded host-side only:

- workspace namespace: guest `pwd` under `/workspace`; no host username/home in `pwd`/`realpath`/args/errors;
- identity environment: `USER`/`LOGNAME`/`HOME`/hostname/git identity/config contain only synthetic identity; account home `/home/developer` distinguished from process `HOME=/hideout/profile/home`;
- mount metadata: `/proc/mounts`, `/proc/self/mountinfo`, `mount`, `findmnt` contain no host username/home/workspace path;
- per-channel detector self-test: each detector matches a deliberately-present host username/home before asserting absence;
- preserve-mode control: a `preserve` fixture exposes the host path shape, proving the alias assertions exercise the real mapping.

## Scenario 4 — safe vs trusted mode (US3, real Lima)

```bash
# default safe mode: a folder-open task must NOT run
hideout run --profile privacy --backend lima -- code .   # workspace contains .vscode/tasks.json runOn:folderOpen
# assert the host marker the task would write is absent

# request trusted mode; keep the target session alive (for example, an agent or shell)
hideout profile ide-mode privacy trusted-host-ide
hideout run --profile privacy --backend lima -- sh
# inside that guest session: code . is denied once and creates a run-bound decision

# from an operator terminal: claim, approve, then retry inside the same guest session
hideout decision claim <decision-id>
hideout decision approve --claim-token <claim-token> <decision-id>
# inside the guest: code . opens with operator config; audit mode=trusted-host-ide

# revoke from the operator terminal; the next same-session guest retry is denied
hideout decision revoke <decision-id>
hideout profile ide-mode privacy safe
# subsequent runs use safe mode again
```

Expected: safe-mode marker absent, `--disable-workspace-trust` never used; trusted denied without grant; granted → operator config; revoke → denied; explicitly selecting `safe` restores safe opens.

## Scenario 5 — inspection (US4)

```bash
hideout doctor --feature projection --format json
```

Expected: lists the `code` binding, its capability, active mode, and PATH shadow order (whether a real guest `code` is shadowed and the resolution order), with no host absolute path or secret.

## Evidence

- Gate 0 records mechanics-only product-hardening evidence.
- Real Lima Gate 2 records the guest-visible `code .`, fail-closed, privacy three-channel, and safe/trusted lanes under stable proof ids; `not-run` is honest and does not satisfy the guest-visible or privacy claims.
- Gate 3 privacy assertions must still pass with the projected/aliased environment.
