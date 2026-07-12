# Quickstart: Validate HostFS Discoverable Namespace

<!-- markdownlint-disable MD013 -->

This guide is a validation path, not an implementation guide. Run commands
from the repository root. Real guest-visible claims require macOS arm64, Lima,
virtualization support, and the normal Gate 2 prerequisites.

## 1. Static And Selector Contract

**Covers**: FR-001 through FR-007, SC-001, SC-009.

```sh
go test ./internal/hostfs ./internal/profile ./internal/manager \
  -run 'Discover|Visibility|Selector|LegacyList|Reserved|Precedence'
```

Expected:

- `see`, `see-dir`, and `see-tree` compile to discover rules with exact,
  one-level, and recursive scopes;
- all discover globs and new `list:` input fail;
- reserved roots and discover denies win;
- a profile without `see*` exposes no new real names and keeps legacy denial
  behavior.

## 2. Coarse Metadata And Complete Listing

**Covers**: FR-002 through FR-004, FR-008, FR-009, SC-001 through SC-003.

```sh
go test ./internal/hostfs \
  -run 'Coarse|Complete|Overflow|ChildInspection|Depth|Symlink'
```

Expected locked nodes contain only name, coarse kind, locked state, and generic
capabilities. Fixtures with more than 4096 entries, depth beyond 32, child
inspection failure, or observed inconsistent enumeration return an explicit
incomplete error. No partial list or sentinel filename is accepted.

## 3. Typed Broker Errno

**Covers**: FR-010 through FR-016, SC-004, SC-011.

```sh
go test ./internal/broker -run 'HostFSError|HostFS.*Typed|Prerequisite'
GOOS=linux GOARCH="$(go env GOARCH)" go test -c \
  -o "${TMPDIR:-/tmp}/hideout-hostfsd-029.test" ./cmd/hideout-hostfsd
rm -f "${TMPDIR:-/tmp}/hideout-hostfsd-029.test"
```

Expected:

- all ten code/errno pairs match
  [broker-hostfs-error.md](contracts/broker-hostfs-error.md);
- stderr changes do not alter errno;
- unknown/malformed records map to EIO;
- request-limited can be retryable without a decision reference;
- protected-directory failure is EIO and cannot create a decision.

## 4. Decision Lifecycle And Abuse Bounds

**Covers**: FR-017, FR-018, FR-021 through FR-025, SC-005, SC-007, SC-008.

```sh
go test ./internal/decision ./internal/manager ./internal/hostfs/readgrant \
  -run 'HostFSRead|Dedup|Rate|Pending|Timeout|Reopen|OwnerLock'
```

Expected:

- repeated equivalent reads produce one decision and unchanged deadline;
- one session never exceeds eight pending or eight creations per rolling
  minute under concurrent processes;
- explicit deny, timeout, exact-directory list denial, and capacity refusal
  produce no grant;
- only an authenticated reopen for a provably live session creates a new
  revision/deadline.

## 5. Cross-Process Session Grant

**Covers**: FR-019, FR-020, FR-024 through FR-026, SC-006, SC-007.

```sh
go test ./internal/hostfs/readgrant ./internal/manager ./internal/broker \
  -run 'HostFSReadApproval|HostFSReadGrant|OpenStore|Owner|Atomic|Retarget'
```

Expected a separate Manager instance can approve a decision while a broker is
running, the broker observes the atomic exact-file grant on its next denial
check, malformed/expired/mismatched state denies, and release of the owner lock
revokes liveness. No profile rule changes.

## 6. Legacy Rule Migration

**Covers**: FR-005, FR-031, SC-009, SC-013.

```sh
go test ./internal/profile ./internal/manager \
  -run 'LegacyList|ProfileHostFSMigrateList'
```

Expected the plan shows both disclosure changes and requires confirmation. A
missing mapping, wrong rule ID, drifted profile, or failed final validation
writes nothing. Successful apply writes one fully valid profile atomically and
audits the migration.

## 7. Onboarding Presets

**Covers**: FR-028, FR-029, FR-030, SC-010.

```sh
go test ./internal/profiletemplate ./internal/inittask ./internal/hostpathrisk \
  -run 'Visibility|Landmarks|HomeTree|Disclosure|BroadDiscovery|HomeBoundary|Catalog'
```

Expected:

- privacy and omitted noninteractive selection create no discover rules;
- cancelled interactive landmarks create no rule;
- confirmed landmarks expand only selected roots to `see-dir`;
- home-tree without explicit name-disclosure acknowledgement fails;
- home-tree excludes categorized credential/browser/system-key roots without
  turning the whole-home workspace entry into a visibility deny.

## 8. Explicit Host-Prerequisite Probe

**Covers**: FR-016, FR-030, SC-011.

Use a temporary store so unrelated operator profiles cannot affect the result.
Without a probe:

```sh
doctor_store="$(mktemp -d "${TMPDIR:-/tmp}/hideout-029-doctor.XXXXXX")"
HIDEOUT_STORE_ROOT="$doctor_store" \
  go run ./cmd/hideout doctor --feature hostfs --level deep
```

Expected protected-root status is `unknown/unprobed` and no protected root is
opened.

With an operator-selected fixture root:

```sh
probe_root="$(mktemp -d "${TMPDIR:-/tmp}/hideout-029-probe.XXXXXX")"
probe_root="$(cd "$probe_root" && pwd -P)"
HIDEOUT_STORE_ROOT="$doctor_store" \
  go run ./cmd/hideout doctor --feature hostfs --level deep \
  --probe-hostfs-root "$probe_root"
rm -rf "$doctor_store" "$probe_root"
```

Expected a warning is printed before access. A permission-denied fixture is a
host prerequisite finding/EIO, not an approval decision. Manual macOS testing
may trigger an OS prompt and is documented rather than faked in CI.

## 9. Operator Surfaces

**Covers**: FR-021, FR-024, FR-025, FR-027, FR-031, SC-005, SC-012.

```sh
go test ./internal/manager ./internal/daemon ./internal/liveconsole ./internal/app \
  -run 'HostFSReadDecision|DecisionReopen|UntrustedReason|ProfileScope'
```

Expected generic CLI/Manager/WebUI flows list, inspect, claim, approve, and deny
`hostfs.read`. Only eligible terminal rows offer Reopen. TUI renders the exact
reopen command. Profile scope is respected, reason text is plain and labeled
untrusted, and no token/private grant path appears.

## 10. Audit, Boundary, Evidence, And Docs Truth

**Covers**: FR-031 through FR-033, SC-012 through SC-014.

```sh
scripts/test-hostfs-visibility-e2e.sh --local-fast
scripts/test-doc-truth-smoke.sh
```

Expected local evidence contains the unit policy, typed errno, decision
lifecycle, redaction, and claim-boundary proof IDs. Injected broker/claim
tokens, machine ID, `HIDEOUT_SECRET_*`, file content, symlink target, and
private grant paths are absent. Local-fast output does not satisfy either
real-gate proof requirement.

## 11. Real Gate 2 Namespace

**Covers**: FR-002 through FR-018, SC-001 through SC-004, SC-009, SC-011.

```sh
HIDEOUT_GATE_TIMEOUT=45m \
  scripts/test-hostfs-visibility-e2e.sh --real-gate2
```

The real lane invokes `scripts/test-gate2-lima.sh`. It must prove:

- outside paths and categorized sensitive roots under a manually authored broad
  rule return ENOENT for both lookup and direct enumeration;
- `see-dir` is complete and coarse, and discover-denied exact content remains
  directly usable without reappearing in parent enumeration;
- exact-visible directory readdir is EACCES with no decision;
- locked read is prompt EACCES;
- explicit read deny creates no decision;
- overflow/incomplete directory is not partial success;
- explicit-discover unauthorized write is EACCES while a legacy profile keeps
  prior behavior;
- host prerequisite failure is typed EIO.

If prerequisites are unavailable, the script writes only the supporting
`029.hostfs-visibility.real-gate2.not-run` proof and exits according to the
documented prerequisite contract. It must not report the namespace proof as
passed.

## 12. Real Gate 2 Live Approval

**Covers**: FR-017 through FR-027, SC-005 through SC-008.

The same real run must keep one target session alive while a separate
authenticated host process claims and approves its read decision. It then
proves:

- repeated reads coalesce without deadline extension;
- the unchanged guest retries and reads exact expected content;
- no watcher or run restart occurs;
- real stat metadata converges within one second while content does not wait;
- deny and timeout stay denied until explicit reopen;
- ended/unprovable reopen fails;
- symlink retarget after approval fails;
- grant expiry/cleanup removes authority.

The resulting manifest must include passed
`029.hostfs-visibility.real-gate2.live-grant` with the real Gate 2 log as a
digest-validated artifact.

## 13. Existing HostFS Regression

**Covers**: FR-006, FR-007, FR-011, FR-013, FR-019, FR-029, SC-007, SC-009.

The real Gate 2 run must also retain all existing read/dir/tree/glob and 010
write-overlay assertions. In particular, staged guest content remains visible
before operator apply, host lower remains unchanged before apply, and approved
write apply mutates only its planned path.

## 14. Final Battery And Completion Rule

```sh
go build ./...
go vet ./...
test -z "$(gofmt -l internal cmd)"
git diff --check
go test ./...
scripts/test-gate0.sh
npx --yes markdownlint-cli2 README.md README.zh-CN.md 'docs/**/*.md' \
  'specs/029-hostfs-discoverable-namespace/**/*.md'
```

Completion additionally requires a successful real Lima Gate 2 run and a
validated evidence manifest containing both real proof IDs. `docs/STATUS.md`
must not mark 029 Implemented before that evidence exists.
