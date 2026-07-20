# Contract: Trusted-IDE Grant Record & Open-Time Check

<!-- markdownlint-disable MD013 -->

## Record

- Path: `profiles/<profile>/ide-trust-grants.json`, guest-unreachable, `0600`,
  atomic write.
- Manifest shape:

```json
{
  "version": "hideout.trusted-ide-grants/v1",
  "profile": "<profile>",
  "grants": [
    {
      "workspaceId": "wrk_<hex>",
      "qualifiedAppRef": "builtin.vscode/rev_<hex>/vscode",
      "bindingDigest": "sha256:<hex>",
      "grantedAt": "2026-07-20T00:00:00Z"
    }
  ]
}
```

- Strict decode (unknown fields rejected). A malformed or unreadable manifest is
  treated as "no grants" (fail closed), never as an implicit allow.

## Open-time check (authoritative, single path)

Location: `runProjectionGrantChecker.TrustedGrantActive` (the only checker the
production data plane wires into the broker).

Order:

1. Resolve `binding` for `scope.Command`; require `scope == binding.scope()`
   (unchanged guard).
2. **Persistent grant check (new)**: read the profile's grant manifest; a grant
   matches iff `workspaceId == scope.WorkspaceID`,
   `qualifiedAppRef == scope.QualifiedAppRef`,
   `bindingDigest == scope.BindingDigest`, and the profile IDE mode is
   `trusted-host-ide`. On match → authorized (trusted launch).
3. On no match → fall through to the existing per-run decision lookup (kept for
   compatibility with any non-trusted-IDE ask-each-run binding), which for a
   one-shot trusted `code .` yields no approval → refuse.

`TrustedGrantActiveForResource` continues to require the resource class to be in
the binding's resource classes, then delegates to `TrustedGrantActive`.

## Removed / documented twin

`decisionIdeGrantChecker` (`hostcap_projection.go`) has only test callers and is
never wired into the production broker. It is deleted, or, if a test still needs
it, renamed/commented explicitly as test-only so there is exactly one
production trusted-grant decision path (FR-011, SC-006).

## Audit events

| Event | When | Key details (Core-derived only) |
| --- | --- | --- |
| `host-app.ide-trust` decision=`grant` | operator grants | profile, workspaceId, qualifiedAppRef, bindingDigest |
| `host-app.ide-trust` decision=`revoke` | operator revokes / safe-mode drop | profile, workspaceId (or "all") |
| `host.app.open-resource` outcome=`launched` mode=`trusted` | grant match → native launch | existing projection audit + trusted mode |
| `host.app.open-resource` outcome=`refused` code=trusted-denied | no grant | existing projection refusal audit |

No host path, username, token, machine id, or raw argv in any of these.

## Invariants (must have mutation proofs / negative fixtures)

- Guest writing the workspace cannot create/refresh/read a grant (grant lives
  under `profiles/<p>/`, not the workspace).
- A grant for workspace A does not authorize workspace B; a changed
  `bindingDigest` does not reuse a prior grant.
- Malformed manifest → fail closed.
- Safe mode ignores grants; switching to safe deletes them.
- Exactly one production trusted-grant check path.
