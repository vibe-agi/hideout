# Quickstart: Packaging, Install, Upgrade, And Uninstall

<!-- markdownlint-disable MD013 -->

## Prerequisites

- Go toolchain for building local alpha package.
- `jq`, `tar`, and SHA-256 tool (`shasum` or `sha256sum`) for smoke scripts.
- macOS host for primary smoke; Linux target smoke where available.

## Scenario 1: Build And Verify Package

Maps: FR-001, FR-004, SC-001, SC-002.

```sh
tmp="$(mktemp -d)"
scripts/package-local.sh --out "$tmp/hideout.tar.gz"
tar -xzf "$tmp/hideout.tar.gz" -C "$tmp"
"$tmp/hideout/bin/hideout" package verify "$tmp/hideout"
```

Expected:

- package archive exists;
- `package verify` reports `package: ok`;
- manifest includes CLI, helper binaries, schemas, docs, and installer.

## Scenario 2: Fresh Install Without Source Checkout

Maps: FR-002, FR-013, SC-002.

```sh
tmp="$(mktemp -d)"
scripts/package-local.sh --out "$tmp/hideout.tar.gz"
tar -xzf "$tmp/hideout.tar.gz" -C "$tmp"
"$tmp/hideout/install.sh" --prefix "$tmp/prefix" --store "$tmp/store" \
  --backend native --network direct
"$tmp/prefix/bin/hideout" package verify "$tmp/prefix"
```

Expected:

- installed `hideout` works from `$tmp/prefix/bin`;
- installed-state manifest records `$tmp/prefix`;
- source checkout is not needed after extraction.

## Scenario 3: Helper Mismatch Fails Closed

Maps: FR-004, FR-005, SC-003.

```sh
tmp="$(mktemp -d)"
scripts/package-local.sh --out "$tmp/hideout.tar.gz"
tar -xzf "$tmp/hideout.tar.gz" -C "$tmp"
rm "$tmp"/hideout/bin/hideout-hostfsd-linux-*
"$tmp/hideout/install.sh" --prefix "$tmp/prefix" --store "$tmp/store"
```

Expected:

- install exits nonzero;
- diagnostic names the missing HostFS helper;
- no package files are copied to `$tmp/prefix`.

## Scenario 4: Upgrade Preserves Durable State

Maps: FR-006, FR-007, FR-008, SC-004, SC-005.

```sh
scripts/test-package-smoke.sh
```

Expected:

- compatible reinstall/upgrade keeps store fixture files;
- incompatible installed-state schema fails before file mutation;
- idempotent reinstall is accepted.

## Scenario 5: Uninstall Dry-Run And Preserve

Maps: FR-009, FR-010, FR-012, SC-006, SC-007.

```sh
scripts/test-package-smoke.sh
```

Expected:

- dry-run lists package-owned files and removes none;
- uninstall removes only manifest-owned files;
- durable store fixture files remain.

## Scenario 6: Purge Requires Explicit Flag

Maps: FR-011, SC-008.

```sh
scripts/test-package-smoke.sh
```

Expected:

- durable state remains without `--purge`;
- durable state is removed only with `--purge`;
- purge is visible in output and survivor package audit evidence outside the
  deleted store.

## Scenario 7: Docs Use Packaged Path

Maps: FR-014, FR-015, SC-009.

```sh
npx --yes markdownlint-cli2 README.md 'docs/**/*.md' \
  'specs/013-packaging-install-upgrade-uninstall/**/*.md'
```

Expected:

- README/docs primary install path uses package commands;
- source checkout commands are labeled development-only;
- deferred packaging surfaces are not described as the alpha release bar.

## Scenario 8: Final Gate

Maps: FR-013, FR-016, SC-010.

```sh
go build ./...
go vet ./...
gofmt -l internal cmd
git diff --check
go test ./...
scripts/test-gate0.sh
```

Expected: all commands exit 0 and Gate 0 includes package smoke.
