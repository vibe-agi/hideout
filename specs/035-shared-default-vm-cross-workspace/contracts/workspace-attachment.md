# Contract: Workspace Attachment

## Authority Input

Manager constructs an attachment only from the operator-selected current
project after workspace safety validation. Guest, JavaScript, adapters, events,
and display labels cannot supply or replace the canonical host root.

The immutable authority tuple is:

```text
environment incarnation
+ session ID
+ canonical root and captured root identity
+ workspace ID
+ logical and physical guest roots
+ selected transport
```

## Attach Transaction

1. Enter the existing lifecycle attach serialization.
2. Observe and bind the exact backend incarnation.
3. Canonicalize, safety-check, open/capture, and identify the root.
4. Derive the one store-keyed workspace ID.
5. Compute overlap notices against active attachments.
6. Register provider, guest view, and real environment service as `planned`.
7. Commit the complete planned subgraph before concrete side effects.
8. Start and validate the provider/share.
9. Mount the exact view inside the planned session namespace.
10. Bind the immutable attachment to the run session.
11. On authenticated supervisor-ready proof, activate resources in dependency
    order and publish target readiness.

Failure before effects releases the planned graph. Failure after effects must
prove rollback or record unproved state. No failed attach may execute the target.

## Path Contract

- Operator-facing logical root is `/workspace`.
- Physical root is opaque and workspace-specific inside the private namespace.
- The target cannot replace the logical entry or select another physical ID.
- Broker/host-app references contain a verified guest path and relative path;
  Core resolves them through the attachment.
- String replacement of `/workspace` with a host prefix is forbidden.
- Git safe-directory contains only the minimum logical/physical forms observed
  by the accepted path model; wildcard and sibling entries are forbidden.

## Root Safety

Every content operation is rooted in the captured identity using
descriptor-relative or equivalent race-safe primitives. Lexical joins and
final-component checks alone are insufficient. Parent/sibling traversal,
ancestor symlink replacement, rename escape, root replacement, alternate case
or Unicode alias, and reserved-root access fail closed.

If the root is renamed/replaced during admission, attach fails. After attach,
new lookups never switch to a new object at the old path. Existing open-handle
behavior follows only the explicitly tested host filesystem semantics.

## Direct I/O Contract

Successful guest mutation changes the host project before success returns.
Successful host mutation becomes guest-visible within the accepted convergence
bound. There is no decision, overlay, apply, merge, or exit-time copy.

Required ordinary operations and unsupported behavior are recorded in the
accepted research operation matrix. Flush/fsync/lock errors are propagated;
unsupported operations return stable errno and never fake success.

## Same, Nested, And Disjoint

- Same-root sessions may share a concrete provider but own independent views,
  handles, locks, and lifecycle bindings.
- Nested roots retain exact asymmetric authority and produce an informational
  notice.
- Disjoint ordinary non-root sessions cannot enumerate or open siblings through
  the workspace surface.

These relations never authorize provider reuse by themselves; reuse also
requires matching captured root identity and transport state.

## Cleanup

1. Stop target/process group.
2. Revoke attachment-based broker and host-app authority.
3. Drain dependent live bridges.
4. Flush and close workspace handles.
5. Unmount and prove guest-view absence.
6. Release `workspace.guest-view`.
7. Remove/stop and prove provider/share absence or release one same-root binding.
8. Release `workspace.host-provider`.
9. Release an idle environment service only after its final binding.
10. Finish ordinary 034 cleanup and let 036 evaluate grace/stop.

Cleanup never deletes or applies project content. Ambiguity becomes unproved
and blocks new attach/reuse/automatic stop for that incarnation.
