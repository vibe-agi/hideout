# Contract: Operator Install And Support

## Public Install Journey

The public release page presents:

```bash
curl -fLO <versioned-package-url>
curl -fLO <versioned-SHA256SUMS-url>
grep 'hideout-v0.1.0-alpha.1-darwin-arm64.tar.gz' SHA256SUMS \
  | shasum -a 256 -c -
tar -xzf hideout-v0.1.0-alpha.1-darwin-arm64.tar.gz
cd hideout
./install.sh --prefix "$HOME/.local" --store "$HOME/.hideout" --skip-init
PATH="$HOME/.local/bin:$PATH" hideout version
PATH="$HOME/.local/bin:$PATH" hideout package verify "$HOME/.local"
PATH="$HOME/.local/bin:$PATH" hideout doctor --backend lima
```

The docs use the exact versioned URL from the published inventory, never
`latest`, a rolling source build, or `curl | sh`.

Installation does not create a profile, start a daemon, install Lima, install
`tun2socks`, or download the runtime.

## First Success

After prerequisites are visible:

```bash
hideout init \
  --template dev \
  --profile first-run \
  --backend lima \
  --network direct \
  --runtime developer-standard \
  --no-input
hideout run --profile first-run -- pwd
```

The operator is told that the retained runtime is a separate approximately
1 GB first-use download. Direct mode demonstrates package/Lima/runtime and does
not claim privacy networking. The stronger privacy path is shown separately
and never silently falls back.

After first success, docs link to the pinned agent installation walkthrough and
safe `code .` host-capability projection walkthrough.

## Recovery

Documented commands use existing typed package authority:

```text
hideout package verify <prefix>
hideout package repair --prefix <prefix> --dry-run
hideout package repair --prefix <prefix>
hideout package uninstall --prefix <prefix> --dry-run
hideout package uninstall --prefix <prefix>
hideout package uninstall --prefix <prefix> --purge
hideout doctor --level deep --feature packaging
hideout support matrix [--json]
```

Normal uninstall preserves the store. Purge is never suggested as the first
repair action. Unsupported downgrade supplies a stable code and export/recreate
guidance before mutation.

## Support Matrix

Human and JSON forms contain the same entries and non-claims. Required new
subjects cover:

- public-alpha package channel;
- developer-standard preview runtime;
- HostFS discoverable namespace;
- host capability projection;
- community host-app recipes;
- macOS signing/notarization status; and
- unsupported Linux/Windows package channels.

## Feedback And Security

Normal issue forms request only:

- full `hideout version` output;
- release version and package SHA-256;
- host OS/architecture and backend;
- recovery code;
- redacted doctor summary; and
- sanitized reproduction steps.

They never request a raw proxy URL, token, full environment dump, unredacted
audit log, or private workspace content.

Evidence sharing reuses:

```bash
hideout doctor --format json --evidence-out doctor-report.json
hideout audit export \
  --source doctor-report \
  --doctor-report doctor-report.json \
  --out hideout-support.json \
  --acknowledge-full-fidelity
```

The UI warns that an export decision may permit user data; deterministic
control-plane redaction is not a promise to remove all workspace content.
Security reports route to GitHub private vulnerability reporting through
`SECURITY.md`, with no bounty or response-time SLA claim.
