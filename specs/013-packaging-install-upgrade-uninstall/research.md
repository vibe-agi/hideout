# Research: Packaging, Install, Upgrade, And Uninstall

<!-- markdownlint-disable MD013 -->

## Decision 1: Keep Alpha Packaging Tarball-Based

**Decision**: Keep `scripts/package-local.sh` as the package builder and ship a
tarball containing `hideout/`, `install.sh`, binaries, schemas, docs, and a
package manifest.

**Rationale**: The roadmap explicitly defers Homebrew, signing, notarization,
auto-update, and OS package managers. Existing `scripts/package-local.sh` and
`scripts/test-package-smoke.sh` already provide a working local package shape.

**Alternatives considered**: Homebrew-first release bar, OS packages, and
auto-update were rejected as premature public release infrastructure.

## Decision 2: Go Owns Install, Upgrade, Verify, And Uninstall

**Decision**: Add Go-owned `hideout package install|verify|uninstall` behavior.
The shell `install.sh` wrapper only validates obvious package shape and calls
the packaged `hideout package install`.

**Rationale**: The constitution requires lifecycle and helper assembly to be
product behavior. Shell can wrap, but authority and fail-closed checks belong
in Go where tests can directly validate manifest parsing, checksum validation,
prefix recording, migration range, and purge behavior.

**Alternatives considered**: A shell-only installer was rejected because it
would duplicate manifest semantics and make unit-level failure tests brittle.

## Decision 3: Separate Artifact Manifest From Installed-State Manifest

**Decision**: The package artifact manifest records package-relative files. On
install, Hideout writes an installed-state manifest under the install prefix
that records the actual prefix, store root, installed file paths, checksums,
installed time, package build identity, and migration state.

**Rationale**: The artifact cannot know the operator-selected prefix before
install. The installed-state manifest is the authority for verify, upgrade, and
uninstall ownership decisions.

**Alternatives considered**: Treating the package artifact manifest as the
installed manifest was rejected because it would either lie about prefix or
make installed packages appear relocatable.

## Decision 4: Installed Packages Are Not Relocatable

**Decision**: `hideout package verify <prefix>` checks that the installed-state
manifest prefix matches the resolved prefix being verified.

**Rationale**: The user accepted operator-selected prefixes but not transparent
relocation. Relocation without reinstall can break helper discovery and docs.

**Alternatives considered**: Fully relocatable packages were rejected for alpha
because helper discovery and manifest ownership would become more complex.

## Decision 5: Upgrade Is Install With Existing-State Validation

**Decision**: Installing over a prefix with an installed-state manifest is an
upgrade. The new package must declare that the existing installed-state schema
is in its migration range before any package-owned file is replaced.

**Rationale**: This gives alpha users safe replacement semantics without
inventing a separate upgrade daemon or migration framework.

**Alternatives considered**: Separate `upgrade.sh` was rejected because it
would duplicate install behavior. Auto-update is out of scope.

## Decision 6: Uninstall Removes Only Manifest-Owned Files

**Decision**: Uninstall reads installed-state manifest ownership and removes
only listed package-owned files plus empty package-owned directories. Unrelated
files under the prefix are ignored.

**Rationale**: This satisfies exact dry-run reporting and avoids deleting user
or operator files that happen to share the prefix.

**Alternatives considered**: Removing the entire prefix was rejected because
the prefix may be shared with unrelated tools.

## Decision 7: Durable Store Is Preserved By Default

**Decision**: Profiles, audit, evidence, adapter registry, decisions, notices,
and store data are left intact unless `--purge` is explicitly present.

**Rationale**: The feature goal prioritizes cleanup without data loss.

**Alternatives considered**: Removing store on uninstall was rejected as a
data-loss footgun.

## Decision 8: Gate 0 Owns Package Smoke

**Decision**: Gate 0 keeps running package smoke, and package smoke expands to
prove fresh install, verify, missing-helper fail-closed, upgrade preservation,
uninstall dry-run, uninstall preservation, and purge behavior.

**Rationale**: 013 changes distribution and lifecycle, not real guest
isolation. Gate 0 is the right release gate.

**Alternatives considered**: Real Lima Gate 2/3 was rejected because packaging
does not change guest isolation behavior.
