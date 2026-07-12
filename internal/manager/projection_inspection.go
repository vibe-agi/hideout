package manager

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/recovery"
)

const ProjectionInspectionVersion = "hideout.projection-inspection/v1"

type ProjectionCapabilitySummary struct {
	ID             string `json:"id"`
	RiskClass      string `json:"riskClass"`
	ResultPolicy   string `json:"resultPolicy"`
	DecisionPolicy string `json:"decisionPolicy"`
	Status         string `json:"status"`
}

type ProjectionBindingSummary struct {
	Command    string `json:"command"`
	Action     string `json:"action"`
	Grammar    string `json:"grammar"`
	Resolution string `json:"resolution"`
}

// ProjectionInspection is a host-path-free diagnostic read model. Policy and
// observed facts are separate so a host-side doctor cannot turn a configured
// shim order into a false claim about a real guest PATH.
type ProjectionInspection struct {
	Version               string                        `json:"version"`
	Status                string                        `json:"status"`
	Profile               string                        `json:"profile"`
	ProfileStatus         string                        `json:"profileStatus"`
	EnvironmentStatus     string                        `json:"environmentStatus"`
	EnvironmentCount      int                           `json:"environmentCount"`
	ProjectedCapabilities []ProjectionCapabilitySummary `json:"projectedCapabilities"`
	Bindings              []ProjectionBindingSummary    `json:"bindings"`
	RequestedMode         string                        `json:"requestedMode"`
	ApprovedGrantRecords  int                           `json:"approvedGrantRecords"`
	LiveGrantStatus       string                        `json:"liveGrantStatus"`
	ProviderStatus        string                        `json:"providerStatus"`
	ProviderCode          string                        `json:"providerCode,omitempty"`
	PathShadowPolicy      string                        `json:"pathShadowPolicy"`
	PathShadowObservation string                        `json:"pathShadowObservation"`
	GateRequired          []string                      `json:"gateRequired,omitempty"`
	HostApps              hostapppack.Inspection        `json:"hostApps"`
	RecoveryCodes         []string                      `json:"recoveryCodes,omitempty"`
}

func (c Core) ProjectionInspection(profileName string) (ProjectionInspection, error) {
	profileName, err := normalizeProfileNameForProjection(profileName)
	if err != nil {
		return ProjectionInspection{}, err
	}
	out := ProjectionInspection{
		Version:               ProjectionInspectionVersion,
		Status:                "warn",
		Profile:               profileName,
		ProfileStatus:         "missing",
		EnvironmentStatus:     "not-found",
		RequestedMode:         ReadProjectionIdeMode(c.Store.Root, profileName),
		LiveGrantStatus:       "absent",
		ProviderStatus:        "unknown",
		PathShadowPolicy:      "projected shim first; projection refusal has no fallback to guest or host commands",
		PathShadowObservation: "not-run",
		GateRequired:          []string{"real macOS arm64 Lima Gate 2 observes guest PATH resolution and code projection"},
	}
	out.HostApps, out.RecoveryCodes, err = c.hostAppInspectionWithRecovery(profileName, "")
	if err != nil {
		return ProjectionInspection{}, err
	}
	for _, descriptor := range hostcap.Registry() {
		out.ProjectedCapabilities = append(out.ProjectedCapabilities, ProjectionCapabilitySummary{
			ID: descriptor.ID, RiskClass: string(descriptor.RiskClass), ResultPolicy: string(descriptor.ResultPolicy),
			DecisionPolicy: string(descriptor.DecisionPolicy), Status: string(descriptor.Status),
		})
	}
	_, registrations, catalogErr := c.CompileHostAppCatalog(profileName, "inspection", nil)
	for _, binding := range registrations {
		out.Bindings = append(out.Bindings, ProjectionBindingSummary{
			Command: binding.Name, Action: binding.Action, Grammar: binding.ArgvSchema,
			Resolution: "immutable run binding; session shim before guest PATH; no fallback on refusal",
		})
	}

	if _, err := c.Store.Load(profileName); err == nil {
		out.ProfileStatus = "loaded"
	} else if !errors.Is(err, os.ErrNotExist) {
		out.ProfileStatus = "error"
		out.Status = "error"
		return out, nil
	}
	records, err := (environment.Store{Root: c.Store.Root}).List()
	if err != nil {
		out.EnvironmentStatus = "error"
		out.Status = "error"
		return out, nil
	}
	for _, record := range records {
		if record.Profile == profileName {
			out.EnvironmentCount++
		}
	}
	if out.EnvironmentCount > 0 {
		out.EnvironmentStatus = "present"
	}

	if catalogErr == nil && len(registrations) > 0 {
		out.ProviderStatus = "ready"
	} else {
		out.ProviderCode = hostcap.CodeOf(catalogErr)
		switch out.ProviderCode {
		case hostcap.CodeAppAbsent:
			out.ProviderStatus = "absent"
		default:
			if catalogErr == nil {
				out.ProviderStatus = "no-enabled-binding"
			} else {
				out.ProviderStatus = "identity-error"
				out.Status = "error"
			}
		}
	}

	decisionStore, err := c.decisionStore()
	if err != nil {
		return ProjectionInspection{}, err
	}
	grants, err := decisionStore.Decisions(decision.ListFilter{Kind: decision.KindHostAppOpenResource, Profile: profileName, IncludeTerminal: true})
	if err != nil {
		return ProjectionInspection{}, err
	}
	for _, grant := range grants {
		if grant.State == decision.StateApproved {
			out.ApprovedGrantRecords++
		}
	}
	if out.ApprovedGrantRecords > 0 {
		// A profile-only diagnostic cannot prove the current session/workspace/
		// environment binding, so an approved record is not called live here.
		out.LiveGrantStatus = "approved-record-present; current run binding unobserved"
	} else if out.RequestedMode == ProjectionIdeModeTrusted {
		out.LiveGrantStatus = "approval-required"
	}

	if out.Status != "error" && out.ProfileStatus == "loaded" && out.ProviderStatus == "ready" && out.EnvironmentStatus == "present" {
		// Still warn until the guest-side order is observed by the real gate.
		out.Status = "warn"
	}
	return out, nil
}

type hostAppInspectionSource struct {
	manifest       hostapppack.Manifest
	revision       hostapppack.Revision
	enablement     hostapppack.Enablement
	hasEnablement  bool
	packState      string
	builtIn        bool
	candidate      *hostapppack.Manifest
	candidateRev   *hostapppack.Revision
	sourceLoadCode string
}

type hostAppInspectionOutcome struct {
	Outcome      string
	AuditRef     string
	RecoveryCode string
	At           time.Time
}

// HostAppInspection is the shared, host-path-free binding status model used by
// Manager, CLI projection diagnostics, and doctor adapters.
func (c Core) HostAppInspection(profileName, packFilter string) (hostapppack.Inspection, error) {
	inspection, _, err := c.hostAppInspectionWithRecovery(profileName, packFilter)
	return inspection, err
}

func (c Core) hostAppInspectionWithRecovery(profileName, packFilter string) (hostapppack.Inspection, []string, error) {
	profileName, err := normalizeProfileNameForProjection(defaultHostAppInspectionProfile(profileName))
	if err != nil {
		return hostapppack.Inspection{}, nil, err
	}
	sources, err := c.hostAppInspectionSources(profileName, strings.TrimSpace(packFilter))
	if err != nil {
		return hostapppack.Inspection{}, nil, err
	}
	owners := map[string]string{}
	if current, ownerErr := c.currentHostAppCommandOwners(profileName); ownerErr == nil {
		for _, owner := range current {
			owners[owner.Command] = owner.Owner
		}
	}
	grants := c.hostAppInspectionGrants(profileName)
	outcomes := c.hostAppInspectionOutcomes(profileName)
	inspection := hostapppack.Inspection{
		Schema: hostapppack.InspectionVersion, GeneratedAt: time.Now().UTC(), Entries: []hostapppack.InspectionEntry{},
	}
	var recoveryCodes []string
	for _, source := range sources {
		forbidden, forbiddenErr := c.hostAppForbiddenOverlapRoots(profileName, hostapppack.SourceSpec{
			Kind: source.revision.Source.Kind, Path: source.revision.Source.LocalPath,
			URL: source.revision.Source.URL, Commit: source.revision.Source.Commit,
		})
		apps := make(map[string]hostapppack.AppSpec, len(source.manifest.Apps))
		for _, app := range source.manifest.Apps {
			apps[app.ID] = app
		}
		selected := map[string]bool{}
		if source.hasEnablement {
			for _, id := range source.enablement.BindingIDs {
				selected[id] = true
			}
		}
		permissionStatus, permissionDiff := hostAppInspectionPermission(source)
		for _, binding := range source.manifest.Bindings {
			app := apps[binding.AppID]
			identityErr := forbiddenErr
			var identity hostcap.ObservedApplicationIdentity
			if identityErr == nil {
				identity, identityErr = c.resolveHostAppIdentity(applicationExpectation(source.manifest.ID, source.revision.RevisionID, app), forbidden)
			}
			identityView, safetyPosture, identityRecovery := publicHostAppIdentity(identity, identityErr)
			access := binding.RequestedAccess
			if source.hasEnablement && selected[binding.ID] {
				access = source.enablement.Access
			}
			if identityView.Verification == "unverified" {
				access = hostapppack.AccessAskEachRun
			}
			readiness, nextAction, lifecycleRecovery := hostAppInspectionReadiness(source, binding.ID, identityView.Verification)
			if source.sourceLoadCode != "" {
				readiness, lifecycleRecovery = "unavailable", source.sourceLoadCode
			}
			requestedProfile, compatibleProfile := app.RequestedSafetyProfile, ""
			if access == hostapppack.AccessSafe && identityErr == nil {
				if safety, safetyErr := hostcap.SelectCoreSafetyProfile(requestedProfile, identity.SafetyIdentity(c.hostAppPlatform())); safetyErr == nil {
					compatibleProfile = safety.ID
					safetyPosture = "safe"
				} else {
					safetyPosture = "unavailable"
					readiness = "unavailable"
					identityRecovery = recovery.CodeHostAppSafetyUnavailable
				}
			}
			for _, command := range binding.Commands {
				owner := hostAppBindingOwner(source.manifest.ID, source.revision.RevisionID, binding.ID)
				shadow := hostAppShadowStatus(command, owner, owners)
				if shadow == "conflict" || shadow == "reserved" {
					readiness = "review-required"
					lifecycleRecovery = recovery.CodeHostAppCommandConflict
				}
				grantState := hostAppGrantState(grants, source, binding, command, access)
				outcome := outcomes[hostAppInspectionOutcomeKey(source.manifest.ID, source.revision.RevisionID, binding.ID, command)]
				entry := hostapppack.InspectionEntry{
					Summary: hostapppack.InspectionSummary{
						Command: command, App: SanitizeHostAppDisplayText(app.ID, hostapppack.MaxSlugBytes),
						Profile: profileName, Access: access, Readiness: readiness, NextAction: nextAction,
					},
					Package: hostapppack.InspectionPackage{
						ID: source.manifest.ID, RevisionID: source.revision.RevisionID, SourceKind: source.revision.Source.Kind,
						SourceDigest: source.revision.SourceDigest, TestStatus: source.revision.TestStatus,
					},
					Permissions: hostapppack.InspectionPermissions{
						Fingerprint: hostAppInspectionFingerprint(source), Status: permissionStatus,
						Diff: append([]string{}, permissionDiff...),
					},
					AppIdentity: identityView,
					Binding: hostapppack.InspectionBinding{
						ID: binding.ID, Commands: append([]string(nil), binding.Commands...), ResourceKinds: append([]string(nil), binding.ResourceKinds...),
						CapabilityID: binding.CapabilityID, Grammar: binding.Grammar.Kind, ResultPolicy: binding.ResultPolicy, ShadowStatus: shadow,
					},
					Safety: hostapppack.InspectionSafety{
						RequestedProfile: requestedProfile, CompatibleProfile: compatibleProfile, Posture: safetyPosture,
					},
					Runtime: hostapppack.InspectionRuntime{
						ActiveInCurrentRun: false, GrantState: grantState,
						LastOutcome: outcome.Outcome, AuditRef: outcome.AuditRef,
					},
				}
				if source.manifest.InstallHint != nil {
					entry.Hint = &hostapppack.InspectionHint{
						Untrusted: true,
						Text:      SanitizeHostAppDisplayText(source.manifest.InstallHint.Text, hostapppack.MaxHintBytes),
						URL:       SanitizeHostAppDisplayText(source.manifest.InstallHint.URL, hostapppack.MaxURLBytes),
					}
				}
				for _, code := range []string{identityRecovery, lifecycleRecovery, outcome.RecoveryCode} {
					if code != "" {
						recoveryCodes = append(recoveryCodes, code)
					}
				}
				inspection.Entries = append(inspection.Entries, entry)
			}
		}
	}
	sort.Slice(inspection.Entries, func(i, j int) bool {
		a, b := inspection.Entries[i], inspection.Entries[j]
		if a.Package.ID != b.Package.ID {
			return a.Package.ID < b.Package.ID
		}
		return a.Summary.Command < b.Summary.Command
	})
	if len(inspection.Entries) > hostapppack.MaxCommandsPerProfile {
		inspection.Entries = inspection.Entries[:hostapppack.MaxCommandsPerProfile]
	}
	return inspection, sortedUnique(recoveryCodes), nil
}

func (c Core) hostAppInspectionSources(profileName, packFilter string) ([]hostAppInspectionSource, error) {
	var sources []hostAppInspectionSource
	manifest, err := builtinHostAppManifest()
	if err != nil {
		return nil, err
	}
	if packFilter == "" || packFilter == manifest.ID {
		revision := builtinHostAppRevision()
		access := hostapppack.AccessSafe
		if ReadProjectionIdeMode(c.Store.Root, profileName) == ProjectionIdeModeTrusted {
			access = hostapppack.AccessAskEachRun
		}
		enablement, err := builtinHostAppEnablement(profileName, manifest, revision, access)
		if err != nil {
			return nil, err
		}
		sources = append(sources, hostAppInspectionSource{
			manifest: manifest, revision: revision, packState: hostapppack.PackInstalled, builtIn: true, hasEnablement: true,
			enablement: enablement,
		})
	}
	registry, err := c.hostAppPackStore().LoadRegistry()
	if err != nil {
		return nil, err
	}
	for _, entry := range registry.Packs {
		if packFilter != "" && entry.ID != packFilter {
			continue
		}
		enablement, enablementErr := c.hostAppPackStore().LoadEnablement(profileName, entry.ID)
		hasEnablement := enablementErr == nil
		if enablementErr != nil && !errors.Is(enablementErr, os.ErrNotExist) {
			return nil, enablementErr
		}
		revisionID := entry.ActiveRevisionID
		if hasEnablement {
			revisionID = enablement.RevisionID
		}
		if revisionID == "" && len(entry.Revisions) > 0 {
			revisionID = entry.Revisions[len(entry.Revisions)-1].RevisionID
		}
		revision, ok := findHostAppRevision(entry, revisionID)
		if !ok || entry.State == hostapppack.PackRemoved {
			continue
		}
		manifestPath := c.hostAppPackStore().SourceDir(entry.ID, revision.RevisionID) + string(os.PathSeparator) + hostapppack.ManifestFileName
		manifest := hostapppack.Manifest{}
		sourceCode := ""
		if entry.State == hostapppack.PackInstalled && revision.State == hostapppack.RevisionInstalled {
			resolvedRevision, resolvedManifest, resolveErr := c.hostAppPackStore().ResolveRevisionManifest(entry.ID, revision.RevisionID)
			if resolveErr == nil {
				revision, manifest = resolvedRevision, resolvedManifest
			} else {
				sourceCode = recovery.CodeHostAppDigestMismatch
				manifest, _, resolveErr = hostapppack.LoadManifest(manifestPath)
				if resolveErr != nil {
					continue
				}
			}
		} else {
			var loadErr error
			manifest, _, loadErr = hostapppack.LoadManifest(manifestPath)
			if loadErr != nil {
				continue
			}
		}
		if testResult, testErr := c.hostAppPackStore().LoadTestResult(entry.ID, revision.RevisionID); testErr == nil {
			revision.TestStatus = testResult.Status
		}
		source := hostAppInspectionSource{
			manifest: manifest, revision: revision, enablement: enablement, hasEnablement: hasEnablement,
			packState: entry.State, sourceLoadCode: sourceCode,
		}
		if entry.ActiveRevisionID != "" && entry.ActiveRevisionID != revision.RevisionID {
			if candidateRevision, candidateManifest, candidateErr := c.hostAppPackStore().ResolveRevisionManifest(entry.ID, entry.ActiveRevisionID); candidateErr == nil {
				source.candidate, source.candidateRev = &candidateManifest, &candidateRevision
			}
		}
		sources = append(sources, source)
	}
	if packFilter != "" && len(sources) == 0 {
		return nil, fmt.Errorf("host-app pack %q is not installed", packFilter)
	}
	return sources, nil
}

func hostAppInspectionPermission(source hostAppInspectionSource) (string, []string) {
	if source.packState == hostapppack.PackRevoked || source.packState == hostapppack.PackRemoved ||
		(source.hasEnablement && source.enablement.State == hostapppack.EnablementRevoked) {
		return "revoked", []string{}
	}
	if !source.hasEnablement || source.enablement.State == hostapppack.EnablementSuspended {
		return "review-required", hostAppCandidatePermissionDiff(source)
	}
	return "accepted", hostAppCandidatePermissionDiff(source)
}

func hostAppCandidatePermissionDiff(source hostAppInspectionSource) []string {
	if source.candidate == nil || !source.hasEnablement {
		return []string{}
	}
	context, err := effectivePermissionContextForEnablement(source.manifest, source.enablement)
	if err != nil {
		return []string{"Core safety-profile acceptance changed"}
	}
	diff, err := hostapppack.DiffEffectivePermissions(source.manifest, context, *source.candidate, context)
	if err != nil {
		return []string{"candidate permissions require fresh review"}
	}
	return formatHostAppPermissionDiff(diff)
}

func formatHostAppPermissionDiff(diff hostapppack.PermissionDiff) []string {
	out := make([]string, 0, len(diff.Changed)+len(diff.Added)+len(diff.Removed)+1)
	for _, change := range diff.Changed {
		out = append(out, SanitizeHostAppDisplayText(change.Key+": "+change.Before+" -> "+change.After, hostapppack.MaxDescriptionBytes))
	}
	for _, item := range diff.Added {
		out = append(out, SanitizeHostAppDisplayText("+ "+item.Key+"="+item.Value, hostapppack.MaxDescriptionBytes))
	}
	for _, item := range diff.Removed {
		out = append(out, SanitizeHostAppDisplayText("- "+item.Key+"="+item.Value, hostapppack.MaxDescriptionBytes))
	}
	if diff.Truncated {
		out = append(out, fmt.Sprintf("permission diff truncated; total changes=%d", diff.TotalChanges))
	}
	return out
}

func hostAppInspectionFingerprint(source hostAppInspectionSource) string {
	if source.hasEnablement && source.enablement.PermissionFingerprint != "" {
		return source.enablement.PermissionFingerprint
	}
	return source.revision.BasePermissionFingerprint
}

func publicHostAppIdentity(identity hostcap.ObservedApplicationIdentity, err error) (hostapppack.InspectionAppIdentity, string, string) {
	view := hostapppack.InspectionAppIdentity{Verification: "unsupported", RootClass: "none", OwnerClass: "unknown"}
	if err != nil {
		switch hostcap.CodeOf(err) {
		case hostcap.CodeAppAbsent:
			view.Verification = "absent"
			return view, "unavailable", recovery.CodeHostAppAbsent
		case hostcap.CodeAppIdentityDrift:
			view.Verification = "drifted"
			return view, "unavailable", recovery.CodeHostAppIdentityDrift
		default:
			return view, "unavailable", recovery.CodeHostAppIdentityInvalid
		}
	}
	view.Verification = string(identity.Verification)
	view.RootClass = string(identity.RootClass)
	view.OwnerClass = string(identity.OwnerClass)
	view.BundleID = SanitizeHostAppDisplayText(identity.BundleID, hostapppack.MaxDescriptionBytes)
	view.TeamID = SanitizeHostAppDisplayText(identity.TeamID, hostapppack.MaxDescriptionBytes)
	view.CodeIdentity = SanitizeHostAppDisplayText(identity.CodeIdentity, hostapppack.MaxDescriptionBytes)
	view.ContentDigest = identity.ContentDigest
	if identity.Verification == hostcap.AppVerificationUnverified {
		return view, "unverified-app", ""
	}
	return view, "elevated", ""
}

func hostAppInspectionReadiness(source hostAppInspectionSource, bindingID, verification string) (string, string, string) {
	if source.packState == hostapppack.PackRevoked || source.packState == hostapppack.PackRemoved {
		return "disabled", "inspect the retained package tombstone", recovery.CodeHostAppBindingRevoked
	}
	if verification == "absent" || verification == "drifted" || verification == "unsupported" {
		return "unavailable", "inspect the Core-observed application identity", recovery.CodeHostAppIdentityInvalid
	}
	if !source.hasEnablement {
		return "review-required", "review and enable this exact revision", recovery.CodeHostAppPermissionReviewRequired
	}
	selected := false
	for _, selectedID := range source.enablement.BindingIDs {
		selected = selected || selectedID == bindingID
	}
	if !selected || source.enablement.State == hostapppack.EnablementDisabled {
		return "disabled", "review and enable this exact binding", recovery.CodeHostAppBindingDisabled
	}
	if source.enablement.State == hostapppack.EnablementRevoked {
		return "disabled", "inspect the retained revocation record", recovery.CodeHostAppBindingRevoked
	}
	if source.enablement.State == hostapppack.EnablementSuspended || (source.candidateRev != nil && source.candidateRev.RevisionID != source.revision.RevisionID) {
		return "review-required", "review the candidate permission difference", recovery.CodeHostAppPermissionReviewRequired
	}
	return "ready", "start a new run to materialize this exact binding", recovery.CodeHostAppNewRunRequired
}

func hostAppShadowStatus(command, owner string, owners map[string]string) string {
	if _, reserved := cmdproxy.LookupReservedHostAppCommand(command); reserved {
		return "reserved"
	}
	current, exists := owners[command]
	if !exists || current == owner {
		return "owned"
	}
	return "conflict"
}

func (c Core) hostAppInspectionGrants(profileName string) []decision.Decision {
	store, err := c.decisionStore()
	if err != nil {
		return nil
	}
	grants, err := store.Decisions(decision.ListFilter{Kind: decision.KindHostAppOpenResource, Profile: profileName, IncludeTerminal: true})
	if err != nil {
		return nil
	}
	return grants
}

func hostAppGrantState(grants []decision.Decision, source hostAppInspectionSource, binding hostapppack.BindingSpec, command, access string) string {
	if access == hostapppack.AccessSafe {
		return "not-required"
	}
	state := "pending"
	var latest time.Time
	for _, grant := range grants {
		facts, _ := grant.ProposedAction["binding"].(map[string]any)
		if fmt.Sprint(facts["packId"]) != source.manifest.ID || fmt.Sprint(facts["revisionId"]) != source.revision.RevisionID ||
			fmt.Sprint(facts["bindingId"]) != binding.ID || fmt.Sprint(facts["command"]) != command || grant.UpdatedAt.Before(latest) {
			continue
		}
		latest = grant.UpdatedAt
		switch grant.State {
		case decision.StateApproved:
			state = "approved"
		case decision.StateDenied, decision.StateDiscarded, decision.StateFailed:
			state = "denied"
		case decision.StateTimedOut:
			state = "expired"
		case decision.StateStale:
			state = "revoked"
		default:
			state = "pending"
		}
	}
	return state
}

func (c Core) hostAppInspectionOutcomes(profileName string) map[string]hostAppInspectionOutcome {
	out := map[string]hostAppInspectionOutcome{}
	events, err := c.AuditEvents(AuditEventFilter{Profile: profileName, Limit: 1000})
	if err != nil {
		return out
	}
	for _, event := range events {
		if event.Action != "host.app.open-resource" && event.Action != "host.app.launch" && event.Action != "host.app.refuse" {
			continue
		}
		packID := stringDetail(event.Details, "packId")
		revisionID := stringDetail(event.Details, "revisionId")
		bindingID := stringDetail(event.Details, "bindingId")
		command := stringDetail(event.Details, "command")
		if packID == "" || revisionID == "" || bindingID == "" || command == "" {
			continue
		}
		key := hostAppInspectionOutcomeKey(packID, revisionID, bindingID, command)
		if prior, exists := out[key]; exists && !event.Time.After(prior.At) {
			continue
		}
		outcome := SanitizeHostAppDisplayText(stringDetail(event.Details, "outcome"), hostapppack.MaxDescriptionBytes)
		if outcome == "" {
			outcome = event.Decision
		}
		auditRef := ""
		if event.Session != "" {
			auditRef = SanitizeHostAppDisplayText(event.Session+"/"+event.Action, hostapppack.MaxStorageIDBytes)
		}
		recoveryCode := SanitizeHostAppDisplayText(stringDetail(event.Details, "code"), hostapppack.MaxStorageIDBytes)
		if recoveryCode == "" {
			recoveryCode = SanitizeHostAppDisplayText(stringDetail(event.Details, "recoveryCode"), hostapppack.MaxStorageIDBytes)
		}
		out[key] = hostAppInspectionOutcome{
			Outcome: outcome, AuditRef: auditRef,
			RecoveryCode: recoveryCode,
			At:           event.Time,
		}
	}
	return out
}

func hostAppInspectionOutcomeKey(packID, revisionID, bindingID, command string) string {
	return strings.Join([]string{packID, revisionID, bindingID, command}, "\x00")
}

func hostAppInspectionRecovery(inspection hostapppack.Inspection) []string {
	var codes []string
	for _, entry := range inspection.Entries {
		switch entry.Summary.Readiness {
		case "ready":
			codes = append(codes, recovery.CodeHostAppNewRunRequired)
		case "review-required":
			if entry.Binding.ShadowStatus == "conflict" || entry.Binding.ShadowStatus == "reserved" {
				codes = append(codes, recovery.CodeHostAppCommandConflict)
			} else {
				codes = append(codes, recovery.CodeHostAppPermissionReviewRequired)
			}
		case "disabled":
			if entry.Permissions.Status == "revoked" {
				codes = append(codes, recovery.CodeHostAppBindingRevoked)
			} else {
				codes = append(codes, recovery.CodeHostAppBindingDisabled)
			}
		case "unavailable":
			switch entry.AppIdentity.Verification {
			case "absent":
				codes = append(codes, recovery.CodeHostAppAbsent)
			case "drifted":
				codes = append(codes, recovery.CodeHostAppIdentityDrift)
			default:
				codes = append(codes, recovery.CodeHostAppIdentityInvalid)
			}
		}
	}
	return sortedUnique(codes)
}

func (p ProjectionInspection) ObservedFacts() []string {
	facts := []string{
		"profile=" + p.ProfileStatus,
		fmt.Sprintf("environments=%s count=%d", p.EnvironmentStatus, p.EnvironmentCount),
		"requestedMode=" + p.RequestedMode,
		fmt.Sprintf("grant=%s approvedRecords=%d", p.LiveGrantStatus, p.ApprovedGrantRecords),
		"provider=" + p.ProviderStatus,
		"pathShadowPolicy=" + p.PathShadowPolicy,
		"pathShadowObserved=" + p.PathShadowObservation,
	}
	if p.ProviderCode != "" {
		facts = append(facts, "providerCode="+p.ProviderCode)
	}
	for _, capability := range p.ProjectedCapabilities {
		facts = append(facts, fmt.Sprintf("capability=%s status=%s resultPolicy=%s decisionPolicy=%s", capability.ID, capability.Status, capability.ResultPolicy, capability.DecisionPolicy))
	}
	for _, binding := range p.Bindings {
		facts = append(facts, fmt.Sprintf("binding=%s -> %s (grammar=%s; %s)", binding.Command, binding.Action, binding.Grammar, binding.Resolution))
	}
	for _, entry := range p.HostApps.Entries {
		facts = append(facts, fmt.Sprintf("hostApp=%s pack=%s revision=%s appIdentity=%s access=%s readiness=%s permissions=%s grant=%s outcome=%s",
			entry.Summary.Command, entry.Package.ID, entry.Package.RevisionID, entry.AppIdentity.Verification,
			entry.Summary.Access, entry.Summary.Readiness, entry.Permissions.Status, entry.Runtime.GrantState, entry.Runtime.LastOutcome))
	}
	return facts
}

func (p ProjectionInspection) CandidateCauses() []string {
	var causes []string
	if p.ProfileStatus != "loaded" {
		causes = append(causes, "the selected profile is not initialized")
	}
	if p.EnvironmentStatus != "present" {
		causes = append(causes, "no persisted environment currently provides a guest runtime to inspect")
	}
	if p.ProviderStatus != "ready" {
		causes = append(causes, "no enabled host-app binding has a verified provider identity")
	}
	if p.RequestedMode == ProjectionIdeModeTrusted && p.ApprovedGrantRecords == 0 {
		causes = append(causes, "trusted-host-ide is requested but no run-bound decision is approved")
	}
	if strings.TrimSpace(p.PathShadowObservation) == "not-run" {
		causes = append(causes, "host-side doctor has not observed the real guest PATH")
	}
	for _, entry := range p.HostApps.Entries {
		if entry.Summary.Readiness != "ready" && entry.Summary.NextAction != "" {
			causes = append(causes, entry.Summary.Command+": "+entry.Summary.NextAction)
		}
	}
	return causes
}
