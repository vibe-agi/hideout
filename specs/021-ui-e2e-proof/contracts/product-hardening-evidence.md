# Contract: Product-Hardening Evidence

<!-- markdownlint-disable MD013 -->

## Schema

The manifest version is `hideout.product-hardening-evidence/v1`.

Example:

```json
{
  "version": "hideout.product-hardening-evidence/v1",
  "generatedAt": "2026-07-09T00:00:00Z",
  "commit": "abc123",
  "dirty": false,
  "packageIdentity": {
    "name": "hideout",
    "version": "2026-07-alpha"
  },
  "proofs": [
    {
      "proofId": "021.webui.browser.console",
      "featureId": "021-ui-e2e-proof",
      "mode": "browser-e2e",
      "evidenceClass": "local-ui-e2e",
      "status": "passed",
      "commandSummary": "scripts/test-ui-e2e.sh --browser --out <evidence-dir>",
      "coveredClaims": [
        {
          "claimId": "021.FR-001",
          "source": "spec",
          "description": "WebUI opens in a real local browser context",
          "scope": "browser"
        }
      ],
      "prerequisites": [
        {
          "name": "browser",
          "status": "available"
        }
      ],
      "artifacts": [
        {
          "kind": "screenshot",
          "path": "webui-console.png",
          "sha256": "SHA256HEX",
          "redactionStatus": "passed",
          "description": "Visible WebUI console after live event"
        }
      ],
      "redactionStatus": "passed",
      "startedAt": "2026-07-09T00:00:00Z",
      "endedAt": "2026-07-09T00:00:02Z"
    }
  ]
}
```

## Required Manifest Rules

- `version` MUST equal `hideout.product-hardening-evidence/v1`.
- `proofs` MUST contain one entry per requested proof lane.
- `proofId` MUST be stable across runs for the same proof contract.
- `status` MUST be one of `passed`, `failed`, or `not-run`.
- `not-run` entries MUST include a missing/skipped prerequisite or explicit
  reason in the proof entry.
- `coveredClaims` MUST NOT be treated as satisfied unless `status=passed` and
  `redactionStatus=passed`.
- `commandSummary` MUST be redacted and MUST NOT include raw tokens or hidden
  credential paths.
- Artifact paths SHOULD be relative to the evidence directory.
- Artifact digests SHOULD be recorded for durable proof artifacts.

## Required Proof Ids For 021

- `021.webui.browser.console`: real browser opens the WebUI and sees required
  console state.
- `021.webui.browser.live-update`: healthy stream event changes visible state
  without hidden polling.
- `021.webui.browser.notice-ack`: notice acknowledgement round trip.
- `021.webui.browser.auth-refusal`: wrong/missing/expired token refusal.
- `021.tui.pty.console`: real `hideout tui` terminal output.
- `021.tui.pty.live-update`: daemon event changes terminal output without
  hidden interval polling.
- `021.tui.pty.no-interval-polling`: healthy daemon stream does not interval
  poll terminal output.
- `021.tui.pty.fallback`: stream closure or credential invalidation is visible.
- `021.evidence.schema`: evidence manifest schema validation.
- `021.docs.boundary`: documentation distinguishes local UI E2E proof from
  reducer-only proof, test-only automation, and release readiness.

## Redaction Contract

Evidence MUST NOT expose:

- daemon or UI tokens;
- decision claim tokens;
- proxy secrets or `HIDEOUT_SECRET_*` backing material;
- generated machine ids;
- hidden runtime credential paths;
- raw staged HostFS content;
- raw request bodies when they contain control-plane material.

If redaction fails, the proof entry MUST be `failed` or have
`redactionStatus=failed`; downstream tooling MUST NOT mark covered claims as
satisfied.

## Relationship To Release Readiness

This manifest is input evidence. It does not by itself make a release candidate
ready. 016 release-readiness logic may consume these entries later, but release
readiness remains governed by the release readiness schema and by real Gate 2
and Gate 3 requirements where applicable.
