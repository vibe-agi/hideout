# Contract: Packaged Helpers And External Prerequisites

<!-- markdownlint-disable MD013 -->

## Packaged Helpers

Package-owned helpers are listed in the package manifest and verified by:

- relative path containment;
- regular file expectation;
- executable bit expectation;
- checksum;
- target compatibility.

Packaged helper failure status:

```text
package-helper-failed path=<path> expected=<state> actual=<state> hint=<repair>
```

Examples:

- missing helper binary;
- wrong checksum;
- helper path is a symlink;
- executable helper is not executable.

## Schemas, Scripts, And Docs

Schemas, scripts, docs, and installer entrypoints follow the same package-owned
artifact rules as helper binaries. Failure messages name the exact artifact and
repair/reinstall hint.

## External Prerequisites

External prerequisites are runtime dependencies not owned by the package
manifest. In 017, `tun2socks` is external.

External prerequisite status:

```text
external-prerequisite name=tun2socks status=<available|missing|undiscoverable> packageOwned=false hint=<guidance>
```

Rules:

- Missing `tun2socks` is never reported as a package checksum mismatch.
- Package verify may report prerequisite status separately from package-owned
  helper verification.
- Doctor may surface the same prerequisite status for privacy/tun2socks
  profiles.
- Vendoring or checksumming `tun2socks` requires a later spec.
