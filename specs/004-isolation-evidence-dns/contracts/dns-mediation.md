<!-- markdownlint-disable MD013 -->

# Contract: Privacy-Mode DNS Mediation

Behavioral contract for the structural DNS closure and its bidirectional proof.
This is a network-behavior and gate contract, not a service API.

## Structural Enforcement

- In privacy (`tun2socks`) mode, the guest network bootstrap MUST block every
  connected-subnet route that would let a resolver be reached off the TUN
  default route, established immediately after the default route is set to the
  TUN device.
- After the block, no resolver on the connected subnet has a non-TUN path. A
  resolver-configuration change during the run cannot create a leak because the
  bypass route does not exist.
- Blocking closes the leak but does not provide working DNS. Privacy mode
  therefore REQUIRES a resolver whose DNS is verified to traverse the privacy
  path (see Bidirectional Proof). A connected-subnet-only environment (default
  Lima resolver only) MUST be refused: the run fails closed with a clear
  diagnostic that a mediated resolver is required.
- A block without a verified mediated path MUST fail closed with a usable
  diagnostic; it MUST NOT silently break DNS and MUST NOT fall back to leaking
  or to direct networking.
- Working mediated DNS is provided by a guest-local DoH stub (in scope): it
  receives DNS on 127.0.0.1:53 and forwards each query as DoH/HTTPS (RFC 8484) to
  the declared mediated resolver reached by IP, over the TUN and the SOCKS CONNECT
  proxy. The guest resolver is pointed at the stub. A connected-subnet-only
  environment with no mediated resolver declared is still refused (fail closed).
- The enforcement MUST be rolled back at cleanup, symmetric with the existing
  route restoration.
- The network plan's DNS policy statement MUST describe the enforced behavior
  and MUST NOT claim mediation it does not enforce (replacing the current
  "connected-subnet resolvers are not yet verified" wording).

## Bidirectional Proof (DoH end-to-end)

The mediated DNS is provided by the guest-local DoH stub, so the proof observes
the real resolver path end to end rather than a controlled listener's counters:

- **Forward**: the guest resolver is the DoH stub, and a target-style resolution
  through the normal system resolver path resolves a name and completes an HTTPS
  fetch — DNS traversed the privacy path (DoH over the TUN and the CONNECT
  proxy). A synthetic probe alone MUST NOT satisfy the forward proof.
- **Reverse**: every real upstream (connected-subnet) resolver captured before
  the resolver override MUST be unreachable — a direct query to it MUST fail
  after the block. The reverse check is mandatory: if it cannot be run (no
  captured resolvers, no query tool), the run fails closed rather than passing.
- A run is reported mediated only when both halves pass. Either half failing
  fails the run closed.
- Claim scope: the proof covers the tested resolver path and the structural
  bypass block; it does not claim to cover every libc/DNS-mechanism
  implementation detail.

## Backend Capability

- DNS mediation enforcement is a backend capability. A backend MUST be able to
  establish the bypass-route block before it may serve privacy mode; a backend
  that cannot MUST fail closed rather than serve an unenforced privacy mode.
- Native remains a weak harness and MUST NOT satisfy a privacy/isolation claim.

## Non-Claim: A3 Guest-Root Routing

- The block guarantees that non-root target processes and resolver-configuration
  changes cannot reach a bypass route. It does NOT guarantee that a target which
  gains guest root (adversary A3) cannot rewrite the guest routing table to
  restore a bypass.
- The proof and Gate 3 cover the post-bootstrap, pre-target, and ordinary run
  path, NOT a hostile guest-root reconfiguring guest networking.
- Constraining the target's guest network privileges to close this is a separate
  guest-privilege-model concern, out of scope for this slice. The limitation is
  recorded as a non-claim in `docs/threat-model.md`.

## Gate 3 DNS Assertion (DoH end-to-end proof)

The observable proof is the DoH end-to-end resolution plus a reverse block check
(the guest-local DoH stub replaced the earlier controlled-listener design):

- Forward: the guest resolver is the DoH stub (`dns_mediated=yes`), a
  target-style query succeeds through the mediated DoH path
  (`dns_forward=ok`), and a separate HTTPS request completes through that path
  (`https_request=ok`).
- Reverse: every real upstream (connected-subnet) resolver captured before the
  override is unreachable — a direct query to it fails (`connected_subnet_blocked=yes`).
- Not theater: because the reverse check fails the gate if a connected-subnet
  resolver is still reachable, a run where the block did not take effect fails.
- Validated on real Lima; Gate 3 requires `HIDEOUT_GATE3_MEDIATED_RESOLVER` (a
  DoH server IP, default `1.1.1.1`).

## Failure And Redaction

- All failure diagnostics name the offending resolver/route and reason without
  exposing proxy secrets or control-plane material.
- Direct mode is explicitly outside this contract: host-resolver DNS in direct
  mode is not a leak.
