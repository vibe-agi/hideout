package productevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxReleaseArtifactBytes int64 = 64 << 20

const (
	EvalSatisfied              = "satisfied"
	EvalMissing                = "missing"
	EvalFailed                 = "failed"
	EvalNotRun                 = "not-run"
	EvalStale                  = "stale"
	EvalRedactionFailed        = "redaction-failed"
	EvalArtifactMissing        = "artifact-missing"
	EvalArtifactDigestMismatch = "artifact-digest-mismatch"
	EvalArtifactInvalid        = "artifact-invalid"
	EvalRuntimeMismatch        = "runtime-mismatch"
	EvalProofShapeMismatch     = "proof-shape-mismatch"
	EvalNotRequired            = "not-required"
)

type EvaluationOptions struct {
	Requirements    []ProofRequirement
	Target          string
	ExpectedCommit  string
	ExpectedPackage *PackageIdentity
	ArtifactRoot    string
	ArtifactRoots   map[string]string
	ExpectedRuntime *RuntimeExpectation
}

type EvaluationReport struct {
	Target        string
	Results       []ProofEvaluationResult
	StatusCounts  map[string]int
	ExtraProofIDs []string
}

type ProofEvaluationResult struct {
	FeatureID   string   `json:"featureId"`
	ProofID     string   `json:"proofId"`
	ClaimIDs    []string `json:"claimIds"`
	Layer       string   `json:"layer"`
	RequiredFor string   `json:"requiredFor"`
	Status      string   `json:"status"`
	Summary     string   `json:"summary"`
}

type proofFreshnessIdentity struct {
	commit          string
	dirty           bool
	packageIdentity *PackageIdentity
}

type proofIdentity struct {
	featureID string
	proofID   string
}

func EvaluateManifests(manifests []Manifest, opts EvaluationOptions) (EvaluationReport, error) {
	reqs := opts.Requirements
	if len(reqs) == 0 {
		reqs = ProductHardeningRequirements()
	}
	reqs = append([]ProofRequirement(nil), reqs...)
	sortRequirements(reqs)
	if err := ValidateRegistry(reqs); err != nil {
		return EvaluationReport{}, err
	}
	agg, err := AggregateManifests(manifests...)
	if err != nil {
		return EvaluationReport{}, err
	}
	identities := proofFreshnessIdentities(manifests)
	proofs := proofsByIdentity(agg)
	report := EvaluationReport{
		Target:       strings.TrimSpace(opts.Target),
		StatusCounts: map[string]int{},
	}
	if report.Target == "" {
		report.Target = RequiredForTargetedCompletion
	}
	requiredProofs := map[proofIdentity]struct{}{}
	var runtimeAnchor *RuntimeBinding
	for _, req := range reqs {
		key := proofIdentity{featureID: req.FeatureID, proofID: req.ProofID}
		requiredProofs[key] = struct{}{}
		proof := proofs[key]
		result := evaluateRequirementForTarget(req, report.Target, proof, identities[key], opts)
		if result.Status == EvalSatisfied && req.RuntimePolicy == RuntimePolicyExactReal {
			binding := proof.Runtime
			if runtimeAnchor == nil {
				copy := *binding
				runtimeAnchor = &copy
			} else if !runtimeAnchor.SameArtifactBuild(*binding) {
				result.Status = EvalRuntimeMismatch
				result.Summary = "runtime artifact and candidate evidence does not match the other required real proofs"
			}
		}
		report.Results = append(report.Results, result)
		report.StatusCounts[result.Status]++
	}
	for _, proof := range agg.Proofs {
		key := proofIdentity{featureID: proof.FeatureID, proofID: proof.ProofID}
		if _, ok := requiredProofs[key]; !ok {
			report.ExtraProofIDs = append(report.ExtraProofIDs, proof.ProofID)
		}
	}
	sort.Strings(report.ExtraProofIDs)
	return report, nil
}

func evaluateRequirementForTarget(req ProofRequirement, target string, proof ProofEntry, identity proofFreshnessIdentity, opts EvaluationOptions) ProofEvaluationResult {
	if !requirementAppliesToTarget(req, target) {
		return ProofEvaluationResult{
			FeatureID:   req.FeatureID,
			ProofID:     req.ProofID,
			ClaimIDs:    append([]string(nil), req.ClaimIDs...),
			Layer:       req.Layer,
			RequiredFor: req.RequiredFor,
			Status:      EvalNotRequired,
			Summary:     "proof is not required for target=" + target,
		}
	}
	return evaluateRequirement(req, proof, identity, opts, target == RequiredForReleaseCandidate || target == RequiredForPublicRelease)
}

func requirementAppliesToTarget(req ProofRequirement, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		target = RequiredForTargetedCompletion
	}
	if target == RequiredForPublicRelease {
		return req.RequiredFor == RequiredForReleaseCandidate || req.RequiredFor == RequiredForPublicRelease
	}
	return req.RequiredFor == target
}

func proofsByIdentity(agg Aggregate) map[proofIdentity]ProofEntry {
	out := make(map[proofIdentity]ProofEntry, len(agg.Proofs))
	for _, proof := range agg.Proofs {
		out[proofIdentity{featureID: proof.FeatureID, proofID: proof.ProofID}] = proof
	}
	return out
}

func proofFreshnessIdentities(manifests []Manifest) map[proofIdentity]proofFreshnessIdentity {
	out := make(map[proofIdentity]proofFreshnessIdentity)
	for _, manifest := range manifests {
		identity := proofFreshnessIdentity{commit: manifest.Commit, dirty: manifest.Dirty}
		if manifest.PackageIdentity != nil {
			packageIdentity := *manifest.PackageIdentity
			identity.packageIdentity = &packageIdentity
		}
		for _, proof := range manifest.Proofs {
			out[proofIdentity{featureID: proof.FeatureID, proofID: proof.ProofID}] = identity
		}
	}
	return out
}

func EvaluateManifest(manifest Manifest, opts EvaluationOptions) (EvaluationReport, error) {
	return EvaluateManifests([]Manifest{manifest}, opts)
}

func evaluateRequirement(req ProofRequirement, proof ProofEntry, identity proofFreshnessIdentity, opts EvaluationOptions, releaseCandidate bool) ProofEvaluationResult {
	result := ProofEvaluationResult{
		FeatureID:   req.FeatureID,
		ProofID:     req.ProofID,
		ClaimIDs:    append([]string(nil), req.ClaimIDs...),
		Layer:       req.Layer,
		RequiredFor: req.RequiredFor,
	}
	if proof.ProofID == "" {
		result.Status = EvalMissing
		result.Summary = "registered proof is missing"
		return result
	}
	if proof.FeatureID != req.FeatureID || proof.ProofID != req.ProofID {
		result.Status = EvalFailed
		result.Summary = "proof featureId/proofId identity does not match the registry requirement"
		return result
	}
	if missing := missingRequiredClaimIDs(req.ClaimIDs, proof.CoveredClaims); len(missing) > 0 {
		result.Status = EvalFailed
		result.Summary = fmt.Sprintf("proof is missing registered claimIds %v", missing)
		return result
	}
	if proof.Status == StatusNotRun {
		result.Status = EvalNotRun
		result.Summary = "proof was not run"
		return result
	}
	if proof.RedactionStatus != RedactionPassed {
		result.Status = EvalRedactionFailed
		result.Summary = fmt.Sprintf("redactionStatus=%s", proof.RedactionStatus)
		return result
	}
	if proof.Status != StatusPassed {
		result.Status = EvalFailed
		result.Summary = fmt.Sprintf("status=%s", proof.Status)
		return result
	}
	if req.RuntimePolicy == RuntimePolicyExactReal {
		if runtimeProofIsStale(identity, opts) {
			result.Status = EvalStale
			result.Summary = "real runtime proof is not bound to the trusted clean candidate and package identity"
			return result
		}
	} else if isStale(req, identity, opts) {
		result.Status = EvalStale
		result.Summary = staleSummary(req)
		return result
	}
	if status, summary := evaluateRuntimeBinding(req, proof, opts.ExpectedRuntime); status != EvalSatisfied {
		result.Status = status
		result.Summary = summary
		return result
	}
	if req.RequiredMode != "" && proof.Mode != req.RequiredMode {
		result.Status = EvalProofShapeMismatch
		result.Summary = fmt.Sprintf("proof mode=%s, required=%s", proof.Mode, req.RequiredMode)
		return result
	}
	if req.RequiredEvidenceClass != "" && proof.EvidenceClass != req.RequiredEvidenceClass {
		result.Status = EvalProofShapeMismatch
		result.Summary = fmt.Sprintf("proof evidenceClass=%s, required=%s", proof.EvidenceClass, req.RequiredEvidenceClass)
		return result
	}
	artifactRoot := opts.ArtifactRoot
	if root := strings.TrimSpace(opts.ArtifactRoots[req.ProofID]); root != "" {
		artifactRoot = root
	}
	if status, summary := evaluateArtifacts(req, proof, artifactRoot, releaseCandidate, opts.ExpectedCommit, opts.ExpectedPackage, opts.ExpectedRuntime); status != EvalSatisfied {
		result.Status = status
		result.Summary = summary
		return result
	}
	result.Status = EvalSatisfied
	result.Summary = "proof satisfied"
	return result
}

func missingRequiredClaimIDs(required []string, covered []CoveredClaim) []string {
	seen := make(map[string]struct{}, len(covered))
	for _, claim := range covered {
		seen[claim.ClaimID] = struct{}{}
	}
	var missing []string
	for _, claimID := range required {
		if _, ok := seen[claimID]; !ok {
			missing = append(missing, claimID)
		}
	}
	sort.Strings(missing)
	return missing
}

func evaluateRuntimeBinding(req ProofRequirement, proof ProofEntry, expected *RuntimeExpectation) (string, string) {
	if req.RuntimePolicy == RuntimePolicyNone {
		return EvalSatisfied, ""
	}
	if req.RuntimePolicy != RuntimePolicyExactReal {
		return EvalRuntimeMismatch, "unsupported runtime policy=" + req.RuntimePolicy
	}
	if proof.Mode != "real-gate" || proof.Runtime == nil {
		return EvalRuntimeMismatch, "exact real runtime binding is required"
	}
	if expected == nil {
		return EvalRuntimeMismatch, "trusted runtime expectation is required"
	}
	if err := expected.Validate(); err != nil {
		return EvalRuntimeMismatch, err.Error()
	}
	if err := proof.Runtime.Matches(*expected); err != nil {
		return EvalRuntimeMismatch, err.Error()
	}
	return EvalSatisfied, ""
}

func runtimeProofIsStale(identity proofFreshnessIdentity, opts EvaluationOptions) bool {
	if opts.ExpectedRuntime == nil || opts.ExpectedRuntime.Validate() != nil || identity.dirty {
		return true
	}
	packageCommit := strings.TrimSpace(opts.ExpectedCommit)
	if opts.ExpectedPackage == nil || opts.ExpectedPackage.ValidateCandidateCommit(packageCommit) != nil {
		return true
	}
	if !IsCanonicalCommit(identity.commit) || identity.commit != packageCommit {
		return true
	}
	if identity.packageIdentity == nil || identity.packageIdentity.ValidateCandidateCommit(packageCommit) != nil {
		return true
	}
	return packageStale(identity.packageIdentity, opts.ExpectedPackage)
}

func isStale(req ProofRequirement, identity proofFreshnessIdentity, opts EvaluationOptions) bool {
	switch req.FreshnessPolicy {
	case FreshnessSameCommit:
		return strings.TrimSpace(opts.ExpectedCommit) != "" && identity.commit != strings.TrimSpace(opts.ExpectedCommit)
	case FreshnessSamePackage:
		return packageStale(identity.packageIdentity, opts.ExpectedPackage)
	case FreshnessSameCommitAndPackage:
		commitStale := strings.TrimSpace(opts.ExpectedCommit) != "" && identity.commit != strings.TrimSpace(opts.ExpectedCommit)
		return commitStale || packageStale(identity.packageIdentity, opts.ExpectedPackage)
	default:
		return false
	}
}

func packageStale(actual, expected *PackageIdentity) bool {
	if expected == nil || strings.TrimSpace(expected.Name) == "" {
		return false
	}
	if actual == nil {
		return true
	}
	if strings.TrimSpace(actual.Name) != strings.TrimSpace(expected.Name) {
		return true
	}
	return actual.ProductVersion != expected.ProductVersion ||
		actual.SourceCommit != expected.SourceCommit ||
		actual.ArtifactSHA256 != expected.ArtifactSHA256 ||
		actual.HostOS != expected.HostOS || actual.HostArch != expected.HostArch
}

func staleSummary(req ProofRequirement) string {
	return "proof is stale for freshnessPolicy=" + req.FreshnessPolicy
}

func evaluateArtifacts(req ProofRequirement, proof ProofEntry, root string, releaseCandidate bool, expectedCommit string, expectedPackage *PackageIdentity, expectedRuntime *RuntimeExpectation) (string, string) {
	switch req.ArtifactPolicy {
	case ArtifactPolicyNone:
		return EvalSatisfied, ""
	case ArtifactPolicyExists, ArtifactPolicyExistsAndDigestIfSupplied:
	default:
		return EvalFailed, "unsupported artifactPolicy=" + req.ArtifactPolicy
	}
	if len(proof.Artifacts) == 0 {
		return EvalArtifactMissing, "required artifact refs are missing"
	}
	if strings.TrimSpace(root) == "" {
		return EvalArtifactMissing, "artifact root is required"
	}
	artifactRoot, err := os.OpenRoot(root)
	if err != nil {
		return EvalArtifactMissing, fmt.Sprintf("artifact root: %v", err)
	}
	defer artifactRoot.Close()
	artifactData := make(map[string][]byte, len(proof.Artifacts))
	for _, artifact := range proof.Artifacts {
		path, err := rootedArtifactPath(artifactRoot, artifact.Path)
		if err != nil {
			return EvalArtifactMissing, fmt.Sprintf("artifact %s: %v", artifact.Path, err)
		}
		requireDigest := req.RuntimePolicy == RuntimePolicyExactReal
		if requireDigest && artifact.SHA256 == "" {
			return EvalArtifactDigestMismatch, fmt.Sprintf("artifact %s requires an authoritative sha256", artifact.Path)
		}
		if artifact.RedactionStatus != RedactionPassed {
			return EvalRedactionFailed, fmt.Sprintf("artifact %s redactionStatus=%s", artifact.Path, artifact.RedactionStatus)
		}
		verifyDigest := req.ArtifactPolicy == ArtifactPolicyExistsAndDigestIfSupplied && artifact.SHA256 != ""
		if releaseCandidate || verifyDigest || req.ArtifactValidator != ArtifactValidatorNone {
			data, err := readBoundedArtifact(artifactRoot, path)
			if err != nil {
				return EvalArtifactMissing, fmt.Sprintf("artifact %s: %v", artifact.Path, err)
			}
			if releaseCandidate && ContainsControlPlaneBytes(data) {
				return EvalRedactionFailed, fmt.Sprintf("artifact %s contains unredacted control-plane material", artifact.Path)
			}
			if verifyDigest {
				sum := sha256.Sum256(data)
				digest := hex.EncodeToString(sum[:])
				if digest != artifact.SHA256 {
					return EvalArtifactDigestMismatch, fmt.Sprintf("artifact %s digest mismatch", artifact.Path)
				}
			}
			artifactData[artifact.Path] = data
		}
	}
	if err := validateRegisteredArtifact(req.ArtifactValidator, proof.Artifacts, artifactData, strings.TrimSpace(expectedCommit), expectedPackage, expectedRuntime); err != nil {
		return EvalArtifactInvalid, err.Error()
	}
	return EvalSatisfied, ""
}

func rootedArtifactPath(root *os.Root, rel string) (string, error) {
	clean := filepath.Clean(rel)
	if !filepath.IsLocal(clean) {
		return "", errors.New("artifact path must stay inside the evidence root")
	}
	parts := strings.Split(clean, string(filepath.Separator))
	current := ""
	for i, part := range parts {
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		info, err := root.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("artifact path component %q is a symbolic link", current)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("artifact path component %q is not a directory", current)
		}
	}
	return clean, nil
}

func readBoundedArtifact(root *os.Root, path string) ([]byte, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxReleaseArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxReleaseArtifactBytes {
		return nil, fmt.Errorf("exceeds %d-byte authoritative scan limit", maxReleaseArtifactBytes)
	}
	return data, nil
}

func (r EvaluationReport) Satisfied() bool {
	for _, result := range r.Results {
		if result.Status != EvalSatisfied && result.Status != EvalNotRequired {
			return false
		}
	}
	return true
}

func (r EvaluationReport) RequireSatisfied() error {
	if r.Satisfied() {
		return nil
	}
	var parts []string
	for _, result := range r.Results {
		if result.Status == EvalSatisfied || result.Status == EvalNotRequired {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s/%s=%s(%s)", result.FeatureID, result.ProofID, result.Status, result.Summary))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return errors.New("proof evaluation failed")
	}
	return fmt.Errorf("unsatisfied proof requirements: %s", strings.Join(parts, "; "))
}
