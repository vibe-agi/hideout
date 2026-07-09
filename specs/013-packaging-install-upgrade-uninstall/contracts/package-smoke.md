# Contract: Package Smoke

<!-- markdownlint-disable MD013 -->

Package smoke is part of Gate 0.

It must prove:

- package artifact builds;
- extracted package verifies;
- install into a temporary prefix works without source checkout paths;
- installed prefix verifies;
- installed `hideout version` works;
- required helper binaries are executable;
- required schemas and docs are installed;
- missing helper or checksum mismatch fails closed before copying;
- reinstall to the same prefix is idempotent;
- compatible upgrade preserves durable store fixture files;
- incompatible migration range fails before mutation;
- uninstall dry-run removes no files and reports package-owned files;
- uninstall without purge removes package-owned files and preserves durable
  store fixture files;
- uninstall with purge removes durable store fixture files and records the purge
  action in output plus survivor package audit evidence outside the deleted
  store.

Smoke must not require real Lima unless a later feature changes the packaging
contract to include real isolation behavior.
