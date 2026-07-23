# Contract: Projection Readiness Observability And Evidence

<!-- markdownlint-disable MD013 -->

## Local Audit

One structured readiness event is emitted per run:

```text
action: projection.readiness
decision: allow | deny
details:
  status: ready | refused | timed-out | cancelled
  reasonCode: <closed code or omitted on ready>
  command: <public projected command or omitted>
  catalogDigest: <sha256>
  expectedEntries: <count>
  observedEntries: <count>
  durationMs: <bounded integer>
  targetProjected: <boolean>
```

The event must not contain host paths, runtime paths, usernames, raw argv,
tokens, application private directories, grant records, machine IDs, or
session/environment identifiers in exported evidence.

## Gate 0

Gate 0 proves:

- canonical catalog/manifest digest and strict decoding;
- manifest written after all exact entries;
- complete built-in/external catalog carriage through Prepare;
- regular/non-symlink/executable/digest guest checks;
- ready/commit catalog and identity match;
- bounded timeout and pre-commit cancellation;
- zero target/effect/fallback on every refusal;
- concurrent session catalog isolation;
- ordinary command behavior unchanged;
- broker registry/binding mismatch denial;
- four-template alias assertion and existing pathMode recreate proof;
- real capability descriptor and unbound intent schema parity;
- mutation-red results for every new assertion;
- strict evaluator negative fixtures;
- docs truth and debt disposition.

Native/fake lanes remain mechanics only.

## Real Artifact Inventory

The 043 producer retains exactly:

```text
product-hardening-evidence.json
artifacts/projection-readiness.json
artifacts/readiness-samples.tsv
artifacts/projection-flows.json
artifacts/projection-privacy-gate3.json   # only for privacy promotion
artifacts/package-manifest.json
artifacts/runtime-manifest.json
logs/...
```

Every declared artifact has an exact relative path and SHA-256. Unknown
top-level or nested fields are rejected.

## Gate 2 Readiness Artifact

Required methodology:

- at least 10 genuinely fresh environment creations;
- projected command is the first target in every fresh sample;
- at least 30 new sessions on warm environments;
- at least one overlapping pair with disjoint catalogs;
- nearest-rank p95 from raw samples;
- readiness p95 at most 2,000 ms;
- pre-commit cancellation at most 2,000 ms;
- zero operator retries, target retries, fallbacks, timeouts, unauthorized host
  effects, and cross-session access.

The evaluator recomputes sample counts and p95 from TSV; producer summaries are
not trusted.

## Closed Gate 2 Check Families

- `readiness.*`: catalog, manifest, dispatcher, entry properties, exact session
  view, ready/commit proof.
- `refusal.*`: stale catalog, identity/boot drift, timeout, cancellation,
  symlink/type/digest failure, zero target/effect/fallback.
- `concurrency.*`: disjoint catalog isolation and ordinary-command compatibility.
- `projection030.*`: safe host effect, task suppression, alias channels,
  preserve positive control, run-bound grant/revoke.
- `external032.*`: old-session immutability, workspace and authorized HostFS
  resources, unsafe identity denial, disable/revoke no fallback.
- `persistent039.*`: initial refusal, host grant, separate-run reuse, revoke,
  later refusal.
- `redaction.*`: exact application identity class and public artifact scan.

The concrete inventory is fixed in the production evaluator. Missing, extra,
or false checks fail.

## Gate 3 Privacy Artifact

Clean privacy promotion requires a matching candidate/package/runtime artifact
with all closed checks true:

- `guestWorkspaceAlias`;
- `proxyEnvAbsent`;
- `dnsMediated`;
- `connectedSubnetBlocked`;
- `httpsRequest`;
- `privilegeSeparation`;
- `publicEvidenceRedacted`.

If Gate 3 does not run, the artifact is omitted and 030/043 alias privacy
remains explicitly unpromoted. A `not-run` record cannot satisfy it.

## Production Proofs

The unified producer supplies:

- new 043 Gate 0 and real first-attempt readiness proofs;
- the existing 030 built-in code-open, alias, and run-bound trusted proofs;
- the existing 032 external-pack real proof;
- a new 039 persistent trusted-grant real proof;
- a 043/030 privacy proof only when matching Gate 3 passes.

All real proof requirements use:

- exact clean source commit and package;
- exact runtime artifact/build identity;
- fixed real mode and evidence class;
- a Go semantic artifact validator;
- independent release-readiness fixture support.

## Mandatory Negative Fixtures

The production evaluator must reject:

- dirty source;
- wrong source commit or package digest;
- wrong runtime artifact/build;
- wrong platform/backend/guest;
- missing, extra, false, or unknown checks;
- nine fresh or 29 warm samples;
- edited summary not derived from raw TSV;
- p95 above threshold;
- nonzero retry, fallback, timeout, unauthorized effect, or cross-session
  access;
- missing/altered artifact or digest;
- absent redaction;
- edited `passed`, reduced probe, local/native result, or `not-run`;
- unknown JSON fields.

## Promotion Rule

Docs may promote only the proof families that independently pass. Gate 2
readiness, 032 external pack, and 039 persistent grant may become clean while
alias privacy remains explicitly dirty/pending if Gate 3 prerequisites are not
available. Aggregate gate success remains regression context and never
substitutes for the strict artifact evaluator.
