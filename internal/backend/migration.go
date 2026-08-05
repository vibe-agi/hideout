package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/migration"
)

var (
	ErrMigrationProviderRequest    = errors.New("migration provider request is invalid")
	ErrMigrationProviderResponse   = errors.New("migration provider response is invalid")
	ErrMigrationProviderCapability = errors.New("migration provider capability is invalid")
)

var (
	migrationProviderTokenPattern  = regexp.MustCompile(`^[a-z][a-z0-9.-]{1,127}$`)
	migrationProviderCodePattern   = regexp.MustCompile(`^migration\.provider\.[a-z][a-z0-9._-]{1,127}$`)
	migrationProviderObjectPattern = regexp.MustCompile(
		`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`,
	)
	migrationGuestUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// MigrationCapabilityProvider is the common discovery boundary. Export and
// import are intentionally separate interfaces: a provider may safely support
// immutable source capture while destination adoption remains unavailable.
type MigrationCapabilityProvider interface {
	MigrationCapabilities(context.Context) (MigrationCapabilities, error)
}

// MigrationExportProvider owns immutable source inspection and capture. Bundle
// crypto, output paths, and Manager publication remain outside this interface.
type MigrationExportProvider interface {
	MigrationCapabilityProvider
	InspectMigrationSource(context.Context, SourceInspectionRequest) (SourceInventory, error)
	SnapshotMigrationSource(context.Context, SourceSnapshotRequest) (SourceSnapshot, error)
	ReadMigrationComponent(context.Context, ComponentReadRequest, func(MigrationExtent) error) error
	ReleaseMigrationSnapshot(context.Context, SnapshotReleaseRequest) error
}

// MigrationImportProvider owns private destination staging, adoption,
// verification, stopped-object promotion, and rollback. It is absent until all
// of those effects can be proved; FullImport alone never synthesizes missing
// method authority.
type MigrationImportProvider interface {
	MigrationCapabilityProvider
	InspectMigrationDestination(context.Context, DestinationInspectionRequest) (DestinationInventory, error)
	StageMigrationDestination(context.Context, DestinationStageRequest) (DestinationStage, error)
	AdoptMigrationDestination(context.Context, DestinationAdoptionRequest) (DestinationAdoption, error)
	VerifyMigrationDestination(context.Context, DestinationVerifyRequest) (DestinationProof, error)
	ActivateMigrationDestination(context.Context, DestinationActivationRequest) (DestinationActivation, error)
	RollbackMigrationDestination(context.Context, DestinationRollbackRequest) error
}

// MigrationProvider is the complete optional full-state provider. It does not
// extend Backend; callers request only the narrower capability they need.
type MigrationProvider interface {
	MigrationExportProvider
	MigrationImportProvider
}

type MigrationEffectBinding struct {
	OperationID        migration.OpaqueID `json:"operationId"`
	EffectID           migration.OpaqueID `json:"effectId"`
	CapabilityRevision migration.Digest   `json:"capabilityRevision"`
}

func (binding MigrationEffectBinding) Validate() error {
	if _, err := migration.ParseOpaqueID(string(binding.OperationID)); err != nil {
		return fmt.Errorf("%w: operation identity", ErrMigrationProviderRequest)
	}
	if _, err := migration.ParseOpaqueID(string(binding.EffectID)); err != nil {
		return fmt.Errorf("%w: effect identity", ErrMigrationProviderRequest)
	}
	if err := binding.CapabilityRevision.Validate(); err != nil {
		return fmt.Errorf("%w: capability revision", ErrMigrationProviderRequest)
	}
	return nil
}

type MigrationArchitecturePair struct {
	Host  string `json:"host"`
	Guest string `json:"guest"`
}

type MigrationHelperCapability struct {
	PackageID         string           `json:"packageId"`
	Version           string           `json:"version"`
	GuestArchitecture string           `json:"guestArchitecture"`
	Digest            migration.Digest `json:"digest"`
}

type MigrationProviderBlocker struct {
	Code        string `json:"code"`
	Summary     string `json:"summary"`
	Remediation string `json:"remediation,omitempty"`
}

// MigrationCapabilities are live, revision-bound facts. Limits may be smaller
// than the bundle envelope but can never enlarge it.
type MigrationCapabilities struct {
	Provider            string                      `json:"provider"`
	ProviderVersion     string                      `json:"providerVersion"`
	Revision            migration.Digest            `json:"revision"`
	DiskRepresentations []string                    `json:"diskRepresentations"`
	ArchitecturePairs   []MigrationArchitecturePair `json:"architecturePairs"`
	FullExport          bool                        `json:"fullExport"`
	FullImport          bool                        `json:"fullImport"`
	RootDiskKinds       []string                    `json:"rootDiskKinds"`
	AttachedDiskKinds   []string                    `json:"attachedDiskKinds"`
	SparseExtents       bool                        `json:"sparseExtents"`
	Limits              migration.Limits            `json:"limits"`
	AdoptionHelper      *MigrationHelperCapability  `json:"adoptionHelper,omitempty"`
	Unavailable         *MigrationProviderBlocker   `json:"unavailable,omitempty"`
}

func (capability MigrationCapabilities) Validate() error {
	if !migrationProviderTokenPattern.MatchString(capability.Provider) ||
		!boundedProviderText(capability.ProviderVersion, 128) {
		return fmt.Errorf("%w: provider identity", ErrMigrationProviderCapability)
	}
	if err := capability.Revision.Validate(); err != nil {
		return fmt.Errorf("%w: revision", ErrMigrationProviderCapability)
	}
	if err := capability.Limits.Validate(); err != nil {
		return fmt.Errorf("%w: limits", ErrMigrationProviderCapability)
	}
	if err := validateProviderTokens(capability.DiskRepresentations, 16); err != nil {
		return err
	}
	if err := validateProviderTokens(capability.RootDiskKinds, 16); err != nil {
		return err
	}
	if err := validateProviderTokens(capability.AttachedDiskKinds, 16); err != nil {
		return err
	}
	if len(capability.ArchitecturePairs) == 0 || len(capability.ArchitecturePairs) > 16 {
		return fmt.Errorf("%w: architecture pairs", ErrMigrationProviderCapability)
	}
	seenPairs := make(map[string]struct{}, len(capability.ArchitecturePairs))
	for _, pair := range capability.ArchitecturePairs {
		if !validArchitecture(pair.Host) || !validArchitecture(pair.Guest) {
			return fmt.Errorf("%w: architecture pair", ErrMigrationProviderCapability)
		}
		key := pair.Host + "->" + pair.Guest
		if _, exists := seenPairs[key]; exists {
			return fmt.Errorf("%w: duplicate architecture pair", ErrMigrationProviderCapability)
		}
		seenPairs[key] = struct{}{}
	}
	if capability.FullExport &&
		(len(capability.DiskRepresentations) == 0 || len(capability.RootDiskKinds) == 0) {
		return fmt.Errorf("%w: full export lacks disk support", ErrMigrationProviderCapability)
	}
	if capability.FullImport && capability.AdoptionHelper == nil {
		return fmt.Errorf("%w: full import lacks adoption helper", ErrMigrationProviderCapability)
	}
	if capability.AdoptionHelper != nil {
		helper := capability.AdoptionHelper
		if !migrationProviderTokenPattern.MatchString(helper.PackageID) ||
			!boundedProviderText(helper.Version, 128) ||
			!validArchitecture(helper.GuestArchitecture) ||
			helper.Digest.Validate() != nil {
			return fmt.Errorf("%w: adoption helper", ErrMigrationProviderCapability)
		}
	}
	if capability.Unavailable != nil {
		blocker := capability.Unavailable
		if !migrationProviderCodePattern.MatchString(blocker.Code) ||
			!boundedProviderText(blocker.Summary, 512) ||
			(blocker.Remediation != "" && !boundedProviderText(blocker.Remediation, 1024)) {
			return fmt.Errorf("%w: unavailable reason", ErrMigrationProviderCapability)
		}
	}
	return nil
}

type MigrationLifecycleState string

const (
	MigrationLifecycleStopped       MigrationLifecycleState = "stopped"
	MigrationLifecycleRunning       MigrationLifecycleState = "running"
	MigrationLifecycleTransitioning MigrationLifecycleState = "transitioning"
	MigrationLifecycleUnknown       MigrationLifecycleState = "unknown"
)

// MigrationSourceSelection binds a Manager-owned source identity to the exact
// backend object that was recorded for it. The provider must never derive an
// instance name from EnvironmentRef: doing so would turn a display/control ID
// convention into storage authority.
type MigrationSourceSelection struct {
	EnvironmentRef   migration.OpaqueID `json:"environmentRef"`
	ProviderInstance string             `json:"providerInstance"`
}

type SourceInspectionRequest struct {
	Binding    MigrationEffectBinding     `json:"binding"`
	Mode       migration.ExportMode       `json:"mode"`
	Selections []MigrationSourceSelection `json:"selections"`
}

func (request SourceInspectionRequest) Validate() error {
	if err := request.Binding.Validate(); err != nil || request.Mode != migration.ExportModeFull ||
		len(request.Selections) == 0 || len(request.Selections) > int(migration.HardMaxEnvironments) {
		return fmt.Errorf("%w: source inspection envelope", ErrMigrationProviderRequest)
	}
	seenInstances := make(map[string]struct{}, len(request.Selections))
	var previous migration.OpaqueID
	for _, selection := range request.Selections {
		if _, err := migration.ParseOpaqueID(string(selection.EnvironmentRef)); err != nil ||
			!migrationProviderObjectPattern.MatchString(selection.ProviderInstance) ||
			(previous != "" && previous >= selection.EnvironmentRef) {
			return fmt.Errorf("%w: source selection", ErrMigrationProviderRequest)
		}
		if _, exists := seenInstances[selection.ProviderInstance]; exists {
			return fmt.Errorf("%w: duplicate provider instance", ErrMigrationProviderRequest)
		}
		seenInstances[selection.ProviderInstance] = struct{}{}
		previous = selection.EnvironmentRef
	}
	return nil
}

type MigrationSourceInstance struct {
	EnvironmentRef        migration.OpaqueID      `json:"environmentRef"`
	ProviderRef           migration.OpaqueID      `json:"providerRef"`
	Lifecycle             MigrationLifecycleState `json:"lifecycle"`
	ConfigurationRevision uint64                  `json:"configurationRevision"`
	ConfigurationDigest   migration.Digest        `json:"configurationDigest"`
	RootDiskRef           migration.OpaqueID      `json:"rootDiskRef"`
}

type MigrationSourceDisk struct {
	DiskRef            migration.OpaqueID   `json:"diskRef"`
	ProviderRef        migration.OpaqueID   `json:"providerRef"`
	Role               migration.DiskRole   `json:"role"`
	Format             string               `json:"format"`
	LogicalBytes       uint64               `json:"logicalBytes"`
	AllocatedBytesHint uint64               `json:"allocatedBytesHint"`
	Consumers          []migration.OpaqueID `json:"consumers"`
}

type MigrationSourceAttachment struct {
	EnvironmentRef migration.OpaqueID `json:"environmentRef"`
	DiskRef        migration.OpaqueID `json:"diskRef"`
	Attachment     migration.DiskRole `json:"attachment"`
	GuestPath      string             `json:"guestPath"`
	ReadOnly       bool               `json:"readOnly"`
}

type SourceInventory struct {
	Binding         MigrationEffectBinding      `json:"binding"`
	Provider        string                      `json:"provider"`
	InventoryDigest migration.Digest            `json:"inventoryDigest"`
	Instances       []MigrationSourceInstance   `json:"instances"`
	Disks           []MigrationSourceDisk       `json:"disks"`
	Attachments     []MigrationSourceAttachment `json:"attachments"`
	ExcludedClasses []string                    `json:"excludedClasses"`
	SelectionClosed bool                        `json:"selectionClosed"`
	Capturable      bool                        `json:"capturable"`
	Blockers        []MigrationProviderBlocker  `json:"blockers"`
}

func (inventory SourceInventory) Validate() error {
	if err := inventory.Binding.Validate(); err != nil ||
		!migrationProviderTokenPattern.MatchString(inventory.Provider) ||
		inventory.InventoryDigest.Validate() != nil ||
		len(inventory.Instances) == 0 ||
		len(inventory.Instances) > int(migration.HardMaxEnvironments) ||
		len(inventory.Disks) == 0 || len(inventory.Disks) > 256 ||
		len(inventory.Attachments) == 0 || len(inventory.Attachments) > 1024 ||
		len(inventory.ExcludedClasses) == 0 || len(inventory.ExcludedClasses) > 32 ||
		len(inventory.Blockers) > 256 ||
		inventory.Capturable != (inventory.SelectionClosed && len(inventory.Blockers) == 0) {
		return fmt.Errorf("%w: source inventory envelope", ErrMigrationProviderResponse)
	}

	environments := make(map[migration.OpaqueID]MigrationSourceInstance, len(inventory.Instances))
	rootRefs := make(map[migration.OpaqueID]migration.OpaqueID, len(inventory.Instances))
	providerRefs := make(map[migration.OpaqueID]struct{}, len(inventory.Instances)+len(inventory.Disks))
	var previousEnvironment migration.OpaqueID
	for _, instance := range inventory.Instances {
		if !validMigrationOpaqueID(instance.EnvironmentRef) ||
			!validMigrationOpaqueID(instance.ProviderRef) ||
			!validMigrationOpaqueID(instance.RootDiskRef) ||
			!validMigrationLifecycle(instance.Lifecycle) || instance.ConfigurationRevision == 0 ||
			instance.ConfigurationDigest.Validate() != nil ||
			(previousEnvironment != "" && previousEnvironment >= instance.EnvironmentRef) {
			return fmt.Errorf("%w: source instance", ErrMigrationProviderResponse)
		}
		if _, exists := providerRefs[instance.ProviderRef]; exists {
			return fmt.Errorf("%w: duplicate source provider reference", ErrMigrationProviderResponse)
		}
		providerRefs[instance.ProviderRef] = struct{}{}
		environments[instance.EnvironmentRef] = instance
		rootRefs[instance.RootDiskRef] = instance.EnvironmentRef
		previousEnvironment = instance.EnvironmentRef
	}

	disks := make(map[migration.OpaqueID]MigrationSourceDisk, len(inventory.Disks))
	var previousDisk migration.OpaqueID
	var aggregateLogical uint64
	for _, disk := range inventory.Disks {
		if !validMigrationOpaqueID(disk.DiskRef) ||
			!validMigrationOpaqueID(disk.ProviderRef) ||
			(disk.Role != migration.DiskRoleRoot && disk.Role != migration.DiskRoleAttached) ||
			!migrationProviderTokenPattern.MatchString(disk.Format) ||
			disk.LogicalBytes == 0 || disk.LogicalBytes > migration.HardMaxLogicalBytes ||
			disk.AllocatedBytesHint > disk.LogicalBytes ||
			len(disk.Consumers) == 0 || len(disk.Consumers) > int(migration.HardMaxEnvironments) ||
			(previousDisk != "" && previousDisk >= disk.DiskRef) {
			return fmt.Errorf("%w: source disk", ErrMigrationProviderResponse)
		}
		if _, exists := disks[disk.DiskRef]; exists {
			return fmt.Errorf("%w: duplicate source disk", ErrMigrationProviderResponse)
		}
		if _, exists := providerRefs[disk.ProviderRef]; exists {
			return fmt.Errorf("%w: duplicate source provider reference", ErrMigrationProviderResponse)
		}
		if disk.LogicalBytes > migration.HardMaxLogicalBytes-aggregateLogical {
			return fmt.Errorf("%w: aggregate source disk size", ErrMigrationProviderResponse)
		}
		aggregateLogical += disk.LogicalBytes
		providerRefs[disk.ProviderRef] = struct{}{}
		var previousConsumer migration.OpaqueID
		for _, consumer := range disk.Consumers {
			if _, exists := environments[consumer]; !exists ||
				(previousConsumer != "" && previousConsumer >= consumer) {
				return fmt.Errorf("%w: source disk consumer", ErrMigrationProviderResponse)
			}
			previousConsumer = consumer
		}
		if disk.Role == migration.DiskRoleRoot {
			rootOwner, exists := rootRefs[disk.DiskRef]
			if !exists || len(disk.Consumers) != 1 || disk.Consumers[0] != rootOwner {
				return fmt.Errorf("%w: source root disk ownership", ErrMigrationProviderResponse)
			}
		} else if _, exists := rootRefs[disk.DiskRef]; exists {
			return fmt.Errorf("%w: attached disk reused a root reference", ErrMigrationProviderResponse)
		}
		disks[disk.DiskRef] = disk
		previousDisk = disk.DiskRef
	}
	if len(rootRefs) != len(inventory.Instances) {
		return fmt.Errorf("%w: source root disk cardinality", ErrMigrationProviderResponse)
	}

	edges := make(map[string]struct{}, len(inventory.Attachments))
	consumerEdges := make(map[migration.OpaqueID]map[migration.OpaqueID]struct{}, len(disks))
	previousEdge := ""
	for _, attachment := range inventory.Attachments {
		instance, environmentExists := environments[attachment.EnvironmentRef]
		disk, diskExists := disks[attachment.DiskRef]
		key := string(attachment.EnvironmentRef) + "\x00" + string(attachment.DiskRef)
		if !environmentExists || !diskExists || key <= previousEdge ||
			attachment.Attachment != disk.Role || !validMigrationGuestPath(attachment.GuestPath) {
			return fmt.Errorf("%w: source attachment", ErrMigrationProviderResponse)
		}
		if disk.Role == migration.DiskRoleRoot &&
			(attachment.DiskRef != instance.RootDiskRef || attachment.GuestPath != "/" || attachment.ReadOnly) {
			return fmt.Errorf("%w: source root attachment", ErrMigrationProviderResponse)
		}
		if _, exists := edges[key]; exists {
			return fmt.Errorf("%w: duplicate source attachment", ErrMigrationProviderResponse)
		}
		edges[key] = struct{}{}
		if consumerEdges[attachment.DiskRef] == nil {
			consumerEdges[attachment.DiskRef] = make(map[migration.OpaqueID]struct{})
		}
		consumerEdges[attachment.DiskRef][attachment.EnvironmentRef] = struct{}{}
		previousEdge = key
	}
	for diskRef, disk := range disks {
		if len(consumerEdges[diskRef]) != len(disk.Consumers) {
			return fmt.Errorf("%w: source disk edge closure", ErrMigrationProviderResponse)
		}
		for _, consumer := range disk.Consumers {
			if _, exists := consumerEdges[diskRef][consumer]; !exists {
				return fmt.Errorf("%w: source disk edge closure", ErrMigrationProviderResponse)
			}
		}
	}

	if !sort.StringsAreSorted(inventory.ExcludedClasses) {
		return fmt.Errorf("%w: excluded source classes are unsorted", ErrMigrationProviderResponse)
	}
	for index, class := range inventory.ExcludedClasses {
		if !migrationProviderTokenPattern.MatchString(class) ||
			(index > 0 && inventory.ExcludedClasses[index-1] == class) {
			return fmt.Errorf("%w: excluded source class", ErrMigrationProviderResponse)
		}
	}
	previousBlocker := ""
	for _, blocker := range inventory.Blockers {
		key := blocker.Code + "\x00" + blocker.Summary
		if !migrationProviderCodePattern.MatchString(blocker.Code) ||
			!boundedProviderText(blocker.Summary, 512) ||
			(blocker.Remediation != "" && !boundedProviderText(blocker.Remediation, 1024)) ||
			(previousBlocker != "" && previousBlocker >= key) {
			return fmt.Errorf("%w: source blocker", ErrMigrationProviderResponse)
		}
		previousBlocker = key
	}

	digest, err := SourceInventoryDigest(inventory)
	if err != nil || digest != inventory.InventoryDigest {
		return fmt.Errorf("%w: source inventory digest", ErrMigrationProviderResponse)
	}
	return nil
}

// SourceInventoryDigest computes the canonical digest of observed source facts.
// Binding is deliberately excluded: it proves the authority of one read, while
// the digest must remain stable when apply re-observes identical facts under its
// own operation/effect binding.
func SourceInventoryDigest(inventory SourceInventory) (migration.Digest, error) {
	inventory.InventoryDigest = ""
	inventory.Binding = MigrationEffectBinding{}
	data, err := json.Marshal(inventory)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return migration.Digest("sha256:" + hex.EncodeToString(digest[:])), nil
}

func validMigrationLifecycle(state MigrationLifecycleState) bool {
	switch state {
	case MigrationLifecycleStopped,
		MigrationLifecycleRunning,
		MigrationLifecycleTransitioning,
		MigrationLifecycleUnknown:
		return true
	default:
		return false
	}
}

func validMigrationOpaqueID(value migration.OpaqueID) bool {
	_, err := migration.ParseOpaqueID(string(value))
	return err == nil
}

func validMigrationGuestPath(value string) bool {
	return value != "" && len(value) <= 4096 && filepath.IsAbs(value) &&
		filepath.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

type SourceSnapshotRequest struct {
	Binding         MigrationEffectBinding     `json:"binding"`
	InventoryDigest migration.Digest           `json:"inventoryDigest"`
	Selections      []MigrationSourceSelection `json:"selections"`
	DiskRefs        []migration.OpaqueID       `json:"diskRefs"`
}

func (request SourceSnapshotRequest) Validate() error {
	if err := (SourceInspectionRequest{
		Binding: request.Binding, Mode: migration.ExportModeFull,
		Selections: request.Selections,
	}).Validate(); err != nil || request.InventoryDigest.Validate() != nil ||
		len(request.DiskRefs) == 0 || len(request.DiskRefs) > 256 {
		return fmt.Errorf("%w: source snapshot envelope", ErrMigrationProviderRequest)
	}
	var previous migration.OpaqueID
	for _, diskRef := range request.DiskRefs {
		if !validMigrationOpaqueID(diskRef) || (previous != "" && previous >= diskRef) {
			return fmt.Errorf("%w: source snapshot disk", ErrMigrationProviderRequest)
		}
		previous = diskRef
	}
	return nil
}

type MigrationComponent struct {
	ComponentID    migration.OpaqueID `json:"componentId"`
	SnapshotHandle migration.OpaqueID `json:"snapshotHandle"`
	DiskRef        migration.OpaqueID `json:"diskRef"`
	Kind           string             `json:"kind"`
	LogicalBytes   uint64             `json:"logicalBytes"`
	ContentDigest  migration.Digest   `json:"contentDigest"`
}

// MigrationSourceIdentity binds guest identity evidence to the exact root
// component captured for one Manager-owned environment. The evidence is
// produced from a disposable, zero-network COW probe of the stopped snapshot;
// it is not inferred from a host or control-plane identifier.
type MigrationSourceIdentity struct {
	EnvironmentRef migration.OpaqueID              `json:"environmentRef"`
	RootComponent  migration.OpaqueID              `json:"rootComponent"`
	Evidence       migration.GuestIdentityEvidence `json:"evidence"`
}

type SourceSnapshot struct {
	Binding              MigrationEffectBinding    `json:"binding"`
	SnapshotHandle       migration.OpaqueID        `json:"snapshotHandle"`
	Components           []MigrationComponent      `json:"components"`
	Identities           []MigrationSourceIdentity `json:"identities"`
	Independent          bool                      `json:"independent"`
	SourceClaimsRequired bool                      `json:"sourceClaimsRequired"`
}

func (snapshot SourceSnapshot) Validate() error {
	if err := snapshot.Binding.Validate(); err != nil ||
		!validMigrationOpaqueID(snapshot.SnapshotHandle) ||
		len(snapshot.Components) == 0 || len(snapshot.Components) > 256 ||
		len(snapshot.Identities) == 0 ||
		len(snapshot.Identities) > int(migration.HardMaxEnvironments) ||
		(snapshot.Independent && snapshot.SourceClaimsRequired) {
		return fmt.Errorf("%w: source snapshot envelope", ErrMigrationProviderResponse)
	}
	components := make(map[migration.OpaqueID]struct{}, len(snapshot.Components))
	var previous migration.OpaqueID
	for _, component := range snapshot.Components {
		if !validMigrationOpaqueID(component.ComponentID) ||
			component.SnapshotHandle != snapshot.SnapshotHandle ||
			!validMigrationOpaqueID(component.DiskRef) ||
			!migrationProviderTokenPattern.MatchString(component.Kind) ||
			component.LogicalBytes == 0 || component.LogicalBytes > migration.HardMaxLogicalBytes ||
			(component.ContentDigest != "" && component.ContentDigest.Validate() != nil) ||
			(previous != "" && previous >= component.ComponentID) {
			return fmt.Errorf("%w: source snapshot component", ErrMigrationProviderResponse)
		}
		if _, exists := components[component.ComponentID]; exists {
			return fmt.Errorf("%w: duplicate source snapshot component", ErrMigrationProviderResponse)
		}
		components[component.ComponentID] = struct{}{}
		previous = component.ComponentID
	}
	var previousEnvironment migration.OpaqueID
	rootComponents := make(map[migration.OpaqueID]struct{}, len(snapshot.Identities))
	for _, identity := range snapshot.Identities {
		if !validMigrationOpaqueID(identity.EnvironmentRef) ||
			!validMigrationOpaqueID(identity.RootComponent) ||
			identity.Evidence.Validate() != nil ||
			(previousEnvironment != "" && previousEnvironment >= identity.EnvironmentRef) {
			return fmt.Errorf("%w: source snapshot identity", ErrMigrationProviderResponse)
		}
		if _, exists := components[identity.RootComponent]; !exists {
			return fmt.Errorf("%w: source snapshot identity component", ErrMigrationProviderResponse)
		}
		if _, exists := rootComponents[identity.RootComponent]; exists {
			return fmt.Errorf("%w: duplicate source root identity", ErrMigrationProviderResponse)
		}
		rootComponents[identity.RootComponent] = struct{}{}
		previousEnvironment = identity.EnvironmentRef
	}
	return nil
}

type ComponentReadRequest struct {
	Binding        MigrationEffectBinding `json:"binding"`
	SnapshotHandle migration.OpaqueID     `json:"snapshotHandle"`
	ComponentID    migration.OpaqueID     `json:"componentId"`
	ResumeOffset   uint64                 `json:"resumeOffset"`
	MaxChunkBytes  uint32                 `json:"maxChunkBytes"`
}

func (request ComponentReadRequest) Validate() error {
	if err := request.Binding.Validate(); err != nil ||
		!validMigrationOpaqueID(request.SnapshotHandle) ||
		!validMigrationOpaqueID(request.ComponentID) ||
		request.ResumeOffset > migration.HardMaxLogicalBytes ||
		request.MaxChunkBytes == 0 || request.MaxChunkBytes > migration.HardMaxChunkBytes {
		return fmt.Errorf("%w: component read envelope", ErrMigrationProviderRequest)
	}
	return nil
}

type MigrationExtent struct {
	Kind          migration.ExtentKind `json:"kind"`
	LogicalOffset uint64               `json:"logicalOffset"`
	Length        uint64               `json:"length"`
	Data          []byte               `json:"-"`
}

func (extent MigrationExtent) Validate(maxChunkBytes uint32) error {
	if maxChunkBytes == 0 || maxChunkBytes > migration.HardMaxChunkBytes ||
		extent.Length == 0 ||
		extent.LogicalOffset > migration.HardMaxLogicalBytes ||
		extent.Length > migration.HardMaxLogicalBytes-extent.LogicalOffset {
		return fmt.Errorf("%w: extent bounds", ErrMigrationProviderResponse)
	}
	switch extent.Kind {
	case migration.ExtentData:
		if extent.Length > uint64(maxChunkBytes) || uint64(len(extent.Data)) != extent.Length {
			return fmt.Errorf("%w: data extent length", ErrMigrationProviderResponse)
		}
	case migration.ExtentZero, migration.ExtentHole:
		if len(extent.Data) != 0 {
			return fmt.Errorf("%w: sparse extent carried bytes", ErrMigrationProviderResponse)
		}
	default:
		return fmt.Errorf("%w: extent kind", ErrMigrationProviderResponse)
	}
	return nil
}

type SnapshotReleaseRequest struct {
	Binding        MigrationEffectBinding `json:"binding"`
	SnapshotHandle migration.OpaqueID     `json:"snapshotHandle"`
}

func (request SnapshotReleaseRequest) Validate() error {
	if err := request.Binding.Validate(); err != nil ||
		!validMigrationOpaqueID(request.SnapshotHandle) {
		return fmt.Errorf("%w: snapshot release envelope", ErrMigrationProviderRequest)
	}
	return nil
}

type DestinationInspectionRequest struct {
	Binding              MigrationEffectBinding         `json:"binding"`
	ManifestDigest       migration.Digest               `json:"manifestDigest"`
	SourceProduct        migration.SourceProduct        `json:"sourceProduct"`
	EnvironmentRefs      []migration.OpaqueID           `json:"environmentRefs"`
	Disks                []migration.DiskObject         `json:"disks"`
	ProfileStateBytes    uint64                         `json:"profileStateBytes"`
	Edges                []migration.DiskEdge           `json:"edges"`
	RequiredCapabilities []migration.RequiredCapability `json:"requiredCapabilities"`
	RequiredBytes        uint64                         `json:"requiredBytes"`
	Capacity             migration.CapacityRequirement  `json:"capacity"`
}

func (request DestinationInspectionRequest) Validate() error {
	if err := request.Binding.Validate(); err != nil ||
		request.ManifestDigest.Validate() != nil ||
		!validMigrationSourceProduct(request.SourceProduct) ||
		len(request.EnvironmentRefs) == 0 ||
		len(request.EnvironmentRefs) > int(migration.HardMaxEnvironments) ||
		len(request.Disks) == 0 || len(request.Disks) > 256 ||
		len(request.Edges) == 0 || len(request.Edges) > 1024 ||
		len(request.RequiredCapabilities) > 256 || request.RequiredBytes == 0 ||
		request.Capacity.Validate() != nil ||
		request.RequiredBytes != request.Capacity.PeakAdditionalBytes {
		return fmt.Errorf("%w: destination inspection envelope", ErrMigrationProviderRequest)
	}
	environments := make(map[migration.OpaqueID]struct{}, len(request.EnvironmentRefs))
	var previousEnvironment migration.OpaqueID
	for _, environmentRef := range request.EnvironmentRefs {
		if !validMigrationOpaqueID(environmentRef) ||
			(previousEnvironment != "" && previousEnvironment >= environmentRef) {
			return fmt.Errorf("%w: destination inspection environment", ErrMigrationProviderRequest)
		}
		environments[environmentRef] = struct{}{}
		previousEnvironment = environmentRef
	}
	disks, aggregateLogical, err := validateDestinationDisks(request.Disks)
	if err != nil || request.ProfileStateBytes == 0 ||
		request.ProfileStateBytes > migration.HardMaxLogicalBytes-aggregateLogical ||
		aggregateLogical+request.ProfileStateBytes != request.Capacity.StagingBytes {
		return fmt.Errorf("%w: destination inspection disks", ErrMigrationProviderRequest)
	}
	if err := validateDestinationEdges(environments, disks, request.Edges); err != nil {
		return err
	}
	previousCapability := ""
	for _, capability := range request.RequiredCapabilities {
		key := capability.ID + "\x00" + capability.Provider
		if !migrationProviderTokenPattern.MatchString(capability.ID) ||
			!migrationProviderTokenPattern.MatchString(capability.Provider) ||
			(capability.MinimumVersion != "" &&
				!boundedProviderText(capability.MinimumVersion, 128)) ||
			(previousCapability != "" && previousCapability >= key) {
			return fmt.Errorf("%w: destination required capability", ErrMigrationProviderRequest)
		}
		previousCapability = key
	}
	return nil
}

type DestinationInventory struct {
	Binding            MigrationEffectBinding     `json:"binding"`
	Compatible         bool                       `json:"compatible"`
	CapabilityRevision migration.Digest           `json:"capabilityRevision"`
	AvailableBytes     uint64                     `json:"availableBytes"`
	SparseExtents      bool                       `json:"sparseExtents"`
	Conflicts          []migration.OpaqueID       `json:"conflicts"`
	Blockers           []MigrationProviderBlocker `json:"blockers"`
}

func (inventory DestinationInventory) Validate() error {
	if err := inventory.Binding.Validate(); err != nil ||
		inventory.CapabilityRevision.Validate() != nil ||
		len(inventory.Conflicts) > 1024 || len(inventory.Blockers) > 256 {
		return fmt.Errorf("%w: destination inventory envelope", ErrMigrationProviderResponse)
	}
	var previousConflict migration.OpaqueID
	for _, conflict := range inventory.Conflicts {
		if !validMigrationOpaqueID(conflict) ||
			(previousConflict != "" && previousConflict >= conflict) {
			return fmt.Errorf("%w: destination inventory conflict", ErrMigrationProviderResponse)
		}
		previousConflict = conflict
	}
	previousBlocker := ""
	for _, blocker := range inventory.Blockers {
		key := blocker.Code + "\x00" + blocker.Summary
		if !migrationProviderCodePattern.MatchString(blocker.Code) ||
			!boundedProviderText(blocker.Summary, 512) ||
			(blocker.Remediation != "" && !boundedProviderText(blocker.Remediation, 1024)) ||
			(previousBlocker != "" && previousBlocker >= key) {
			return fmt.Errorf("%w: destination inventory blocker", ErrMigrationProviderResponse)
		}
		previousBlocker = key
	}
	return nil
}

// MigrationComponentReader is a Manager-owned, bounded plaintext capability.
// The provider cannot use it to access bundle keys, records, or arbitrary files.
type MigrationComponentReader func(
	context.Context,
	migration.OpaqueID,
	uint64,
	uint32,
	func(MigrationExtent) error,
) error

type MigrationDestinationObject struct {
	EnvironmentRef    migration.OpaqueID         `json:"environmentRef"`
	BackendIdentity   migration.OpaqueID         `json:"backendIdentity"`
	Runtime           string                     `json:"runtime"`
	GuestArchitecture string                     `json:"guestArchitecture"`
	GuestUser         string                     `json:"guestUser"`
	ProfileComponent  migration.OpaqueID         `json:"profileComponent"`
	ImageProvenance   *migration.ImageProvenance `json:"imageProvenance,omitempty"`
}

// MigrationDestinationComponent is the authenticated bundle-to-disk binding
// presented to a provider. Keeping DiskID explicit prevents a provider from
// inferring storage authority from coincidentally equal opaque identifiers.
type MigrationDestinationComponent struct {
	ComponentID     migration.OpaqueID `json:"componentId"`
	DiskID          migration.OpaqueID `json:"diskId"`
	BackendIdentity migration.OpaqueID `json:"backendIdentity"`
	Kind            string             `json:"kind"`
	LogicalBytes    uint64             `json:"logicalBytes"`
	ContentDigest   migration.Digest   `json:"contentDigest"`
}

type DestinationStageRequest struct {
	Binding       MigrationEffectBinding          `json:"binding"`
	StagingHandle migration.OpaqueID              `json:"stagingHandle"`
	Objects       []MigrationDestinationObject    `json:"objects"`
	Disks         []migration.DiskObject          `json:"disks"`
	Edges         []migration.DiskEdge            `json:"edges"`
	Components    []MigrationDestinationComponent `json:"components"`
	ReadComponent MigrationComponentReader        `json:"-"`
}

func (request DestinationStageRequest) Validate() error {
	if err := request.Binding.Validate(); err != nil ||
		!validMigrationOpaqueID(request.StagingHandle) || request.ReadComponent == nil ||
		len(request.Objects) == 0 || len(request.Objects) > int(migration.HardMaxEnvironments) ||
		len(request.Disks) == 0 || len(request.Disks) > 256 ||
		len(request.Edges) == 0 || len(request.Edges) > 1024 ||
		len(request.Components) != len(request.Disks) {
		return fmt.Errorf("%w: destination stage envelope", ErrMigrationProviderRequest)
	}

	objects := make(map[migration.OpaqueID]MigrationDestinationObject, len(request.Objects))
	backendIdentities := make(map[migration.OpaqueID]struct{}, len(request.Objects))
	var previousEnvironment migration.OpaqueID
	for _, object := range request.Objects {
		if !validMigrationOpaqueID(object.EnvironmentRef) ||
			!validMigrationOpaqueID(object.BackendIdentity) ||
			!migrationProviderTokenPattern.MatchString(object.Runtime) ||
			!validArchitecture(object.GuestArchitecture) ||
			!migrationGuestUserPattern.MatchString(object.GuestUser) || object.GuestUser == "root" ||
			!validMigrationOpaqueID(object.ProfileComponent) ||
			(previousEnvironment != "" && previousEnvironment >= object.EnvironmentRef) {
			return fmt.Errorf("%w: destination object", ErrMigrationProviderRequest)
		}
		if object.ImageProvenance != nil &&
			(!boundedProviderText(object.ImageProvenance.Reference, 4096) ||
				object.ImageProvenance.Digest.Validate() != nil) {
			return fmt.Errorf("%w: destination object image provenance", ErrMigrationProviderRequest)
		}
		if _, exists := backendIdentities[object.BackendIdentity]; exists {
			return fmt.Errorf("%w: duplicate destination backend identity", ErrMigrationProviderRequest)
		}
		backendIdentities[object.BackendIdentity] = struct{}{}
		objects[object.EnvironmentRef] = object
		previousEnvironment = object.EnvironmentRef
	}

	disks, _, err := validateDestinationDisks(request.Disks)
	if err != nil {
		return err
	}

	componentDisks := make(map[migration.OpaqueID]MigrationDestinationComponent, len(request.Components))
	componentBackendIdentities := make(map[migration.OpaqueID]struct{}, len(request.Components))
	var previousComponent migration.OpaqueID
	for _, component := range request.Components {
		disk, exists := disks[component.DiskID]
		if !exists || !validMigrationOpaqueID(component.ComponentID) ||
			!validMigrationOpaqueID(component.BackendIdentity) || component.Kind != "disk" ||
			component.LogicalBytes != disk.LogicalBytes ||
			component.ContentDigest != disk.ContentDigest ||
			(previousComponent != "" && previousComponent >= component.ComponentID) {
			return fmt.Errorf("%w: destination component", ErrMigrationProviderRequest)
		}
		if _, exists := componentDisks[component.DiskID]; exists {
			return fmt.Errorf("%w: duplicate destination disk component", ErrMigrationProviderRequest)
		}
		if _, exists := componentBackendIdentities[component.BackendIdentity]; exists {
			return fmt.Errorf("%w: duplicate destination disk identity", ErrMigrationProviderRequest)
		}
		if disk.Role == migration.DiskRoleAttached {
			if _, exists := backendIdentities[component.BackendIdentity]; exists {
				return fmt.Errorf("%w: attached disk reused an environment identity", ErrMigrationProviderRequest)
			}
		}
		componentDisks[component.DiskID] = component
		componentBackendIdentities[component.BackendIdentity] = struct{}{}
		previousComponent = component.ComponentID
	}
	if len(componentDisks) != len(disks) {
		return fmt.Errorf("%w: destination component graph", ErrMigrationProviderRequest)
	}

	environments := make(map[migration.OpaqueID]struct{}, len(objects))
	for environmentRef := range objects {
		environments[environmentRef] = struct{}{}
	}
	if err := validateDestinationEdges(environments, disks, request.Edges); err != nil {
		return err
	}
	for _, edge := range request.Edges {
		if edge.Attachment == migration.DiskRoleRoot &&
			componentDisks[edge.DiskID].BackendIdentity != objects[edge.EnvironmentRef].BackendIdentity {
			return fmt.Errorf("%w: root disk identity is not its environment identity", ErrMigrationProviderRequest)
		}
	}
	return nil
}

func validateDestinationDisks(
	values []migration.DiskObject,
) (map[migration.OpaqueID]migration.DiskObject, uint64, error) {
	disks := make(map[migration.OpaqueID]migration.DiskObject, len(values))
	var previousDisk migration.OpaqueID
	var aggregateLogical uint64
	for _, disk := range values {
		if !validMigrationOpaqueID(disk.DiskID) ||
			(disk.Role != migration.DiskRoleRoot && disk.Role != migration.DiskRoleAttached) ||
			!migrationProviderTokenPattern.MatchString(disk.Format) ||
			disk.LogicalBytes == 0 || disk.LogicalBytes > migration.HardMaxLogicalBytes ||
			disk.AllocatedBytesHint > disk.LogicalBytes || disk.ContentDigest.Validate() != nil ||
			!boundedProviderText(disk.Provider.Name, 128) ||
			!migrationProviderTokenPattern.MatchString(disk.Provider.Kind) ||
			(previousDisk != "" && previousDisk >= disk.DiskID) {
			return nil, 0, fmt.Errorf("%w: destination disk", ErrMigrationProviderRequest)
		}
		if err := validateProviderTokens(disk.Provider.Features, 32); err != nil {
			return nil, 0, fmt.Errorf("%w: destination disk features", ErrMigrationProviderRequest)
		}
		if disk.LogicalBytes > migration.HardMaxLogicalBytes-aggregateLogical {
			return nil, 0, fmt.Errorf("%w: aggregate destination disk size", ErrMigrationProviderRequest)
		}
		aggregateLogical += disk.LogicalBytes
		disks[disk.DiskID] = disk
		previousDisk = disk.DiskID
	}
	return disks, aggregateLogical, nil
}

func validateDestinationEdges(
	environments map[migration.OpaqueID]struct{},
	disks map[migration.OpaqueID]migration.DiskObject,
	edges []migration.DiskEdge,
) error {
	rootEdges := make(map[migration.OpaqueID]int, len(environments))
	diskEdges := make(map[migration.OpaqueID]int, len(disks))
	previousEdge := ""
	for _, edge := range edges {
		_, environmentExists := environments[edge.EnvironmentRef]
		disk, diskExists := disks[edge.DiskID]
		key := string(edge.EnvironmentRef) + "\x00" + string(edge.DiskID)
		if !environmentExists || !diskExists || edge.Attachment != disk.Role ||
			!validMigrationGuestPath(edge.GuestPath) || key <= previousEdge {
			return fmt.Errorf("%w: destination disk edge", ErrMigrationProviderRequest)
		}
		if disk.Role == migration.DiskRoleRoot {
			if edge.GuestPath != "/" || edge.ReadOnly {
				return fmt.Errorf("%w: destination root edge", ErrMigrationProviderRequest)
			}
			rootEdges[edge.EnvironmentRef]++
		}
		diskEdges[disk.DiskID]++
		previousEdge = key
	}
	for environmentRef := range environments {
		if rootEdges[environmentRef] != 1 {
			return fmt.Errorf("%w: destination root cardinality", ErrMigrationProviderRequest)
		}
	}
	for diskID := range disks {
		if diskEdges[diskID] == 0 {
			return fmt.Errorf("%w: destination disk is unreachable", ErrMigrationProviderRequest)
		}
	}
	return nil
}

func validMigrationSourceProduct(product migration.SourceProduct) bool {
	return boundedProviderText(product.Version, 128) &&
		migrationProviderTokenPattern.MatchString(product.HostOS) &&
		migrationProviderTokenPattern.MatchString(product.HostArch) &&
		migrationProviderTokenPattern.MatchString(product.Backend) &&
		boundedProviderText(product.BackendVersion, 128) &&
		migrationProviderTokenPattern.MatchString(product.GuestArch)
}

type MigrationStageCheckpoint struct {
	ComponentID   migration.OpaqueID `json:"componentId"`
	NextOffset    uint64             `json:"nextOffset"`
	ContentDigest migration.Digest   `json:"contentDigest"`
}

type DestinationStage struct {
	Binding       MigrationEffectBinding     `json:"binding"`
	StageHandle   migration.OpaqueID         `json:"stageHandle"`
	ObjectHandles []migration.OpaqueID       `json:"objectHandles"`
	Checkpoints   []MigrationStageCheckpoint `json:"checkpoints"`
	Stopped       bool                       `json:"stopped"`
	Runnable      bool                       `json:"runnable"`
}

func (stage DestinationStage) Validate() error {
	if err := stage.Binding.Validate(); err != nil || !validMigrationOpaqueID(stage.StageHandle) ||
		len(stage.ObjectHandles) == 0 || len(stage.ObjectHandles) > 512 ||
		len(stage.Checkpoints) == 0 || len(stage.Checkpoints) > 256 ||
		!stage.Stopped || stage.Runnable {
		return fmt.Errorf("%w: destination stage", ErrMigrationProviderResponse)
	}
	var previousHandle migration.OpaqueID
	for _, handle := range stage.ObjectHandles {
		if !validMigrationOpaqueID(handle) || (previousHandle != "" && previousHandle >= handle) {
			return fmt.Errorf("%w: destination stage object", ErrMigrationProviderResponse)
		}
		previousHandle = handle
	}
	var previousComponent migration.OpaqueID
	for _, checkpoint := range stage.Checkpoints {
		if !validMigrationOpaqueID(checkpoint.ComponentID) || checkpoint.NextOffset == 0 ||
			checkpoint.NextOffset > migration.HardMaxLogicalBytes ||
			checkpoint.ContentDigest.Validate() != nil ||
			(previousComponent != "" && previousComponent >= checkpoint.ComponentID) {
			return fmt.Errorf("%w: destination stage checkpoint", ErrMigrationProviderResponse)
		}
		previousComponent = checkpoint.ComponentID
	}
	return nil
}

type DestinationAdoptionRequest struct {
	Binding        MigrationEffectBinding          `json:"binding"`
	StageHandle    migration.OpaqueID              `json:"stageHandle"`
	EnvironmentRef migration.OpaqueID              `json:"environmentRef"`
	Policy         migration.GuestIdentityPolicy   `json:"policy"`
	SourceIdentity migration.GuestIdentityEvidence `json:"sourceIdentity"`
	Helper         migration.HelperBinding         `json:"helper"`
}

type DestinationAdoption struct {
	Binding                   MigrationEffectBinding    `json:"binding"`
	StageHandle               migration.OpaqueID        `json:"stageHandle"`
	Request                   migration.AdoptionRequest `json:"request"`
	Receipt                   migration.AdoptionReceipt `json:"receipt"`
	Stopped                   bool                      `json:"stopped"`
	TemporaryAuthorityRemoved bool                      `json:"temporaryAuthorityRemoved"`
}

func (request DestinationAdoptionRequest) Validate() error {
	if err := request.Binding.Validate(); err != nil ||
		!validMigrationOpaqueID(request.StageHandle) ||
		!validMigrationOpaqueID(request.EnvironmentRef) ||
		(request.Policy != migration.GuestIdentitySafeClone &&
			request.Policy != migration.GuestIdentityExactRestore) ||
		request.SourceIdentity.Validate() != nil || request.Helper.Validate() != nil {
		return fmt.Errorf("%w: destination adoption request", ErrMigrationProviderRequest)
	}
	return nil
}

func (adoption DestinationAdoption) Validate() error {
	if err := adoption.Binding.Validate(); err != nil ||
		!validMigrationOpaqueID(adoption.StageHandle) || adoption.Request.Validate() != nil ||
		adoption.Request.OperationID != adoption.Binding.OperationID ||
		adoption.Receipt.MatchesRequest(adoption.Request) != nil ||
		adoption.Receipt.Status != migration.AdoptionReceiptStatusCompleted ||
		!adoption.Stopped || !adoption.TemporaryAuthorityRemoved {
		return fmt.Errorf("%w: destination adoption response", ErrMigrationProviderResponse)
	}
	return nil
}

func (adoption DestinationAdoption) MatchesRequest(
	request DestinationAdoptionRequest,
) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := adoption.Validate(); err != nil || adoption.Binding != request.Binding ||
		adoption.StageHandle != request.StageHandle ||
		adoption.Request.EnvironmentRef != request.EnvironmentRef ||
		adoption.Request.Policy != request.Policy ||
		!adoption.Request.SourceIdentity.Equal(request.SourceIdentity) ||
		adoption.Request.Helper != request.Helper {
		return fmt.Errorf("%w: destination adoption binding", ErrMigrationProviderResponse)
	}
	return nil
}

type DestinationVerifyRequest struct {
	Binding          MigrationEffectBinding        `json:"binding"`
	StageHandle      migration.OpaqueID            `json:"stageHandle"`
	ExpectedDisks    []migration.DiskObject        `json:"expectedDisks"`
	IdentityPolicies []migration.IdentitySelection `json:"identityPolicies"`
	AdoptionRequests []migration.AdoptionRequest   `json:"adoptionRequests"`
	AdoptionReceipts []migration.AdoptionReceipt   `json:"adoptionReceipts"`
}

type DestinationProof struct {
	Binding                   MigrationEffectBinding `json:"binding"`
	StageHandle               migration.OpaqueID     `json:"stageHandle"`
	ProofDigest               migration.Digest       `json:"proofDigest"`
	Stopped                   bool                   `json:"stopped"`
	DigestsMatch              bool                   `json:"digestsMatch"`
	IdentityPolicySatisfied   bool                   `json:"identityPolicySatisfied"`
	TemporaryAuthorityRemoved bool                   `json:"temporaryAuthorityRemoved"`
	ImportedAuthorityAbsent   bool                   `json:"importedAuthorityAbsent"`
}

type DestinationRollbackRequest struct {
	Binding       MigrationEffectBinding `json:"binding"`
	StageHandle   migration.OpaqueID     `json:"stageHandle"`
	ObjectHandles []migration.OpaqueID   `json:"objectHandles"`
}

// DestinationActivationRequest binds the final provider promotion to both the
// activation effect and the already verified private stage. Promotion keeps
// every object stopped; Manager publication remains the sole point that makes
// the imported environment selectable through Hideout.
type DestinationActivationRequest struct {
	Binding       MigrationEffectBinding `json:"binding"`
	Proof         DestinationProof       `json:"proof"`
	ObjectHandles []migration.OpaqueID   `json:"objectHandles"`
}

type DestinationActivation struct {
	Binding       MigrationEffectBinding `json:"binding"`
	StageHandle   migration.OpaqueID     `json:"stageHandle"`
	ProofDigest   migration.Digest       `json:"proofDigest"`
	ObjectHandles []migration.OpaqueID   `json:"objectHandles"`
	Stopped       bool                   `json:"stopped"`
	Promoted      bool                   `json:"promoted"`
}

func (request DestinationActivationRequest) Validate() error {
	if err := request.Binding.Validate(); err != nil || request.Proof.Validate() != nil ||
		request.Binding.OperationID != request.Proof.Binding.OperationID ||
		request.Binding.CapabilityRevision != request.Proof.Binding.CapabilityRevision ||
		request.Binding.EffectID == request.Proof.Binding.EffectID ||
		len(request.ObjectHandles) == 0 || len(request.ObjectHandles) > 512 {
		return fmt.Errorf("%w: destination activation request", ErrMigrationProviderRequest)
	}
	var previous migration.OpaqueID
	for _, handle := range request.ObjectHandles {
		if !validMigrationOpaqueID(handle) || (previous != "" && previous >= handle) {
			return fmt.Errorf("%w: destination activation object", ErrMigrationProviderRequest)
		}
		previous = handle
	}
	return nil
}

func (activation DestinationActivation) Validate() error {
	if err := activation.Binding.Validate(); err != nil ||
		!validMigrationOpaqueID(activation.StageHandle) ||
		activation.ProofDigest.Validate() != nil ||
		len(activation.ObjectHandles) == 0 || len(activation.ObjectHandles) > 512 ||
		!activation.Stopped || !activation.Promoted {
		return fmt.Errorf("%w: destination activation response", ErrMigrationProviderResponse)
	}
	var previous migration.OpaqueID
	for _, handle := range activation.ObjectHandles {
		if !validMigrationOpaqueID(handle) || (previous != "" && previous >= handle) {
			return fmt.Errorf("%w: destination activation object", ErrMigrationProviderResponse)
		}
		previous = handle
	}
	return nil
}

func (activation DestinationActivation) MatchesRequest(
	request DestinationActivationRequest,
) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := activation.Validate(); err != nil ||
		activation.Binding != request.Binding ||
		activation.StageHandle != request.Proof.StageHandle ||
		activation.ProofDigest != request.Proof.ProofDigest ||
		!slices.Equal(activation.ObjectHandles, request.ObjectHandles) {
		return fmt.Errorf("%w: destination activation binding", ErrMigrationProviderResponse)
	}
	return nil
}

func (request DestinationVerifyRequest) Validate() error {
	if err := request.Binding.Validate(); err != nil ||
		!validMigrationOpaqueID(request.StageHandle) ||
		len(request.ExpectedDisks) == 0 || len(request.ExpectedDisks) > 256 ||
		len(request.IdentityPolicies) == 0 ||
		len(request.IdentityPolicies) > int(migration.HardMaxEnvironments) ||
		len(request.AdoptionRequests) != len(request.IdentityPolicies) ||
		len(request.AdoptionReceipts) != len(request.IdentityPolicies) {
		return fmt.Errorf("%w: destination verify request", ErrMigrationProviderRequest)
	}
	if _, _, err := validateDestinationDisks(request.ExpectedDisks); err != nil {
		return err
	}
	policies := make(map[migration.OpaqueID]migration.GuestIdentityPolicy, len(request.IdentityPolicies))
	var previousPolicy migration.OpaqueID
	for _, selection := range request.IdentityPolicies {
		if !validMigrationOpaqueID(selection.SourceRef) ||
			(selection.Policy != migration.GuestIdentitySafeClone &&
				selection.Policy != migration.GuestIdentityExactRestore) ||
			(previousPolicy != "" && previousPolicy >= selection.SourceRef) {
			return fmt.Errorf("%w: destination verify identity policy", ErrMigrationProviderRequest)
		}
		policies[selection.SourceRef] = selection.Policy
		previousPolicy = selection.SourceRef
	}
	for index, adoptionRequest := range request.AdoptionRequests {
		selection := request.IdentityPolicies[index]
		receipt := request.AdoptionReceipts[index]
		policy, exists := policies[adoptionRequest.EnvironmentRef]
		if !exists || adoptionRequest.Validate() != nil ||
			adoptionRequest.OperationID != request.Binding.OperationID ||
			adoptionRequest.EnvironmentRef != selection.SourceRef ||
			adoptionRequest.Policy != selection.Policy || adoptionRequest.Policy != policy ||
			receipt.EnvironmentRef != selection.SourceRef ||
			receipt.Status != migration.AdoptionReceiptStatusCompleted ||
			receipt.MatchesRequest(adoptionRequest) != nil {
			return fmt.Errorf("%w: destination verify adoption receipt", ErrMigrationProviderRequest)
		}
	}
	return nil
}

func (proof DestinationProof) Validate() error {
	if err := proof.Binding.Validate(); err != nil ||
		!validMigrationOpaqueID(proof.StageHandle) || proof.ProofDigest.Validate() != nil ||
		!proof.Stopped || !proof.DigestsMatch || !proof.IdentityPolicySatisfied ||
		!proof.TemporaryAuthorityRemoved || !proof.ImportedAuthorityAbsent {
		return fmt.Errorf("%w: destination verification proof", ErrMigrationProviderResponse)
	}
	return nil
}

func (proof DestinationProof) MatchesRequest(
	request DestinationVerifyRequest,
) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := proof.Validate(); err != nil || proof.Binding != request.Binding ||
		proof.StageHandle != request.StageHandle {
		return fmt.Errorf("%w: destination verification binding", ErrMigrationProviderResponse)
	}
	return nil
}

func (request DestinationRollbackRequest) Validate() error {
	if err := request.Binding.Validate(); err != nil ||
		!validMigrationOpaqueID(request.StageHandle) || len(request.ObjectHandles) == 0 ||
		len(request.ObjectHandles) > 512 {
		return fmt.Errorf("%w: destination rollback request", ErrMigrationProviderRequest)
	}
	var previous migration.OpaqueID
	for _, handle := range request.ObjectHandles {
		if !validMigrationOpaqueID(handle) || (previous != "" && previous >= handle) {
			return fmt.Errorf("%w: destination rollback object", ErrMigrationProviderRequest)
		}
		previous = handle
	}
	return nil
}

// MigrationProviderError keeps raw provider diagnostics behind Unwrap while its
// ordinary rendering exposes only registered-looking codes and opaque IDs.
type MigrationProviderError struct {
	Code             string
	Binding          MigrationEffectBinding
	OpaqueRef        string
	Retryable        bool
	RecoveryRequired bool
	Cause            error
}

func (providerErr *MigrationProviderError) Error() string {
	if providerErr == nil {
		return "migration.provider.failed"
	}
	code := providerErr.Code
	if !migrationProviderCodePattern.MatchString(code) {
		code = "migration.provider.failed"
	}
	message := code
	if _, err := migration.ParseOpaqueID(string(providerErr.Binding.OperationID)); err == nil {
		message += " operation=" + string(providerErr.Binding.OperationID)
	}
	if _, err := migration.ParseOpaqueID(string(providerErr.Binding.EffectID)); err == nil {
		message += " effect=" + string(providerErr.Binding.EffectID)
	}
	if _, err := migration.ParseOpaqueID(providerErr.OpaqueRef); err == nil {
		message += " object=" + providerErr.OpaqueRef
	}
	return message
}

func (providerErr *MigrationProviderError) Unwrap() error {
	if providerErr == nil {
		return nil
	}
	return providerErr.Cause
}

func validateProviderTokens(values []string, maximum int) error {
	if len(values) > maximum {
		return fmt.Errorf("%w: provider token count", ErrMigrationProviderCapability)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !migrationProviderTokenPattern.MatchString(value) {
			return fmt.Errorf("%w: provider token", ErrMigrationProviderCapability)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%w: duplicate provider token", ErrMigrationProviderCapability)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validArchitecture(value string) bool {
	parts := strings.Split(value, "/")
	return len(parts) == 2 && migrationProviderTokenPattern.MatchString(parts[0]) &&
		migrationProviderTokenPattern.MatchString(parts[1])
}

func boundedProviderText(value string, maximum int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && trimmed == value && len(value) <= maximum &&
		!strings.ContainsAny(value, "\x00\r\n")
}
