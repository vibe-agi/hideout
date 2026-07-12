# Data Model: HostFS Discoverable Namespace

<!-- markdownlint-disable MD013 -->

## Visibility Rule

An operator-authored HostFS rule stored in the existing profile, environment,
or run policy.

| Field | Type | Rules |
| --- | --- | --- |
| `id` | string | Stable HostFS rule ID; unique within one profile config |
| `hostPath` | absolute path | Clean, canonicalized for enforcement; discover selectors reject glob metacharacters |
| `ops` | operation array | Contains `discover` for visibility rules; discover is not normalized to stat/read/list |
| `scope` | enum | `exact-file`, `dir`, or `recursive-dir` for `see`, `see-dir`, and `see-tree` |
| `effect` | derived enum | Allow when in `grants`, deny when in `deny` |
| `source` | enum | `profile`, `environment`, or `run` after effective-policy compilation |
| `subject` | enum | Existing source-compatible HostFS subject |
| `ttl` | enum | Existing source-compatible HostFS TTL |
| `reason` | string | Required local user data; deterministic control-plane redaction applies |
| `createdAt` / `expiresAt` | timestamps | Existing HostFS lifetime rules |

### Validation

- Reserved-root allow coverage is rejected before runtime.
- Discover allow and deny use the same path/scope validator.
- `see*` globs and new `list:` input are rejected.
- Existing raw list-only rules can be read only by the dedicated migration
  loader and cannot be saved until every legacy list rule is mapped and the
  resulting profile passes normal validation.

### Relationships

- A **Visibility Rule** may produce many **Discovered Nodes**.
- A locked discovered regular file may produce one **Read Decision** per
  session/canonical-path/operation key.
- A read/content/stat/overlay rule may imply only the exact visibility required
  for its existing operation; that does not make it an explicit discover domain.

## Visibility Evaluation

The Go-owned result of evaluating one requested host path.

| Field | Type | Meaning |
| --- | --- | --- |
| `state` | enum | `hidden`, `exact-visible`, `enumerable`, `content-granted` |
| `explicitDomain` | bool | True only when an explicit discover allow/deny covers the path |
| `enumerationDepth` | integer | Relative depth allowed beneath the winning root; zero for exact visibility |
| `ruleId` | string | Winning visibility rule, if public-safe for local evidence |
| `source` | enum | Winning policy source |
| `reason` | string | Core-generated local explanation |
| `reservedHidden` | bool | Reserved-root denial won and no other authority can override it |
| `discoverHidden` | bool | Discover deny suppresses discover allow, broad listing, and read proposals |

### Precedence

1. Reserved control-plane root denies every operation and visibility result.
2. Explicit operation deny controls the requested content operation.
3. Existing exact operation-specific grant/staged-node authority retains the
   exact lookup needed for that operation outside reserved roots.
4. Explicit or Core-injected categorized sensitive-root discover deny
   suppresses discover allows, enumeration, and read proposals for all
   remaining discovery-only access.
5. Explicit discover allow provides coarse visibility.
6. Hidden default applies when no exact operation or discover result exists.

`explicitDomain` controls whether the new EACCES distinctions apply. A node
visible only because of a legacy operation grant preserves legacy denial
collapse for other operations.

## Discovered Node

The target-safe representation of a name visible below full stat/read
authority.

| Field | Type | Rules |
| --- | --- | --- |
| `name` | string | Basename only; no synthetic sentinel names |
| `kind` | enum | `file`, `dir`, `symlink`, or `other` |
| `locked` | bool | True when the requested content authority is absent |
| `caps` | string array | Generic target-safe labels such as `discover`; never a claim/capability token |

The locked representation has no size, mode, owner/group, timestamps,
inode/device identity, xattrs, content, or symlink target. FUSE derives coarse
presentation attributes from kind; those presentation attributes are not
authority.

## Typed HostFS Error

An additive, target-safe broker response error.

| Field | Type | Rules |
| --- | --- | --- |
| `code` | closed enum | One V1 HostFS/broker code from the contract |
| `errno` | closed enum | `ENOENT`, `EACCES`, `EOVERFLOW`, `EIO`, or `EROFS`; code/errno pair is validated |
| `retryable` | bool | Unchanged request may progress later due to time or external control-plane state |
| `decisionRef` | optional string | Public decision ID only when a real pending/claimed decision exists |
| `retryAfterMs` | optional integer | Positive bounded interval only when Core can state one honestly |

`stderr` is separate human context and cannot change the errno. An unknown or
malformed typed error maps to EIO in the guest helper.

## Read Proposal Key

The provider's stable identity for equivalent target requests.

| Field | Type | Rules |
| --- | --- | --- |
| `sessionId` | session ID | Must match current broker session |
| `canonicalPath` | absolute canonical path | Recomputed after symlink resolution |
| `operation` | enum | `read` only in V1 |
| `keyDigest` | SHA-256-derived opaque value | No raw path embedded in public decision ID |

One key has at most one unresolved decision. Terminal denial/timeout remains
remembered for the session until an operator reopens it.

## Read Decision

An actionable generic decision with a `hostfs.read` provider reference.

| Field | Type | Rules |
| --- | --- | --- |
| `id` | opaque decision ID | Deterministic for proposal key; no raw path |
| `kind` | constant | `hostfs.read` |
| `source` | object | Profile, session, backend |
| `state` | decision state | Existing pending/claimed/applied/denied/timed-out/failed/stale vocabulary |
| `revision` | positive integer | Incremented by explicit reopen |
| `timeoutAt` | timestamp | Five minutes after creation/reopen; retries do not extend it |
| `defaultOutcome` | constant | `deny` |
| `proposedAction` | object | Exact read, requested/canonical path, discover grant ID/source, session lifetime |
| `preview` | object | Core summary and facts; no file content or symlink target |
| `untrustedReason` | optional string | At most 512 UTF-8 bytes, labeled untrusted, plain-text rendered |
| `providerRef` | object | Provider name, decision ID, session ID; private paths omitted |
| `auditRef` | string | Stable local audit reference |

### State Transitions

```text
eligible read -> pending -> claimed -> applied
                        |          `-> denied
                        `-> timed-out

denied/timed-out --operator reopen + live session--> pending (revision + 1)
terminal --dead/orphaned/unprovable session--> unchanged + fail closed
```

Repeated target requests do not change timeout, claim lease, revision, or
terminal state.

## Read Provider State

Private Manager-owned state under one session's `hostfs-read` directory.

| Field | Type | Rules |
| --- | --- | --- |
| `version` | constant | `hideout.hostfs-read-provider/v1` |
| `sessionId` | session ID | Must match directory/session owner |
| `requests` | keyed records | Dedup key, decision ID, terminal state, revision, creation history |
| `creationTimes` | timestamp array | Rolling 60-second rate accounting; compacted under lock |
| `updatedAt` | timestamp | Informational; not authority by itself |

All create, limit, terminal, reopen, and activation operations hold the
provider's exclusive advisory lock. Broker grant reads hold a shared lock.

## Session Read Grant Manifest

The cross-process authority artifact consumed by the already-running broker.

| Field | Type | Rules |
| --- | --- | --- |
| `version` | constant | `hideout.hostfs-read-grants/v1` |
| `sessionId` | session ID | Exact match required |
| `generation` | unsigned integer | Monotonic under provider lock |
| `updatedAt` | timestamp | Must be valid and no later than the containing entry activation |
| `grants` | grant array | Exact-file read grants only |

Each grant contains:

| Field | Type | Rules |
| --- | --- | --- |
| `decisionId` / `revision` | decision identity | Must match an applied provider decision |
| `operation` | constant | `read` |
| `requestedPath` | absolute path | Local private state; never exported directly |
| `canonicalPath` | absolute path | Must equal current no-surprise canonicalization on every read |
| `visibilityRuleId` / `visibilitySource` | policy identity | Approval-time policy revalidation result |
| `issuedAt` | timestamp | Set only by Core apply |
| `expiresAt` | timestamp | No later than 24 hours after issue |

The manifest is atomically replaced. Malformed, unreadable, mismatched,
expired, non-applied, or retargeted entries are ignored as authority and
produce fail-closed diagnostics/audit.

## Session Owner Lock

An OS-held liveness fact, not a JSON assertion.

| Field | Type | Rules |
| --- | --- | --- |
| path | private file | `sessions/<id>/hostfs-read/owner.lock` |
| owner | open file descriptor | Run data-plane process holds exclusive lock for its lifetime |
| probe | nonblocking exclusive lock attempt | Lock contention plus matching session metadata means live; success/unknown means unprovable |

The lock and read-provider directory are removed by session ephemeral cleanup.
No later run re-adopts a previous session ID or grant.

## Sensitive Root Entry

A categorized source-of-truth record shared by workspace and visibility logic.

| Field | Type | Rules |
| --- | --- | --- |
| `path` | canonical host path | Platform-aware, deduplicated |
| `category` | enum | `home-boundary`, `control-plane`, `credential`, `browser`, `system-key` |
| `workspaceRestricted` | bool | Whether it blocks ordinary workspace selection |
| `broadDiscoveryHidden` | bool | Whether broad preset expansion adds discover deny |
| `reason` | Core string | Stable operator explanation |

The whole-home entry is workspace-restricted but not broad-discovery-hidden.
The Hideout store remains reserved independently of catalog state.

## Visibility Preset And Onboarding Evidence

| Field | Type | Rules |
| --- | --- | --- |
| `selection` | enum | `none`, `landmarks`, `home-tree` |
| `selectedRoots` | path array | Explicit current-user roots only |
| `nameDisclosureAcknowledged` | bool | Required for `home-tree` |
| `expandedRuleIds` | string array | Resulting ordinary HostFS rules |
| `warnings` / `nonClaims` | string arrays | Include name disclosure, TCC, and hidden-predictable-path boundaries |
| `posture` | string | Honest visibility posture in plan/evidence/Boundary Summary |

Noninteractive omission normalizes to `none`. The profile template never
performs host enumeration while expanding a preset.

## Visibility Evidence

Evidence uses the existing `hideout.product-hardening-evidence/v1` manifest and
proof registry.

| Proof class | Required facts |
| --- | --- |
| Unit/Gate 0 | Policy, typed errno, lifecycle, limits, migration, redaction, docs |
| Real Gate 2 namespace | Real Lima/FUSE names, hidden paths, locked errno, complete-or-error, compatibility |
| Real Gate 2 live grant | Separate process approval, same target retry, attr convergence, symlink retarget refusal |
| Not run | Missing real prerequisite only; supporting evidence and never satisfaction |

Real proof artifacts require existence and digest validation. Public evidence
contains no file content, symlink target, token, or private grant path.
