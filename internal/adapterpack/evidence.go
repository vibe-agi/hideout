package adapterpack

import "github.com/vibe-agi/hideout/internal/audit"

const Action = "adapter.pack"

func Evidence(operation, decision string, entry RegistryEntry, rev Revision, reason string) audit.Event {
	details := map[string]any{
		"operation": operation,
		"packId":    entry.PackID,
		"state":     entry.State,
	}
	if rev.RevisionID != "" {
		details["revisionId"] = rev.RevisionID
		details["version"] = rev.Version
		details["manifestDigest"] = rev.ManifestDigest
		details["sourceDigest"] = rev.SourceDigest
		details["testStatus"] = rev.TestStatus
	}
	if reason != "" {
		details["reason"] = reason
	}
	return audit.Event{
		Profile:  "default",
		Backend:  "native",
		Action:   Action,
		Decision: decision,
		Details:  details,
	}
}

func RedactedDetails(details map[string]any) map[string]any {
	return audit.RedactDetails(details)
}
