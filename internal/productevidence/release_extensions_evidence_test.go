package productevidence

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

const releaseExtensionCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestReleaseExtensionRegistryRequiresClosureAndExactMigration(t *testing.T) {
	closure := RequirementsForFeature(Feature045)
	if len(closure) != 1 {
		t.Fatalf("045 requirements=%d, want 1", len(closure))
	}
	if got := closure[0]; got.ProofID != Proof045ReleaseClosure ||
		got.RequiredFor != RequiredForReleaseCandidate ||
		got.FreshnessPolicy != FreshnessSameCommit ||
		got.RequiredEvidenceClass != "operator-console-final-closure" ||
		got.ArtifactValidator != ArtifactValidatorReleaseClosureV1 {
		t.Fatalf("045 release requirement is weak: %+v", got)
	}
	migration := RequirementsForFeature(Feature046)
	if len(migration) != 1 {
		t.Fatalf("046 requirements=%d, want 1", len(migration))
	}
	if got := migration[0]; got.ProofID != Proof046RealMigration ||
		got.Layer != LayerRealGate || got.RequiredFor != RequiredForReleaseCandidate ||
		got.FreshnessPolicy != FreshnessSameCommitAndPackage ||
		got.RequiredMode != "real-gate" ||
		got.RequiredEvidenceClass != "portable-migration-real-lima" ||
		got.ArtifactValidator != ArtifactValidatorMigrationLimaV1 {
		t.Fatalf("046 migration requirement is weak: %+v", got)
	}
}

func TestReleaseClosureArtifactRequiresFinalReadyTwelveGateClosure(t *testing.T) {
	packageIdentity := releaseExtensionPackageIdentity()
	evidence := releaseClosureFixture()
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateReleaseClosureArtifact(data, releaseExtensionCommit, packageIdentity); err != nil {
		t.Fatal(err)
	}

	evidence.Stage = "installed-local"
	evidence.ReleaseReadiness = false
	data, _ = json.Marshal(evidence)
	if err := validateReleaseClosureArtifact(data, releaseExtensionCommit, packageIdentity); err == nil {
		t.Fatal("non-final release closure passed")
	}
	evidence = releaseClosureFixture()
	evidence.Gates = evidence.Gates[:len(evidence.Gates)-1]
	data, _ = json.Marshal(evidence)
	if err := validateReleaseClosureArtifact(data, releaseExtensionCommit, packageIdentity); err == nil ||
		!strings.Contains(err.Error(), "gate inventory") {
		t.Fatalf("missing gate error=%v", err)
	}
	evidence = releaseClosureFixture()
	evidence.Package.Files = nil
	data, _ = json.Marshal(evidence)
	if err := validateReleaseClosureArtifact(data, releaseExtensionCommit, packageIdentity); err == nil ||
		!strings.Contains(err.Error(), "package inventory") {
		t.Fatalf("missing package inventory error=%v", err)
	}
	evidence = releaseClosureFixture()
	evidence.Formal.InvariantCount = 0
	data, _ = json.Marshal(evidence)
	if err := validateReleaseClosureArtifact(data, releaseExtensionCommit, packageIdentity); err == nil ||
		!strings.Contains(err.Error(), "formal inventory") {
		t.Fatalf("missing formal inventory error=%v", err)
	}
	evidence = releaseClosureFixture()
	for index := range evidence.Gates {
		if evidence.Gates[index].ID == "lima" {
			evidence.Gates[index].Scope = "source"
			evidence.Gates[index].CandidateAcceptance = false
		}
	}
	data, _ = json.Marshal(evidence)
	if err := validateReleaseClosureArtifact(data, releaseExtensionCommit, packageIdentity); err == nil ||
		!strings.Contains(err.Error(), "gate \"lima\"") {
		t.Fatalf("wrong gate scope error=%v", err)
	}
}

func TestMigrationLimaArtifactRequiresExactPackageAndIndependentIdentities(t *testing.T) {
	packageIdentity := releaseExtensionPackageIdentity()
	evidence := migrationLimaFixture()
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMigrationLimaArtifact(data, releaseExtensionCommit, packageIdentity); err != nil {
		t.Fatal(err)
	}

	evidence.Candidate.ArchiveSHA256 = strings.Repeat("f", 64)
	data, _ = json.Marshal(evidence)
	if err := validateMigrationLimaArtifact(data, releaseExtensionCommit, packageIdentity); err == nil ||
		!strings.Contains(err.Error(), "exact package") {
		t.Fatalf("package mismatch error=%v", err)
	}
	packageIdentity = releaseExtensionPackageIdentity()
	packageIdentity.HostOS = "linux"
	evidence = migrationLimaFixture()
	data, _ = json.Marshal(evidence)
	if err := validateMigrationLimaArtifact(data, releaseExtensionCommit, packageIdentity); err == nil ||
		!strings.Contains(err.Error(), "exact package") {
		t.Fatalf("wrong package platform error=%v", err)
	}
	packageIdentity = releaseExtensionPackageIdentity()
	evidence = migrationLimaFixture()
	evidence.IdentityEvidence.Guest.SafeCloneDigests[2] = evidence.IdentityEvidence.Guest.SafeCloneDigests[1]
	data, _ = json.Marshal(evidence)
	if err := validateMigrationLimaArtifact(data, releaseExtensionCommit, packageIdentity); err == nil ||
		!strings.Contains(err.Error(), "guest identity") {
		t.Fatalf("duplicate Safe Clone identity error=%v", err)
	}
	evidence = migrationLimaFixture()
	evidence.SourceImmutability.ProfileState.AfterSHA256 = strings.Repeat("f", 64)
	data, _ = json.Marshal(evidence)
	if err := validateMigrationLimaArtifact(data, releaseExtensionCommit, packageIdentity); err == nil ||
		!strings.Contains(err.Error(), "source immutability") {
		t.Fatalf("mutated source profile state error=%v", err)
	}
	evidence = migrationLimaFixture()
	evidence.Checks.ProfileApplicationStateFidelity = false
	data, _ = json.Marshal(evidence)
	if err := validateMigrationLimaArtifact(data, releaseExtensionCommit, packageIdentity); err == nil ||
		!strings.Contains(err.Error(), "failed required check") {
		t.Fatalf("missing profile-state fidelity error=%v", err)
	}
	evidence = migrationLimaFixture()
	evidence.Artifacts = evidence.Artifacts[:6]
	data, _ = json.Marshal(evidence)
	if err := validateMigrationLimaArtifact(data, releaseExtensionCommit, packageIdentity); err == nil ||
		!strings.Contains(err.Error(), "artifact inventory") {
		t.Fatalf("stale six-artifact inventory error=%v", err)
	}
}

func releaseExtensionPackageIdentity() *PackageIdentity {
	return &PackageIdentity{
		Name:           "hideout",
		ProductVersion: "0.1.0-alpha.4",
		SourceCommit:   releaseExtensionCommit,
		ArtifactSHA256: strings.Repeat("c", 64),
		HostOS:         "darwin",
		HostArch:       "arm64",
	}
}

func releaseClosureFixture() releaseClosureEvidence {
	ref := func(path string) releaseClosureArtifactRef {
		return releaseClosureArtifactRef{
			Path: path, SHA256: strings.Repeat("d", 64), Bytes: 1, Mode: "0600",
		}
	}
	var evidence releaseClosureEvidence
	evidence.Schema = "hideout.release-evidence/v1"
	evidence.GeneratedAt = "2026-08-05T00:00:00Z"
	evidence.Result = "passed"
	evidence.Stage = "final-ready"
	evidence.ReleaseReadiness = true
	evidence.Source.Commit = releaseExtensionCommit
	evidence.Source.Tree = strings.Repeat("b", 40)
	evidence.Source.CommittedAt = "2026-08-04T00:00:00Z"
	evidence.Source.Manifest = ref("source/manifest.tsv")
	evidence.Candidate.Version = "0.1.0-alpha.4"
	evidence.Candidate.Tag = "v0.1.0-alpha.4"
	evidence.Candidate.Channel = "developer-preview"
	evidence.Candidate.SigningMode = "developer-preview-unsigned"
	evidence.Candidate.PublicationStatus = "local-only"
	evidence.Candidate.Archive = ref("package/archive.tar.gz")
	evidence.Candidate.PackageManifest = ref("package/package-manifest.json")
	evidence.Candidate.PackageSummary = ref("package/summary.json")
	evidence.Candidate.LifecycleSummary = ref("package-lifecycle/summary.json")
	evidence.Package = releaseClosurePackageFixture()
	evidence.Formal = releaseClosureFormal{
		Inventory:          ref("formal/inventory.json"),
		SourceInventory:    ref("formal/source-inventory.json"),
		ConfigurationCount: 17,
		ModuleCount:        12,
		InvariantCount:     150,
		PropertyCount:      28,
		GoTestCount:        27,
	}
	for _, id := range []string{
		"dependencies", "formal", "lima", "local", "migration-lima", "package-build",
		"package-components", "package-lifecycle", "performance", "privacy", "recovery", "ui",
	} {
		scope := "candidate"
		candidateAcceptance := true
		if slices.Contains([]string{"dependencies", "formal", "local", "package-components", "recovery"}, id) {
			scope = "source"
			candidateAcceptance = false
		}
		evidence.Gates = append(evidence.Gates, releaseClosureGate{
			ID: id, Scope: scope, Schema: "hideout.fixture/v1",
			GeneratedAt: "2026-08-05T00:00:00Z", Result: "passed",
			CandidateAcceptance: candidateAcceptance, Evidence: ref("gates/" + id + ".json"),
		})
	}
	evidence.Review.Result = "passed"
	evidence.Review.RequiredFindings = 1
	evidence.Review.Report = ref("docs/review.md")
	evidence.Review.ClaimMatrix = ref("docs/claims.md")
	evidence.Limitations = []string{"one", "two", "three", "four", "five"}
	localInstall := ref("closure/local-install.json")
	publication := ref("closure/publication-absence.json")
	evidence.Closure.LocalInstall = releaseClosureItem{Status: "passed", Evidence: &localInstall}
	evidence.Closure.PublicationAbsence = releaseClosureItem{Status: "passed", Evidence: &publication}
	evidence.Digest.Algorithm = "sha256"
	evidence.Digest.DetachedPath = "evidence.json.sha256"
	return evidence
}

func releaseClosurePackageFixture() releaseClosurePackage {
	digest := strings.Repeat("d", 64)
	file := func(path, kind string, executable bool) releaseClosurePackageFile {
		mode := "0644"
		if executable {
			mode = "0755"
		}
		return releaseClosurePackageFile{
			Path: path, Kind: kind, SHA256: digest, Bytes: 1, Mode: mode, Executable: executable,
		}
	}
	var value releaseClosurePackage
	for index := range 100 {
		value.Files = append(value.Files, file(fmt.Sprintf("files/file-%03d", index), "data", false))
	}
	for index := range 8 {
		value.Helpers = append(value.Helpers,
			file(fmt.Sprintf("helpers/manifest-%d.json", index), "helper-manifest", false),
			file(fmt.Sprintf("helpers/linux-%d", index), "linux-helper", true),
		)
	}
	value.Helpers = append(value.Helpers,
		file("bin/hideout-migration-vz-adopt-darwin-arm64", "binary", true))
	value.BrowserConsole.Manifest = file("share/hideout/browser/manifest.json", "browser-manifest", false)
	value.BrowserConsole.Container = file("bin/hideout", "binary", true)
	value.BrowserConsole.Inventory.Schema = "hideout.embedded-asset-manifest/v1"
	value.BrowserConsole.Inventory.ID = "browser-console"
	value.BrowserConsole.Inventory.Container = "bin/hideout"
	value.BrowserConsole.Inventory.ContainerSHA256 = digest
	value.BrowserConsole.Inventory.License = "Apache-2.0"
	for _, asset := range []struct {
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
	} {
		value.BrowserConsole.Inventory.Assets = append(value.BrowserConsole.Inventory.Assets,
			releaseClosureBrowserAsset{Path: asset.path, MediaType: asset.mediaType, SHA256: digest})
	}
	value.Runtime.Family = "hideout-runtime"
	value.Runtime.Revision = "fixture"
	value.Runtime.CatalogFileSHA256 = digest
	value.Runtime.ArtifactSHA256 = digest
	value.Runtime.Catalog = file("share/hideout/runtime/catalog.json", "runtime-catalog", false)
	value.Runtime.Contract = file("share/hideout/runtime/contract.json", "runtime-contract", false)
	return value
}

func migrationLimaFixture() migrationLimaEvidence {
	digest := func(character string) string { return strings.Repeat(character, 64) }
	var evidence migrationLimaEvidence
	evidence.Schema = "hideout.migration-lima-evidence/v1"
	evidence.GeneratedAt = "2026-08-05T00:00:00Z"
	evidence.Result = "passed"
	evidence.CandidateAcceptance = true
	evidence.Source.Commit = releaseExtensionCommit
	evidence.Source.Tree = strings.Repeat("b", 40)
	evidence.Candidate.PointerSHA256 = digest("d")
	evidence.Candidate.ArchiveSHA256 = digest("c")
	evidence.Candidate.InstalledBinarySHA256 = digest("e")
	evidence.Bundle.SHA256 = digest("f")
	evidence.Bundle.Bytes = 1
	evidence.Bundle.ReusedDestinations = 4
	evidence.SourceImmutability.RootDisk = migrationLimaBeforeAfter{BeforeSHA256: digest("1"), AfterSHA256: digest("1")}
	evidence.SourceImmutability.AttachedDisk = migrationLimaBeforeAfter{BeforeSHA256: digest("2"), AfterSHA256: digest("2")}
	evidence.SourceImmutability.ProfileState = migrationLimaBeforeAfter{BeforeSHA256: digest("3"), AfterSHA256: digest("3")}
	evidence.SourceImmutability.EnvironmentRecord = migrationLimaBeforeAfter{BeforeSHA256: digest("3"), AfterSHA256: digest("3")}
	evidence.IdentityEvidence.Control = migrationLimaDestinationIdentities{
		SourceDigest: digest("1"), DestinationDigests: []string{digest("2"), digest("3"), digest("4"), digest("5")},
	}
	evidence.IdentityEvidence.Backend = migrationLimaDestinationIdentities{
		SourceDigest: digest("6"), DestinationDigests: []string{digest("7"), digest("8"), digest("9"), digest("a")},
	}
	evidence.IdentityEvidence.Guest.SourceDigest = digest("b")
	evidence.IdentityEvidence.Guest.SafeCloneDigests = []string{digest("c"), digest("d"), digest("e")}
	evidence.IdentityEvidence.Guest.ExactRestoreDigest = digest("b")
	evidence.CrashRecovery.Cuts = append(evidence.CrashRecovery.Cuts,
		struct {
			Phase                string `json:"phase"`
			DaemonInstanceDigest string `json:"daemonInstanceDigest"`
		}{Phase: "materializing", DaemonInstanceDigest: digest("f")},
		struct {
			Phase                string `json:"phase"`
			DaemonInstanceDigest string `json:"daemonInstanceDigest"`
		}{Phase: "adopting", DaemonInstanceDigest: digest("0")},
	)
	evidence.CrashRecovery.FinalDaemonInstanceDigest = digest("1")
	evidence.CrashRecovery.MaterializationRequiredProtectedResume = true
	evidence.CrashRecovery.AdoptionRestartedWithoutBundleSecret = true
	evidence.CompatibilityEvidence.Fixture = "missing-package-owned-zero-network-executor"
	evidence.CompatibilityEvidence.ErrorCode = "migration.capability.unavailable"
	evidence.Checks = migrationLimaChecks{
		PackageCandidateInstalled: true, EncryptedBundleSealed: true,
		RootDiskFidelity: true, AttachedDiskFidelity: true,
		ProfileApplicationStateFidelity: true, GeneratedProfileStateExcluded: true,
		HostWorkspaceExcluded: true,
		SourceImmutable:       true, WrongPassphraseNoDestinationEnvironment: true,
		IncompatibleAdoptionExecutorRejectedBeforeEffects: true, TerminalReceipts: true,
		LimaInventoryStopped: true, NetworkAuthorityReapproved: true,
		SameBundleThreeSafeClones: true, FreshControlIdentity: true,
		FreshBackendIdentity: true, SafeCloneGuestIdentityFresh: true,
		ExactRestoreGuestIdentityPreserved: true, MaterializationCrashResumed: true,
		AdoptionCrashRecovered: true, DaemonIdentityFreshAcrossCrashRecovery: true,
	}
	for index, path := range []string{
		"export-terminal.json", "gate.log", "import-exact-terminal.json",
		"import-safe-one-terminal.json", "import-safe-three-terminal.json",
		"import-safe-two-terminal.json", "run-review.json", "stage-events.jsonl",
	} {
		evidence.Artifacts = append(evidence.Artifacts, migrationLimaArtifact{
			Path: path, SHA256: digest(string(rune('2' + index))), Bytes: 1, Mode: "0600",
		})
	}
	evidence.Limitations = []string{"functional, not performance", "one physical host"}
	return evidence
}
