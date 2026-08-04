package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
)

const migrationAuthoritySummaryLimit = 1024

type migrationNetworkAuthorityValue struct {
	Mode             string `json:"mode"`
	MediatedResolver string `json:"mediatedResolver,omitempty"`
	ProxySecretRef   string `json:"proxySecretRef,omitempty"`
}

type migrationHostAppAuthorityValue struct {
	Open profile.OpenCapability `json:"open"`
}

type migrationEndpointAuthorityValue struct {
	Candidate  *profile.EndpointCandidate `json:"candidate,omitempty"`
	PortBridge bool                       `json:"portBridge,omitempty"`
	Expose     bool                       `json:"expose,omitempty"`
}

type migrationAuthorityUnavailableSummary struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Risk string `json:"risk"`
}

type migrationEnvironmentAuthoritySummary struct {
	PublicNames []string `json:"publicNames,omitempty"`
	Inherit     []string `json:"inherit,omitempty"`
}

// migrationAuthorityProposalsForProfile extracts source authority separately
// from the inert portable profile. The values are authenticated source facts,
// never destination grants: every proposal is emitted disabled and is bound to
// exactly one environment by the manifest's proposal refs.
func migrationAuthorityProposalsForProfile(
	operationID, environmentID string,
	source profile.Profile,
) ([]migration.AuthorityProposal, []migration.OpaqueID, error) {
	if strings.TrimSpace(operationID) == "" || strings.TrimSpace(environmentID) == "" ||
		source.Validate() != nil {
		return nil, nil, ErrMigrationPlanInvalid
	}
	proposals := make([]migration.AuthorityProposal, 0)
	appendProposal := func(class, discriminator, summary string) error {
		if strings.TrimSpace(summary) == "" || len(summary) > migrationAuthoritySummaryLimit {
			return ErrMigrationPlanInvalid
		}
		proposals = append(proposals, migration.AuthorityProposal{
			ProposalID: migrationExportDerivedID(
				"authority", operationID, environmentID, class, discriminator,
			),
			Class: class, SourceSummary: summary, State: "disabled",
		})
		return nil
	}

	networkClass := "network"
	if source.Network.Mode == profile.NetworkModeTun2Socks {
		networkClass = "proxy"
	}
	networkSummary, err := migrationAuthorityCanonicalJSON(migrationNetworkAuthorityValue{
		Mode: source.Network.Mode, MediatedResolver: source.Network.MediatedResolver,
	})
	if err != nil || appendProposal(networkClass, "network", networkSummary) != nil {
		return nil, nil, ErrMigrationPlanInvalid
	}

	if slices.Contains(source.Policy.MaxCapabilities, "host.open") ||
		source.HostCapabilities.Open.AllowURLs ||
		source.HostCapabilities.Open.AllowLocalURLs ||
		source.HostCapabilities.Open.AllowPrivateNetworkURLs ||
		source.HostCapabilities.Open.AllowWorkspaceFiles {
		summary, encodeErr := migrationAuthorityCanonicalJSON(
			migrationHostAppAuthorityValue{
				Open: source.HostCapabilities.Open,
			},
		)
		if encodeErr != nil || appendProposal("host-app", "open", summary) != nil {
			return nil, nil, ErrMigrationPlanInvalid
		}
	}
	defaultCommands, _ := json.Marshal(profile.Default("source").CommandProxy)
	sourceCommands, _ := json.Marshal(source.CommandProxy)
	if !bytes.Equal(defaultCommands, sourceCommands) {
		summary, encodeErr := migrationAuthorityUnavailableJSON(
			"command-proxy", fmt.Sprintf("%d-routes", len(source.CommandProxy.Commands)),
			"destination-command-review-required",
		)
		if encodeErr != nil || appendProposal("command-adapter", "command-proxy", summary) != nil {
			return nil, nil, ErrMigrationPlanInvalid
		}
	}

	portBridge := slices.Contains(source.Policy.MaxCapabilities, "portbridge.host-to-guest")
	expose := slices.Contains(source.Policy.MaxCapabilities, "endpoint.expose.host-to-guest")
	if portBridge || expose {
		summary, encodeErr := migrationAuthorityCanonicalJSON(
			migrationEndpointAuthorityValue{PortBridge: portBridge, Expose: expose},
		)
		if encodeErr != nil || appendProposal("endpoint", "capabilities", summary) != nil {
			return nil, nil, ErrMigrationPlanInvalid
		}
	}
	for index := range source.EndpointExposure.HostToGuest {
		candidate := source.EndpointExposure.HostToGuest[index]
		summary, encodeErr := migrationAuthorityCanonicalJSON(
			migrationEndpointAuthorityValue{Candidate: &candidate},
		)
		if encodeErr != nil || appendProposal(
			"endpoint", fmt.Sprintf("candidate-%04d", index), summary,
		) != nil {
			return nil, nil, ErrMigrationPlanInvalid
		}
	}

	for index, rule := range append(
		append([]hostfs.Rule(nil), source.HostFS.Grants...), source.HostFS.Deny...,
	) {
		summary, encodeErr := migrationAuthorityUnavailableJSON(
			"hostfs-rule", rule.ID, "destination-path-review-required",
		)
		if encodeErr != nil || appendProposal(
			"hostfs", fmt.Sprintf("rule-%04d", index), summary,
		) != nil {
			return nil, nil, ErrMigrationPlanInvalid
		}
	}

	adapterIDs := make([]string, 0, len(source.CommandAdapters.Adapters))
	for id := range source.CommandAdapters.Adapters {
		adapterIDs = append(adapterIDs, id)
	}
	sort.Strings(adapterIDs)
	for _, id := range adapterIDs {
		adapter := source.CommandAdapters.Adapters[id]
		class := "command-adapter"
		if adapter.PackID != "" {
			class = "pack"
		}
		summary, encodeErr := migrationAuthorityUnavailableJSON(
			class, id, "destination-artifact-reobservation-required",
		)
		if encodeErr != nil || appendProposal(class, "adapter-"+id, summary) != nil {
			return nil, nil, ErrMigrationPlanInvalid
		}
	}
	for index, ref := range source.Policy.ScriptRefs {
		summary, encodeErr := migrationAuthorityUnavailableJSON(
			"script", ref.ID, "destination-script-review-required",
		)
		if encodeErr != nil || appendProposal(
			"script", fmt.Sprintf("script-%04d-%s", index, ref.ID), summary,
		) != nil {
			return nil, nil, ErrMigrationPlanInvalid
		}
	}
	if len(source.Env.Public) != 0 || len(source.Env.Inherit) != 0 {
		publicNames := make([]string, 0, len(source.Env.Public))
		for name := range source.Env.Public {
			publicNames = append(publicNames, name)
		}
		sort.Strings(publicNames)
		inherit := append([]string(nil), source.Env.Inherit...)
		sort.Strings(inherit)
		summary, encodeErr := migrationAuthorityCanonicalJSON(
			migrationEnvironmentAuthoritySummary{
				PublicNames: publicNames, Inherit: inherit,
			},
		)
		if encodeErr != nil {
			summary, encodeErr = migrationAuthorityUnavailableJSON(
				"environment", fmt.Sprintf("%d-fields", len(publicNames)+len(inherit)),
				"destination-environment-review-required",
			)
		}
		if encodeErr != nil || appendProposal("environment", "environment", summary) != nil {
			return nil, nil, ErrMigrationPlanInvalid
		}
	}
	if !source.Audit.Enabled {
		summary, encodeErr := migrationAuthorityUnavailableJSON(
			"audit", "disabled", "destination-observation-policy-review-required",
		)
		if encodeErr != nil || appendProposal("profile", "audit", summary) != nil {
			return nil, nil, ErrMigrationPlanInvalid
		}
	}

	sort.Slice(proposals, func(left, right int) bool {
		return proposals[left].ProposalID < proposals[right].ProposalID
	})
	refs := make([]migration.OpaqueID, len(proposals))
	for index := range proposals {
		refs[index] = proposals[index].ProposalID
	}
	return proposals, refs, nil
}

func migrationAuthorityCanonicalJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > migrationAuthoritySummaryLimit {
		return "", ErrMigrationPlanInvalid
	}
	return string(encoded), nil
}

func migrationAuthorityUnavailableJSON(kind, id, risk string) (string, error) {
	return migrationAuthorityCanonicalJSON(migrationAuthorityUnavailableSummary{
		Kind: kind, ID: id, Risk: risk,
	})
}

func planMigrationAuthorityActions(
	draft migration.ImportDraft,
	manifest migration.Manifest,
	secretActions []migration.SecretAction,
) ([]migration.AuthorityAction, []migration.OpaqueID, []migration.PlanNotice, error) {
	selected := make(map[migration.OpaqueID]struct{}, len(draft.SelectedEnvironmentRefs))
	for _, ref := range draft.SelectedEnvironmentRefs {
		selected[ref] = struct{}{}
	}
	proposals := make(map[migration.OpaqueID]migration.AuthorityProposal, len(manifest.AuthorityProposals))
	for _, proposal := range manifest.AuthorityProposals {
		proposals[proposal.ProposalID] = proposal
	}
	proposalEnvironment := make(map[migration.OpaqueID]migration.OpaqueID)
	for _, environmentSnapshot := range manifest.Environments {
		if _, exists := selected[environmentSnapshot.SourceEnvironmentRef]; !exists {
			continue
		}
		for _, proposalID := range environmentSnapshot.AuthorityProposalRefs {
			if _, duplicate := proposalEnvironment[proposalID]; duplicate {
				return nil, nil, nil, ErrMigrationPlanInvalid
			}
			if _, exists := proposals[proposalID]; !exists {
				return nil, nil, nil, ErrMigrationPlanInvalid
			}
			proposalEnvironment[proposalID] = environmentSnapshot.SourceEnvironmentRef
		}
	}
	decisions := make(map[migration.OpaqueID]migration.AuthorityDecision, len(draft.AuthorityDecisions))
	for _, decision := range draft.AuthorityDecisions {
		if _, exists := proposalEnvironment[decision.ProposalID]; !exists {
			return nil, nil, nil, ErrMigrationPlanInvalid
		}
		decisions[decision.ProposalID] = decision
	}
	proposalIDs := make([]migration.OpaqueID, 0, len(proposalEnvironment))
	for proposalID := range proposalEnvironment {
		proposalIDs = append(proposalIDs, proposalID)
	}
	slices.Sort(proposalIDs)
	actions := make([]migration.AuthorityAction, 0, len(proposalIDs))
	disabled := make([]migration.OpaqueID, 0, len(proposalIDs))
	blockers := make([]migration.PlanNotice, 0)
	for _, proposalID := range proposalIDs {
		proposal := proposals[proposalID]
		decision, decided := decisions[proposalID]
		if !decided || decision.Decision == migrationAuthorityDecisionDisabled ||
			decision.Decision == migrationAuthorityDecisionRejected {
			disabled = append(disabled, proposalID)
			continue
		}
		if decision.Decision != migrationAuthorityDecisionApproved {
			return nil, nil, nil, ErrMigrationRequestInvalid
		}
		canonical, supported, noticeCode, err := canonicalMigrationAuthorityDestination(
			proposal.Class, decision.DestinationValue,
			proposalEnvironment[proposalID], secretActions,
		)
		if err != nil {
			return nil, nil, nil, err
		}
		if !supported {
			disabled = append(disabled, proposalID)
			blockers = append(blockers, migration.PlanNotice{
				Code:        noticeCode,
				Summary:     "This imported authority cannot be enabled until its destination prerequisite is independently verified.",
				Remediation: "Keep it disabled and configure the destination capability after import, or provide a supported reviewed destination value.",
				SourceRef:   proposalID,
			})
			continue
		}
		actions = append(actions, migration.AuthorityAction{
			ProposalID: proposalID, EnvironmentRef: proposalEnvironment[proposalID],
			Class: proposal.Class, DestinationValue: canonical, Approved: true,
		})
	}
	sortMigrationPlanNotices(blockers)
	return actions, disabled, blockers, nil
}

func canonicalMigrationAuthorityDestination(
	class, raw string,
	environmentRef migration.OpaqueID,
	secretActions []migration.SecretAction,
) (string, bool, string, error) {
	switch class {
	case "network", "proxy":
		var value migrationNetworkAuthorityValue
		if err := decodeCanonicalMigrationAuthority(raw, &value); err != nil {
			return "", false, "", err
		}
		if class == "network" {
			if value.Mode != profile.NetworkModeDirect || value.ProxySecretRef != "" ||
				value.MediatedResolver != "" {
				return "", false, "", ErrMigrationRequestInvalid
			}
		} else {
			if value.Mode != profile.NetworkModeTun2Socks || value.ProxySecretRef != "" {
				return "", false, "", ErrMigrationRequestInvalid
			}
			secretRef := ""
			for _, action := range secretActions {
				if !slices.Contains(action.EnvironmentRefs, environmentRef) {
					continue
				}
				if secretRef != "" || action.DestinationRef == "" ||
					action.Decision == migrationSecretDecisionUnresolved {
					return "", false, "migration.authority.secret_mapping_required", nil
				}
				secretRef = action.DestinationRef
			}
			if secretRef == "" {
				return "", false, "migration.authority.secret_mapping_required", nil
			}
			value.ProxySecretRef = secretRef
		}
		candidate := profile.Default("authority-validation")
		candidate.Network = profile.Network{
			Mode: value.Mode, ProxySecretRef: value.ProxySecretRef,
			MediatedResolver: value.MediatedResolver,
		}
		if err := candidate.Validate(); err != nil {
			return "", false, "", ErrMigrationRequestInvalid
		}
		canonical, err := migrationAuthorityCanonicalJSON(value)
		return canonical, true, "", err
	case "host-app":
		var value migrationHostAppAuthorityValue
		if err := decodeCanonicalMigrationAuthority(raw, &value); err != nil {
			return "", false, "", err
		}
		candidate := profile.Default("authority-validation")
		candidate.HostCapabilities.Open = value.Open
		if err := candidate.Validate(); err != nil {
			return "", false, "", ErrMigrationRequestInvalid
		}
		canonical, err := migrationAuthorityCanonicalJSON(value)
		return canonical, true, "", err
	case "endpoint":
		var value migrationEndpointAuthorityValue
		if err := decodeCanonicalMigrationAuthority(raw, &value); err != nil {
			return "", false, "", err
		}
		if value.Candidate == nil && !value.PortBridge && !value.Expose {
			return "", false, "", ErrMigrationRequestInvalid
		}
		candidate := profile.Default("authority-validation")
		candidate.EndpointExposure.HostToGuest = nil
		candidate.Policy.MaxCapabilities = []string{"guest.exec"}
		if value.Candidate != nil {
			candidate.EndpointExposure.HostToGuest = []profile.EndpointCandidate{*value.Candidate}
		}
		if value.PortBridge {
			candidate.Policy.MaxCapabilities = append(candidate.Policy.MaxCapabilities, "portbridge.host-to-guest")
		}
		if value.Expose {
			candidate.Policy.MaxCapabilities = append(candidate.Policy.MaxCapabilities, "endpoint.expose.host-to-guest")
		}
		if err := candidate.Validate(); err != nil {
			return "", false, "", ErrMigrationRequestInvalid
		}
		canonical, err := migrationAuthorityCanonicalJSON(value)
		return canonical, true, "", err
	case "workspace", "hostfs", "mount", "command-adapter", "script", "pack", "environment", "profile":
		if !boundedMigrationString(raw, migrationAuthoritySummaryLimit) ||
			raw != redactMigrationInspectionText(raw) {
			return "", false, "", ErrMigrationRequestInvalid
		}
		return "", false, "migration.authority.approval_unavailable", nil
	default:
		return "", false, "", ErrMigrationPlanInvalid
	}
}

func decodeCanonicalMigrationAuthority(raw string, destination any) error {
	if !boundedMigrationString(raw, migrationAuthoritySummaryLimit) ||
		raw != redactMigrationInspectionText(raw) {
		return ErrMigrationRequestInvalid
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrMigrationRequestInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrMigrationRequestInvalid
	}
	canonical, err := json.Marshal(destination)
	if err != nil || string(canonical) != raw {
		return ErrMigrationRequestInvalid
	}
	return nil
}

func applyMigrationAuthorityActions(
	destination *profile.Profile,
	environmentRef migration.OpaqueID,
	actions []migration.AuthorityAction,
	secretActions []migration.SecretAction,
) error {
	if destination == nil {
		return ErrMigrationOperationInvalid
	}
	for _, action := range actions {
		if action.EnvironmentRef != environmentRef {
			continue
		}
		canonical, supported, _, err := canonicalMigrationAuthorityDestination(
			action.Class, action.DestinationValue, environmentRef, secretActions,
		)
		if err != nil || !supported || canonical != action.DestinationValue || !action.Approved {
			return ErrMigrationOperationInvalid
		}
		switch action.Class {
		case "network", "proxy":
			var value migrationNetworkAuthorityValue
			if decodeCanonicalMigrationAuthority(action.DestinationValue, &value) != nil {
				return ErrMigrationOperationInvalid
			}
			destination.Network = profile.Network{
				Mode: value.Mode, ProxySecretRef: value.ProxySecretRef,
				MediatedResolver: value.MediatedResolver,
			}
			addMigrationProfileCapability(destination, "network.connect")
		case "host-app":
			var value migrationHostAppAuthorityValue
			if decodeCanonicalMigrationAuthority(action.DestinationValue, &value) != nil {
				return ErrMigrationOperationInvalid
			}
			destination.HostCapabilities.Open = value.Open
			addMigrationProfileCapability(destination, "host.open")
		case "endpoint":
			var value migrationEndpointAuthorityValue
			if decodeCanonicalMigrationAuthority(action.DestinationValue, &value) != nil {
				return ErrMigrationOperationInvalid
			}
			if value.Candidate != nil {
				for _, existing := range destination.EndpointExposure.HostToGuest {
					if existing.ID == value.Candidate.ID {
						return ErrMigrationOperationInvalid
					}
				}
				destination.EndpointExposure.HostToGuest = append(
					destination.EndpointExposure.HostToGuest, *value.Candidate,
				)
			}
			if value.PortBridge {
				addMigrationProfileCapability(destination, "portbridge.host-to-guest")
			}
			if value.Expose {
				addMigrationProfileCapability(destination, "endpoint.expose.host-to-guest")
			}
		default:
			return ErrMigrationOperationInvalid
		}
	}
	if err := destination.Validate(); err != nil {
		return ErrMigrationOperationInvalid
	}
	return nil
}

func addMigrationProfileCapability(destination *profile.Profile, capability string) {
	if !slices.Contains(destination.Policy.MaxCapabilities, capability) {
		destination.Policy.MaxCapabilities = append(destination.Policy.MaxCapabilities, capability)
		sort.Strings(destination.Policy.MaxCapabilities)
	}
}

func validMigrationAuthorityClass(class string) bool {
	switch class {
	case "workspace", "hostfs", "mount", "network", "proxy", "endpoint",
		"host-app", "command-adapter", "script", "pack", "environment", "profile":
		return true
	default:
		return false
	}
}

func migrationApprovedAuthorityProposalIDs(
	actions []migration.AuthorityAction,
) []migration.OpaqueID {
	ids := make([]migration.OpaqueID, len(actions))
	for index, action := range actions {
		ids[index] = action.ProposalID
	}
	return ids
}

func revalidateMigrationAuthorityActions(
	plan migration.ImportPlan,
	manifest migration.Manifest,
) error {
	selected := make(map[migration.OpaqueID]struct{}, len(plan.Objects))
	for _, object := range plan.Objects {
		selected[object.SourceRef] = struct{}{}
	}
	proposals := make(map[migration.OpaqueID]migration.AuthorityProposal, len(manifest.AuthorityProposals))
	for _, proposal := range manifest.AuthorityProposals {
		proposals[proposal.ProposalID] = proposal
	}
	expected := make(map[migration.OpaqueID]migration.OpaqueID)
	for _, environmentSnapshot := range manifest.Environments {
		if _, exists := selected[environmentSnapshot.SourceEnvironmentRef]; !exists {
			continue
		}
		for _, proposalID := range environmentSnapshot.AuthorityProposalRefs {
			if _, duplicate := expected[proposalID]; duplicate {
				return ErrMigrationPlanStale
			}
			expected[proposalID] = environmentSnapshot.SourceEnvironmentRef
		}
	}
	seen := make(map[migration.OpaqueID]struct{}, len(plan.AuthorityActions)+len(plan.DisabledProposals))
	for _, action := range plan.AuthorityActions {
		proposal, exists := proposals[action.ProposalID]
		if !exists || expected[action.ProposalID] != action.EnvironmentRef ||
			proposal.Class != action.Class {
			return ErrMigrationPlanStale
		}
		canonical, supported, _, err := canonicalMigrationAuthorityDestination(
			action.Class, action.DestinationValue, action.EnvironmentRef, plan.SecretActions,
		)
		if err != nil || !supported || canonical != action.DestinationValue {
			return ErrMigrationPlanStale
		}
		seen[action.ProposalID] = struct{}{}
	}
	for _, proposalID := range plan.DisabledProposals {
		if _, duplicate := seen[proposalID]; duplicate {
			return ErrMigrationPlanStale
		}
		if _, exists := expected[proposalID]; !exists {
			return ErrMigrationPlanStale
		}
		seen[proposalID] = struct{}{}
	}
	if len(seen) != len(expected) {
		return ErrMigrationPlanStale
	}
	return nil
}

func validateMigrationPlanAuthorityClosure(plan migration.ImportPlan) error {
	selected := make(map[migration.OpaqueID]struct{}, len(plan.Objects))
	for _, object := range plan.Objects {
		selected[object.SourceRef] = struct{}{}
	}
	seen := make(map[migration.OpaqueID]struct{}, len(plan.AuthorityActions)+len(plan.DisabledProposals))
	for _, action := range plan.AuthorityActions {
		if _, exists := selected[action.EnvironmentRef]; !exists {
			return ErrMigrationPlanInvalid
		}
		if _, duplicate := seen[action.ProposalID]; duplicate {
			return ErrMigrationPlanInvalid
		}
		seen[action.ProposalID] = struct{}{}
	}
	for _, proposalID := range plan.DisabledProposals {
		if _, duplicate := seen[proposalID]; duplicate {
			return ErrMigrationPlanInvalid
		}
		seen[proposalID] = struct{}{}
	}
	return nil
}
