# Contract: Release Workflow

## Repository Prerequisites

Before public-alpha promotion:

- repository visibility is public;
- immutable releases are enabled and independently observed through the API;
- private vulnerability reporting is enabled and independently observed;
- the `public-alpha` environment requires one authenticated reviewer;
- public-alpha signing/notarization credentials exist only in a protected
  signing environment;
- actions are pinned to full commit SHAs; and
- Apache-2.0, notices, security policy, feedback templates, release notes, and
  release inventory schemas pass Gate 0.

## Phase 1: Candidate Build

Trigger: exact product prerelease tag or an equivalent manual dispatch naming
that exact tag.

The candidate job uses a staged package-tree/finalize contract:

1. verifies version/tag/full commit/clean source and protected-branch reachability;
2. creates the package tree once without an archive or final package manifest;
3. signs every detected host Mach-O with Developer ID, secure timestamp, and
   hardened runtime;
4. finalizes and verifies the canonical package manifest over the signed tree,
   creates the final `tar.gz` exactly once, and never mutates signed binaries;
5. creates and submits one private ZIP envelope of that same finalized tree,
   waits for accepted notarization, and writes a sanitized observation;
6. independently observes signing identity and each command-line binary's
   online notarization ticket against the finalized tree using the platform's
   non-app code check;
7. computes and records the final archive SHA-256 without rebuilding it;
8. runs local/package/Gate 0 checks against those bytes;
9. uploads the candidate package plus bounded build observations as a workflow
   artifact; and
10. retains them in a private draft release with an exact asset allowlist.

Real Lima validation MUST run below a short, candidate-owned temporary root so
macOS Unix socket limits cannot turn a valid package into a false failure. The
same root remains inside the candidate cleanup domain. Tests that accept
`HIDEOUT_RELEASE_BINARY` MUST exercise that packaged binary rather than rebuild
an equivalent command from the source checkout.

Before the first publication, a failed or superseded candidate MAY replace the
same tag only through an explicit `replace_private_draft` workflow input. The
workflow MUST re-observe that the existing release is still a draft prerelease
with no publication timestamp, MUST reject any existing Git tag or published
release, and MUST leave the old draft untouched until the replacement
candidate has passed signing, notarization, and Gate 0. This retry path does not
permit rebuilding or mutating a published identity.

Developer-preview mode follows the same shape without signing credentials, but
uses a distinct version/tag/channel and cannot satisfy public-alpha signing or
promotion requirements.

## Phase 2: Independent Real Proof

An operator on a real macOS arm64 host:

1. downloads the draft package through authenticated release APIs;
2. verifies its predeclared SHA-256 before extraction;
3. runs clean-install E2E with fresh HOME/store/Lima home and no Go/source
   fallback;
4. runs the real Gate 2 and Gate 3 lanes against the exact package/runtime;
5. runs required 029-032 real proofs and cleanup checks;
6. builds the bounded evidence bundle and candidate release manifest;
7. evaluates canonical release readiness;
8. uploads the exact evidence, release manifest, and checksums to the existing
   draft; and
9. creates one typed promotion request containing only public identity and
   draft asset metadata.

The promotion request is repository data, not evidence authority. Promotion
recomputes and validates every referenced value.

## Phase 3: Promotion

The promotion workflow runs with content-write permission but no Apple signing
credentials. It:

1. locates exactly one draft release for the tag and exact source commit;
2. downloads every draft asset and verifies the exact four-name allowlist;
3. verifies checksums, archive safety, canonical package, evidence bundle, readiness,
   signing/notarization observations, runtime identity, proof registry,
   license/notices, support matrix, and candidate docs;
4. compares GitHub draft asset sizes/digests with local computed values;
5. waits at the protected `public-alpha` environment for authenticated manual
   approval;
6. publishes once as prerelease with `make_latest=false` and immutable release
   enforcement observed; and
7. performs bounded anonymous redownload and post-public evaluation.

Approval cannot alter inputs or bypass a failed check.

## Failure Semantics

- Failure before publish leaves the release draft and emits no public status.
- Publication happens only after the complete asset set is present.
- Anonymous verification retries transient failures three times with bounded
  backoff.
- Immutable publication cannot be rolled back or repaired in place. A
  persistent post-public verification failure emits no `public-verified`
  receipt, leaves public inventory and documentation unchanged, records an
  incident artifact, and consumes the version/tag permanently. The complete
  immutable prerelease may remain visible, but it is not an endorsed current
  release and its assets are never modified or replaced.
- Documentation public state is unchanged until a `public-verified` receipt
  exists.
- Cancellation and failure clean candidate-created Lima instances, browser
  processes, temporary roots, keychains, and secret-bearing session state.

## Phase 4: Public Truth

A successful receipt is retained as workflow evidence and drives a generated,
reviewable source pull request that:

- adds the release to `releases/current.json`;
- updates README/STATUS/changelog/release index from inventory;
- verifies public URLs anonymously;
- renders support and non-claim parity; and
- preserves candidate package docs as channel-neutral historical truth.

The promotion workflow does not write release facts directly to the protected
default branch. The generated pull request includes the validated receipt
digest and fails if regenerated inventory/docs differ from its checked output.

The receipt is never appended to the immutable release asset set.
