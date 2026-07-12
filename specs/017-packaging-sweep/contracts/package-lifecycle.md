# Contract: Package Lifecycle Verification

<!-- markdownlint-disable MD013 -->

## Migration Compatibility

Upgrade is allowed only when all are true:

- old installed-state manifest parses successfully;
- old installed-state schema is in the new manifest's accepted
  installed-state schema set;
- old source package schema is within the new manifest's package schema
  compatibility range;
- install prefix recorded by old state resolves to the prefix being upgraded;
- new package artifact verifies successfully.

Upgrade is rejected before mutation when any item is false or unknown.

Diagnostic fields:

- installed-state schema;
- old package schema;
- new package schema;
- accepted installed-state schemas;
- supported package schema range;
- guidance.

## Obsolete File Detection

Detection compares:

- old installed-state file paths; and
- new installed file paths after artifact-to-prefix path mapping.

An obsolete package-owned file is present when the old path is absent from the
new set.

Detection must not:

- scan unrelated prefix contents as package-owned;
- delete obsolete paths during upgrade;
- classify unmanifested files as stale package-owned files.

## Repair Eligibility

A stale path is eligible only when:

- it appeared in the old installed-state file list;
- it is absent from the new installed-state file list;
- it resolves under the install prefix;
- it is not a symlink escape;
- current filesystem type is safe for package-file removal.

Ambiguous entries are reported but not removed.

## Evidence And Redaction

Lifecycle evidence must include:

- operation;
- status;
- file counts;
- stale file counts;
- durable-state action;
- repair/removal summary;
- purge survivor audit path when applicable.

Lifecycle evidence must not include:

- broker/UI/claim tokens;
- proxy secret values;
- `HIDEOUT_SECRET_*` backing material;
- generated machine ids;
- hidden runtime credential paths.
