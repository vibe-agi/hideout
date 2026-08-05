package manager

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/migration"
)

const MigrationBundleInspectionSchema = "hideout.migration-bundle-inspection/v1"

type MigrationBundleSourceProjection struct {
	ProductVersion string `json:"productVersion"`
	HostOS         string `json:"hostOS"`
	HostArch       string `json:"hostArch"`
	Backend        string `json:"backend"`
	BackendVersion string `json:"backendVersion"`
	GuestArch      string `json:"guestArch"`
}

type MigrationBundleWorkspaceProjection struct {
	ProposalID   migration.OpaqueID `json:"proposalId"`
	GuestPath    string             `json:"guestPath"`
	HostPathHint string             `json:"hostPathHint"`
	State        string             `json:"state"`
}

type MigrationBundleEnvironmentProjection struct {
	SourceRef            migration.OpaqueID                   `json:"sourceRef"`
	DisplayNameHint      string                               `json:"displayNameHint"`
	Runtime              string                               `json:"runtime"`
	Backend              string                               `json:"backend"`
	Mode                 migration.ExportMode                 `json:"mode"`
	ImageReference       string                               `json:"imageReference,omitempty"`
	WorkspaceProposals   []MigrationBundleWorkspaceProjection `json:"workspaceProposals"`
	AuthorityProposalIDs []migration.OpaqueID                 `json:"authorityProposalIds"`
	DiskIDs              []migration.OpaqueID                 `json:"diskIds"`
	GuestIdentityPresent bool                                 `json:"guestIdentityPresent"`
	SSHHostKeyCount      uint32                               `json:"sshHostKeyCount"`
}

type MigrationBundleDiskProjection struct {
	DiskID             migration.OpaqueID `json:"diskId"`
	Role               migration.DiskRole `json:"role"`
	Format             string             `json:"format"`
	LogicalBytes       uint64             `json:"logicalBytes"`
	AllocatedBytesHint uint64             `json:"allocatedBytesHint"`
	Provider           string             `json:"provider"`
	ProviderKind       string             `json:"providerKind"`
	Features           []string           `json:"features"`
}

type MigrationBundleSecretProjection struct {
	SecretRef     migration.OpaqueID       `json:"secretRef"`
	DisplayName   string                   `json:"displayName"`
	Provider      string                   `json:"provider"`
	Transfer      migration.SecretTransfer `json:"transfer"`
	ValueIncluded bool                     `json:"valueIncluded"`
}

type MigrationBundleAuthorityProjection struct {
	ProposalID    migration.OpaqueID `json:"proposalId"`
	Class         string             `json:"class"`
	SourceSummary string             `json:"sourceSummary"`
	State         string             `json:"state"`
}

type MigrationBundleComponentCounts struct {
	Profiles         uint32 `json:"profiles"`
	ProfileStates    uint32 `json:"profileStates"`
	Environments     uint32 `json:"environments"`
	Disks            uint32 `json:"disks"`
	SecretValues     uint32 `json:"secretValues"`
	ProviderMetadata uint32 `json:"providerMetadata"`
	Total            uint32 `json:"total"`
}

type MigrationBundleInspectionProjection struct {
	Schema               string                                 `json:"schema"`
	BundleID             migration.BundleID                     `json:"bundleId"`
	FormatVersion        uint16                                 `json:"formatVersion"`
	CreatedAt            string                                 `json:"createdAt"`
	Sealed               bool                                   `json:"sealed"`
	EncodedBytes         uint64                                 `json:"encodedBytes"`
	LogicalBytes         uint64                                 `json:"logicalBytes"`
	RecordCount          uint64                                 `json:"recordCount"`
	Source               MigrationBundleSourceProjection        `json:"source"`
	Environments         []MigrationBundleEnvironmentProjection `json:"environments"`
	Disks                []MigrationBundleDiskProjection        `json:"disks"`
	Secrets              []MigrationBundleSecretProjection      `json:"secrets"`
	AuthorityProposals   []MigrationBundleAuthorityProjection   `json:"authorityProposals"`
	ExcludedClasses      []string                               `json:"excludedClasses"`
	RequiredCapabilities []migration.RequiredCapability         `json:"requiredCapabilities"`
	Components           MigrationBundleComponentCounts         `json:"components"`
	Warnings             []migration.PlanNotice                 `json:"warnings"`
}

// ProjectMigrationBundleInspection is the shared read-only inventory for CLI,
// TUI, WebUI, and API surfaces. Guest identity digests, content digests, record
// plaintext, passphrases, and secret values are deliberately absent. Paths stay
// visible for mapping; credential-bearing URL userinfo and known secret
// assignments are redacted.
func ProjectMigrationBundleInspection(
	inspection migration.SealedBundleInspection,
) (MigrationBundleInspectionProjection, error) {
	if err := validateSealedBundleInspection(inspection); err != nil {
		return MigrationBundleInspectionProjection{}, err
	}
	manifest := inspection.Manifest
	projection := MigrationBundleInspectionProjection{
		Schema:   MigrationBundleInspectionSchema,
		BundleID: inspection.Binding.BundleID, FormatVersion: inspection.Binding.FormatVersion,
		CreatedAt: inspection.CreatedAt, Sealed: inspection.Summary.Sealed,
		EncodedBytes: inspection.Summary.EncodedBytes,
		LogicalBytes: inspection.Summary.LogicalBytes,
		RecordCount:  inspection.Summary.RecordCount,
		Source: MigrationBundleSourceProjection{
			ProductVersion: manifest.SourceProduct.Version,
			HostOS:         manifest.SourceProduct.HostOS, HostArch: manifest.SourceProduct.HostArch,
			Backend:        manifest.SourceProduct.Backend,
			BackendVersion: manifest.SourceProduct.BackendVersion,
			GuestArch:      manifest.SourceProduct.GuestArch,
		},
		Environments:         make([]MigrationBundleEnvironmentProjection, len(manifest.Environments)),
		Disks:                make([]MigrationBundleDiskProjection, len(manifest.DiskObjects)),
		Secrets:              make([]MigrationBundleSecretProjection, len(manifest.SecretEntries)),
		AuthorityProposals:   make([]MigrationBundleAuthorityProjection, len(manifest.AuthorityProposals)),
		ExcludedClasses:      append([]string(nil), manifest.ExcludedClasses...),
		RequiredCapabilities: append([]migration.RequiredCapability(nil), manifest.RequiredCapabilities...),
		Warnings:             []migration.PlanNotice{},
	}
	for index, environment := range manifest.Environments {
		projected := MigrationBundleEnvironmentProjection{
			SourceRef:       environment.SourceEnvironmentRef,
			DisplayNameHint: redactMigrationInspectionText(environment.DisplayNameHint),
			Runtime:         environment.Runtime, Backend: environment.Backend, Mode: environment.Mode,
			WorkspaceProposals: make(
				[]MigrationBundleWorkspaceProjection, len(environment.WorkspaceProposals),
			),
			AuthorityProposalIDs: append([]migration.OpaqueID(nil), environment.AuthorityProposalRefs...),
			DiskIDs:              append([]migration.OpaqueID(nil), environment.DiskRefs...),
			GuestIdentityPresent: true,
			SSHHostKeyCount:      uint32(len(environment.GuestIdentityEvidence.SSHHostKeyDigests)),
		}
		if environment.ImageProvenance != nil {
			projected.ImageReference = redactMigrationInspectionText(environment.ImageProvenance.Reference)
		}
		for proposalIndex, proposal := range environment.WorkspaceProposals {
			projected.WorkspaceProposals[proposalIndex] = MigrationBundleWorkspaceProjection{
				ProposalID: proposal.ProposalID, GuestPath: proposal.GuestPath,
				HostPathHint: redactMigrationInspectionText(proposal.HostPathHint), State: proposal.State,
			}
		}
		projection.Environments[index] = projected
	}
	for index, disk := range manifest.DiskObjects {
		projection.Disks[index] = MigrationBundleDiskProjection{
			DiskID: disk.DiskID, Role: disk.Role, Format: disk.Format,
			LogicalBytes: disk.LogicalBytes, AllocatedBytesHint: disk.AllocatedBytesHint,
			Provider: disk.Provider.Name, ProviderKind: disk.Provider.Kind,
			Features: append([]string(nil), disk.Provider.Features...),
		}
	}
	for index, secret := range manifest.SecretEntries {
		projection.Secrets[index] = MigrationBundleSecretProjection{
			SecretRef:   secret.SecretRef,
			DisplayName: redactMigrationInspectionText(secret.DisplayName),
			Provider:    secret.Provider, Transfer: secret.Transfer,
			ValueIncluded: secret.Transfer == migration.SecretSelectedValue,
		}
	}
	for index, proposal := range manifest.AuthorityProposals {
		projection.AuthorityProposals[index] = MigrationBundleAuthorityProjection{
			ProposalID: proposal.ProposalID, Class: proposal.Class,
			SourceSummary: redactMigrationInspectionText(proposal.SourceSummary),
			State:         proposal.State,
		}
	}
	for _, component := range manifest.ComponentIndex {
		switch component.Kind {
		case "profile":
			projection.Components.Profiles++
		case "profile-state":
			projection.Components.ProfileStates++
		case "environment":
			projection.Components.Environments++
		case "disk":
			projection.Components.Disks++
		case "secret-value":
			projection.Components.SecretValues++
		case "provider-metadata":
			projection.Components.ProviderMetadata++
		}
		projection.Components.Total++
	}
	projection.Warnings = migrationInspectionWarnings(manifest)
	if err := projection.Validate(); err != nil {
		return MigrationBundleInspectionProjection{}, err
	}
	return projection, nil
}

func validateSealedBundleInspection(inspection migration.SealedBundleInspection) error {
	if _, err := time.Parse(time.RFC3339Nano, inspection.CreatedAt); err != nil {
		return errors.Join(ErrMigrationPlanInvalid, errors.New("bundle creation time is invalid"))
	}
	if validateMigrationBundleBinding(inspection.Binding) != nil ||
		inspection.Manifest.Validate(migration.DefaultLimits()) != nil ||
		inspection.Manifest.BundleID != inspection.Binding.BundleID ||
		inspection.Summary.BundleID != inspection.Binding.BundleID ||
		inspection.Summary.ManifestDigest != inspection.Binding.ManifestDigest ||
		!inspection.Summary.Sealed || inspection.Summary.RecordCount < 3 ||
		inspection.Summary.EncodedBytes == 0 || inspection.Summary.PrefixDigest.Validate() != nil {
		return ErrMigrationPlanInvalid
	}
	last := inspection.Manifest.ComponentIndex[len(inspection.Manifest.ComponentIndex)-1]
	if inspection.Summary.RecordCount != last.LastRecord+3 {
		return ErrMigrationPlanInvalid
	}
	var logical uint64
	for _, disk := range inspection.Manifest.DiskObjects {
		if disk.LogicalBytes > ^uint64(0)-logical {
			return ErrMigrationPlanInvalid
		}
		logical += disk.LogicalBytes
	}
	for _, component := range inspection.Manifest.ComponentIndex {
		if component.Kind != "profile-state" {
			continue
		}
		if component.LogicalBytes > ^uint64(0)-logical {
			return ErrMigrationPlanInvalid
		}
		logical += component.LogicalBytes
	}
	if logical != inspection.Summary.LogicalBytes {
		return ErrMigrationPlanInvalid
	}
	return nil
}

func migrationInspectionWarnings(manifest migration.Manifest) []migration.PlanNotice {
	warnings := make([]migration.PlanNotice, 0, 3)
	for _, environment := range manifest.Environments {
		if environment.Mode != migration.ExportModeFull {
			continue
		}
		warnings = append(warnings, migration.PlanNotice{
			Code:        "migration.bundle.full_state_may_contain_secrets",
			Summary:     "A full guest disk and profile application state can contain credentials that Hideout cannot enumerate.",
			Remediation: "Review the source guest before importing it onto another computer.",
		})
		break
	}
	for _, secret := range manifest.SecretEntries {
		if secret.Transfer != migration.SecretSelectedValue {
			continue
		}
		warnings = append(warnings, migration.PlanNotice{
			Code:        "migration.bundle.selected_secret_values",
			Summary:     "The encrypted bundle contains explicitly selected Hideout-managed secret values.",
			Remediation: "Import them only into a reviewed destination secret provider.",
		})
		break
	}
	if len(manifest.AuthorityProposals) > 0 {
		warnings = append(warnings, migration.PlanNotice{
			Code:        "migration.bundle.authority_disabled",
			Summary:     "Imported host and network authority is disabled until reviewed on this destination.",
			Remediation: "Review each proposal independently during import planning.",
		})
	}
	slices.SortFunc(warnings, func(left, right migration.PlanNotice) int {
		if left.Code < right.Code {
			return -1
		}
		if left.Code > right.Code {
			return 1
		}
		return 0
	})
	return warnings
}

func redactMigrationInspectionText(value string) string {
	value = audit.RedactString(value)
	value = migrationURLUserInfoPattern.ReplaceAllString(value, `${1}REDACTED@`)
	value = migrationSensitiveAssignmentPattern.ReplaceAllString(value, "credential=REDACTED")
	return truncateMigrationText(value, 4096)
}

func (projection MigrationBundleInspectionProjection) Validate() error {
	if projection.Schema != MigrationBundleInspectionSchema ||
		projection.BundleID == "" || projection.FormatVersion != migration.BundleFormatVersion ||
		projection.CreatedAt == "" || !projection.Sealed ||
		len(projection.Environments) == 0 || projection.Components.Total == 0 {
		return fmt.Errorf("%w: bundle inspection projection", ErrMigrationPlanInvalid)
	}
	return nil
}
