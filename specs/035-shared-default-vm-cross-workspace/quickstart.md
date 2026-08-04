# Quickstart: Validate Shared Default VM Across Workspaces

This guide is an acceptance runbook. It does not authorize skipping the Phase R
decision or using source-tree helpers as promotion evidence.

## Prerequisites

- macOS arm64 host with the supported Lima version and promoted runtime image;
- clean candidate commit and release-shaped installed Hideout package;
- two disjoint fixtures, one same-root fixture and one nested fixture;
- fixed 10,000-entry Git and 20,000-operation package fixtures;
- product-evidence root with the accepted research decision and artifact
  digests; and
- host permissions for every process named in the accepted topology.

Use temporary profiles and projects that contain no credentials. Record the
exact host, Lima, runtime, shell, Git, language, Claude and Codex versions.

## 1. Research Gate

Validate the strict research artifact and its referenced artifacts. Confirm it
selects exactly one complete transport/path pair, contains both candidate
results, meets every fixed threshold, and is bound to the candidate commit.

Expected:

- accepted artifact passes schema, path-containment and digest checks;
- absent, rejected, stale, dirty-for-promotion, missing-artifact and digest-
  mismatch fixtures fail; and
- the losing candidate is not selectable or retained as product fallback.

**Covers**: FR-006, FR-028, FR-030, FR-031, FR-032, FR-033; SC-005, SC-015,
SC-019, SC-021.

## 2. Stable Slot And Drift

Create two no-flag plans from disjoint projects under one profile, then mutate
one included machine field and one excluded session field.

Expected:

- project and session changes keep one slot;
- one shared record and instance are created under simultaneous first-run race;
- included machine drift returns one typed recreate/dedicated recovery; and
- no second automatic record or VM appears.

**Covers**: FR-001, FR-002, FR-038; SC-001.

## 3. Mode Matrix And Alpha Reset

Exercise promoted Lima shared mode, explicit named mode, native,
session-identity `--ephemeral`, disposable `--rm`, and an old record fixture.

Expected:

- shared, dedicated and workspace-bound records satisfy their field invariants;
- `--ephemeral` reuses the corresponding platform record and `--rm` creates no
  reusable record;
- named environment rejects a different project;
- old record fails with a real remove/recreate command and no dual reader; and
- no unsupported path silently enters shared mode.

**Covers**: FR-003, FR-026, FR-036, FR-040; SC-013, SC-014, SC-020, SC-022.

## 4. Machine Configuration

Inspect generated shared Lima configuration and guest mount metadata before and
after attachment.

Expected:

- no selected project, raw host root, common parent, home, `/Users`, or dummy
  workspace is present in machine YAML;
- machine activation succeeds before project attachment; and
- only accepted opaque candidate identifiers appear where required.

**Covers**: FR-009, FR-029, FR-030; SC-017.

## 5. One Machine, Two Projects

Hold session A open in project A, start session B in project B, and observe
environment ID, Lima instance, guest boot ID, workspace IDs and fixture markers.

Expected:

- both sessions share one environment/instance/boot identity;
- each sees only its own marker at logical `/workspace`;
- workspace IDs and physical project-state identities differ; and
- the operator still uses `/workspace` in both sessions.

**Covers**: FR-004, FR-005, FR-010, FR-041; SC-001, SC-002, SC-023.

## 6. Root Relations And Escape Probes

Repeat with same, nested and disjoint roots. Probe parent/sibling paths,
reserved roots, guessed opaque IDs, mount metadata, protocol endpoints,
`/proc`, symlinks, rename races and root replacement.

Expected:

- same root collaborates with independent bindings/lock owners;
- nested root gets no upward/sibling authority and surfaces an asymmetric
  notice;
- disjoint ordinary targets cannot enumerate or open siblings; and
- root replacement invalidates attachment instead of switching authority.

**Covers**: FR-007, FR-027, FR-037; SC-002, SC-004.

## 7. Filesystem Correctness

From host and guest, exercise create, read/write, truncate, mkdir, readdir,
unlink/rmdir, rename, atomic replace, executable mode, symlink/readlink,
fsync/flush/close, advisory lock, hard-link/xattr/sparse/mmap probes and file
watchers according to the accepted operation matrix.

Expected:

- successful mutations are live host mutations with no later apply;
- unsupported operations return the recorded stable error;
- no silent short write, false durability, lock collapse or escape occurs; and
- atomic-save and notification visibility meet the declared bounds.

**Covers**: FR-008, FR-021, FR-022, FR-037; SC-003, SC-004.

## 8. HostFS Separation

Stage an existing HostFS overlay write while changing the selected workspace
directly.

Expected:

- workspace change reaches the host immediately;
- HostFS lower remains unchanged until its existing decision/apply; and
- no `host.fs.*` action, pending decision or overlay object represents workspace
  I/O.

**Covers**: FR-008; SC-011.

## 9. Same-Root Locks And Sibling Detach

Open a file and contend on a lock from two same-root sessions. Exit session A
while B retains the lock/open handle and continues terminal, network, HostFS
and file I/O.

Expected:

- lock conflict matches independent owners;
- A cleanup does not remove the shared concrete provider while B is bound; and
- B remains usable with no interruption or stale-handle fallback.

**Covers**: FR-011, FR-031; SC-004, SC-006.

## 10. Lifecycle And Final Stop

Observe planned resources before provider effects and active resources only
after supervisor-ready. Close both sessions while a VM-dependent bridge remains,
then release the bridge. Start another project during a separate idle grace.

Expected:

- provider/view/service topology matches the accepted process graph;
- cleanup is dependent-first and does not modify project content;
- the bridge prevents stop;
- final release produces one existing grace and one exact-incarnation stop; and
- new attach cancels grace before provider side effects and reuses the VM.

**Covers**: FR-013, FR-014, FR-015, FR-023, FR-024; SC-007, SC-008.

## 11. Crash And Unproved Cleanup

Terminate provider/daemon at controlled attach, active I/O and cleanup points,
then restart the daemon.

Expected:

- old credentials grant no authority;
- absence may be proved but a live view is never re-adopted;
- unproved state blocks new attach/reuse/automatic stop for that incarnation;
- recovery uses the existing non-destructive stop path; and
- project content is unchanged except for operations already truthfully
  completed before failure.

**Covers**: FR-016, FR-031; SC-009.

## 12. Network And Capacity

Run two sessions through one environment network service and change proxy
upstream and mediated DNS while both remain active. Verify that direct/proxy
posture switching is refused while a sibling target remains active; after it
exits, switch posture in both directions without changing the environment,
instance, or boot. Then saturate each declared workspace limit.

Expected:

- service generations switch atomically while secrets stay session scoped;
- VM-global posture is never changed underneath an active sibling target;
- a failed guest transition proves rollback or fails closed without claiming
  the previous or new generation ready;
- one session cannot exceed bounded memory/handles/bytes or starve sibling I/O;
- teardown retains reserved progress; and
- overload creates no fake approval decision or hidden fallback.

**Covers**: FR-020, FR-032; SC-021.

## 13. Host Projection Mapping

From sessions A and B invoke representative workspace-relative host projection
for `open .`, `code .`, and a file/line resource.

Expected:

- both guest requests may name `/workspace` yet resolve to their own host root;
- Core uses the immutable attachment and structured relative path;
- no environment-level fallback exists; and
- the guest receives no host root.

**Covers**: FR-017, FR-039; SC-010.

## 14. Alias And Project-State Identity

Probe `PWD`, `getcwd`, `realpath`, `cd .`, `cd /workspace`, subprocesses, Git
safe-directory, and project-scoped state for Bash, Git, Node, Python, Go,
Claude and Codex fixtures. Test preserve mode and linked external Git metadata.

The non-performance packaged-product stage runs this matrix independently of
the performance samples:

```sh
scripts/test-shared-workspace-lima-e2e.sh \
  --require-real \
  --non-performance \
  --out .hideout-release-evidence/035-shared-workspace-path
```

If that checkpoint passes and performance must be measured later, reuse the
same exact package and retained digests instead of rerunning correctness:

```sh
scripts/test-shared-workspace-lima-e2e.sh \
  --require-real \
  --performance-only \
  --samples 30 \
  --out .hideout-release-evidence/035-shared-workspace-path
```

Expected:

- logical and exact physical aliases have the same device/inode and preserve
  bidirectional create/read/write/rename/chmod/fsync/delete behavior;
- Bash, Git, Node and Python observe the opaque physical cwd, while Go's
  same-inode `$PWD` behavior is classified explicitly when `go env GOMOD`
  remains under logical `/workspace`; no distinct Go project-state claim is
  fabricated;
- representative Claude and Codex state fixtures keep distinct
  trust/history/cache/socket keys across roots and stable keys for the same
  root;
- logical `/workspace` remains usable;
- only verified logical/physical Git paths are trusted;
- guessed sibling roots and parent traversal are denied; resolved workspace
  file paths are projected to `/workspace`, while relative paths from
  path-oriented file hooks remain explicitly `aliased`; process cwd is marked
  `cwd-unavailable` when the kernel does not supply it; and production-shaped
  physical or sibling argv paths that exceed
  the kernel capture width either become the fixed unbound placeholder with
  `argv-truncated` or are omitted with `argv-unavailable`, without exposing
  `/hideout/workspaces`;
- shared preserve mode and linked external Git metadata fail before target
  start, and ambient Git safe-directory entries cannot add an unbound or
  wildcard path; and
- incompatible path modes fail with executable dedicated-environment guidance.

**Covers**: FR-035, FR-041; SC-019, SC-023.

## 15. Product Surfaces And Redaction

With two active projects, inspect Manager API, CLI, TUI, WebUI, events, doctor,
audit and export using sentinel host username/path, identity key and tokens.

Expected:

- one machine row and two session workspace-view rows appear;
- profile scoping, relation notices and blockers are correct;
- display labels are never accepted as authority;
- guest/public output contains no injected host/control sentinel;
- operator-local paths follow the documented local/export boundary; and
- UI runtime tests use real browser and PTY paths rather than source grep.

**Covers**: FR-018, FR-019, FR-025, FR-034; SC-012, SC-016, SC-018.

## 16. Performance Evidence

Run the Portal fixture and an identical static-virtiofs fixture from one target
process in one shared VM. Warm each side once, alternate candidate/control order
for every Git and package sample, retain the paired records, and keep cold and
first-target-byte results separate. The accepted research baseline remains the
first-byte reference and transport-selection provenance only.

The shared Portal target must expose the Core-owned, process-scoped Git policy
`core.preloadIndex=false`. The probe rejects a missing policy and verifies that
no repository/global configuration or wildcard `safe.directory` grant is used.

Expected:

- paired raw samples, their extracted distributions, and fixture digest validate;
- the Portal-only Git scheduling policy is present without persistent mutation;
- filesystem, attach and end-to-end timing are separate;
- every SC-005 threshold passes without post-result relaxation; and
- no idle provider process or VM pin remains with no attachment.

**Covers**: FR-006, FR-022, FR-028, FR-032; SC-005, SC-008.

## 17. Final Battery And Promotion

Run formatting, diff check, build, vet, full tests, markdown/schema checks,
Gate 0, real Lima gate, installed-package smoke and product-evidence evaluation
on the exact candidate.

Expected:

- all commands exit zero;
- evidence records exact commit and honest dirty state;
- stable 035 proof IDs resolve to validated artifacts; and
- docs/status claims change only after clean real-backend proof.

**Covers**: FR-040; SC-015, SC-022.
