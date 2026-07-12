# Contract: Package Sweep Commands

<!-- markdownlint-disable MD013 -->

## CLI Surface

```text
hideout package verify <package-root-or-install-prefix>
hideout package install <package-root> --prefix <dir> [--store <dir>]
                       [--backend native|lima|auto]
                       [--network direct|tun2socks]
                       [--proxy-secret <ref>]
                       [--mediated-resolver <ip>]
                       [--skip-init]
hideout package repair --prefix <dir> [--dry-run]
hideout package uninstall --prefix <dir> [--store <dir>]
                         [--dry-run] [--purge]
```

## Verify

Inputs:

- Extracted package root containing `package-manifest.json`; or
- installed prefix containing installed-state manifest under the package
  metadata location.

Success output:

```text
package: ok mode=<artifact|installed> root=<resolved-root> files=<n>
```

Failure output must include, when applicable:

- artifact or installed file path;
- expected state;
- actual state;
- ownership source for obsolete package-owned files;
- repair or reinstall hint.

Verify returns nonzero for:

- missing or malformed manifest;
- helper/schema/script checksum mismatch;
- non-regular or symlinked package-owned file;
- executable expectation mismatch;
- relocated install prefix;
- proven obsolete package-owned file still present.

## Install Or Upgrade

Upgrade behavior:

1. Verify the new package artifact.
2. Load existing installed state when present.
3. Check migration compatibility before copying files.
4. Compare old installed file set with the new installed file set.
5. Copy new package-owned files.
6. Write new installed-state manifest.
7. Report obsolete package-owned leftovers without deleting them.

Output must name:

- operation: install or upgrade;
- prefix;
- store;
- files copied;
- installed-state manifest path;
- obsolete package-owned file count;
- repair hint when obsolete files exist.

Upgrade must fail before package mutation when migration compatibility cannot
be proven.

## Repair

Inputs:

- `--prefix`: installed prefix.
- `--dry-run`: report only.

Behavior:

- Read installed-state metadata needed to identify obsolete files.
- Revalidate ownership and prefix containment.
- Refuse ambiguous, symlinked, escaping, or unprovable paths.
- Remove only eligible obsolete package-owned files.
- Preserve durable store state.

Output must name:

- obsolete paths considered;
- paths removed;
- paths rejected with reasons;
- durable state action: preserved.

Dry-run removes nothing.

## Uninstall

017 keeps the 013 uninstall contract and adds stronger evidence expectations:

- dry-run removes nothing;
- uninstall preserve removes only manifest-owned package files;
- purge removes durable state only when explicitly selected;
- output lists package-owned files and durable-state action;
- purge survivor audit path remains available after store deletion.
