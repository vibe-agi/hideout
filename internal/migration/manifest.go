package migration

import (
	"fmt"
	"math"
	"path"
	"regexp"
	"strings"
)

const (
	manifestSchema = "hideout.migration-manifest/v1"

	maxManifestDisks                = 256
	maxManifestEdges                = 1024
	maxManifestSecrets              = 256
	maxManifestAuthorityProposals   = 1024
	maxManifestComponents           = 4096
	maxManifestExcludedClasses      = 32
	maxManifestRequiredCapabilities = 128
)

var (
	manifestTokenPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	manifestGuestUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

var manifestAuthorityClasses = map[string]struct{}{
	"workspace": {}, "hostfs": {}, "mount": {}, "network": {},
	"proxy": {}, "endpoint": {}, "host-app": {}, "command-adapter": {},
	"script": {}, "pack": {}, "environment": {}, "profile": {},
}

var manifestComponentKinds = map[string]struct{}{
	"profile": {}, "environment": {}, "disk": {}, "secret-value": {},
	"provider-metadata": {}, "profile-state": {},
}

var manifestExcludedClasses = map[string]struct{}{
	"host-workspace-content": {}, "hostfs-content": {},
	"activity-history": {}, "audit-history": {}, "command-history": {},
	"process-state": {}, "memory-state": {}, "runtime-state": {},
	"logs": {}, "caches": {}, "host-runtime-identity": {},
	"unselected-secret-values": {},
}

// Validate checks the authenticated manifest's semantic closure. It does not
// grant destination authority and does not replace record-range validation
// against the encrypted stream performed by InspectSealedBundle.
func (manifest Manifest) Validate(limits Limits) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	if manifest.Schema != manifestSchema ||
		manifest.FormatVersion != BundleFormatVersion ||
		!validManifestBundleID(manifest.BundleID) ||
		!validManifestSourceProduct(manifest.SourceProduct) {
		return corruptManifest("manifest envelope is invalid")
	}
	if len(manifest.Environments) == 0 ||
		len(manifest.Environments) > int(limits.MaxEnvironments) ||
		len(manifest.DiskObjects) > maxManifestDisks ||
		len(manifest.DiskEdges) > maxManifestEdges ||
		len(manifest.SecretEntries) > maxManifestSecrets ||
		len(manifest.AuthorityProposals) > maxManifestAuthorityProposals ||
		len(manifest.ComponentIndex) == 0 ||
		len(manifest.ComponentIndex) > maxManifestComponents ||
		len(manifest.ExcludedClasses) == 0 ||
		len(manifest.ExcludedClasses) > maxManifestExcludedClasses ||
		len(manifest.RequiredCapabilities) > maxManifestRequiredCapabilities {
		return corruptManifest("manifest collection bounds are invalid")
	}

	components, indexedRecords, err := validateManifestComponents(
		manifest.ComponentIndex, limits,
	)
	if err != nil {
		return err
	}
	disks, aggregateDiskBytes, err := validateManifestDisks(
		manifest.DiskObjects, limits,
	)
	if err != nil {
		return err
	}
	if indexedRecords == 0 || aggregateDiskBytes > limits.MaxLogicalBytes {
		return corruptManifest("manifest aggregate bounds are invalid")
	}
	if err := bindManifestDiskComponents(disks, components); err != nil {
		return err
	}
	authorities, err := validateManifestAuthorities(manifest.AuthorityProposals)
	if err != nil {
		return err
	}
	environments, err := validateManifestEnvironments(
		manifest.Environments, manifest.SourceProduct, components, authorities,
	)
	if err != nil {
		return err
	}
	if err := validateManifestDiskGraph(
		manifest.DiskEdges, environments, disks,
	); err != nil {
		return err
	}
	if err := validateManifestSecrets(
		manifest.SecretEntries, components, environments,
	); err != nil {
		return err
	}
	if err := validateManifestExcluded(manifest.ExcludedClasses); err != nil {
		return err
	}
	if err := validateManifestCapabilities(manifest.RequiredCapabilities); err != nil {
		return err
	}
	return nil
}

type manifestComponentFacts struct {
	entry ComponentIndexEntry
	used  bool
}

func validateManifestComponents(
	entries []ComponentIndexEntry,
	limits Limits,
) (map[OpaqueID]*manifestComponentFacts, uint64, error) {
	components := make(map[OpaqueID]*manifestComponentFacts, len(entries))
	var nextRecord uint64
	for _, entry := range entries {
		if !validManifestOpaqueID(entry.ComponentID) ||
			!validManifestComponentKind(entry.Kind) ||
			entry.ContentDigest.Validate() != nil ||
			entry.LogicalBytes > limits.MaxLogicalBytes ||
			entry.RecordCount == 0 || entry.RecordCount > limits.MaxPayloadRecords ||
			entry.FirstRecord != nextRecord ||
			entry.FirstRecord >= limits.MaxPayloadRecords ||
			entry.RecordCount-1 > math.MaxUint64-entry.FirstRecord ||
			entry.LastRecord != entry.FirstRecord+entry.RecordCount-1 ||
			entry.LastRecord >= limits.MaxPayloadRecords {
			return nil, 0, corruptManifest("component index is invalid")
		}
		if _, exists := components[entry.ComponentID]; exists {
			return nil, 0, corruptManifest("component identity is duplicated")
		}
		if entry.Kind == "disk" {
			if !validManifestOpaqueID(entry.DiskID) || entry.LogicalBytes == 0 {
				return nil, 0, corruptManifest("disk component binding is invalid")
			}
		} else if entry.DiskID != "" {
			return nil, 0, corruptManifest("non-disk component carries a disk binding")
		}
		copyEntry := entry
		components[entry.ComponentID] = &manifestComponentFacts{entry: copyEntry}
		nextRecord = entry.LastRecord + 1
	}
	return components, nextRecord, nil
}

func validateManifestDisks(
	diskEntries []DiskObject,
	limits Limits,
) (map[OpaqueID]DiskObject, uint64, error) {
	disks := make(map[OpaqueID]DiskObject, len(diskEntries))
	var previous OpaqueID
	var aggregate uint64
	for _, disk := range diskEntries {
		if !validManifestOpaqueID(disk.DiskID) ||
			(previous != "" && previous >= disk.DiskID) ||
			(disk.Role != DiskRoleRoot && disk.Role != DiskRoleAttached) ||
			(disk.Format != "raw" && disk.Format != "qcow2") ||
			disk.LogicalBytes == 0 || disk.LogicalBytes > limits.MaxLogicalBytes ||
			disk.AllocatedBytesHint > HardMaxLogicalBytes ||
			disk.ContentDigest.Validate() != nil ||
			!validManifestProviderToken(disk.Provider.Name, 32) ||
			!validManifestProviderToken(disk.Provider.Kind, 64) ||
			len(disk.Provider.Features) > 32 ||
			!validSortedManifestTokens(disk.Provider.Features, 64) {
			return nil, 0, corruptManifest("disk object is invalid")
		}
		if disk.LogicalBytes > limits.MaxLogicalBytes-aggregate {
			return nil, 0, corruptManifest("aggregate disk size exceeds the bundle limit")
		}
		aggregate += disk.LogicalBytes
		disks[disk.DiskID] = disk
		previous = disk.DiskID
	}
	return disks, aggregate, nil
}

// ValidateDiskObjects applies the same strict, sorted, bounded validation used
// by an authenticated manifest. Manager uses it when retaining the selected
// disk expectations needed for post-adoption verification after the in-memory
// inspection cache has expired.
func ValidateDiskObjects(diskEntries []DiskObject, limits Limits) error {
	if err := limits.Validate(); err != nil || len(diskEntries) == 0 {
		return corruptManifest("disk object limits or selection are invalid")
	}
	_, _, err := validateManifestDisks(diskEntries, limits)
	return err
}

func bindManifestDiskComponents(
	disks map[OpaqueID]DiskObject,
	components map[OpaqueID]*manifestComponentFacts,
) error {
	bound := make(map[OpaqueID]struct{}, len(disks))
	for _, component := range components {
		if component.entry.Kind != "disk" {
			continue
		}
		disk, exists := disks[component.entry.DiskID]
		if !exists || component.entry.LogicalBytes != disk.LogicalBytes ||
			component.entry.ContentDigest != disk.ContentDigest {
			return corruptManifest("disk component does not match its disk object")
		}
		if _, exists := bound[disk.DiskID]; exists {
			return corruptManifest("disk has more than one component")
		}
		bound[disk.DiskID] = struct{}{}
		component.used = true
	}
	if len(bound) != len(disks) {
		return corruptManifest("disk object lacks an authenticated component")
	}
	return nil
}

type manifestEnvironmentFacts struct {
	entry    EnvironmentSnapshot
	diskRefs map[OpaqueID]struct{}
}

func validateManifestEnvironments(
	entries []EnvironmentSnapshot,
	source SourceProduct,
	components map[OpaqueID]*manifestComponentFacts,
	authorities map[OpaqueID]AuthorityProposal,
) (map[OpaqueID]manifestEnvironmentFacts, error) {
	environments := make(map[OpaqueID]manifestEnvironmentFacts, len(entries))
	workspaceIDs := make(map[OpaqueID]struct{})
	var previous OpaqueID
	for _, environment := range entries {
		if !validManifestOpaqueID(environment.SourceEnvironmentRef) ||
			(previous != "" && previous >= environment.SourceEnvironmentRef) ||
			!validManifestText(environment.DisplayNameHint, 1, 128) ||
			(environment.Runtime != "linux" && environment.Runtime != "native") ||
			!manifestGuestUserPattern.MatchString(environment.GuestUser) ||
			environment.GuestUser == "root" ||
			!validManifestProviderToken(environment.Backend, 32) ||
			(environment.Mode == ExportModeFull && environment.Backend != source.Backend) ||
			(environment.Mode != ExportModeConfig && environment.Mode != ExportModeFull) ||
			(environment.Mode == ExportModeFull && environment.Runtime != "linux") ||
			!validManifestOpaqueID(environment.ProfileComponentID) ||
			len(environment.WorkspaceProposals) > 128 ||
			len(environment.AuthorityProposalRefs) > maxManifestAuthorityProposals ||
			len(environment.DiskRefs) > maxManifestDisks {
			return nil, corruptManifest("environment snapshot is invalid")
		}
		profile, exists := components[environment.ProfileComponentID]
		if !exists || profile.entry.Kind != "profile" ||
			profile.entry.LogicalBytes == 0 ||
			profile.entry.LogicalBytes > MaxPortableProfileBytes {
			return nil, corruptManifest("environment profile component is invalid")
		}
		profile.used = true
		if environment.Mode == ExportModeFull {
			if !validManifestOpaqueID(environment.ProfileStateComponentID) {
				return nil, corruptManifest("environment profile state component is absent")
			}
			profileState, exists := components[environment.ProfileStateComponentID]
			if !exists || profileState.entry.Kind != "profile-state" ||
				profileState.entry.LogicalBytes == 0 {
				return nil, corruptManifest("environment profile state component is invalid")
			}
			profileState.used = true
		} else if environment.ProfileStateComponentID != "" {
			return nil, corruptManifest("config environment carries profile state")
		}
		if environment.ImageProvenance != nil &&
			(!validManifestText(environment.ImageProvenance.Reference, 1, 2048) ||
				environment.ImageProvenance.Digest.Validate() != nil) {
			return nil, corruptManifest("image provenance is invalid")
		}
		identityValid := environment.GuestIdentityEvidence.MachineIDDigest.Validate() == nil &&
			len(environment.GuestIdentityEvidence.SSHHostKeyDigests) > 0 &&
			len(environment.GuestIdentityEvidence.SSHHostKeyDigests) <= 32 &&
			validSortedManifestDigests(environment.GuestIdentityEvidence.SSHHostKeyDigests)
		if environment.Mode == ExportModeConfig {
			identityValid = IsConfigIdentityUnavailableEvidence(
				environment.GuestIdentityEvidence,
			)
		}
		if !identityValid {
			return nil, corruptManifest("guest identity evidence is invalid")
		}
		var previousWorkspace OpaqueID
		for _, proposal := range environment.WorkspaceProposals {
			if !validManifestOpaqueID(proposal.ProposalID) ||
				(previousWorkspace != "" && previousWorkspace >= proposal.ProposalID) ||
				!validManifestGuestPath(proposal.GuestPath) ||
				!validManifestText(proposal.HostPathHint, 0, 4096) ||
				proposal.State != "disabled" {
				return nil, corruptManifest("workspace proposal is invalid")
			}
			if _, exists := workspaceIDs[proposal.ProposalID]; exists {
				return nil, corruptManifest("workspace proposal identity is duplicated")
			}
			workspaceIDs[proposal.ProposalID] = struct{}{}
			previousWorkspace = proposal.ProposalID
		}
		var previousAuthority OpaqueID
		for _, proposalID := range environment.AuthorityProposalRefs {
			if !validManifestOpaqueID(proposalID) ||
				(previousAuthority != "" && previousAuthority >= proposalID) {
				return nil, corruptManifest("environment authority references are invalid")
			}
			if _, exists := authorities[proposalID]; !exists {
				return nil, corruptManifest("environment authority reference is not closed")
			}
			previousAuthority = proposalID
		}
		diskRefs := make(map[OpaqueID]struct{}, len(environment.DiskRefs))
		var previousDisk OpaqueID
		for _, diskID := range environment.DiskRefs {
			if !validManifestOpaqueID(diskID) ||
				(previousDisk != "" && previousDisk >= diskID) {
				return nil, corruptManifest("environment disk references are invalid")
			}
			diskRefs[diskID] = struct{}{}
			previousDisk = diskID
		}
		if (environment.Mode == ExportModeFull && len(diskRefs) == 0) ||
			(environment.Mode == ExportModeConfig && len(diskRefs) != 0) {
			return nil, corruptManifest("environment mode and disk references disagree")
		}
		environments[environment.SourceEnvironmentRef] = manifestEnvironmentFacts{
			entry: environment, diskRefs: diskRefs,
		}
		previous = environment.SourceEnvironmentRef
	}
	for _, component := range components {
		if component.entry.Kind == "profile-state" && !component.used {
			return nil, corruptManifest("profile state component is unreferenced")
		}
	}
	return environments, nil
}

func validateManifestDiskGraph(
	edges []DiskEdge,
	environments map[OpaqueID]manifestEnvironmentFacts,
	disks map[OpaqueID]DiskObject,
) error {
	edgeSet := make(map[string]struct{}, len(edges))
	diskConsumers := make(map[OpaqueID]int, len(disks))
	environmentEdges := make(map[OpaqueID]map[OpaqueID]struct{}, len(environments))
	rootCount := make(map[OpaqueID]int, len(environments))
	previous := ""
	for _, edge := range edges {
		environment, environmentExists := environments[edge.EnvironmentRef]
		disk, diskExists := disks[edge.DiskID]
		key := string(edge.EnvironmentRef) + "\x00" + string(edge.DiskID)
		if !environmentExists || !diskExists || key <= previous ||
			edge.Attachment != disk.Role || !validManifestGuestPath(edge.GuestPath) ||
			(disk.Role == DiskRoleAttached &&
				(!diskFSTypePattern.MatchString(edge.FSType) || edge.FSType == "swap")) {
			return corruptManifest("disk attachment graph is invalid")
		}
		if _, exists := environment.diskRefs[edge.DiskID]; !exists {
			return corruptManifest("disk attachment is absent from its environment")
		}
		if disk.Role == DiskRoleRoot &&
			(edge.GuestPath != "/" || edge.FSType != "" || edge.ReadOnly) {
			return corruptManifest("root disk attachment is invalid")
		}
		if _, exists := edgeSet[key]; exists {
			return corruptManifest("disk attachment is duplicated")
		}
		edgeSet[key] = struct{}{}
		if environmentEdges[edge.EnvironmentRef] == nil {
			environmentEdges[edge.EnvironmentRef] = make(map[OpaqueID]struct{})
		}
		environmentEdges[edge.EnvironmentRef][edge.DiskID] = struct{}{}
		diskConsumers[edge.DiskID]++
		if disk.Role == DiskRoleRoot {
			rootCount[edge.EnvironmentRef]++
		}
		previous = key
	}
	for environmentID, environment := range environments {
		if len(environmentEdges[environmentID]) != len(environment.diskRefs) {
			return corruptManifest("environment disk graph is not closed")
		}
		expectedRoots := 0
		if environment.entry.Mode == ExportModeFull {
			expectedRoots = 1
		}
		if rootCount[environmentID] != expectedRoots {
			return corruptManifest("environment root disk cardinality is invalid")
		}
	}
	for diskID, disk := range disks {
		if diskConsumers[diskID] == 0 ||
			(disk.Role == DiskRoleRoot && diskConsumers[diskID] != 1) {
			return corruptManifest("disk object is not closed by attachments")
		}
	}
	return nil
}

func validateManifestSecrets(
	entries []SecretEntry,
	components map[OpaqueID]*manifestComponentFacts,
	environments map[OpaqueID]manifestEnvironmentFacts,
) error {
	selectedComponents := make(map[OpaqueID]struct{})
	var previous OpaqueID
	for _, secret := range entries {
		if !validManifestOpaqueID(secret.SecretRef) ||
			(previous != "" && previous >= secret.SecretRef) ||
			!validManifestText(secret.DisplayName, 1, 128) ||
			!validManifestProviderToken(secret.Provider, 32) ||
			secret.RequiredAvailability != "available" ||
			len(secret.EnvironmentRefs) == 0 ||
			len(secret.EnvironmentRefs) > int(HardMaxEnvironments) {
			return corruptManifest("secret entry is invalid")
		}
		var previousEnvironment OpaqueID
		for _, environmentRef := range secret.EnvironmentRefs {
			if !validManifestOpaqueID(environmentRef) ||
				(previousEnvironment != "" && previousEnvironment >= environmentRef) {
				return corruptManifest("secret environment references are invalid")
			}
			if _, exists := environments[environmentRef]; !exists {
				return corruptManifest("secret environment reference is not closed")
			}
			previousEnvironment = environmentRef
		}
		switch secret.Transfer {
		case SecretSelectedValue:
			component, exists := components[secret.ValueComponentID]
			if !exists || component.entry.Kind != "secret-value" {
				return corruptManifest("selected secret component is invalid")
			}
			if _, exists := selectedComponents[secret.ValueComponentID]; exists {
				return corruptManifest("selected secret component is reused")
			}
			selectedComponents[secret.ValueComponentID] = struct{}{}
			component.used = true
		case SecretReferenceOnly, SecretNonExportable:
			if secret.ValueComponentID != "" {
				return corruptManifest("unselected secret carries a value component")
			}
		default:
			return corruptManifest("secret transfer mode is invalid")
		}
		previous = secret.SecretRef
	}
	for _, component := range components {
		if component.entry.Kind == "secret-value" && !component.used {
			return corruptManifest("secret value component is unreferenced")
		}
	}
	return nil
}

func validateManifestAuthorities(
	entries []AuthorityProposal,
) (map[OpaqueID]AuthorityProposal, error) {
	authorities := make(map[OpaqueID]AuthorityProposal, len(entries))
	var previous OpaqueID
	for _, proposal := range entries {
		_, validClass := manifestAuthorityClasses[proposal.Class]
		if !validManifestOpaqueID(proposal.ProposalID) ||
			(previous != "" && previous >= proposal.ProposalID) ||
			!validClass || !validManifestText(proposal.SourceSummary, 0, 1024) ||
			proposal.State != "disabled" {
			return nil, corruptManifest("authority proposal is invalid")
		}
		authorities[proposal.ProposalID] = proposal
		previous = proposal.ProposalID
	}
	return authorities, nil
}

func validateManifestExcluded(classes []string) error {
	previous := ""
	for _, class := range classes {
		_, allowed := manifestExcludedClasses[class]
		if !allowed || (previous != "" && previous >= class) {
			return corruptManifest("excluded classes are invalid")
		}
		previous = class
	}
	return nil
}

func validateManifestCapabilities(capabilities []RequiredCapability) error {
	previous := ""
	for _, capability := range capabilities {
		key := capability.ID + "\x00" + capability.Provider
		if !validManifestProviderToken(capability.ID, 64) ||
			!validManifestProviderToken(capability.Provider, 32) ||
			(capability.MinimumVersion != "" &&
				(!adoptionVersionPattern.MatchString(capability.MinimumVersion) ||
					len(capability.MinimumVersion) > 64)) ||
			(previous != "" && previous >= key) {
			return corruptManifest("required capability is invalid")
		}
		previous = key
	}
	return nil
}

func validManifestSourceProduct(source SourceProduct) bool {
	return adoptionVersionPattern.MatchString(source.Version) && len(source.Version) <= 64 &&
		(source.HostOS == "darwin" || source.HostOS == "linux") &&
		(source.HostArch == "arm64" || source.HostArch == "amd64") &&
		validManifestProviderToken(source.Backend, 32) &&
		adoptionVersionPattern.MatchString(source.BackendVersion) &&
		len(source.BackendVersion) <= 64 &&
		(source.GuestArch == "aarch64" || source.GuestArch == "x86_64")
}

func validManifestBundleID(value BundleID) bool {
	_, err := ParseBundleID(string(value))
	return err == nil
}

func validManifestOpaqueID(value OpaqueID) bool {
	_, err := ParseOpaqueID(string(value))
	return err == nil
}

func validManifestComponentKind(value string) bool {
	_, exists := manifestComponentKinds[value]
	return exists
}

func validManifestProviderToken(value string, maximum int) bool {
	return len(value) <= maximum && manifestTokenPattern.MatchString(value)
}

func validManifestText(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func validManifestGuestPath(value string) bool {
	return validManifestText(value, 1, 4096) && strings.HasPrefix(value, "/") &&
		path.Clean(value) == value
}

func validSortedManifestTokens(values []string, maximum int) bool {
	previous := ""
	for _, value := range values {
		if !validManifestProviderToken(value, maximum) ||
			(previous != "" && previous >= value) {
			return false
		}
		previous = value
	}
	return true
}

func validSortedManifestDigests(values []Digest) bool {
	var previous Digest
	for _, value := range values {
		if value.Validate() != nil || (previous != "" && previous >= value) {
			return false
		}
		previous = value
	}
	return true
}

func corruptManifest(reason string) error {
	return fmt.Errorf("%w: %s", ErrCorruptBundle, reason)
}
