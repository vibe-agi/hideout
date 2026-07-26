# Contract: Shareable Support Report

## Command

```text
hideout support report --out <path> \
  [--profile <name>] \
  [--backend auto|lima] \
  [--workspace <path>]
```

The command is local-only and does not upload data.

## Output

- Schema: `hideout.support-report/v1`
- Encoding: UTF-8 JSON with one trailing newline
- Maximum size: 1 MiB
- File mode: `0600`
- Write: atomic replacement is allowed only when the destination is an
  existing regular file owned by the current user and explicit overwrite
  semantics are selected; default is no overwrite

Top-level fields:

```text
schema
generatedAt
product
support
package
doctor
recovery
collection
redaction
provenance
```

Unknown fields are rejected.

## Collection

Required:

- binary product version, commit, build time, host OS and architecture;
- support matrix schema/version and platform/backend levels;
- redacted light doctor report;
- unique registered recovery codes and safe next actions.

Conditional:

- package identity and verification when the executable belongs to an installed
  package;
- `not-applicable` when running from source/development output.

Excluded by default:

- raw audit events;
- workspace file names or contents;
- environment variable names used as secret backing;
- proxy URL or credentials;
- daemon/capability/UI tokens;
- generated machine ID;
- raw host-user paths;
- session runtime files;
- full process/environment dumps.

## Failure behavior

- Unsafe destination: fail before collection and write no file.
- One optional collector fails: record `failed` for that section and continue.
- Required doctor collection fails: write no successful report.
- Size limit exceeded: fail and retain no final file.
- Redaction validation fails: fail and retain no final file.
- No output path: print usage and fail without state changes.

## Evidence

Positive, negative, and mutation tests must cover:

- healthy source/development executable;
- installed verified package;
- damaged installed package;
- missing profile/backend;
- injected control-plane tokens and proxy values;
- raw user path and workspace fixture content;
- symlink and unsafe-parent destinations;
- oversized diagnostic input;
- attempted overwrite.
