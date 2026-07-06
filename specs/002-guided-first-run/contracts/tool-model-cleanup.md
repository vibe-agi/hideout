<!-- markdownlint-disable MD013 -->

# Contract: Tool Model Cleanup

This contract defines the user-facing and schema-facing behavior for
`002-guided-first-run` after the scope reset to tool model cleanup. It is a CLI,
profile, schema, and documentation contract; it is not a new service API.

## Removed Surfaces

The following surfaces must not be accepted as live product behavior:

- npm global package declarations;
- npm-specific first-run flags;
- package-manager provider declarations;
- tool presets or preset providers;
- provider execution hooks that install or materialize guest tools;
- setup tasks whose purpose is to download, install, or provision user CLI
  tools.

If a removed surface is encountered, the contract is:

1. fail closed before side effects;
2. report an unsupported legacy tool-supply diagnostic;
3. do not silently migrate or ignore the input;
4. do not run package managers, backend setup, or host commands because of it;
5. point the operator to expected-command diagnostics and external environment
   setup.

## Expected-Command Profile Shape

Profiles or setup state may declare expected commands in product-neutral form:

```json
{
  "tools": {
    "expectedCommands": [
      "example-cli"
    ]
  }
}
```

For this feature, `tools.expectedCommands` is the canonical profile spelling.
Its entries are command-name strings. Changing the top-level field name or the
entry shape requires updating the spec and plan before implementation
continues. The semantic contract is fixed:

- the declaration names a guest command symbol;
- the declaration is diagnostic state;
- the declaration does not identify a package, registry, installer, script,
- host path, provider, description, or per-command required flag;
- the declaration does not request installation or repair.

## Diagnostics Contract

Doctor/status/profile validation should distinguish these states. This feature
defines the diagnostic contract and may validate it with controlled check
contexts. It does not require starting a guest environment only to prove command
existence; when no environment inspection is available, the correct state is
`not-checkable`.

| State | Meaning | Allowed side effects |
| --- | --- | --- |
| `present` | Expected command was observed in the selected guest/environment context. | None beyond diagnostics. |
| `missing` | Expected command was not observed. | None; no install or repair. |
| `not-checkable` | Environment could not be inspected. | None; report the blocked context. |
| `blocked` | Legacy unsupported tool-supply fields prevent trusted diagnostics. | None; reject legacy input. |

If the expected command is also required for the requested target run and is
missing or not checkable, Hideout must fail closed before claiming run
readiness.

## CLI Contract

Public help and command parsing must reflect the cleanup:

- removed npm/provider/preset flags are not advertised as supported commands;
- if a removed flag or subcommand is still parsed for diagnostic purposes, it
  must fail with an unsupported legacy tool-supply message;
- expected-command configuration is presented as diagnostic/readiness input,
  not as an installer;
- target command execution remains unchanged when the command already exists in
  the selected guest environment.

## Schema Contract

Schema validation must enforce the same boundary as runtime validation:

- valid expected-command declarations pass;
- removed npm/provider/preset fields fail;
- mixed old and new tool state fails rather than partially applying the new
  state;
- expected-command entries cannot carry objects, arguments, scripts, package
  sources, registries, host paths, notes, per-command required flags, or secret
  material.

## Scan Allowlist Contract

Repository scans for removed npm/provider/preset vocabulary must use an
explicit allowlist. Allowed hits are limited to:

- negative tests proving removed surfaces fail;
- unsupported legacy diagnostic strings;
- 002 spec/plan/contract/quickstart removal prose;
- historical comments that cannot affect runtime behavior.

Disallowed hits include:

- public help advertising removed flags or subcommands as supported;
- schema fields accepting removed tool state;
- profile/app/Manager/backend code applying removed state;
- InitTask kinds or backend config that install, provision, or materialize
  tools.

## Documentation Contract

Docs and specs must use this vocabulary:

- "expected command" for diagnostic declarations;
- "present", "missing", "not checkable", or "blocked" for diagnostic states;
- "base image", "named environment setup", or "ordinary in-boundary setup run"
  for materializing tools outside this feature.

Docs must not describe Hideout as installing npm globals, applying tool
presets, running package-manager providers, or shipping product-specific agent
CLI setup.
