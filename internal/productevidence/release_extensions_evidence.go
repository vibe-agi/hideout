package productevidence

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type releaseClosureArtifactRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Mode   string `json:"mode"`
}

type releaseClosurePackageFile struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
	Mode       string `json:"mode"`
	Executable bool   `json:"executable"`
}

type releaseClosureBrowserAsset struct {
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
	SHA256    string `json:"sha256"`
}

type releaseClosurePackage struct {
	Files          []releaseClosurePackageFile `json:"files"`
	Helpers        []releaseClosurePackageFile `json:"helpers"`
	BrowserConsole struct {
		Manifest  releaseClosurePackageFile `json:"manifest"`
		Container releaseClosurePackageFile `json:"container"`
		Inventory struct {
			Schema          string                       `json:"schema"`
			ID              string                       `json:"id"`
			Container       string                       `json:"container"`
			ContainerSHA256 string                       `json:"containerSHA256"`
			License         string                       `json:"license"`
			Assets          []releaseClosureBrowserAsset `json:"assets"`
		} `json:"inventory"`
	} `json:"browserConsole"`
	Runtime struct {
		Family            string                    `json:"family"`
		Revision          string                    `json:"revision"`
		CatalogFileSHA256 string                    `json:"catalogFileSHA256"`
		ArtifactSHA256    string                    `json:"artifactSHA256"`
		Catalog           releaseClosurePackageFile `json:"catalog"`
		Contract          releaseClosurePackageFile `json:"contract"`
	} `json:"runtime"`
}

type releaseClosureFormal struct {
	Inventory          releaseClosureArtifactRef `json:"inventory"`
	SourceInventory    releaseClosureArtifactRef `json:"sourceInventory"`
	ConfigurationCount int                       `json:"configurationCount"`
	ModuleCount        int                       `json:"moduleCount"`
	InvariantCount     int                       `json:"invariantCount"`
	PropertyCount      int                       `json:"propertyCount"`
	GoTestCount        int                       `json:"goTestCount"`
}

type releaseClosureGate struct {
	ID                  string                     `json:"id"`
	Scope               string                     `json:"scope"`
	Schema              string                     `json:"schema"`
	GeneratedAt         string                     `json:"generatedAt"`
	Result              string                     `json:"result"`
	CandidateAcceptance bool                       `json:"candidateAcceptance"`
	Evidence            releaseClosureArtifactRef  `json:"evidence"`
	Pointer             *releaseClosureArtifactRef `json:"pointer,omitempty"`
}

type releaseClosureItem struct {
	Status   string                     `json:"status"`
	Evidence *releaseClosureArtifactRef `json:"evidence,omitempty"`
}

type releaseClosureEvidence struct {
	Schema           string `json:"schema"`
	GeneratedAt      string `json:"generatedAt"`
	Result           string `json:"result"`
	Stage            string `json:"stage"`
	ReleaseReadiness bool   `json:"releaseReadiness"`
	Source           struct {
		Commit      string                    `json:"commit"`
		Tree        string                    `json:"tree"`
		Dirty       bool                      `json:"dirty"`
		CommittedAt string                    `json:"committedAt"`
		Manifest    releaseClosureArtifactRef `json:"manifest"`
	} `json:"source"`
	Candidate struct {
		Version           string                    `json:"version"`
		Tag               string                    `json:"tag"`
		Channel           string                    `json:"channel"`
		SigningMode       string                    `json:"signingMode"`
		PublicationStatus string                    `json:"publicationStatus"`
		Archive           releaseClosureArtifactRef `json:"archive"`
		PackageManifest   releaseClosureArtifactRef `json:"packageManifest"`
		PackageSummary    releaseClosureArtifactRef `json:"packageSummary"`
		LifecycleSummary  releaseClosureArtifactRef `json:"lifecycleSummary"`
	} `json:"candidate"`
	Package releaseClosurePackage `json:"package"`
	Formal  releaseClosureFormal  `json:"formal"`
	Gates   []releaseClosureGate  `json:"gates"`
	Review  struct {
		Result               string                    `json:"result"`
		RequiredFindings     int                       `json:"requiredFindings"`
		OpenRequiredFindings int                       `json:"openRequiredFindings"`
		Report               releaseClosureArtifactRef `json:"report"`
		ClaimMatrix          releaseClosureArtifactRef `json:"claimMatrix"`
	} `json:"review"`
	Limitations []string `json:"limitations"`
	Closure     struct {
		LocalInstall       releaseClosureItem `json:"localInstall"`
		PublicationAbsence releaseClosureItem `json:"publicationAbsence"`
	} `json:"closure"`
	Digest struct {
		Algorithm    string `json:"algorithm"`
		DetachedPath string `json:"detachedPath"`
	} `json:"digest"`
}

type migrationLimaEvidence struct {
	Schema              string `json:"schema"`
	GeneratedAt         string `json:"generatedAt"`
	Result              string `json:"result"`
	CandidateAcceptance bool   `json:"candidateAcceptance"`
	Source              struct {
		Commit string `json:"commit"`
		Tree   string `json:"tree"`
		Dirty  bool   `json:"dirty"`
	} `json:"source"`
	Candidate struct {
		PointerSHA256         string `json:"pointerSHA256"`
		ArchiveSHA256         string `json:"archiveSHA256"`
		InstalledBinarySHA256 string `json:"installedBinarySHA256"`
	} `json:"candidate"`
	Bundle struct {
		SHA256             string `json:"sha256"`
		Bytes              int64  `json:"bytes"`
		ReusedDestinations int    `json:"reusedDestinations"`
	} `json:"bundle"`
	SourceImmutability struct {
		RootDisk          migrationLimaBeforeAfter `json:"rootDisk"`
		AttachedDisk      migrationLimaBeforeAfter `json:"attachedDisk"`
		ProfileState      migrationLimaBeforeAfter `json:"profileState"`
		EnvironmentRecord migrationLimaBeforeAfter `json:"environmentRecord"`
	} `json:"sourceImmutability"`
	IdentityEvidence struct {
		Control migrationLimaDestinationIdentities `json:"control"`
		Backend migrationLimaDestinationIdentities `json:"backend"`
		Guest   struct {
			SourceDigest       string   `json:"sourceDigest"`
			SafeCloneDigests   []string `json:"safeCloneDigests"`
			ExactRestoreDigest string   `json:"exactRestoreDigest"`
		} `json:"guest"`
	} `json:"identityEvidence"`
	CrashRecovery struct {
		Cuts []struct {
			Phase                string `json:"phase"`
			DaemonInstanceDigest string `json:"daemonInstanceDigest"`
		} `json:"cuts"`
		FinalDaemonInstanceDigest              string `json:"finalDaemonInstanceDigest"`
		MaterializationRequiredProtectedResume bool   `json:"materializationRequiredProtectedResume"`
		AdoptionRestartedWithoutBundleSecret   bool   `json:"adoptionRestartedWithoutBundleSecret"`
	} `json:"crashRecovery"`
	CompatibilityEvidence struct {
		Fixture                       string `json:"fixture"`
		ErrorCode                     string `json:"errorCode"`
		OperationCreated              bool   `json:"operationCreated"`
		DestinationEnvironmentCreated bool   `json:"destinationEnvironmentCreated"`
	} `json:"compatibilityEvidence"`
	Checks      migrationLimaChecks     `json:"checks"`
	Artifacts   []migrationLimaArtifact `json:"artifacts"`
	Limitations []string                `json:"limitations"`
}

type migrationLimaBeforeAfter struct {
	BeforeSHA256 string `json:"beforeSHA256"`
	AfterSHA256  string `json:"afterSHA256"`
}

type migrationLimaDestinationIdentities struct {
	SourceDigest       string   `json:"sourceDigest"`
	DestinationDigests []string `json:"destinationDigests"`
}

type migrationLimaChecks struct {
	PackageCandidateInstalled                         bool `json:"packageCandidateInstalled"`
	EncryptedBundleSealed                             bool `json:"encryptedBundleSealed"`
	RootDiskFidelity                                  bool `json:"rootDiskFidelity"`
	AttachedDiskFidelity                              bool `json:"attachedDiskFidelity"`
	ProfileApplicationStateFidelity                   bool `json:"profileApplicationStateFidelity"`
	GeneratedProfileStateExcluded                     bool `json:"generatedProfileStateExcluded"`
	HostWorkspaceExcluded                             bool `json:"hostWorkspaceExcluded"`
	SourceImmutable                                   bool `json:"sourceImmutable"`
	WrongPassphraseNoDestinationEnvironment           bool `json:"wrongPassphraseNoDestinationEnvironment"`
	IncompatibleAdoptionExecutorRejectedBeforeEffects bool `json:"incompatibleAdoptionExecutorRejectedBeforeEffects"`
	TerminalReceipts                                  bool `json:"terminalReceipts"`
	LimaInventoryStopped                              bool `json:"limaInventoryStopped"`
	NetworkAuthorityReapproved                        bool `json:"networkAuthorityReapproved"`
	SameBundleThreeSafeClones                         bool `json:"sameBundleThreeSafeClones"`
	FreshControlIdentity                              bool `json:"freshControlIdentity"`
	FreshBackendIdentity                              bool `json:"freshBackendIdentity"`
	SafeCloneGuestIdentityFresh                       bool `json:"safeCloneGuestIdentityFresh"`
	ExactRestoreGuestIdentityPreserved                bool `json:"exactRestoreGuestIdentityPreserved"`
	MaterializationCrashResumed                       bool `json:"materializationCrashResumed"`
	AdoptionCrashRecovered                            bool `json:"adoptionCrashRecovered"`
	DaemonIdentityFreshAcrossCrashRecovery            bool `json:"daemonIdentityFreshAcrossCrashRecovery"`
}

type migrationLimaArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Mode   string `json:"mode"`
}

func validateReleaseClosureArtifact(data []byte, expectedCommit string, expectedPackage *PackageIdentity) error {
	var evidence releaseClosureEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("release closure evidence: %w", err)
	}
	if evidence.Schema != "hideout.release-evidence/v1" || evidence.Result != "passed" ||
		evidence.Stage != "final-ready" || !evidence.ReleaseReadiness {
		return errors.New("release closure is not final-ready passing evidence")
	}
	if !validEvidenceTime(evidence.GeneratedAt) || !validEvidenceTime(evidence.Source.CommittedAt) ||
		!IsCanonicalCommit(evidence.Source.Commit) || !IsCanonicalCommit(evidence.Source.Tree) || evidence.Source.Dirty {
		return errors.New("release closure source identity is invalid")
	}
	if strings.TrimSpace(expectedCommit) != "" && evidence.Source.Commit != strings.TrimSpace(expectedCommit) {
		return errors.New("release closure is not bound to the expected commit")
	}
	if !IsCanonicalProductVersion(evidence.Candidate.Version) ||
		evidence.Candidate.Tag != "v"+evidence.Candidate.Version ||
		evidence.Candidate.Channel != "developer-preview" ||
		evidence.Candidate.SigningMode != "developer-preview-unsigned" ||
		evidence.Candidate.PublicationStatus != "local-only" {
		return errors.New("release closure candidate identity is invalid")
	}
	if expectedPackage == nil || expectedPackage.ValidateCandidateCommit(evidence.Source.Commit) != nil ||
		expectedPackage.ProductVersion != evidence.Candidate.Version ||
		expectedPackage.HostOS != "darwin" || expectedPackage.HostArch != "arm64" {
		return errors.New("release closure source/version does not match the signed package expectation")
	}
	for _, artifact := range []releaseClosureArtifactRef{
		evidence.Source.Manifest, evidence.Candidate.Archive,
		evidence.Candidate.PackageManifest, evidence.Candidate.PackageSummary,
		evidence.Candidate.LifecycleSummary, evidence.Review.Report, evidence.Review.ClaimMatrix,
	} {
		if err := validateReleaseClosureRef(artifact); err != nil {
			return err
		}
	}
	if err := validateReleaseClosurePackage(evidence.Package); err != nil {
		return err
	}
	if evidence.Formal.ConfigurationCount != 17 || evidence.Formal.ModuleCount != 12 ||
		evidence.Formal.InvariantCount != 140 || evidence.Formal.PropertyCount != 28 ||
		evidence.Formal.GoTestCount != 27 {
		return errors.New("release closure formal inventory counts are invalid")
	}
	for _, artifact := range []releaseClosureArtifactRef{
		evidence.Formal.Inventory, evidence.Formal.SourceInventory,
	} {
		if err := validateReleaseClosureRef(artifact); err != nil {
			return err
		}
	}
	wantGateIDs := []string{
		"dependencies", "formal", "lima", "local", "migration-lima", "package-build",
		"package-components", "package-lifecycle", "performance", "privacy", "recovery", "ui",
	}
	wantGateScopes := map[string]string{
		"dependencies":       "source",
		"formal":             "source",
		"lima":               "candidate",
		"local":              "source",
		"migration-lima":     "candidate",
		"package-build":      "candidate",
		"package-components": "source",
		"package-lifecycle":  "candidate",
		"performance":        "candidate",
		"privacy":            "candidate",
		"recovery":           "source",
		"ui":                 "candidate",
	}
	gotGateIDs := make([]string, 0, len(evidence.Gates))
	for _, gate := range evidence.Gates {
		if gate.Result != "passed" || !validEvidenceTime(gate.GeneratedAt) ||
			gate.Scope != wantGateScopes[gate.ID] || strings.TrimSpace(gate.Schema) == "" {
			return fmt.Errorf("release closure gate %q is invalid", gate.ID)
		}
		if gate.Scope == "candidate" && !gate.CandidateAcceptance {
			return fmt.Errorf("release closure candidate gate %q is not acceptance evidence", gate.ID)
		}
		if gate.Scope == "source" && gate.CandidateAcceptance {
			return fmt.Errorf("release closure source gate %q claims candidate acceptance", gate.ID)
		}
		if err := validateReleaseClosureRef(gate.Evidence); err != nil {
			return err
		}
		if gate.Pointer != nil {
			if err := validateReleaseClosureRef(*gate.Pointer); err != nil {
				return err
			}
		}
		gotGateIDs = append(gotGateIDs, gate.ID)
	}
	slices.Sort(gotGateIDs)
	if !slices.Equal(gotGateIDs, wantGateIDs) {
		return fmt.Errorf("release closure gate inventory=%v, want=%v", gotGateIDs, wantGateIDs)
	}
	if evidence.Review.Result != "passed" || evidence.Review.RequiredFindings < 1 ||
		evidence.Review.OpenRequiredFindings != 0 || len(evidence.Limitations) < 5 {
		return errors.New("release closure review or limitations are incomplete")
	}
	for _, item := range []releaseClosureItem{evidence.Closure.LocalInstall, evidence.Closure.PublicationAbsence} {
		if item.Status != "passed" || item.Evidence == nil {
			return errors.New("release closure receipts are incomplete")
		}
		if err := validateReleaseClosureRef(*item.Evidence); err != nil {
			return err
		}
	}
	if evidence.Digest.Algorithm != "sha256" || !safeEvidencePath(evidence.Digest.DetachedPath) {
		return errors.New("release closure detached digest contract is invalid")
	}
	return nil
}

func validateMigrationLimaArtifact(data []byte, expectedCommit string, expectedPackage *PackageIdentity) error {
	var evidence migrationLimaEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("migration Lima evidence: %w", err)
	}
	if evidence.Schema != "hideout.migration-lima-evidence/v1" || evidence.Result != "passed" ||
		!evidence.CandidateAcceptance || !validEvidenceTime(evidence.GeneratedAt) {
		return errors.New("migration Lima evidence identity or result is invalid")
	}
	if !IsCanonicalCommit(evidence.Source.Commit) || !IsCanonicalCommit(evidence.Source.Tree) || evidence.Source.Dirty ||
		(strings.TrimSpace(expectedCommit) != "" && evidence.Source.Commit != strings.TrimSpace(expectedCommit)) {
		return errors.New("migration Lima evidence is not bound to the clean candidate source")
	}
	if expectedPackage == nil || expectedPackage.ValidateCandidateCommit(evidence.Source.Commit) != nil ||
		expectedPackage.HostOS != "darwin" || expectedPackage.HostArch != "arm64" ||
		evidence.Candidate.ArchiveSHA256 != expectedPackage.ArtifactSHA256 {
		return errors.New("migration Lima evidence is not bound to the exact package archive")
	}
	for _, digest := range []string{
		evidence.Candidate.PointerSHA256, evidence.Candidate.ArchiveSHA256,
		evidence.Candidate.InstalledBinarySHA256, evidence.Bundle.SHA256,
	} {
		if !isLowerHexSHA256(digest) {
			return errors.New("migration Lima evidence contains a non-canonical digest")
		}
	}
	if evidence.Bundle.Bytes <= 0 || evidence.Bundle.ReusedDestinations != 4 {
		return errors.New("migration bundle was not reused across four destinations")
	}
	for _, pair := range []migrationLimaBeforeAfter{
		evidence.SourceImmutability.RootDisk,
		evidence.SourceImmutability.AttachedDisk,
		evidence.SourceImmutability.ProfileState,
		evidence.SourceImmutability.EnvironmentRecord,
	} {
		if !isLowerHexSHA256(pair.BeforeSHA256) || pair.BeforeSHA256 != pair.AfterSHA256 {
			return errors.New("migration source immutability evidence is invalid")
		}
	}
	for _, identities := range []migrationLimaDestinationIdentities{
		evidence.IdentityEvidence.Control, evidence.IdentityEvidence.Backend,
	} {
		if !validFreshDestinationIdentities(identities, 4) {
			return errors.New("migration control/backend identities are not independently fresh")
		}
	}
	guest := evidence.IdentityEvidence.Guest
	if !isLowerHexSHA256(guest.SourceDigest) || len(guest.SafeCloneDigests) != 3 ||
		!allDistinctDigests(guest.SafeCloneDigests...) || slices.Contains(guest.SafeCloneDigests, guest.SourceDigest) ||
		guest.ExactRestoreDigest != guest.SourceDigest {
		return errors.New("migration guest identity policy evidence is invalid")
	}
	if len(evidence.CrashRecovery.Cuts) != 2 || evidence.CrashRecovery.Cuts[0].Phase != "materializing" ||
		evidence.CrashRecovery.Cuts[1].Phase != "adopting" ||
		!evidence.CrashRecovery.MaterializationRequiredProtectedResume ||
		!evidence.CrashRecovery.AdoptionRestartedWithoutBundleSecret {
		return errors.New("migration crash-recovery cuts are incomplete")
	}
	daemonDigests := []string{
		evidence.CrashRecovery.Cuts[0].DaemonInstanceDigest,
		evidence.CrashRecovery.Cuts[1].DaemonInstanceDigest,
		evidence.CrashRecovery.FinalDaemonInstanceDigest,
	}
	if !allDistinctDigests(daemonDigests...) {
		return errors.New("migration crash recovery reused a daemon identity")
	}
	compatibility := evidence.CompatibilityEvidence
	if compatibility.Fixture != "missing-package-owned-zero-network-executor" ||
		compatibility.ErrorCode != "migration.capability.unavailable" ||
		compatibility.OperationCreated || compatibility.DestinationEnvironmentCreated {
		return errors.New("migration incompatibility was not rejected before effects")
	}
	if !evidence.Checks.allPassed() {
		return errors.New("migration Lima evidence contains a failed required check")
	}
	wantArtifactPaths := []string{
		"export-terminal.json", "gate.log", "import-exact-terminal.json",
		"import-safe-one-terminal.json", "import-safe-three-terminal.json",
		"import-safe-two-terminal.json", "run-review.json", "stage-events.jsonl",
	}
	gotArtifactPaths := make([]string, 0, len(evidence.Artifacts))
	for _, artifact := range evidence.Artifacts {
		if artifact.Bytes <= 0 || artifact.Mode != "0600" || !isLowerHexSHA256(artifact.SHA256) ||
			filepath.Base(artifact.Path) != artifact.Path || !safeEvidencePath(artifact.Path) {
			return fmt.Errorf("migration artifact %q is invalid", artifact.Path)
		}
		gotArtifactPaths = append(gotArtifactPaths, artifact.Path)
	}
	slices.Sort(gotArtifactPaths)
	if !slices.Equal(gotArtifactPaths, wantArtifactPaths) {
		return fmt.Errorf("migration artifact inventory=%v, want=%v", gotArtifactPaths, wantArtifactPaths)
	}
	if len(evidence.Limitations) != 2 || slices.Contains(evidence.Limitations, "") {
		return errors.New("migration evidence limitations are incomplete")
	}
	return nil
}

func validateReleaseClosureRef(ref releaseClosureArtifactRef) error {
	if !safeEvidencePath(ref.Path) || !isLowerHexSHA256(ref.SHA256) || ref.Bytes < 0 ||
		(ref.Mode != "0600" && ref.Mode != "0644" && ref.Mode != "0755") {
		return fmt.Errorf("release closure artifact %q is invalid", ref.Path)
	}
	return nil
}

func validateReleaseClosurePackage(value releaseClosurePackage) error {
	if len(value.Files) < 100 || len(value.Helpers) != 17 {
		return errors.New("release closure package inventory counts are invalid")
	}
	paths := make(map[string]struct{}, len(value.Files))
	for _, file := range value.Files {
		if err := validateReleaseClosurePackageFile(file); err != nil {
			return err
		}
		if _, exists := paths[file.Path]; exists {
			return fmt.Errorf("release closure package file %q is duplicated", file.Path)
		}
		paths[file.Path] = struct{}{}
	}
	helperKinds := map[string]int{}
	for _, helper := range value.Helpers {
		if err := validateReleaseClosurePackageFile(helper); err != nil {
			return err
		}
		helperKinds[helper.Kind]++
	}
	if helperKinds["helper-manifest"] != 8 || helperKinds["linux-helper"] != 8 ||
		helperKinds["binary"] != 1 {
		return errors.New("release closure helper inventory is invalid")
	}
	if !slices.ContainsFunc(value.Helpers, func(helper releaseClosurePackageFile) bool {
		return helper.Kind == "binary" && helper.Path == "bin/hideout-migration-vz-adopt-darwin-arm64"
	}) {
		return errors.New("release closure package omits the Darwin migration adoption helper")
	}
	for _, file := range []releaseClosurePackageFile{
		value.BrowserConsole.Manifest, value.BrowserConsole.Container,
		value.Runtime.Catalog, value.Runtime.Contract,
	} {
		if err := validateReleaseClosurePackageFile(file); err != nil {
			return err
		}
	}
	inventory := value.BrowserConsole.Inventory
	if inventory.Schema != "hideout.embedded-asset-manifest/v1" || inventory.ID != "browser-console" ||
		inventory.Container != "bin/hideout" || inventory.License != "Apache-2.0" ||
		!isLowerHexSHA256(inventory.ContainerSHA256) || len(inventory.Assets) != 9 {
		return errors.New("release closure browser-console inventory is invalid")
	}
	wantAssets := []struct {
		path      string
		mediaType string
	}{
		{"index.html", "text/html; charset=utf-8"},
		{"style.css", "text/css; charset=utf-8"},
		{"state.js", "text/javascript; charset=utf-8"},
		{"client.js", "text/javascript; charset=utf-8"},
		{"activity.js", "text/javascript; charset=utf-8"},
		{"config.js", "text/javascript; charset=utf-8"},
		{"migration.js", "text/javascript; charset=utf-8"},
		{"presentation.js", "text/javascript; charset=utf-8"},
		{"app.js", "text/javascript; charset=utf-8"},
	}
	for index, asset := range inventory.Assets {
		if asset.Path != wantAssets[index].path || asset.MediaType != wantAssets[index].mediaType ||
			!isLowerHexSHA256(asset.SHA256) {
			return fmt.Errorf("release closure browser-console asset %d is invalid", index)
		}
	}
	if strings.TrimSpace(value.Runtime.Family) == "" || strings.TrimSpace(value.Runtime.Revision) == "" ||
		!isLowerHexSHA256(value.Runtime.CatalogFileSHA256) ||
		!isLowerHexSHA256(value.Runtime.ArtifactSHA256) {
		return errors.New("release closure runtime inventory is invalid")
	}
	return nil
}

func validateReleaseClosurePackageFile(file releaseClosurePackageFile) error {
	if !safeEvidencePath(file.Path) || strings.TrimSpace(file.Kind) == "" || len(file.Kind) > 64 ||
		!isLowerHexSHA256(file.SHA256) || file.Bytes < 0 {
		return fmt.Errorf("release closure package file %q is invalid", file.Path)
	}
	if file.Executable {
		if file.Mode != "0700" && file.Mode != "0755" {
			return fmt.Errorf("release closure executable package file %q has mode %q", file.Path, file.Mode)
		}
	} else if file.Mode != "0600" && file.Mode != "0644" {
		return fmt.Errorf("release closure data package file %q has mode %q", file.Path, file.Mode)
	}
	return nil
}

func safeEvidencePath(path string) bool {
	return path != "" && filepath.IsLocal(filepath.Clean(path)) && filepath.Clean(path) == path &&
		!strings.ContainsAny(path, "\x00\r\n\t")
}

func validEvidenceTime(value string) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && !parsed.IsZero()
}

func validFreshDestinationIdentities(identities migrationLimaDestinationIdentities, want int) bool {
	return isLowerHexSHA256(identities.SourceDigest) && len(identities.DestinationDigests) == want &&
		allDistinctDigests(identities.DestinationDigests...) &&
		!slices.Contains(identities.DestinationDigests, identities.SourceDigest)
}

func allDistinctDigests(values ...string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !isLowerHexSHA256(value) {
			return false
		}
		if _, ok := seen[value]; ok {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func (checks migrationLimaChecks) allPassed() bool {
	return checks.PackageCandidateInstalled && checks.EncryptedBundleSealed &&
		checks.RootDiskFidelity && checks.AttachedDiskFidelity &&
		checks.ProfileApplicationStateFidelity && checks.GeneratedProfileStateExcluded &&
		checks.HostWorkspaceExcluded &&
		checks.SourceImmutable && checks.WrongPassphraseNoDestinationEnvironment &&
		checks.IncompatibleAdoptionExecutorRejectedBeforeEffects && checks.TerminalReceipts &&
		checks.LimaInventoryStopped && checks.NetworkAuthorityReapproved &&
		checks.SameBundleThreeSafeClones && checks.FreshControlIdentity &&
		checks.FreshBackendIdentity && checks.SafeCloneGuestIdentityFresh &&
		checks.ExactRestoreGuestIdentityPreserved && checks.MaterializationCrashResumed &&
		checks.AdoptionCrashRecovered && checks.DaemonIdentityFreshAcrossCrashRecovery
}
