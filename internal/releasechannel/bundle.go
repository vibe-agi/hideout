package releasechannel

import (
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	exportboundary "github.com/vibe-agi/hideout/internal/export"
	"github.com/vibe-agi/hideout/internal/packagekit"
	"github.com/vibe-agi/hideout/internal/productevidence"
)

var canonicalEvidenceCore = map[string]string{
	"candidate-identity.json":       "manifest",
	"release-readiness.json":        "manifest",
	"package/package-manifest.json": "manifest",
	"package/verify.json":           "manifest",
	"signing/observation.json":      "manifest",
	"notarization/observation.json": "manifest",
	"runtime/build-provenance.json": "manifest",
	"gates/gate2.json":              "gate",
	"gates/gate3.json":              "gate",
	"proof-registry.json":           "manifest",
}

type EvidenceBundle struct {
	Schema          string          `json:"schema"`
	GeneratedAt     time.Time       `json:"generatedAt"`
	SourceCommit    string          `json:"sourceCommit"`
	Package         PackageIdentity `json:"package"`
	RegistrySchema  string          `json:"registrySchema"`
	ProofIDs        []string        `json:"proofIds"`
	FeatureIDs      []string        `json:"featureIds,omitempty"`
	Files           []BundleFile    `json:"files"`
	RedactionStatus string          `json:"redactionStatus"`
}

type BundleFile struct {
	Path            string                          `json:"path"`
	Kind            string                          `json:"kind"`
	SHA256          string                          `json:"sha256"`
	Bytes           int64                           `json:"bytes"`
	RedactionStatus string                          `json:"redactionStatus"`
	ExportDecision  exportboundary.ExportDecision   `json:"exportDecision"`
	RedactionStages []exportboundary.RedactionStage `json:"redactionStages"`
}

func (b EvidenceBundle) Validate(root string, requiredProofIDs []string) error {
	if b.Schema != EvidenceBundleSchema || b.GeneratedAt.IsZero() || !IsCommit(b.SourceCommit) {
		return errors.New("evidence bundle schema, generatedAt, and sourceCommit are required")
	}
	if err := b.Package.Validate(); err != nil || b.Package.SourceCommit != b.SourceCommit {
		return errors.New("evidence bundle package identity is invalid")
	}
	if b.RegistrySchema != productevidence.RegistrySchema || b.RedactionStatus != productevidence.RedactionPassed {
		return errors.New("evidence bundle requires the authoritative registry and passed redaction")
	}
	want := append([]string(nil), requiredProofIDs...)
	got := append([]string(nil), b.ProofIDs...)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "\x00") != strings.Join(got, "\x00") {
		return errors.New("evidence bundle proof IDs do not match registry requirements")
	}
	if err := validateFeatureIDs(b.FeatureIDs); err != nil {
		return fmt.Errorf("evidence bundle: %w", err)
	}
	seen := map[string]string{}
	var total int64
	for _, file := range b.Files {
		if file.Path == "bundle-manifest.json" || file.Path == "SHA256SUMS" || seen[file.Path] != "" {
			return fmt.Errorf("invalid or duplicate bundle file %q", file.Path)
		}
		seen[file.Path] = file.Kind
		if file.RedactionStatus != productevidence.RedactionPassed {
			return fmt.Errorf("bundle file %q has not passed redaction", file.Path)
		}
		if err := VerifyRootedRegularFile(root, file.Path, file.SHA256, file.Bytes); err != nil {
			return err
		}
		total += file.Bytes
		if total > MaxEvidenceBundleBytes {
			return fmt.Errorf("evidence bundle exceeds %d-byte limit", MaxEvidenceBundleBytes)
		}
		data, err := ReadRootedBounded(root, file.Path, MaxEvidenceBundleBytes)
		if err != nil {
			return err
		}
		review, err := exportboundary.ReviewPublicEvidence(data)
		if err != nil {
			return fmt.Errorf("bundle file %q: %w", file.Path, err)
		}
		if file.ExportDecision != review.Decision || !reflect.DeepEqual(file.RedactionStages, review.Stages) {
			return fmt.Errorf("bundle file %q export/redaction decision is not authoritative", file.Path)
		}
	}
	if len(b.Files) == 0 {
		return errors.New("evidence bundle has no files")
	}
	if isCanonicalCandidateProofSet(requiredProofIDs) {
		if err := validateCanonicalEvidence(root, b.Package, seen, b.FeatureIDs); err != nil {
			return err
		}
	}
	return nil
}

func BuildEvidenceBundle(root string, pkg PackageIdentity, requiredProofIDs []string, generatedAt time.Time) (EvidenceBundle, error) {
	if err := pkg.Validate(); err != nil {
		return EvidenceBundle{}, err
	}
	if generatedAt.IsZero() {
		return EvidenceBundle{}, errors.New("generatedAt is required")
	}
	proofIDs := append([]string(nil), requiredProofIDs...)
	sort.Strings(proofIDs)
	if len(proofIDs) == 0 {
		return EvidenceBundle{}, errors.New("at least one required proof ID is required")
	}
	for i := 1; i < len(proofIDs); i++ {
		if proofIDs[i] == proofIDs[i-1] {
			return EvidenceBundle{}, fmt.Errorf("duplicate required proof ID %q", proofIDs[i])
		}
	}

	var files []BundleFile
	var proofManifests []productevidence.Manifest
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("evidence path %q is a symlink", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("evidence path %q is not a regular file", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "bundle-manifest.json" || rel == "SHA256SUMS" {
			return fmt.Errorf("evidence root already contains generated file %q", rel)
		}
		data, err := ReadRootedBounded(root, filepath.FromSlash(rel), MaxEvidenceBundleBytes)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return fmt.Errorf("evidence file %q is empty", rel)
		}
		review, err := exportboundary.ReviewPublicEvidence(data)
		if err != nil {
			return fmt.Errorf("evidence file %q: %w", rel, err)
		}
		if isProofManifestPath(rel) {
			manifest, err := productevidence.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read proof manifest %q: %w", rel, err)
			}
			proofManifests = append(proofManifests, manifest)
		}
		digest, size, err := RootedFileSHA256(root, filepath.FromSlash(rel))
		if err != nil {
			return err
		}
		files = append(files, BundleFile{
			Path: rel, Kind: bundleFileKind(rel), SHA256: digest, Bytes: size,
			RedactionStatus: productevidence.RedactionPassed,
			ExportDecision:  review.Decision, RedactionStages: review.Stages,
		})
		return nil
	})
	if err != nil {
		return EvidenceBundle{}, err
	}
	if len(proofManifests) == 0 {
		return EvidenceBundle{}, errors.New("evidence bundle has no product proof manifests")
	}
	aggregate, err := productevidence.AggregateManifests(proofManifests...)
	if err != nil {
		return EvidenceBundle{}, err
	}
	if err := aggregate.RequirePassed(proofIDs...); err != nil {
		return EvidenceBundle{}, err
	}
	featureIDs := featureIDsFromProofManifests(proofManifests)
	for _, manifest := range proofManifests {
		if manifest.Dirty || manifest.Commit != pkg.SourceCommit {
			return EvidenceBundle{}, errors.New("proof manifest is dirty or bound to another source commit")
		}
		if manifest.PackageIdentity != nil && !packageIdentityMatchesProductEvidence(pkg, *manifest.PackageIdentity) {
			return EvidenceBundle{}, errors.New("proof manifest package identity does not match candidate package")
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	bundle := EvidenceBundle{
		Schema: EvidenceBundleSchema, GeneratedAt: generatedAt.UTC(), SourceCommit: pkg.SourceCommit,
		Package: pkg, RegistrySchema: productevidence.RegistrySchema, ProofIDs: proofIDs,
		FeatureIDs: featureIDs,
		Files:      files, RedactionStatus: productevidence.RedactionPassed,
	}
	if err := bundle.Validate(root, requiredProofIDs); err != nil {
		return EvidenceBundle{}, err
	}
	return bundle, nil
}

func WriteEvidenceBundle(root string, bundle EvidenceBundle) error {
	if err := bundle.Validate(root, bundle.ProofIDs); err != nil {
		return err
	}
	manifestPath := filepath.Join(root, "bundle-manifest.json")
	if err := WriteJSONAtomic(manifestPath, bundle, 0o600); err != nil {
		return err
	}
	paths := make([]string, 0, len(bundle.Files)+1)
	for _, file := range bundle.Files {
		paths = append(paths, file.Path)
	}
	paths = append(paths, "bundle-manifest.json")
	sort.Strings(paths)
	var lines []string
	for _, rel := range paths {
		digest, _, err := RootedFileSHA256(root, filepath.FromSlash(rel))
		if err != nil {
			return err
		}
		lines = append(lines, digest+"  "+rel)
	}
	return os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func ValidateEvidenceBundleRoot(root string, requiredProofIDs []string) (EvidenceBundle, error) {
	var bundle EvidenceBundle
	if err := ReadStrict(filepath.Join(root, "bundle-manifest.json"), MaxJSONBytes, &bundle); err != nil {
		return EvidenceBundle{}, err
	}
	if err := bundle.Validate(root, requiredProofIDs); err != nil {
		return EvidenceBundle{}, err
	}
	want := make([]string, 0, len(bundle.Files)+1)
	for _, file := range bundle.Files {
		want = append(want, file.Path)
	}
	want = append(want, "bundle-manifest.json")
	sort.Strings(want)
	data, err := ReadRootedBounded(root, "SHA256SUMS", MaxJSONBytes)
	if err != nil {
		return EvidenceBundle{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(want) {
		return EvidenceBundle{}, errors.New("evidence SHA256SUMS line count does not match bundle inventory")
	}
	for i, line := range lines {
		parts := strings.Split(line, "  ")
		if len(parts) != 2 || parts[1] != want[i] || !IsSHA256(parts[0]) {
			return EvidenceBundle{}, fmt.Errorf("invalid evidence SHA256SUMS line %q", line)
		}
		digest, _, err := RootedFileSHA256(root, filepath.FromSlash(parts[1]))
		if err != nil || digest != parts[0] {
			return EvidenceBundle{}, fmt.Errorf("evidence digest mismatch for %q", parts[1])
		}
	}
	return bundle, nil
}

func bundleFileKind(path string) string {
	switch {
	case strings.HasPrefix(path, "gates/"):
		return "gate"
	case strings.HasPrefix(path, "proofs/"):
		return "proof"
	case strings.HasPrefix(path, "logs/"):
		return "log"
	case strings.HasPrefix(path, "docs/"):
		return "docs-report"
	default:
		return "manifest"
	}
}

func isProofManifestPath(value string) bool {
	parts := strings.Split(value, "/")
	return len(parts) == 3 && parts[0] == "proofs" && parts[1] != "" && parts[2] == "manifest.json"
}

func isCanonicalCandidateProofSet(values []string) bool {
	want := productevidence.RequiredProofIDsForTarget(productevidence.RequiredForReleaseCandidate)
	got := append([]string(nil), values...)
	sort.Strings(want)
	sort.Strings(got)
	return strings.Join(want, "\x00") == strings.Join(got, "\x00")
}

func validateCanonicalEvidence(
	root string,
	pkg PackageIdentity,
	actual map[string]string,
	featureIDs []string,
) error {
	expected := make(map[string]bool, len(canonicalEvidenceCore))
	for rel, kind := range canonicalEvidenceCore {
		if actual[rel] != kind {
			return fmt.Errorf("canonical evidence file %q is missing or has the wrong kind", rel)
		}
		expected[rel] = true
	}

	var proofManifests []productevidence.Manifest
	for rel := range actual {
		if !isProofManifestPath(rel) {
			continue
		}
		manifest, err := productevidence.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("read canonical proof manifest %q: %w", rel, err)
		}
		proofManifests = append(proofManifests, manifest)
		expected[rel] = true
		base := pathpkg.Dir(rel)
		for _, proof := range manifest.Proofs {
			for _, artifact := range proof.Artifacts {
				if artifact.SHA256 == "" {
					return fmt.Errorf("proof artifact %q has no digest", artifact.Path)
				}
				artifactRel := pathpkg.Clean(pathpkg.Join(base, artifact.Path))
				if !strings.HasPrefix(artifactRel, base+"/") {
					return fmt.Errorf("proof artifact %q escapes %q", artifact.Path, base)
				}
				digest, size, err := RootedFileSHA256(root, filepath.FromSlash(artifactRel))
				if err != nil || size <= 0 || digest != artifact.SHA256 {
					return fmt.Errorf("proof artifact %q is missing, empty, or digest-invalid", artifactRel)
				}
				expected[artifactRel] = true
			}
		}
	}
	if len(proofManifests) == 0 {
		return errors.New("canonical evidence has no proof manifests")
	}
	if len(featureIDs) > 0 {
		observed := featureIDsFromProofManifests(proofManifests)
		if strings.Join(featureIDs, "\x00") != strings.Join(observed, "\x00") {
			return errors.New("canonical evidence feature IDs do not match proof manifests")
		}
	}
	for rel := range actual {
		if !expected[rel] {
			return fmt.Errorf("canonical evidence contains undeclared file %q", rel)
		}
	}

	var identity PackageIdentity
	if err := ReadStrict(filepath.Join(root, "candidate-identity.json"), MaxJSONBytes, &identity); err != nil || !identity.Equal(pkg) {
		return errors.New("candidate identity does not match evidence bundle package")
	}
	manifestPath := filepath.Join(root, "package", "package-manifest.json")
	manifest, err := packagekit.LoadManifestForDistribution(manifestPath)
	if err != nil {
		return fmt.Errorf("read evidence package manifest: %w", err)
	}
	if err := packageManifestMatchesIdentity(manifest, pkg); err != nil {
		return err
	}
	manifestDigest, _, err := FileSHA256(manifestPath)
	if err != nil {
		return err
	}
	var verification PackageVerificationObservation
	if err := ReadStrict(filepath.Join(root, "package", "verify.json"), MaxJSONBytes, &verification); err != nil {
		return fmt.Errorf("read package verification observation: %w", err)
	}
	if err := verification.Validate(manifest, pkg, manifestDigest); err != nil {
		return err
	}
	var provenance RuntimeBuildProvenance
	if err := ReadStrict(filepath.Join(root, "runtime", "build-provenance.json"), MaxJSONBytes, &provenance); err != nil {
		return fmt.Errorf("read runtime build provenance: %w", err)
	}
	if err := provenance.Validate(manifest); err != nil {
		return err
	}
	for _, proofManifest := range proofManifests {
		for _, proof := range proofManifest.Proofs {
			if proof.Runtime == nil {
				continue
			}
			if proof.Runtime.Family != manifest.Runtime.Family || proof.Runtime.Revision != manifest.Runtime.Revision ||
				proof.Runtime.ArtifactSHA256 != provenance.Output.SHA256 || proof.Runtime.BuildCommit != provenance.Source.Commit ||
				proof.Runtime.BuildDirty {
				return fmt.Errorf("proof %q runtime binding does not match build provenance", proof.ProofID)
			}
		}
	}
	gate2, err := readCanonicalGate(filepath.Join(root, "gates", "gate2.json"), "gate2-lima", manifest, provenance)
	if err != nil {
		return err
	}
	gate3, err := readCanonicalGate(filepath.Join(root, "gates", "gate3.json"), "gate3-hidden-proxy", manifest, provenance)
	if err != nil {
		return err
	}
	if gate2.Runtime.EnvironmentID == gate3.Runtime.EnvironmentID {
		return errors.New("Gate 2 and Gate 3 evidence must use distinct environment IDs")
	}
	var signing SigningObservation
	if err := ReadStrict(filepath.Join(root, "signing", "observation.json"), MaxJSONBytes, &signing); err != nil {
		return err
	}
	if err := signing.Validate(true); err != nil || signing.PackageManifestSHA256 != manifestDigest {
		return errors.New("signing observation does not match evidence package manifest")
	}
	var notarization NotarizationObservation
	if err := ReadStrict(filepath.Join(root, "notarization", "observation.json"), MaxJSONBytes, &notarization); err != nil {
		return err
	}
	if err := notarization.Validate(true); err != nil || notarization.PackageManifestSHA256 != manifestDigest {
		return errors.New("notarization observation does not match evidence package manifest")
	}
	var registry productevidence.ProofRegistryView
	if err := ReadStrict(filepath.Join(root, "proof-registry.json"), MaxJSONBytes, &registry); err != nil {
		return err
	}
	canonicalRegistry, err := productevidence.RegistryView()
	if err != nil || !reflect.DeepEqual(registry, canonicalRegistry) {
		return errors.New("evidence proof registry is not the authoritative registry")
	}
	return nil
}

func featureIDsFromProofManifests(manifests []productevidence.Manifest) []string {
	seen := make(map[string]struct{})
	for _, manifest := range manifests {
		for _, proof := range manifest.Proofs {
			seen[proof.FeatureID] = struct{}{}
		}
	}
	featureIDs := make([]string, 0, len(seen))
	for featureID := range seen {
		featureIDs = append(featureIDs, featureID)
	}
	sort.Strings(featureIDs)
	return featureIDs
}

func validateFeatureIDs(featureIDs []string) error {
	for index, featureID := range featureIDs {
		if !validFeatureID(featureID) {
			return fmt.Errorf("invalid feature ID %q", featureID)
		}
		if index > 0 && featureIDs[index-1] >= featureID {
			return errors.New("feature IDs must be sorted and unique")
		}
	}
	return nil
}

func validFeatureID(value string) bool {
	if len(value) < 5 || len(value) > 128 || value[3] != '-' {
		return false
	}
	for index := 0; index < 3; index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	previousHyphen := true
	for index := 4; index < len(value); index++ {
		char := value[index]
		if char == '-' {
			if previousHyphen {
				return false
			}
			previousHyphen = true
			continue
		}
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
		previousHyphen = false
	}
	return !previousHyphen
}

type canonicalEvidenceGate struct {
	ID              string                         `json:"id"`
	Backend         string                         `json:"backend"`
	Result          string                         `json:"result"`
	Reason          string                         `json:"reason"`
	BoundarySummary string                         `json:"boundarySummary"`
	EnvironmentName string                         `json:"environmentName"`
	Runtime         productevidence.RuntimeBinding `json:"runtime"`
}

func readCanonicalGate(path, id string, manifest packagekit.Manifest, provenance RuntimeBuildProvenance) (canonicalEvidenceGate, error) {
	var gate canonicalEvidenceGate
	if err := ReadStrict(path, MaxJSONBytes, &gate); err != nil {
		return canonicalEvidenceGate{}, fmt.Errorf("read %s evidence: %w", id, err)
	}
	if gate.ID != id || gate.Backend != "lima" || gate.Result != "passed" || gate.Reason != "" {
		return canonicalEvidenceGate{}, fmt.Errorf("%s evidence did not pass the Lima gate", id)
	}
	if err := gate.Runtime.Validate(); err != nil {
		return canonicalEvidenceGate{}, fmt.Errorf("%s runtime binding: %w", id, err)
	}
	if gate.Runtime.Family != manifest.Runtime.Family || gate.Runtime.Revision != manifest.Runtime.Revision ||
		gate.Runtime.ArtifactSHA256 != provenance.Output.SHA256 || gate.Runtime.BuildCommit != provenance.Source.Commit ||
		gate.Runtime.BuildDirty {
		return canonicalEvidenceGate{}, fmt.Errorf("%s runtime binding does not match build provenance", id)
	}
	return gate, nil
}

func packageIdentityMatchesProductEvidence(actual PackageIdentity, proof productevidence.PackageIdentity) bool {
	return actual.Name == proof.Name && actual.ProductVersion == proof.ProductVersion &&
		actual.SourceCommit == proof.SourceCommit && actual.ArtifactSHA256 == proof.ArtifactSHA256 &&
		actual.HostOS == proof.HostOS && actual.HostArch == proof.HostArch
}
