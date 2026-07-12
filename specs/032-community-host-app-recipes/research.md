# Research: Community Host-App Recipes

<!-- markdownlint-disable MD013 MD060 -->

## Decision 1: Keep Host-App Packs Separate From Adapter Packs

**Decision**: Create a distinct host-app pack manifest, registry, trust record,
and enablement lifecycle. Extract only immutable source acquisition and digest
primitives into an authority-free shared package.

**Rationale**: Adapter packs contain constrained JavaScript that can only return
validated proposals; their runtime resolver returns script source and allowed
proposal capabilities (`internal/adapterpack/registry.go:226-270`). Host-app
packs select a real host application and launch recipe, so installing and
enabling them changes a host escape. Sharing one manifest/state model would
blur zero-authority script data with host authority.

**Alternatives considered**:

- Add app fields to `hideout.adapter-pack/v1`: rejected because it turns an
  authority-free ecosystem artifact into a host-effect package.
- Duplicate source acquisition: rejected because 011 already hardened exact
  git checkout, config/filter isolation, symlink refusal, and immutable tree
  digest (`internal/adapterpack/source.go:19-250`).

## Decision 2: Snapshot Before Trust And Re-Snapshot At Apply

**Decision**: Planning reads a bounded source and computes a candidate digest
without persistent authority. Apply reacquires it, requires the same digest and
permission fingerprint, then atomically publishes the private snapshot and
enablement under the store lock. Runtime reads only that snapshot.

**Rationale**: Existing adapter pack install snapshots to staging before an
atomic rename (`internal/adapterpack/registry.go:122-164`), but a local source
can change between review and apply. Reacquisition plus equality closes that
window without giving a plan durable side effects. Exact-commit git remains
deterministic; local drift fails closed.

**Alternatives considered**:

- Trust the original directory: rejected due to source-to-runtime TOCTOU.
- Retain hidden plan temp state: rejected because plan becomes mutation and
  introduces cleanup/lease authority.

## Decision 3: Community Apps Supply Basenames, Core Owns Roots

**Decision**: Community app declarations contain bounded bundle basenames and a
clean executable-relative path. Core expands only beneath `/Applications`,
`/System/Applications`, and the current operator's `$HOME/Applications`, then
rechecks canonical containment, ownership, ancestor write posture, and overlap
with guest-writable/control paths immediately before launch.

**Rationale**: The 030 registry accepts package-owned absolute candidates
because only reviewed embedded data can reach it (`internal/hostcap/appregistry.go:18-44,102-124`). Making those fields community-controlled would let a recipe
name a guest-written app. The root and ownership checks preserve the original
Core-owned identity invariant.

**Alternatives considered**:

- Accept arbitrary absolute paths after one prompt: rejected because a guest
  can write the named executable and convert open-resource into host execution.
- Search ambient PATH: rejected because PATH is mutable identity, not an app
  trust anchor.

## Decision 4: Observe Identity Independently; Allow Explicit Unverified Apps

**Decision**: Core queries the host signing identity and records the observed
Team ID, bundle ID, code identity, canonical path class, and digest. Package
expectations may only narrow those facts. A valid unsigned app can be accepted
only through a separate exact-digest `unverified-app` trust state and always
uses elevated run-scoped approval.

**Rationale**: Current 030 verification runs a package-owned designated
requirement (`internal/hostcap/appregistry.go:127-134,190-227`). If a community
package supplies both app bytes and requirement, the check self-attests. The
project deliberately permits operator trust comparable to local package
ecosystems, so an honest unverified state is preferable to pretending unsigned
material is verified.

Unsigned trust uses a Core-owned `bundle-tree-v1` digest, not a package hash or
path timestamp. Descriptor-relative traversal authenticates normalized paths,
entry types, permission bits, regular-file bytes, and contained link targets;
unsupported entries, out-of-bundle links, configured limits, or concurrent
mutation fail closed. This is intentionally more expensive than signed identity
observation and is outside the normal local inspection latency budget.

**Alternatives considered**:

- Mandatory signed apps: rejected for local/private alpha usability.
- Package requirement as identity: rejected as self-assertion.
- Unsigned app in safe mode: rejected because Core cannot bind it to a reviewed
  app family with stable identity.

## Decision 5: Safe Is A Core-Owned Effect Profile

**Decision**: Move safe launch posture into named, versioned Core safety
profiles compatible with observed app identities. A profile owns forbidden and
required argv, isolated state layout, allowed/required settings, and pre-launch
verification as one effect-level contract.

**Rationale**: Current hard-forbidden checks inspect argv
(`internal/hostcap/appopen/render.go:25-31,119-132`), while recipe safe settings
are written separately. Community-controlled settings could reproduce a
forbidden effect without its flag. Combined validation preserves safe meaning.

**Alternatives considered**:

- Let each package declare its safe settings: rejected because `safe` becomes
  package marketing rather than an invariant.
- Reject apps without a known safety profile: rejected because explicit
  ask-each-run trust remains useful and honest.

## Decision 6: V1 Uses One Declarative Grammar, No JavaScript

**Decision**: Generalize the existing code grammar into
`open-resource-v1`: one resource, optional file/line/column, new/reuse window,
bounded aliases, and unknown-flag denial. Parser output is unbound resource,
location, and window data only.

**Rationale**: The existing grammar already proves the common editor shape and
is strictly decoded. Adding Goja in the same slice adds another untrusted ABI
before immutable binding and identity are stable. Community adapters remain
available for authority-free command intent but do not route this host effect
in v1.

**Alternatives considered**:

- Keep `ParseCode`: rejected because every app needs a production branch.
- V1 Goja parser: deferred; it can later return only the same unbound fields.

## Decision 7: Remove App Identity From Guest Intent

**Decision**: Per-run registration is the sole source of command, package,
revision, binding, grammar, capability, and qualified app identity. The guest
intent schema omits `appRef` and rejects app/binding/capability/result/host-path
overrides and unknown fields. Broker validates command ownership before decode.

**Rationale**: The current intent includes `appRef`
(`schemas/open-resource-intent.schema.json`) and the provider resolves it
directly (`internal/hostcap/openresource.go:86-108`). The normal broker command
paths validate registration (`internal/broker/broker.go:561,671`), but the 030
host-app path was built around one app. Multiple recipes make that omission an
authority crossing.

**Alternatives considered**:

- Compare guest appRef to binding: rejected because an unnecessary attacker-
  controlled authority field remains in the protocol.
- Trust the shim's command name alone: rejected because request metadata must
  also match the session's immutable registration.

## Decision 8: Consume HostFS Authority, Never Create It

**Decision**: Replace the workspace-only resolver with a Core resource resolver
that supports workspace and active HostFS portal references. HostFS resolution
requires same-session ownership and existing content/tree authority, then
re-canonicalizes immediately before launch. Discover-only visibility is denied.

**Rationale**: Current provider accepts only a workspace resolver and workspace
resource (`internal/hostcap/openresource.go:11-17,110-125`; current intent
schema). HostFS already owns path policy, symlink revalidation, reserved roots,
and session-local read/write decisions. Reusing its authoritative decision
avoids a second path policy.

**Alternatives considered**:

- Workspace only: rejected as a poor product experience for mapped resources.
- Let recipe map `/hideout/hostfs` text: rejected because mount spelling is not
  authority and can drift.

## Decision 9: Safe Or Ask-Each-Run Only

**Decision**: V1 enablement records either `safe` for a compatible Core safety
profile or `ask-each-run`. Elevated approval uses the existing decision center
but extends identity to app/package/binding/command/workspace/environment/run.
Persistent profile allowance is deferred.

**Rationale**: Current trusted IDE decision binds session/profile/workspace
facts (`internal/manager/hostcap_projection.go:210-275`) but assumes one app and
command. Extending that record is smaller and safer than inventing a durable
policy in the same feature.

**Alternatives considered**:

- Always prompt every safe launch: rejected as unnecessary friction when Core
  can enforce the reviewed profile.
- Always allow after package trust: rejected because package trust is not app
  launch authority.

## Decision 10: Fingerprint Authority, Digest Everything

**Decision**: Source digest covers every installed byte. A separate canonical
permission fingerprint covers commands, qualified identities, app roots/names,
executable path, identity expectations, launch syntax, safety profile/version,
resource classes, grammar, result and access policy, and return declaration.

**Rationale**: Source changes in docs/tests should remain visible without
claiming an authority change; conversely, a one-field launch or resource change
must force reacceptance even if package prose hides it.

**Alternatives considered**:

- Source digest only: safe but gives noisy permission prompts for docs.
- Package semantic version: attacker-controlled and unrelated to authority.

## Decision 11: One Low-Friction Add Plan/Apply

**Decision**: `hideout app add <source> --profile <name>` is the ordinary path.
Plan returns Core-derived source, identity, commands, shadowing, resource,
return, safety, and permission-diff facts. After confirmation, one apply stores
the exact snapshot and enablement atomically. `--install-only` and explicit
enable remain advanced paths.

**Rationale**: The user asked for npm/Homebrew-like trust ergonomics rather than
a central review bureaucracy. Separating stored bytes from authority remains an
internal invariant; it need not force two ordinary commands.

**Alternatives considered**:

- Mandatory install then enable commands: rejected as product friction.
- Enable during unreviewed download: rejected because cancellation could leave
  authority the operator never accepted.

## Decision 12: Built-In And Community Must Share Runtime Proof

**Decision**: Migrate VS Code into a built-in pack and Core safety profile,
remove app-specific generic branches, and require a real external test pack in
Gate 2. Local package tests are quality evidence only.

**Rationale**: Current run setup explicitly calls `CodeRegistration`, uses a
`vscode-user-data` path, and resolves one requested mode
(`internal/manager/run_dataplane.go:85-126`). Keeping that as a compatibility
path would let community tests pass while production still depends on the
special case.

**Alternatives considered**:

- Keep built-in special path plus community catalog: rejected as two authority
  systems and a false genericity proof.
- Embedded-only Gate 2 fixture: rejected because it does not prove external
  install/trust/enable/runtime resolution.
