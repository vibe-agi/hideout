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

## Closure sequence

Run the source, model, privacy, UI, performance, real-Lima, package, and
lifecycle lanes in the order fixed by
`specs/045-operator-observability-console/gate-matrix.md`. Then consume the
same package bytes on this machine:

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
