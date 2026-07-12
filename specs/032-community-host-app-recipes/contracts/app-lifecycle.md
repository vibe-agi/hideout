# Contract: Host-App Lifecycle And Manager Parity

<!-- markdownlint-disable MD013 MD060 -->

## Operations

CLI commands consume the same Core models as Manager routes:

| Product command | Manager resource | Authority |
|-----------------|------------------|-----------|
| `app add` | `app/add` plan/apply | Store exact revision and optionally enable exact reviewed binding atomically |
| `app list` | `app/list` | Read-only installed/enabled summary |
| `app inspect` | `app/inspect` | Read-only full Core-derived facts |
| `app validate` | `app/validate` | Read-only source validation, no install/trust |
| `app test` | `app/test` | Run deterministic quality vectors, no authority |
| `app enable` | `app/enable` plan/apply | Enable exact installed revision/fingerprint for one profile |
| `app update` | `app/update` plan/apply | Install candidate and expose permission diff; never inherit broadened trust |
| `app disable` | `app/disable` plan/apply | Remove from future run compilation for one profile |
| `app remove` | `app/remove` plan/apply | Disable all bindings, remove owned snapshot after checks, retain audit |
| advanced revoke | `app/revoke` plan/apply | Store-wide terminal revoke of exact revision |

No route writes raw profile JSON. API authorization and daemon transport reuse
the existing Manager boundary.

`app validate` and `app test` accept either `--path <dir>`, exact
`--git <url> --commit <40-hex>`, or an installed `<pack-id>` plus optional exact
`--revision`. Source operations acquire an immutable temporary snapshot, bind
plan/apply to its digest and permission fingerprint, and never write registry,
test-result, enablement, trust, or command-authority state.

## Add Plan

The plan is read-only outside temporary acquisition and returns:

- source kind and exact lock;
- source/manifest digest and revision;
- permission fingerprint and diff from active revision;
- commands, aliases, shadow/conflict facts;
- package/app/binding identity;
- Core-observed app identity or absence/drift;
- resource and result classes;
- compatible safety profile or elevated/unverified posture;
- profile scope and old-session impact;
- package-provided hint clearly marked untrusted;
- exact effects and required explicit acceptance.

It contains no token, raw host/executable path, repository credential, or
mutable candidate path.

## Add Apply

Apply requires the exact plan version/digests/fingerprint/profile/access choice
and explicit acceptance. It reacquires the source, revalidates all facts and app
identity, acquires registry/profile locks, writes snapshot+registry+enablement
atomically, and emits one outcome audit. Any drift or partial write leaves no
new authority. Install-only omits enablement deliberately.

TTY may confirm the plan. Non-interactive callers must provide an acceptance
flag and may pin expected digest. Missing confirmation defaults deny. There is
no daemon-generated implicit prompt.

## Update

Update is a new immutable revision. A changed permission fingerprint suspends
the old binding and requires explicit diff acceptance. Even unchanged
permissions require exact new source revision selection; source version text
does not move the binding automatically.

## Disable, Revoke, Remove

Disable affects future run compilation only and preserves installed bytes and
audit. Revoke marks an exact revision terminal and prevents all future compile
or launch. Remove first disables all profile bindings, proves package ownership,
removes only owned snapshot bytes, and preserves registry tombstone/audit.
Running sessions retain their immutable shim set but every runtime request
rechecks disabled/revoked state and therefore fails closed without fallback.

## Inspection And Recovery

CLI human, Manager JSON, doctor, audit, and Boundary Summary share stable IDs
and states. Product summary leads with command, app, access, readiness, and one
next action; technical details are expandable. Typed recovery covers malformed
source, missing git, digest drift, reserved/conflicting command, app absent,
unsafe app root/owner, identity mismatch/drift, safety unavailable, permission
review required, stale portal, disabled/revoked binding, and new-run required.

## Audit

Every applied lifecycle attempt, plus every launch/refusal after a stable
operation identity exists, emits one event with operation, exact package and
binding identity, profile scope, permission/source digests, observed status,
decision, and recovery. A parse- or plan-time read-only rejection that cannot
form a stable package/revision identity returns a typed diagnostic and does not
invent a persistent lifecycle event. Launch/refusal audit uses the validated
immutable binding, not guest metadata. Local source URL/path and app facts are operator
data; public export strips host paths and control-plane material through the
existing boundary.
