# Contract: Host-App Mode & `trusted-host-app` Grant

<!-- markdownlint-disable MD013 MD060 -->

## Safe mode (default)

- Launch parameters (VS Code, darwin): isolated `--user-data-dir` under a Hideout-owned path (or a dedicated Hideout VS Code profile), `--disable-extensions`, workspace auto-tasks not run, Workspace Trust enabled. **Never** `--disable-workspace-trust`.
- `DecisionPolicy = default-allow-audited`: opens without a per-invocation prompt; every open is audited (`ide.open`, mode `safe`).
- Behavioral guarantee (test): a workspace `.vscode/tasks.json` with `runOn: folderOpen` that would write a host marker does NOT write it in safe mode.

## `trusted-host-app` mode

- Uses the operator's normal VS Code configuration.
- `DecisionPolicy = operator-grant`: requires an explicit grant obtained through the existing operator decision center. Without a live grant → `projection.mode.trusted-denied`, with no host launch and no silent safe-mode substitution.
- The grant is a decision-center record (claim/approve/revoke lifecycle already exists): visible, revocable, bound to session/profile/subject.
- Persistence: the mode selection and grant reference live only in guest-unreachable control-plane state (profile/decision store under `~/.hideout`), keyed by workspace/profile identity. Never read from or written to the guest-writable workspace.
- Invalidation: revoke → next launch denied while trusted remains requested. The operator must explicitly select safe mode or obtain a new run-bound grant. Profile or environment identity change invalidates the grant or requires re-affirmation. Target retries can never grant or flip the mode.

## Grant flow (reuses decision center)

```text
operator: request trusted-host-app for profile P
  -> decision center creates a grant decision (kind: host-app.trusted-host-app)
  -> operator approves -> HostAppMode(P) = trusted-host-app, GrantRef set
  -> code .  -> opens with operator config, ide.open audit mode=trusted-host-app
operator: revoke
  -> GrantRef invalidated; requested HostAppMode(P) remains trusted-host-app
  -> next code .  -> denied
operator: select safe
  -> HostAppMode(P) = safe
  -> next code .  -> safe mode
```

## Non-claim (docs)

- Hideout does not claim to protect the host IDE from a malicious workspace; Workspace Trust remains VS Code's mechanism. Hideout disarms the obvious auto-execution vectors by default (safe mode) and records, in evidence, that a guest-writable workspace was opened in a host application.

## Test obligations

- Trusted denied without grant; granted → operator config; revoke → denied; explicit safe selection → safe launch.
- Mode/grant state is not influenced by anything the guest writes into the workspace.
- Safe-mode folder-open task marker never written; `--disable-workspace-trust` never used.
