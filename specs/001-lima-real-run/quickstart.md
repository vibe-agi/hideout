<!-- markdownlint-disable MD013 -->

# Quickstart: Validate Lima Real Run

This guide describes how the completed feature should be validated. It is not a
general first-run setup guide and does not replace release-candidate gates.

## Prerequisites

- macOS host with Lima available.
- Hideout checkout with helper build capability.
- A sanitized workspace. Do not use `$HOME`, the Hideout store, browser
  profile directories, credential roots, or parents containing those roots.
- Optional: operator proxy if validating privacy network mode.

## Fast Local Checks

Run the fast checks first:

```bash
scripts/test-phase1.sh --quick
```

Expected outcome:

- Gate 0 passes.
- Gate 1 native smoke passes as wiring evidence only.
- Gate 4 dry-run passes.

## Reference Lima Run

Run the dedicated reference smoke directly:

```bash
scripts/test-lima-real-run.sh
```

Or include it after Gate 2 through the phase-one gate driver:

```bash
scripts/test-phase1.sh --lima-real-run
```

Expected outcome:

- A temporary sanitized workspace is created.
- The target CLI runs through `hideout run --backend lima`.
- The target updates the expected workspace file.
- The success check passes.
- The target reaches the declared endpoint through the selected network mode.
- The smoke prints session ID, environment ID, audit path, network mode, and
  Boundary Summary presence.
- The smoke asserts the fixed Boundary Action Set from the run audit instead of
  recomputing boundary facts independently.
- The smoke exits with `lima-real-run: passed`.

## Privacy Network Variant

When validating privacy mode, provide an operator proxy and run the same smoke
with privacy network mode enabled:

```bash
HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:<port> \
HIDEOUT_LIMA_REAL_RUN_NETWORK=privacy \
  scripts/test-lima-real-run.sh
```

Expected outcome:

- The run uses the existing `tun2socks` options and reaches the declared
  endpoint through the selected privacy network path.
- The full proxy URL does not appear in target env, run output, audit, summary,
  or smoke logs.
- If proxy prerequisites are missing, the smoke fails closed before claiming
  dogfood success.

## Boundary Evidence Check

The smoke must assert a fixed boundary action set. A human reviewer can also
inspect the session:

```bash
hideout audit show --session <session-id> --json
```

Expected evidence:

- host.open denial for unsafe localhost/private target;
- HostFS reserved-root or denied access;
- network setup mode;
- session lifecycle events;
- preview.open / endpoint.expose.host-to-guest evidence;
- Boundary Summary derived from audit facts;
- no broker token, proxy credential, hidden endpoint internals, or callback/open
  query secret.

## Negative Checks

The feature is not valid unless these cases fail closed:

```bash
# Unsafe workspace must reject before backend prepare.
hideout run --backend lima --workspace "$HOME" -- true

# Native backend must not count as dogfood isolation evidence.
hideout run --backend native --allow-weak-isolation -- true

# Missing target command must not fall back to host execution.
hideout run --backend lima -- definitely-missing-hideout-command
```

Expected outcome:

- Unsafe workspace is rejected before command execution.
- Native run is labeled wiring-only and does not satisfy the feature.
- Missing command reports backend context and no fallback.

## Not Covered By This Feature

- Release-candidate evidence bundles.
- Guided first-run setup for new users.
- Full TUI/WebUI observation.
- Product-specific real-agent auth or prompts.
- Guest-to-host host-control capabilities.
