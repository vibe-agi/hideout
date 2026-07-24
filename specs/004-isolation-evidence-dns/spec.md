<!-- markdownlint-disable MD013 -->

# Feature Specification: Isolation Boundary Evidence And DNS Leak Closure

**Feature Branch**: `004-isolation-evidence-dns`

**Created**: 2026-07-06

**Status**: Implemented — the exact claim surface and non-claims live in [docs/STATUS.md](../../docs/STATUS.md)

**Input**: User description: "004 = Isolation Boundary Evidence & DNS Leak Closure. In privacy mode, Hideout must prove that guest network identity is mediated by the configured privacy path, including DNS resolution, and must produce repeatable Lima gate evidence for isolation-sensitive behavior without silently falling back to direct, native, or ambient host paths."

## Current Status Context

Hideout is private alpha / supervised dogfood. The word "supervised" reflects a
specific gap: isolation is believed, not proven, and the threat model carries
one documented-but-open privacy hole. In `tun2socks` privacy mode the network
plan's DNS policy states in so many words that "connected-subnet resolvers are
not yet verified" — a guest-configured resolver that sits on a connected subnet
(for example a Lima slirp resolver on the guest's local network) can be reached
through a more-specific route that bypasses the TUN default route, so DNS
queries can leave through the host's real network identity. Privacy mode's
entire promise is to hide that identity, so this is a real leak that is
currently only honestly documented as a non-claim.

This feature has one spine: an isolation-sensitive claim must be proven or fail
closed, and must never be silently downgraded. It delivers two things. First,
it closes the DNS resolver bypass structurally: privacy mode blocks the
connected-subnet bypass routes so no resolver on that subnet has a non-TUN
path. Blocking closes the leak without providing working DNS, so a separate
verified mediated resolver is required; a connected-subnet-only environment
fails closed rather than leaking.
Second, it turns isolation evidence into a repeatable, retained, reviewable
artifact for the real Lima backend — Gate 2, Gate 3 (with a new DNS leak
assertion), Gate 4, and the named-environment image gate — so "believe
isolation" becomes "prove isolation."

The unifying invariant is no silent fallback: a privacy-mode failure must not
fall back to direct networking, a Lima failure must not fall back to the native
harness, and nothing degrades to an ambient host path without stopping.

This feature deliberately does not build daemon, TUI/WebUI expansion, the shared
default environment, Claude credential delivery, guest-to-host capabilities, or
marketplace/bundle trust. Those are later slices; the boundary must be proven
before the surface grows.

## Clarifications

### Session 2026-07-06

- Q: How is the DNS verification time-of-check/time-of-use race eliminated? →
  A: Structurally, not by a point-in-time check. Hideout blocks the
  connected-subnet bypass routes so no resolver on that subnet has a non-TUN
  path; bypass is made impossible by construction rather than detected, so a
  resolver change during the run cannot create a leak. Blocking closes the leak
  but does not provide working DNS, so privacy mode requires a separate verified
  mediated resolver and fails closed when none is verified (a
  connected-subnet-only environment is refused). A single observable
  verification at target execution backstops the enforcement.

- Q: What is the minimum observable proof that a run is fully mediated? → A:
  Bidirectional. Forward: the guest resolver MUST be the DoH stub and a
  target-style resolution plus HTTPS fetch MUST succeed through the mediated DoH
  path (proving the privacy path carries DNS). Reverse: every captured
  connected-subnet resolver MUST be unreachable after the block — a direct query
  to it MUST fail (proving the bypass route is blocked). Both are required: the
  forward proof alone does not show the bypass is closed, and the reverse proof
  alone does not show the privacy path carried the query. A static route-table
  judgment is at most a fast pre-check, never the proof.

- Q: Beyond the same commit, what must be held fixed for two evidence runs to
  count as equivalent? → A: The operator-declarable, controllable dimensions:
  same commit, same backend (Lima), same proxy mode including the same operator
  proxy, and the same host prerequisites (whether the Gate 4 browser/escape
  scenario is present). Uncontrollable external factors — external network
  state, the real DNS upstream — are explicitly excluded from the equivalence
  judgment and are recorded as an environment snapshot in the artifact rather
  than required to match. Repeatability is "reproducible under held-fixed
  controlled conditions," not "reproducible across arbitrary network changes."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Privacy Mode Closes The DNS Resolver Bypass (Priority: P1)

An operator runs a target in privacy mode expecting all network identity —
including DNS — to be mediated by the configured privacy path. Hideout enforces
this structurally: it blocks the connected-subnet bypass routes so no resolver
on that subnet has a non-TUN path at any time during the run. Blocking is by
construction, not detection, so a resolver change cannot create a leak. Because
blocking closes the leak without providing working DNS, privacy mode requires a
separate resolver whose DNS is verified to traverse the privacy path; a
connected-subnet-only environment is refused and the run fails closed with a
clear diagnostic rather than starting and letting DNS queries leak the real
network identity.

**Why this priority**: This closes an actual, currently-open privacy hole in
the product's headline privacy feature. Until it is closed, privacy mode can
silently leak the exact thing it claims to hide. Everything else in this slice
is evidence around this closure.

**Independent Test**: Start privacy-mode runs against two guest resolver
configurations — one whose resolver routing bypasses the privacy path and one
whose resolution is fully mediated. The test passes only if the bypassing
configuration fails closed before the target executes with a diagnostic naming
the resolver and the routing problem, and the mediated configuration proceeds
and resolves through the privacy path.

**Acceptance Scenarios**:

1. **Given** a privacy-mode run and a guest whose configured resolver is
   reachable through a route that bypasses the privacy path, **When** Hideout
   prepares the network, **Then** setup fails closed before the target executes,
   names the offending resolver and route, and no run is claimed ready.
2. **Given** a privacy-mode run whose guest DNS resolution is fully mediated by
   the privacy path, **When** Hideout prepares the network and runs, **Then**
   the run proceeds and DNS resolution is observably routed through the privacy
   path, not the host's real network identity.
3. **Given** privacy-mode network setup that cannot verify DNS routing at all,
   **When** the run is attempted, **Then** it fails closed rather than
   proceeding on an unverified assumption, and it does not fall back to direct
   networking.
4. **Given** a privacy-mode run whose bypass routes are structurally blocked,
   **When** the guest switches its effective resolver to a connected-subnet
   resolver at any point during the run, **Then** that resolver's queries still
   traverse the privacy path (or fail) rather than leaking, because the bypass
   route does not exist — the mediation does not depend on the resolver
   configuration staying unchanged.
5. **Given** a direct-mode run, **When** DNS resolution uses the host resolver,
   **Then** this is not treated as a leak, because direct mode already declares
   that it exposes normal network identity.

---

### User Story 2 - Isolation Evidence Is Repeatable And Retained (Priority: P2)

An operator runs the isolation-sensitive gates on a real macOS/Lima machine and
receives a single retained evidence artifact that records what was proven —
Gate 2 (Lima isolation), Gate 3 (hidden proxy plus the new DNS leak check),
Gate 4 (host escape), and the named-environment image gate — with enough
context (Hideout version, commit, backend, environment name, gate results,
audit and Boundary Summary references) to review and reproduce later.

**Why this priority**: Isolation evidence today is produced ad hoc by running
gates by hand. To move from supervised alpha toward beta, the same gates must
produce a reviewable, reproducible record. This does not change what the gates
prove; it makes the proof durable.

**Independent Test**: Run the isolation evidence gates on macOS/Lima. The test
passes only if a single evidence artifact is produced under the existing
release-evidence location, records each gate's identity and result, references
the audit and Boundary Summary for the runs, and can be re-produced by re-running
the same command on the same commit.

**Acceptance Scenarios**:

1. **Given** a macOS/Lima machine with the prerequisites, **When** the operator
   runs the isolation evidence gates, **Then** a single evidence artifact
   records Gate 2, Gate 3, Gate 4, and the image gate results with version,
   commit, backend, and environment identity, extending the existing release
   evidence format rather than introducing a second one.
2. **Given** a produced evidence artifact, **When** a reviewer inspects it,
   **Then** it references the audit path and Boundary Summary for each
   isolation-sensitive run and states pass/fail per gate without exposing
   control-plane secrets.
3. **Given** a gate that did not run (for example Gate 4 without the required
   host prerequisites), **When** the artifact is produced, **Then** that gate is
   recorded as not-run rather than silently omitted or falsely marked passed.

---

### User Story 3 - Evidence Surfaces Do Not Leak Identity Or Control-Plane Material (Priority: P3)

A reviewer reading audit and evidence surfaces never sees Hideout's generated
machine identity material in a form that can be reversed to the raw value, and
never sees InitTask control-plane detail that the deterministic redaction
contract is supposed to strip.

**Why this priority**: The evidence produced by this slice must itself be clean.
Two known medium-severity gaps let identity or control-plane material reach
surfaces that claim to be redacted: the generated machine-id can be recovered by
stripping a prefix from a displayed identity ID, and the InitTask audit writer
does not pass through the shared deterministic redaction.

**Independent Test**: Inspect audit, evidence, and InitTask surfaces after
representative runs. The test passes only if no displayed value can be reduced
to the raw generated machine-id, and InitTask audit entries pass through the same
deterministic control-plane redaction as the rest of audit.

**Acceptance Scenarios**:

1. **Given** any surface that displays an identity reference, **When** a reviewer
   attempts to derive the raw generated machine-id from it, **Then** no
   deterministic derivation yields the raw machine-id.
2. **Given** InitTask activity that records audit, **When** those entries are
   written, **Then** they pass through the same deterministic control-plane
   redaction contract as the rest of audit, with no control-plane detail
   bypassing it.
3. **Given** the identity redaction change, **When** environment identity and
   drift behavior from the named-environment model are exercised, **Then** they
   are unchanged — the fix must not perturb the environment identity model.

### Edge Cases

- The guest exposes multiple resolvers, only some of which bypass the privacy
  path.
- The guest resolver configuration changes during the run (a time-of-check /
  time-of-use race): resolved structurally — bypass routes are blocked so a
  resolver change cannot create a leak, so this race does not need a
  point-in-time re-check (see Clarifications).
- A guest-root (A3) target process rewrites the guest routing table after the
  bootstrap block (for example `ip route del`/`replace` to restore the bypass
  route): the structural block guarantees non-root target processes and
  resolver-configuration changes, but a target that gains guest root can
  reconfigure guest networking. This is a known limitation, not a guarantee of
  this slice (see Constitutional Alignment); closing it would require
  constraining the target's guest network privileges, which is a separate
  guest-privilege-model concern out of scope here.
- Enforcement of the bypass-route block cannot be established for the selected
  backend: the run fails closed rather than proceeding on unenforced mediation.
- The forward proof fails (a target-style resolution or its HTTPS fetch does not
  complete through the mediated DoH path), or a captured connected-subnet
  resolver is still reachable after the block: the mediated proof fails and the
  run is not reported as mediated.
- Host prerequisites (Gate 4 browser/escape) differ between evidence runs: the
  affected gate is recorded as not-run and does not break repeatability, which
  is scoped to held-fixed controlled conditions (see Clarifications).
- Privacy mode is requested on a backend that cannot verify DNS routing.
- The privacy path itself is unavailable (missing or unusable proxy) — this must
  fail closed without falling back to direct.
- The Lima backend is unavailable during an evidence run — this must not fall
  back to the native harness for an isolation claim.
- Gate 4 host prerequisites (real browser, escape scenario) are absent on the
  evidence machine.
- An evidence run is attempted on a dirty working tree or a commit that differs
  from a prior bundle.
- A resolver-route check passes but the leak still survives (the check itself is
  wrong) — the check must be validated against a known-bad configuration so that
  a passing check means a real closure, not theater.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Network setup and privacy-path verification, DNS
  routing verification, gate/evidence tooling, audit redaction, and InitTask
  audit emission. No new host reach-back capability is introduced.
- **Fail-closed behavior**: A resolver whose routing bypasses the privacy path,
  an unverifiable DNS routing state, an unavailable privacy path, and an
  unavailable Lima backend during an isolation claim all fail closed before the
  target executes. No fallback to direct, native, or ambient host networking.
- **User authority and policy**: Privacy mode is an operator choice; when its
  guarantees cannot be met the run stops rather than silently downgrading. Deny
  precedence and existing network policy are unchanged.
- **Generality and provider scope**: DNS routing verification is a backend
  capability. This slice closes the leak for the primary Lima backend and
  defines the verification as a capability a backend must satisfy before it may
  serve privacy mode; it does not hardcode one backend's networking quirk as
  generic product semantics.
- **Evidence surface**: The isolation evidence artifact extends the existing
  release-evidence bundle format; it records gate identity and results and
  references audit and Boundary Summary. Redaction contract applies to every new
  field.
- **Secret/redaction boundary**: Proxy secrets, broker/UI tokens, generated
  machine-id, and InitTask control-plane detail must not appear in target env,
  audit, evidence artifacts, or gate output. The machine-id must not be
  derivable from any displayed identity reference. Raw proxy URLs remain a flow
  obligation as already specified.
- **Backend/gate expectation**: Real Lima is required for the isolation evidence
  gates. Native remains a weak wiring harness and must never satisfy an
  isolation claim. Gate 3 gains a DNS leak assertion; Gate 4 evidence is recorded
  when its host prerequisites are present and recorded as not-run otherwise.
- **Non-claim (A3 guest-root routing)**: The structural DNS block guarantees
  that non-root target processes and resolver-configuration changes cannot
  reach a bypass route. It does NOT guarantee that a target which gains guest
  root (adversary A3 in `docs/threat-model.md`) cannot rewrite the guest routing
  table to restore a bypass. Gate 3 proves the closure for the post-bootstrap,
  pre-target, and ordinary run path, not against a hostile guest-root
  reconfiguring guest networking. Constraining the target's guest network
  privileges to close this is a separate guest-privilege-model concern, out of
  scope for this slice, and this limitation is recorded as a non-claim (to be
  reflected in `docs/threat-model.md`).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: In privacy mode, the system MUST verify that guest DNS resolution
  is routed through the configured privacy path, and MUST fail closed before the
  target executes when any configured resolver would be reached through a route
  that bypasses that path.
- **FR-002**: The DNS routing diagnostic on failure MUST name the offending
  resolver and the reason its routing bypasses the privacy path, and MUST NOT
  expose proxy secrets or control-plane material.
- **FR-003**: When privacy-mode DNS routing cannot be verified at all, the system
  MUST fail closed rather than proceed on an unverified assumption.
- **FR-003a**: Privacy-mode DNS mediation MUST be enforced structurally, not by
  a point-in-time check: Hideout MUST block the connected-subnet bypass routes
  so that no resolver on that subnet has a non-TUN path at any time during the
  run. Because the bypass route does not exist, a resolver configuration change
  between prepare and execution cannot create a leak. Blocking closes the leak
  but does not provide working DNS, so privacy mode MUST require a resolver whose
  DNS is verified to traverse the privacy path and MUST fail closed with a clear
  diagnostic when no mediated resolver is verified (a connected-subnet-only
  environment is refused). A single observable verification at target execution
  MUST backstop the enforcement.
- **FR-003b**: A run MUST NOT be reported as fully mediated without bidirectional
  observable proof: forward — the guest resolver MUST be the DoH stub and a
  target-style resolution plus HTTPS fetch MUST succeed through the mediated DoH
  path; reverse — every captured connected-subnet resolver MUST be unreachable
  after the block (a direct query to it MUST fail). A static route-table judgment
  MAY serve as a fast pre-check but MUST NOT stand in for either observable proof.
- **FR-004**: A privacy-mode failure MUST NOT fall back to direct networking; a
  Lima failure during an isolation claim MUST NOT fall back to the native
  harness; nothing MUST degrade to an ambient host path without stopping.
- **FR-005**: The network plan's DNS policy statement MUST reflect the verified
  behavior and MUST NOT claim mediation it does not enforce.
- **FR-006**: The Gate 3 privacy gate MUST include a DNS leak assertion that
  fails on a resolver configuration that would leak and passes on a fully
  mediated configuration.
- **FR-007**: The DNS routing verification MUST be validated against a known-bad
  resolver configuration so that a passing verification demonstrably corresponds
  to a closed leak, not an ineffective check.
- **FR-008**: The isolation evidence gates (Gate 2, Gate 3, Gate 4, and the
  named-environment image gate) MUST produce a single retained evidence artifact
  that extends the existing release-evidence bundle format rather than
  introducing a separate one.
- **FR-009**: The evidence artifact MUST record, per isolation-sensitive run, the
  Hideout version and commit, backend, environment name, gate identity and
  pass/fail result, and references to the audit path and Boundary Summary.
- **FR-010**: A gate that did not run MUST be recorded as not-run in the evidence
  artifact, never silently omitted or marked passed.
- **FR-011**: No surface that displays an identity reference MUST allow the raw
  generated machine-id to be recovered by a deterministic derivation.
- **FR-012**: InitTask audit entries MUST pass through the same deterministic
  control-plane redaction contract as the rest of audit.
- **FR-013**: The machine-id redaction change MUST NOT alter the
  named-environment identity or drift behavior established in the environment
  model.
- **FR-014**: All new evidence and diagnostic surfaces MUST comply with the
  deterministic redaction contract: only Hideout-minted control-plane material is
  stripped, and user/application data is preserved verbatim on local surfaces.
- **FR-015**: This slice MUST NOT introduce a daemon, TUI/WebUI expansion, the
  shared default environment, Claude credential delivery, guest-to-host
  capabilities, or marketplace/bundle trust machinery.

### Key Entities

- **DNS Routing Verification**: The determination, per configured guest
  resolver, of whether its routing is mediated by the privacy path or bypasses
  it. Inputs are the guest's configured resolvers and the effective routes;
  output is mediated / bypassing / unverifiable, with bypassing and unverifiable
  both forcing fail-closed.
- **Isolation Evidence Artifact**: The retained record of an isolation evidence
  run, extending the existing release-evidence bundle. Records version, commit,
  backend, environment identity, per-gate identity and result, audit and
  Boundary Summary references, and an environment snapshot (proxy mode, host
  prerequisites, and uncontrolled external-network context) that scopes what
  repeatability holds fixed.
- **Gate Result**: A single isolation-sensitive gate's outcome within the
  artifact — passed, failed, or not-run — with the reason a gate did not run.
- **Redaction Gap Fix**: The two closed leaks — machine-id non-derivability from
  identity references, and InitTask audit passing through deterministic
  redaction.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of tested privacy-mode runs with a bypassing resolver
  configuration fail closed before the target executes, and 100% of tested
  fully-mediated configurations proceed and resolve through the privacy path.
- **SC-002**: The DNS leak check demonstrably works with bidirectional evidence:
  a target-style resolution plus HTTPS fetch succeed through the mediated DoH
  path (forward proof) and every captured connected-subnet resolver is
  unreachable after the block (reverse proof); a known-bad configuration fails
  and a known-good configuration passes, proving the check is not theater.
- **SC-003**: 0% of tested privacy-mode failures fall back to direct networking,
  and 0% of tested Lima isolation-claim failures fall back to the native harness.
- **SC-004**: A single evidence artifact accounts for 100% of the isolation
  evidence gates run on the machine, extending the existing release-evidence
  format, with each gate recorded as passed, failed, or not-run.
- **SC-005**: Re-running the isolation evidence command with the same commit,
  backend, proxy mode (and operator proxy), and host prerequisites held fixed
  produces an equivalent artifact (same gate set and results). External network
  state and the real DNS upstream are excluded from the equivalence judgment and
  recorded as an environment snapshot, so repeatability holds under controlled
  conditions rather than across arbitrary network changes.
- **SC-006**: 0% of sampled identity references allow deterministic recovery of
  the raw generated machine-id, and 100% of InitTask audit entries pass through
  the deterministic redaction contract.
- **SC-007**: The named-environment identity and drift behavior are unchanged by
  this slice, verified by the existing environment tests remaining green.
- **SC-008**: `go test ./...` and Gate 0 remain green.

## Assumptions

- The primary and only real backend for isolation claims is Lima on macOS; the
  DNS routing verification closes the leak for Lima and is expressed as a backend
  capability that other backends must satisfy before serving privacy mode.
- Privacy-mode DNS mediation is enforced structurally by blocking the
  connected-subnet bypass routes, not by editing the guest's resolver
  configuration; this makes bypass impossible by construction rather than
  relying on a mutable resolver configuration staying unchanged.
- Blocking the bypass closes the leak but does not provide working DNS. Privacy
  mode therefore requires a resolver whose DNS is verified to traverse the
  privacy path; a connected-subnet-only environment (the default Lima resolver
  only) is refused and the run fails closed with a clear diagnostic. To make DNS
  usable over the privacy path, this slice builds a guest-local DoH stub that
  forwards each query as DoH/HTTPS to the declared mediated resolver (reached by
  IP) through the TUN and the SOCKS CONNECT proxy; the guest resolver is pointed
  at the stub. (Superseded note: an earlier draft deferred the guest-local DoH
  stub as an out-of-scope follow-on; it is now in scope and implemented, and
  validated on real Lima by Gate 3.) 004 closes the leak and proves it end to
  end; the operator declares the mediated DoH resolver (a DoH server IP), and a
  connected-subnet-only environment with no mediated resolver is refused.
- The evidence artifact extends the existing release-evidence bundle mechanism
  (the `.hideout-release-evidence` manifest family) rather than a new format, to
  avoid a parallel evidence subsystem.
- Gate 4 requires host prerequisites (a real browser and the escape scenario) that
  may be absent on a given machine; the slice records Gate 4 as not-run in that
  case rather than requiring this slice to itself produce a passing Gate 4.
- The machine-id and InitTask redaction fixes are scoped to non-derivability and
  redaction pass-through; they must be checked against the current
  named-environment identity model so they do not perturb environment fingerprints
  or drift.
- The two real-backend evidence runs still execute on the operator's macOS/Lima
  machine; this slice makes their output repeatable and retained, and does not
  claim to run them in environments without a real VM.
