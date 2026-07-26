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
- the operator proxy URL is consumed by a host-side per-environment gateway;
- proxy URL is written to a session-only file with restrictive permissions;
- target env does not contain proxy credentials;
- guest default route points to the TUN device after setup;
- proxy endpoint route bypasses the TUN device to avoid loops;
- DNS behavior is defined and verified for the backend;
- failure to verify routes fails closed before the target command runs.

A proxy running on the same host is configured with its host-loopback URL
(`127.0.0.1`), while a remote proxy keeps its normal remote hostname. The
operator URL is never handed to the guest. Hideout instead gives privileged
guest setup a separate authenticated gateway endpoint at
`host.lima.internal`; its generated credentials remain control-plane material
and do not enter target env or public evidence.

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

Phase 1 keeps direct mode as `backend-default` and enforces `proxy-mediated`
DNS for tun2socks privacy mode structurally. After the default route moves to
the TUN, the guest bootstrap:

1. starts a guest-local DoH stub (`hideout-dns-stub`) that receives ordinary DNS
   on `127.0.0.1:53` and forwards each query as DoH (RFC 8484, HTTPS) to the
   declared mediated resolver, reached by IP (for example
   `https://1.1.1.1/dns-query`). The DoH request is HTTPS, so it traverses the
   TUN, tun2socks, and the SOCKS CONNECT proxy like any other connection — no
   SOCKS UDP ASSOCIATE is needed;
2. points the guest resolver at the stub (overriding `/etc/resolv.conf` and, when
   present, systemd-resolved via `resolvectl`);
3. blackholes the connected-subnet resolvers so no query can bypass the TUN, and
   rolls everything back at cleanup.

Because blocking the bypass closes the leak but does not by itself provide
working DNS, privacy mode requires a mediated resolver and refuses a
connected-subnet-only environment (fail closed). A target that gains guest root
(A3) can still rewrite the routing table or resolver configuration to restore a
bypass; that is a recorded non-claim in [threat-model.md](threat-model.md).

This closure is validated on real Lima: Gate 3 proves it end to end — the guest
resolves a name through the mediated DoH path (`dns_forward=ok`) and completes
an HTTPS request (`https_request=ok`) while the connected-subnet leak is blocked
and the proxy secret stays hidden. For Lima,
the privileged route/DNS bootstrap runs through Hideout's root-control setup
identity, not through target-user passwordless sudo; Gate 3 also asserts
`privilege_status=enforced` and `privileged_setup=network` so DNS closure is tied
to the 009 setup path.

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

Privacy runs also emit `network.gateway.observe`, a redacted set of
protocol-stage counters (`accepted`, authentication, request parsing, route
selection, upstream dial, and upstream connection). It contains no address,
destination, credential, URL, or raw error. Its scope is explicitly
`environment-window`: concurrent sessions share an environment gateway, so
the counters diagnose which hop was reached but do not claim exact per-session
attribution.

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
verification, guest privilege separation, setup identity, and cleanup.
Finer-grained facts — TUN device creation, default route and proxy bypass route
setup, and exact helper commands — are backend adapter implementation concerns
validated by the network and privilege gates.

## Phase Plan

### Phase 1

- direct mode;
- proxy env hidden from target;
- tun2socks route bootstrap for privacy mode;
- DNS mediation through the guest-local DoH stub with Gate 3 forward/reverse
  proof;
- Lima privileged network setup through the 009 setup identity;
- audit and doctor explain direct risk.

### Next Product Increment

- add `doctor --fix --dry-run|--apply` for missing tun2socks;
- expose network state in Manager/TUI/WebUI.

### Later

- per-domain policy: the allow/deny/route judgment is a constrained JS decision
  point, while route setup, verification, and fail-closed behavior stay in Go;
- Tor-specific mode.

## Open Questions

- Should tun2socks be opt-in or onboarding-recommended?
- Which proxy schemes are supported first?
- How should DNS verification work in Lima vs Linux container?
