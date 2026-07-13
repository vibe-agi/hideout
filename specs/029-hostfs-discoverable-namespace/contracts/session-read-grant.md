# Contract: Session HostFS Read Grant

<!-- markdownlint-disable MD013 -->

## Storage Layout

```text
$HIDEOUT_STORE_ROOT/sessions/<session-id>/hostfs-read/
|-- grants.json
|-- owner.lock
|-- provider.lock
`-- state.json
```

The directory and files are private control-plane state (`0700` directory,
`0600` files), are never mounted into the guest, and are included in ephemeral
session cleanup. Their concrete paths never appear in public evidence.

## Owner Lock

- `StartRunDataPlane` opens `owner.lock` and holds an exclusive advisory lock
  until ordered data-plane close or process exit.
- Approval/reopen performs a nonblocking exclusive lock attempt.
- Lock contention plus matching session metadata proves a live owner for this
  operation.
- Acquiring the lock, missing/unreadable state, invalid session identity, or an
  ambiguous lock error means liveness is unprovable and the operation fails
  closed.
- An endpoint or PID file alone is never liveness proof.

## Provider State

`state.json` has version `hideout.hostfs-read-provider/v1` and is atomically
replaced while `provider.lock` is held exclusively. It records deterministic
request keys, public decision IDs, revisions, terminal memory, and the rolling
creation timestamps required for limits. It contains no content or token.

Broker grant readers take a shared provider lock. Manager create, apply, deny,
timeout, and reopen take an exclusive provider lock.

## Grant Manifest

```json
{
  "version": "hideout.hostfs-read-grants/v1",
  "sessionId": "ses_...",
  "generation": 2,
  "updatedAt": "2026-07-10T12:00:00Z",
  "grants": [
    {
      "decisionId": "dec_hfr_opaque",
      "revision": 1,
      "operation": "read",
      "requestedPath": "/Users/operator/Documents/report.txt",
      "canonicalPath": "/Users/operator/Documents/report.txt",
      "visibilityRuleId": "hfs_...",
      "visibilitySource": "profile",
      "issuedAt": "2026-07-10T12:00:00Z",
      "expiresAt": "2026-07-11T12:00:00Z"
    }
  ]
}
```

The manifest is written to a same-directory temporary file, fsynced as needed
by the repository's atomic-file convention, chmodded `0600`, and renamed while
the exclusive provider lock is held. `generation` strictly increases.

## Grant Validation

Before using a grant, the already-running broker must validate:

1. schema/version and strict JSON shape;
2. directory session ID, broker session ID, and manifest session ID match;
3. decision ID/revision identifies the applied `hostfs.read` decision;
4. operation is exactly `read`;
5. requested path is clean and current re-canonicalization equals
   `canonicalPath`;
6. current policy still has the approved explicit visibility and no reserved or
   operation-specific deny;
7. current node is the approved regular file, not a directory or retargeted
   symlink;
8. issue/expiry timestamps are valid, not future-skewed, and expiry is no more
   than 24 hours after issue;
9. current time is before expiry;
10. owner lock remains held by the run process.

Any failed validation denies authority. A malformed manifest does not fall back
to profile, ambient host access, or a stale cached grant.

## Check-Before-Deny

For an explicit-discover locked read, the broker sequence is:

1. clean and re-canonicalize the requested path;
2. evaluate reserved, discover, and read policy;
3. take the provider shared lock and reload/validate `grants.json`;
4. allow immediately if an exact active grant matches;
5. otherwise ask the Manager provider for a decision result;
6. return the corresponding typed denial immediately.

There is no watcher, long-poll, heartbeat, background grant poll, or run
restart. A separate operator process can approve; the same target's next retry
observes the atomic manifest.

## Approval Transaction And Failure

Approval holds the exclusive provider lock for policy/liveness revalidation,
generic decision resolution, required local audit, and final manifest publish.
Readers cannot observe a half-applied provider state. If the process crashes
before the final active manifest rename, no new authority exists. If final
activation fails after decision mutation, Core records an activation failure
and the decision is not presented as a successful usable grant.

No operation writes a profile rule. No grant is copied to a later session.

## Revocation And Cleanup

A grant stops authorizing when any of these occurs:

- its `expiresAt` is reached;
- the run owner lock is released;
- session cleanup removes the read-provider directory;
- the canonical target changes due to symlink retargeting;
- current policy/reserved-root evaluation no longer permits it;
- its decision/grant state is malformed or cannot be verified.

Ordinary grant reads may cache parsed data only behind a validated generation
and only if expiry and owner-lock checks are repeated. Cache state may never
extend an allow beyond the artifact's current validity.

## Evidence Boundary

Local audit may record public decision ID, revision, session/profile/backend,
operation, winning rule ID/source, result, and aggregate suppression count.
It must not record grant file path, file content, symlink target, claim token,
broker token, or capability token. Export/share applies the existing 005
boundary to paths and other local user data.
