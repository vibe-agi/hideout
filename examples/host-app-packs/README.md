# Host-App Pack Examples

These examples show the declarative `hideout.host-app-pack/v1` shape. They are
quality fixtures, not reviewed publishers or security certifications.

A recipe may name commands, macOS bundle basenames, one relative executable,
the closed `open-resource-v1` grammar, and existing resource classes. It cannot
provide a host path, executable hook, JavaScript parser, shell command, safety
profile, capability provider, result channel, or profile mutation.

Before enabling a copied recipe:

1. replace illustrative app identity constraints with facts for the app you
   intend to use;
2. run `hideout app validate <directory>` and `hideout app test <directory>`;
3. inspect the Core-observed app identity and permission fingerprint;
4. choose the offered access posture explicitly;
5. start a new Hideout run because existing sessions never receive new shims.

Package tests only check argv parsing. A passing test does not make a recipe or
host application safe.
