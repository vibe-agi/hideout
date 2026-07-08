# Contract: Onboarding Command

<!-- markdownlint-disable MD013 -->

## CLI Surface

`hideout init` accepts template-aware flags:

```text
hideout init \
  --profile <name> \
  --template <privacy|hardened|dev|debug> \
  --backend <lima|native|auto> \
  --network <direct|tun2socks> \
  [--proxy-secret <secret-ref>] \
  [--mediated-resolver <ip>] \
  [--privilege-status <enforced|degraded|unknown>] \
  [--allow-degraded-template] \
  [--no-input] \
  [--dry-run]
```

Existing `--profile`, `--backend`, `--network`, `--proxy-secret`,
`--no-input`, and `--dry-run` behavior remains supported.

## Non-Interactive Rules

With `--no-input`, the command fails before mutation unless all required
choices are explicit:

- `--profile`
- `--template`
- `--backend`
- `--network`

Additional rules:

- `--network tun2socks` requires `--proxy-secret` and
  `--mediated-resolver`.
- `--network direct` rejects `--proxy-secret` and `--mediated-resolver`.
- `--template privacy` and `--template hardened` require
  `--network tun2socks`.
- `--template hardened` requires `--privilege-status enforced`.
- `--template hardened --privilege-status degraded|unknown` requires
  `--allow-degraded-template` to create a degraded fallback.

## Interactive Rules

Without `--no-input`, CLI may recommend defaults and prompt for confirmation.
The review must show:

- recommended template;
- selected backend and network;
- proxy/DNS requirements without secret values;
- HostFS default posture;
- adapter-pack default posture;
- privilege status and hardened requirement;
- warnings and non-claims.

If the operator answers no, cancels, or input ends before confirmation, the
command creates no profile and writes no success evidence summary.

## Manager Parity

Manager `PlanInit` and `ApplyInit` use the same request shape. Any daemon or UI
consumer must call the typed plan/apply operation; no surface may write template
profiles directly.

## Output

Success output includes:

- profile name;
- template id/effective posture;
- evidence path;
- init audit path when available;
- next-step commands.

Failure output includes an actionable reason and, for hardened privilege
failure, recreate/base-image guidance.
