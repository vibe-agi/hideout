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
	Feature033                  = "033-public-alpha-release-channel"
	Proof033PackageIdentity     = "033.release.package-identity"
	Proof033SigningNotarization = "033.release.signing-notarization"
	Proof033CleanInstall        = "033.release.clean-install"
	Proof033RealGateBinding     = "033.release.real-gate-binding"
	Proof033DocsCandidateTruth  = "033.release.docs-candidate-truth"
	Proof033PublicDownload      = "033.release.public-download"
	Proof033DocsPublicTruth     = "033.release.docs-public-truth"
)

const (
	LayerUnit             = "unit"
	LayerGate0            = "gate0"
	LayerProductHardening = "product-hardening"
	LayerRealGate         = "real-gate"
	LayerReleaseCandidate = "release-candidate"

	RequiredForLocalDogfood       = "local-dogfood"
	RequiredForTargetedCompletion = "targeted-completion"
	RequiredForReleaseCandidate   = "release-candidate"
	RequiredForPublicRelease      = "public-release"
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

	ArtifactValidatorNone                           = ""
	ArtifactValidatorConcurrentIsolationV1          = "concurrent-sessions-isolation/v1"
	ArtifactValidatorConcurrentPerformanceV2        = "concurrent-sessions-performance/v2"
	ArtifactValidatorLifecycleLocalV1               = "resource-lifecycle-local/v1"
	ArtifactValidatorLifecycleModelV1               = "resource-lifecycle-model/v1"
	ArtifactValidatorLifecycleRealV1                = "resource-lifecycle-real/v1"
	ArtifactValidatorLifecyclePerformanceV1         = "resource-lifecycle-performance/v1"
	ArtifactValidatorAttachReservationPerformanceV1 = "attach-reservation-performance/v1"
	ArtifactValidatorSharedWorkspaceBehaviorV1      = "shared-workspace-behavior/v1"
	ArtifactValidatorSharedWorkspacePerformanceV1   = "shared-workspace-performance/v1"
	ArtifactValidatorWorkspaceExecutableV1          = "workspace-executable/v1"
	ArtifactValidatorDisposableRecoveryV1           = "disposable-recovery/v1"
	ArtifactValidatorProjectionReadinessV1          = "projection-readiness/v1"
	ArtifactValidatorProjectionPrivacyV1            = "projection-privacy/v1"
	ArtifactValidatorReleaseClosureV1               = "release-closure/v1"
	ArtifactValidatorMigrationLimaV1                = "migration-lima/v1"
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
	RequiredForPublicRelease,
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

var validArtifactValidators = []string{
	ArtifactValidatorNone,
	ArtifactValidatorConcurrentIsolationV1,
	ArtifactValidatorConcurrentPerformanceV2,
	ArtifactValidatorLifecycleLocalV1,
	ArtifactValidatorLifecycleModelV1,
	ArtifactValidatorLifecycleRealV1,
	ArtifactValidatorLifecyclePerformanceV1,
	ArtifactValidatorAttachReservationPerformanceV1,
	ArtifactValidatorSharedWorkspaceBehaviorV1,
	ArtifactValidatorSharedWorkspacePerformanceV1,
	ArtifactValidatorWorkspaceExecutableV1,
	ArtifactValidatorDisposableRecoveryV1,
	ArtifactValidatorProjectionReadinessV1,
	ArtifactValidatorProjectionPrivacyV1,
	ArtifactValidatorReleaseClosureV1,
	ArtifactValidatorMigrationLimaV1,
}

type ProofRequirement struct {
	FeatureID             string   `json:"featureId"`
	ProofID               string   `json:"proofId"`
	Layer                 string   `json:"layer"`
	RequiredFor           string   `json:"requiredFor"`
	FreshnessPolicy       string   `json:"freshnessPolicy"`
	ClaimIDs              []string `json:"claimIds"`
	ArtifactPolicy        string   `json:"artifactPolicy"`
	RuntimePolicy         string   `json:"runtimePolicy"`
	ArtifactValidator     string   `json:"artifactValidator,omitempty"`
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
		runtimeEvidenceClassValidatorReq(Feature030, Proof030RealGate2CodeOpen, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "projection-readiness-real-gate2", ArtifactValidatorProjectionReadinessV1, "030.SC-001", "030.SC-002", "030.SC-004"),
		runtimeEvidenceClassValidatorReq(Feature030, Proof030RealGate2PrivacyChannels, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "projection-privacy-real-gate3", ArtifactValidatorProjectionPrivacyV1, "030.FR-014", "030.FR-015", "030.FR-016", "030.SC-005"),
		runtimeEvidenceClassValidatorReq(Feature030, Proof030RealGate2TrustedGrant, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "projection-readiness-real-gate2", ArtifactValidatorProjectionReadinessV1, "030.FR-010", "030.FR-012", "030.SC-006"),
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
		runtimeEvidenceClassValidatorReq(Feature032, Proof032RealGate2External, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "projection-readiness-real-gate2", ArtifactValidatorProjectionReadinessV1, "032.FR-033", "032.SC-001", "032.SC-002", "032.SC-003", "032.SC-004", "032.SC-005", "032.SC-006", "032.SC-007", "032.SC-008", "032.SC-009", "032.SC-010", "032.SC-011", "032.SC-012", "032.SC-013", "032.SC-014"),

		req(Feature033, Proof033PackageIdentity, LayerReleaseCandidate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "033.FR-001", "033.FR-003", "033.FR-005", "033.FR-006"),
		req(Feature033, Proof033SigningNotarization, LayerReleaseCandidate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "033.FR-010", "033.FR-011", "033.SC-003"),
		req(Feature033, Proof033CleanInstall, LayerReleaseCandidate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "033.FR-008", "033.FR-009", "033.FR-022", "033.FR-023"),
		runtimeReq(Feature033, Proof033RealGateBinding, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "033.FR-018", "033.FR-024", "033.SC-006", "033.SC-007"),
		req(Feature033, Proof033DocsCandidateTruth, LayerReleaseCandidate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "033.FR-027", "033.FR-039", "033.SC-018"),
		req(Feature033, Proof033PublicDownload, LayerReleaseCandidate, RequiredForPublicRelease, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "033.FR-020", "033.SC-001", "033.SC-008"),
		req(Feature033, Proof033DocsPublicTruth, LayerReleaseCandidate, RequiredForPublicRelease, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "033.FR-027", "033.FR-039", "033.SC-011", "033.SC-018"),

		req(Feature034, Proof034Gate0Mechanics, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"034.FR-001", "034.FR-002", "034.FR-003", "034.FR-004", "034.FR-005", "034.FR-006", "034.FR-007", "034.FR-010", "034.FR-011", "034.FR-012", "034.FR-014", "034.FR-015", "034.FR-016", "034.FR-019", "034.FR-020", "034.FR-021", "034.FR-022", "034.FR-023", "034.FR-025", "034.SC-015"),
		runtimeEvidenceClassValidatorReq(Feature034, Proof034RealIsolation, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "concurrent-sessions-real-gate2", ArtifactValidatorConcurrentIsolationV1,
			"034.FR-007", "034.FR-008", "034.FR-010", "034.FR-011", "034.FR-012", "034.FR-013", "034.FR-014", "034.FR-015", "034.FR-016", "034.FR-017", "034.FR-018", "034.FR-019", "034.FR-020", "034.FR-021", "034.FR-022", "034.SC-001", "034.SC-002", "034.SC-003", "034.SC-005", "034.SC-006", "034.SC-007", "034.SC-008", "034.SC-009", "034.SC-010", "034.SC-011", "034.SC-012", "034.SC-013"),
		runtimeEvidenceClassValidatorReq(Feature034, Proof034RealPerformance, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "concurrent-sessions-performance-real-gate2", ArtifactValidatorConcurrentPerformanceV2, "034.FR-009", "034.SC-004"),
		req(Feature034, Proof034RealGate2NotRun, LayerRealGate, RequiredForSupportingOnly, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "034.SC-001", "034.SC-012"),
		req(Feature034, Proof034DocsClaimBoundary, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "034.FR-017", "034.FR-024", "034.SC-014"),

		req(Feature035, Proof035Gate0Mechanics, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"035.FR-001", "035.FR-002", "035.FR-003", "035.FR-004", "035.FR-005", "035.FR-006", "035.FR-009", "035.FR-012", "035.FR-013", "035.FR-014", "035.FR-015", "035.FR-016", "035.FR-017", "035.FR-018", "035.FR-020", "035.FR-021", "035.FR-023", "035.FR-024", "035.FR-025", "035.FR-026", "035.FR-029", "035.FR-031", "035.FR-032", "035.FR-033", "035.FR-035", "035.FR-036", "035.FR-038", "035.FR-039", "035.FR-040", "035.FR-041",
			"035.SC-012", "035.SC-013", "035.SC-014", "035.SC-015", "035.SC-017", "035.SC-019", "035.SC-020", "035.SC-021", "035.SC-023"),
		runtimeEvidenceClassValidatorReq(Feature035, Proof035RealBehavior, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "shared-workspace-real-gate2", ArtifactValidatorSharedWorkspaceBehaviorV1,
			"035.FR-001", "035.FR-003", "035.FR-004", "035.FR-005", "035.FR-007", "035.FR-008", "035.FR-009", "035.FR-010", "035.FR-011", "035.FR-012", "035.FR-013", "035.FR-014", "035.FR-015", "035.FR-016", "035.FR-017", "035.FR-018", "035.FR-019", "035.FR-020", "035.FR-021", "035.FR-022", "035.FR-023", "035.FR-024", "035.FR-025", "035.FR-027", "035.FR-029", "035.FR-030", "035.FR-031", "035.FR-032", "035.FR-033", "035.FR-034", "035.FR-035", "035.FR-037", "035.FR-039", "035.FR-040", "035.FR-041",
			"035.SC-001", "035.SC-002", "035.SC-003", "035.SC-004", "035.SC-006", "035.SC-007", "035.SC-008", "035.SC-009", "035.SC-010", "035.SC-011", "035.SC-012", "035.SC-013", "035.SC-014", "035.SC-016", "035.SC-017", "035.SC-018", "035.SC-019", "035.SC-020", "035.SC-021", "035.SC-022", "035.SC-023"),
		runtimeEvidenceClassValidatorReq(Feature035, Proof035RealPerformance, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "shared-workspace-performance-real-gate2", ArtifactValidatorSharedWorkspacePerformanceV1,
			"035.FR-006", "035.FR-022", "035.FR-028", "035.FR-032", "035.SC-005"),
		req(Feature035, Proof035RealGate2NotRun, LayerRealGate, RequiredForSupportingOnly, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"035.FR-028", "035.SC-001", "035.SC-005"),
		req(Feature035, Proof035DocsClaimBoundary, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone,
			"035.FR-018", "035.FR-019", "035.FR-025", "035.FR-027", "035.FR-034", "035.FR-040", "035.SC-012", "035.SC-018", "035.SC-022"),

		validatorReq(Feature036, Proof036Gate0Mechanics, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, ArtifactValidatorLifecycleLocalV1,
			"036.FR-001", "036.FR-002", "036.FR-003", "036.FR-004", "036.FR-005", "036.FR-006", "036.FR-007", "036.FR-008", "036.FR-009", "036.FR-010", "036.FR-011", "036.FR-012", "036.FR-013", "036.FR-014", "036.FR-015", "036.FR-016", "036.FR-017", "036.FR-018", "036.FR-019", "036.FR-020", "036.FR-021", "036.FR-022", "036.FR-023", "036.FR-024", "036.FR-025", "036.FR-026", "036.FR-027", "036.FR-028", "036.FR-029", "036.FR-030", "036.FR-031", "036.SC-011", "036.SC-012", "036.SC-013", "036.SC-018", "036.SC-019", "036.SC-020", "036.SC-021"),
		validatorReq(Feature036, Proof036Gate0Model, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, ArtifactValidatorLifecycleModelV1,
			"036.FR-005", "036.FR-006", "036.FR-010", "036.FR-011", "036.FR-012", "036.FR-013", "036.FR-015", "036.FR-017", "036.FR-020", "036.FR-023", "036.FR-028", "036.SC-001", "036.SC-009", "036.SC-016"),
		runtimeEvidenceClassValidatorReq(Feature036, Proof036RealLifecycle, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "resource-lifecycle-real-gate2", ArtifactValidatorLifecycleRealV1,
			"036.FR-002", "036.FR-005", "036.FR-006", "036.FR-007", "036.FR-008", "036.FR-009", "036.FR-010", "036.FR-011", "036.FR-012", "036.FR-013", "036.FR-014", "036.FR-015", "036.FR-017", "036.FR-018", "036.FR-019", "036.FR-022", "036.FR-023", "036.FR-024", "036.FR-025", "036.FR-026", "036.FR-027", "036.FR-028", "036.FR-029", "036.FR-030", "036.SC-002", "036.SC-003", "036.SC-004", "036.SC-005", "036.SC-006", "036.SC-007", "036.SC-008", "036.SC-009", "036.SC-011", "036.SC-012", "036.SC-014", "036.SC-015", "036.SC-016", "036.SC-017", "036.SC-019", "036.SC-020"),
		runtimeEvidenceClassValidatorReq(Feature036, Proof036RealPerformance, LayerRealGate, RequiredForSupportingOnly, FreshnessNone, ArtifactPolicyExistsAndDigestIfSupplied, "resource-lifecycle-performance-real-gate2", ArtifactValidatorLifecyclePerformanceV1,
			"036.SC-010"),
		req(Feature036, Proof036RealGate2NotRun, LayerRealGate, RequiredForSupportingOnly, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, "036.SC-002", "036.SC-003"),
		req(Feature036, Proof036DocsClaimBoundary, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "036.FR-003", "036.FR-008", "036.FR-014", "036.FR-019", "036.FR-021", "036.FR-024"),

		req(Feature038, Proof038IntentPlanParity, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"038.FR-001", "038.FR-002", "038.FR-008", "038.FR-010", "038.FR-030", "038.FR-031", "038.SC-005", "038.SC-015"),
		req(Feature038, Proof038CancelDriftReadonly, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"038.FR-005", "038.FR-006", "038.FR-007", "038.FR-014", "038.FR-015", "038.FR-016", "038.SC-003", "038.SC-004"),
		req(Feature038, Proof038DaemonRecovery, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"038.FR-029", "038.FR-032", "038.FR-035", "038.SC-010", "038.SC-012"),
		req(Feature038, Proof038PackagePTY, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied,
			"038.FR-001", "038.FR-003", "038.FR-004", "038.FR-005", "038.FR-009", "038.FR-012", "038.FR-013", "038.FR-017", "038.FR-018", "038.FR-019", "038.FR-020", "038.FR-021", "038.FR-026", "038.FR-027", "038.SC-001", "038.SC-002", "038.SC-009", "038.SC-013"),
		runtimeReq(Feature038, Proof038RealFirstRun, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied,
			"038.FR-022", "038.FR-023", "038.FR-024", "038.FR-025", "038.SC-006", "038.SC-007", "038.SC-008", "038.SC-016"),
		runtimeReq(Feature038, Proof038RealAgentInstallRun, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied,
			"038.FR-033", "038.FR-034", "038.SC-014"),
		req(Feature038, Proof038RealGate2NotRun, LayerRealGate, RequiredForSupportingOnly, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"038.FR-024", "038.FR-028", "038.SC-006", "038.SC-011"),
		req(Feature038, Proof038DocsTruth, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone,
			"038.FR-028", "038.SC-013"),

		runtimeEvidenceClassValidatorReq(Feature039, Proof039RealPersistentGrant, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "projection-readiness-real-gate2", ArtifactValidatorProjectionReadinessV1,
			"039.FR-001", "039.FR-002", "039.FR-003", "039.FR-004", "039.FR-005", "039.FR-006", "039.FR-007", "039.FR-008", "039.FR-009", "039.FR-010", "039.FR-011",
			"039.SC-001", "039.SC-002", "039.SC-003", "039.SC-004", "039.SC-005", "039.SC-006"),
		req(Feature039, Proof039RealGate2NotRun, LayerRealGate, RequiredForSupportingOnly, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"039.SC-001", "039.SC-002", "039.SC-003"),

		req(Feature040, Proof040Gate0Mechanics, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"040.FR-001", "040.FR-002", "040.FR-003", "040.FR-004", "040.FR-005", "040.FR-006", "040.FR-007", "040.FR-008", "040.FR-010", "040.FR-011", "040.FR-012", "040.FR-013", "040.FR-014", "040.FR-015", "040.SC-001", "040.SC-002", "040.SC-003", "040.SC-004", "040.SC-007"),
		req(Feature040, Proof040Gate0Model, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"040.FR-001", "040.FR-002", "040.FR-003", "040.FR-005", "040.FR-006", "040.FR-007", "040.FR-008", "040.FR-009", "040.FR-010", "040.FR-011", "040.SC-001", "040.SC-002", "040.SC-003", "040.SC-004"),
		runtimeEvidenceClassReq(Feature040, Proof040RealLifecycle, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "attach-reservation-real-gate2",
			"040.FR-001", "040.FR-002", "040.FR-003", "040.FR-004", "040.FR-005", "040.FR-006", "040.FR-007", "040.FR-008", "040.FR-009", "040.FR-010", "040.FR-011", "040.FR-012", "040.FR-013", "040.SC-001", "040.SC-003", "040.SC-004", "040.SC-006", "040.SC-007"),
		runtimeEvidenceClassValidatorReq(Feature040, Proof040RealPerformance, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "attach-reservation-performance-real-gate2", ArtifactValidatorAttachReservationPerformanceV1,
			"040.FR-011", "040.FR-012", "040.SC-005"),
		req(Feature040, Proof040RealGate2NotRun, LayerRealGate, RequiredForSupportingOnly, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"040.SC-005", "040.SC-006"),
		req(Feature040, Proof040DocsClaimBoundary, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone,
			"040.FR-012", "040.FR-013", "040.FR-014", "040.FR-015", "040.SC-006"),

		req(Feature041, Proof041Gate0Mechanics, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"041.FR-002", "041.FR-003", "041.FR-004", "041.FR-007", "041.FR-009", "041.FR-015", "041.FR-016", "041.SC-005", "041.SC-007"),
		runtimeEvidenceClassValidatorReq(Feature041, Proof041RealExecution, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "workspace-executable-real-gate2", ArtifactValidatorWorkspaceExecutableV1,
			"041.FR-001", "041.FR-002", "041.FR-003", "041.FR-004", "041.FR-005", "041.FR-006", "041.FR-007", "041.FR-008", "041.FR-009", "041.FR-012", "041.FR-013", "041.FR-014", "041.FR-015", "041.FR-016", "041.SC-001", "041.SC-002", "041.SC-003", "041.SC-004", "041.SC-005", "041.SC-006", "041.SC-007"),
		req(Feature041, Proof041RealGate2NotRun, LayerRealGate, RequiredForSupportingOnly, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"041.SC-002", "041.SC-003", "041.SC-004", "041.SC-005", "041.SC-006"),
		req(Feature041, Proof041DocsClaimBoundary, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone,
			"041.FR-010", "041.FR-011", "041.FR-013", "041.FR-014", "041.FR-016", "041.SC-007"),

		req(Feature042, Proof042Gate0Mechanics, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"042.FR-001", "042.FR-002", "042.FR-003", "042.FR-004", "042.FR-005", "042.FR-006", "042.FR-007", "042.FR-008",
			"042.FR-009", "042.FR-010", "042.FR-011", "042.FR-012", "042.FR-013", "042.FR-014", "042.FR-015", "042.FR-016",
			"042.FR-017", "042.FR-018", "042.FR-019", "042.FR-020", "042.FR-021", "042.FR-022", "042.FR-023", "042.FR-024",
			"042.SC-002", "042.SC-003", "042.SC-004", "042.SC-006", "042.SC-007", "042.SC-008"),
		validatorReq(Feature042, Proof042Gate0Model, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied, ArtifactValidatorLifecycleModelV1,
			"042.FR-003", "042.FR-006", "042.FR-007", "042.FR-009", "042.FR-010", "042.FR-011", "042.FR-017",
			"042.SC-002", "042.SC-003", "042.SC-006"),
		runtimeEvidenceClassValidatorReq(Feature042, Proof042RealRecovery, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "disposable-recovery-real-gate2", ArtifactValidatorDisposableRecoveryV1,
			"042.FR-001", "042.FR-002", "042.FR-003", "042.FR-004", "042.FR-005", "042.FR-006", "042.FR-007", "042.FR-008",
			"042.FR-009", "042.FR-010", "042.FR-011", "042.FR-012", "042.FR-013", "042.FR-014", "042.FR-015", "042.FR-016",
			"042.FR-017", "042.FR-018", "042.FR-019", "042.FR-020", "042.FR-021", "042.FR-022", "042.FR-023", "042.FR-024",
			"042.SC-001", "042.SC-002", "042.SC-003", "042.SC-004", "042.SC-005", "042.SC-006", "042.SC-007", "042.SC-008"),
		req(Feature042, Proof042RealGate2NotRun, LayerRealGate, RequiredForSupportingOnly, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"042.SC-001", "042.SC-003", "042.SC-005"),
		req(Feature042, Proof042DocsClaimBoundary, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone,
			"042.FR-001", "042.FR-018", "042.FR-020", "042.FR-023", "042.FR-024", "042.SC-007", "042.SC-008"),

		req(Feature043, Proof043Gate0Mechanics, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"043.FR-001", "043.FR-002", "043.FR-003", "043.FR-004", "043.FR-005", "043.FR-006", "043.FR-007", "043.FR-008", "043.FR-009", "043.FR-010", "043.FR-011", "043.FR-012", "043.FR-013", "043.FR-014", "043.FR-015", "043.FR-016", "043.FR-017", "043.FR-018", "043.FR-024", "043.FR-025"),
		runtimeEvidenceClassValidatorReq(Feature043, Proof043RealReadiness, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "projection-readiness-real-gate2", ArtifactValidatorProjectionReadinessV1,
			"043.FR-019", "043.FR-020", "043.FR-021", "043.FR-023", "043.SC-001", "043.SC-002", "043.SC-003", "043.SC-004", "043.SC-007", "043.SC-008"),
		runtimeEvidenceClassValidatorReq(Feature043, Proof043RealPrivacy, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "projection-privacy-real-gate3", ArtifactValidatorProjectionPrivacyV1,
			"043.FR-022"),
		req(Feature043, Proof043RealGate2NotRun, LayerRealGate, RequiredForSupportingOnly, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"043.FR-023", "043.SC-007"),
		req(Feature043, Proof043DocsClaimBoundary, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone,
			"043.FR-026", "043.SC-009"),

		req(Feature044, Proof044Gate0Journeys, LayerGate0, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone,
			"044.FR-002", "044.FR-003", "044.FR-005", "044.FR-006", "044.FR-007", "044.FR-008", "044.FR-009", "044.FR-010",
			"044.FR-011", "044.FR-012", "044.FR-013", "044.FR-014", "044.FR-015", "044.FR-016", "044.FR-017", "044.FR-018",
			"044.FR-019", "044.FR-020", "044.FR-021", "044.FR-025", "044.FR-026", "044.FR-027", "044.FR-029", "044.FR-034",
			"044.FR-035", "044.SC-003", "044.SC-004", "044.SC-005", "044.SC-006", "044.SC-007", "044.SC-009", "044.SC-010",
			"044.SC-012"),
		runtimeReq(Feature044, Proof044RealGate2FirstRun, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied,
			"044.FR-001", "044.FR-002", "044.FR-004", "044.FR-028", "044.FR-030", "044.SC-001", "044.SC-002", "044.SC-011", "044.SC-014"),
		runtimeReq(Feature044, Proof044RealGate3Privacy, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied,
			"044.FR-019", "044.FR-021", "044.FR-022", "044.FR-023", "044.FR-024", "044.FR-028", "044.FR-030", "044.SC-008", "044.SC-009", "044.SC-011"),
		req(Feature044, Proof044PackageUI, LayerReleaseCandidate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied,
			"044.FR-030", "044.SC-011"),
		req(Feature044, Proof044ReleaseCandidate, LayerReleaseCandidate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied,
			"044.FR-028", "044.FR-030", "044.FR-031", "044.FR-033", "044.SC-011", "044.SC-014"),
		req(Feature044, Proof044DocsTruth, LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone,
			"044.FR-001", "044.FR-024", "044.FR-025", "044.FR-027", "044.FR-032", "044.FR-033", "044.SC-013"),
		req(Feature044, Proof044PublicReceipt, LayerReleaseCandidate, RequiredForPublicRelease, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied,
			"044.FR-031", "044.FR-032", "044.SC-011", "044.SC-013"),

		evidenceClassValidatorReq(Feature045, Proof045ReleaseClosure, LayerReleaseCandidate, RequiredForReleaseCandidate, FreshnessSameCommit, ArtifactPolicyExistsAndDigestIfSupplied,
			"operator-console-final-closure", ArtifactValidatorReleaseClosureV1,
			"045.SC-015"),

		evidenceClassValidatorReq(Feature046, Proof046RealMigration, LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied,
			"portable-migration-real-lima", ArtifactValidatorMigrationLimaV1,
			"046.SC-001", "046.SC-002", "046.SC-004", "046.SC-007", "046.SC-008", "046.SC-014", "046.SC-015", "046.SC-016"),
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

func validatorReq(featureID, proofID, layer, requiredFor, freshness, artifact, validator string, claimIDs ...string) ProofRequirement {
	r := req(featureID, proofID, layer, requiredFor, freshness, artifact, claimIDs...)
	r.ArtifactValidator = validator
	return r
}

func evidenceClassValidatorReq(featureID, proofID, layer, requiredFor, freshness, artifact, evidenceClass, validator string, claimIDs ...string) ProofRequirement {
	r := validatorReq(featureID, proofID, layer, requiredFor, freshness, artifact, validator, claimIDs...)
	r.RequiredEvidenceClass = evidenceClass
	return r
}

func runtimeReq(featureID, proofID, layer, requiredFor, freshness, artifact string, claimIDs ...string) ProofRequirement {
	r := req(featureID, proofID, layer, requiredFor, freshness, artifact, claimIDs...)
	r.RuntimePolicy = RuntimePolicyExactReal
	return r
}

func runtimeEvidenceClassReq(featureID, proofID, layer, requiredFor, freshness, artifact, evidenceClass string, claimIDs ...string) ProofRequirement {
	r := runtimeReq(featureID, proofID, layer, requiredFor, freshness, artifact, claimIDs...)
	r.RequiredEvidenceClass = evidenceClass
	return r
}

func runtimeEvidenceClassValidatorReq(featureID, proofID, layer, requiredFor, freshness, artifact, evidenceClass, validator string, claimIDs ...string) ProofRequirement {
	r := runtimeEvidenceClassReq(featureID, proofID, layer, requiredFor, freshness, artifact, evidenceClass, claimIDs...)
	r.ArtifactValidator = validator
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
	if !slices.Contains(validArtifactValidators, r.ArtifactValidator) {
		return fmt.Errorf("unsupported artifactValidator %q", r.ArtifactValidator)
	}
	if r.ArtifactValidator != ArtifactValidatorNone && r.ArtifactPolicy == ArtifactPolicyNone {
		return errors.New("artifactValidator requires an artifact policy")
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

func RequirementsForTarget(target string) []ProofRequirement {
	var out []ProofRequirement
	for _, req := range ProductHardeningRequirements() {
		if requirementAppliesToTarget(req, target) {
			out = append(out, req)
		}
	}
	return out
}

func RequiredProofIDsForTarget(target string) []string {
	reqs := RequirementsForTarget(target)
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
