# First-Run Alpha Path

<!-- markdownlint-disable MD013 -->

This page is the canonical first 15 minutes for an external alpha operator.
It assumes a release-like package install and a dedicated project workspace.
It does not create new security claims; current claims and non-claims remain in
[STATUS.md](STATUS.md), [threat-model.md](threat-model.md), and
[support-matrix.md](support-matrix.md).

## Preconditions

- Use macOS arm64 with Lima for the first-class alpha path.
- Use a dedicated project checkout, not `$HOME`, `~/.hideout`, browser profile
  directories, SSH credential roots, or the Hideout store.
- Provide a local proxy only when using the privacy path.
- Treat `--backend native` as a weak development harness, not isolation.

## Install And Verify

Extract the package and install with the packaged binary:

```bash
tar -xzf hideout-<platform>.tar.gz
cd hideout
./install.sh --skip-init
export PATH="$HOME/.local/bin:$PATH"
hideout version
hideout package verify "$HOME/.local"
hideout doctor
```

If package verification reports obsolete package-owned leftovers, inspect first
and then explicitly repair:

```bash
hideout package repair --prefix "$HOME/.local" --dry-run
hideout package repair --prefix "$HOME/.local"
```

If `tun2socks` is missing, install it as an external prerequisite. The alpha
package verifies Hideout-owned helpers, but does not checksum an external
`tun2socks` binary.

## Recommended Privacy Profile

Use the privacy template on Lima for the primary path:

```bash
export HIDEOUT_SECRET_PROXY_URL=socks5://host.lima.internal:7890
hideout init \
  --template privacy \
  --profile default \
  --backend lima \
  --network tun2socks \
  --proxy-secret proxy-url \
  --mediated-resolver 1.1.1.1 \
  --runtime developer-standard \
  --no-input
hideout doctor --profile default --backend lima --level deep
```

This path expects `tun2socks`, a proxy secret, and a mediated resolver. If those
are unavailable, `doctor` should report observed facts and next actions. Real
network privacy proof still requires Gate 3 evidence; local doctor output is not
a replacement for that gate.

`developer-standard` is an explicit preview selection. It does not change old
profiles or the built-in image default. Inspect its exact revision, digest,
size, source, SBOM status, and command contract before first boot:

```bash
hideout runtime inspect developer-standard
```

## First Run

Move into a dedicated workspace and run a simple command:

```bash
cd /path/to/sanitized/project
hideout run --profile default --backend lima -- pwd
hideout audit show --limit 20
```

## Install The Tested Agent CLI

Install the pinned evidence package into the durable target-owned prefix. This
runs inside the selected guest and requires neither `sudo` nor a host-global npm
prefix:

```bash
hideout run --profile default -- sh -eu -c '
  rm -rf "$HOME/.npm" "$HOME/.local/lib/node_modules/@openai/codex" "$HOME/.local/bin/codex"
  npm install --global --prefix "$HOME/.local" @openai/codex@0.144.1
  "$HOME/.local/bin/codex" --version
'
```

The privacy evidence lane proves the registry DNS and HTTPS request crosses the
existing DoH/tun2socks path and that proxy credentials stay out of target and
public evidence. Package installation does not log in. Interactive login,
browser callbacks, and durable agent authentication are explicitly outside
031.

If the command needs a host file outside the workspace, grant read access
explicitly:

```bash
hideout run --profile default --fs read:/absolute/file -- <cli>
```

If the CLI first needs to navigate names without reading file contents, use an
explicit discover rule:

```bash
hideout run --profile default --fs see-dir:/absolute/directory -- <cli>
```

The CLI sees complete immediate names and coarse kinds, but locked reads return
`EACCES`. An eligible exact-file request appears in `hideout decision list
--kind hostfs.read`; after a local operator claims and approves it, the same
running CLI retries the read. Visible names are user data and may enter model
context. `see*` does not grant content, writes, execution, or symlink targets.

Onboarding keeps visibility at `none` unless selected explicitly. For example,
`--hostfs-visibility landmarks` creates reviewed one-level roots. Broad
`--hostfs-visibility home-tree` additionally requires
`--acknowledge-name-disclosure`; do not enable it merely to avoid individual
grants.

macOS protected directories can show a TCC prompt when HostFS first enumerates
the selected root. Hideout cannot perform a truthful silent preflight. To probe
one root deliberately, use a command that warns before touching it:

```bash
hideout doctor --feature hostfs --probe-hostfs-root /absolute/directory
```

If the command needs to write through HostFS, stage the write with an overlay
grant and then apply the local decision explicitly:

```bash
hideout run --profile default --fs overlay-dir:/absolute/directory -- <cli>
hideout hostfs write status
hideout decision list
```

HostFS write apply changes host lower files only after an authenticated local
operator claims and applies the decision. Workspace writes are intentionally
visible to the target and are audited rather than blocked.

## Open In A Host Editor

With a supported, code-signed Visual Studio Code installation, a CLI running
inside the Lima guest can open the mapped host workspace without learning its
host path:

```bash
hideout doctor --profile default --feature projection --level deep
hideout run --profile default --backend lima -- code .
hideout run --profile default --backend lima -- code -g src/main.go:12:3
```

The default safe mode uses run-scoped VS Code state, disables extensions and
automatic workspace tasks, and returns no host data to the guest. A trusted IDE
request is separate from authority: the command fails closed until a local
operator claims and approves the resulting `host-app.open-resource` decision for
that live run. Revocation makes the next trusted launch fail; selecting safe
mode explicitly restores the default path. Hideout does not pass through raw
guest argv, resolve `code` from ambient `PATH`, or fall back to generic host
execution.

## Operator Console

Start the local daemon when you want a live local console:

```bash
hideout daemon start
hideout tui --profile default
hideout ui --no-open --print-url
```

The TUI and WebUI organize existing Manager state: action-required counts,
HostFS write decisions, generic decisions, notices, background work, stream
health, and explicit doctor/package/support commands. They do not add authority
or auto-run doctor.

## Fast Development Harness

For local development of Hideout itself, `native` can be useful:

```bash
hideout init \
  --template dev \
  --profile dev \
  --backend native \
  --network direct \
  --no-input
hideout run --profile dev --backend native -- pwd
```

This is a weak harness only. Do not use native output as isolation, DNS, HostFS,
or privilege-separation evidence.

## When Stuck

Use doctor first:

```bash
hideout doctor --level deep
hideout doctor --feature packaging --level deep
hideout doctor --feature dns --level deep
hideout doctor --feature hostfs --level deep
hideout doctor --feature privilege --level deep
hideout doctor --fix --dry-run
```

Doctor reports local observed facts, candidate causes, next actions, and
gate-required markers. It should not claim release readiness or replace real
Gate 2/Gate 3 proof.

Common failure hints:

- missing helper: run `hideout package verify "$HOME/.local"` and inspect the
  named helper.
- missing `tun2socks` (`package.prerequisite.missing`): install or expose the
  external prerequisite, then rerun `doctor --feature packaging`.
- native backend warning: switch to Lima for isolation evidence.
- hardened native refusal: use a non-native backend with enforced-capable
  privilege separation, or create an explicit degraded fallback profile.
- missing proxy secret (`init.proxy-secret.missing`) or resolver
  (`init.mediated-resolver.missing`): set `HIDEOUT_SECRET_PROXY_URL` and rerun
  `hideout init` or `doctor --feature dns`.
- degraded privilege posture (`privilege.status.degraded`): recreate with an
  enforced-capable image or keep the profile explicitly degraded.
- stale package install (`package.obsolete-leftover`): run
  `hideout package repair --prefix "$HOME/.local" --dry-run` before applying
  repair.
- missing release gate evidence (`release.gate-evidence.missing`) or stale
  supporting evidence (`release.evidence.stale`): rerun the named gate or
  product-hardening proof on the exact candidate commit.

The registry behind these stable codes is inspectable with
`hideout support recovery-codes --json`.

## First-Run E2E Proof Modes

The local Gate 0 proof uses the packaged installer and installed binary, but it
does not claim Lima/privacy isolation:

```bash
scripts/test-first-run-e2e.sh --local-fast --out /tmp/hideout-first-run
```

This local-fast lane installs with `--skip-init`, verifies the package,
initializes a weak/dev native profile once, runs one installed-binary command,
and writes `hideout.product-hardening-evidence/v1` with audit and Boundary
references.

Real backend first-run proof is explicit and prerequisite-gated:

```bash
scripts/test-first-run-e2e.sh --real-backend --out /tmp/hideout-first-run-real
scripts/test-first-run-e2e.sh --real-backend --require-real --out /tmp/hideout-first-run-real
```

When Lima/privacy prerequisites are missing, the first command records
`not-run` evidence; `--require-real` turns that into a non-zero result. A pass
in real-backend mode must use the Lima/privacy path and must not fall back to
native.

## Alpha Readiness Reminder

Completing this page does not by itself make a release candidate. Release
readiness requires the support/readiness gate and real Gate 2/Gate 3 evidence
for the exact release commit and artifact.
