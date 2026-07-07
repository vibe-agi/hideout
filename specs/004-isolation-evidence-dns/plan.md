<!-- markdownlint-disable MD013 -->

# Implementation Plan: Isolation Boundary Evidence And DNS Leak Closure

**Branch**: `004-isolation-evidence-dns` | **Date**: 2026-07-06 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/004-isolation-evidence-dns/spec.md`

## Summary

Close the tun2socks DNS resolver bypass structurally and make isolation
evidence repeatable. Privacy mode blocks the connected-subnet bypass routes in
the guest network bootstrap so no resolver on that subnet (including the
Lima-injected `192.168.5.3`) has a non-TUN path; the leak is closed by
construction, not detected. Blocking alone does not provide working DNS, so
privacy mode requires a resolver whose DNS is verified to traverse the privacy
path; a connected-subnet-only environment is refused and fails closed with a
clear diagnostic. Working mediated DNS is provided by an in-scope guest-local
DoH stub (`hideout-dns-stub`): it receives DNS on 127.0.0.1:53 and forwards each
query as DoH/HTTPS to the declared mediated resolver over the TUN and the SOCKS
CONNECT proxy, and the guest resolver is pointed at it; 004 closes the leak and
proves it end to end on real Lima. Gate 3 is the observable proof: forward — the
guest resolver is the DoH stub and a target-style resolution + HTTPS fetch
succeed through the mediated DoH path; reverse — every captured connected-subnet
resolver is unreachable after the block (fail closed if the check cannot run).
The isolation gates (Gate 2/3/4 + env-image) record per-gate results into the
existing
release-evidence manifest rather than a new format. Two evidence-cleanliness
gaps close: machine-id non-derivability and InitTask audit redaction. No silent
fallback: privacy failure never degrades to direct, Lima failure never to
native.

## Technical Context

**Language/Version**: Go 1.25.0 plus existing POSIX shell gate scripts.

**Primary Dependencies**: Existing packages — `internal/network`
(`BootstrapScript`/`CleanupScript`/`Prepare`, the host-side generator of the
guest bootstrap shell), `internal/backend/lima` (runs the guest bootstrap as
root before the target), `internal/manager/run_network.go` (RuntimeVerify
wiring), `internal/audit` and `internal/inittask` (redaction), plus the gate
scripts and `schemas/release-dogfood.schema.json`. One new guest-local product
helper is added — `hideout-dns-stub`, which mediates DNS as DoH over the privacy
path — delivered like the existing tun2socks/hostfsd helpers; a controlled DNS
listener test fixture is also retained. The rest reuses existing mechanisms.

**Storage**: No new persistent store. Evidence extends the existing
`.hideout-release-evidence` bundle. Guest-side verification writes to the
existing session `network/` evidence files.

**Testing**: `go test ./...`, `scripts/test-gate0.sh`, and the real-Lima gates
(`test-gate2-lima.sh`, `test-gate3-hidden-proxy.sh` with the new DNS assertion,
`test-gate4-host-escape.sh`, `test-env-image.sh`) producing per-gate evidence.
Unit tests for the bootstrap route-blocking generation and the DNS listener;
the bidirectional proof itself is a real-Lima gate.

**Target Platform**: macOS host with Lima; the bypass being closed is the Lima
usernet resolver on the guest connected subnet. Native remains a weak harness
and must never satisfy an isolation claim.

**Performance Goals**: The route-blocking adds a small fixed number of `ip
route` commands to the existing guest bootstrap; no per-query overhead.

**Constraints**: The connected-subnet leak is closed structurally (iptables
drops DNS to the connected-subnet resolvers, fail-closed); working DNS is
provided by pointing the guest resolver at the DoH stub (overriding
`/etc/resolv.conf` and, when present, systemd-resolved via `resolvectl`), both
rolled back at cleanup. Bidirectional proof required before a run is reported
mediated. Fail closed when enforcement cannot be established. No
silent fallback. Redaction contract applies to every new evidence field.

**Scale/Scope**: One network-bootstrap change, one new test DNS service, Gate 3
DNS assertion, an isolation-evidence aggregation over existing gates into the
existing manifest, and two redaction fixes. Single-operator macOS/Lima scale.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: PASS. Strengthens the boundary: closes a documented DNS
  leak structurally and fails closed when enforcement cannot be established.
  No privacy→direct, Lima→native, or ambient fallback (FR-004).
- **Typed Authority**: PASS. The route-blocking is generated host-side in
  `network.go` and executed as the existing Go-owned guest bootstrap; no script
  or config gains new authority. The DNS listener is a test-only helper, not a
  product capability.
- **Workspace And Policy**: PASS. No workspace, HostFS, mount, or profile
  authority change. Network policy is unchanged except the added structural
  DNS enforcement and its verification.
- **Generality And Provider Scope**: PASS. The bypass-route block is expressed
  as a backend capability that a backend must satisfy before serving privacy
  mode; the Lima usernet resolver is handled as that backend's concrete case,
  not hardcoded as generic product semantics. The DNS listener is a lab/smoke
  fixture.
- **Evidence And Redaction**: PASS. Evidence extends the existing manifest with
  per-gate results and an environment snapshot; every new field passes the
  deterministic redaction contract. The two redaction fixes (machine-id,
  InitTask audit) make the evidence itself clean.
- **Backend And Distribution**: PASS. Real Lima is required for the isolation
  gates; native cannot satisfy an isolation claim. The one new product-path
  helper, `hideout-dns-stub`, is delivered through the existing
  helper-distribution mechanism (like tun2socks/hostfsd), consistent with the
  constitution's "more than one binary" distribution model; the DNS listener
  remains a test-only command, like the socks5 gate.
- **Gates**: Gate 0 for schema/docs/static contracts; the real-Lima gates for
  the DNS closure and evidence; `go test ./...` for bootstrap generation, the
  DNS listener, and the redaction fixes.
- **Status And Docs**: `docs/threat-model.md` (the connected-subnet DNS
  non-claim becomes a closed claim for non-root paths, AND a new A3 guest-root
  routing non-claim is added), `docs/network-privacy-architecture.md`
  (structural DNS mediation + the A3 limitation), `docs/privacy-run-design.md`
  (network DNS policy), `docs/STATUS.md` (network row + the two redaction
  known-issues), `docs/privacy-run-test-plan.md` (Gate 3 DNS assertion +
  isolation evidence).

**Pre-design result**: PASS. No constitution violation or complexity exception.

## Project Structure

### Documentation (this feature)

```text
specs/004-isolation-evidence-dns/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── dns-mediation.md
│   └── isolation-evidence.md
└── tasks.md        # generated by /speckit-tasks after this plan
```

### Source Code (repository root)

```text
internal/
├── network/            # BootstrapScript: block connected-subnet bypass routes;
│                       # guest verify: bidirectional DNS proof; CleanupScript
│                       # rollback; DNSPolicy string reflects enforced behavior
├── backend/lima/       # runs the guest bootstrap (unchanged mechanism)
├── audit/              # machine-id non-derivability
└── inittask/           # InitTask audit through deterministic redaction

internal/dnsstub/       # NEW: guest-local DoH stub (RFC 8484), product-path helper

internal/testproxy/
└── dns/                # NEW: controlled DNS listener (UDP+TCP, ordinary non-privileged port), records queries — retained test fixture

cmd/
├── hideout-dns-stub/   # NEW: product-path DoH stub binary (delivered like tun2socks/hostfsd)
└── hideout-gate-dns/   # NEW: exposes the DNS listener like cmd/hideout-gate-socks5

schemas/
└── release-dogfood.schema.json  # per-gate results + environment snapshot objects

scripts/
├── test-gate3-hidden-proxy.sh   # add bidirectional DNS leak assertion
├── test-phase1.sh               # fix env-image misplacement; isolation-evidence orchestration
├── test-release-dogfood.sh      # write per-gate results + snapshot into manifest
└── gate-result helper           # shared per-gate result emission

docs/
├── threat-model.md
├── network-privacy-architecture.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
└── STATUS.md
```

**Structure Decision**: Stay inside existing packages; the only new components
are one test-only DNS service (`internal/testproxy/dns` + `cmd/hideout-gate-dns`)
mirroring the existing socks5 gate, and a shared gate-result emission helper.
The structural DNS closure lives entirely in `internal/network` (bootstrap +
verify + cleanup), reusing the existing root-privileged guest bootstrap and the
RuntimeVerify hand-off.

## Phase 0: Research

See [research.md](research.md).

## Phase 1: Design

See [data-model.md](data-model.md),
[contracts/dns-mediation.md](contracts/dns-mediation.md),
[contracts/isolation-evidence.md](contracts/isolation-evidence.md), and
[quickstart.md](quickstart.md).

## Post-Design Constitution Check

- **Privacy Boundary**: PASS. Structural closure with fail-closed enforcement;
  bidirectional proof prevents a passing-but-leaking check.
- **Typed Authority**: PASS. Enforcement is Go-generated bootstrap; the DNS
  listener never enters the product path.
- **Workspace And Policy**: PASS. No authority broadening.
- **Generality And Provider Scope**: PASS. Backend-capability framing; Lima
  resolver handled as its case; fixtures are test-only.
- **Evidence And Redaction**: PASS. Manifest extension and both redaction fixes
  covered by contracts and success criteria.
- **Backend And Distribution**: PASS. Real Lima required; native excluded from
  isolation claims.
- **Gates**: PASS. Quickstart maps each requirement to unit/Gate 0/real-Lima
  evidence.
- **Status And Docs**: PASS. Doc updates enumerated above and carried to tasks.

## Complexity Tracking

No constitution violations or exceptional complexity are required. The new
product subsystem is the guest-local DoH stub (`hideout-dns-stub`) that makes DNS
usable over the privacy path; it is unavoidable because blocking the bypass alone
leaves privacy mode with no working DNS. The reverse proof needs no dedicated
service — it queries each captured connected-subnet resolver directly and
confirms it is unreachable after the block. A controlled UDP+TCP DNS listener is
retained as a test fixture (the repo's only other test network service, socks5,
is TCP-CONNECT only), but it is not on the reverse-proof path.

Scope boundary recorded so tasks do not diverge: 004 blocks the bypass and
fails closed when a mediated resolver is not declared. It builds an in-scope
guest-local DoH stub (`hideout-dns-stub`) to make DNS usable over the privacy
path (superseding the earlier draft that deferred this). The stub path makes
privacy mode usable out-of-box; conflating it with the closure was the review
gap this plan resolves.
