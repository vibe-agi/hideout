# Data Model: Guest Privilege Separation And Risk Audit

<!-- markdownlint-disable MD013 -->

## Guest Privilege Status

Represents the per-run privilege separation result.

Fields:

- `version`: status schema version.
- `status`: `enforced`, `degraded`, or `unknown`.
- `profile`: profile name.
- `backend`: backend name.
- `environmentId`: optional reusable environment ID.
- `targetIdentity`: target identity summary.
- `setupIdentity`: setup identity summary.
- `checks`: ordered privilege check results.
- `reason`: human-readable status reason.
- `guidance`: operator hint, such as recreate or base-image replacement.
- `createdAt`: timestamp.

Validation:

- exactly one status value is present;
- `enforced` requires non-root target, failed passwordless sudo checks, and
  either no privileged setup requirement or a proven setup identity;
- `degraded` requires a risk reason;
- `unknown` requires a missing-proof reason.

## Target Identity

Represents the guest identity that runs untrusted target commands.

Fields:

- `user`: guest username.
- `uid`: numeric user ID when known.
- `home`: target home path when known.
- `sudoN`: result for `sudo -n true`.
- `absoluteSudoN`: result for `/usr/bin/sudo -n true`.
- `canPasswordlessSudo`: boolean derived from sudo checks.

Validation:

- `uid == 0` cannot produce `enforced`;
- `canPasswordlessSudo == true` produces `degraded`;
- missing checks produce `unknown` unless backend explicitly marks unsupported.

## Hideout Setup Identity

Represents the Hideout-owned privileged setup path.

Fields:

- `kind`: `root-control-ssh`, `root-helper`, `shared-sudo`, or `none-required`.
- `available`: whether the setup path can be used.
- `separateFromTarget`: whether target can use the same authority.
- `credentialLocation`: redacted control-plane location class, not a raw path.
- `proof`: short proof label, such as `system-provisioned-root-key`.

Validation:

- `shared-sudo` cannot produce `enforced`;
- raw credential values and private key paths never leave local control-plane
  evidence;
- target-writable locations are invalid for setup credentials.

## Privilege Check Result

Represents one check performed before target launch.

Fields:

- `name`: `target.uid`, `target.sudo-n`, `target.absolute-sudo-n`,
  `setup.identity`, or `setup.credential-location`.
- `status`: `pass`, `fail`, `unsupported`, or `error`.
- `observed`: bounded, redacted observation.
- `error`: redacted failure reason.
- `checkedAt`: timestamp.

Validation:

- observations cannot contain setup secrets, broker tokens, UI tokens, or
  `HIDEOUT_SECRET_*` values;
- command output is bounded and redacted.

## Privileged Setup Event

Represents a Hideout control-plane setup or cleanup action that required guest
privilege.

Fields:

- `action`: `hideout.privileged_setup` or `hideout.privileged_cleanup`.
- `category`: `network`, `dns`, `hostfs`, `cleanup`, or `future-apply`.
- `setupIdentityKind`: setup identity kind.
- `status`: `started`, `completed`, `failed`, or `skipped`.
- `reason`: redacted reason.
- `session`: session ID.
- `profile`: profile name.

Validation:

- target root attempts must not be logged as setup events;
- requested setup failure is fail-closed and visible.

## Target Root Attempt Event

Represents target root-sensitive intent captured through command proxy or the
008 command adapter.

Fields:

- `action`: `target.root_attempt`.
- `command`: command symbol.
- `argvSummary`: bounded argument summary.
- `adapterId`: adapter ID when present.
- `separationStatus`: run's privilege status.
- `decision`: deny or audit-only proposal.
- `reason`: redacted reason.

Validation:

- command-name events do not imply absolute-path interception;
- evidence wording follows the current privilege status.

## State Transitions

```text
unknown -> enforced
unknown -> degraded
enforced -> degraded   # environment drift or sudo risk discovered
degraded -> enforced   # only after recreate or fresh proof
```

Pre-009 environments start at `unknown` or `degraded`; they do not transition
to `enforced` without recreate and fresh setup identity proof.
