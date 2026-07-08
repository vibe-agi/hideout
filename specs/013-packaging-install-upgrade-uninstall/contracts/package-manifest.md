# Contract: Package Manifest

<!-- markdownlint-disable MD013 -->

## Artifact Manifest

Artifact manifest schema id: `hideout.package-manifest.v1`.

Required top-level fields:

- `schema`
- `builtAt`
- `git`
- `target`
- `layout`
- `files`
- `migration`

Each file entry contains:

- `path`: package-relative path using slash separators.
- `kind`: one of `binary`, `linux-helper`, `helper-manifest`, `installer`,
  `entrypoint`, `schema`, `doc`, `script`, or `packaging`.
- `sha256`: lowercase hex SHA-256.
- `executable`: boolean.

The artifact manifest must never contain:

- absolute paths;
- parent traversal;
- control-plane token values;
- proxy secret values;
- generated machine ids;
- transient runtime credential paths.

## Installed-State Manifest

Installed-state schema id: `hideout.package-install-state.v1`.

Required top-level fields:

- `schema`
- `installedAt`
- `installPrefix`
- `storeRoot`
- `package`
- `files`
- `directories`
- `migration`

Each installed file entry contains:

- `path`: install-prefix-relative path using slash separators.
- `kind`: copied file kind.
- `sha256`: lowercase hex SHA-256.
- `executable`: boolean.

Verification fails when:

- `installPrefix` differs from the resolved prefix being verified;
- any file is missing, symlinked, non-regular, non-executable when required, or
  checksum-mismatched;
- a path escapes the prefix;
- the manifest schema is unsupported.
