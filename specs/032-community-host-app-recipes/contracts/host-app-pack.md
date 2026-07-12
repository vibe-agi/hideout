# Contract: Host-App Pack V1

<!-- markdownlint-disable MD013 MD060 -->

## File And Source

Manifest file: `hideout.host-app-pack.json`

Schema: `hideout.host-app-pack/v1`

V1 accepts a local directory or a git URL plus exact 40-hex commit. Intake:

1. uses an isolated git configuration and no prompt, hooks, filters, or
   submodule recursion;
2. copies only regular bounded files, rejecting symlinks and special nodes;
3. validates the strict manifest and file references;
4. computes source, manifest, and permission digests;
5. removes acquisition state;
6. at apply, reacquires and requires exact plan digest equality before atomic
   publication to a private Core-owned revision directory.

Runtime never reads the original source.

## Manifest Shape

```json
{
  "schemaVersion": "hideout.host-app-pack/v1",
  "id": "community.cursor",
  "version": "1.0.0",
  "description": "Cursor host application projection",
  "apps": [
    {
      "id": "cursor",
      "platforms": ["darwin"],
      "bundleNames": ["Cursor.app"],
      "executableRelativePath": "Contents/MacOS/Cursor",
      "expectedBundleId": "com.todesktop.230313mzl4w4u92",
      "expectedTeamId": "PACKAGE_EXPECTATION_ONLY",
      "requestedSafetyProfile": "vscode-family-v1",
      "launch": {
        "gotoFlag": "--goto",
        "newWindowFlag": "--new-window",
        "reuseWindowFlag": "--reuse-window",
        "gotoSeparator": ":"
      }
    }
  ],
  "bindings": [
    {
      "id": "cursor-command",
      "commands": ["cursor"],
      "appId": "cursor",
      "capabilityId": "host.app.open-resource",
      "resourceKinds": ["workspace", "hostfs-portal"],
      "resultPolicy": "none",
      "requestedAccess": "ask-each-run",
      "grammar": {
        "kind": "open-resource-v1",
        "resourceCount": 1,
        "gotoFlags": ["-g", "--goto"],
        "newWindowFlags": ["-n", "--new-window"],
        "reuseWindowFlags": ["-r", "--reuse-window"],
        "unknownFlags": "deny"
      }
    }
  ],
  "tests": []
}
```

Package identity expectations never authenticate the app. Launch fields are
bounded recipe data, not raw invocation argv. Community data cannot define a
safety profile, executable hook, script parser, capability, provider, result
channel, or profile mutation.

## Permission Diff

Inspection and enable plans report added/removed/changed canonical permission
items. The revision base fingerprint covers all package authority fields. The
enablement effective fingerprint also covers selected access and the exact
Core safety-profile id/version; a Core safety-profile version change is a
visible permission change. Package prose is excluded. An update may install as
a candidate, but an active enablement becomes suspended until the new effective
fingerprint is accepted.

## Tests

Package tests may parse representative argv and compare unbound resource,
location, and window outputs. They have no host app resolver, filesystem,
network, process, token, Manager, profile, or provider access. Missing or failed
tests remain visible quality facts but never bypass Core validation.

## Limits

- 256 files, 4 MiB total, 256 KiB per file;
- 16 apps, 32 bindings, 16 command aliases per binding;
- 64 total projected command names per profile;
- all identifiers, descriptions, flags, and hints are bounded and control-
  character free after one shared Core sanitizer; human renderers additionally
  reject ANSI/OSC escape sequences and label package prose as untrusted;
- unknown fields fail closed.
