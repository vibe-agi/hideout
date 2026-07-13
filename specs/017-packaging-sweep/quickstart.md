# Quickstart: Packaging Sweep

<!-- markdownlint-disable MD013 -->

## Prerequisites

- Go toolchain for local package build.
- `jq`, `tar`, and SHA-256 tool for smoke scripts.
- macOS host for primary local package smoke; Linux target smoke where
  available.

## Scenario 1: Obsolete File Reported, Not Removed

Maps: FR-001, FR-002, FR-004, FR-005, SC-001, SC-002.

```sh
scripts/test-package-smoke.sh
```

Expected:

- package A installs an old package-owned file;
- package B omits that file;
- upgrade reports the obsolete file and repair hint;
- the obsolete file still exists after upgrade;
- package verify fails until repair is run.

## Scenario 2: Explicit Repair Removes Only Proven Obsolete Files

Maps: FR-003, FR-005, FR-012, FR-015, SC-003, SC-007.

```sh
scripts/test-package-smoke.sh
```

Expected:

- repair dry-run removes 0 files;
- repair apply removes only proven obsolete package-owned files;
- unrelated files under the prefix remain;
- durable store fixtures remain.

## Scenario 3: Incompatible Migration Fails Before Mutation

Maps: FR-006, FR-007, SC-004.

```sh
scripts/test-package-smoke.sh
```

Expected:

- incompatible installed-state schema is rejected;
- incompatible previous package schema is rejected;
- package-owned files are unchanged after rejection;
- diagnostic includes installed state, supported range, and guidance.

## Scenario 4: Packaged Helper Mismatch Names Artifact

Maps: FR-008, FR-009, SC-005.

```sh
tmp="$(mktemp -d)"
scripts/package-local.sh --out "$tmp/hideout.tar.gz"
tar -xzf "$tmp/hideout.tar.gz" -C "$tmp"
rm "$tmp"/hideout/bin/hideout-hostfsd-linux-*
"$tmp/hideout/bin/hideout" package verify "$tmp/hideout"
```

Expected:

- verify exits nonzero;
- output names the missing helper artifact;
- output includes expected state, actual state, and repair/rebuild hint.

## Scenario 5: `tun2socks` Is External Prerequisite

Maps: FR-010, FR-011, SC-006.

```sh
scripts/test-package-smoke.sh
```

Expected:

- package/doctor diagnostics classify `tun2socks` separately from packaged
  helpers;
- missing `tun2socks` is reported as external missing/undiscoverable;
- no package checksum failure is claimed for `tun2socks`.

## Scenario 6: Uninstall Preserve And Purge Evidence

Maps: FR-012, FR-013, FR-015, SC-007, SC-008.

```sh
scripts/test-package-smoke.sh
```

Expected:

- uninstall preserve lists package-owned removals and preserves store fixtures;
- uninstall purge requires explicit flag;
- survivor purge audit remains outside the deleted store.

## Scenario 7: Redaction Scan

Maps: SC-010.

```sh
go test ./internal/packagekit ./internal/app
```

Expected:

- lifecycle output/evidence tests inject control-plane-looking values;
- package lifecycle output contains 0 raw control-plane secret matches.

## Scenario 8: Final Gate

Maps: FR-014, SC-009.

```sh
go build ./...
go vet ./...
gofmt -l internal cmd
git diff --check
go test ./...
scripts/test-gate0.sh
```

Expected: all commands exit 0 and Gate 0 includes the expanded package smoke.
