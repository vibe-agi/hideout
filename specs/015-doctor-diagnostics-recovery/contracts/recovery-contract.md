# Contract: Doctor Recovery

<!-- markdownlint-disable MD013 -->

## Safe Recovery

Safe recovery is limited to repairs that can be represented as existing typed initialization or metadata repair tasks. Examples include missing store directories, missing profile metadata, schema metadata repair, helper manifest repair, and other already-safe InitTask repairs.

## Refused Recovery

Doctor must refuse automatic apply for:

- deleting audit or evidence;
- purging durable store state;
- removing adapter packs;
- recreating or destroying environments;
- changing backend isolation posture;
- broadening HostFS, network, command adapter, daemon, or profile authority;
- running arbitrary shell commands.

## Dry-Run

Dry-run:

- computes the same eligible/refused finding set as apply;
- prints the typed repair plan;
- writes no durable state;
- exits nonzero only when required diagnostics failed and no safe plan can be produced.

## Apply

Apply:

- requires explicit `--apply`;
- delegates eligible repairs to typed InitTask plan/apply;
- writes audit evidence for every applied repair;
- reports refused high-risk findings separately;
- fails closed if the plan changes incompatibly between dry-run and apply.
