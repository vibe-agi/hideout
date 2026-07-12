# Contract: Application Identity And Safe Effect Floor

<!-- markdownlint-disable MD013 MD060 -->

## Application Roots

Community bundle declarations are basenames. Core searches only:

```text
/System/Applications
/Applications
$HOME/Applications
```

Immediately before review and every launch, Core:

1. joins the basename beneath one root without traversal;
2. resolves bundle and executable symlinks;
3. proves both remain beneath the same root and the executable remains inside
   the bundle;
4. proves every ancestor is not group/world writable;
5. proves owner is root or the current host operator;
6. rejects overlap with workspace, HostFS writable roots, temporary roots,
   source intake/snapshots, runtime/session state, or the Hideout store;
7. proves the executable is a regular executable file.

Any ambiguity is `identity-drift` and launches nothing.

## Observed Identity

Core obtains signing facts from the host. Package `expectedBundleId`,
`expectedTeamId`, or other expectations may reject an observation but cannot
turn it into verified identity. The trust record binds the Core observation;
launch re-observes and compares it.

An unsigned app requires a distinct explicit acceptance of canonical identity
and a Core-computed `bundle-tree-v1` content digest. Core computes that digest
from an already validated bundle descriptor using descriptor-relative reads:

- normalized relative path, entry type, permission bits, regular-file bytes,
  and contained symlink target text are authenticated;
- links that resolve outside the bundle, devices, sockets, FIFOs, excessive
  entry count/bytes, and unreadable entries fail closed;
- Core compares descriptor identity and size before and after each read and
  rejects any tree mutation observed during measurement;
- package fields, package tests, timestamps, and caller-supplied hashes cannot
  provide or override the digest.

The configured limits are product constants and appear in inspection when an
app exceeds them. The digest may be expensive and is excluded from the local
500 ms inspection budget. An accepted app is always `unverified-app`, never
safe, and requires ask-each-run. Digest change suspends enablement before
launch.

## Core Safety Profiles

A package may request a profile id but cannot define profile contents. Core
selects it only when the observed signed app identity matches a reviewed family.
The selected profile owns:

- exact version and compatible identity matchers;
- required and forbidden launch flags;
- isolated per-qualified-app/per-run state layout;
- exact allowed, required, and forbidden configuration keys/values;
- extension/task/trust and equivalent-effect checks;
- pre-launch state verification.

Core builds the final argv and settings, then validates their combined effect.
A forbidden setting cannot bypass a forbidden flag rule. Unknown identity,
profile mismatch, unreviewed recipe settings, or failed state preparation cannot
be labeled safe; the operator may select ask-each-run where otherwise valid.

## Product Labels

| Fact | Product label | V1 access |
|------|---------------|-----------|
| Signed, observed identity, compatible Core safety profile | Safe available | `safe` or `ask-each-run` |
| Signed, observed identity, no compatible safety profile | Elevated | `ask-each-run` |
| Explicitly accepted unsigned exact digest | Unverified app | `ask-each-run` |
| Absent, unsupported, drifted, unsafe root/owner | Unavailable | none |

Package descriptions and tests cannot suppress or replace these labels.
