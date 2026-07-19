package doctor

import (
	"fmt"
	"slices"

	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/recovery"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const (
	WorkspaceMetadataPassed   = "passed"
	WorkspaceMetadataExternal = "external"
	WorkspaceMetadataUnproved = "unproved"
	WorkspaceMetadataNotRun   = "not-run"
)

// WorkspaceEnvironmentObservation is the non-authority environment state that
// doctor needs to diagnose shared workspace admission and cleanup. It excludes
// host paths, root handles, credentials, and provider internals.
type WorkspaceEnvironmentObservation struct {
	ID                     string
	Mode                   string
	AutoNamed              bool
	SharedSlot             string
	ActiveSessions         int
	ActiveWorkspaceViews   int
	WorkspaceProviderState string
	OwnerHealth            string
}

// WorkspaceDiagnosticInput contains observed facts only. AddWorkspaceDiagnostics
// assigns status and recovery semantics so CLI, JSON, and future UI consumers do
// not independently reinterpret workspace failures.
type WorkspaceDiagnosticInput struct {
	Backend            string
	PathMode           string
	SelectedTransport  string
	TransportSupported bool
	ExpectedSharedSlot string
	Root               workspaceattach.HostRootPrerequisiteReport
	MetadataStatus     string
	MetadataKind       string
	Environments       []WorkspaceEnvironmentObservation
	Attachments        []workspaceattach.AttachmentSummary
	Lifecycle          []lifecycle.Status
	DaemonObserved     bool
}

// AddWorkspaceDiagnostics reports each independent prerequisite separately so
// simultaneous failures retain distinct recovery codes instead of collapsing
// into one prose summary.
func (b *Builder) AddWorkspaceDiagnostics(input WorkspaceDiagnosticInput) {
	b.addWorkspaceTransport(input)
	b.addWorkspaceRoot(input.Root)
	b.addWorkspaceMode(input)
	b.addWorkspaceMetadata(input.MetadataStatus, input.MetadataKind)
	b.addWorkspaceLifecycle(input)
}

func (b *Builder) addWorkspaceTransport(input WorkspaceDiagnosticInput) {
	details := map[string]any{
		"backend":           input.Backend,
		"selectedTransport": input.SelectedTransport,
	}
	switch {
	case input.Backend != "lima":
		b.Add("feature-workspace", "workspace", StatusUnsupported,
			"dynamic shared workspace transport is not used by this backend",
			WithRequired(false), WithDetails(details))
	case !input.TransportSupported:
		b.Add("feature-workspace", "workspace", StatusError,
			"the selected platform cannot provide the promoted shared workspace transport",
			WithRequired(false), WithDetails(details), WithRecovery(recovery.CodeWorkspaceTransportUnsupported))
	default:
		b.Add("feature-workspace", "workspace", StatusPass,
			"the promoted shared workspace transport is selected for this platform",
			WithRequired(false), WithDetails(details),
			WithEvidenceRefs("gate-required:035-real-lima-workspace-transport"))
	}
}

func (b *Builder) addWorkspaceRoot(report workspaceattach.HostRootPrerequisiteReport) {
	principalFacts := make([]string, 0, len(report.Principals))
	for _, principal := range report.Principals {
		principalFacts = append(principalFacts, fmt.Sprintf("%s:%s:%s", principal.Process, principal.Role, principal.State))
	}
	checkFacts := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		checkFacts = append(checkFacts, check.ID+"="+check.Status)
	}
	details := map[string]any{
		"schema":     report.Schema,
		"scope":      report.Scope,
		"tccStatus":  report.TCCStatus,
		"principals": principalFacts,
		"checks":     checkFacts,
	}
	switch {
	case report.Status == workspaceattach.ResearchCheckPassed:
		b.Add("feature-workspace-root", "workspace", StatusPass,
			"the selected project root can be opened, enumerated, and watched by the prerequisite probe",
			WithRequired(false), WithDetails(details))
	case report.TCCStatus == "denied":
		b.Add("feature-workspace-root", "workspace", StatusError,
			"the host denied project-root access to a required workspace principal",
			WithRequired(false), WithDetails(details), WithRecovery(recovery.CodeWorkspaceHostPermission))
	default:
		b.Add("feature-workspace-root", "workspace", StatusError,
			"the selected project root identity and watcher prerequisite could not be proved",
			WithRequired(false), WithDetails(details), WithRecovery(recovery.CodeWorkspaceRootUnstable))
	}
}

func (b *Builder) addWorkspaceMode(input WorkspaceDiagnosticInput) {
	modeStatus := StatusPass
	modeSummary := "the profile uses the neutral alias required by shared automatic mode"
	modeOptions := []FindingOption{WithRequired(false), WithDetails(map[string]any{"pathMode": input.PathMode})}
	if input.PathMode != "alias" {
		modeStatus = StatusError
		modeSummary = "shared automatic mode cannot use a profile that preserves host workspace paths"
		modeOptions = append(modeOptions, WithRecovery(recovery.CodeEnvironmentSharedPreserve))
	}
	b.Add("feature-workspace-mode", "workspace", modeStatus, modeSummary, modeOptions...)

	drifted := make([]string, 0)
	for _, environment := range input.Environments {
		if !environment.AutoNamed && environment.Mode != "shared" {
			continue
		}
		if environment.Mode != "shared" || environment.SharedSlot != input.ExpectedSharedSlot {
			drifted = append(drifted, environment.ID)
		}
	}
	driftDetails := map[string]any{
		"environmentCount": len(input.Environments),
		"driftedCount":     len(drifted),
		"drifted":          drifted,
	}
	if len(drifted) != 0 {
		b.Add("feature-workspace-mode-drift", "workspace", StatusError,
			"one or more automatic environments do not match the single shared-slot model",
			WithRequired(false), WithDetails(driftDetails), WithRecovery(recovery.CodeEnvironmentCompatibilityDrift))
		return
	}
	b.Add("feature-workspace-mode-drift", "workspace", StatusPass,
		"automatic environments match the single shared-slot model",
		WithRequired(false), WithDetails(driftDetails))
}

func (b *Builder) addWorkspaceMetadata(status, kind string) {
	details := map[string]any{"status": status, "kind": kind}
	switch status {
	case WorkspaceMetadataPassed:
		b.Add("feature-workspace-metadata", "workspace", StatusPass,
			"project metadata is representable under the neutral workspace alias",
			WithRequired(false), WithDetails(details))
	case WorkspaceMetadataExternal:
		b.Add("feature-workspace-metadata", "workspace", StatusError,
			"project metadata refers to an absolute location outside the selected workspace",
			WithRequired(false), WithDetails(details), WithRecovery(recovery.CodeWorkspaceExternalMetadata))
	case WorkspaceMetadataNotRun:
		b.Add("feature-workspace-metadata", "workspace", StatusSkipped,
			"project metadata validation did not run because an earlier root or mode prerequisite failed",
			WithRequired(false), WithDetails(details))
	default:
		b.Add("feature-workspace-metadata", "workspace", StatusError,
			"project metadata could not be proved safe for the neutral workspace alias",
			WithRequired(false), WithDetails(details), WithRecovery(recovery.CodeWorkspaceRootUnstable))
	}
}

func (b *Builder) addWorkspaceLifecycle(input WorkspaceDiagnosticInput) {
	unprovedAttachments := 0
	for _, attachment := range input.Attachments {
		if attachment.State == workspaceattach.AttachmentUnproved ||
			(attachment.CleanupProof != nil && attachment.CleanupProof.Status == workspaceattach.CleanupUnproved) {
			unprovedAttachments++
		}
	}
	blockedLifecycle := 0
	for _, status := range input.Lifecycle {
		if status.Activity == lifecycle.ActivityBlocked || status.Activity == lifecycle.ActivityStoppingUnknown {
			blockedLifecycle++
		}
	}
	ownerBlockers, providerBlockers, activeSessions, activeViews := 0, 0, 0, 0
	for _, environment := range input.Environments {
		activeSessions += environment.ActiveSessions
		activeViews += environment.ActiveWorkspaceViews
		if slices.Contains([]string{"stale", "unprovable"}, environment.OwnerHealth) {
			ownerBlockers++
		}
		if environment.WorkspaceProviderState == "unproved" {
			providerBlockers++
		}
	}
	details := map[string]any{
		"daemonObserved":       input.DaemonObserved,
		"activeSessions":       activeSessions,
		"activeWorkspaceViews": activeViews,
		"attachmentCount":      len(input.Attachments),
		"unprovedAttachments":  unprovedAttachments,
		"blockedLifecycle":     blockedLifecycle,
		"ownerBlockers":        ownerBlockers,
		"providerBlockers":     providerBlockers,
	}
	if unprovedAttachments+blockedLifecycle+ownerBlockers+providerBlockers != 0 {
		b.Add("feature-workspace-lifecycle", "workspace", StatusError,
			"workspace provider, view, owner, or machine cleanup is not proved complete",
			WithRequired(false), WithDetails(details), WithRecovery(recovery.CodeWorkspaceCleanupUnproved))
		return
	}
	if !input.DaemonObserved {
		b.Add("feature-workspace-lifecycle", "workspace", StatusSkipped,
			"no authenticated daemon inventory is available; no live workspace lifecycle claim is made",
			WithRequired(false), WithDetails(details),
			WithNextActions("hideout daemon status --human"))
		return
	}
	b.Add("feature-workspace-lifecycle", "workspace", StatusPass,
		"daemon workspace providers, views, owners, and lifecycle state have no observed blockers",
		WithRequired(false), WithDetails(details))
}
