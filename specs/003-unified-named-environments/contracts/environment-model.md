<!-- markdownlint-disable MD013 -->

# Contract: Unified Environment Model

CLI, record, and evidence contract for `003-unified-named-environments`. This
is not a new service API; Manager plan/apply operations back every mutation.

## Command Surface

```text
hideout env create <name> [--image <declaration>] [--workspace <path>] [--profile <p>] [--backend <b>]
hideout env list
hideout env inspect <name>
hideout env recreate <name> [--force]
hideout env remove <name> [--force]
hideout run --env <name> -- <command> [args...]
hideout run -- <command> [args...]          # resolves the auto-named environment
hideout stop <name>
hideout clean --stopped <name>
```

Contract points:

- `env create` validates name, image declaration, and workspace, then writes
  the record. It does not boot the guest and performs no network activity.
- `env list` is the only environment listing command; the previous top-level
  `hideout list` is removed from the public surface. Columns: name, backend,
  image declaration (digest suffix abbreviated for URL form), workspace,
  status, disk, last used. Auto-named environments are included and marked.
- `env inspect` prints the full pinned declaration (verbatim), identity axes,
  instance state, and record id.
- `run --env <name>` accepts names only, never record ids. The previous
  run-scoped environment-variable flag is renamed to `--env-var KEY=VALUE` so
  `--env` unambiguously selects environments. The selected
  environment record supplies the profile, backend, pinned image declaration,
  and pinned workspace binding. Supplying a conflicting `--profile`,
  `--backend`, or workspace input fails closed; a non-conflicting workspace
  input is compared to the pinned workspace by real file identity.
- A run without `--env` derives the auto-name for (profile, workspace),
  creating the environment on first use with the profile's image default.
- `--rm` keeps disposable, record-less semantics. `--ephemeral` keeps
  identity state session-local while resolving the same reusable environment
  as the corresponding normal run.
- `env recreate`/`env remove` on a running guest fail closed printing a
  copyable `hideout stop <name>`; `--force` stops first, then proceeds.
- Reserved name: `default` (any letter case) is rejected at create with a
  note that it is reserved for the shared environment.

## Image Declaration Forms

```text
template:<built-in-name>
https://<host>/<path>.(img|qcow2)#sha256:<64 hex>
```

- URL form without the digest fragment: rejected with guidance to the
  distributor's published checksums. No network resolution is attempted.
- Userinfo/embedded credentials in the URL: rejected.
- Anything else (OCI refs, other schemes, local paths): rejected as
  unsupported in this slice.
- Precedence at create: `--image` flag > profile `environment.baseImage`.
  The shipped default profile declares `template:_images/ubuntu-lts`.
- The backend consumes the declaration: template form maps to a Lima base
  template reference; URL form generates a lima.yaml `images` entry with
  `location` and `digest` so Lima downloads and verifies. Pull or digest
  failure fails the first boot closed, naming ref and digest.

## Drift Contract

Use-time drift axes: `backendConfig` (backend configuration version) and
`workspace` (real file identity). The image declaration is immutable pinned
environment data: profile default changes do not drift existing environments,
and URL digest mismatch is a boot-time image verification failure. On any
backend/workspace mismatch at use:

1. fail closed before backend preparation;
2. print a drift report naming each drifted axis with pinned and current
   values;
3. print a copyable `hideout env recreate <name>`;
4. audit `env.drift.denied`;
5. never rebuild, switch, or create a replacement silently.

Expected-command declarations are explicitly not a drift axis: changing them
produces no drift; they are evaluated live and a missing declared command
that is required for the requested target fails readiness with a diagnostic,
not a drift report.

## Record Versioning Contract

- The record version constant bumps once in this feature.
- Any record with a different version fails every operation that touches it
  with model-changed guidance (clean and recreate). No migration, no partial
  reads, no silent skips in listings.
- Listings may show such records only as `unsupported-version` rows keyed by
  record id/path and version. Name, image, workspace, and profile fields from
  a foreign-version record are not trusted for display or selection.

## Warning Contract (shadowed HostFS rules)

- During run planning and doctor, a profile HostFS rule whose path is inside
  the environment's pinned workspace (real-file-identity containment)
  produces a warning naming the rule and the workspace.
- The warning never blocks the run and is emitted at most once per rule per
  invocation.

## Evidence Contract

- `env.create`, `env.recreate`, `env.remove` (with force flag state), and
  `env.drift.denied` are audited with environment name, image ref, workspace,
  and backend, all verbatim.
- Run summaries and run audit name the selected environment.
- Manager overview/API expose name, image ref, workspace, status for each
  environment; TUI/WebUI render the same fields read-only in this slice.
- No new secret classes exist: image refs must not contain credentials and
  are rejected if they do, so evidence never needs to redact them.
