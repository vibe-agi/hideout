# Network Privacy Architecture

<!-- markdownlint-disable MD013 -->

## Contract

Network Privacy defines how Hideout routes guest network traffic while keeping
proxy credentials out of the target process environment.

This document follows [architecture-principles.md](architecture-principles.md).

## Product Position

Hideout supports two primary network modes:

```text
direct
tun2socks
```

`direct` is a compatibility mode. It lets the target use network access through
the backend's default route and does not hide the user's network identity.

`tun2socks` is the privacy mode. It routes guest traffic through a TUN device
and an operator-configured proxy without placing proxy credentials in target
env.

## Important Boundary

`tun2socks` is an engine. It is not the whole privacy policy.

Hideout still owns:

- proxy secret resolution;
- route setup;
- proxy endpoint bypass route;
- DNS policy;
- route verification;
- fail-closed behavior;
- `doctor` and `explain` status;
- audit of network setup decisions.

## Domain Model

```text
NetworkPlan
  mode
  proxySecretRef
  proxyEndpoint
  guestProxySecretPath
  routeEngine
  dnsPolicy
  localBypassHosts
  runtimeVerify
  failClosed
  reason
```

Modes:

```text
direct
  routeEngine=none
  proxyEnv=absent
  leakRisk=host network identity visible

tun2socks
  routeEngine=tun2socks
  proxySecretRef=required
  proxyEnv=hidden from target
  defaultRoute=guest TUN device
  proxyEndpointRoute=bypass TUN
```

## Direct Mode

Direct mode must be explicit in `explain` and `doctor`:

```text
Network: direct
Network privacy: host network identity may be visible
Proxy env in target: absent
```

Direct mode is allowed because many tools need compatibility. It must not be
marketed as hiding IP address, DNS behavior, or network origin. These `explain`
and `doctor` declarations are the product answer for direct-mode risk; no extra
acknowledgment flow is required in WebUI or TUI.

## Tun2socks Mode

Tun2socks mode must satisfy:

- proxy secret is resolved by Hideout setup;
- proxy URL is written to a session-only file with restrictive permissions;
- target env does not contain proxy credentials;
- guest default route points to the TUN device after setup;
- proxy endpoint route bypasses the TUN device to avoid loops;
- DNS behavior is defined and verified for the backend;
- failure to verify routes fails closed before the target command runs.

## DNS Policy

DNS is part of network privacy. A successful tun2socks process is not enough.

DNS policy options:

```text
backend-default
  DNS uses backend resolver. Allowed only when documented as a leak risk.

proxy-mediated
  DNS queries are routed through proxy-compatible behavior or resolved by the
  proxy path.

blocked-until-defined
  Fail closed when Hideout cannot verify DNS behavior for the selected backend.
```

Phase 1 may keep direct mode as `backend-default` and supports tun2socks privacy
mode with route verification. DNS verification remains backend-specific
hardening governed by this policy and the release gates. Until that hardening
ships, `tun2socks` mode does not claim DNS leak protection: a guest resolver on
a connected backend subnet can bypass the TUN default route.
[threat-model.md](threat-model.md) records this as an explicit non-claim, and
Gate 3 does not yet include a DNS leak check.

## Route Verification

Runtime verification must prove:

- default route is changed to the TUN device;
- proxy endpoint route does not use the TUN device;
- local bypass entries are explicitly listed;
- tun2socks process remains alive after route replacement;
- failure produces a clear `doctor` and audit reason.

Audit fields:

```text
mode
routeEngine
proxySecretRef=present
targetProxyEnv=absent
defaultRouteVerified=true|false
proxyEndpointBypassVerified=true|false
dnsPolicy
decision
reason
```

Audit must not include proxy credentials.

## Secret Handling

Proxy secrets are SecretRef values. They must not appear in:

- target env;
- `explain`;
- audit;
- broker requests;
- profile JSON output;
- TUI/WebUI logs.

Setup components may receive the secret through a session-only file or process
environment that is not inherited by the target command.

## Backend Requirements

For each backend, the capability matrix records tun2socks support, DNS
verification, and cleanup. Finer-grained facts — TUN device creation, default
route and proxy bypass route setup, and required privileges — are backend
adapter implementation concerns validated by the network gates, not separate
matrix rows.

## Phase Plan

### Phase 1

- direct mode;
- proxy env hidden from target;
- tun2socks route bootstrap for privacy mode;
- audit and doctor explain direct risk.

### Next Product Increment

- add DNS verification per supported backend;
- add `doctor --fix` for missing tun2socks;
- expose network state in Manager/TUI/WebUI.

### Later

- per-domain policy: the allow/deny/route judgment is a constrained JS decision
  point, while route setup, verification, and fail-closed behavior stay in Go;
- Tor-specific mode.

## Open Questions

- Should tun2socks be opt-in or onboarding-recommended?
- Which proxy schemes are supported first?
- How should DNS verification work in Lima vs Linux container?
