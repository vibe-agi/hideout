// Package export owns the export/share boundary for evidence artifacts.
//
// The boundary always reasserts the internal/audit control-plane strip, then
// applies operator-selected user-data redaction through the audit.redact policy
// evaluator. It must not reuse the broker's preserveBrokerAuditMetadata restore
// behavior: broker local audit restores Core evidentiary fields, while export
// fails closed when a selection or script tries to alter those fields.
package export
