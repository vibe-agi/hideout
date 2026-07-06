<!-- markdownlint-disable MD013 -->

# Research: Hideout Lima Real Run

## Decision: Use a dedicated reference-run smoke, not a new Core success-check API

**Rationale**: The spec needs an operator-declared success check, but exposing a
generic host-side success-check command would create a new ambient host
execution channel. The safer product slice is a dogfood validation fixture that
runs the target and its success check in the guest/workspace context, then lets
the host harness verify workspace artifacts and evidence.

**Alternatives considered**:

- Add `hideout run --success-check <host command>`: rejected because it would
  create a generic host command execution surface.
- Add `hideout run --success-check <guest command>` as a product API: deferred
  because it is broader than this first dogfood slice and needs separate command
  proxy / policy design.
- Reuse existing `scripts/test-dogfood-cli-smoke.sh` unchanged: rejected because
  it proves many mechanisms but does not define the spec's file-diff +
  success-check + declared-endpoint reference workload.

## Decision: Extend or wrap `hideout-test-cli` for the reference workload

**Rationale**: `cmd/hideout-test-cli` already exists as the generic fake CLI for
dogfood mechanisms and is not product-specific. Adding a workload mode keeps
the fixture deterministic and avoids putting real third-party accounts into
default gates. The mode should update an expected workspace file, call a
declared endpoint, and print stable result markers.

**Alternatives considered**:

- Use a shell script entirely inside the workspace: rejected for the primary
  fixture because it would be less portable and harder to keep consistent with
  existing Go-based test CLI behavior.
- Use a real agent CLI: rejected for default validation because accounts,
  package names, and API behavior are operator-specific and belong in optional
  operator smoke.
- Make `hideout-gate-lab-target` the target CLI: rejected because it is better
  suited as the declared network endpoint provider.

## Decision: Test direct network mode by default; require Gate 3 when privacy mode changes

**Rationale**: The reference run must prove egress through the selected network
policy. Direct mode can be validated by running a host-local endpoint reachable
from the guest through the existing Lima host gateway. Privacy mode uses
existing tun2socks/Gate 3 infrastructure and should be exercised whenever this
slice is used to validate proxy routing or when network setup changes.

**Alternatives considered**:

- Require privacy mode for every reference run: rejected because it would make
  the first slice depend on operator proxy setup and reintroduce release-bundle
  scope.
- Treat network as audit-only: rejected because agentic CLIs need egress for
  useful work and the spec makes network reachability part of the workload.

## Decision: Define a fixed boundary-triggering action set for SC-006

**Rationale**: "100% of decisions appear in evidence" is only measurable if the
test fixture controls the decisions. The known set should intentionally trigger
host.open denial, HostFS allow/deny or reserved-root denial, session lifecycle,
network setup, and one endpoint exposure/preview.open event.

**Alternatives considered**:

- Inspect whatever audit events happen naturally: rejected because it is not
  deterministic and can miss regressions.
- Require all authority classes in the primary reference run command: rejected
  because it would make the first slice too broad. The boundary set can be a
  second phase of the same smoke script.

## Decision: Use existing evidence surfaces for this slice

**Rationale**: `hideout run --verbose` already surfaces session/environment
hints and Boundary Summary, while `hideout audit show` exposes redacted audit
events. This slice should not require the richer TUI/WebUI observer work.

**Alternatives considered**:

- Require TUI/WebUI for dogfood evidence: rejected as scope creep; those are
  separate observer-surface features.
- Store a release evidence bundle: rejected for this slice; release bundles are
  a separate feature and already have dedicated scripts.

## Decision: Native backend remains a negative/wiring-only path

**Rationale**: Constitution and docs consistently mark native as weak
development harness. The reference smoke should either reject native as
dogfood evidence or label it wiring-only, never count it as Lima evidence.

**Alternatives considered**:

- Allow native to satisfy the reference workload: rejected because it would
  weaken the dogfood isolation claim.
