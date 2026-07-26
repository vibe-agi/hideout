# Contract: Ordinary User Release Acceptance

## Candidate inputs

- clean public source commit;
- one retained package archive;
- package manifest and digest;
- release version/tag/channel;
- exact Gate 2 and Gate 3 evidence;
- required UI E2E evidence;
- signing and notarization observations.

## Required journeys

The same package digest must be observed for:

1. clean install without Go/source state;
2. concise and expanded help;
3. interactive setup and cancellation;
4. concise healthy and failing doctor;
5. support report and adversarial redaction;
6. direct first real command;
7. package-owned privacy helper and Gate 3;
8. upgrade and repair from the supported prior package;
9. normal uninstall preservation and explicit purge;
10. required TUI/WebUI behavior;
11. cleanup/residue check.

## Evidence output

The acceptance runner emits registered product evidence:

```text
featureId: 044-ordinary-user-release
evidenceClass: ordinary-user-release
candidate:
  version
  commit
  packageSHA256
  manifestSHA256
  clean
  public
journeys:
  install
  help
  setup
  doctor
  support
  directFirstRun
  privacy
  upgrade
  repair
  uninstall
  ui
realGates:
  gate2
  gate3
release:
  signing
  notarization
  anonymousDownload
  publicationReceipt
redactionStatus
cleanupStatus
status
```

Required missing, failed, stale, mismatched, or `not-run` fields make overall
status non-passing.

## Candidate invariants

- Every journey uses the same package SHA-256.
- Signing and notarization observe the retained package tree/archive without a
  rebuild.
- Public download bytes equal the retained candidate.
- Publication receipt references the same version, tag, commit, package digest,
  and evidence digest.
- A private-only or unpushed commit is not a publishable candidate.

## Cleanup

The runner inventories candidate-created sessions, environments, temporary
stores, package prefixes, and support reports. Completion requires zero
unaccounted residue; retained evidence is explicit and outside the user store.
