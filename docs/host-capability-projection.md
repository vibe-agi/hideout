# Host Capability Projection

<!-- markdownlint-disable MD013 -->

Host Capability Projection lets a CLI inside the isolated guest invoke commands
it already knows — `code .`, and later `open .`, `cursor .` — even though those
commands do not exist in the guest. The command is transformed into a typed
intent, brokered to the host, and executed there as a typed, audited,
fail-closed capability. The guest never learns the host absolute path or the
host username.

This is the reconciliation of strong isolation with a native-feeling developer
experience: the guest is a real VM (separate kernel), so it starts without the
host's tools; projection makes the authorized host actions "just work" through
typed brokered routes rather than by weakening isolation.

The built-in 030 `code` projection and the 032 community recipe lifecycle are
implemented. The latter adds local and exact-commit intake plus lifecycle
management without adding a new Core host effect. Feature 043 retains one clean
exact-package Gate 2 covering first-attempt readiness and the distinct
030/032/039 real flows. Clean alias privacy still requires a matching Gate 3.

## Layers

```text
guest: code .
  -> immutable command binding      (pack/revision/app/grammar/capability digest)
  -> declarative command grammar    (zero authority: parse argv -> propose intent)
  -> UnboundOpenResourceIntent      ({resources[], location, windowMode})
  -> Go re-decodes + binds app/kind (current session binding + resource mapping)
  -> host.app.open-resource provider (Core capability)
  -> observed application identity   (on first use; Core signing/ownership)
  -> generic argv renderer           (internal/hostcap/appopen)
  -> host application (code)         (Core execs the real CLI)
```

- **Command grammar / adapter** (`internal/cmdgrammar`, or a separate goja
  adapter ABI) is a zero-authority transformer: it parses guest argv into a
  proposal and never touches a host path or binary. The built-in 030 path uses
  a declarative grammar. A 032 host-app pack accepts only declarative
  `open-resource-v1`; it cannot carry JavaScript.
- **Capability registry** (`internal/hostcap`) is a static, Core-owned set of
  `CapabilityDescriptor`s. It is not runtime-extensible: new authority is a
  reviewed code change, never a runtime plugin.
- **Built-in host-app pack**
  (`internal/hostcap/recipes/builtin-vscode.json`) uses the same manifest,
  immutable binding, application identity, grammar, and provider path as an
  external pack. Ambient `PATH` is never an application identity source. Core
  safety effects remain separate reviewed data in `safety-profiles.json`.
- **host.app.open-resource provider** is the Core Go capability that resolves the
  app, maps each workspace resource to a host path (reusing the `host.open`
  workspace resolution and symlink-escape recheck), enforces the mode, renders
  argv generically, and launches. Result policy is `none`: nothing returns to the
  guest.

## Where JavaScript fits (and where it must not)

- Existing JS belongs only to the separate **command adapter** layer (parse argv
  to a proposal). It carries zero authority; Go re-validates every field.
- JS must **not** implement a launcher, supply a host binary path, or write the
  app recipe. Host identity (which binary a stable id resolves to, and how its
  argv is rendered) is Core/package-owned data; if untrusted JS could control
  it, that would be arbitrary host execution.
- A 032 community host-app pack has **no JavaScript authority at all**. It can
  bind only the bounded declarative grammar to the existing Go-owned
  `host.app.open-resource` provider. New effects remain Core work.

## Community Recipe Extension (032)

The 032 target replaces guest-selectable app identity with an immutable binding
compiled at run start. Default-safe bindings lock package, command, grammar,
permission, expected app family, and Core safety-profile identity without
touching the host application. Core observes and caches the exact application
identity only when that command is first invoked, then revalidates it at every
launch boundary. `ask-each-run` bindings remain eagerly observed because the
operator decision is bound to the exact observed identity:

```text
command -> exact pack revision -> binding -> declarative grammar
        -> host.app.open-resource -> qualified app -> access/safety
        -> profile/session/environment
```

Operators may intake only a local directory or an exact 40-hex Git commit. Core
copies a bounded regular-file snapshot into private storage; inspect, validate,
test, and install-only add remain authority-free. Confirmed add may atomically
store and enable an exact revision, while explicit enable binds one exact
revision, fingerprint, binding set, access choice, and profile. Update creates a
new revision and permission diff. Disable affects future compilation, and
remove deletes only owned snapshots after disabling bindings and retaining
audit. Enablement never hot-injects an old session; its effect starts with a new
run.

`safe` is available only when command-time Core observation finds a compatible
signed app and selects the exact named, versioned, Core-owned safety profile
locked into the run binding. That profile checks argv, settings, and state
together. A pack can request the profile but cannot define or attest it. An
unsigned app stays visibly `unverified-app`, is bound to an exact Core-computed
bundle-tree digest, and uses `ask-each-run`. Package signing requirements,
package tests, or a self-signed bundle do not manufacture a verified identity.

Workspace resources come from the current session's immutable workspace
attachment. Under the promoted 035 contract, Core first verifies that the attachment's
workspace ID, captured root identity, logical root, physical view, session, and
backend incarnation still match. It then resolves the structured relative path
through that attachment. Environment history, the current process directory,
display labels, guest-supplied app references, and independent path hashing are
not authority. Two sessions may both name `/workspace/same.go`; each resolves
only through its own attachment, and a mismatch fails before host effect.

A HostFS resource must already have active same-session content authority;
discover-only `see`/`see-dir`/`see-tree` visibility is not enough. The recipe,
guest, event stream, and public evidence never receive the resolved host path.
There is no generic host exec, raw argv, result stream, persistent profile
allowance, JavaScript pack grammar, or marketplace signing claim.

See [host-app-recipes.md](host-app-recipes.md) for the operator/contributor
lifecycle and its explicit CLI boundary.

## Invariants

1. **No generic fallback.** A projected command that is unbound, whose provider
   is unavailable, whose flag is unknown, whose path has no host mapping, or
   whose app is absent fails closed with a typed recovery code. It never
   delegates to host execution or to a real same-named guest binary the shim
   shadows.
2. **Host identity only from Core.** The immutable command binding selects the
   qualified app revision; Core resolves it under fixed application roots and
   independently observes signing/ownership facts. Guest intent cannot carry
   an app id, binary path, bundle id, or script source.
3. **Go re-validates every intent.** The grammar/adapter proposes; Go strictly
   decodes (unknown fields rejected) and field-validates before the provider
   acts. Raw argv never reaches the host.
4. **Typed result channel.** Each capability declares a result policy;
   `host.app.open-resource` is `none`.
5. **Validate only the selected host effect.** Starting a run or executing an
   unrelated guest command does not inspect optional host applications. A
   valid projected command resolves its selected app on first use and every
   actual launch still performs final identity and resource revalidation.

## Safe and trusted host-app modes

- **safe** (default): the host app opens with a run-scoped, profile-owned
  isolated user-data directory,
  extensions disabled, workspace auto-tasks not run, and Workspace Trust left
  enabled. It never uses `--disable-workspace-trust`. A later run does not inherit
  a prior run's workspace-trust state, and the package-owned safe configuration
  disables automatic tasks even within the current run.
- **trusted**: uses the operator's normal host-app configuration. It requires an
  explicit operator grant created on the host with `hideout allow host-app
  <command>` from inside the project directory, and is denied without one. The
  grant is a durable per-profile, per-workspace policy keyed by a Core-derived
  workspace identity plus the resolved app binding digest, stored only in
  guest-unreachable control-plane state and never in the workspace. Because it
  persists, a one-shot `code .` reuses it with no live approval window — the gap
  that made trusted mode unusable for one-shot commands before. It is revocable
  with `hideout deny host-app <command>`, and a change to the workspace or app
  identity re-closes it until the operator re-affirms. A denied trusted request
  does not silently launch safe mode; selecting `safe`
  (`hideout profile host-app-mode <p> safe`) both returns to safe opens and drops
  every trusted grant for the profile.

Hideout does not claim to protect the host app from a malicious workspace;
Workspace Trust remains the editor's mechanism. Hideout disarms the obvious
auto-execution vectors by default and records that a guest-writable workspace was
opened in a host application.

## Privacy: workspace path and identity

Privacy and hardened profiles default the workspace to alias mode: the guest sees
`/workspace`, and Core alone knows the host path. This is the same one-directional
invariant that powers projection — the host path lives only in Core — and it also
means the host username and host path shape are not synthesized into the guest's
default workspace path, identity environment, generated Git identity, or verified
guest-visible mount metadata. See `docs/threat-model.md` for the scoped positive
claim and its adjacent non-claim.

## Design-ready (not implemented in v1)

The registry accommodates, but v1 does not implement: adb / device services
(`host.service.bridge`), AppleScript templates (`host.automation.invoke`), and
result-streaming capabilities. These are present as `design-ready` descriptors
and fail closed if dispatched. Multi-capability proposals (e.g. an open that also
requests a port mapping) are also design-ready.

## Evidence

Gate 0 proves the mechanics (registry/grammar/intent/no-fallback/redaction). Real
macOS arm64 Lima Gate 2 has proved the guest-visible `code .`/`code -g` open, fail-closed
behavior, safe-mode (auto-tasks did not run), the trusted grant/revoke lifecycle,
and the three-channel username-hiding with per-channel detector self-test and a
preserve-mode positive control. Gate 3 re-verified alias mode together with
proxy-env absence, DoH forward resolution, connected-subnet DNS blocking, and
enforced privilege separation.

Feature 043 now retains clean exact-package Gate 2 evidence under
`.hideout-release-evidence/043-projection-readiness-real-gate2/`. The strict
production evaluator accepts the complete-catalog first-attempt readiness,
built-in safe projection, external pack, and durable grant/revoke artifacts.
The raw run passed 10 fresh and 30 warm first attempts, one concurrent
disjoint-catalog pair, and pre-commit cancellation with zero retry, fallback,
timeout, unauthorized host effect, or cross-session access.

No matching clean Gate 3 was produced. The retained 043 artifact therefore
records `privacy.status=not-promoted`. The older 2026-07-11 dirty Gate 3 remains
useful real-backend engineering evidence, but it is not clean privacy
provenance and cannot be substituted into the newer candidate.

030 evidence does not validate 032 by implication. Community recipe completion
separately requires artifact-backed `032.host-app-pack.gate0.*` evidence and
`032.host-app-pack.real-gate2.external` from an externally installed pack; the
043 clean Gate 2 contains that distinct proof. Native, local-only, embedded,
package-self-test, static-source, and `not-run` evidence cannot replace it.
