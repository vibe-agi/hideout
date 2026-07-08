# Contract: Built-In Root-Sensitive Adapter

<!-- markdownlint-disable MD013 -->

## Purpose

The root-sensitive adapter detects command-name invocations that indicate guest
privilege escalation or system mutation intent. In 008 it is an intent/audit
layer, not a privilege boundary.

## Covered Categories

The built-in adapter must classify at least:

- escalation: `sudo`, `su`, `doas`;
- package manager: `apt`, `apt-get`, `dnf`, `yum`, `apk`, `pacman`,
  `brew` when used in guest context;
- mount: `mount`, `umount`;
- network mutation: `iptables`, `nft`, `ip`, `route`, `pfctl`;
- resolver: `resolvectl`, `systemd-resolve`, writes to resolver config via
  command-name routes when visible;
- service manager: `systemctl`, `service`, `launchctl` in guest context;
- system management: `sysctl`, `modprobe`, `kmod`.

## Required Outcomes

- Destructive or privileged mutation attempts deny by default.
- Package install attempts may deny or propose `guest.privilege.plan`.
- The adapter must not return successful simulation for system mutation.
- Evidence must include category, command, bounded argv summary, outcome,
  reason, and separation status.

## Separation Status

Allowed status values:

- `intent-only`: 008 command-name intent capture without 009 enforcement.
- `enforced-009`: later privilege separation confirms target user has no sudo
  path and setup identity is separate.
- `degraded-009`: later check found passwordless sudo or another degraded
  separation condition.
- `unknown`: separation status could not be established.

008 must emit `intent-only` or `unknown`; it must not emit `enforced-009`.

## Non-Claims

The adapter does not intercept:

- absolute paths such as `/usr/bin/sudo`;
- direct syscalls or setuid binaries;
- guest-root processes;
- commands not routed through the command proxy path;
- target behavior outside command-name invocation.

## Test Requirements

Automated tests must prove classification for escalation, package manager,
network mutation, resolver, and service-manager categories. Tests and evidence
must not claim that 008 blocks root escalation.
