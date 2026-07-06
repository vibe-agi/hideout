<!-- markdownlint-disable MD013 -->

# Data Model: Unified Named Environments With Declared Base Image

## Environment (record)

The stored representation of a named environment. Extends the existing record
with naming and image identity; drops diagnostic state from identity.

Fields (identity-relevant and new fields only; timestamps/status fields are
unchanged):

- `version`: record schema version, bumped by this feature. Any other version
  is rejected with clean-and-recreate guidance; never migrated.
- `id`: internal random identifier; keys the record path and instance name.
  Unchanged.
- `name`: unique user-facing handle. Conservative charset (letters, digits,
  `.`/`_`/`-`, no leading separator), unique case-insensitively across the
  store, `default` reserved in any case. Set at create; immutable in this
  slice (no rename).
- `autoNamed`: whether the name was derived (true) or operator-chosen
  (false). Display-only; semantics are identical.
- `profile` / `profileId` / `identityId` / `user` / `hostname`: unchanged.
- `backend`: unchanged (`lima` or `native`).
- `backendConfigVersion`: unchanged; use-time drift axis 1. For built-in
  template image declarations, changes to the backend's template mapping are
  represented by this version rather than by a separate image digest field.
- `workspace` / `guestWorkspace`: pinned at create; use-time drift axis 2.
  Comparison at use is by real file identity (`os.SameFile`-grade), not
  string equality; the stored string is evidence.
- `imageRef`: the declared base image string, verbatim; immutable pinned
  identity data. Forms: `template:<name>` or
  `https://…#sha256:<64hex>`. It is not compared to a later profile default:
  changing the profile default does not drift existing environments. URL
  digest mismatch is reported as a boot-time image verification failure, not a
  drift report.
- `toolsHash`: **removed** from identity and from the record. Expected-command
  declarations are read live from the profile at use time.
- `instanceName`: unchanged; empty for native.

Validation rules:

- `name` must pass charset validation, must not collide (case-insensitive)
  with any existing environment, and must not be `default`.
- `imageRef` must be present and valid (see Base Image Declaration).
- `workspace` must pass the dangerous-workspace-root policy (existing guard,
  existing high-risk override).
- Records with a foreign `version` fail every operation with guidance; no
  field of such a record is trusted or displayed except its path/version.
- `env list` may surface foreign-version records only as unsupported entries
  keyed by record id/path and version; name, image, workspace, and profile
  fields from such records are not trusted for display or selection.

State transitions:

```text
create (validate name, imageRef, workspace) -> record exists, no guest
first run -> guest boots from pinned image -> ready
use with identity match -> reuse guest
use with backend/workspace drift -> fail closed + drift report (no state change)
recreate (guest stopped, or --force) -> guest destroyed -> rebuilt from
  pinned declaration, same name, same record id
remove (guest stopped, or --force) -> guest and record deleted
prior-version record on any touch -> guidance, no operation
```

## Base Image Declaration

A single string pinned into the environment at create.

- Form 1 — built-in template: `template:<built-in-name>`. The shipped default
  profile carries `template:_images/ubuntu-lts` explicitly; no backend
  hardcode remains. The template name is the pinned image declaration; the
  concrete template mapping is covered by `backendConfigVersion`.
- Form 2 — disk-image URL: `https://<host>/<path>` ending in a disk-image
  extension, with a mandatory `#sha256:<64 hex>` fragment carrying the
  distributor-published digest.
- Rejected: missing digest on URL form (with guidance to the distributor's
  checksum file), non-https schemes, userinfo/embedded credentials, malformed
  digest strings, OCI-style references.
- Source precedence at create: explicit `--image` flag > selected profile's
  `environment.baseImage`. The value is copied into the record; later profile
  changes never affect existing environments.

## Profile (extension)

- `environment.baseImage`: optional string, same syntax and validation as the
  declaration above. The shipped default profile sets it explicitly.
- Existing `tools.expectedCommands` is unchanged and explicitly not part of
  environment identity.

## Drift Report

Produced when a selected environment's identity no longer matches current
inputs. Never triggers an automatic rebuild.

Fields:

- `environment`: name.
- `axes`: list of drifted axes, each with `axis`
  (`backendConfig` | `workspace`), the pinned value, and the current value
  (verbatim operator data).
- `hint`: copyable `hideout env recreate <name>` command (plus stop hint when
  the guest is running).

Validation rules:

- At least one axis present.
- Emitted to stderr and audited; the run exits non-zero before backend
  preparation.

## Expected-Command Diagnostic (relationship change only)

Unchanged entity from 002. Relationship change: diagnostics are computed at
use time from the profile's current declarations against the selected
environment's check context. They appear in readiness evidence labeled as
expectations and never enter environment identity or drift.

## Audit Events (evidence shape)

- `env.create` / `env.recreate` / `env.remove`: decision, environment name,
  image ref, workspace, backend; force usage recorded on
  recreate/remove.
- `env.drift.denied`: environment name plus the drifted axes (pinned and
  current values verbatim).
- Run evidence (existing run summary/audit) additionally names the selected
  environment.
- All values in these events are operator-declared user data and are recorded
  verbatim under the deterministic redaction contract.
