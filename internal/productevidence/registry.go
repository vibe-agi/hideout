package productevidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
)

const RegistrySchema = "hideout.proof-registry/v1"

const (
	LayerUnit             = "unit"
	LayerGate0            = "gate0"
	LayerProductHardening = "product-hardening"
	LayerRealGate         = "real-gate"
	LayerReleaseCandidate = "release-candidate"

	RequiredForLocalDogfood       = "local-dogfood"
	RequiredForTargetedCompletion = "targeted-completion"
	RequiredForReleaseCandidate   = "release-candidate"
	RequiredForSupportingOnly     = "supporting-only"

	FreshnessNone                 = "none"
	FreshnessSameCommit           = "same-commit"
	FreshnessSamePackage          = "same-package"
	FreshnessSameCommitAndPackage = "same-commit-and-package"

	ArtifactPolicyNone                      = "none"
	ArtifactPolicyExists                    = "exists"
	ArtifactPolicyExistsAndDigestIfSupplied = "exists-and-digest-if-supplied"

	RuntimePolicyNone      = "none"
	RuntimePolicyExactReal = "exact-real"
)

var validRequirementLayers = []string{
	LayerUnit,
	LayerGate0,
	LayerProductHardening,
	LayerRealGate,
	LayerReleaseCandidate,
}

var validRequiredFor = []string{
	RequiredForLocalDogfood,
	RequiredForTargetedCompletion,
	RequiredForReleaseCandidate,
	RequiredForSupportingOnly,
}

var validFreshnessPolicies = []string{
	FreshnessNone,
	FreshnessSameCommit,
	FreshnessSamePackage,
	FreshnessSameCommitAndPackage,
}

var validArtifactPolicies = []string{
	ArtifactPolicyNone,
	ArtifactPolicyExists,
	ArtifactPolicyExistsAndDigestIfSupplied,
}

var validRuntimePolicies = []string{RuntimePolicyNone, RuntimePolicyExactReal}

type ProofRequirement struct {
	FeatureID             string   `json:"featureId"`
	ProofID               string   `json:"proofId"`
	Layer                 string   `json:"layer"`
	RequiredFor           string   `json:"requiredFor"`
	FreshnessPolicy       string   `json:"freshnessPolicy"`
	ClaimIDs              []string `json:"claimIds"`
	ArtifactPolicy        string   `json:"artifactPolicy"`
	RuntimePolicy         string   `json:"runtimePolicy"`
	RequiredMode          string   `json:"requiredMode,omitempty"`
	RequiredEvidenceClass string   `json:"requiredEvidenceClass,omitempty"`
}

type ProofRegistryView struct {
	Schema       string             `json:"schema"`
	Requirements []ProofRequirement `json:"requirements"`
}

func ProductHardeningRequirements() []ProofRequirement {
	rows := []ProofRequirement{
		req(Feature021, Proof021EvidenceSchema, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, Claim021EvidenceSchema.ClaimID),
		req(Feature021, Proof021DocsBoundary, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "021.FR-016"),
		req(Feature021, Proof021WebUIBrowserConsole, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, Claim021BrowserConsole.ClaimID),
		req(Feature021, Proof021WebUIBrowserLive, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "021.FR-002"),
		req(Feature021, Proof021WebUIBrowserNoticeAck, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "021.FR-008"),
		req(Feature021, Proof021WebUIBrowserAuth, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "021.FR-010"),
		req(Feature021, Proof021TUIPTYConsole, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "021.FR-004"),
		req(Feature021, Proof021TUIPTYLive, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "021.FR-004"),
		req(Feature021, Proof021TUIPTYNoPolling, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "021.FR-004"),
		req(Feature021, Proof021TUIPTYFallback, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "021.FR-004"),

		req(Feature022, Proof022LocalFastInstall, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommitAndPackage, ArtifactPolicyNone, Claim022LocalFast.ClaimID),
		req(Feature022, Proof022LocalFastVerify, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommitAndPackage, ArtifactPolicyNone, Claim022LocalFast.ClaimID),
		req(Feature022, Proof022LocalFastInit, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommitAndPackage, ArtifactPolicyNone, Claim022LocalFast.ClaimID, Claim022SingleInit.ClaimID),
		req(Feature022, Proof022LocalFastRun, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommitAndPackage, ArtifactPolicyNone, Claim022LocalFast.ClaimID),
		req(Feature022, Proof022LocalFastAuditBoundary, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommitAndPackage, ArtifactPolicyNone, Claim022AuditBoundary.ClaimID),
		req(Feature022, Proof022DocsOrder, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim022DocsOrder.ClaimID),
		req(Feature022, Proof022FailureFixtures, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim022FailureFixtures.ClaimID),

		req(Feature023, Proof023LocalFastLifecycle, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim023Lifecycle.ClaimID, Claim023Conflict.ClaimID, Claim023RealBoundary.ClaimID, Claim023Coverage.ClaimID),
		req(Feature023, Proof023LocalFastClaimRace, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim023ClaimRace.ClaimID),
		req(Feature023, Proof023LocalFastTimeout, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim023DecisionOutcomes.ClaimID),
		req(Feature023, Proof023LocalFastVisibility, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim023Visibility.ClaimID),
		req(Feature023, Proof023LocalFastRedaction, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim023Redaction.ClaimID),

		req(Feature024, Proof024PackageRepairLoop, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommitAndPackage, ArtifactPolicyNone, Claim024PackageRepair.ClaimID, Claim024PackagePreservation.ClaimID, Claim024LocalOnly.ClaimID),
		req(Feature024, Proof024DoctorSafeFixLoop, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim024DoctorSafeFix.ClaimID, Claim024LocalOnly.ClaimID),
		req(Feature024, Proof024DoctorGuidance, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim024DoctorGuidance.ClaimID, Claim024LocalOnly.ClaimID),
		req(Feature024, Proof024Redaction, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim024Redaction.ClaimID, Claim024DoctorExport.ClaimID),

		req(Feature025, Proof025ClaimBoundaries, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim025ClaimBoundaries.ClaimID),
		req(Feature025, Proof025OverclaimScan, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim025OverclaimScan.ClaimID),
		req(Feature025, Proof025CommandExamples, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim025CommandExamples.ClaimID),
		req(Feature025, Proof025CrossDoc, LayerProductHardening, RequiredForLocalDogfood, FreshnessSameCommit, ArtifactPolicyNone, Claim025CrossDoc.ClaimID),

		req(Feature029, Proof029UnitPolicy, LayerUnit, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "029.FR-001", "029.FR-003", "029.FR-004", "029.FR-005", "029.FR-006"),
		req(Feature029, Proof029UnitTypedErrno, LayerUnit, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "029.FR-007", "029.FR-008", "029.FR-009"),
		req(Feature029, Proof029LocalDecisionLifecycle, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "029.FR-014", "029.FR-015", "029.FR-016", "029.FR-017", "029.FR-018", "029.FR-019", "029.FR-020"),
		req(Feature029, Proof029LocalRedaction, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "029.FR-024", "029.FR-025", "029.FR-026"),
		req(Feature029, Proof029DocsClaimBoundary, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "029.FR-032", "029.SC-014"),
		req(Feature029, Proof029RealGate2Namespace, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "029.SC-001", "029.SC-002", "029.SC-003", "029.SC-004"),
		req(Feature029, Proof029RealGate2LiveGrant, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "029.SC-005", "029.SC-006", "029.SC-007", "029.SC-008"),
		req(Feature029, Proof029RealGate2NotRun, LayerRealGate, RequiredForSupportingOnly, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "029.FR-027"),

		req(Feature030, Proof030Gate0Mechanics, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "030.FR-001", "030.FR-002", "030.FR-003", "030.FR-004", "030.FR-005", "030.FR-006", "030.FR-007", "030.FR-008", "030.FR-009", "030.FR-011", "030.FR-018", "030.FR-019"),
		req(Feature030, Proof030DocsClaimBoundary, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "030.FR-013", "030.FR-017"),
		req(Feature030, Proof030RealGate2CodeOpen, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "030.SC-001", "030.SC-002", "030.SC-004"),
		req(Feature030, Proof030RealGate2PrivacyChannels, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "030.FR-014", "030.FR-015", "030.FR-016", "030.SC-005"),
		req(Feature030, Proof030RealGate2TrustedGrant, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "030.FR-010", "030.FR-012", "030.SC-006"),
		req(Feature030, Proof030RealGate2NotRun, LayerRealGate, RequiredForSupportingOnly, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "030.SC-008"),

		req(Feature031, Proof031Gate0Mechanics, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "031.FR-001", "031.FR-002", "031.FR-003", "031.FR-004", "031.FR-005", "031.FR-006", "031.FR-010", "031.SC-002", "031.SC-013"),
		runtimeReq(Feature031, Proof031RealImage, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "031.FR-001", "031.FR-002", "031.FR-019", "031.SC-004", "031.SC-012"),
		runtimeReq(Feature031, Proof031Baseline, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "031.FR-007", "031.FR-008", "031.FR-012", "031.SC-003", "031.SC-005"),
		runtimeReq(Feature031, Proof031AgentInstall, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "031.FR-013", "031.FR-016", "031.SC-001", "031.SC-006"),
		runtimeReq(Feature031, Proof031AgentPrivacy, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "031.FR-014", "031.FR-017", "031.SC-008", "031.SC-009"),
		req(Feature031, Proof031ReadinessParity, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "031.FR-008", "031.FR-009", "031.FR-010", "031.FR-011", "031.FR-015", "031.SC-005", "031.SC-007", "031.SC-010"),
		runtimeReq(Feature031, Proof031BoundaryRegression, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "031.FR-018", "031.FR-020", "031.SC-008", "031.SC-011"),
		req(Feature031, Proof031DocsClaimBoundary, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "031.FR-011", "031.FR-016", "031.FR-018", "031.SC-013"),

		req(Feature032, Proof032Gate0Lifecycle, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "032.FR-001", "032.FR-002", "032.FR-003", "032.FR-004", "032.FR-005", "032.FR-006", "032.FR-007", "032.FR-008", "032.FR-009", "032.FR-025", "032.FR-026", "032.FR-027", "032.FR-031", "032.FR-032"),
		req(Feature032, Proof032Gate0Binding, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "032.FR-010", "032.FR-011", "032.FR-018", "032.FR-019", "032.FR-020", "032.FR-021", "032.FR-022", "032.FR-023", "032.FR-024", "032.FR-028", "032.FR-029"),
		req(Feature032, Proof032Gate0IdentitySafety, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "032.FR-012", "032.FR-013", "032.FR-014", "032.FR-015", "032.FR-016", "032.FR-017", "032.FR-030"),
		evidenceClassReq(Feature032, Proof032RealGate2External, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "host-app-pack-external-real-gate2", "032.FR-033", "032.SC-001", "032.SC-002", "032.SC-003", "032.SC-004", "032.SC-005", "032.SC-006", "032.SC-007", "032.SC-008", "032.SC-009", "032.SC-010", "032.SC-011", "032.SC-012", "032.SC-013", "032.SC-014"),
	}
	sortRequirements(rows)
	return rows
}

func req(featureID, proofID, layer, requiredFor, freshness, artifact string, claimIDs ...string) ProofRequirement {
	r := ProofRequirement{
		FeatureID:       featureID,
		ProofID:         proofID,
		Layer:           layer,
		RequiredFor:     requiredFor,
		FreshnessPolicy: freshness,
		ClaimIDs:        compactStrings(claimIDs),
		ArtifactPolicy:  artifact,
		RuntimePolicy:   RuntimePolicyNone,
	}
	if layer == LayerRealGate {
		r.RequiredMode = "real-gate"
	}
	return r
}

func evidenceClassReq(featureID, proofID, layer, requiredFor, freshness, artifact, evidenceClass string, claimIDs ...string) ProofRequirement {
	r := req(featureID, proofID, layer, requiredFor, freshness, artifact, claimIDs...)
	r.RequiredEvidenceClass = evidenceClass
	return r
}

func runtimeReq(featureID, proofID, layer, requiredFor, freshness, artifact string, claimIDs ...string) ProofRequirement {
	r := req(featureID, proofID, layer, requiredFor, freshness, artifact, claimIDs...)
	r.RuntimePolicy = RuntimePolicyExactReal
	return r
}

func RegistryView() (ProofRegistryView, error) {
	view := ProofRegistryView{Schema: RegistrySchema, Requirements: ProductHardeningRequirements()}
	if err := ValidateRegistry(view.Requirements); err != nil {
		return ProofRegistryView{}, err
	}
	return view, nil
}

func WriteRegistryJSON(w io.Writer) error {
	view, err := RegistryView()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(view)
}

func RegistryJSON() ([]byte, error) {
	view, err := RegistryView()
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(view, "", "  ")
}

func ValidateRegistry(reqs []ProofRequirement) error {
	if len(reqs) == 0 {
		return errors.New("proof registry requires at least one requirement")
	}
	seen := map[string]struct{}{}
	for i, req := range reqs {
		if err := req.Validate(); err != nil {
			return fmt.Errorf("requirements[%d]: %w", i, err)
		}
		if _, exists := seen[req.ProofID]; exists {
			return fmt.Errorf("duplicate proofId %q", req.ProofID)
		}
		seen[req.ProofID] = struct{}{}
	}
	return nil
}

func (r ProofRequirement) Validate() error {
	if strings.TrimSpace(r.FeatureID) == "" {
		return errors.New("featureId is required")
	}
	if strings.TrimSpace(r.ProofID) == "" {
		return errors.New("proofId is required")
	}
	if !slices.Contains(validRequirementLayers, r.Layer) {
		return fmt.Errorf("unsupported layer %q", r.Layer)
	}
	if !slices.Contains(validRequiredFor, r.RequiredFor) {
		return fmt.Errorf("unsupported requiredFor %q", r.RequiredFor)
	}
	if !slices.Contains(validFreshnessPolicies, r.FreshnessPolicy) {
		return fmt.Errorf("unsupported freshnessPolicy %q", r.FreshnessPolicy)
	}
	if !slices.Contains(validArtifactPolicies, r.ArtifactPolicy) {
		return fmt.Errorf("unsupported artifactPolicy %q", r.ArtifactPolicy)
	}
	if !slices.Contains(validRuntimePolicies, r.RuntimePolicy) {
		return fmt.Errorf("unsupported runtimePolicy %q", r.RuntimePolicy)
	}
	if r.RequiredMode != "" && !slices.Contains(validModes, r.RequiredMode) {
		return fmt.Errorf("unsupported requiredMode %q", r.RequiredMode)
	}
	if r.Layer == LayerRealGate && r.RequiredMode != "real-gate" {
		return errors.New("real-gate requirements must require real-gate mode")
	}
	if strings.ContainsAny(r.RequiredEvidenceClass, "\x00\r\n") || len(r.RequiredEvidenceClass) > 128 {
		return errors.New("requiredEvidenceClass is invalid")
	}
	if len(r.ClaimIDs) == 0 {
		return errors.New("claimIds is required")
	}
	for _, claimID := range r.ClaimIDs {
		if strings.TrimSpace(claimID) == "" {
			return errors.New("claimIds must not contain empty values")
		}
	}
	return nil
}

func RequirementsForFeature(featureID string) []ProofRequirement {
	var out []ProofRequirement
	for _, req := range ProductHardeningRequirements() {
		if req.FeatureID == featureID {
			out = append(out, req)
		}
	}
	return out
}

func RequiredProofIDsForFeature(featureID string) []string {
	reqs := RequirementsForFeature(featureID)
	out := make([]string, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, req.ProofID)
	}
	return out
}

func RequirementsForFeatures(featureIDs ...string) []ProofRequirement {
	wanted := map[string]struct{}{}
	for _, id := range featureIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	var out []ProofRequirement
	for _, req := range ProductHardeningRequirements() {
		if _, ok := wanted[req.FeatureID]; ok {
			out = append(out, req)
		}
	}
	return out
}

func sortRequirements(reqs []ProofRequirement) {
	sort.Slice(reqs, func(i, j int) bool {
		if reqs[i].FeatureID != reqs[j].FeatureID {
			return reqs[i].FeatureID < reqs[j].FeatureID
		}
		return reqs[i].ProofID < reqs[j].ProofID
	})
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
