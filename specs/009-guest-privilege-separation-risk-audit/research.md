# Research: Guest Privilege Separation And Risk Audit

<!-- markdownlint-disable MD013 -->

## Decision 1: Enforced Lima Uses Dual Guest Identities

**Decision**: Enforced 009 status for Lima requires a non-sudo target user plus
a Hideout-owned root/control setup identity. Target commands keep using the
profile user, while setup commands use the control path.

**Rationale**: Current Lima config creates a profile user with UID 1000
(`internal/backend/lima/lima.go:457`), but current setup still relies on that
same user's `sudo -n` in network and HostFS setup
(`internal/network/network.go:361`, `internal/backend/lima/lima.go:644`).
If target and setup share that sudo-capable user, the feature can only report
`degraded`.

**Alternatives considered**:

- Keep shared sudo and add audit only: honest but does not improve the
  boundary.
- Root-owned in-guest helper: viable later, but adds a new product helper and
  larger authentication surface.
- Kernel/syscall interception: out of scope and not necessary for the v1 target.

## Decision 2: Root/Control SSH Is Preferred Over A New Guest Helper

**Decision**: Use Lima system provisioning to install a host-held root/control
SSH path for Hideout setup, and reserve a root-owned helper as a fallback only
if SSH control proves insufficient.

**Rationale**: The roadmap feasibility spike already proved root/control SSH on
local Lima. Existing `limactl shell` uses the configured user, so a separate
control path is needed for root setup without giving target sudo.

**Alternatives considered**:

- `limactl shell` user switching: not available in the current Lima CLI.
- Root helper socket: stronger encapsulation later, but larger implementation.
- Continue `sudo -n`: cannot support `enforced`.

## Decision 3: Existing Environments Are Not Upgraded In Place

**Decision**: Environments created before 009 report `degraded` or `unknown`
until recreated with 009-capable identity setup.

**Rationale**: In-place mutation of guest users, sudoers, and control keys is
hard to prove safely and can break operator state. Recreate already exists as
the product recovery path for identity/image drift.

**Alternatives considered**:

- In-place hardening: high blast radius and difficult evidence.
- Silent enforced claim after partial checks: violates FR-005.

## Decision 4: Degraded Is Warning/Audit By Default

**Decision**: Passwordless sudo on the target user produces `degraded` with a
warning, audit evidence, and recreate/base-image hint. It does not block runs
by default.

**Rationale**: The operator controls their workspace and base image. The user
explicitly selected warning over default refusal for sudo-capable images. A
future enforced-only profile/test mode can fail closed when needed.

**Alternatives considered**:

- Always fail closed on passwordless sudo: too strict for internal alpha and
  operator-owned images.
- Ignore sudo risk: repeats the 008 root-overclaim problem.

## Decision 5: Requested Privileged Setup Fails Closed

**Decision**: If tun2socks, DNS mediation, HostFS mount, or cleanup is requested
and cannot run through an allowed Hideout setup path, that requested setup fails
closed.

**Rationale**: Degraded status can describe image risk, but it must not turn a
broken network or HostFS setup into a pretend success. Current network setup
already fails closed on route and DNS proof; 009 keeps that standard.

**Alternatives considered**:

- Mark degraded and continue with requested setup absent: violates user intent.
- Fall back to target sudo: prevents enforced status and hides the real risk.

## Decision 6: Status Model Is Backend-Neutral, Proof Is Backend-Specific

**Decision**: Put `enforced`/`degraded`/`unknown` classification in a shared
package and let Lima provide the v1 enforced proof. Native reports `unknown` or
weak status.

**Rationale**: Manager, audit, Boundary Summary, and UI need a stable data
shape independent of backend mechanics. Only Lima currently has the VM boundary
and setup mechanics needed for product proof.

**Alternatives considered**:

- Lima-only status structs: faster but duplicates evidence plumbing later.
- Treat native as degraded: misleading because native cannot prove guest user
  state in the same model.

## Decision 7: Evidence Separates Target Intent From Hideout Setup

**Decision**: Add distinct evidence categories for privilege status, Hideout
privileged setup, and target root attempts.

**Rationale**: 008 root-sensitive adapter evidence is target intent. 009 setup
events are Hideout control-plane actions. Merging them would obscure whether a
privileged action came from the target or from Go-owned setup.

**Alternatives considered**:

- Reuse only `broker.request`: misses setup evidence.
- Reuse only network/hostfs events: misses target root intent and status.

## Decision 8: Setup Credentials Are Control-Plane Secrets

**Decision**: Root/control private keys, setup tokens, and setup scripts are
Hideout control-plane material. They must not appear in target env,
target-writable paths, audit, UI, exports, or HostFS grants.

**Rationale**: The constitution requires deterministic stripping of
Hideout-minted control-plane credentials. Setup identity material is equivalent
authority and must follow the same evidence boundary.

**Alternatives considered**:

- Store setup keys under session shims/network: target-writable in current
  layouts and not acceptable for enforced status.
- Redact after leaking into target env: too late; target already has authority.

## Decision 9: Gate 3 Remains Required For DNS Setup Changes

**Decision**: Any implementation that changes tun2socks or DNS mediation setup
must rerun Gate 3 real Lima proof.

**Rationale**: 004 closed DNS leak with real forward/reverse proof. Replacing
shared sudo setup changes that path and must not regress DNS mediation.

**Alternatives considered**:

- Unit tests only: cannot prove guest routing/DNS behavior.
- Gate 2 only: insufficient for hidden proxy and DNS closure.
