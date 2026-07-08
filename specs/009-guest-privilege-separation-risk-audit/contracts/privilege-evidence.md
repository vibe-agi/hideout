# Contract: Privilege Evidence

<!-- markdownlint-disable MD013 -->

## Audit Actions

### `guest.privilege.status`

Emitted once per run when checks complete.

Required details:

- `status`
- `reason`
- `guidance`
- `target.uid`
- `target.sudoN`
- `target.absoluteSudoN`
- `setup.kind`
- `setup.separateFromTarget`
- `checks`

### `hideout.privileged_setup`

Emitted for Hideout setup actions that require guest privilege.

Required details:

- `category`
- `status`
- `setupIdentityKind`
- `separateFromTarget`
- `reason`

### `hideout.privileged_cleanup`

Same shape as `hideout.privileged_setup`, used for cleanup.

### `target.root_attempt`

Emitted when command-name root-sensitive intent is captured.

Required details:

- `command`
- `argvSummary`
- `separationStatus`
- `adapterId`
- `decision`
- `reason`

## Boundary Summary

Boundary Summary must include:

- privilege status;
- target UID known/non-root result;
- sudo risk result;
- setup identity kind;
- guidance for degraded/unknown;
- explicit non-claim when status is not `enforced`.

## Manager, TUI, And WebUI

Local management surfaces must display:

- current status;
- status reason;
- whether status blocks an enforced-only path;
- recreate/base-image guidance for degraded environments.

These surfaces must not expose setup secrets or raw setup credential paths.

## Export And Redaction

Export/share redaction from 005 applies. Exported evidence may include
redacted setup identity class and status reasons, but not:

- setup private keys;
- setup tokens;
- root/control SSH config contents;
- raw Hideout control-plane paths that reveal secret material.

## Non-Claim Text

When status is `degraded` or `unknown`, evidence must state that Hideout does
not claim guest-root containment. When status is `enforced`, evidence may claim
target non-root/no-sudo separation for command execution, but still must not
claim protection against an adversary that already obtained guest root.
