# Data Model: Packaging Sweep

<!-- markdownlint-disable MD013 -->

## Installed Package Manifest

Represents the package state currently installed under a prefix.

Core fields:

- `schema`: installed-state schema id.
- `installedAt`: timestamp of the install or upgrade.
- `installPrefix`: resolved install prefix.
- `storeRoot`: durable Hideout store associated with the install.
- `package`: source package metadata, including package manifest schema,
  commit, build time, and target.
- `files`: package-owned installed files with relative path, kind, checksum,
  and executable expectation.
- `directories`: package-owned directories eligible for empty cleanup.
- `migration`: compatibility metadata recorded with the installed state.

Validation:

- `schema` must equal the supported installed-state schema.
- `installPrefix` must resolve to the prefix being verified or mutated.
- File paths must be prefix-relative slash paths.
- File paths must not be absolute, parent-traversing, symlink escapes, or
  backslash-separated.

## New Package Manifest

Represents the package being installed or upgraded to.

Core fields:

- artifact schema;
- build metadata;
- target host/guest architecture;
- layout;
- package-owned files;
- migration compatibility range.

Validation:

- Artifact schema must be supported.
- Target host OS/architecture must match the local host.
- Required binaries, entrypoints, schemas, scripts, and docs must be covered by
  file checksums.
- Migration metadata must state which installed-state schemas are accepted.

## Obsolete Package-Owned File

Represents a file that the previous installed package owned but the new package
does not own.

Fields:

- `path`: install-prefix-relative stale path.
- `oldKind`: kind recorded by the old installed state.
- `oldSHA256`: checksum recorded by the old installed state.
- `exists`: whether the path currently exists.
- `currentType`: regular file, directory, symlink, special file, or missing.
- `eligibleForRepair`: true only when ownership is proven, the path stays under
  prefix, and the current path is safe to remove.
- `reason`: why the path is reported, eligible, or rejected.

State transitions:

```text
detected -> reported -> repaired
detected -> reported -> rejected
detected -> missing
```

Rules:

- Upgrade may create the `reported` state but must not transition to
  `repaired`.
- Repair revalidates path containment and ownership immediately before removal.
- Unrelated files never become obsolete package-owned files.

## Repair Plan

Represents an explicit operator action to remove obsolete package-owned files.

Fields:

- install prefix;
- stale file list;
- rejected stale entries;
- ownership proof source;
- dry-run flag;
- removed count;
- durable-state action (`preserved` only for repair);
- status.

Rules:

- Repair is explicit; upgrade does not imply repair.
- Repair must not delete durable store state.
- Partial repair failures must report files already removed and files not
  removed.

## Migration Compatibility Decision

Represents the decision made before upgrade mutates package-owned files.

Fields:

- installed-state schema;
- previous package artifact schema;
- new package artifact schema;
- allowed installed-state schemas;
- minimum package schema;
- maximum package schema;
- decision: `compatible` or `rejected`;
- reason and operator guidance.

Rules:

- Rejected decisions occur before copying package files.
- Unknown or malformed schemas are rejected.
- v1 uses schema allowlists/ranges only; no migration scripts.

## Packaged Helper

Represents a package-owned executable or support artifact verified by package
checksum.

Fields:

- relative path;
- kind;
- executable expectation;
- expected checksum;
- actual state;
- status.

Rules:

- Missing, non-regular, symlinked, mode-mismatched, or checksum-mismatched
  helpers fail package verification.
- Failure messages name expected and actual state.

## External Prerequisite

Represents a runtime dependency needed by a profile or host but not owned by the
package.

Fields:

- name;
- requiredFor;
- discovery status;
- guidance;
- packageOwned: always false for 017 `tun2socks`.

Rules:

- External prerequisites are not package checksum failures.
- `tun2socks` absence is reported as missing or undiscoverable external
  prerequisite when relevant.

## Lifecycle Evidence

Represents local evidence emitted by package lifecycle commands.

Fields:

- operation: install, upgrade, verify, repair, uninstall, purge;
- status;
- install prefix;
- store root when applicable;
- package-owned file counts;
- stale file counts;
- durable-state action;
- survivor audit path when purge deletes the store.

Rules:

- Evidence must not include control-plane secrets.
- Survivor purge audit must live outside the deleted store.
