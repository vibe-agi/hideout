# Community Host-App Recipes

<!-- markdownlint-disable MD013 -->

> **Status: Implemented.** The 032 v1 lifecycle is covered by its three
> artifact-backed Gate 0 proofs and an external-pack real macOS arm64 Lima Gate
> 2 proof. The clean exact-package receipt is retained with the 043 readiness
> and 030/039 flow artifacts under
> `.hideout-release-evidence/043-projection-readiness-real-gate2/`.

Community host-app recipes let an operator project a familiar command into a
guest while Core performs one existing, typed host effect:
`host.app.open-resource`. A recipe is bounded data. It is not a plugin, a host
launcher, or a source of authority.

## V1 Boundary

A v1 pack may declare application bundle basenames, a bundle-relative
executable, identity expectations, bounded launch fields, commands, the
declarative `open-resource-v1` grammar, workspace or `hostfs-portal` resource
classes, a requested Core safety profile, and optional quality tests.

A v1 pack cannot provide:

- a capability or provider;
- generic host execution, raw host argv, shell hooks, or automation;
- JavaScript grammar or JavaScript launch authority;
- a host path, executable path outside the resolved bundle, or host result
  stream;
- profile mutation, HostFS authority, or a persistent approval;
- package, publisher, marketplace, or signing verification.

New host effects still require reviewed Core code. The separate adapter-pack
JavaScript ABI does not grant a host-app pack JavaScript authority.

## Source And Snapshot

V1 accepts only these intake forms:

- a local directory; or
- a Git repository pinned to one full 40-hex commit.

Core copies bounded regular files into a private immutable snapshot before
digesting, testing, trusting, enabling, or running the pack. Runtime never reads
the mutable local checkout or Git worktree. Intake rejects escaping symlinks,
special files, submodule recursion, checkout hooks or filters, and package
installation hooks.

The source digest covers installed bytes. A separate permission fingerprint
covers every authority-bearing command, app identity expectation, bundle and
executable selection, launch field, grammar, resource/result class, access
choice, and exact Core safety-profile version. A changed source is a new
revision. A changed permission fingerprint requires a visible diff and fresh
acceptance.

Read-only parsing or planning that fails before Core can form a stable
package/revision identity returns a typed diagnostic without inventing a
persistent lifecycle event. Once that identity exists, applied lifecycle
attempts and launch/refusal outcomes are audited from validated Core facts.

## Operator Lifecycle

| Operation | V1 behavior | Authority effect |
| --- | --- | --- |
| `inspect` | Show the exact source, revision, Core-observed app identity, commands, conflicts, safety posture, access, tests, permission fingerprint, profile scope, and next action. | Read-only. |
| `validate` | Strictly validate a source or exact installed revision against Core schemas and invariants. | Read-only; does not install, trust, or enable. |
| `test` | Run bounded package-authored grammar vectors. | Quality evidence only; cannot certify security or replace Core validation. |
| `add` | Plan source acquisition and review, then reacquire and atomically store the exact snapshot. The ordinary confirmed flow may also test and enable it; `--install-only` stores inert bytes. | No authority before exact review and acceptance. Cancellation or drift leaves no new binding. |
| `enable` | Bind an exact installed revision, binding set, permission fingerprint, access choice, and profile. | Compiled only into future runs. Existing sessions receive no shim or silent restart. |
| `update` | Acquire a new immutable revision and show source and permission differences. | Never moves a binding automatically. Changed permissions suspend inherited trust until accepted. |
| `disable` | Remove a profile binding from future run compilation while retaining installed bytes and audit. | Existing shims remain immutable, but each request rechecks live disabled state and fails closed. |
| `remove` | Disable all owned bindings, prove package ownership, remove only owned snapshots, and retain a tombstone plus audit. | No fallback to host execution or a same-named guest binary. |

Store-wide revoke is an advanced terminal operation on an exact revision. It is
not a synonym for profile-scoped disable or owned-byte removal.

### CLI Shape

The commands below are the implemented v1 operator surface. Mutating commands
show an exact plan before apply and require interactive confirmation or `--yes`.

```bash
# Local source: interactive review before any apply.
hideout app add \
  --path ./my-host-app-pack \
  --profile default \
  --access safe

# Exact-commit source: a full 40-hex commit is mandatory.
hideout app add \
  --git https://example.invalid/community/editor-pack.git \
  --commit <40-hex-commit> \
  --profile default \
  --access ask-each-run

hideout app list
hideout app inspect --profile default <pack-id>

# Contributor checks are read-only and do not install or enable the source.
hideout app validate --path ./my-host-app-pack
hideout app test --path ./my-host-app-pack

# Exact-commit source checks are also read-only.
hideout app validate --git https://example.invalid/community/editor-pack.git --commit <40-hex-commit>
hideout app test --git https://example.invalid/community/editor-pack.git --commit <40-hex-commit>

# Installed immutable revisions can be checked again by identity.
hideout app validate --revision <revision-id> <pack-id>
hideout app test --revision <revision-id> <pack-id>
hideout app enable \
  --profile default \
  --pack <pack-id> \
  --revision <revision-id> \
  --access ask-each-run

# Review and select a new immutable revision for future runs.
hideout app update \
  --path ./my-host-app-pack \
  --pack <pack-id> \
  --profile default \
  --access ask-each-run

hideout app disable --pack <pack-id> --profile default
hideout app revoke --pack <pack-id> --revision <revision-id>
hideout app remove --pack <pack-id>
```

Non-interactive add or enable requires explicit acceptance with `--yes`; add
may also pin `--expected-digest <sha256>`. The exact accepted plan, not `--yes`
by itself, defines the authority.

## Access And App Identity

The operator selects one v1 access posture for an exact binding:

- `safe` is available only when Core independently observes a compatible signed
  app identity and selects a named, versioned, Core-owned safety profile. Core
  builds and validates the combined argv, settings, and run-scoped state.
- `ask-each-run` uses a visible default-deny decision bound to the exact
  capability, qualified app, pack revision, binding, command, session, profile,
  workspace, environment, resource class, and observed identity.
- an explicitly trusted unsigned app remains `unverified-app`, is bound to a
  Core-computed exact bundle-tree digest, always uses `ask-each-run`, and needs
  re-trust after change.

A pack may request a safety profile or narrow an independently observed app
identity. It cannot define safe behavior or authenticate the app. A package
requirement, declared Team ID, declared bundle ID, package test, or self-signed
bundle does not become `verified` merely because the package says so.

## HostFS Resources

A recipe can consume only authority that HostFS already granted to the same
live session. Core derives the active portal and resource kind, requires
sufficient content/tree authority for the exact resource, and reauthorizes it
immediately before launch. `see`, `see-dir`, `see-tree`, or other discover-only
visibility is insufficient to open content.

The guest, recipe, parser, decision preview, response, and public evidence never
receive the resolved host path. Revoking or ending the HostFS authority makes a
retry fail; the app decision cannot preserve or widen it.

## Contributor Lifecycle

The scaffold command is part of the implemented contributor surface:

```bash
hideout app init \
  --dir ./editor-pack \
  --id community.editor \
  --app-id editor \
  --command editor \
  --bundle Editor.app \
  --executable Contents/MacOS/Editor
```

After scaffolding, a contributor should:

1. Keep `hideout.host-app-pack.json` declarative and within the v1 schema.
2. Add bounded parser vectors that assert unbound resource, location, and
   window output only.
3. Validate locally without treating validation or package tests as trust.
4. Have an operator inspect and add the local snapshot explicitly.
5. Publish a Git source by immutable commit, then have the operator review that
   exact commit and digest.
6. Treat every release as a new revision and make permission changes obvious.

There is no v1 registry, marketplace, publisher signing, namespace ownership,
remote revocation service, or marketplace review claim.

## Session Scope And Failure

Enablement changes future runs only. An already-running session is unchanged and
receives no hot-injected command. A subsequent run compiles immutable command to
pack, binding, grammar, app, access, profile, session, and environment facts.

Unknown, conflicting, absent, unsafe, unverified without acceptance, drifted,
disabled, revoked, stale, or unowned bindings fail before a host effect. A
projected command never falls through to generic host execution or a shadowed
guest command.

## Evidence Boundary

The registered 032 proof IDs are:

- `032.host-app-pack.gate0.lifecycle`;
- `032.host-app-pack.gate0.binding`;
- `032.host-app-pack.gate0.identity-safety`;
- `032.host-app-pack.real-gate2.external`.

These IDs back the current v1 claim. The first three are artifact-backed Gate 0
proofs. The final proof records an externally installed pack reaching the
existing generic host-app effect in real macOS arm64 Lima while proving scope,
HostFS authority, lifecycle, no fallback, and redaction. Its clean exact-package
manifest and flow artifact are retained under
`.hideout-release-evidence/043-projection-readiness-real-gate2/`. Native runs,
local-only fixtures, embedded recipes, static source inspection, package
self-tests, and `not-run` records still cannot satisfy that real Gate 2 proof.
