# Contract: Operator Host-App Trust Grant Commands

<!-- markdownlint-disable MD013 -->

Reuses the `operatorintent` `allow`/`deny` surface. All commands are host-CLI +
authenticated-daemon only; no guest/broker entry point exists.

> Naming: the operator surface and the internal Go types, files, schema, and
> spec directory are all fully generic (`host-app`, not `ide`).

## `hideout allow host-app <command>`

Grant trusted (native) opening of one projected host-app command (e.g. `code`)
for the current workspace under the active profile.

- Run in the project directory. Core derives the workspace identity the same way
  a run does, then reads the exact host-app binding identifiers
  (`qualifiedAppRef`, `bindingDigest`) from the request hint a prior refused
  trusted run recorded for that `(workspace, command)`. Because the binding
  digest depends on the run-time observed app identity, the grant promotes that
  hint instead of recomputing the digest independently.
- Optional `--for-profile <name>` selects a non-default profile (mirrors the
  existing `allow` scope flag).
- Preconditions: the profile's host-app mode is `trusted`. If it is `safe`, the
  command refuses and names `hideout profile host-app-mode <p> trusted` (a grant
  while safe would be inert and misleading).
- Idempotent: granting an already-granted `(workspace, command, binding)` is a
  no-op success.
- Output: confirms the workspace + command now trusted and names the revoke
  command; no host path or secret in the output. Example:

  ```text
  native host app "code" allowed for this project under profile default
  it will open natively here; revoke with: hideout deny host-app code
  ```

- Audit: one operator-center event `action=host-app.trust`, `decision=grant`,
  with Core-derived identifiers only (`profile`, `command`, `workspaceId`,
  `qualifiedAppRef`, `bindingDigest`).

## `hideout deny host-app <command>`

Revoke the current workspace's trusted host-app grant under the active profile.

- Run in the project directory (same identity derivation) or accept the same
  scope flag.
- Removing a non-existent grant is a no-op success.
- After revoke, the next projected open returns to the safe isolated window.
- Output example:

  ```text
  native host app "code" revoked for this project under profile default; it now opens in the safe isolated window
  ```

- Audit: one operator-center event `action=host-app.trust`, `decision=revoke`.

## Fail-closed refusal message (open time)

When trusted mode is selected but no grant matches, the projected open refuses
with no host launch. The guest-visible stderr names the exact grant path:

```text
this project is not trusted for the native host app; to allow it, run on the host: hideout allow host-app code
```

## `hideout profile host-app-mode <p>` (existing, extended output)

- Continues to show `safe` / `trusted`.
- When trusted, additionally indicates whether the profile holds any standing
  host-app trust grants, so a standing grant is visible (FR-008).
- `hideout profile host-app-mode <p> safe` additionally deletes the profile's
  trusted host-app grants (revocation on mode downgrade).

## Invariants

- These commands never accept or emit a host filesystem path, host username,
  capability token, machine id, or raw guest argv.
- No guest-reachable trigger: the grant/revoke path is host CLI + authenticated
  daemon only; a guest process cannot invoke it or forge its effect by writing
  the workspace.
- Grant and revoke go through the profile mutation lock, like other profile
  policy writes.
