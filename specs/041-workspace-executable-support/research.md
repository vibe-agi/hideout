# Research: Workspace Executable Support

<!-- markdownlint-disable MD013 -->

## Decision 1: Treat `FMODE_EXEC` As A Local FUSE Hint

**Decision**: Accept Linux FUSE `FMODE_EXEC` (`0x20`) in the Workspace Portal
client's local open-flag allowlist, omit it from the Portal wire request, and
retain the existing access-mode encoding.

**Rationale**: A real macOS arm64 Lima trace failed before file content was read:
the Linux kernel issued `OPEN {EXEC,0x20000}`, and the client returned
`EOPNOTSUPP` while encoding flags. `FMODE_EXEC` says that an open was initiated
for execution; it is not a host filesystem access right. Upstream go-fuse's
loopback implementation likewise removes `FMODE_EXEC` before opening the
underlying file. The existing Portal request already carries the authoritative
read/write/create/truncate/no-follow semantics.

**Alternatives rejected**:

- Add `FMODE_EXEC` to the Portal protocol: exposes a kernel-private hint as new
  host authority without changing the required host operation.
- Retry every `EOPNOTSUPP` as read-only: could turn genuinely unknown flags into
  silently accepted behavior.
- Copy the executable into guest-local storage: creates a divergent workspace
  copy and evades the product boundary being tested.

## Decision 2: Keep Cached Portal I/O Unchanged

**Decision**: Preserve the existing cached FUSE mode and `FOPEN_KEEP_CACHE`.

**Rationale**: The observed failure occurred in flag validation before read or
mapping behavior. Accepting only the execution hint made both an interpreted
script and a Linux arm64 binary execute successfully through the real Portal.
Changing cache or direct-I/O policy would enlarge the performance and
correctness surface without addressing the demonstrated cause.

**Alternatives rejected**:

- Force direct I/O: unnecessary for the failure and can alter mmap behavior.
- Disable cache retention: would regress established shared-workspace latency
  and invalidation behavior without evidence of benefit.

## Decision 3: Promote Only The Existing Shared Workspace Portal

**Decision**: Feature 041 promotes workspace-local execution only for compatible
macOS arm64 Lima automatic/shared sessions using the daemon-owned Workspace
Portal. Static/dedicated virtiofs remains an explicit non-claim.

**Rationale**: Shared mode already has typed attachment identity, exact-root
validation, provider ownership, session-bound credentials, lifecycle cleanup,
and clean 035 evidence. Generalizing Portal attachment into dedicated/static
environment identity would be a separate lifecycle and provider feature. The
small transport correction can deliver value without changing environment
selection, mount topology, or trust-domain semantics.

**Alternatives rejected**:

- Claim all Lima workspaces: contradicted by the retained static virtiofs
  `EOPNOTSUPP` observation.
- Transparently switch dedicated environments to Portal: changes their mount
  topology and lifecycle contract beyond the diagnosed defect.
- Fall back to host execution: violates the isolation boundary.

## Decision 4: Exercise The Product Path, Not Only The Research Helper

**Decision**: Keep the focused Portal correctness probe for transport evidence,
and add a feature-specific packaged shared-mode Gate 2 that directly executes
workspace content. Keep the legacy static-virtiofs Gate 2 helper copies as
controls outside the promoted claim.

**Rationale**: The focused probe gives fast flag-level diagnosis and retained
logs. The feature Gate 2 proves the selected workspace, daemon, attachment,
Portal helper, target, and candidate package all participate in the same
execution. The legacy aggregate Gate 2 still protects HostFS and other static
topology behavior, but its guest-local helper copy cannot satisfy 041. Both real
lanes are required; neither may be replaced by a native test.

## Decision 5: Judge The Evidence Semantically

**Decision**: Register 041 Gate 0, real-execution, not-run, and docs proof IDs.
The real artifact validator requires a clean exact commit, macOS arm64 Lima,
the `workspace-portal` mechanism, direct script and binary execution, host
checkout effects, boundary negatives, and no fallback/copy.

**Rationale**: A shell exit status or an artifact that merely says `passed` can
be false green. A Go-owned validator plus a retained negative fixture makes the
claim independently reviewable.

## Decision 6: Repair The Research Probe's Admission Wiring

**Decision**: Construct the current Portal admission controller and supply the
required environment/provider identity in `hideout-workspace-probe`.

**Rationale**: Lifecycle admission became mandatory after the original 035
research probe was written. Without this repair the probe fails before reaching
the filesystem operation under study, masking the execution defect.

## Known Boundaries

- The file must already have execute permission and be compatible with the
  Linux guest's architecture, format, and installed interpreter.
- Workspace content is intentionally executable target input; 041 adds no
  trust claim about that content.
- Static/dedicated virtiofs execution remains tracked as debt and receives no
  automatic copy, host fallback, or promoted support from this feature.
- Windows, guest-root containment, DLP, and outside-workspace executable
  discovery remain out of scope.
