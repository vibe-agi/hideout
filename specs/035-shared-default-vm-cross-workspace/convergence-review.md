# Feature 035 Workspace Path Convergence Review

Date: 2026-08-03

This review covers the post-promotion question of whether `/workspace` and the
opaque per-attachment physical cwd are safe and sufficiently tested. It does
not replace the retained historical 035 proof. The fresh behavior-only
real-Lima stage now passes, but its dirty source binding and intentionally
missing performance proof do not constitute a new release candidate.

## Fresh-eyes findings

1. The research probe accepted short synthetic workspace IDs even though
   production emits `wrk_` plus 64 lowercase hexadecimal characters.
2. Backend Portal validation checked only the `/hideout/workspaces/` prefix, so
   lower layers could accept a non-production physical identity if an upper
   layer regressed.
3. Broker cwd validation cleaned `..` before attachment authority validation,
   making a traversal-shaped request indistinguishable from a clean request.
4. File/process observation retained kernel-visible physical aliases, and an
   exact physical path used as a standalone argv element could reach operator
   activity output.
5. The old pseudo-agent probe returned only `getcwd`; it did not create
   representative history, cache or Unix-socket state. It also incorrectly
   assumed Go reports a physical module path. In the actual same-inode logical
   `$PWD` session, Go reports logical `/workspace/go.mod`.
6. Workspace correctness assertions were coupled to the performance driver,
   so a performance or harness failure could discard otherwise useful path
   evidence.
7. Portal `Unlink`/`Rmdir` returned after the host mutation but left its
   userspace path cache dependent on a later watcher event. An immediate lookup
   through the other alias could therefore observe a stale directory.

## Implemented controls

- One dependency-light structured resolver accepts only logical `/workspace`
  or the exact physical root derived from the immutable production workspace
  ID. Host paths, siblings, malformed identities, NULs and parent elements fail
  closed.
- Backend, Broker, Host App, Manager and observer paths use that attachment
  identity. Operator-facing exact file/process paths are projected to
  `/workspace`; guessed standalone workspace argv paths use a fixed
  non-authoritative placeholder.
- The real installed-product non-performance stage checks same-object alias
  identity, the bidirectional I/O matrix, nested navigation, project-state
  separation/stability, sibling denial, bounded Git safety inputs, pre-target
  preserve/external-metadata rejection, and activity-path/argv normalization.
- The retained path artifact names current partial-coverage boundaries instead
  of fabricating fields: process cwd may be `cwd-unavailable`, a relative
  workspace path from a path-oriented file hook is `aliased`, and a production
  physical argv path exceeds the kernel capture width and is fail-closed either
  as the unbound placeholder with `argv-truncated` or as an omitted argument
  with `argv-unavailable`.
- Go's logical `$PWD` behavior is named explicitly and does not satisfy a
  stateful-project-identity assertion. Claude/Codex representative fixtures
  create history, cache and Unix-socket state keyed by the physical cwd.
- The path judge retains a deliberately divergent-inode negative fixture.
- Successful Portal removal now clears the userspace node/info subtree and
  bumps the parent generation before returning. The FUSE response owns kernel
  dentry invalidation; the later watcher notification remains idempotent.

## Mutation and negative evidence

- Before the production-ID fix, the updated path-plan test failed with
  `research workspace id is invalid`; it passed only after the loose guard was
  replaced by the production validator.
- Weakening the structured resolver to accept the physical base/sibling made
  the sibling negative test fail; restoring exact attachment binding returned
  the suite to green.
- Mutating Broker cwd handling to call `filepath.Clean` before authority
  validation made
  `TestNormalizeBrokerRequestCWDCanonicalizesAliasesAndRejectsTraversal` fail
  because `/workspace/src/../src` was accepted. Restoring raw structured
  validation returned the test to green.
- Gate 0 proves `gate2_035_path_correctness_judge` rejects an incomplete
  all-true fixture. Its separate negative judge accepts only the exact retained
  divergent-inode case and rejects an ambiguously failing negative fixture.
- attempt9 retained `deleteAcrossAliases=false` for workspace B. A first repair
  that synchronously sent `NotifyEntry` from inside the FUSE callback deadlocked
  the real relation probe and was rejected. Splitting synchronous userspace
  invalidation from asynchronous kernel notification removed that deadlock;
  the final gate then passed 100 immediate cross-alias delete iterations.

## Retained convergence evidence

- The expanded Gate 0 passes resolver, Broker, product-evidence, race, Linux
  test-binary cross-compilation, readiness-owner early-exit, and shell-judge
  mutation checks. Full `go test ./...`, native vet, and Linux arm64/amd64
  cross-vet also pass on the final worktree.
- The installed-product macOS arm64 Lima run retained at
  `.hideout-release-evidence/035-path-convergence-20260803T0950Z-attempt13/`
  passed relation, path, lifecycle, package/helper identity, schema, redaction,
  and exact divergent-inode negative checks. All 27 path checks are true,
  including 100 repeated cross-alias deletes, and the three fixed
  partial-coverage limitations are present.
- Candidate package SHA-256 is
  `af761bf3e9c66c4c829bd5df04c263221a424e36a489cb3a2deb95504906f354`;
  behavior SHA-256 is
  `0444dabaf0958cbae3e9a6b9177e9229c1e26832614068c7635c9596c8946fdc`;
  outer evidence-manifest SHA-256 is
  `f27b23c8e1b6a8aa16fb7116bdd01f31601525ec010ff3fcdcfcb68540c710da`.
- The release evaluator correctly rejects promotion from this checkpoint:
  `dirty:true` makes the behavior proof stale for a trusted clean candidate,
  and the registered performance proof is absent. Performance remains a
  separate promotion input and was intentionally not run in this slice.
