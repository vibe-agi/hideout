# Contract: Transport Research Decision

## Purpose

The research artifact is the sole Phase R to Phase I gate. It is evidence, not
a feature flag. Product code cannot select a transport from configuration or
fall back to the losing candidate.

## Candidate Set

Closed candidate IDs:

- `vz-live-multiple-share`
- `workspace-portal`

The path-identity mechanism is evaluated with each transport and named in each
candidate result.

## Strict Result Rules

`accepted` requires:

- exactly one selected candidate;
- every mandatory correctness/isolation/lifecycle/TCC/path-identity check
  passed;
- every fixed performance threshold passed after at most one bounded
  optimization pass;
- declared admission limits and package/support posture;
- raw sample, fixture, topology and operation-matrix artifacts with verified
  digests; and
- provenance bound to exact commit and declared dirty state.

`rejected` requires both candidates' failure reasons and leaves Phase I blocked.
Unknown/missing fields, unknown candidate IDs, stale commit/runtime/tool facts,
artifact escape, missing artifact, or digest mismatch invalidate the artifact.

## VZ Mandatory Proof

- Boot with one empty multiple-share device and no project in Lima YAML.
- Mutate the retained live device through authenticated host-only control bound
  to exact instance, driver process and boot incarnation.
- Bind each file URL to the captured root identity across rename/replacement
  races.
- Atomically add/remove share and watcher state without restart.
- Mount only the selected opaque child inside a private staging namespace and
  remove staging before target readiness.
- Keep sibling mount/open handle stable through another key's detach.
- Observe enough running-device/share state to prove cleanup or fail closed.
- Provide a released or productized package/signing/support path.

## Portal Mandatory Proof

- Binary or equivalently efficient multiplexed frames with bounded payloads.
- Explicit open file/directory handles rooted under one opened canonical root.
- Request IDs, cancellation, backpressure, fairness and cleanup-reserved budget.
- Independent lock ownership for same-root sessions even in one host process.
- Session/incarnation/audience-bound credentials with renewal, expiry and
  revocation behavior.
- Deterministic completion/cancellation for disconnect and stale handles.
- No HostFS action, overlay, JSON/base64 hot path, or path fallback.
- Package-owned host/guest helpers and proven guest FUSE prerequisites.

## Benchmark Method

- Static virtiofs and candidate use the same host, runtime, fixture, Git state,
  cache state and command set.
- Product validation runs the literal Git workload with the selected Portal
  session's Core-owned process policy `core.preloadIndex=false`; absence of that
  policy is a failure. It never writes repository, global, or host Git config.
- Warm/cold results are separate.
- Filesystem workload, attach-to-ready, and end-to-end first-byte are separate.
- Each warm distribution has one unrecorded warm-up and at least 30 recorded
  samples, with median and p95 plus raw samples.
- One noisy session must not starve sibling metadata or teardown at limits.

Thresholds are those in `spec.md` SC-005 and may not be relaxed after results
without an explicit spec/product decision.

## Phase I Consumption

The build/test path validates the artifact before compiling/enabling the shared
mode. Production uses the selected constant from generated/validated build
metadata, not an operator option. Losing probe code and dependencies are removed.

Research evidence may be dirty and still guide the decision. Promotion evidence
must be clean, release-shaped, and independently verify the accepted behavior.
