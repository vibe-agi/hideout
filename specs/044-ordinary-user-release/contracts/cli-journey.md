# CLI Contract: Ordinary User Journey

## Primary help

```text
hideout
hideout help
```

Both commands render the same concise journey and exit 0.

Required content order:

1. setup;
2. run;
3. readiness/repair;
4. connection/privacy posture;
5. audit/support reporting;
6. update/uninstall;
7. expanded help pointer;
8. concise platform/network/maturity boundary.

The first 20 non-blank lines must contain setup, readiness, first run, and the
expanded-help pointer.

## Expanded help

```text
hideout help --all
```

Renders every command currently registered by the CLI, including advanced
profile, environment, package, build-helper, and lab surfaces.

## Contextual help

```text
hideout help setup
hideout setup --help
hideout help run
hideout run --help
hideout help doctor
hideout doctor --help
hideout help privacy
hideout help package
hideout package --help
hideout help support
hideout support --help
```

All exit 0 and perform zero durable writes.

## Doctor presentation

```text
hideout doctor
```

Renders concise human readiness.

```text
hideout doctor --verbose
hideout doctor --level deep
hideout doctor --feature <name>
```

Renders the complete human report.

```text
hideout doctor --format json
```

Renders the complete existing machine-readable report. JSON fields and schema
remain backward compatible.

Default concise output must include:

- overall `Ready`, `Needs attention`, or `Blocked`;
- selected profile/backend and direct/private network wording;
- each user-actionable non-pass finding with reason and next action;
- a runnable first/next command;
- a pointer to `--verbose`.

Default output must not include a repository `scripts/test-*` instruction.

## Package lifecycle guidance

Primary and contextual help must teach:

```text
brew upgrade vibe-agi/tap/hideout
brew reinstall vibe-agi/tap/hideout
brew uninstall vibe-agi/tap/hideout
```

for Homebrew, and:

```text
hideout package verify <prefix>
hideout package repair --prefix <prefix> --dry-run
hideout package uninstall --prefix <prefix> --dry-run
hideout package uninstall --prefix <prefix>
hideout package uninstall --prefix <prefix> --purge
```

for standalone packages. Guidance must not guess a prefix for a destructive
operation.
