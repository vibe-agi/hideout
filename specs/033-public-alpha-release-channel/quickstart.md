# Quickstart: Validate The Public Alpha Release Channel

This guide validates the release contracts. Commands whose implementation is
introduced by 033 are normative acceptance commands, not evidence that the
current checkout already satisfies them.

## Prerequisites

- macOS arm64
- Go 1.25.x, `jq`, `gh`, SHA-256 tools, Xcode command-line tools, and Lima
  2.1.x for candidate construction and local validation
- clean source checkout at the exact candidate tag
- a dedicated disposable HOME, store, install prefix, Lima home, and workspace
- access to the retained developer-standard runtime release
- for public-alpha mode: Developer ID Application identity, notarization
  credentials, enabled immutable releases, enabled private vulnerability
  reporting, and a protected `public-alpha` environment

The current workstation has no Developer ID Application identity. Until one is
provided, run the developer-preview/no-publish contract lanes and expect the
public-alpha signing gate to fail closed.

## 1. Schema And Identity Contract

**Covers**: FR-001..007, FR-014, FR-017, SC-002, SC-005

```bash
go test ./internal/packagekit ./internal/productevidence \
  ./internal/releasecompat ./internal/releasechannel
scripts/test-public-alpha-release.sh --contract-only
```

Verify positive canonical-v1 fixtures and mutations for version/tag mismatch,
abbreviated commit, dirty source, unsupported target, same-commit changed
archive, missing archive digest, and field substitution. Every mutation must
fail before readiness.

## 2. Binary And Package Identity

**Covers**: FR-003..009, FR-022, SC-002, SC-004, SC-005

```bash
./hideout/bin/hideout version --json | jq -e '
  .schema == "hideout.binary-identity/v1" and
  (.productVersion | test("^[0-9]+\\.[0-9]+\\.[0-9]+-")) and
  (.sourceCommit | test("^[0-9a-f]{40}$")) and
  .hostOS == "darwin" and .hostArch == "arm64"'

./hideout/bin/hideout package verify ./hideout
```

Extract the exact archive, validate package manifest v1, verify every package
file, and confirm that the archive digest is supplied externally rather than
claimed by the inner manifest.

## 3. Signing And Notarization

**Covers**: FR-010..012, FR-018, SC-003, SC-010, SC-012

```bash
hideout support release validate-signing \
  --package-root ./hideout \
  --observation signing-observation.json

hideout support release validate-notarization \
  --package-root ./hideout \
  --observation notarization-observation.json
```

The real lane must observe every host Mach-O as Developer ID signed with the
declared Team ID, secure timestamp, and hardened runtime, and must validate an
accepted ZIP submission derived from the unchanged signed tree. Empty,
package-declared, Apple Development, rejected, or credential-bearing
observations fail. Developer preview returns an explicit unsigned status and
cannot satisfy public-alpha readiness.

## 4. Public Asset And Bundle Validation

**Covers**: FR-013..017, FR-032, SC-001, SC-005, SC-007, SC-008, SC-010

```bash
hideout support release validate \
  --manifest hideout-v0.1.0-alpha.1-release.json \
  --asset-root ./candidate-assets
```

Run adversarial fixtures for missing/extra/zero-byte assets, unsorted or
incomplete checksums, symlink/hard-link/path traversal, duplicate tar names,
unregistered proof IDs, digest mismatch, stale package/runtime identity,
control-plane credentials, and local absolute paths. The exact four-asset set
must pass.

## 5. Candidate Readiness

**Covers**: FR-005..006, FR-017..019, FR-024, FR-037, SC-005..007, SC-012,
SC-019

```bash
hideout support readiness \
  --mode release-candidate \
  --package-artifact candidate-assets/package.tar.gz \
  --runtime-family developer-standard \
  --gate2-evidence evidence/gates/gate2.json \
  --gate3-evidence evidence/gates/gate3.json \
  --signing-observation evidence/signing/observation.json \
  --notarization-observation evidence/notarization/observation.json \
  --product-evidence evidence/proofs/product-hardening-evidence.json \
  --out evidence/release-readiness.json
```

Assert `releaseReady=true`, exact archive digest, full source commit, product
version, runtime build, distinct Gate 2/Gate 3 environment IDs, all registered
candidate proofs satisfied, and zero required `not-run` row. Remove each 033
proof and each required claim mapping in turn; every mutation must fail.

## 6. Clean Install And Direct First Success

**Covers**: FR-008..009, FR-022..023, FR-030, SC-004

```bash
scripts/test-public-alpha-clean-install.sh \
  --package candidate-assets/hideout-v0.1.0-alpha.1-darwin-arm64.tar.gz \
  --runtime-family developer-standard
```

The harness supplies and records a supported Lima version, removes Go/source
and developer PATH fallback, creates fresh HOME/store/Lima/workspace roots,
installs with `--skip-init`, proves no profile exists before explicit init,
then runs one Lima/direct `pwd` as the non-root synthetic target. It must show
that the approximately 1 GB runtime is separate and must not claim privacy
networking.

## 7. Exact Privacy Gate

**Covers**: FR-018, FR-024, SC-006..007

```bash
HIDEOUT_SECRET_DEFAULT_PROXY=<operator-secret-ref> \
scripts/test-public-alpha-candidate.sh \
  --tag v0.1.0-alpha.1 \
  --package candidate-assets/hideout-v0.1.0-alpha.1-darwin-arm64.tar.gz \
  --candidate-observation candidate-assets/candidate.json \
  --signing-observation candidate-assets/signing-observation.json \
  --notarization-observation candidate-assets/notarization-observation.json \
  --out .hideout-release-evidence/033-public-alpha/v0.1.0-alpha.1
```

Gate 2 and Gate 3 must use the same package digest and retained runtime build
but distinct managed environments. Gate 3 must use the actual privacy network;
direct fallback and native evidence fail.

## 8. Reinstall, Repair, Uninstall, And Compatibility

**Covers**: FR-025..026, FR-030, SC-009

```bash
scripts/test-package-smoke.sh
go test ./internal/packagekit -run 'Install|Migration|Downgrade|Repair|Uninstall'
```

Prove same-version idempotence, bounded repair of package-owned files, normal
uninstall preserving the store, explicit purge, v1 private-state import, and
fail-closed unsupported downgrade before mutation. After a second public alpha,
replace the synthetic N-1 fixture with the real previous package.

## 9. Release Workflow Rehearsal

**Covers**: FR-002, FR-004, FR-018..021, FR-031..032, SC-008, SC-012..013

```bash
scripts/test-public-alpha-release.sh --no-publish
```

The rehearsal builds once, freezes bytes, validates the full asset set, and
ends at `not-published`. Fault fixtures cover failed gate, missing asset,
approval absence, post-upload digest drift, cancellation cleanup, and attempted
rebuild. No fixture may create a public release.

## 10. Repository Trust Prerequisites

**Covers**: FR-011..012, FR-018..019, FR-033..035, SC-012, SC-015..016

```bash
gh api -H 'X-GitHub-Api-Version: 2026-03-10' \
  repos/vibe-agi/hideout/immutable-releases
gh api -H 'X-GitHub-Api-Version: 2026-03-10' \
  repos/vibe-agi/hideout/private-vulnerability-reporting
```

Both responses must have `enabled=true`. Independently verify the protected
approval environment, Apache-2.0 parity, third-party notices, and absence of
release credentials from actions artifacts/logs. Source text alone cannot
satisfy repository-state checks.

## 11. Public Promotion And Anonymous Download

**Covers**: FR-001..006, FR-013..021, FR-031..032, FR-039, SC-001..003,
SC-005..008, SC-010, SC-012..013, SC-018

Trigger promotion only after the draft and real evidence pass. After manual
approval, verify:

If a pre-publication candidate must be rebuilt, rerun the candidate workflow
with `replace_private_draft=true`. That input is valid only while the same tag
is an unpublished draft; an existing Git tag or published release remains
immutable and fails closed.

```bash
curl --fail --location --retry 3 \
  -o package.tar.gz \
  https://github.com/vibe-agi/hideout/releases/download/v0.1.0-alpha.1/hideout-v0.1.0-alpha.1-darwin-arm64.tar.gz
shasum -a 256 package.tar.gz
gh release verify v0.1.0-alpha.1 --repo vibe-agi/hideout
gh release verify-asset v0.1.0-alpha.1 package.tar.gz \
  --repo vibe-agi/hideout
```

The post-public evaluator must produce a `public-verified` receipt from
anonymous downloads and immutable release observations. Persistent failure
emits an incident instead of a receipt, consumes the version, and leaves the
current inventory and public docs unchanged. It does not attempt to delete,
revoke, mutate, or endorse the immutable prerelease.

## 12. Documentation And Support Truth

**Covers**: FR-027..029, FR-038..039, SC-011, SC-014, SC-018..019

```bash
hideout support matrix > matrix.txt
hideout support matrix --json > matrix.json
scripts/test-doc-truth-smoke.sh
```

Before publication, candidate docs must not claim public availability. After a
validated receipt, README, STATUS, changelog, release notes, support docs, and
human/JSON support output must derive matching version/platform/maturity and
non-claims from `releases/current.json` plus the authoritative support matrix.
Deleting a required non-claim or claim mapping must fail.

## 13. License, Notices, Feedback, And Security

**Covers**: FR-033..036, SC-015..017

```bash
npx --yes markdownlint-cli2 \
  'README*.md' 'SECURITY.md' 'CONTRIBUTING.md' 'CHANGELOG.md' \
  '.github/**/*.md' 'docs/**/*.md' 'specs/033-*/**/*.md'
```

Verify Apache-2.0 in repository/package/manifest/release notes, separately
attributed third-party notices, an independently enabled private vulnerability
channel, bounded issue forms, and executable doctor/export support commands.
Injected secrets must appear in no output.

## 14. Unsupported And Preview Lanes

**Covers**: FR-007, FR-010, FR-026, FR-028..030, SC-012, SC-014

Test Intel macOS, Linux package, Windows, unsigned package, missing Lima,
missing `tun2socks`, unknown install state, and developer preview. Every lane
must produce exact scope/recovery and zero public-alpha, GA, stable-update,
Linux-package, guest-root, workspace-DLP, or marketplace claim.

## 15. Final Battery

**Covers**: FR-001..039, SC-001..019

```bash
go build ./...
go vet ./...
test -z "$(gofmt -l internal cmd)"
git diff --check
go test ./...
scripts/test-gate0.sh
npx --yes markdownlint-cli2 '**/*.md' '#node_modules' '#dist'
```

For a real public candidate, append clean-install, Gate 2, Gate 3, candidate
bundle/readiness, draft validation, protected approval, immutable publication,
anonymous redownload, receipt validation, and post-public docs truth. A green
local battery alone is not a public release proof.

## Requirement Coverage Index

The scenarios above explicitly cover every requirement. This index is also a
machine-checkable guard against a requirement being added without a validation
scenario.

- Scenarios 1-5 and 11 cover FR-001, FR-002, FR-003, FR-004, FR-005, FR-006,
  FR-007, FR-010, FR-011, FR-012, FR-013, FR-014, FR-015, FR-016, FR-017,
  FR-018, FR-019, FR-020, and FR-021.
- Scenarios 2, 6, and 7 cover FR-008, FR-009, FR-022, FR-023, and FR-024.
- Scenario 8 covers FR-025 and FR-026.
- Scenarios 10 and 12-14 cover FR-027, FR-028, FR-029, FR-030, FR-031,
  FR-032, FR-033, FR-034, FR-035, FR-036, FR-037, FR-038, and FR-039.
- Scenarios 1-7 and 11 cover SC-001, SC-002, SC-003, SC-004, SC-005, SC-006,
  SC-007, SC-008, SC-010, and SC-012.
- Scenarios 8-10 and 12-14 cover SC-009, SC-011, SC-013, SC-014, SC-015,
  SC-016, SC-017, SC-018, and SC-019.
