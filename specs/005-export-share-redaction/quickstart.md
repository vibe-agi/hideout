<!-- markdownlint-disable MD013 -->

# Quickstart: Export/Share Redaction Boundary

Validation guide. Each scenario maps a requirement to unit or Gate 0 evidence.
This feature makes a data-handling/redaction claim, not an isolation claim, so no
real-Lima gate is used.

## 1. Build And Static Gates

```bash
go test ./...
scripts/test-gate0.sh
```

Expected: green. New coverage: the `internal/export` two-stage redaction and
fail-closed paths; the `schemas/export-artifact.schema.json` validation; and the
exported-artifact-cleanliness static check in Gate 0.

## 2. Control-Plane Strip And Provenance On Export (unit + Gate 0) — FR-001, FR-002, FR-009, SC-001

Seed an evidence source with known control-plane material (a `HIDEOUT_SECRET_*`
value, a `cap_`/`ui_` token value, a `capabilityToken` field, a
`machineId=<32 hex>`) and user data, export it, and assert none of the
control-plane material appears in the artifact. Run for all three sources
(`audit`, `bundle`, `boundary-summary`) — the single mediated surface (FR-001) —
so the re-asserted strip covers the shell-built bundle and the
`BoundarySummary.auditPath`. Also assert the artifact carries provenance (source,
commit where applicable, redaction stages applied) so a recipient can tell what
was scrubbed (FR-009).

## 3. User-Data Redaction Uses Export Semantics (unit) — FR-003, FR-010, SC-006, SC-007

- With an `audit.redact` selection that scrubs a user field, export and assert the
  field is redacted while an unselected user field is preserved (SC-006).
- With a selection that targets a Non-Redactable Evidentiary Set field
  (`command`, `route`, ...), assert the export **fails closed** — no artifact —
  rather than the broker's local restore behavior (SC-007). Confirm the broker's
  local record path (`preserveBrokerAuditMetadata`) is unchanged.

## 4. Missing Decision Fails Closed (unit) — FR-004, FR-012, SC-002

With user data present and no selection, no `--acknowledge-full-fidelity`, and no
interactive terminal, assert the export is refused, no artifact is written, and
the diagnostic names the missing decision. Assert no partial file remains.

## 5. Acknowledge Covers Only Residual (unit) — FR-013, SC-008

With a configured `audit.redact` policy AND `--acknowledge-full-fidelity`, assert
the policy-selected fields are still redacted (acknowledgment did not bypass the
policy) and only residual unselected data ships verbatim.

## 6. Empty Export Is Valid, Not An Error (unit) — Edge Cases

Export a filter that matches nothing; assert a valid zero-record artifact with a
"0 records matched" notice and a zero exit, not an error and not a missing file.

## 7. Local Artifact Only; References Resolved (unit) — FR-006, FR-011

- Assert `--out` accepts only a local path and the command performs no send.
- For a Boundary Summary with an `auditPath`, and a bundle referencing logs,
  assert the artifact inlines resolved-and-redacted content and contains no
  dangling local reference.

## 8. Every Export Emits A Redacted Meta-Audit Event (unit) — FR-007, SC-005

Assert each export (success and fail-closed) emits a local `audit.Event`
recording the source, record count, redaction stages, and decision. Assert it
passes the deterministic control-plane strip (`RedactDetails`, no control-plane
secret) and, being a summary, embeds no source evidence content. Do NOT assert
export stage-2 user-data rules here — the meta-audit is a local summary; its
summary fields (for example the `out` path) are kept verbatim locally, and export
stage 2 applies only if the meta-audit event is itself later exported.

## 9. Local Full-Fidelity Surfaces Unchanged (unit) — FR-005, SC-004

Assert `hideout audit show` and the local JSONL still present user/application
data verbatim after the feature ships; the export boundary changes only artifacts
leaving the box.

## 10. Pre-Export Review Is Accurate And Redacted (unit) — FR-008, SC-003

Assert the pre-export review is built by a shared builder from authoritative
export facts, is consumed by BOTH the CLI TTY path and the Manager plan, lists the
included vs. redacted content and the decision required, and is itself
control-plane redacted (no `HIDEOUT_SECRET_*`/token/machine-id leaks in the
review). See `internal/export/review_test.go`.

## 11. Manager Plan/Apply Parity (unit) — FR-004, SC-008

Assert the Manager typed `evidence.export` op uses the same Go export application
as the CLI: plan returns the same pre-export review; apply produces the same
artifact; policy failure and missing-decision fail closed identically to the CLI;
`--acknowledge-full-fidelity` does not bypass a configured policy. See
`internal/manager/export_test.go`.

## 12. Schema And Docs (Gate 0)

`scripts/test-gate0.sh` validates `schemas/export-artifact.schema.json` and the
doc updates (`docs/STATUS.md`, `docs/threat-model.md`,
`docs/privacy-run-design.md`, `docs/privacy-run-test-plan.md`), and runs the
exported-artifact-cleanliness check.
