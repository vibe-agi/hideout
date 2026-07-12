# Research: Packaging Sweep

<!-- markdownlint-disable MD013 -->

## Decision 1: Obsolete Package-Owned Files Are Report-First

**Decision**: Upgrade detects old package-owned files absent from the new
manifest and reports them, but normal upgrade does not remove them. A separate
explicit repair action removes only paths whose ownership and prefix containment
are revalidated at repair time.

**Rationale**: This matches the project's fail-closed and no-surprise cleanup
pattern. Existing uninstall already preserves durable state by default
(`internal/packagekit/uninstall.go:67-71`), and destructive state deletion
requires explicit purge (`internal/packagekit/uninstall.go:93-104`).

**Alternatives considered**:

- Auto-remove during upgrade when ownership is proven. Rejected because a
  corrupt or stale manifest could turn a supportability issue into destructive
  cleanup.
- Warn only and never provide repair. Rejected because external alpha users need
  an actionable path to return package verify to green.

## Decision 2: Repair Uses Old Installed-State Ownership, Not Directory Scans

**Decision**: Obsolete detection compares the old installed-state `Files` list
with the new installed file mapping. Repair candidates come only from old
manifest entries missing in the new install state.

**Rationale**: Current install state records copied file paths and checksums
(`internal/packagekit/manifest.go:55-64`). Uninstall already removes only
manifest-owned files (`internal/packagekit/uninstall.go:49-81`). Directory scans
would risk classifying operator-created files under the prefix as package-owned.

**Alternatives considered**:

- Scan install prefix for unknown files. Rejected because unrelated operator
  files under the prefix are intentionally ignored.
- Compare package artifact paths directly. Rejected because installed paths are
  remapped through `installPathForArtifact` (`internal/packagekit/path.go:46-55`).

## Decision 3: Migration Range Remains Schema Allowlist Plus Fail-Closed

**Decision**: 017 does not introduce migration scripts or data transformation
hooks. It makes the existing migration fields effective by validating installed
state schema and previous package schema before any package mutation.

**Rationale**: Current install checks only whether `existing.Schema` appears in
`manifest.Migration.FromInstalledSchemas` (`internal/packagekit/install.go:54-60`).
The manifest already has `MinimumPackageSchema` and `MaximumPackageSchema`
fields (`internal/packagekit/manifest.go:41-46`), but they do not yet affect
upgrade. A fail-closed schema gate is enough for alpha package compatibility.

**Alternatives considered**:

- Typed migration hooks now. Rejected because the current package data model has
  no transformation needs and arbitrary package migration logic would add a new
  authority surface.
- Ignore the min/max fields until public release. Rejected because inert fields
  create false confidence and were explicitly identified as a cleanup target.

## Decision 4: `tun2socks` Is An External Prerequisite In 017

**Decision**: Package verify and doctor may report `tun2socks` discovery status,
but `tun2socks` is not package-owned, not checksummed by the package manifest,
and not claimed as installed by Hideout in 017.

**Rationale**: The product uses `tun2socks` for privacy-profile network
mediation, but 017 is a packaging sweep, not a vendoring or helper-build spec.
The package manifest file model covers package-owned files with checksums
(`internal/packagekit/manifest.go:48-53`); claiming coverage for a host-provided
tool would be an overclaim.

**Alternatives considered**:

- Vendor `tun2socks` now. Rejected as a new distribution and support surface.
- Ignore it in package diagnostics. Rejected because privacy-profile first-run
  failures need a clear prerequisite message.

## Decision 5: Verification Fails On Proven Obsolete Package-Owned Files

**Decision**: Installed package verification fails when obsolete package-owned
leftovers are proven by lifecycle metadata, and the failure includes the stale
path and repair hint.

**Rationale**: A package with known old package-owned leftovers is not cleanly
verified. Current `VerifyInstalled` checks manifest-owned files only
(`internal/packagekit/verify.go:130-163`), so it can pass while old owned files
linger. 017 turns that silent ambiguity into an actionable failure.

**Alternatives considered**:

- Verification warning with exit 0. Rejected because automation and Gate 0 need
  a hard signal.
- Treat stale files as uninstall-only cleanup. Rejected because the issue occurs
  at upgrade/verify time and should be visible there.

## Decision 6: Lifecycle Evidence Stays Local And Redacted

**Decision**: Upgrade, repair, uninstall preserve, and uninstall purge output
the package-owned file lists and durable-state actions. Audit records keep
summary counts plus action/status; survivor purge audit remains outside the
deleted store.

**Rationale**: Package lifecycle evidence is local operational evidence, not an
export artifact. The existing purge fix writes survivor audit after store
deletion (`internal/packagekit/uninstall.go:93-104`). 017 should preserve that
property while adding repair and stale-file evidence.

**Alternatives considered**:

- Include full package manifest snapshots in audit. Rejected because command
  output and manifest files already provide detail, while audit should remain
  compact and redaction-safe.
