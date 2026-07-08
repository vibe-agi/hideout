# Contract: Guest Privilege Status

<!-- markdownlint-disable MD013 -->

## Purpose

Every run that can report guest privilege state emits a single status:
`enforced`, `degraded`, or `unknown`.

## Status Values

### `enforced`

Allowed only when all conditions hold:

- target user ID is known and non-zero;
- `sudo -n true` does not grant target privilege;
- `/usr/bin/sudo -n true` does not grant target privilege when `/usr/bin/sudo`
  exists;
- every privileged setup required by the run uses a Hideout-owned setup path, or
  no privileged setup is required;
- setup credentials are not target-readable or target-writable.

### `degraded`

Required when any condition holds:

- target user is non-root but can passwordless sudo;
- setup still uses the same sudo-capable target user;
- existing environment predates 009 identity separation;
- image/backend blocks setup identity separation but target can still run.

Default v1 behavior allows the run to continue with warning and audit unless a
profile/test path explicitly asks for enforced-only behavior.

### `unknown`

Required when checks cannot prove either enforced or degraded:

- backend is native or unsupported;
- check command cannot run;
- output is ambiguous;
- environment metadata is missing.

## Fail-Closed Rules

- A profile/test path that requests enforced-only behavior fails closed unless
  status is `enforced`.
- Requested tun2socks, DNS mediation, HostFS mount, or cleanup setup fails
  closed if no allowed setup path is available.
- No surface may convert `degraded` or `unknown` into a root-containment claim.

## Evidence Fields

Required fields:

- `status`
- `reason`
- `guidance`
- `target.uid`
- `target.sudoN`
- `target.absoluteSudoN`
- `setup.kind`
- `setup.separateFromTarget`
- `checks[]`

Forbidden fields:

- raw setup private keys;
- raw setup tokens;
- broker/UI tokens;
- `HIDEOUT_SECRET_*` backing names or values;
- raw generated machine-id.
