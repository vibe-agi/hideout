# Quickstart and Acceptance Scenarios

This document is the executable product path for the feature. Commands are
illustrative until their implementation task lands; the release gate must run
the final spellings from a clean package, not a developer binary.

## Safety prerequisites

- macOS arm64 host with supported Lima and Debian 13 runtime;
- a disposable test workspace with no credentials;
- a local test SOCKS proxy and deterministic DNS/HTTP fixtures;
- no unrelated VM mutation;
- no remote tag, GitHub Release, or Homebrew publication.

Use an isolated Hideout store and Keychain references reserved for the test.
The gate must remove them on completion and prove absence.

## 1. Discover the product as a first-time user

```sh
hideout help
hideout help connect
hideout help activity
```

Expected:

- primary help is grouped by tasks, not an undifferentiated command list;
- each task has a copyable example and a clear next step;
- contextual help states prerequisites, effect scope, safety/recovery, and
  whether a daemon or VM restart is needed;
- `hideout help all` is grouped/searchable and `hideout help --all` clearly
  identifies itself as a compatibility alias;
- no task requires more than two help invocations to find its supported path.

## 2. Configure a secret without restarting the daemon

Start or confirm the daemon, then write the secret through stdin/TTY:

```sh
hideout daemon status
printf '%s\n' 'socks5://127.0.0.1:7890' | hideout secret set local-proxy --stdin
hideout secret status local-proxy
```

Expected review:

```text
Secret:       local-proxy
Change:       missing -> available (generation 1)
Value shown:  never
Live effects: eligible proxy routes use generation 1 for new connections
Restart:      daemon no; VM no
Recovery:     prior generation retained until activation is proved
```

The apply path must require confirmation unless the explicit non-interactive
confirmation contract is used. Process inspection, shell history, daemon audit,
activity storage, console output, and support export must contain no raw value.

The old environment workflow:

```sh
export HIDEOUT_SECRET_LOCAL_PROXY='socks5://127.0.0.1:7890'
```

is compatibility-only. Help must explain that exporting after daemon start
cannot change the daemon's environment and direct users to `hideout secret set`.

## 3. Review and apply a connection change

```sh
hideout connect plan \
  --profile default \
  --through local-proxy \
  --dns 1.1.1.1
```

Expected:

- plan identifies profile revision and operation ID;
- diff separates desired and currently effective state;
- effects show stage, activate for new connections, preserve/drain existing
  connections, prove, and rollback;
- active blocking sessions are named;
- proxy ref/availability/generation is visible, value is not;
- no state changes during plan.

Apply the exact reviewed plan through the CLI's confirmation flow:

```sh
hideout connect apply <operation-id> --yes
```

`<operation-id>` is the exact ID printed by `connect plan`; `--yes` confirms
that displayed diff non-interactively. Do not substitute a newly planned ID
after an uncertain apply—inspect the original operation first. Then:

```sh
hideout connect status --profile default
hideout daemon status
```

Expected: healthy eligible changes become effective without `daemon stop` or VM
recreation. Existing accepted connections retain their bound route. A forced
stage/probe failure returns rolled-back/failed evidence and an exact recovery
action, never success.

## 4. Start a reference workload

```sh
hideout run --profile default -- sh -lc '
  printf "hello\n" > observed.txt
  mv observed.txt renamed.txt
  getent hosts example.test
  curl -fsS https://example.test/health >/dev/null
  sh -c "true"
'
```

Expected:

- target and all descendants are members of one fresh session cgroup;
- supervisor and observer are outside it;
- command/fork/exec/exit, file write/rename, DNS, and connection records belong
  to the correct session/execution;
- no unrelated concurrent fixture process is attributed;
- coverage is `Available` only for successfully probed providers;
- no environment value, file content, terminal input/full output, or packet
  payload is retained.

Repeat concurrently in two sessions and include deliberate PID reuse/rapid
children. Attribution must remain isolated by owner/session/cgroup/execution,
not PID alone.

## 5. Use the terminal HUD

```sh
hideout tui
```

Within one screen, verify the active command, effective connection, four
coverage states, highest risk/blocker, and next action. Then:

1. press `2` for Activity;
2. select the file write and press Enter;
3. inspect process ancestry, path, count/time, coverage, and risk evidence;
4. press `3` for Config;
5. select proxy/DNS and press Enter;
6. edit a draft, review diff/effects/blockers/recovery, cancel it;
7. repeat, confirm, and watch the same operation to terminal evidence.

Expected:

- cancel has zero effect;
- Enter on a field opens a modal but never immediately mutates;
- a stale/disconnected stream changes the UI to read-only;
- reconnect discards old authority and seeds a fresh snapshot;
- `Ctrl+C` restores the terminal and does not stop daemon/session/operation.

Check deterministic fallback:

```sh
hideout tui --once
```

## 6. Investigate deeper history in the browser

```sh
hideout ui
```

Filter one exact environment incarnation by time, execution, file operation,
domain/IP, coverage reason, and risk. Open the same records used in the TUI.

Expected:

- facts and terminal operation states match CLI/TUI exactly;
- the browser does not poll while SSE is healthy;
- a gap or credential rotation makes mutation controls read-only until re-seed;
- export is a separate review/apply operation with path/redaction disclosure.

## 7. Prove loss and coverage behavior

Run negative fixtures that inject:

- one ring-buffer drop;
- one observer sequence gap;
- observer restart;
- unsupported file hook/fanotify fallback;
- encrypted/external DNS;
- unresolved file path/actor;
- redaction rejection;
- activity quota pruning;
- daemon crash and reconnect.

Expected: the affected subsystem and interval becomes `Partial` or
`Unavailable` before any console presents it as complete. Other subsystem
coverage may remain available. Detail gives a stable reason and safe next step.

## 8. Prove redaction

Use unique canaries in:

- a managed secret;
- URI userinfo;
- `Authorization`/proxy auth fields;
- `--token VALUE` and `--token=VALUE`;
- sensitive query parameters;
- supported encoded and split-field forms;
- session/daemon/control tokens.

Search all activity segments/indexes, audit/event output, console snapshots,
browser responses, support exports, test logs, and release evidence. There must
be zero raw canary matches. Local authenticated ordinary paths remain visible.
A redaction failure must drop the event and degrade coverage.

## 9. Prove exact lifecycle deletion

Capture the exact current activity owner, then:

```sh
hideout clean
```

For disposable runs, exercise `--rm`/session cleanup. For reusable
environments, also test delete and recreate of the same display slot.

Expected:

- the prior exact owner's activity directory and manifest are absent;
- a concurrently retained different incarnation is untouched;
- cleanup success contains an absence proof;
- injected deletion failure produces unproved/failed lifecycle state, not
  success;
- custom legacy audit destinations are unaffected by the new activity cleanup.

## 10. Run engineering gates

The implementation task must bind these to repository scripts. At minimum:

```sh
go test ./...
go test -race ./...
```

Then run:

- all TLC configurations and Go refinement traces;
- lint, format, generated-artifact, license, dependency, vulnerability, and
  unreachable-advisory scans;
- mutation proofs for each new assertion and negative fixtures for each new
  judge;
- PTY/TUI and browser parity suites;
- real Lima observation, concurrent-session, crash/retry/drop, lifecycle, and
  online proxy rotation suites;
- performance and quota benchmarks;
- clean package install, old-to-new upgrade with disposable old data,
  uninstall, and reinstall.

Every required result must be fresh, exact, and passing. `not-run`, stale,
reduced, or unsupported is not acceptable for a claim included in the
candidate.

## 11. Produce a local release candidate

Build the exact package artifact and evidence manifest from a clean commit.
Record:

- commit and version;
- helper/runtime manifest digests;
- dependency/advisory results;
- model/test/gate evidence paths;
- supported coverage matrix and limitations;
- performance and retention defaults;
- upgrade/uninstall proof;
- final code-review findings and resolutions.

Stop after the local candidate. Creating a remote tag, GitHub Release, or
Homebrew update requires a separate explicit instruction from the operator.
