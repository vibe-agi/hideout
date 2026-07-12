package runtimeverify

import (
	"slices"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/recovery"
)

type StatusView struct {
	Status          string        `json:"status"`
	Family          string        `json:"family,omitempty"`
	Revision        string        `json:"revision,omitempty"`
	Maturity        string        `json:"maturity,omitempty"`
	ArtifactSHA256  string        `json:"artifactSHA256,omitempty"`
	Running         bool          `json:"running"`
	ObservedAt      string        `json:"observedAt,omitempty"`
	LastStatus      string        `json:"lastStatus,omitempty"`
	FailedIDs       []string      `json:"failedIds,omitempty"`
	PrivilegeStatus string        `json:"privilegeStatus,omitempty"`
	RecoveryCode    string        `json:"recoveryCode,omitempty"`
	Recovery        *RecoveryView `json:"recovery,omitempty"`
	Reason          string        `json:"reason,omitempty"`
}

type RecoveryView struct {
	Code        string   `json:"code"`
	Reason      string   `json:"reason"`
	Hint        string   `json:"hint"`
	NextActions []string `json:"nextActions,omitempty"`
	DocsRefs    []string `json:"docsRefs,omitempty"`
}

func BuildStatus(record environment.Record, running bool, receipt *Receipt) StatusView {
	view := StatusView{Running: running}
	if record.Runtime == nil {
		view.Status = StatusCustomUnverified
		view.Reason = "environment has no package-catalog runtime provenance"
		return view
	}
	view.Family = record.Runtime.Family
	view.Revision = record.Runtime.Revision
	view.Maturity = record.Runtime.Maturity
	view.ArtifactSHA256 = record.Runtime.ArtifactSHA256
	if !running {
		view.Status = StatusNotRunning
		view.Reason = "guest is stopped; last observation is context, not current readiness"
		if receiptMatches(record, receipt) {
			copyReceiptContext(&view, *receipt)
			view.LastStatus = receipt.Status
		}
		return view
	}
	if !receiptMatches(record, receipt) {
		view.Status = StatusUnknown
		view.Reason = "no valid current runtime verification receipt"
		return view
	}
	copyReceiptContext(&view, *receipt)
	if receipt.Backend != "lima" || !receipt.BackendReal || !receipt.Running {
		view.Status = StatusUnknown
		view.Reason = "runtime observation is not real running Lima evidence"
		return view
	}
	view.Status = receipt.Status
	return view
}

func receiptMatches(record environment.Record, receipt *Receipt) bool {
	if receipt == nil || record.Runtime == nil || receipt.Validate() != nil {
		return false
	}
	return receipt.EnvironmentID == record.ID &&
		receipt.ImageRef == record.ImageRef &&
		receipt.SessionID == record.LastSessionID &&
		!receipt.ObservedAt.Before(record.LastStartedAt) &&
		receipt.ContractDigest == record.Runtime.ContractDigest &&
		receipt.Provenance == *record.Runtime
}

func copyReceiptContext(view *StatusView, receipt Receipt) {
	view.ObservedAt = receipt.ObservedAt.UTC().Format(time.RFC3339)
	view.FailedIDs = slices.Clone(receipt.FailedIDs)
	view.PrivilegeStatus = receipt.PrivilegeStatus
	view.RecoveryCode = receipt.RecoveryCode
	if record, ok := recovery.Lookup(receipt.RecoveryCode); ok {
		view.Recovery = &RecoveryView{
			Code: record.Code, Reason: record.Reason, Hint: record.Hint,
			NextActions: slices.Clone(record.NextActions), DocsRefs: slices.Clone(record.DocsRefs),
		}
	}
}
