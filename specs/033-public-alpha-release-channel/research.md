# Research: Public Alpha Release Channel

## 1. Release Identity Is Version, Commit, Digest, And Target

**Decision**: Replace the current pre-release `PackageIdentity{Name, Version}`
shortcut with explicit product version, full source commit, archive SHA-256,
host OS, and host architecture. `Readiness.Commit` remains the source commit;
version and archive digest get separate readiness fields.

**Rationale**: `internal/productevidence/runtime.go:109-129` currently requires
`Version` to be a commit, and `internal/releasecompat/readiness.go:102-107`
copies it into readiness. That cannot distinguish two archives from one commit
and cannot represent SemVer without weakening commit validation. The package
archive digest cannot be embedded recursively inside itself, so release
candidate validation receives both the archive path and the validated inner
package manifest.

**Alternatives considered**:

- Accept SemVer in the existing `Version` field: rejected because it removes a
  commit trust anchor and preserves the overload.
- Keep commit-only package freshness: rejected because a same-commit rebuild
  would falsely satisfy release evidence.
- Infer archive digest from an extracted root: rejected because different
  archive bytes can extract to the same tree.

## 2. Establish One Canonical First-Release Schema

**Decision**: Establish one canonical v1 each for package manifest, package
install state, product-hardening evidence, and release readiness. Reject the
unpublished legacy internal package shape with explicit rebuild/reinstall
guidance instead of carrying dual readers into the first public release.

**Rationale**: `internal/packagekit/manifest.go:13-20` and
`schemas/package-manifest.schema.json:158-263` have no release identity and
strictly reject unknown fields. Product evidence v1 similarly exposes only
`name/version`. Because no Hideout product package has been published, the
release can cleanly define the first public schema instead of manufacturing a
compatibility obligation to private fixtures.

**Alternatives considered**:

- Keep both private and public schema variants: rejected because it creates
  permanent branching before any external compatibility promise exists.
- Reinterpret the legacy dot-versioned package schema in place: rejected
  because stale internal artifacts could be mistaken for release candidates;
  the canonical slash-versioned v1 identifier makes the break explicit.

## 3. Keep `tar.gz`, Use A ZIP Notarization Envelope

**Decision**: Distribute one `tar.gz`, but submit a private ZIP envelope made
from the frozen, Developer ID-signed package tree to Apple. Record ZIP digest,
submission ID, accepted status, observed Team ID/common name, and every signed
Mach-O identity. Build the final tar archive from that unchanged tree and make
no staple/offline-ticket claim.

**Rationale**: Apple accepts ZIP archives, UDIF disk images, and signed flat
installer packages; it does not accept `tar.gz`. Apple also states that a ZIP
cannot be stapled directly, while its ticket is available online. See
[Notarizing macOS software before distribution](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)
and
[Customizing the notarization workflow](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow).
The local host currently has only an Apple Development identity, not a
Developer ID Application identity, so real public-alpha signing remains an
external prerequisite; an unsigned build is developer-preview only.

**Alternatives considered**:

- Submit `tar.gz` directly: rejected because the notary service does not accept
  it.
- Switch v1 distribution to DMG or PKG: rejected because it would replace the
  already tested package/install path and enlarge 033.
- Describe the tar archive as stapled: rejected as false.

## 4. Observe Signing Independently And Require Hardened Runtime

**Decision**: Sign every host Mach-O with Developer ID Application, secure
timestamp, and hardened runtime; detect Mach-O by file content rather than a
manifest label. Core independently runs strict signature and system-policy
checks and records the first signing authority, Team ID, code identity, and
timestamp state. Package-declared identity is never authoritative.

**Rationale**: Apple's notarization guidance requires a valid Developer ID,
secure timestamp, and hardened runtime for custom workflows. See
[Resolving common notarization issues](https://developer.apple.com/documentation/security/resolving-common-notarization-issues).
`internal/hostcap/appidentity_darwin.go:78-123` already establishes the pattern
of bounded `codesign` plus `spctl` observation but needs a release-focused
observer that handles command-line binaries and preserves the leaf authority.

**Alternatives considered**:

- Trust identity fields in the package manifest: rejected as self-attestation.
- Sign only `bin/hideout`: rejected because `hideout-shim` is also a host
  Mach-O and every executable distributed for the host needs one policy.

## 5. Use Build, Retain, Real-Prove, Promote

**Decision**: Build/sign/notarize once on GitHub-hosted macOS arm64 and retain
the exact assets in a draft release. An operator downloads that draft package
to an independent macOS arm64 host, runs clean-install plus real Gate 2/3 and
033 evidence, uploads the bounded evidence, and commits a typed promotion
request. Promotion validates the draft and requires a protected environment
approval before publishing.

**Rationale**: The runtime workflows already implement draft retention and
promotion (`.github/workflows/runtime-developer-standard-retain.yml:23-74` and
`runtime-developer-standard-promote.yml:74-184`). GitHub's arm64 macOS runners
are suitable for builds but explicitly do not support nested virtualization,
so they cannot honestly replace real Lima gates. See
[GitHub-hosted runners reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).

**Alternatives considered**:

- Run Gate 2/3 on GitHub-hosted macOS: rejected because nested virtualization
  is unsupported.
- Rebuild locally for real gates: rejected because it breaks exact-byte
  identity.
- Treat old commit-bound evidence as sufficient: rejected because 033 requires
  the package archive digest.

## 6. Enable Immutable Releases And Consume Failed Versions

**Decision**: Require repository release immutability before promotion. Upload
the complete allowlisted asset set while draft, publish once, then anonymously
verify. The post-public receipt is workflow evidence, not a late release asset.
If bounded retries fail, emit no public-verified receipt or current-inventory
update, record a publication incident, and never reuse that tag or version.
The immutable prerelease may remain visible, but it is not endorsed as the
current alpha and its assets are never rewritten.

**Rationale**: GitHub recommends attaching all assets to a draft before
publishing an immutable release. Immutable releases lock the tag and assets and
generate a release attestation. Its tag and assets cannot be reused or
replaced. See [Immutable releases](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases)
and [Preventing changes to your releases](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/establish-provenance-and-integrity/prevent-release-changes).
The repository currently reports immutable releases disabled, so enablement is
an explicit implementation and publication prerequisite.

**Alternatives considered**:

- Upload the receipt after publication: rejected because immutable assets
  cannot be added or replaced.
- Return a failed public release to draft: rejected because that works only for
  mutable releases and would make workflow behavior repository-setting
  dependent.
- Reuse a failed version: rejected because GitHub reserves immutable tag names
  and users may have observed the bytes.

## 7. Use A Distinct Developer-Preview Identity

**Decision**: Public alpha mode requires Developer ID and accepted
notarization. If credentials are unavailable, an optional unsigned developer
preview uses a distinct SemVer prerelease/tag and explicit
`developer-preview-unsigned` status; it cannot consume or later be replaced in
place as `v0.1.0-alpha.1`.

**Rationale**: Release assets are immutable, and the ordinary alpha contract
requires platform identity. Distinct identity prevents an unsigned artifact
from occupying the version operators expect to be signed.

**Alternatives considered**:

- Publish unsigned bytes under the final alpha tag: rejected because later
  replacement is forbidden and the alpha label would overclaim platform trust.
- Block all release rehearsal without credentials: rejected because the
  complete non-public workflow still needs testable developer-preview paths.

## 8. Add A Post-Public Evidence Target Without A Cycle

**Decision**: Add a `public-release` evaluation target. It includes all
`release-candidate` requirements plus post-public download and docs-truth
requirements. Candidate readiness never requires public download; publication
receipt evaluation does.

**Rationale**: `internal/productevidence/evaluate.go:122-143` currently matches
one exact `requiredFor` target. Requiring public download in candidate readiness
would be circular, while evaluating only new public proofs would forget the
candidate's real gates. Target inclusion makes the state transition explicit.

**Alternatives considered**:

- Put public-download proof in the immutable evidence bundle: rejected because
  it cannot exist before publication.
- Keep the proof shell-only: rejected because the proof registry is the
  authoritative claim map.

## 9. Bound Public Evidence And Preserve Export Semantics

**Decision**: Publish a strict evidence-bundle manifest plus relative,
non-symlinked, digest-matched artifacts; cap the uncompressed bundle at 64 MiB
and retain existing per-file/gate limits. Include only registered proof inputs
and bounded logs. User-data-bearing inputs must first pass the existing 005
export decision boundary.

**Rationale**: `internal/productevidence/evaluate.go:15` already caps a release
artifact at 64 MiB, and the evaluator validates contained paths and digests.
Public release evidence must not become a raw dump of real-gate directories.

**Alternatives considered**:

- Publish complete dogfood output: rejected because it may contain user paths
  and content unrelated to required claims.
- Rely on source greps for secrets: rejected because injected credential and
  path fixtures are needed to prove refusal/redaction.

## 10. Use One Release Inventory With Two Truth Phases

**Decision**: Candidate/package docs remain exact but channel-neutral. After
anonymous verification, a publication receipt drives a generated update to
`releases/current.json`; README, STATUS, changelog, support docs, and their
human/JSON output validate against that inventory. Public facts are not
prewritten into the candidate commit.

**Rationale**: A package cannot truthfully claim an anonymous URL before the
URL exists. `docs/STATUS.md` currently cites local evidence and
`internal/releasecompat/matrix.go` owns support/non-claim data; those must be
joined through validated release inventory rather than duplicated Markdown
values.

**Alternatives considered**:

- Commit public URLs before promotion: rejected as a false claim on failed
  releases.
- Let each document hardcode version/digest: rejected because drift is
  inevitable and not machine-checkable.

## 11. Apache-2.0 Plus Separate Third-Party Attribution

**Decision**: Ship the product under Apache-2.0 and include the canonical
license in repository, package, manifest, and release notes. Generate and
review a separate third-party notice inventory for compiled Go dependencies
and packaged content; audit the already-public runtime separately.

**Rationale**: Apache-2.0 is the product-owner decision and includes an explicit
patent grant. The repository was public without a license, and the package and
runtime contain software whose terms are not replaced by the project license.

**Alternatives considered**:

- MIT: rejected by the product owner in favor of an explicit patent grant.
- Copyleft or source-available terms: rejected because they conflict with the
  intended permissive ecosystem and local-tool distribution model.

## 12. Verify Security Reporting As Repository State

**Decision**: Add `SECURITY.md`, bounded issue forms, contribution guidance,
and release-note templates, then enable GitHub private vulnerability reporting
and verify it through the repository API. Do not infer enablement from a file.

**Rationale**: GitHub treats `SECURITY.md` and private vulnerability reporting
as separate features. A public repository owner must enable the latter for the
private report button/API to exist. See
[Configuring private vulnerability reporting](https://docs.github.com/en/code-security/how-tos/report-and-fix-vulnerabilities/configure-vulnerability-reporting/configuring-private-vulnerability-reporting-for-a-repository).
The repository currently reports the feature disabled.

**Alternatives considered**:

- Direct security reports to public issues: rejected because disclosure may
  expose users and unresolved vulnerabilities.
- Claim a private channel from `SECURITY.md` alone: rejected because the
  external repository setting may be disabled.
