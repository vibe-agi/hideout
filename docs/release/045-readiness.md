# Feature 045 release readiness

<!-- markdownlint-disable MD013 -->

This page defines the release handoff for the operator observability console.
It deliberately contains no mutable commit, version, digest, timestamp, or
“latest” verdict. The private machine-readable files under
`.artifacts/045/` are authoritative so that editing this tracked document
cannot invalidate an otherwise exact candidate.

## Authoritative verdict

A candidate is locally **READY** only when this command exits zero:

```sh
scripts/release/collect-evidence.sh --require-closure
```

The resulting `.artifacts/045/evidence.json` must also satisfy all of these
conditions:

```sh
jq -e '
  .schema == "hideout.release-evidence/v1" and
  .result == "passed" and
  .stage == "final-ready" and
  .releaseReadiness == true and
  .source.dirty == false and
  .candidate.publicationStatus == "local-only" and
  .closure.localInstall.status == "passed" and
  .closure.publicationAbsence.status == "passed" and
  all(.gates[]; .result == "passed") and
  all(.gates[] | select(.scope == "candidate");
    .candidateAcceptance == true) and
  .review.openRequiredFindings == 0
' .artifacts/045/evidence.json
```

The detached SHA-256 file at `.artifacts/045/evidence.json.sha256` must verify
the manifest bytes. Any missing, stale, reduced, unsupported, dirty, mismatched,
or failed required input makes the verdict **BLOCKED**, not conditionally
ready.

Performance evidence is valid only when the release operator first pauses
unrelated CPU-heavy tests, VMs, and emulators and keeps the host quiet through
all thirty recorded real-Lima samples. Host-wide samples decide whether the run
is eligible; they are not Hideout's overhead metric. Acceptance is independently
computed from the exact reference child process's user/system CPU and from its
paired elapsed time. The median and one-sided exact 95% upper confidence bound
for both must remain at or below ten percent. Known contention invalidates that
run; it is never explained away after seeing the result or used to change the
frozen threshold. The full lane requires the explicit
`HIDEOUT_PERFORMANCE_QUIET_HOST_CONFIRMED=1` acknowledgement and retains
private host-state snapshots at the run start and real-Lima boundaries. Before
building, it also takes three one-second process-name/CPU snapshots and rejects
sustained high-CPU, virtualization, or build/test contention in at least two;
it records no argv or environment and never stops the reported process.

A retained run may be reused after an evidence-only change only through
`scripts/release/revalidate-performance-evidence.sh`. Its versioned receipt must
verify the original summary and every retained artifact, independently recompute
both workload CPU and elapsed-time results from the raw samples, enumerate the
exact Git diff, and prove the normalized measurement entrypoint is unchanged.
Any product, BPF, workload, measurement, threshold, or unclassified path change
fails closed and requires a new performance run.

## Closure sequence

Run the source, model, privacy, UI, performance, real-Lima, package,
package-bound migration, and lifecycle lanes in the order fixed by
`specs/045-operator-observability-console/gate-matrix.md`. Then consume the
same package bytes on this machine:

The real-Lima aggregate stops at its first failed lane and records every later
lane as `not-run`. If investigation establishes an external or transient cause
and the repository is still at the identical clean commit, retry with
`scripts/gates/release-candidate-lima.sh --resume-passed`; it reauthenticates
the immediately preceding failed receipt and reuses only its passed lanes. A
source change invalidates all such reuse and starts a fresh aggregate.

```sh
scripts/release/install-local-candidate.sh \
  --yes-discard-legacy-data

scripts/release/verify-publication-absence.sh

scripts/release/collect-evidence.sh \
  --require-closure
```

The local-install command is intentionally destructive within two exact
scopes: the recognized existing Hideout installation and the current user's
canonical `~/.hideout` directory. It first stops and cleans every Hideout
environment through the product lifecycle authority. It refuses a symlink,
another store path, another installation prefix, a dirty source tree, or a
candidate that is not digest-bound to the current commit.

The installed-machine receipt proves the exact package digest and binary
identity, interactive setup, managed Keychain secret input without argv or
environment exposure, connection plan/apply without daemon restart, a
deterministic proxied Lima run, task-oriented Help, one-shot and real-PTY TUI,
an authenticated loopback WebUI API request, environment cleanup,
same-candidate update, normal uninstall with durable-state preservation,
package-file absence, and a final exact reinstall. Its final host state is:

- the exact standalone candidate at the active Homebrew prefix;
- a fresh default profile using direct networking;
- no retained Hideout environment;
- no `local-proxy` secret; and
- a stopped Hideout daemon.

Raw proxy credentials, the WebUI control URL/token, the authenticated WebUI
response body, and the interactive TUI byte stream are used only in a private
temporary directory. The receipt retains digests and semantic outcomes, then
scans both retained evidence and the fresh store for the exact secret values.

## Publication-absence boundary

`scripts/release/verify-publication-absence.sh` is read-only. It observes the
candidate tag twice with `git ls-remote`, requires an exact GitHub API `404`
for the candidate release twice, compares two remote Homebrew formula
observations, and proves that the clean local tap HEAD, tree, formula digest,
and worktree did not change. Its receipt is point-in-time evidence for the
supported GitHub Release and Homebrew channels; it is not a claim that no
future publication can occur.

None of the closure commands may create or push a tag, create a GitHub Release,
edit or push the Homebrew tap, upload candidate bytes, or publish a package.
Local readiness grants no publication authority.

## Publication handoff

After the authoritative verdict is READY, publication remains a separate
operator action. It requires a new, explicit instruction naming the intended
version and channels. Before any mutation, the publisher must reverify the
detached evidence digest, exact source commit/tree, candidate archive digest,
remote tag/release absence, and unchanged tap base. If any identity or remote
state has changed, stop and rebuild or re-review rather than reusing the
receipt.

The post-publication workflow must create its own receipts for the pushed tag,
GitHub Release assets and checksums, Homebrew formula commit, clean-install
test, and rollback/recovery information. Those mutations and receipts are
outside Feature 045's local-only authorization.
