# Contract: Template Catalog

<!-- markdownlint-disable MD013 -->

## Built-In Templates

### `privacy`

- Recommended: yes.
- Network: `tun2socks` only in v1. Non-interactive setup must explicitly
  choose backend and provide proxy secret ref plus mediated resolver.
- HostFS: no workspace-external HostFS grants by default.
- Adapter packs: none installed or enabled by default.
- Privilege: does not require enforced separation and must not claim guest-root
  containment.
- Evidence label: `privacy`.

### `hardened`

- Recommended: no.
- Network: `tun2socks` only in v1 with explicit proxy secret ref plus mediated
  resolver.
- HostFS: no workspace-external HostFS grants by default.
- Adapter packs: none installed or enabled by default.
- Privilege: requires `enforced`.
- Evidence label: `hardened`.
- Failure: `degraded` or `unknown` privilege facts fail before profile
  creation unless degraded fallback is explicitly requested.

### `dev`

- Recommended: no.
- Network: practical operator-selected network, commonly `direct`.
- HostFS: no workspace-external HostFS grants by default.
- Adapter packs: none installed or enabled by default.
- Privilege: warnings only; no hardened claim.
- Evidence label: `dev`.

### `debug`

- Recommended: no.
- Network: local development/debug posture.
- HostFS: no workspace-external HostFS grants by default.
- Adapter packs: none installed or enabled by default.
- Privilege: warnings only; no privacy or hardened claim.
- Evidence label: `debug-local`.

## Catalog Invariants

- Exactly four built-in template ids exist in v1.
- `privacy` is the only recommended template.
- Every template renders a schema-valid profile.
- Every template renders zero HostFS grants.
- Every template renders zero command adapter bindings.
- Every template renders no adapter-pack installed state.
- No template records raw proxy secret values, UI tokens, broker tokens,
  `HIDEOUT_SECRET_*`, or generated machine ids in template evidence.

## Degraded Hardened Fallback

The degraded fallback is not a fifth built-in template. It is an explicit
operator decision that renders a profile with:

- original requested template: `hardened`;
- effective posture: `hardened-degraded`;
- metadata marker: `templateDegraded=true`;
- evidence warning that hardened was not achieved;
- no hardened boundary claim.
