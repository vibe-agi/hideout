# Contract: Projection Readiness

<!-- markdownlint-disable MD013 -->

## Authority Split

Manager owns:

- final command registry and immutable binding catalog;
- catalog canonicalization and digest;
- file materialization and last-written manifest;
- reviewed run digest and stale-plan comparison;
- validation of the authenticated backend ready proof;
- lifecycle activation and audit disposition.

The backend owns:

- mounting the exact session runtime view;
- binding and validating the current instance/boot identity;
- bounded guest visibility and file-integrity observation;
- publishing the authenticated ready proof;
- refusing target commit when observation is not `ready`.

The broker and host-app provider independently retain:

- exact command registry lookup;
- binding, grant, resource, application identity, and lifecycle validation;
- host effect and no-fallback behavior.

Readiness does not grant or execute host authority.

## Admission Sequence

```text
compile exact Manager catalog
  -> bind catalog digest into reviewed run
  -> materialize dispatcher and every command entry
  -> atomically write readiness manifest last
  -> Backend.Prepare receives immutable expectation
  -> Manager rebinds exact expectation on returned Session
  -> activate/prove exact backend boot
  -> bind exact session runtime view
  -> validate manifest and complete entry catalog
  -> guest supervisor reports ready
  -> Manager validates ready proof/catalog digest
  -> supervisor commit
  -> target launch
```

Any skipped, reordered, or mismatched step refuses commit.

## Catalog Contract

- Catalog input is the final `RunDataPlane.Registry`, not a profile-only
  reconstruction.
- Names and aliases are explicit, sorted, unique entries.
- The dispatcher is an explicit entry.
- Every entry records a digest of the bytes written to the session directory.
- The catalog digest includes session, environment, session snapshot,
  dispatcher, and all entries.
- The reviewed plan carries the digest. Apply recompiles and compares before
  materialization.
- Manager overwrites/rebinds the expectation after backend `Prepare`; a backend
  cannot omit or rewrite it.

## Manifest Contract

- The manifest lives under the exact private session runtime root.
- It is written with private permissions using a temporary regular file,
  durable close, and atomic rename after all entries succeed.
- Existing manifest, temp collision, symlink, non-regular path, or wrong parent
  identity fails closed.
- Decoder rejects unknown fields, trailing data, duplicate entries, invalid
  names/digests, and any identity mismatch.
- No manifest field is authority or a host path.

## Guest Observation Contract

The exact session view validates:

1. expected boot identity;
2. exact session source mounted at `/hideout/session`;
3. manifest regular/non-symlink/executable policy as applicable;
4. manifest identity and catalog digest;
5. dispatcher and every command as regular, non-symlink executables;
6. exact entry digests;
7. expected entry count.

Missing/not-yet-visible state may retry up to the existing two-second bound.
Structural, symlink, identity, and digest failures refuse immediately.

## Ready/Commit Contract

The authenticated ready proof includes:

- existing session/environment/snapshot/instance/boot identities;
- `projectionStatus=ready`;
- catalog digest;
- expected and observed entry counts;
- bounded readiness duration.

Manager compares all fields with the reviewed expectation before:

- activating the shared workspace view;
- activating supervisor and target lifecycle resources;
- publishing daemon `Started`;
- sending `SupervisorCommit`.

No target output, stdin, resize, signal, or host-app request is accepted before
commit.

## Failure Contract

| Failure | Disposition |
| --- | --- |
| Missing entry within deadline | retry before target |
| Deadline reached | typed timeout; no target/effect/fallback |
| Caller cancelled before commit | close owning SSH session immediately |
| Manifest/catalog identity mismatch | immediate catalog-drift refusal |
| Symlink/non-regular/non-executable entry | immediate entry-invalid refusal |
| Digest mismatch | immediate digest-mismatch refusal |
| Instance/boot/session drift | immediate identity-drift refusal |
| Ordinary non-projected target missing | existing command-not-found behavior; no projection retry |

## Compatibility Contract

- Shared fresh, shared warm, dedicated, disposable, and workspace-bound Lima
  sessions use the same catalog/manifest contract.
- Native may validate catalog construction and stale-plan behavior but cannot
  satisfy guest-visibility claims.
- Existing safe/trusted grants and external pack enablement are unchanged.
- No new CLI, configuration, provider, workspace copy, or host fallback exists.
