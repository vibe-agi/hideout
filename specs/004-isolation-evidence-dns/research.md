<!-- markdownlint-disable MD013 -->

# Research: Isolation Boundary Evidence And DNS Leak Closure

All decisions are grounded in the current code; file:line references are from
the codebase survey performed for this plan.

## T000 empirical finding (real Lima, 2026-07-07)

Ran the DNS-closure Gate 3 on real Lima (vz, aarch64) with
`HIDEOUT_GATE3_MEDIATED_RESOLVER=1.1.1.1`. Result:
`proxy_env_absent=yes` printed (bootstrap completed, including the DNS mediation
DNAT verify) then `curl: (6) Could not resolve host: example.com`.

Interpretation:

- The connected-subnet block WORKS: with the closure, the guest can no longer
  resolve via the `192.168.5.3` bypass (before the closure, that resolution
  succeeded — confirming Gate 3 previously relied on the leak, the exact thing
  T000 was meant to confirm). The reverse proof (leak closed) is empirically
  validated.
- The mediated DNS path does NOT deliver: the DNAT redirects DNS to the mediated
  resolver over the TUN, but resolution fails. Root cause is UDP-relay
  reachability — the host-side SOCKS proxy's UDP ASSOCIATE relay is on host
  loopback / a dynamic port the Lima guest cannot reach, so UDP DNS through the
  test proxy cannot be delivered. libc uses UDP DNS by default, and DNS-over-TCP
  (which the SOCKS CONNECT proxy could carry) is not forced.

Resolution (2026-07-07): the working mediated DNS path was implemented with a
guest-local DoH stub (`hideout-dns-stub`). The stub receives DNS on
`127.0.0.1:53` and forwards each query as DoH (RFC 8484, HTTPS) to the mediated
resolver reached by IP; because DoH is HTTPS/TCP, it traverses the SOCKS CONNECT
proxy without needing UDP ASSOCIATE. The guest resolver is pointed at the stub
(overriding `/etc/resolv.conf` and systemd-resolved via `resolvectl`); the
connected-subnet resolvers are blackholed. Two further real-Lima findings were
fixed along the way: the guest uses systemd-resolved (`127.0.0.53`), so a
loopback REDIRECT could not un-NAT connected-UDP replies (switched to a resolver
override); and Lima installs a `LIMADNS` nat chain. Gate 3 now passes end to end
on real Lima (`proxy_env_absent=yes`, `dns_mediated=yes`, `dns_forward=ok`,
`https_request=ok`),
so both the leak closure and the working mediated DNS are validated. A SOCKS5
UDP ASSOCIATE relay was also added to the test proxy but is not on the DoH path.

## Decision: Block the bypass and require a verified mediated resolver; fail closed otherwise. Do not build a turnkey mediated resolver in this slice

**Rationale**: Blocking the bypass and providing a working mediated DNS are two
different things, and conflating them was the core gap. Lima injects the
usernet resolver at `192.168.5.3` on the guest's `192.168.5.0/24` connected
subnet at VM boot, before any Hideout code runs; Hideout never touches
`/etc/resolv.conf` (verified: the generated `limaConfig` has no `dns`/
`hostResolver` fields, `internal/backend/lima/lima.go:66-79`). The
connected-subnet `/24` is a kernel link-scope route more specific than the TUN
default route, so DNS to `192.168.5.3` bypasses TUN. The current bootstrap runs
`tun2socks --device tun://hideout0 --proxy "$proxy_url"` with no `--dns` flag
(`network.go:383`), and the current Gate 3 `curl https://example.com`
(`test-gate3-hidden-proxy.sh:206-224`) resolves its name *before* connecting —
so today that resolution most likely succeeds via the bypass route itself.
Blackholing `192.168.5.3` would therefore break DNS (fail-closed, no leak but no
DNS); naively redirecting `192.168.5.0/24` into `hideout0` does not by itself
make DNS work, because the query would target the Lima guest-subnet resolver
that the external proxy cannot reach.

The decision, faithful to the "prove or fail closed" spine: this slice's
guarantee is closure and fail-closed, not turnkey mediated DNS. The bootstrap
blocks the connected-subnet bypass route immediately after `network.go:387`
(default route to `hideout0`), and rolls it back in `CleanupScript`. Privacy
mode then *requires* a resolver whose DNS is verified to traverse the privacy
path (bidirectional proof below). A connected-subnet-only environment — the
default Lima config with only `192.168.5.3` — is refused: the run fails closed
with a clear diagnostic that a mediated resolver is required. This slice does
NOT build a guest-local stub resolver or a DNS-over-proxy path to make the
default resolver work; that is the follow-on that makes privacy mode usable
out-of-box, and is explicitly out of scope here. 004 closes the leak and proves
it; it does not promise turnkey DNS for the default connected-subnet resolver.

Implementation Phase 0 must empirically confirm on real Lima whether Gate 3's
current DNS resolves via the bypass; if so (expected), the gate is updated to
provide a controlled mediated resolver for the known-good path rather than
relying on the leak, so closing the leak does not silently break the gate.

**Alternatives considered**:

- Blackhole only (fail closed on any DNS): rejected — makes privacy mode
  unusable even with a mediated resolver; the block must coexist with a
  verified mediated path.
- Build a guest-local stub / DNS-over-proxy resolver in this slice so the
  default resolver works: rejected as scope — it is a real networking feature,
  larger than this evidence/closure slice; recorded as the follow-on.
- Point-in-time resolver check at prepare or exec (spec Q1 A/B): rejected in
  clarification — races a mutable resolver config; blocking the route removes
  the condition.
- Editing `/etc/resolv.conf`: rejected — mutable and guest-rewritable mid-run.

## Decision: Rollback the bypass block in CleanupScript

**Rationale**: The bootstrap's routing changes are already rolled back in
`CleanupScript` (`network.go:428-437` restores the default route and tears down
`hideout0`). The added bypass-route block gets a matching rollback there so the
environment is left as found, consistent with the existing symmetric
setup/cleanup.

**Alternatives considered**: Leaving the block in place — rejected; cleanup
must be symmetric and the environment restored.

## Decision: Bidirectional DNS proof rides the existing guest-side verify block

**Rationale**: Verification is already guest-side and run-time, not host-side:
Lima runs with `Verified=false, RuntimeVerify=true` (manager sets
`RuntimeVerify: backend=="lima"` in `run_network.go:32-33`, and `opts.Verified`
is never true in non-test code), so `Prepare` hands verification to the guest
bootstrap (`network.go:140-149`), which already verifies the default route,
local-bypass routes, and proxy route before the target launches
(`network.go:388-404`) with `exit 127`-style fail-closed. The bidirectional DNS
proof is added into that same block: forward proof — the guest resolver is the
DoH stub and a target-style resolution plus HTTPS fetch succeed through the
mediated DoH path; reverse proof — every captured connected-subnet resolver is
unreachable after the block (a direct query fails). No new plan state or verification
timing is introduced; it extends an existing hook that already fails closed
before exec.

**Alternatives considered**:

- Host-side verification in `Prepare`: rejected — the guest is where routing is
  observable, and the existing RuntimeVerify hand-off already puts verification
  there.
- Forward proof only or reverse proof only: rejected in clarification —
  bidirectional is required; forward alone does not show the bypass is closed,
  reverse alone does not show the privacy path carried the query.

## Decision: Two controlled DNS listeners the gate fully controls; not an attempt to observe the real Lima resolver

> Superseded by the T000 empirical finding above: the DoH stub design proves the
> reverse direction by confirming the real connected-subnet resolvers are
> unreachable (a direct query fails), so the gate no longer needs controlled
> known-bad/mediated listeners. The `hideout-gate-dns` listener is retained as a
> fixture. The rest of this decision is kept for history.

**Rationale**: The reverse proof cannot literally watch the real `192.168.5.3`
— it is Lima's usernet gateway DNS inside Lima's network stack, not something
we can bind or observe, and binding `:53` on the real connected-subnet address
from the macOS host is not generally possible. So the gate instead sets up a
*controlled* model of the bypass, which is faithful because the same
route-block mechanism (blocking the more-specific `192.168.5.0/24` route)
covers both the controlled known-bad resolver and the real `192.168.5.3`.

The gate establishes two controlled resolvers, both host-side helpers bound to
ordinary (non-privileged) ports, reached from the guest via routes the gate
configures — no `:53` binding on macOS and no binding of the real Lima subnet
address:

- **known-bad resolver**: a new `internal/testproxy/dns` (UDP+TCP) exposed by
  `cmd/hideout-gate-dns`, mirroring `internal/testproxy/socks5` +
  `cmd/hideout-gate-socks5` (prints address on stdout, serves until SIGTERM,
  exposes a "received N queries" assertion). The gate installs a
  connected-subnet route in the guest pointing DNS at it, modelling the
  `192.168.5.3` bypass. After the block, it MUST receive zero queries.
- **mediated resolver**: a second controlled DNS endpoint reachable only
  through the privacy path (via the proxy). The forward proof is that a query
  resolves through it and is observed on the privacy path.

Because the proxy is TCP-CONNECT (the repo's socks5 gate rejects non-CONNECT,
`socks5.go:148-151`, so UDP ASSOCIATE is unavailable), the mediated resolver is
reached via DNS-over-TCP, which the existing socks5 gate can carry and observe;
the socks5 gate gets a small addition to record observed CONNECT targets
(today `handleConn` is silent). The listener runs where the gate can control
routing (host-side, reachable via `host.lima.internal` / the slirp gateway for
the connected-subnet model); the plan's tasks pin the exact addressing.

**Why this is equivalent to the real bypass**: the closure is a route block on
the connected-subnet `/24`; proving it stops the controlled known-bad resolver
on that subnet proves the mechanism that also blocks the real `192.168.5.3` on
the same subnet. The gate additionally asserts that the guest's own resolver
path (the default resolver) no longer has a non-TUN route to the connected
subnet.

**Alternatives considered**:

- Observe the real `192.168.5.3`: rejected — un-bindable/un-observable; the
  controlled-resolver model on the same subnet tests the same route block.
- A high-port `dig @addr -p port` synthetic probe alone: rejected as the *only*
  proof — it shows a probe can reach a port, not that the system resolver path
  is mediated; the gate therefore also asserts a target-style resolution (see
  forward-proof decision).
- Adding UDP ASSOCIATE to the socks5 gate: rejected — more surface than needed
  for this slice.

## Decision: DNS proof lives in Gate 3, extending its existing inline hook

**Rationale**: `test-gate3-hidden-proxy.sh` already runs `hideout run --network
tun2socks -- sh -eu -c '<inline>'` (`:206`) and asserts proxy-env absence and a
TUN-routed HTTPS request via the guest-inline / host-`grep -q` pattern
(`:207-236`). Gate 3 gains three assertions in that same block: (1) a
target-style resolution — the guest resolves a controlled name through its
normal system resolver path (e.g. `getent hosts` / the same path `curl` uses),
now pointed at the DoH stub, observed to traverse the privacy path — so the
forward proof is not merely a synthetic DNS-over-TCP probe but the actual
resolver path a target uses; (2) the forward-proof marker (`dns_mediated=yes`
plus the HTTPS fetch); (3) the reverse-proof (every captured connected-subnet
resolver is unreachable after the block — a direct query fails). Gate 3 already
starts a host-side helper (`start_local_proxy`, `:106-128`) and reclaims it in
cleanup, so the mediated-resolver plumbing follows an established pattern.

**Scope of the claim (honest)**: this slice proves (a) the connected-subnet
bypass route is structurally blocked and (b) a controlled name resolves through
the privacy path via the target's normal resolver path. It does NOT claim to
cover every libc/system-resolver implementation detail or every DNS mechanism a
guest could use; those beyond the tested resolver path are out of scope and are
handled by the same structural route block, not by per-mechanism assertions.

**Alternatives considered**: A standalone DNS gate — rejected; the DNS leak is a
privacy-mode property and Gate 3 is the privacy gate, so it belongs there.
Synthetic probe only — rejected; without a target-style resolution it proves a
probe can traverse the proxy, not that the resolver path is mediated.

## Decision: Extend the release-dogfood manifest with per-gate results and a snapshot

**Rationale**: The evidence bundle already exists
(`schemas/release-dogfood.schema.json`, `hideout.release-dogfood.v1`) with a
manifest writer (`test-release-dogfood.sh:267-363`) and validator, recording
version/commit/host/tools/proxy(redacted)/releaseArtifact/cleanup. Its gap: the
`gates` field is a static array of seven fixed names (`:347-355`, schema
`:229-272`) with no per-gate result, no audit path, no Boundary Summary, no
environment name, no environment snapshot, because `test-phase1.sh
--release-candidate` runs as one opaque subprocess and only its overall exit
code is captured. The spec requires extending this bundle, not a new one. Add
an `isolationGates` object array (id, backend, environmentName, result
passed|failed|not-run, reason, auditPath, boundarySummaryRef) and an
`environmentSnapshot` object (proxy mode, host prerequisites, uncontrolled
external-network/DNS-upstream context). Because the schema is
`additionalProperties:false` everywhere, the schema is extended in lockstep.

**Alternatives considered**:

- A separate isolation-evidence bundle format: rejected in clarification and
  spec (FR-008) — a parallel evidence subsystem is the anti-pattern.
- Recording only overall pass/fail: rejected — FR-010 requires per-gate
  passed/failed/not-run, and not-run must be explicit (e.g., Gate 4 without host
  prerequisites, env-image without `HIDEOUT_ENV_IMAGE_URL`).

## Decision: Per-gate results via a shared emission helper into the evidence dir

**Rationale**: No gate emits a machine-readable result today; each uses `echo
"<gate>: passed"` plus exit code, and there is no shared format. `test-gate0.sh`
already consumes `HIDEOUT_RELEASE_EVIDENCE_DIR` (`:44`), giving a precedent. A
shared bash helper lets each isolation gate write
`$HIDEOUT_RELEASE_EVIDENCE_DIR/gates/<gate>.json` with
`{id,result,reason,auditPath,boundarySummary,environmentName}` when the evidence
dir is set; the manifest writer aggregates those files. This keeps each gate's
human output unchanged and adds machine-readable results without making the
orchestrator parse stdout.

**Alternatives considered**:

- Orchestrator wraps each gate and parses stdout: rejected — brittle; the
  per-gate JSON file is deterministic and each gate already knows its own
  environment name and result.

## Decision: Fix env-image so it is a real gate that can record not-run

**Rationale**: `--env-image` is currently misplaced — its dispatch runs inside
`print_plan()` (`test-phase1.sh:51-54`), so it executes only on the print-plan
path and is not in the main gate sequence, and it `exit 2`s when
`HIDEOUT_ENV_IMAGE_URL` is unset. For 004 to fold the image gate into the
evidence bundle, env-image must run in the real gate sequence and record
`not-run` (with reason "no image URL declared") when the URL is absent, rather
than hard-exiting.

**Alternatives considered**: Leaving env-image out of the isolation evidence —
rejected; spec FR-008 names the image gate as part of the isolation evidence set.

## Decision: machine-id non-derivability decouples machine-id from the identity ID

**Rationale**: The known gap is that the generated machine-id can be recovered
by stripping a prefix from a displayed identity ID (they share a body). The fix
makes the machine-id independent of any displayed identity reference (a distinct
random value or one-way derivation), so no displayed value yields the raw
machine-id. This must be checked against the 003 named-environment identity
model — 003 fixed environment identity to the image digest, backend config
version, and workspace and removed toolsHash — so the machine-id change must
not perturb the environment fingerprints or drift behavior (FR-013, SC-007),
which is verified by the existing environment tests staying green.

**Alternatives considered**: Redacting the identity ID on display — rejected;
the identity ID is legitimate traceability and user-facing; the fix is to make
the machine-id not derivable from it, not to hide the identity ID.

## Decision: InitTask audit passes through the shared deterministic redaction

**Rationale**: InitTask's own audit writer does not run through
`RedactDetails`, so control-plane detail can bypass the deterministic redaction
the rest of audit enforces. The fix routes InitTask audit emission through the
same redaction path (`internal/audit` `RedactDetails`) so control-plane material
is stripped identically everywhere. This is a redaction-pass-through change, not
a change to what InitTask records.

**Alternatives considered**: A separate InitTask redaction — rejected; one
deterministic redaction contract, applied uniformly, is the invariant.
