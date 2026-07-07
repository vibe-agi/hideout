<!-- markdownlint-disable MD013 -->

# Data Model: Isolation Boundary Evidence And DNS Leak Closure

## DNS Bypass-Route Block (guest network state)

The structural enforcement generated into the guest bootstrap. Not a stored
record — it is routing state established before the target runs and rolled back
at cleanup.

- `bypassRoutes`: the connected-subnet resolver routes that would bypass the
  TUN default route (for Lima, the `192.168.5.0/24` link-scope route carrying
  the injected `192.168.5.3` resolver).
- `action`: block the connected-subnet bypass route, established immediately
  after the default route is set to `hideout0`, so no resolver on that subnet
  has a non-TUN path. Blocking closes the leak; it does not by itself provide
  working DNS.
- `mediatedResolverRequired`: privacy mode requires a resolver whose DNS is
  verified to traverse the privacy path (see DNS Mediation Proof). A
  connected-subnet-only environment (default Lima resolver only) is refused and
  the run fails closed with a clear diagnostic.
- `rollback`: the inverse action, added to the existing cleanup that restores
  the default route and tears down `hideout0`.

Rules:

- Any resolver on the connected subnet has no non-TUN path after the block; the
  block does not depend on resolver configuration.
- The block coexists with a verified mediated resolver; blocking without a
  mediated path is fail-closed (usable diagnostic), never silent DNS breakage
  and never fallback to leaking.
- Providing a turnkey mediated resolver for the default connected-subnet config
  (a guest-local stub / DNS-over-proxy path) is out of scope for this slice and
  is the follow-on that makes privacy mode usable out-of-box.
- Enforcement is expressed as a backend capability: a backend must be able to
  establish this block and verify a mediated resolver before it may serve
  privacy mode.

## DNS Mediation Proof (guest-side verification)

The bidirectional observable proof performed in the existing guest-side verify
block before the target launches. Fail-closed on either half.

- `forward`: the guest resolver is the DoH stub, and a target-style resolution
  through the normal system resolver path resolves a name and completes an HTTPS
  fetch — DNS traversed the privacy path (DoH over the TUN and the CONNECT
  proxy), not merely a synthetic probe.
- `reverse`: every real upstream (connected-subnet) resolver captured before the
  resolver override is unreachable — a direct query to it fails after the block.
  Mandatory: if it cannot be run (no captured resolvers, no query tool), the run
  fails closed.
- `result`: mediated only when both forward and reverse pass; otherwise the run
  is not reported mediated and fails closed.

Rules:

- A static route judgment MUST NOT substitute for either observable proof.
- The forward proof MUST include a target-style resolution, not only a synthetic
  probe.
- The mediated resolver is a precondition: a connected-subnet-only environment
  cannot pass the forward proof and fails closed.
- Claim scope is honest: the proof covers the tested resolver path and the
  structural bypass block, not every libc/DNS-mechanism implementation detail.
- Direct mode is out of scope: host-resolver DNS in direct mode is not a leak.

## Controlled DNS Listener (test-only, retained fixture)

A test service, `internal/testproxy/dns` exposed by `cmd/hideout-gate-dns`,
mirroring `internal/testproxy/socks5` + `cmd/hideout-gate-socks5`. Superseded on
the proof path: the DoH stub design proves the reverse direction by confirming
the real connected-subnet resolvers are unreachable (direct query fails), so
this listener is no longer used as the Gate 3 reverse-proof observation point.
It is retained as a fixture (with a `--count-file` assertion surface).

- `listen`: UDP and TCP on an ordinary (non-privileged) port on a host-side
  address the gate controls. The guest reaches it via a connected-subnet route
  the gate installs, modelling the `192.168.5.3` bypass — no `:53` binding on
  macOS and no binding of the real Lima subnet address.
- `observed`: records queries received (count and, for diagnostics, names).
- `assertion`: exposes "received N queries" so the reverse proof can assert
  zero after the route block.
- Lifecycle: prints its address on stdout, serves until SIGTERM, like the
  socks5 gate.
- Scope: test/lab fixture only; never part of the product runtime path.
- Equivalence: the same route-block mechanism that stops this controlled
  resolver on the connected subnet also covers the real `192.168.5.3` on the
  same subnet, so proving it here proves the mechanism.

## Isolation Evidence Artifact (manifest extension)

Extends the existing `hideout.release-dogfood.v1` manifest; every new field is
added in lockstep with `schemas/release-dogfood.schema.json`
(`additionalProperties:false` throughout).

- `isolationGates`: array of gate results, each with:
  - `id`: gate identity (`gate2-lima`, `gate3-hidden-proxy`, `gate4-host-escape`,
    `env-image`).
  - `backend`: backend used (Lima; native never satisfies an isolation claim).
  - `environmentName`: the named environment exercised, when applicable.
  - `result`: `passed` | `failed` | `not-run`.
  - `reason`: required when `not-run` (for example, Gate 4 without host
    prerequisites, env-image without a declared image URL).
  - `auditPath`: reference to the run's audit for the gate.
  - `boundarySummaryRef`: reference to the Boundary Summary for the gate.
- `environmentSnapshot`: what repeatability holds fixed and what it does not:
  - `proxyMode`: privacy/direct and the operator proxy identity.
  - `hostPrerequisites`: presence of the Gate 4 browser/escape scenario, image
    URL, etc.
  - `externalContext`: the uncontrolled external-network / DNS-upstream snapshot,
    recorded for context and explicitly excluded from the equivalence judgment.

Rules:

- A gate that did not run MUST be recorded as `not-run` with a reason, never
  omitted or marked passed.
- All values pass the deterministic redaction contract; no control-plane secret
  appears; proxy stays redacted as today.
- The manifest command constant must accommodate the isolation-evidence
  orchestration (relax the fixed `command` const to an enum or superset).

## Gate Result File (shared emission contract)

The per-gate machine-readable result each isolation gate writes when
`HIDEOUT_RELEASE_EVIDENCE_DIR` is set (precedent: `test-gate0.sh` already
consumes that variable).

- `path`: `$HIDEOUT_RELEASE_EVIDENCE_DIR/gates/<gate>.json`.
- `fields`: `{ id, result, reason, auditPath, boundarySummary, environmentName }`.
- The manifest writer aggregates these files into `isolationGates`; each gate's
  human-readable output is unchanged.

## Redaction Fixes (relationship changes)

- **machine-id non-derivability**: the generated machine-id becomes independent
  of any displayed identity reference; no displayed value yields the raw
  machine-id. Must not perturb the 003 environment identity model.
- **InitTask audit redaction**: InitTask audit emission routes through the same
  `internal/audit` deterministic redaction as the rest of audit; no
  control-plane detail bypasses it.
