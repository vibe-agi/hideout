# Runtime Verification Contract

<!-- markdownlint-disable MD013 MD060 -->

## Execution Order

For a catalog-selected Lima environment, target execution is:

```text
environment lock
  -> prepare/start exact pinned image
  -> observe active package-inventory build identity with fixed direct argv
  -> observe target privilege separation
  -> execute bounded image-owned runtime contract against actual target environment
  -> persist receipt and emit audit/status
  -> existing network/bootstrap and HostFS setup
  -> existing exact target-command check
  -> execute target command
```

A boundary observation failure stops before network/HostFS setup, the exact
command check, and target. Runtime observations cover image-owned commands;
package/session helpers copied by Hideout remain under their existing setup
validators. This prevents setup failure from hiding a missing image
prerequisite.
A baseline observation failure records and surfaces degradation, then continues
to the exact command check. The exact command check remains authoritative.

## Backend Input

Manager supplies a validated contract and sink through backend session state.
The backend receives no catalog parser, package-manager instructions, remote
URLs, repair commands, or credential.

Each observation executes as direct argv under the same sanitized target env,
guest home, PATH, user, and workspace identity used by the target. The backend
must not use the setup/root identity for runtime observations.

## Probe Bounds

- one overall 20-second timeout on a warm guest;
- at most 64 observations;
- at most four args per version probe;
- stdout and stderr combined are capped at 4 KiB per process;
- persisted output is UTF-8/control-stripped and capped at 512 bytes;
- no stdin, TTY, network request, shell source, background process, or retry;
- cancellation terminates probe processes and writes no passing receipt.

The implementation may batch existence checks into one fixed Core-owned shell
program, but catalog values are positional data and never interpolated as
source. Version commands execute directly.

## Classification

### Boundary

Missing or mismatched boundary observation:

- receipt status `preview-failed`;
- stable `runtime.boundary.missing` recovery;
- target command does not run;
- environment record remains, but no ready claim is written;
- cleanup remains ordered.

### Baseline

Missing or mismatched baseline observation:

- receipt status `preview-failed`;
- `runtime.baseline.missing` recovery and visible warning;
- an unrelated present exact target command may run;
- preview-ready is not restored until a later real observation passes.

### Exact Target Command

The existing backend check runs after runtime verification. Its
`CommandNotFoundError` maps to `runtime.command.missing` only for a
catalog-selected environment; custom images retain generic missing-command
wording and no false package suggestion.

## Receipt Integrity

Manager, not the backend, writes the receipt atomically. Before write it checks:

- current environment lock and ownership;
- environment ID/image/provenance equal the observation context;
- active guest package-inventory digest equals catalog provenance;
- one result per contract observation, no extras/duplicates;
- bounded output and valid statuses;
- backend is Lima for preview claims;
- privilege status is authoritative and from this run.

Malformed, incomplete, canceled, or mismatched observations produce no passing
receipt. A previous receipt may remain as historical context but status becomes
unknown/failed for the current operation.

## Audit And Redaction

Emit one aggregate `runtime.verify` event, not one event per command. Details:

- family/revision/contract digest/artifact digest;
- backend and real-backend boolean;
- status, counts, failed IDs/classes;
- privilege status;
- receipt reference and recovery code.

Do not emit version output, environment values, cache paths, proxy endpoint,
credentials, machine ID, target package contents, or host absolute paths.

## Agent Install Evidence

The real Gate 3 fixture is an operator-style target command, not runtime repair:

```sh
rm -rf "$HOME/.npm" "$HOME/.local/lib/node_modules/@openai/codex" \
  "$HOME/.local/bin/codex"
npm install --global --prefix "$HOME/.local" @openai/codex@0.144.1
"$HOME/.local/bin/codex" --version
```

The gate verifies empty caches before install, exact npm integrity and resulting
version, target ownership, no sudo, mediated DNS/HTTPS evidence, connected-
subnet DNS blocking, and absence of proxy credentials from target/public
evidence. It does not authenticate the agent.

## Failure Evidence

Gate 0 fixtures separately exercise artifact, network-denied, DNS, registry,
unwritable-prefix, boundary-command, baseline-command, and exact-command
failures. A failure fixture must assert no target side effect and the stable
recovery code/next action; success-only output matching is insufficient.
