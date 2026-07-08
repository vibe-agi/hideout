# Contract: Lima Setup Identity

<!-- markdownlint-disable MD013 -->

## Purpose

Lima enforced status requires a setup identity that is separate from the target
user. The setup identity exists only for Hideout-owned setup and cleanup.

## Target Identity

The target identity:

- is the profile guest user;
- runs untrusted target commands;
- has a non-zero UID;
- has no passwordless sudo path in enforced environments;
- cannot read setup credentials.

## Setup Identity

The setup identity:

- is Hideout-owned control-plane authority;
- may be root/control SSH or a later root-owned helper;
- is created during system provisioning for Hideout-created Lima environments;
- runs only fixed Go-owned setup/cleanup commands;
- is never exposed as a target command, JavaScript capability, command adapter
  outcome, shell prompt, or raw Manager API action.

## Allowed Setup Categories

- network setup and cleanup;
- DNS mediation setup and cleanup;
- HostFS mount setup and cleanup;
- future Go-owned privileged apply operations after their own specs.

## Forbidden Uses

- package installation as a product path in 009;
- arbitrary shell execution;
- JavaScript-triggered privileged execution;
- target-requested root command execution;
- storing private key material under `/hideout/session/shims`,
  `/hideout/session/network`, workspace mounts, or other target-writable paths.

## Compatibility

Existing environments that lack setup identity metadata are not upgraded in
place. They report `degraded` or `unknown` and receive recreate guidance.

## Failure Behavior

- Missing setup identity for a requested setup action fails closed.
- Setup identity credential permission errors fail closed.
- Setup identity authentication failure fails closed for requested setup and
  reports `unknown` or `degraded` status based on available target checks.
