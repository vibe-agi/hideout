# Data Model: Packaging, Install, Upgrade, And Uninstall

<!-- markdownlint-disable MD013 -->

## Package Artifact

Local release artifact produced by `scripts/package-local.sh`.

Fields:

- `root`: package root directory after extraction.
- `manifest`: package artifact manifest.
- `files`: package-relative files included in the artifact.

Validation:

- Root must contain `package-manifest.json`.
- Package files must be regular files and stay inside package root.
- Executable package files must have an executable bit.

## Package Artifact Manifest

Declarative manifest shipped inside the package artifact.

Fields:

- `schema`: `hideout.package-manifest.v1`.
- `builtAt`: UTC build timestamp.
- `git.commit`, `git.dirty`: build source identity.
- `target.hostOS`, `target.hostArch`, `target.linuxGuestArch`: build targets.
- `layout.root`: `hideout`.
- `layout.binaries`: package-relative binary paths.
- `layout.entrypoints`: package-relative entrypoint files.
- `layout.directories`: package-relative directories included in artifact.
- `files[]`: package-relative file path, kind, SHA-256, executable expectation.
- `migration.fromInstalledSchemas[]`: installed-state schemas this package can
  upgrade from.

Validation:

- Required helper binaries and schemas must be present.
- Checksums must match current artifact contents.
- No absolute or parent-traversing paths.
- No symlink package files or directories.

## Installed Package State

Manifest written under the install prefix after install or upgrade.

Fields:

- `schema`: `hideout.package-install-state.v1`.
- `installedAt`: UTC install/upgrade timestamp.
- `installPrefix`: resolved install prefix.
- `storeRoot`: resolved durable store root selected during install.
- `package`: version/commit/build target copied from artifact manifest.
- `files[]`: installed prefix-relative package-owned file paths, kind,
  SHA-256, executable expectation.
- `directories[]`: installed prefix-relative package-owned directories.
- `migration.stateSchema`: current installed-state schema.

Validation:

- `installPrefix` must match the prefix being verified.
- Files must be regular files and match checksum.
- Executables must be executable.
- Paths must stay inside the install prefix.

## Install Prefix

Operator-selected directory where package-owned files are installed.

Rules:

- Must be resolved before writing installed state.
- May contain spaces.
- Is not treated as relocatable after install.
- May contain unrelated files that Hideout does not own.

## Durable Store State

User-owned state rooted at the selected Hideout store.

Includes:

- profiles;
- audit logs;
- evidence;
- adapter pack registry;
- decisions and notices;
- runtime/cache data that belongs to user state.

Rules:

- Preserved by install and upgrade.
- Preserved by uninstall unless `--purge` is explicitly present.
- Purge must be visible in output and survivor package audit evidence outside
  the deleted store when possible.

## Package Operation Result

Structured result for install, verify, and uninstall.

Fields:

- operation: install, upgrade, verify, uninstall, or dry-run.
- status: passed, failed, or denied.
- prefix and store paths where applicable.
- files copied, verified, or removed.
- durable state action: preserved or purged.
- diagnostic hint on failure.

## State Transitions

```text
no install -> install -> installed
installed -> install compatible package -> upgraded
installed -> verify -> installed
installed -> uninstall dry-run -> installed
installed -> uninstall -> package-files-removed, durable-state-preserved
installed -> uninstall --purge -> package-files-removed, durable-state-purged
```

Invalid transitions:

- incompatible migration range -> denied before mutation;
- manifest missing or untrusted -> denied before mutation;
- uninstall without installed-state manifest -> denied;
- relocation mismatch -> denied with reinstall hint.
