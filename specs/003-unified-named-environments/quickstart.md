<!-- markdownlint-disable MD013 -->

# Quickstart: Validate Unified Named Environments

Validation guide for `003-unified-named-environments`. Contracts live in
[contracts/environment-model.md](contracts/environment-model.md); entity
shapes in [data-model.md](data-model.md).

## Preconditions

- Repository root, clean or disposable `HIDEOUT_STORE_ROOT`.
- Lima installed for the real-backend steps; unit/schema steps need neither
  Lima nor network.

## 1. Static And Unit Gates

```bash
go test ./...
scripts/test-gate0.sh
```

Expected: all green; profile schema accepts `environment.baseImage` in both
forms and rejects digest-less URLs, credentialed URLs, and OCI-style refs.

## 2. Create And Inspect (no boot, no network)

```bash
hideout env create work --image 'template:_images/ubuntu-lts'
hideout env inspect work
hideout env list
```

Expected: create returns without booting a guest or touching the network;
inspect shows the verbatim pinned declaration and the three identity axes;
list shows `work` alongside nothing else on a clean store. Creating `default`
(any case) fails with the reserved-name message. Creating `work` again fails
as a collision.

## 3. Auto-Named Resolution

```bash
cd /path/to/sanitized/project
hideout run -- pwd
hideout env list
```

Expected: the run resolves to a deterministic auto-named environment for
(profile, workspace), creating it on first use; `env list` shows it marked as
auto-named with the profile's default image; rerunning reuses it. The
previous top-level `hideout list` no longer exists as a public command.

## 4. Declared Image Boots (real Lima gate)

```bash
hideout env create imgtest --image 'https://<distributor>/<image>.img#sha256:<digest>'
hideout run --env imgtest -- cat /etc/os-release
```

Expected: first run boots the guest from the declared image (Lima downloads
and verifies the digest); the run summary names `imgtest`. A wrong digest
fails the boot closed naming ref and digest. This step is the Lima gate
variant; record its evidence per the test plan.

## 5. Drift Fails Closed, Recreate Recovers

Change one use-time drift input at a time and verify each produces a drift
report, not a silent new environment:

- bump the backend configuration version (test hook) → drift names
  `backendConfig`;
- run `--env work` from a different directory than its pinned workspace →
  drift names `workspace` (real file identity, not string compare).

```bash
hideout env recreate work
hideout run --env work -- pwd
```

Expected: recreate refuses while the guest runs (prints stop hint; `--force`
stops then proceeds), rebuilds under the same name, and the next run
succeeds from the pinned image declaration. Changing the profile base image
default or `tools.expectedCommands` alone produces no drift and no recreate
requirement; a missing declared command required for the target fails
readiness with a diagnostic instead.

## 6. Old Records Are Guided, Not Migrated

Plant a prior-version record fixture in the store, then:

```bash
hideout env list
hideout run -- pwd
```

Expected: the old record surfaces as unsupported-version with
clean-and-recreate guidance; no operation reads through it; nothing migrates.

## 7. Shadow Warning

Add a profile HostFS rule targeting a path inside the pinned workspace, then
run plan/doctor.

Expected: a warning names the shadowed rule and the workspace; the run is not
blocked.

## 8. Evidence

```bash
hideout audit show --limit 30
```

Expected: `env.create`/`env.recreate`/`env.remove` and any
`env.drift.denied` events appear with environment name, image ref, workspace
verbatim; run evidence names the selected environment; force usage is
recorded on forced recreate/remove.

## 9. Docs And Lint

```bash
markdownlint-cli2 README.md README.zh-CN.md docs specs/003-unified-named-environments
```

Expected: lint passes; PRD environment chapter and identity wording, test
plan, STATUS, and both READMEs reflect the named model, pinned image
declaration, backend/workspace drift comparison, and the `env` command family.

## Walk Results (2026-07-06, implementation session)

Steps 2, 3, 5, 6, 7, and 8 executed locally against the built binary with an
isolated store (step 4 — real Lima image boot — is operator-run via
`scripts/test-phase1.sh --env-image`):

- step 2: create/inspect verbatim declaration, reserved `default` rejected,
  collision rejected, no boot/no network — PASS;
- step 3: auto-named environment created on first native run, listed with the
  auto marker, image column, and disk usage — PASS;
- step 5: workspace drift failed closed with pinned/current values and the
  recreate hint; `env recreate` rebuilt under the same record id — PASS;
- step 6: planted v1 record listed as `unsupported-version` (id/version only)
  and lifecycle ops rejected it with clean-and-recreate guidance — PASS;
- step 7: in-workspace HostFS rule produced the shadowed-rule warning without
  blocking the run — PASS;
- step 8: `env.create` audited for both explicit and auto-named creation
  (auto-named audit gap found during the walk and fixed) — PASS.
