# Contract: Operator Trusted-IDE Grant Commands

<!-- markdownlint-disable MD013 -->

Reuses the `operatorintent` `allow`/`deny` surface. All commands are host-CLI +
authenticated-daemon only; no guest/broker entry point exists.

## `hideout allow ide-trust`

Grant trusted (native) host-IDE for the current workspace under the active
profile.

- Run in the project directory. Core derives the workspace identity the same way
  a run does and reads the built-in editor binding's `qualifiedAppRef` +
  `bindingDigest`, then writes a grant entry.
- Optional `--for-profile <name>` selects a non-default profile (mirrors the
  existing `allow` scope flag).
- Preconditions: the profile's IDE mode is `trusted-host-ide`. If it is `safe`,
  the command refuses and names `hideout profile ide-mode <p> trusted-host-ide`
  (a grant while safe would be inert and misleading).
- Idempotent: granting an already-granted `(workspace, binding)` is a no-op
  success.
- Output: confirms the workspace + editor now trusted; no host path or secret in
  the output.
- Audit: one `host-app.ide-trust` grant event with Core-derived identifiers only.

## `hideout deny ide-trust`

Revoke the current workspace's trusted-IDE grant under the active profile.

- Run in the project directory (same identity derivation) or accept the same
  scope flag.
- Removing a non-existent grant is a no-op success.
- After revoke, the next projected open returns to the guided/refused path.
- Audit: one `host-app.ide-trust` revoke event.

## Fail-closed refusal message (open time)

When trusted mode is selected but no grant matches, the projected open refuses
with no host launch. The guest-visible stderr names the exact grant path, e.g.:

```text
hideout: this project is not trusted for your native editor; to allow it:
  hideout allow ide-trust
(safe mode opens an isolated editor window with no grant needed)
```

## `hideout profile ide-mode <p>` (existing, extended output)

- Continues to show `safe` / `trusted-host-ide`.
- When trusted, additionally indicates whether the current (or a) workspace is
  granted, so a standing grant is visible (FR-008).
- `hideout profile ide-mode <p> safe` additionally deletes the profile's
  trusted-IDE grants (revocation on mode downgrade).

## Invariants

- These commands never accept or emit a host filesystem path, host username,
  capability token, machine id, or raw guest argv.
- No guest-reachable trigger: the grant/revoke path is host CLI + authenticated
  daemon only; a guest process cannot invoke it or forge its effect by writing
  the workspace.
- Grant and revoke go through the profile mutation lock, like other profile
  policy writes.
