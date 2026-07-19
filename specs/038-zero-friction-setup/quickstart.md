# Quickstart: Zero-Friction Setup Verification

This quickstart is an implementation and review guide. Commands describe the
target contract until 038 is marked implemented.

## 1. Static And Unit Gate

```bash
go test ./internal/operatorintent ./internal/manager ./internal/app \
  ./internal/inittask ./internal/profile ./internal/productevidence
go test ./internal/backend/lima
```

Verify exact grammar, setup-to-init parity, review redaction, default-no input,
pure ready inspection, cross-process stale rejection, generated-value
stability, and daemon fail-closed behavior.

## 2. Fresh Local Setup

Use an empty isolated store and a real PTY:

```text
$ hideout setup

Hideout will prepare:
  Isolation    Lima VM
  Runtime      developer-standard <revision> (preview, <declared size>)
  Workspace    the project you later run in, read/write at /workspace
  Network      direct (does not hide your public network origin)
  Other files  hidden unless you grant access
  Audit        always on

Setup writes local configuration only. It will not start a VM or download the runtime.
Continue? [y/N]: y
```

After success, verify one valid default profile and setup audit/evidence, but no
new Lima instance and no runtime cache artifact.

## 3. Negative Confirmation Matrix

Exercise:

- `n` and arbitrary non-affirmative input;
- empty line;
- EOF;
- Ctrl-C;
- non-TTY invocation; and
- control-byte-containing input.

For every case, compare the store before and after. A daemon socket/token/lock
may appear; profile, identity, onboarding evidence, passing 038 evidence, VM,
and runtime cache must not.

## 4. Existing State Matrix

Run setup against:

1. a valid default profile;
2. a valid customized default profile;
3. a profile missing safe repairable state;
4. malformed JSON;
5. unknown fields;
6. an unsafe symlinked store ancestor; and
7. unprovable ownership.

The first two are pure reads and send no apply request. Record profile digest,
metadata, identity files, evidence, and relevant directory mtimes before and
after. The remaining states fail closed with explicit recovery and no writes.

## 5. Review-To-Apply Race

Prepare a fresh setup plan, then create or mutate the default profile from a
second Manager instance before applying the first plan. Apply must return a
stale-plan error before any task executes. Repeat by changing runtime catalog
identity and prerequisite observations.

## 6. Explicit Init Parity

Compare the semantic prepared plans for:

```bash
hideout setup
```

and:

```bash
hideout init --template dev --profile default --backend lima \
  --network direct --runtime developer-standard --no-input
```

Ignore presentation-only setup wording and incidental generated times. Profile,
template, backend, network, runtime, workspace, HostFS, tasks, risks, and effect
inputs must match.

## 7. Packaged PTY Lane

Build or consume the candidate package, install it with `--skip-init`, and run
the installed binary under a PTY. Verify the review and success output, invoke
doctor, and emit `038.setup.local-fast.package-pty` evidence. This lane proves
packaging and UI only, not isolation.

## 8. Real Lima First Run

On macOS arm64 with installed Lima:

```bash
mkdir -p "$TEST_PROJECT"
git -C "$TEST_PROJECT" init
cd "$TEST_PROJECT"
hideout run -- git status --short
```

Observe the exact runtime wait notice and bounded heartbeat if startup is slow.
Inside the guest verify:

```bash
pwd
id -u
getent passwd "$(id -u)"
printf '%s\n' "$HOME"
git config --global user.name
git config --global user.email
```

Expected: `/workspace`, non-root synthetic account, account home
`/home/developer`, target `HOME=/hideout/profile/home`, and synthetic Git
identity. Validate runtime digest, audit, Boundary evidence, and final lifecycle
state from host evidence.

## 9. Agent Install And Separate Run

Use the package-owned exact fixture rather than mutable `latest`:

```bash
hideout run -- sh -lc '<exact-integrity install into $HOME/.local>'
hideout run -- codex --version
```

Verify target ownership, no `sudo`, normal PATH lookup in the second session,
expected pinned version, and absence of imported login/proxy/control-plane
credentials. Emit `038.setup.real-gate2.agent-install-run` evidence.

## 10. Exact Reuse

Record environment identity and runtime provenance before the second run. Prove
the same compatible environment is reused and no instance-creation operation
occurs. Do not infer reuse from a faster elapsed time.

## 11. Documentation Truth

After behavior and evidence exist, verify that English/Chinese README,
first-run docs, distribution docs, CLI help, source formula, published tap,
support matrix, status, claim boundaries, and privacy test plan show the same
setup-first sequence and direct-network non-claim.

## 12. Final Battery

```bash
go build ./...
go vet ./...
test -z "$(gofmt -l internal cmd)"
git diff --check
go test ./...
scripts/test-gate0.sh
```

Also run markdownlint, package smoke, first-run local-fast, packaged PTY, and
the required real Gate 2 lane. Mark 038 implemented only after the registered
proof manifests evaluate successfully.
