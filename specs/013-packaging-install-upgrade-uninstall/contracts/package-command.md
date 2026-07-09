# Contract: Package Commands

<!-- markdownlint-disable MD013 -->

## CLI Surface

```text
hideout package verify <package-root-or-install-prefix>
hideout package install <package-root> --prefix <dir> [--store <dir>]
                       [--backend native|lima|auto]
                       [--network direct|tun2socks]
                       [--proxy-secret <ref>]
                       [--skip-init]
hideout package uninstall --prefix <dir> [--store <dir>]
                         [--dry-run] [--purge]
```

## Verify

`verify` accepts either:

- extracted package root containing `package-manifest.json`; or
- install prefix containing installed-state manifest under the package metadata
  location.

Success output:

```text
package: ok mode=<artifact|installed> root=<resolved-root> files=<n>
```

Failure requirements:

- nonzero exit;
- artifact or installed file name in diagnostic when applicable;
- doctor-style hint when the operator can repair by reinstalling or rebuilding.

## Install Or Upgrade

Inputs:

- `package-root`: extracted package root.
- `--prefix`: destination prefix.
- `--store`: durable store root.
- optional init flags forwarded to typed `hideout init`.

Behavior:

- Verify package artifact before copying.
- Resolve prefix and store.
- If installed-state manifest already exists, validate migration range before
  mutation.
- Copy package-owned files to prefix.
- Write installed-state manifest with actual prefix and store.
- Run typed init unless `--skip-init` is present.
- Preserve durable store contents.

Output must name:

- operation: install or upgrade;
- prefix;
- store;
- files copied;
- installed-state manifest path;
- next command hint.

## Uninstall

Inputs:

- `--prefix`: installed prefix.
- `--store`: durable store root, optional when installed-state already records
  it.
- `--dry-run`: report only.
- `--purge`: remove durable state after package-owned files are removed.

Behavior:

- Read installed-state manifest.
- Refuse if prefix does not match installed-state manifest.
- Remove only manifest-owned files and empty package-owned directories.
- Preserve durable state unless `--purge` is present.
- On dry-run, remove nothing.

Output must name:

- package-owned files that would be or were removed;
- durable state action: preserved or purged;
- explicit purge warning when `--purge` is present;
- survivor purge audit path when durable store state is deleted.
