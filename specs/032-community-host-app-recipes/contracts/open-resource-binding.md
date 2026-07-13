# Contract: Immutable Open-Resource Binding

<!-- markdownlint-disable MD013 MD060 -->

## Compile At Run Start

Manager loads built-in and enabled exact revisions for the selected profile,
rechecks registry/source/permission/identity state, resolves conflicts, and
compiles one immutable registration per command:

```text
command -> action -> pack/revision/binding -> grammar
        -> host.app.open-resource -> qualified app -> access/safety profile
        -> profile/session/environment identity
```

Only a new run receives newly enabled commands. A failed compile starts no
broker or target. Generated shims contain fixed command, action, and binding
identity and occupy PATH ahead of a same-named guest binary.

## Guest Intent

`hideout.open-resource-intent/v2` contains exactly:

```json
{
  "resources": [
    {"guestPath": "/workspace/src/main.go"}
  ],
  "location": {"line": 12, "column": 3},
  "windowMode": "reuse"
}
```

The intent has no resource kind, relative path, portal ref, appRef, pack,
binding, capability, result policy, access mode, host/executable path, raw argv,
or host result field. Unknown fields fail strict decode. Core independently
revalidates every field after parsing and derives kind/relative/portal identity
from the live session, then checks it against the binding's allowed resource
kinds.

## Broker Dispatch

For every host-app request the broker:

1. authenticates the session token;
2. validates request command/action/registration ownership;
3. resolves the immutable binding by command and binding digest;
4. parses argv with that binding's strict grammar;
5. rejects any request/binding mismatch or override;
6. derives the qualified app and provider internally;
7. resolves and authorizes the resource;
8. enforces safe or exact run-scoped decision admission;
9. revalidates resource and observed app identity;
10. builds Core-owned argv/state, launches, audits, and returns only bounded
    launched/refused status plus a typed recovery code.

At no point can a request select a different enabled app. Any failure ends the
projection; it never invokes generic host exec or the shadowed guest command.

## Resource Resolution

### Workspace

The current session maps the guest path to one host path beneath its pinned
workspace and rechecks canonical containment immediately before launch.

### HostFS Portal

Core locates the active same-session portal from authoritative HostFS state and
requires existing content/tree authority for the exact requested resource.
Discover-only visibility is not enough. Reserved roots, stale/ended portals,
owner/profile mismatch, policy deny, symlink retarget, or authority expiry deny.
The recipe, intent, decision preview, guest response, and public evidence never
receive the host path.

## Elevated Decision Identity

Ask-each-run uses the existing default-deny decision lifecycle and binds:

```text
capability + qualified app + pack revision + binding + command
+ session + profile + workspace + environment + identity + resource class
```

Approval for any different value is unusable. Timeout, owner loss, update,
disable, revoke, identity/resource drift, or session end invalidates it.
