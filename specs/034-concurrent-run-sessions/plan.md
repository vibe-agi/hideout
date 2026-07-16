# Implementation Plan: Daemon-Owned Concurrent Run Sessions

<!-- markdownlint-disable MD013 MD060 -->

**Branch**: `034-concurrent-run-sessions` | **Date**: 2026-07-16 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/034-concurrent-run-sessions/spec.md`

## Summary

Replace executable embedded runs with one resident per-store control plane.
`hideout run` becomes a thin local client that parses intent, captures terminal
facts, renders confirmation, and streams interaction. The `hideoutd` process
role owns Manager Core, environment transitions, session workers, backend/SSH,
per-run providers, cleanup, credential rotation, status, and recovery.

Add a dedicated private Unix session socket and a bounded framed protocol. For
Lima, the daemon uses non-PTY root-control SSH to start a fixed packaged Linux
supervisor inside the existing per-session mount/PID view. The supervisor owns
the guest PTY, target process group, resize, signals, and reaping. This removes
the measured OpenSSH PTY-request delay while preserving unique broker, HostFS,
network, host-capability, terminal, process, and audit authority per run.

The delivery cut line is same-workspace concurrency with dynamic resize and
truthful non-interactive streams. Cross-workspace VM sharing, last-session
auto-stop, detach, browser terminals, guest-root containment, and exhaustive
theme/OSC compatibility remain outside 034.

## Technical Context

**Language/Version**: Go 1.25; strict JSON control payloads; a compact binary frame envelope; trusted POSIX shell generated only by the Lima backend

**Primary Dependencies**: Existing Manager, daemon, backend/Lima, session, environment, network, HostFS, broker, host-capability, helperbin, `golang.org/x/crypto/ssh`, `golang.org/x/term`, `golang.org/x/sys`, and `github.com/creack/pty` for the fixed Linux supervisor PTY; dependency metadata is resolved through `go mod tidy`

**Storage**: Existing private store, daemon runtime, environment records, owner records, per-session runtime children, audit files, and helper manifests; rotating daemon token is atomically persisted, while live workers and per-run credentials are memory/session scoped

**Testing**: Go unit/race/contract tests, protocol fuzz-style malformed fixtures, Manager/CLI parity, auto-start races, credential rotation, helper cross-build/tests, package verification, Gate 0 smoke, real PTY tests, and real macOS arm64 Lima Gate 2 evidence

**Target Platform**: Product claim is macOS arm64 with Lima and the supported Linux guest runtime; native remains a weak mechanics harness and cannot supply isolation evidence

**Project Type**: Go CLI/client plus a local single-user daemon role, canonical Manager Core, VM backend, and a fixed Linux guest helper

**Performance Goals**: Warm real-terminal invocation to first target byte is at most 2.0 seconds p95 across at least 20 samples; no command-specific fast path; protocol framing adds no silent output loss or unbounded buffering

**Constraints**: No embedded executable fallback, no browser terminal authority, no generic host/guest/root action, no client-visible backend or per-run credentials, no SSH `RequestPty` for the new path, no workspace transport change, no automatic VM stop, no detach, and no guest-root containment claim

**Scale/Scope**: One operator and store, one daemon, one pinned workspace per environment, up to 16 concurrent active sessions, multi-hour sessions with token renewal, bounded 64 KiB data frames, and bounded per-connection queues

## Constitution Check

*GATE: Passed before research and re-checked after Phase 1 design.*

- **Privacy Boundary - PASS**: Daemon connection, authentication, plan drift,
  confirmation, protocol mismatch, supervisor failure, lease expiry, ownership
  ambiguity, isolation failure, active-session stop, and cleanup failure all
  fail closed. There is no ambient or embedded backend fallback.
- **Typed Authority - PASS**: Manager and existing Go providers remain the only
  authority. The client carries typed intent and terminal data. The fixed guest
  helper can start only the daemon-built session contract and cannot express a
  generic privileged command.
- **Workspace And Policy - PASS**: The direct workspace mount remains the sole
  intentional shared write surface. Broker, HostFS, decisions, staged writes,
  network runtime, host applications, credentials, process view, and terminal
  stay session scoped.
- **Generality And Provider Scope - PASS**: PTY selection is based on terminal
  capability or an explicit mode, never a shell/agent command list. Bash,
  Claude, Codex, Git, and full-screen programs are fixtures only.
- **Evidence And Redaction - PASS**: One daemon worker/owner model feeds status,
  Manager, CLI, events, doctor, and audit. Protocol diagnostics, status, and
  evidence exclude operator tokens, per-run credentials, raw authority paths,
  proxy secrets, and target-controlled terminal content unless the existing
  local evidence contract explicitly includes it.
- **Lifecycle - PASS**: The daemon is Core's resident lifecycle owner; the
  supervisor owns guest PTY/process lifetime. Client loss cancels rather than
  detaches, daemon loss terminates guest work through transport ownership, and
  restart does not silently adopt or destroy ambiguous state.
- **Distribution - PASS**: The Linux supervisor uses the existing verified
  helper build/manifest/package/install model. Missing, stale, wrong-arch, or
  digest-mismatched helpers fail before target authority. Dependency metadata
  is changed only through Go tooling and `go mod tidy`.
- **Gates - PASS**: Gate 0 proves model, protocol, auth, race, parity, packaging,
  redaction, and failure behavior. Real PTY and real Lima lanes prove latency,
  resize, terminal restoration, namespaces, crash behavior, and sibling
  survival. Pipe timing cannot substitute for terminal evidence.

Post-design re-check: all items remain PASS. 034 intentionally supersedes the
early explicit-opt-in daemon and daemon-less run constraints, but preserves the
deeper constitutional rule that the daemon is a runtime for typed Manager/Core
authority rather than a new authority language. No waiver is required.

## Project Structure

### Documentation (this feature)

```text
specs/034-concurrent-run-sessions/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── client-daemon-session.md
│   ├── guest-supervisor.md
│   ├── run-service.md
│   └── session-ownership.md
├── checklists/
│   └── requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
cmd/
└── hideout-session-supervisor/
    ├── main_linux.go              # fixed guest PTY/process supervisor
    └── main_other.go              # unsupported-platform refusal

internal/
├── sessionwire/
│   ├── frame.go                   # bounded binary envelope and frame catalog
│   ├── control.go                 # strict control payloads and validation
│   └── stream.go                  # serialized writer and bounded reader
├── daemon/
│   ├── credential.go              # token rotation and validation
│   ├── autostart.go               # race-safe detached daemon readiness
│   ├── session_transport.go       # private session listener
│   ├── session_server.go          # authenticated one-connection/one-run worker
│   ├── session_client.go          # thin-client dial and renewal support
│   ├── sessions.go                # live worker registry and shutdown
│   ├── daemon.go                  # composition and ordered lifecycle
│   ├── server.go                  # existing Manager/status/events HTTP surface
│   └── status.go                  # redacted session inventory
├── manager/
│   ├── run_service.go             # canonical structured plan/apply entry point
│   ├── run_apply.go               # existing run lifecycle implementation
│   ├── run_session.go             # unique runtime and audit ownership
│   ├── run_dataplane.go           # session-scoped providers/helpers
│   ├── run_environment.go         # transition and owner state
│   ├── environment_lifecycle.go   # stop refusal
│   ├── api.go                     # HTTP delegates to canonical run service
│   └── manager.go                 # authoritative summaries
├── backend/
│   ├── backend.go                 # stream/session contract
│   └── lima/
│       ├── supervisor.go          # non-PTY SSH supervisor bridge
│       ├── session_view.go        # fixed namespace launcher/cleanup proof
│       ├── ssh_bridge.go          # authenticated SSH transport
│       └── lima.go                # activation and helper materialization
├── session/
│   └── ownership.go              # durable liveness/crash evidence
├── helperbin/
│   └── helperbin.go              # supervisor resolve/build/manifest support
├── recovery/
│   └── registry.go               # stable daemon/session/helper recovery codes
└── app/
    ├── app.go                     # thin CLI parsing/render/raw terminal loop
    └── terminal_client.go         # terminal mode/resize/signal/restoration

schemas/
├── active-session-summary.schema.json
└── daemon-status.schema.json

scripts/
├── test-daemon-session-smoke.sh
├── test-concurrent-sessions-e2e.sh
├── test-gate0.sh
└── lib/gate2-concurrent-sessions.sh

docs/
├── architecture-principles.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
├── threat-model.md
├── claim-boundaries.md
├── support-matrix.md
└── STATUS.md
```

**Structure Decision**: Add one protocol package, one daemon session service,
and one fixed Linux helper. Keep Manager as the authority/lifecycle domain and
Lima as the backend provider. `internal/app` remains presentation and process
composition only. Do not add an alternate scheduler, generic remote shell, or
second host binary contract.

## Complexity Tracking

| Addition | Why Required | Simpler Alternative Rejected Because |
|----------|--------------|---------------------------------------|
| Dedicated session Unix socket | Terminal ownership needs one full-duplex, backpressured connection whose loss cancels one run | SSE/HTTP polling cannot preserve binary terminal and ownership semantics |
| Fixed Linux supervisor helper | Guest-side PTY allocation removes the measured SSH PTY delay and owns resize/process-group cleanup | SSH `RequestPty` is the measured bottleneck; shell `script` lacks the typed control contract |
| Rotating credential manager | Multi-hour daemon/API/session use must survive expiry while stale tokens lose access | A longer static TTL does not implement rotation or revocation |
| Canonical Manager run service | Thin CLI and Manager API must retain every existing run option without duplicate orchestration | Copying CLI logic into daemon would drift and put lifecycle policy in the presentation package |

None of these additions creates a new authority action. Each removes an
existing ownership ambiguity or implements a required lifecycle/terminal
contract.
