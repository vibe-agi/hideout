package migration

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

const (
	BundleFormatVersion  uint16 = 1
	RecordFrameVersion   uint8  = 1
	RecordPrivateVersion uint8  = 1

	BundleMagic    = "HIDMIG01"
	RecordMagic    = "HIDREC01"
	BundleEndMagic = "HIDEND01"

	SuiteV1 = "argon2id-hkdf-sha256-xchacha20poly1305-zstd-v1"

	AdoptionRequestSchema            = "hideout.migration-adoption-request/v1"
	AdoptionReceiptSchema            = "hideout.migration-adoption-receipt/v1"
	IdentityObservationRequestSchema = "hideout.migration-identity-observation-request/v1"
	IdentityObservationReceiptSchema = "hideout.migration-identity-observation-receipt/v1"
	AdoptionHelperPackage            = "hideout-migration-adopt"
	AdoptionGuestRequestPath         = "/run/hideout-migration-request/request.json"
	AdoptionGuestReceiptPath         = "/run/hideout-migration-receipt/receipt.json"

	AdoptionActionResetMachineID   = "reset-machine-id"
	AdoptionActionResetSSHHostKeys = "reset-ssh-host-keys"
	AdoptionActionPreserveIdentity = "preserve-guest-identity"
	AdoptionActionInstallSSHKeys   = "install-destination-ssh-keys"
	AdoptionActionStatusCompleted  = "completed"
	AdoptionActionStatusFailed     = "failed"
	AdoptionReceiptStatusCompleted = "completed"
	AdoptionReceiptStatusFailed    = "failed"
)

var (
	bundleIDPattern = regexp.MustCompile(`^migb_[A-Za-z0-9_-]{8,123}$`)
	opaqueIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{7,127}$`)
	digestPattern   = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	// Adoption is one effect of the durable Manager migration operation, not a
	// second independently named transaction. Accept the canonical Manager
	// operation envelope so provider binding and guest receipts can be equal.
	adoptionOperationIDPattern = regexp.MustCompile(`^op_[A-Za-z0-9_-]{8,124}$`)
	adoptionVersionPattern     = regexp.MustCompile(
		`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`,
	)
	adoptionSSHKeyPattern = regexp.MustCompile(
		`^(?:ssh-ed25519|ecdsa-sha2-nistp256|ssh-rsa) [A-Za-z0-9+/=]+(?: [^\r\n]{0,255})?$`,
	)
	adoptionCodePattern = regexp.MustCompile(`^migration\.[a-z0-9._-]{1,119}$`)
)

type BundleID string
type OpaqueID string
type Digest string

func ParseBundleID(value string) (BundleID, error) {
	if !bundleIDPattern.MatchString(value) {
		return "", fmt.Errorf("%w: bundle ID is invalid", ErrInvalidBundle)
	}
	return BundleID(value), nil
}

func ParseOpaqueID(value string) (OpaqueID, error) {
	if !opaqueIDPattern.MatchString(value) {
		return "", fmt.Errorf("%w: opaque ID is invalid", ErrInvalidBundle)
	}
	return OpaqueID(value), nil
}

func (digest Digest) Validate() error {
	if !digestPattern.MatchString(string(digest)) {
		return fmt.Errorf("%w: digest is invalid", ErrInvalidBundle)
	}
	return nil
}

// KDFParameters are authenticated public inputs. Validate them before calling
// Argon2id so hostile files cannot select unbounded work.
type KDFParameters struct {
	MemoryKiB uint32 `json:"memoryKiB"`
	Passes    uint32 `json:"passes"`
	Lanes     uint8  `json:"lanes"`
}

// PublicHeader contains only the facts necessary to bound parsing and unwrap
// the random master key. Byte slices use JSON base64 encoding.
type PublicHeader struct {
	BundleID         BundleID      `json:"bundleId"`
	CreatedAt        string        `json:"createdAt"`
	Suite            string        `json:"suite"`
	KDF              KDFParameters `json:"kdf"`
	Salt             []byte        `json:"salt"`
	WrapNonce        []byte        `json:"wrapNonce"`
	WrappedMasterKey []byte        `json:"wrappedMasterKey"`
	Limits           Limits        `json:"limits"`
}

type RecordType uint8

const (
	RecordMetadata RecordType = iota + 1
	RecordDataChunk
	RecordRawChunk
	RecordZeroExtent
	RecordHoleExtent
	RecordSecretValue
	RecordCheckpoint
	RecordFinalManifest
	RecordCompletion
)

type RecordFlags uint16

const (
	RecordFlagOptional RecordFlags = 1 << iota
)

// PrivateRecordHeader is encrypted with its payload. Its digest is also bound
// into the public frame header so lengths and record intent are authenticated.
type PrivateRecordHeader struct {
	Version         uint8       `json:"version"`
	Type            RecordType  `json:"type"`
	Flags           RecordFlags `json:"flags"`
	ComponentID     OpaqueID    `json:"componentId"`
	Ordinal         uint64      `json:"ordinal"`
	LogicalOffset   uint64      `json:"logicalOffset"`
	PlaintextLength uint64      `json:"plaintextLength"`
	EncodedLength   uint64      `json:"encodedLength"`
	PlaintextDigest Digest      `json:"plaintextDigest"`
}

func (header PrivateRecordHeader) Validate(limits Limits) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	if header.Version != RecordPrivateVersion {
		return fmt.Errorf("%w: private record version is unsupported", ErrUnsupportedVersion)
	}
	if !validRecordType(header.Type) {
		return fmt.Errorf("%w: record type %d has no v1 extension envelope", ErrUnsupportedRecord, header.Type)
	}
	if header.Flags&^RecordFlagOptional != 0 {
		return fmt.Errorf("%w: record flags are invalid", ErrInvalidBundle)
	}
	if _, err := ParseOpaqueID(string(header.ComponentID)); err != nil {
		return err
	}
	if err := header.PlaintextDigest.Validate(); err != nil {
		return err
	}
	if header.LogicalOffset > limits.MaxLogicalBytes ||
		header.PlaintextLength > limits.MaxLogicalBytes-header.LogicalOffset {
		return fmt.Errorf("%w: record logical extent exceeds the bundle limit", ErrLimitExceeded)
	}

	var plaintextLimit uint64
	switch header.Type {
	case RecordDataChunk, RecordRawChunk:
		plaintextLimit = uint64(limits.MaxChunkBytes)
	case RecordZeroExtent, RecordHoleExtent:
		// Sparse records carry no payload allocation. Providers deliberately
		// coalesce adjacent sparse ranges, so bounding them to the data chunk
		// size would either reject valid disks or force a noncanonical split.
		plaintextLimit = limits.MaxLogicalBytes
	case RecordFinalManifest:
		plaintextLimit = uint64(limits.MaxManifestBytes)
	default:
		plaintextLimit = uint64(limits.MaxMetadataBytes)
	}
	if header.PlaintextLength > plaintextLimit {
		return fmt.Errorf("%w: record plaintext exceeds its type limit", ErrLimitExceeded)
	}
	if header.EncodedLength > header.PlaintextLength+uint64(HardMaxRecordOverhead) {
		return fmt.Errorf("%w: encoded record exceeds bounded overhead", ErrLimitExceeded)
	}
	switch header.Type {
	case RecordZeroExtent, RecordHoleExtent:
		if header.PlaintextLength == 0 || header.EncodedLength != 0 {
			return fmt.Errorf("%w: sparse extent lengths are invalid", ErrInvalidBundle)
		}
	default:
		if header.PlaintextLength == 0 || header.EncodedLength == 0 {
			return fmt.Errorf("%w: payload record is empty", ErrInvalidBundle)
		}
	}
	return nil
}

func validRecordType(recordType RecordType) bool {
	return recordType >= RecordMetadata && recordType <= RecordCompletion
}

type ExportMode string

const (
	ExportModeConfig ExportMode = "config"
	ExportModeFull   ExportMode = "full"
)

type DiskRole string

const (
	DiskRoleRoot     DiskRole = "root"
	DiskRoleAttached DiskRole = "attached"
)

type ExtentKind string

const (
	ExtentData ExtentKind = "data"
	ExtentZero ExtentKind = "zero"
	ExtentHole ExtentKind = "hole"
)

type SecretTransfer string

const (
	SecretReferenceOnly SecretTransfer = "reference-only"
	SecretSelectedValue SecretTransfer = "selected-value"
	SecretNonExportable SecretTransfer = "non-exportable"
)

type Manifest struct {
	Schema               string                `json:"schema"`
	BundleID             BundleID              `json:"bundleId"`
	FormatVersion        uint16                `json:"formatVersion"`
	SourceProduct        SourceProduct         `json:"sourceProduct"`
	Environments         []EnvironmentSnapshot `json:"environments"`
	DiskObjects          []DiskObject          `json:"diskObjects"`
	DiskEdges            []DiskEdge            `json:"diskEdges"`
	SecretEntries        []SecretEntry         `json:"secretEntries"`
	AuthorityProposals   []AuthorityProposal   `json:"authorityProposals"`
	ComponentIndex       []ComponentIndexEntry `json:"componentIndex"`
	ExcludedClasses      []string              `json:"excludedClasses"`
	RequiredCapabilities []RequiredCapability  `json:"requiredCapabilities"`
}

type SourceProduct struct {
	Version        string `json:"version"`
	HostOS         string `json:"hostOS"`
	HostArch       string `json:"hostArch"`
	Backend        string `json:"backend"`
	BackendVersion string `json:"backendVersion"`
	GuestArch      string `json:"guestArch"`
}

type EnvironmentSnapshot struct {
	SourceEnvironmentRef  OpaqueID              `json:"sourceEnvironmentRef"`
	DisplayNameHint       string                `json:"displayNameHint"`
	Runtime               string                `json:"runtime"`
	GuestUser             string                `json:"guestUser"`
	Backend               string                `json:"backend"`
	Mode                  ExportMode            `json:"mode"`
	ImageProvenance       *ImageProvenance      `json:"imageProvenance,omitempty"`
	ProfileComponentID    OpaqueID              `json:"profileComponentId"`
	WorkspaceProposals    []WorkspaceProposal   `json:"workspaceProposals"`
	AuthorityProposalRefs []OpaqueID            `json:"authorityProposalRefs"`
	GuestIdentityEvidence GuestIdentityEvidence `json:"guestIdentityEvidence"`
	DiskRefs              []OpaqueID            `json:"diskRefs"`
}

type ImageProvenance struct {
	Reference string `json:"reference"`
	Digest    Digest `json:"digest"`
}

type WorkspaceProposal struct {
	ProposalID   OpaqueID `json:"proposalId"`
	GuestPath    string   `json:"guestPath"`
	HostPathHint string   `json:"hostPathHint"`
	State        string   `json:"state"`
}

type GuestIdentityEvidence struct {
	MachineIDDigest   Digest   `json:"machineIdDigest"`
	SSHHostKeyDigests []Digest `json:"sshHostKeyDigests"`
}

type DiskObject struct {
	DiskID             OpaqueID          `json:"diskId"`
	Role               DiskRole          `json:"role"`
	Format             string            `json:"format"`
	LogicalBytes       uint64            `json:"logicalBytes"`
	AllocatedBytesHint uint64            `json:"allocatedBytesHint"`
	ContentDigest      Digest            `json:"contentDigest"`
	Provider           ProviderDiskFacts `json:"provider"`
}

type ProviderDiskFacts struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Features []string `json:"features"`
}

type DiskEdge struct {
	EnvironmentRef OpaqueID `json:"environmentRef"`
	DiskID         OpaqueID `json:"diskId"`
	Attachment     DiskRole `json:"attachment"`
	GuestPath      string   `json:"guestPath"`
	ReadOnly       bool     `json:"readOnly"`
}

type SecretEntry struct {
	SecretRef            OpaqueID       `json:"secretRef"`
	DisplayName          string         `json:"displayName"`
	Provider             string         `json:"provider"`
	RequiredAvailability string         `json:"requiredAvailability"`
	EnvironmentRefs      []OpaqueID     `json:"environmentRefs"`
	Transfer             SecretTransfer `json:"transfer"`
	ValueComponentID     OpaqueID       `json:"valueComponentId,omitempty"`
}

type AuthorityProposal struct {
	ProposalID    OpaqueID `json:"proposalId"`
	Class         string   `json:"class"`
	SourceSummary string   `json:"sourceSummary"`
	State         string   `json:"state"`
}

type ComponentIndexEntry struct {
	ComponentID   OpaqueID `json:"componentId"`
	Kind          string   `json:"kind"`
	DiskID        OpaqueID `json:"diskId,omitempty"`
	LogicalBytes  uint64   `json:"logicalBytes"`
	FirstRecord   uint64   `json:"firstRecord"`
	LastRecord    uint64   `json:"lastRecord"`
	RecordCount   uint64   `json:"recordCount"`
	ContentDigest Digest   `json:"contentDigest"`
}

type RequiredCapability struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	MinimumVersion string `json:"minimumVersion,omitempty"`
}

// Checkpoint is authenticated inside the bundle and can only corroborate, not
// replace, the Manager's durable operation binding.
type Checkpoint struct {
	Schema                string     `json:"schema"`
	BundleID              BundleID   `json:"bundleId"`
	OperationID           OpaqueID   `json:"operationId"`
	LastSequence          uint64     `json:"lastSequence"`
	CompletedComponents   []OpaqueID `json:"completedComponents"`
	CurrentComponent      OpaqueID   `json:"currentComponent"`
	NextOrdinal           uint64     `json:"nextOrdinal"`
	CompletedLogicalBytes uint64     `json:"completedLogicalBytes"`
	CompletedEncodedBytes uint64     `json:"completedEncodedBytes"`
	PrefixDigest          Digest     `json:"prefixDigest"`
}

type Completion struct {
	Schema              string   `json:"schema"`
	BundleID            BundleID `json:"bundleId"`
	ManifestSequence    uint64   `json:"manifestSequence"`
	ManifestFrameDigest Digest   `json:"manifestFrameDigest"`
	RecordCount         uint64   `json:"recordCount"`
	PrefixDigest        Digest   `json:"prefixDigest"`
	LogicalBytes        uint64   `json:"logicalBytes"`
	EncodedBytes        uint64   `json:"encodedBytes"`
}

type BundleBinding struct {
	BundleID         BundleID `json:"bundleId"`
	FormatVersion    uint16   `json:"formatVersion"`
	FileDigest       Digest   `json:"fileDigest"`
	ManifestDigest   Digest   `json:"manifestDigest"`
	CompletionDigest Digest   `json:"completionDigest"`
}

type BaseRevision struct {
	Resource string `json:"resource"`
	Revision uint64 `json:"revision"`
	Digest   Digest `json:"digest"`
}

type PlannedEffect struct {
	ID           OpaqueID `json:"id"`
	Kind         string   `json:"kind"`
	Provider     string   `json:"provider"`
	Compensation string   `json:"compensation"`
}

type ExportRequest struct {
	Schema               string     `json:"schema"`
	Mode                 ExportMode `json:"mode"`
	EnvironmentNames     []string   `json:"environmentNames"`
	IncludeSecretRefs    []string   `json:"includeSecretRefs"`
	OutputPath           string     `json:"outputPath"`
	RiskAcknowledgements []string   `json:"riskAcknowledgements"`
}

// ExportEnvironmentEstimate is one concrete, reviewable source environment.
// EstimatedLogicalBytes includes its portable profile plus every disk it
// references. A disk shared by multiple environments therefore appears in
// each environment estimate, while ExportPlan.EstimatedPayloadLogicalBytes
// counts that disk only once.
type ExportEnvironmentEstimate struct {
	EnvironmentRef             OpaqueID   `json:"environmentRef"`
	DisplayName                string     `json:"displayName"`
	PortableConfigLogicalBytes uint64     `json:"portableConfigLogicalBytes"`
	PortableConfigDigest       Digest     `json:"portableConfigDigest"`
	DiskRefs                   []OpaqueID `json:"diskRefs"`
	ReferencedDiskLogicalBytes uint64     `json:"referencedDiskLogicalBytes"`
	EstimatedLogicalBytes      uint64     `json:"estimatedLogicalBytes"`
}

// ExportDiskEstimate authenticates the logical relationship and size facts
// returned by the source provider without exposing provider paths or handles.
type ExportDiskEstimate struct {
	DiskRef            OpaqueID   `json:"diskRef"`
	Role               DiskRole   `json:"role"`
	LogicalBytes       uint64     `json:"logicalBytes"`
	AllocatedBytesHint uint64     `json:"allocatedBytesHint"`
	Consumers          []OpaqueID `json:"consumers"`
}

type ExportPlan struct {
	Schema                       string                      `json:"schema"`
	PlanID                       OpaqueID                    `json:"planId"`
	PlanDigest                   Digest                      `json:"planDigest"`
	BaseRevisions                []BaseRevision              `json:"baseRevisions"`
	Mode                         ExportMode                  `json:"mode"`
	EnvironmentRefs              []OpaqueID                  `json:"environmentRefs"`
	DiskRefs                     []OpaqueID                  `json:"diskRefs"`
	SelectedSecretRefs           []string                    `json:"selectedSecretRefs"`
	IncludedClasses              []string                    `json:"includedClasses"`
	ExcludedClasses              []string                    `json:"excludedClasses"`
	EnvironmentEstimates         []ExportEnvironmentEstimate `json:"environmentEstimates"`
	DiskEstimates                []ExportDiskEstimate        `json:"diskEstimates"`
	EstimatedPayloadLogicalBytes uint64                      `json:"estimatedPayloadLogicalBytes"`
	EstimatedPayloadComplete     bool                        `json:"estimatedPayloadComplete"`
	OutputPath                   string                      `json:"outputPath"`
	ProviderCapabilityRevision   Digest                      `json:"providerCapabilityRevision"`
	SourceInventoryDigest        Digest                      `json:"sourceInventoryDigest"`
	Warnings                     []PlanNotice                `json:"warnings"`
	Effects                      []PlannedEffect             `json:"effects"`
	ConfirmationText             string                      `json:"confirmationText"`
	RiskAcknowledgements         []string                    `json:"riskAcknowledgements,omitempty"`
}

type ImportDraft struct {
	Schema                  string              `json:"schema"`
	BundlePath              string              `json:"bundlePath"`
	BundleBinding           BundleBinding       `json:"bundleBinding"`
	SelectedEnvironmentRefs []OpaqueID          `json:"selectedEnvironmentRefs"`
	NameMappings            []NameMapping       `json:"nameMappings"`
	ConflictDecisions       []ConflictDecision  `json:"conflictDecisions"`
	WorkspaceMappings       []WorkspaceMapping  `json:"workspaceMappings"`
	SecretMappings          []SecretMapping     `json:"secretMappings"`
	IdentityPolicies        []IdentitySelection `json:"identityPolicies"`
	AuthorityDecisions      []AuthorityDecision `json:"authorityDecisions"`
	RiskAcknowledgements    []string            `json:"riskAcknowledgements,omitempty"`
}

type NameMapping struct {
	SourceRef       OpaqueID `json:"sourceRef"`
	DestinationName string   `json:"destinationName"`
}

// ConflictDecision binds a separately completed destructive lifecycle action
// to one imported source object. The delete operation is not executed by the
// import apply path: it has its own immutable plan and confirmation boundary.
type ConflictDecision struct {
	SourceRef            OpaqueID `json:"sourceRef"`
	Decision             string   `json:"decision"`
	LifecycleOperationID string   `json:"lifecycleOperationId"`
	LifecyclePlanDigest  Digest   `json:"lifecyclePlanDigest"`
}

type WorkspaceMapping struct {
	ProposalID      OpaqueID `json:"proposalId"`
	Decision        string   `json:"decision"`
	DestinationPath string   `json:"destinationPath,omitempty"`
}

type SecretMapping struct {
	SourceRef      OpaqueID `json:"sourceRef"`
	Decision       string   `json:"decision"`
	DestinationRef string   `json:"destinationRef,omitempty"`
}

type IdentitySelection struct {
	SourceRef OpaqueID            `json:"sourceRef"`
	Policy    GuestIdentityPolicy `json:"policy"`
}

type AuthorityDecision struct {
	ProposalID       OpaqueID `json:"proposalId"`
	Decision         string   `json:"decision"`
	DestinationValue string   `json:"destinationValue,omitempty"`
}

type ImportPlan struct {
	Schema               string              `json:"schema"`
	PlanID               OpaqueID            `json:"planId"`
	PlanDigest           Digest              `json:"planDigest"`
	BundlePath           string              `json:"bundlePath"`
	BundleBinding        BundleBinding       `json:"bundleBinding"`
	BaseRevisions        []BaseRevision      `json:"baseRevisions"`
	Compatibility        Compatibility       `json:"compatibility"`
	Objects              []ImportObject      `json:"objects"`
	ConflictActions      []ConflictAction    `json:"conflictActions"`
	EnvironmentActions   []EnvironmentAction `json:"environmentActions"`
	IdentityActions      []IdentityAction    `json:"identityActions"`
	WorkspaceActions     []WorkspaceAction   `json:"workspaceActions"`
	SecretActions        []SecretAction      `json:"secretActions"`
	AuthorityActions     []AuthorityAction   `json:"authorityActions"`
	DisabledProposals    []OpaqueID          `json:"disabledProposals"`
	RiskAcknowledgements []string            `json:"riskAcknowledgements"`
	Effects              []PlannedEffect     `json:"effects"`
	Blockers             []PlanNotice        `json:"blockers"`
}

// ConflictAction is the destination-specific, reviewable result of conflict
// planning. Refused conflicts remain blockers. A replacement action records
// only durable proof of a separately confirmed lifecycle delete; import never
// receives implicit authority to destroy an existing destination object.
type ConflictAction struct {
	SourceRef             OpaqueID `json:"sourceRef"`
	DestinationName       string   `json:"destinationName"`
	Kind                  string   `json:"kind"`
	Decision              string   `json:"decision"`
	ExistingEnvironmentID string   `json:"existingEnvironmentId,omitempty"`
	ExistingStatus        string   `json:"existingStatus,omitempty"`
	LifecycleOperationID  string   `json:"lifecycleOperationId,omitempty"`
	LifecyclePlanDigest   Digest   `json:"lifecyclePlanDigest,omitempty"`
	Destructive           bool     `json:"destructive"`
	Effects               []string `json:"effects"`
	Recovery              string   `json:"recovery"`
}

// EnvironmentAction freezes authenticated source facts needed to stage and
// later publish one imported environment. The profile bytes remain encrypted
// until materialization; their exact component digest and size are nevertheless
// part of the confirmed destination plan.
type EnvironmentAction struct {
	SourceRef              OpaqueID `json:"sourceRef"`
	DestinationProfileName string   `json:"destinationProfileName"`
	Runtime                string   `json:"runtime"`
	GuestUser              string   `json:"guestUser"`
	Backend                string   `json:"backend"`
	ProfileComponentID     OpaqueID `json:"profileComponentId"`
	ProfileContentDigest   Digest   `json:"profileContentDigest"`
	ProfileLogicalBytes    uint64   `json:"profileLogicalBytes"`
}

// WorkspaceAction is destination authority frozen by an import plan. Source
// hints never become active paths: a mapped action contains the destination's
// canonical path and real directory identity captured during planning, while a
// disabled action contains neither.
type WorkspaceAction struct {
	ProposalID      OpaqueID `json:"proposalId"`
	EnvironmentRef  OpaqueID `json:"environmentRef"`
	GuestPath       string   `json:"guestPath"`
	Decision        string   `json:"decision"`
	DestinationPath string   `json:"destinationPath,omitempty"`
	RootDevice      uint64   `json:"rootDevice,omitempty"`
	RootInode       uint64   `json:"rootInode,omitempty"`
}

type Compatibility struct {
	Backend            string              `json:"backend"`
	Available          bool                `json:"available"`
	CapabilityRevision Digest              `json:"capabilityRevision"`
	RequiredBytes      uint64              `json:"requiredBytes"`
	AvailableBytes     uint64              `json:"availableBytes"`
	Capacity           CapacityRequirement `json:"capacity"`
	ReasonCode         string              `json:"reasonCode,omitempty"`
}

const CapacityRequirementSchema = "hideout.migration-capacity-requirement/v1"

// CapacityRequirement makes the import-space claim reviewable. BundleBytes is
// already occupied by the operator-supplied artifact and is reported rather
// than charged again. Staging and Final describe the same operation-owned disk
// objects because activation is an in-filesystem no-replace rename. Peak is the
// additional free space that must remain available before materialization.
type CapacityRequirement struct {
	Schema               string `json:"schema,omitempty"`
	BundleBytes          uint64 `json:"bundleBytes"`
	StagingBytes         uint64 `json:"stagingBytes"`
	ValidationBytes      uint64 `json:"validationBytes"`
	RollbackReserveBytes uint64 `json:"rollbackReserveBytes"`
	FinalBytes           uint64 `json:"finalBytes"`
	PeakAdditionalBytes  uint64 `json:"peakAdditionalBytes"`
}

func (requirement CapacityRequirement) IsZero() bool {
	return requirement == (CapacityRequirement{})
}

func (requirement CapacityRequirement) Validate() error {
	if requirement.Schema != CapacityRequirementSchema || requirement.BundleBytes == 0 ||
		requirement.StagingBytes == 0 || requirement.ValidationBytes == 0 ||
		requirement.RollbackReserveBytes == 0 ||
		requirement.FinalBytes != requirement.StagingBytes ||
		requirement.StagingBytes > HardMaxLogicalBytes ||
		requirement.ValidationBytes > HardMaxLogicalBytes-requirement.StagingBytes ||
		requirement.RollbackReserveBytes >
			HardMaxLogicalBytes-requirement.StagingBytes-requirement.ValidationBytes ||
		requirement.PeakAdditionalBytes != requirement.StagingBytes+
			requirement.ValidationBytes+requirement.RollbackReserveBytes {
		return fmt.Errorf("%w: capacity requirement is invalid", ErrInvalidBundle)
	}
	return nil
}

type ImportObject struct {
	SourceRef       OpaqueID   `json:"sourceRef"`
	DestinationName string     `json:"destinationName"`
	Mode            ExportMode `json:"mode"`
	DiskRefs        []OpaqueID `json:"diskRefs"`
}

type IdentityAction struct {
	SourceRef            OpaqueID            `json:"sourceRef"`
	GuestPolicy          GuestIdentityPolicy `json:"guestPolicy"`
	FreshControlIdentity bool                `json:"freshControlIdentity"`
	FreshBackendIdentity bool                `json:"freshBackendIdentity"`
}

type SecretAction struct {
	SourceRef           OpaqueID       `json:"sourceRef"`
	Transfer            SecretTransfer `json:"transfer"`
	Decision            string         `json:"decision"`
	SourceProvider      string         `json:"sourceProvider"`
	DestinationProvider string         `json:"destinationProvider"`
	DestinationRef      string         `json:"destinationRef"`
	BaseGeneration      uint64         `json:"baseGeneration"`
	ValueComponentID    OpaqueID       `json:"valueComponentId,omitempty"`
	EnvironmentRefs     []OpaqueID     `json:"environmentRefs"`
}

type AuthorityAction struct {
	ProposalID       OpaqueID `json:"proposalId"`
	EnvironmentRef   OpaqueID `json:"environmentRef"`
	Class            string   `json:"class"`
	DestinationValue string   `json:"destinationValue"`
	Approved         bool     `json:"approved"`
}

type PlanNotice struct {
	Code        string   `json:"code"`
	Summary     string   `json:"summary"`
	Remediation string   `json:"remediation,omitempty"`
	SourceRef   OpaqueID `json:"sourceRef,omitempty"`
}

type AdoptionRequest struct {
	Schema             string                `json:"schema"`
	OperationID        OpaqueID              `json:"operationId"`
	EnvironmentRef     OpaqueID              `json:"environmentRef"`
	RequestNonce       OpaqueID              `json:"requestNonce"`
	ReceiptNonce       OpaqueID              `json:"receiptNonce"`
	Policy             GuestIdentityPolicy   `json:"policy"`
	SourceIdentity     GuestIdentityEvidence `json:"sourceIdentity"`
	DestinationSSHUser string                `json:"destinationSSHUser"`
	DestinationSSHKeys []string              `json:"destinationSSHKeys"`
	PermittedActions   []string              `json:"permittedActions"`
	Helper             HelperBinding         `json:"helper"`
}

type HelperBinding struct {
	PackageID string `json:"packageId"`
	Version   string `json:"version"`
	SHA256    Digest `json:"sha256"`
}

type AdoptionReceipt struct {
	Schema           string                 `json:"schema"`
	OperationID      OpaqueID               `json:"operationId"`
	EnvironmentRef   OpaqueID               `json:"environmentRef"`
	RequestNonce     OpaqueID               `json:"requestNonce"`
	ReceiptNonce     OpaqueID               `json:"receiptNonce"`
	Policy           GuestIdentityPolicy    `json:"policy"`
	Helper           HelperBinding          `json:"helper"`
	ActionResults    []AdoptionActionResult `json:"actionResults"`
	PostIdentity     *GuestIdentityEvidence `json:"postIdentity,omitempty"`
	Status           string                 `json:"status"`
	CompletionMarker bool                   `json:"completionMarker"`
	FailureCode      string                 `json:"failureCode,omitempty"`
}

type AdoptionActionResult struct {
	Action string `json:"action"`
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
}

// IdentityObservationRequest asks the fixed, package-owned guest helper to
// report identity evidence without applying an adoption policy. It deliberately
// carries no command, path, environment, network, or script input.
type IdentityObservationRequest struct {
	Schema         string        `json:"schema"`
	OperationID    OpaqueID      `json:"operationId"`
	EnvironmentRef OpaqueID      `json:"environmentRef"`
	RequestNonce   OpaqueID      `json:"requestNonce"`
	ReceiptNonce   OpaqueID      `json:"receiptNonce"`
	Helper         HelperBinding `json:"helper"`
}

type IdentityObservationReceipt struct {
	Schema           string                 `json:"schema"`
	OperationID      OpaqueID               `json:"operationId"`
	EnvironmentRef   OpaqueID               `json:"environmentRef"`
	RequestNonce     OpaqueID               `json:"requestNonce"`
	ReceiptNonce     OpaqueID               `json:"receiptNonce"`
	Helper           HelperBinding          `json:"helper"`
	Identity         *GuestIdentityEvidence `json:"identity,omitempty"`
	Status           string                 `json:"status"`
	CompletionMarker bool                   `json:"completionMarker"`
	FailureCode      string                 `json:"failureCode,omitempty"`
}

func (identity GuestIdentityEvidence) Validate() error {
	if identity.MachineIDDigest.Validate() != nil ||
		len(identity.SSHHostKeyDigests) == 0 || len(identity.SSHHostKeyDigests) > 32 {
		return fmt.Errorf("%w: guest identity evidence is invalid", ErrInvalidBundle)
	}
	seen := make(map[Digest]struct{}, len(identity.SSHHostKeyDigests))
	for _, digest := range identity.SSHHostKeyDigests {
		if digest.Validate() != nil {
			return fmt.Errorf("%w: SSH host identity digest is invalid", ErrInvalidBundle)
		}
		if _, exists := seen[digest]; exists {
			return fmt.Errorf("%w: SSH host identity digest is duplicated", ErrInvalidBundle)
		}
		seen[digest] = struct{}{}
	}
	return nil
}

func (helper HelperBinding) Validate() error {
	if helper.PackageID != AdoptionHelperPackage ||
		!adoptionVersionPattern.MatchString(helper.Version) ||
		helper.SHA256.Validate() != nil {
		return fmt.Errorf("%w: adoption helper binding is invalid", ErrInvalidBundle)
	}
	return nil
}

func (request IdentityObservationRequest) Validate() error {
	if request.Schema != IdentityObservationRequestSchema ||
		request.Helper.Validate() != nil {
		return fmt.Errorf("%w: identity observation request is invalid", ErrInvalidBundle)
	}
	for _, value := range []OpaqueID{
		request.OperationID, request.EnvironmentRef,
		request.RequestNonce, request.ReceiptNonce,
	} {
		if _, err := ParseOpaqueID(string(value)); err != nil {
			return err
		}
	}
	if request.RequestNonce == request.ReceiptNonce {
		return fmt.Errorf("%w: identity observation nonces overlap", ErrInvalidBundle)
	}
	return nil
}

func (receipt IdentityObservationReceipt) Validate() error {
	if receipt.Schema != IdentityObservationReceiptSchema ||
		receipt.Helper.Validate() != nil {
		return fmt.Errorf("%w: identity observation receipt is invalid", ErrInvalidBundle)
	}
	for _, value := range []OpaqueID{
		receipt.OperationID, receipt.EnvironmentRef,
		receipt.RequestNonce, receipt.ReceiptNonce,
	} {
		if _, err := ParseOpaqueID(string(value)); err != nil {
			return err
		}
	}
	if receipt.RequestNonce == receipt.ReceiptNonce {
		return fmt.Errorf("%w: identity observation receipt nonces overlap", ErrInvalidBundle)
	}
	switch receipt.Status {
	case AdoptionReceiptStatusCompleted:
		if !receipt.CompletionMarker || receipt.FailureCode != "" ||
			receipt.Identity == nil || receipt.Identity.Validate() != nil {
			return fmt.Errorf("%w: completed identity observation lacks evidence", ErrInvalidBundle)
		}
	case AdoptionReceiptStatusFailed:
		if receipt.CompletionMarker || receipt.Identity != nil ||
			!adoptionCodePattern.MatchString(receipt.FailureCode) {
			return fmt.Errorf("%w: failed identity observation is invalid", ErrInvalidBundle)
		}
	default:
		return fmt.Errorf("%w: identity observation status is invalid", ErrInvalidBundle)
	}
	return nil
}

func (receipt IdentityObservationReceipt) MatchesRequest(
	request IdentityObservationRequest,
) error {
	if request.Validate() != nil || receipt.Validate() != nil ||
		receipt.OperationID != request.OperationID ||
		receipt.EnvironmentRef != request.EnvironmentRef ||
		receipt.RequestNonce != request.RequestNonce ||
		receipt.ReceiptNonce != request.ReceiptNonce ||
		receipt.Helper != request.Helper {
		return fmt.Errorf("%w: identity observation receipt binding changed", ErrInvalidBundle)
	}
	return nil
}

func (request AdoptionRequest) Validate() error {
	if request.Schema != AdoptionRequestSchema ||
		!adoptionOperationIDPattern.MatchString(string(request.OperationID)) {
		return fmt.Errorf("%w: adoption request identity is invalid", ErrInvalidBundle)
	}
	for _, value := range []OpaqueID{
		request.OperationID, request.EnvironmentRef,
		request.RequestNonce, request.ReceiptNonce,
	} {
		if _, err := ParseOpaqueID(string(value)); err != nil {
			return err
		}
	}
	if request.RequestNonce == request.ReceiptNonce ||
		!validGuestIdentityPolicy(request.Policy) ||
		request.SourceIdentity.Validate() != nil || request.Helper.Validate() != nil {
		return fmt.Errorf("%w: adoption request binding is invalid", ErrInvalidBundle)
	}
	if !manifestGuestUserPattern.MatchString(request.DestinationSSHUser) ||
		request.DestinationSSHUser == "root" ||
		len(request.DestinationSSHKeys) == 0 || len(request.DestinationSSHKeys) > 32 {
		return fmt.Errorf("%w: destination SSH keys are invalid", ErrInvalidBundle)
	}
	seenKeys := make(map[string]struct{}, len(request.DestinationSSHKeys))
	for _, key := range request.DestinationSSHKeys {
		if len(key) > 8192 || key != strings.TrimSpace(key) ||
			!adoptionSSHKeyPattern.MatchString(key) {
			return fmt.Errorf("%w: destination SSH key is invalid", ErrInvalidBundle)
		}
		if _, exists := seenKeys[key]; exists {
			return fmt.Errorf("%w: destination SSH key is duplicated", ErrInvalidBundle)
		}
		seenKeys[key] = struct{}{}
	}
	expected := adoptionActionsForPolicy(request.Policy)
	if !reflect.DeepEqual(request.PermittedActions, expected) {
		return fmt.Errorf("%w: adoption actions do not match identity policy", ErrInvalidBundle)
	}
	return nil
}

func (receipt AdoptionReceipt) Validate() error {
	if receipt.Schema != AdoptionReceiptSchema ||
		!adoptionOperationIDPattern.MatchString(string(receipt.OperationID)) {
		return fmt.Errorf("%w: adoption receipt identity is invalid", ErrInvalidBundle)
	}
	for _, value := range []OpaqueID{
		receipt.OperationID, receipt.EnvironmentRef,
		receipt.RequestNonce, receipt.ReceiptNonce,
	} {
		if _, err := ParseOpaqueID(string(value)); err != nil {
			return err
		}
	}
	if receipt.RequestNonce == receipt.ReceiptNonce ||
		!validGuestIdentityPolicy(receipt.Policy) || receipt.Helper.Validate() != nil {
		return fmt.Errorf("%w: adoption receipt binding is invalid", ErrInvalidBundle)
	}
	expected := adoptionActionsForPolicy(receipt.Policy)
	if len(receipt.ActionResults) == 0 || len(receipt.ActionResults) > len(expected) {
		return fmt.Errorf("%w: adoption action results are invalid", ErrInvalidBundle)
	}
	for index, result := range receipt.ActionResults {
		if result.Action != expected[index] ||
			(result.Status != AdoptionActionStatusCompleted &&
				result.Status != AdoptionActionStatusFailed) ||
			(result.Status == AdoptionActionStatusCompleted && result.Code != "") ||
			(result.Status == AdoptionActionStatusFailed &&
				!adoptionCodePattern.MatchString(result.Code)) {
			return fmt.Errorf("%w: adoption action result is invalid", ErrInvalidBundle)
		}
	}
	switch receipt.Status {
	case AdoptionReceiptStatusCompleted:
		if !receipt.CompletionMarker || receipt.FailureCode != "" ||
			receipt.PostIdentity == nil || receipt.PostIdentity.Validate() != nil ||
			len(receipt.ActionResults) != len(expected) {
			return fmt.Errorf("%w: completed adoption receipt is invalid", ErrInvalidBundle)
		}
		for _, result := range receipt.ActionResults {
			if result.Status != AdoptionActionStatusCompleted {
				return fmt.Errorf("%w: completed adoption has a failed action", ErrInvalidBundle)
			}
		}
	case AdoptionReceiptStatusFailed:
		if receipt.CompletionMarker || !adoptionCodePattern.MatchString(receipt.FailureCode) {
			return fmt.Errorf("%w: failed adoption receipt is invalid", ErrInvalidBundle)
		}
		if receipt.PostIdentity != nil && receipt.PostIdentity.Validate() != nil {
			return fmt.Errorf("%w: failed adoption identity evidence is invalid", ErrInvalidBundle)
		}
	default:
		return fmt.Errorf("%w: adoption receipt status is invalid", ErrInvalidBundle)
	}
	return nil
}

// MatchesRequest verifies the nonce and helper binding before evaluating the
// selected per-import policy. It does not make the destination runnable.
func (receipt AdoptionReceipt) MatchesRequest(request AdoptionRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := receipt.Validate(); err != nil {
		return err
	}
	if receipt.OperationID != request.OperationID ||
		receipt.EnvironmentRef != request.EnvironmentRef ||
		receipt.RequestNonce != request.RequestNonce ||
		receipt.ReceiptNonce != request.ReceiptNonce ||
		receipt.Policy != request.Policy || receipt.Helper != request.Helper {
		return fmt.Errorf("%w: adoption receipt does not match request", ErrInvalidBundle)
	}
	if receipt.Status != AdoptionReceiptStatusCompleted {
		return nil
	}
	if request.Policy == GuestIdentityExactRestore {
		if !receipt.PostIdentity.Equal(request.SourceIdentity) {
			return fmt.Errorf("%w: exact guest identity was not preserved", ErrInvalidBundle)
		}
		return nil
	}
	if receipt.PostIdentity.MachineIDDigest == request.SourceIdentity.MachineIDDigest {
		return fmt.Errorf("%w: Safe Clone reused the source machine identity", ErrInvalidBundle)
	}
	sourceSSH := make(map[Digest]struct{}, len(request.SourceIdentity.SSHHostKeyDigests))
	for _, digest := range request.SourceIdentity.SSHHostKeyDigests {
		sourceSSH[digest] = struct{}{}
	}
	for _, digest := range receipt.PostIdentity.SSHHostKeyDigests {
		if _, exists := sourceSSH[digest]; exists {
			return fmt.Errorf("%w: Safe Clone reused a source SSH host identity", ErrInvalidBundle)
		}
	}
	return nil
}

func adoptionActionsForPolicy(policy GuestIdentityPolicy) []string {
	if policy == GuestIdentitySafeClone {
		return []string{
			AdoptionActionResetMachineID,
			AdoptionActionResetSSHHostKeys,
			AdoptionActionInstallSSHKeys,
		}
	}
	if policy == GuestIdentityExactRestore {
		return []string{AdoptionActionPreserveIdentity, AdoptionActionInstallSSHKeys}
	}
	return nil
}
