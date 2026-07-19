# Contract: Setup Evidence

<!-- markdownlint-disable MD013 -->

## Registry

038 extends the existing product-evidence registry with stable requirements.
It does not create a manifest format.

| Proof ID | Layer | Required for | Meaning |
| --- | --- | --- | --- |
| `038.setup.gate0.intent-plan-parity` | Gate 0 | targeted completion | Grammar, fixed projection, and explicit-init semantic parity |
| `038.setup.gate0.cancel-drift-readonly` | Gate 0 | targeted completion | Default-no paths, stale rejection, and byte/mtime/evidence read-only behavior |
| `038.setup.gate0.daemon-recovery` | Gate 0 | targeted completion | Authenticated daemon path, startup/build/socket failures, no embedded fallback |
| `038.setup.local-fast.package-pty` | product hardening | targeted completion | Installed package executes real setup UI in a PTY without VM/download |
| `038.setup.real-gate2.first-run` | real gate | release candidate | Packaged Lima run, workspace, identity, runtime, audit, reuse, lifecycle |
| `038.setup.real-gate2.agent-install-run` | real gate | release candidate | Exact-integrity non-root agent install and separate-session execution |
| `038.setup.real-gate2.not-run` | real gate | supporting only | Required real prerequisites were absent or failed |
| `038.setup.docs.truth` | product hardening | targeted completion | Docs/help/formula/support claims map to implemented commands and proof IDs |

## Provenance

Every manifest records:

- canonical 40-hex commit;
- dirty state including untracked files;
- package identity and digest where applicable;
- runtime family, revision, and digest for real lanes;
- artifact paths under the evidence root and their SHA-256 digests; and
- redaction status.

Runtime caches may be reused only when keyed and verified by the exact expected
SHA-256. A tag, filename, elapsed time, or pre-existing directory is not proof.

## Gate 0

Must execute behavior, not grep for symbols:

- parse exact setup and reject extra words;
- compare fixed setup and explicit init semantic plans;
- execute fresh, ready, repairable, blocked, cancel, EOF, Ctrl-C, and non-TTY;
- race two profile creators across separate lock owners;
- mutate each effect-relevant field between review and apply and prove refusal;
- regenerate incidental time/random fields and prove no false stale result;
- inject real token/proxy/machine-id-shaped values and prove redaction; and
- exercise stale socket, daemon build mismatch, readiness, and authentication
  failures without embedded fallback.

## Packaged PTY

The package lane installs with `--skip-init`, invokes the packaged binary under
a real PTY, captures the review, confirms once, verifies profile/audit/evidence,
and proves no Lima instance or runtime cache addition occurred. Source-tree
execution cannot satisfy this proof.

## Real Gate 2

The direct/setup lane must:

1. use the installed candidate package on macOS arm64;
2. record installed Lima version;
3. run setup under a PTY;
4. enter a dedicated Git fixture;
5. execute `hideout run -- git status --short` through Lima;
6. observe `/workspace`, non-root account identity, account home
   `/home/developer`, target `HOME=/hideout/profile/home`, synthetic Git
   identity, exact runtime provenance, audit, Boundary evidence, and cleanup;
7. install the exact-version, exact-integrity agent fixture as target user into
   `$HOME/.local` over direct networking without `sudo`;
8. end the install session and invoke the agent by name in a separate session;
9. prove exact environment reuse without a new creation operation; and
10. prove no host/agent/proxy/control-plane credential was imported.

The existing privacy/Lima lane remains and retains its own network claims.
Neither lane can satisfy the other's claim.

## Honest Failure

Missing Lima, unavailable network, package-registry failure, invalid runtime,
insufficient disk, or real backend failure produces failed or `not-run`
evidence. Native/local-fast success cannot replace it.
