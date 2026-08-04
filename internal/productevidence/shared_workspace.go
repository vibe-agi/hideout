package productevidence

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/packagekit"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const (
	Feature035 = "035-shared-default-vm-cross-workspace"

	Proof035Gate0Mechanics    = "035.shared-workspace.gate0.mechanics"
	Proof035RealBehavior      = "035.shared-workspace.real-gate2.behavior"
	Proof035RealPerformance   = "035.shared-workspace.real-gate2.performance"
	Proof035RealGate2NotRun   = "035.shared-workspace.real-gate2.not-run"
	Proof035DocsClaimBoundary = "035.shared-workspace.docs.claim-boundary"

	sharedWorkspaceResearchDecisionSHA256 = "8ab8aa0ff609214514ecff1267777094fee8ebff648b6d9d0f4cd01f682ee5b8"
)

var requiredSharedWorkspaceBehaviorChecks = []string{
	"oneMachineTwoProjects",
	"disjointIsolation",
	"sameRootLocks",
	"nestedAuthority",
	"siblingDetach",
	"lifecycleIntegration",
	"restartNoReadoption",
	"packagedHelperVerified",
	"hostPathRedacted",
}

var requiredSharedWorkspaceBehaviorV2Checks = append(
	append([]string(nil), requiredSharedWorkspaceBehaviorChecks...),
	"logicalPhysicalAlias",
	"projectStateSeparated",
	"siblingPhysicalRootDenied",
	"pathJudgeNegativeFixture",
)

var requiredSharedWorkspacePathChecks = []string{
	"productionWorkspaceIdentity",
	"logicalPhysicalSameObject",
	"logicalWritePhysicalRead",
	"physicalWriteLogicalRead",
	"atomicRenameAcrossAliases",
	"modeAcrossAliases",
	"flushAcrossAliases",
	"deleteAcrossAliases",
	"repeatedDeleteAcrossAliases",
	"logicalPwdStable",
	"physicalCwdOpaque",
	"nestedCdStable",
	"subprocessCwdOpaque",
	"distinctRootProjectState",
	"sameRootProjectStateStable",
	"siblingPhysicalRootDenied",
	"goLogicalPwdAliasClassified",
	"boundedGitSafeDirectories",
	"preserveModeSharedRejected",
	"externalGitMetadataRejected",
	"resolvedFileAuditLogical",
	"relativeFileAliasExplicit",
	"processAuditLogical",
	"processCwdUnavailableExplicit",
	"physicalArgvCaptureLimitationExplicit",
	"siblingArgvFailClosed",
	"physicalPathAbsentFromActivity",
}

var sharedWorkspacePathLimitations = []string{
	"process-cwd-unavailable",
	"physical-workspace-argv-exceeds-kernel-capture-width",
	"relative-workspace-file-path-alias",
}

var sharedWorkspacePathTools = []string{
	"bash", "claude", "codex", "git", "go", "node", "python",
}

var requiredSharedWorkspaceRelationChecks = []string{
	"oneEnvironment",
	"oneInstance",
	"sameBootAcrossDisjointRoots",
	"sameBootAcrossNestedRoots",
	"disjointBidirectionalUnavailable",
	"siblingDetachPreservedExecution",
	"sameRootLockOwnersIndependent",
	"nestedAuthorityEnforced",
}

var requiredSharedWorkspaceLifecycleChecks = []string{
	"siblingDetachPreservedExecution",
	"bridgePinnedMachine",
	"graceCancelledByCrossWorkspaceAttach",
	"graceCancelReusedBoot",
	"exactFinalStopObserved",
	"restartDidNotReadoptWorkspaceAuthority",
	"postReconciliationAttachUsedFreshAuthority",
}

var sharedWorkspaceCandidateMetrics = []string{
	"git-status",
	"package-scan",
	"atomic-host-to-guest",
	"atomic-guest-to-host",
	"mount-ready",
	"first-byte",
}

var sharedWorkspaceFilesystemControlMetrics = []string{
	"git-status",
	"package-scan",
}

var sharedWorkspaceResearchBaselineMetrics = []string{
	"first-byte",
}

var sharedWorkspaceCandidateRawPaths = map[string]string{
	"git-status":           "artifacts/performance/candidate/git-status.values",
	"package-scan":         "artifacts/performance/candidate/package-scan.values",
	"atomic-host-to-guest": "artifacts/performance/candidate/atomic-host-to-guest.values",
	"atomic-guest-to-host": "artifacts/performance/candidate/atomic-guest-to-host.values",
	"mount-ready":          "artifacts/performance/candidate/mount-ready.values",
	"first-byte":           "artifacts/performance/candidate/first-byte.values",
	"saturation-metadata":  "artifacts/performance/candidate/saturation-metadata.values",
}

var sharedWorkspaceFilesystemControlRawPaths = map[string]string{
	"git-status":   "artifacts/performance/filesystem-control/git-status.values",
	"package-scan": "artifacts/performance/filesystem-control/package-scan.values",
}

var sharedWorkspaceResearchBaselineRawPaths = map[string]string{
	"first-byte": "artifacts/performance/research-baseline/first-byte.values",
}

const sharedWorkspacePairedSamplesPath = "artifacts/performance/filesystem-control/paired-samples.tsv"

type sharedWorkspaceArtifactDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type sharedWorkspaceBehaviorEvidence struct {
	Schema          string                                   `json:"schema"`
	Status          string                                   `json:"status"`
	Commit          string                                   `json:"commit"`
	Dirty           bool                                     `json:"dirty"`
	Backend         string                                   `json:"backend"`
	HostOS          string                                   `json:"hostOS"`
	HostArch        string                                   `json:"hostArch"`
	Transport       string                                   `json:"transport"`
	PackageIdentity PackageIdentity                          `json:"packageIdentity"`
	Runtime         RuntimeBinding                           `json:"runtime"`
	Artifacts       map[string]sharedWorkspaceArtifactDigest `json:"artifacts"`
	Checks          map[string]bool                          `json:"checks"`
}

type sharedWorkspaceRelationsEvidence struct {
	Schema           string          `json:"schema"`
	Status           string          `json:"status"`
	EnvironmentCount int             `json:"environmentCount"`
	InstanceCount    int             `json:"instanceCount"`
	Checks           map[string]bool `json:"checks"`
}

type sharedWorkspaceLifecycleEvidence struct {
	Schema            string          `json:"schema"`
	Status            string          `json:"status"`
	FirstGeneration   int64           `json:"firstGeneration"`
	RestartGeneration int64           `json:"restartGeneration"`
	ElapsedSeconds    float64         `json:"elapsedSeconds"`
	Checks            map[string]bool `json:"checks"`
}

type sharedWorkspaceCorrectnessEvidence struct {
	Schema                     string `json:"schema"`
	HostCreateVisible          bool   `json:"hostCreateVisible"`
	TargetCreateVisible        bool   `json:"targetCreateVisible"`
	HostAtomicReplaceVisible   bool   `json:"hostAtomicReplaceVisible"`
	TargetAtomicReplaceVisible bool   `json:"targetAtomicReplaceVisible"`
	RenameVisible              bool   `json:"renameVisible"`
	DeleteVisible              bool   `json:"deleteVisible"`
	ModeVisible                bool   `json:"modeVisible"`
	FlushDurable               bool   `json:"flushDurable"`
	SameRootLocksConflict      bool   `json:"sameRootLocksConflict"`
	RootEscapeRejected         bool   `json:"rootEscapeRejected"`
	SymlinkEscapeRejected      bool   `json:"symlinkEscapeRejected"`
	WatcherStreamHealthy       bool   `json:"watcherStreamHealthy"`
	SilentShortWrites          int    `json:"silentShortWrites"`
	FalseSuccesses             int    `json:"falseSuccesses"`
	HostWatcherSamples         int    `json:"hostWatcherSamples"`
	TargetWatcherSamples       int    `json:"targetWatcherSamples"`
}

type sharedWorkspacePathCorrectnessEvidence struct {
	Schema               string          `json:"schema"`
	Status               string          `json:"status"`
	Tools                []string        `json:"tools"`
	RepresentativeAgents []string        `json:"representativeAgents"`
	Limitations          []string        `json:"limitations"`
	Checks               map[string]bool `json:"checks"`
}

type sharedWorkspaceCorrectnessSummary struct {
	Passed      bool                               `json:"passed"`
	Observation sharedWorkspaceCorrectnessEvidence `json:"observation"`
}

type sharedWorkspacePathCorrectnessSummary struct {
	Passed      bool                                   `json:"passed"`
	Observation sharedWorkspacePathCorrectnessEvidence `json:"observation"`
}

type sharedWorkspaceSampleSummary struct {
	Samples  int     `json:"samples"`
	MedianMS float64 `json:"medianMs"`
	P95MS    float64 `json:"p95Ms"`
}

type sharedWorkspacePerformanceMetric struct {
	ID            string                        `json:"id"`
	Candidate     sharedWorkspaceSampleSummary  `json:"candidate"`
	Reference     *sharedWorkspaceSampleSummary `json:"reference,omitempty"`
	ReferenceKind string                        `json:"referenceKind"`
	Passed        bool                          `json:"passed"`
}

type sharedWorkspaceCommitIdentity struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
}

type sharedWorkspaceFilesystemControlIdentity struct {
	Commit      string `json:"commit"`
	Dirty       bool   `json:"dirty"`
	Mechanism   string `json:"mechanism"`
	GuestRoot   string `json:"guestRoot"`
	SampleOrder string `json:"sampleOrder"`
}

type sharedWorkspaceFilesystemControlEvidence struct {
	Schema        string                                   `json:"schema"`
	Commit        string                                   `json:"commit"`
	Dirty         bool                                     `json:"dirty"`
	Mechanism     string                                   `json:"mechanism"`
	GuestRoot     string                                   `json:"guestRoot"`
	FixtureSHA256 string                                   `json:"fixtureSHA256"`
	Samples       int                                      `json:"samples"`
	Warmups       int                                      `json:"warmups"`
	SampleOrder   string                                   `json:"sampleOrder"`
	Artifacts     map[string]sharedWorkspaceArtifactDigest `json:"artifacts"`
	PairedSamples sharedWorkspaceArtifactDigest            `json:"pairedSamples"`
}

type sharedWorkspacePerformanceEvidence struct {
	Schema            string                                   `json:"schema"`
	Result            string                                   `json:"result"`
	ThresholdsPassed  bool                                     `json:"thresholdsPassed"`
	FixtureSHA256     string                                   `json:"fixtureSHA256"`
	Candidate         sharedWorkspaceCommitIdentity            `json:"candidate"`
	ResearchBaseline  sharedWorkspaceCommitIdentity            `json:"researchBaseline"`
	FilesystemControl sharedWorkspaceFilesystemControlIdentity `json:"filesystemControl"`
	PackageIdentity   PackageIdentity                          `json:"packageIdentity"`
	Runtime           RuntimeBinding                           `json:"runtime"`
	Methodology       struct {
		Samples                      int     `json:"samples"`
		Warmups                      int     `json:"warmups"`
		FilesystemSampleOrder        string  `json:"filesystemSampleOrder"`
		FirstByteSampleOrder         string  `json:"firstByteSampleOrder"`
		GitMedianAbsoluteMS          float64 `json:"gitMedianAbsoluteMs"`
		GitMedianBaselineRatio       float64 `json:"gitMedianBaselineRatio"`
		PackageMedianBaselineRatio   float64 `json:"packageMedianBaselineRatio"`
		AtomicVisibilityP95MS        float64 `json:"atomicVisibilityP95Ms"`
		MountReadyP95MS              float64 `json:"mountReadyP95Ms"`
		FirstByteAbsoluteAllowanceMS float64 `json:"firstByteAbsoluteAllowanceMs"`
		FirstByteBaselineAllowance   float64 `json:"firstByteBaselineAllowance"`
		SaturationTeardownMS         float64 `json:"saturationTeardownMs"`
	} `json:"methodology"`
	Metrics         []sharedWorkspacePerformanceMetric     `json:"metrics"`
	Correctness     *sharedWorkspaceCorrectnessSummary     `json:"correctness,omitempty"`
	PathCorrectness *sharedWorkspacePathCorrectnessSummary `json:"pathCorrectness,omitempty"`
	Saturation      struct {
		Passed      bool `json:"passed"`
		Observation struct {
			TeardownMS float64 `json:"teardownMs"`
		} `json:"observation"`
		Metadata sharedWorkspaceSampleSummary `json:"metadata"`
	} `json:"saturation"`
	Artifacts struct {
		Candidate                 map[string]sharedWorkspaceArtifactDigest `json:"candidate"`
		FilesystemControl         map[string]sharedWorkspaceArtifactDigest `json:"filesystemControl"`
		ResearchBaseline          map[string]sharedWorkspaceArtifactDigest `json:"researchBaseline"`
		Fixture                   sharedWorkspaceArtifactDigest            `json:"fixture"`
		FilesystemControlManifest sharedWorkspaceArtifactDigest            `json:"filesystemControlManifest"`
		PairedSamples             sharedWorkspaceArtifactDigest            `json:"pairedSamples"`
		Correctness               *sharedWorkspaceArtifactDigest           `json:"correctness,omitempty"`
		PathCorrectness           *sharedWorkspaceArtifactDigest           `json:"pathCorrectness,omitempty"`
		Saturation                sharedWorkspaceArtifactDigest            `json:"saturation"`
		ResearchDecision          sharedWorkspaceArtifactDigest            `json:"researchDecision"`
	} `json:"artifacts"`
}

func validateSharedWorkspaceBehaviorArtifact(refs []ArtifactRef, artifacts map[string][]byte, expectedCommit string, expectedPackage *PackageIdentity, expectedRuntime *RuntimeExpectation) error {
	data, ok := artifacts["behavior.json"]
	if !ok {
		return errors.New("shared-workspace behavior evidence is missing behavior.json")
	}
	var evidence sharedWorkspaceBehaviorEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("shared-workspace behavior evidence: %w", err)
	}
	if (evidence.Schema != "hideout.shared-workspace-real-gate2/v1" &&
		evidence.Schema != "hideout.shared-workspace-real-gate2/v2") || evidence.Status != "passed" ||
		evidence.Backend != "lima" || evidence.HostOS != "darwin" || evidence.HostArch != "arm64" ||
		evidence.Transport != "workspace-portal" {
		return errors.New("shared-workspace behavior identity, platform, or transport is invalid")
	}
	if !IsCanonicalCommit(evidence.Commit) || evidence.Dirty || (expectedCommit != "" && evidence.Commit != expectedCommit) {
		return errors.New("shared-workspace behavior is not bound to the clean candidate commit")
	}
	if err := validateSharedWorkspacePackageAndRuntime(evidence.PackageIdentity, evidence.Runtime, expectedCommit, expectedPackage, expectedRuntime); err != nil {
		return err
	}
	requiredChecks := requiredSharedWorkspaceBehaviorChecks
	if evidence.Schema == "hideout.shared-workspace-real-gate2/v2" {
		requiredChecks = requiredSharedWorkspaceBehaviorV2Checks
	}
	if err := validateExactBooleanChecks("shared-workspace behavior", evidence.Checks, requiredChecks); err != nil {
		return err
	}
	wantPaths := map[string]string{
		"relations":                     "artifacts/behavior/relations.json",
		"lifecycle":                     "artifacts/behavior/lifecycle.json",
		"researchDecision":              "artifacts/behavior/research-decision.json",
		"packageManifest":               "artifacts/behavior/package-manifest.json",
		"workspacePortalHelperManifest": "artifacts/behavior/workspace-portal-helper.manifest.json",
	}
	if evidence.Schema == "hideout.shared-workspace-real-gate2/v1" {
		wantPaths["correctness"] = "artifacts/behavior/correctness.json"
	} else {
		wantPaths["pathCorrectness"] = "artifacts/behavior/path-correctness.json"
		wantPaths["pathNegativeFixture"] = "artifacts/behavior/path-negative-fixture.json"
	}
	if err := validateSharedWorkspaceDigestMap("shared-workspace behavior", evidence.Artifacts, wantPaths); err != nil {
		return err
	}
	kinds := map[string]string{"behavior.json": "manifest"}
	for _, path := range wantPaths {
		kinds[path] = "manifest"
	}
	if err := validateSharedWorkspaceArtifactInventory(refs, artifacts, kinds, evidence.Artifacts); err != nil {
		return err
	}

	var relations sharedWorkspaceRelationsEvidence
	if err := decodeStrictEvidence(artifacts[evidence.Artifacts["relations"].Path], &relations); err != nil {
		return fmt.Errorf("shared-workspace relations artifact: %w", err)
	}
	if relations.Schema != "hideout.shared-workspace-relations/v1" || relations.Status != "passed" ||
		relations.EnvironmentCount != 1 || relations.InstanceCount != 1 {
		return errors.New("shared-workspace relations did not prove one environment and one instance")
	}
	if err := validateExactBooleanChecks("shared-workspace relations", relations.Checks, requiredSharedWorkspaceRelationChecks); err != nil {
		return err
	}

	var lifecycle sharedWorkspaceLifecycleEvidence
	if err := decodeStrictEvidence(artifacts[evidence.Artifacts["lifecycle"].Path], &lifecycle); err != nil {
		return fmt.Errorf("shared-workspace lifecycle artifact: %w", err)
	}
	if lifecycle.Schema != "hideout.shared-workspace-lifecycle/v1" || lifecycle.Status != "passed" ||
		lifecycle.FirstGeneration <= 0 || lifecycle.RestartGeneration <= lifecycle.FirstGeneration ||
		lifecycle.ElapsedSeconds <= 0 || math.IsNaN(lifecycle.ElapsedSeconds) || math.IsInf(lifecycle.ElapsedSeconds, 0) {
		return errors.New("shared-workspace lifecycle identity or generation transition is invalid")
	}
	if err := validateExactBooleanChecks("shared-workspace lifecycle", lifecycle.Checks, requiredSharedWorkspaceLifecycleChecks); err != nil {
		return err
	}

	if evidence.Schema == "hideout.shared-workspace-real-gate2/v1" {
		var correctness sharedWorkspaceCorrectnessEvidence
		if err := decodeStrictEvidence(artifacts[evidence.Artifacts["correctness"].Path], &correctness); err != nil {
			return fmt.Errorf("shared-workspace correctness artifact: %w", err)
		}
		if err := validateSharedWorkspaceCorrectness(correctness, 30); err != nil {
			return err
		}
	} else {
		var positive, negative sharedWorkspacePathCorrectnessEvidence
		if err := decodeStrictEvidence(artifacts[evidence.Artifacts["pathCorrectness"].Path], &positive); err != nil {
			return fmt.Errorf("shared-workspace path correctness artifact: %w", err)
		}
		if err := validateSharedWorkspacePathCorrectness(positive); err != nil {
			return err
		}
		if err := decodeStrictEvidence(artifacts[evidence.Artifacts["pathNegativeFixture"].Path], &negative); err != nil {
			return fmt.Errorf("shared-workspace path negative fixture: %w", err)
		}
		if err := validateSharedWorkspacePathNegativeFixture(negative); err != nil {
			return err
		}
	}
	if err := validateSharedWorkspaceResearchDecision(artifacts[evidence.Artifacts["researchDecision"].Path]); err != nil {
		return err
	}
	if err := validateSharedWorkspacePackageManifests(
		artifacts[evidence.Artifacts["packageManifest"].Path],
		artifacts[evidence.Artifacts["workspacePortalHelperManifest"].Path],
		evidence.PackageIdentity,
	); err != nil {
		return err
	}
	return nil
}

func validateSharedWorkspacePerformanceArtifact(refs []ArtifactRef, artifacts map[string][]byte, expectedCommit string, expectedPackage *PackageIdentity, expectedRuntime *RuntimeExpectation) error {
	data, ok := artifacts["performance.json"]
	if !ok {
		return errors.New("shared-workspace performance evidence is missing performance.json")
	}
	var evidence sharedWorkspacePerformanceEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("shared-workspace performance evidence: %w", err)
	}
	performanceV2 := evidence.Schema == "hideout.shared-workspace-gate2-evaluation/v2"
	if (evidence.Schema != "hideout.shared-workspace-gate2-evaluation/v1" && !performanceV2) ||
		evidence.Result != "passed" || !evidence.ThresholdsPassed {
		return errors.New("shared-workspace performance result is not passed")
	}
	if (performanceV2 && (evidence.Correctness != nil || evidence.PathCorrectness == nil ||
		evidence.Artifacts.Correctness != nil || evidence.Artifacts.PathCorrectness == nil)) ||
		(!performanceV2 && (evidence.Correctness == nil || evidence.PathCorrectness != nil ||
			evidence.Artifacts.Correctness == nil || evidence.Artifacts.PathCorrectness != nil)) {
		return errors.New("shared-workspace performance correctness evidence does not match its schema")
	}
	if !IsCanonicalCommit(evidence.Candidate.Commit) || evidence.Candidate.Dirty ||
		(expectedCommit != "" && evidence.Candidate.Commit != expectedCommit) {
		return errors.New("shared-workspace performance candidate identity is invalid")
	}
	if !IsCanonicalCommit(evidence.ResearchBaseline.Commit) || evidence.ResearchBaseline.Commit == evidence.Candidate.Commit {
		return errors.New("shared-workspace performance research baseline identity is invalid")
	}
	if !IsCanonicalCommit(evidence.FilesystemControl.Commit) || evidence.FilesystemControl.Dirty ||
		evidence.FilesystemControl.Commit != evidence.Candidate.Commit ||
		evidence.FilesystemControl.Mechanism != "profile-cache-static-virtiofs" ||
		evidence.FilesystemControl.GuestRoot != "/hideout/profile/cache/035-static-virtiofs-control" ||
		evidence.FilesystemControl.SampleOrder != "alternating-pairs" {
		return errors.New("shared-workspace performance filesystem control identity is invalid")
	}
	if err := validateSharedWorkspacePackageAndRuntime(evidence.PackageIdentity, evidence.Runtime, expectedCommit, expectedPackage, expectedRuntime); err != nil {
		return err
	}
	if evidence.Methodology.Samples < 30 || evidence.Methodology.Warmups != 1 ||
		evidence.Methodology.FilesystemSampleOrder != "alternating-pairs" ||
		evidence.Methodology.FirstByteSampleOrder != "one-warmup-then-measured" ||
		evidence.Methodology.GitMedianAbsoluteMS != 2000 || evidence.Methodology.GitMedianBaselineRatio != 2 ||
		evidence.Methodology.PackageMedianBaselineRatio != 3 || evidence.Methodology.AtomicVisibilityP95MS != 250 ||
		evidence.Methodology.MountReadyP95MS != 1000 || evidence.Methodology.FirstByteAbsoluteAllowanceMS != 500 ||
		evidence.Methodology.FirstByteBaselineAllowance != 0.15 || evidence.Methodology.SaturationTeardownMS != 5000 {
		return errors.New("shared-workspace performance methodology or accepted thresholds drifted")
	}
	if !isLowerHexSHA256(evidence.FixtureSHA256) {
		return errors.New("shared-workspace performance fixture digest is invalid")
	}

	wantCandidate := sharedWorkspaceCandidateRawPaths
	wantControl := sharedWorkspaceFilesystemControlRawPaths
	wantResearchBaseline := sharedWorkspaceResearchBaselineRawPaths
	if err := validateSharedWorkspaceDigestMap("shared-workspace candidate samples", evidence.Artifacts.Candidate, wantCandidate); err != nil {
		return err
	}
	if err := validateSharedWorkspaceDigestMap("shared-workspace filesystem control samples", evidence.Artifacts.FilesystemControl, wantControl); err != nil {
		return err
	}
	if err := validateSharedWorkspaceDigestMap("shared-workspace research baseline samples", evidence.Artifacts.ResearchBaseline, wantResearchBaseline); err != nil {
		return err
	}
	if evidence.Artifacts.Fixture.Path != "artifacts/performance/research-baseline/fixture.sha256" ||
		evidence.Artifacts.FilesystemControlManifest.Path != "artifacts/performance/filesystem-control/manifest.json" ||
		evidence.Artifacts.PairedSamples.Path != sharedWorkspacePairedSamplesPath ||
		evidence.Artifacts.Saturation.Path != "artifacts/performance/saturation.json" ||
		evidence.Artifacts.ResearchDecision.Path != "artifacts/performance/research-decision.json" {
		return errors.New("shared-workspace performance artifact paths drifted")
	}
	if (!performanceV2 && evidence.Artifacts.Correctness.Path != "artifacts/performance/correctness.json") ||
		(performanceV2 && evidence.Artifacts.PathCorrectness.Path != "artifacts/performance/path-correctness.json") {
		return errors.New("shared-workspace performance correctness artifact path drifted")
	}
	digests := make(map[string]sharedWorkspaceArtifactDigest, len(evidence.Artifacts.Candidate)+len(evidence.Artifacts.FilesystemControl)+len(evidence.Artifacts.ResearchBaseline)+6)
	for key, value := range evidence.Artifacts.Candidate {
		digests["candidate/"+key] = value
	}
	for key, value := range evidence.Artifacts.FilesystemControl {
		digests["filesystem-control/"+key] = value
	}
	for key, value := range evidence.Artifacts.ResearchBaseline {
		digests["research-baseline/"+key] = value
	}
	digests["fixture"] = evidence.Artifacts.Fixture
	digests["filesystemControlManifest"] = evidence.Artifacts.FilesystemControlManifest
	digests["pairedSamples"] = evidence.Artifacts.PairedSamples
	if performanceV2 {
		digests["pathCorrectness"] = *evidence.Artifacts.PathCorrectness
	} else {
		digests["correctness"] = *evidence.Artifacts.Correctness
	}
	digests["saturation"] = evidence.Artifacts.Saturation
	digests["researchDecision"] = evidence.Artifacts.ResearchDecision
	kinds := map[string]string{"performance.json": "manifest"}
	for _, descriptor := range digests {
		kinds[descriptor.Path] = "log"
	}
	manifestDescriptors := []sharedWorkspaceArtifactDigest{
		evidence.Artifacts.Fixture, evidence.Artifacts.FilesystemControlManifest,
		evidence.Artifacts.Saturation, evidence.Artifacts.ResearchDecision,
	}
	if performanceV2 {
		manifestDescriptors = append(manifestDescriptors, *evidence.Artifacts.PathCorrectness)
	} else {
		manifestDescriptors = append(manifestDescriptors, *evidence.Artifacts.Correctness)
	}
	for _, descriptor := range manifestDescriptors {
		kinds[descriptor.Path] = "manifest"
	}
	if err := validateSharedWorkspaceArtifactInventory(refs, artifacts, kinds, digests); err != nil {
		return err
	}
	if strings.TrimSpace(string(artifacts[evidence.Artifacts.Fixture.Path])) != evidence.FixtureSHA256 {
		return errors.New("shared-workspace fixture file does not match the performance identity")
	}
	if err := validateSharedWorkspaceFilesystemControl(evidence, artifacts); err != nil {
		return err
	}

	metrics := make(map[string]sharedWorkspacePerformanceMetric, len(evidence.Metrics))
	for _, metric := range evidence.Metrics {
		if _, exists := metrics[metric.ID]; exists {
			return fmt.Errorf("shared-workspace performance contains duplicate metric %q", metric.ID)
		}
		metrics[metric.ID] = metric
	}
	if len(metrics) != len(sharedWorkspaceCandidateMetrics) {
		return errors.New("shared-workspace performance metric inventory drifted")
	}
	stats := map[string]sharedWorkspaceSampleSummary{}
	for _, id := range sharedWorkspaceCandidateMetrics {
		metric, ok := metrics[id]
		if !ok || !metric.Passed {
			return fmt.Errorf("shared-workspace performance metric %q is absent or failed", id)
		}
		values, err := parseSharedWorkspaceSamples(artifacts[evidence.Artifacts.Candidate[id].Path])
		if err != nil {
			return fmt.Errorf("shared-workspace candidate %s samples: %w", id, err)
		}
		if err := validateSharedWorkspaceSampleSummary(metric.Candidate, values, evidence.Methodology.Samples); err != nil {
			return fmt.Errorf("shared-workspace candidate %s: %w", id, err)
		}
		stats["candidate/"+id] = metric.Candidate
		referenceKind := "absolute-threshold"
		var referenceDescriptor sharedWorkspaceArtifactDigest
		if slices.Contains(sharedWorkspaceFilesystemControlMetrics, id) {
			referenceKind = "paired-static-virtiofs"
			referenceDescriptor = evidence.Artifacts.FilesystemControl[id]
		} else if slices.Contains(sharedWorkspaceResearchBaselineMetrics, id) {
			referenceKind = "retained-research-baseline"
			referenceDescriptor = evidence.Artifacts.ResearchBaseline[id]
		}
		if metric.ReferenceKind != referenceKind {
			return fmt.Errorf("shared-workspace metric %q has invalid reference kind", id)
		}
		if referenceDescriptor.Path != "" {
			if metric.Reference == nil {
				return fmt.Errorf("shared-workspace metric %q omitted its reference", id)
			}
			reference, err := parseSharedWorkspaceSamples(artifacts[referenceDescriptor.Path])
			if err != nil {
				return fmt.Errorf("shared-workspace reference %s samples: %w", id, err)
			}
			if err := validateSharedWorkspaceSampleSummary(*metric.Reference, reference, evidence.Methodology.Samples); err != nil {
				return fmt.Errorf("shared-workspace reference %s: %w", id, err)
			}
			stats["reference/"+id] = *metric.Reference
		} else if metric.Reference != nil {
			return fmt.Errorf("shared-workspace metric %q has an unexpected reference", id)
		}
	}
	if err := validateSharedWorkspacePairedSamples(evidence, artifacts); err != nil {
		return err
	}

	saturationValues, err := parseSharedWorkspaceSamples(artifacts[evidence.Artifacts.Candidate["saturation-metadata"].Path])
	if err != nil {
		return fmt.Errorf("shared-workspace saturation samples: %w", err)
	}
	if err := validateSharedWorkspaceSampleSummary(evidence.Saturation.Metadata, saturationValues, 100); err != nil {
		return fmt.Errorf("shared-workspace saturation metadata: %w", err)
	}
	var saturation struct {
		TeardownMS float64 `json:"teardownMs"`
	}
	if err := decodeStrictEvidence(artifacts[evidence.Artifacts.Saturation.Path], &saturation); err != nil {
		return fmt.Errorf("shared-workspace saturation artifact: %w", err)
	}
	if !approximatelyEqual(saturation.TeardownMS, evidence.Saturation.Observation.TeardownMS) ||
		math.IsNaN(saturation.TeardownMS) || math.IsInf(saturation.TeardownMS, 0) || saturation.TeardownMS < 0 {
		return errors.New("shared-workspace saturation observation is invalid or not derived")
	}
	if performanceV2 {
		var pathCorrectness sharedWorkspacePathCorrectnessEvidence
		if err := decodeStrictEvidence(artifacts[evidence.Artifacts.PathCorrectness.Path], &pathCorrectness); err != nil {
			return fmt.Errorf("shared-workspace path correctness artifact: %w", err)
		}
		if !slices.Equal(pathCorrectness.Tools, evidence.PathCorrectness.Observation.Tools) ||
			!slices.Equal(pathCorrectness.RepresentativeAgents, evidence.PathCorrectness.Observation.RepresentativeAgents) ||
			!slices.Equal(pathCorrectness.Limitations, evidence.PathCorrectness.Observation.Limitations) ||
			pathCorrectness.Schema != evidence.PathCorrectness.Observation.Schema ||
			pathCorrectness.Status != evidence.PathCorrectness.Observation.Status ||
			!maps.Equal(pathCorrectness.Checks, evidence.PathCorrectness.Observation.Checks) ||
			!evidence.PathCorrectness.Passed {
			return errors.New("shared-workspace path correctness summary is not derived from its artifact")
		}
		if err := validateSharedWorkspacePathCorrectness(pathCorrectness); err != nil {
			return err
		}
	} else {
		var correctness sharedWorkspaceCorrectnessEvidence
		if err := decodeStrictEvidence(artifacts[evidence.Artifacts.Correctness.Path], &correctness); err != nil {
			return fmt.Errorf("shared-workspace correctness artifact: %w", err)
		}
		if correctness != evidence.Correctness.Observation || !evidence.Correctness.Passed {
			return errors.New("shared-workspace correctness summary is not derived from its artifact")
		}
		if err := validateSharedWorkspaceCorrectness(correctness, evidence.Methodology.Samples); err != nil {
			return err
		}
	}
	var decision workspaceattach.ResearchDecision
	if err := decodeStrictEvidence(artifacts[evidence.Artifacts.ResearchDecision.Path], &decision); err != nil {
		return fmt.Errorf("shared-workspace research decision artifact: %w", err)
	}
	if err := validateSharedWorkspaceResearchDecisionValue(decision, artifacts[evidence.Artifacts.ResearchDecision.Path]); err != nil {
		return err
	}
	if evidence.ResearchBaseline.Commit != decision.Provenance.Commit || evidence.FixtureSHA256 != decision.Provenance.FixtureDigest {
		return errors.New("shared-workspace research baseline or fixture is not bound to the accepted research decision")
	}

	git := stats["candidate/git-status"]
	baseGit := stats["reference/git-status"]
	packages := stats["candidate/package-scan"]
	basePackages := stats["reference/package-scan"]
	firstByte := stats["candidate/first-byte"]
	baseFirstByte := stats["reference/first-byte"]
	passed := git.MedianMS <= evidence.Methodology.GitMedianAbsoluteMS &&
		git.MedianMS <= evidence.Methodology.GitMedianBaselineRatio*baseGit.MedianMS &&
		packages.MedianMS <= evidence.Methodology.PackageMedianBaselineRatio*basePackages.MedianMS &&
		stats["candidate/atomic-host-to-guest"].P95MS <= evidence.Methodology.AtomicVisibilityP95MS &&
		stats["candidate/atomic-guest-to-host"].P95MS <= evidence.Methodology.AtomicVisibilityP95MS &&
		stats["candidate/mount-ready"].P95MS <= evidence.Methodology.MountReadyP95MS &&
		firstByte.P95MS <= baseFirstByte.P95MS+math.Max(evidence.Methodology.FirstByteAbsoluteAllowanceMS, evidence.Methodology.FirstByteBaselineAllowance*baseFirstByte.P95MS) &&
		saturation.TeardownMS <= evidence.Methodology.SaturationTeardownMS
	if !passed || !evidence.Saturation.Passed {
		return errors.New("shared-workspace performance thresholds did not pass when recomputed")
	}
	return nil
}

func validateSharedWorkspaceFilesystemControl(evidence sharedWorkspacePerformanceEvidence, artifacts map[string][]byte) error {
	data, ok := artifacts[evidence.Artifacts.FilesystemControlManifest.Path]
	if !ok {
		return errors.New("shared-workspace filesystem control manifest is missing")
	}
	var control sharedWorkspaceFilesystemControlEvidence
	if err := decodeStrictEvidence(data, &control); err != nil {
		return fmt.Errorf("shared-workspace filesystem control manifest: %w", err)
	}
	identity := evidence.FilesystemControl
	if control.Schema != "hideout.shared-workspace-paired-control/v1" ||
		control.Commit != identity.Commit || control.Dirty != identity.Dirty ||
		control.Mechanism != identity.Mechanism || control.GuestRoot != identity.GuestRoot ||
		control.FixtureSHA256 != evidence.FixtureSHA256 ||
		control.Samples != evidence.Methodology.Samples || control.Warmups != evidence.Methodology.Warmups ||
		control.SampleOrder != identity.SampleOrder {
		return errors.New("shared-workspace filesystem control manifest identity or methodology is invalid")
	}
	if err := validateSharedWorkspaceDigestMap("shared-workspace filesystem control manifest", control.Artifacts, sharedWorkspaceFilesystemControlRawPaths); err != nil {
		return err
	}
	for id, descriptor := range control.Artifacts {
		if descriptor != evidence.Artifacts.FilesystemControl[id] {
			return fmt.Errorf("shared-workspace filesystem control artifact %q is not bound to the performance evidence", id)
		}
	}
	if control.PairedSamples != evidence.Artifacts.PairedSamples || control.PairedSamples.Path != sharedWorkspacePairedSamplesPath {
		return errors.New("shared-workspace paired sample artifact is not bound to the filesystem control manifest")
	}
	return nil
}

type sharedWorkspacePairedSample struct {
	Metric string
	Index  int
	Side   string
	Value  float64
}

func validateSharedWorkspacePairedSamples(evidence sharedWorkspacePerformanceEvidence, artifacts map[string][]byte) error {
	data, ok := artifacts[evidence.Artifacts.PairedSamples.Path]
	if !ok {
		return errors.New("shared-workspace paired filesystem samples are missing")
	}
	rows := make([]sharedWorkspacePairedSample, 0, evidence.Methodology.Samples*len(sharedWorkspaceFilesystemControlMetrics)*2)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 4 {
			return errors.New("shared-workspace paired filesystem sample has an invalid record shape")
		}
		index, err := strconv.Atoi(fields[1])
		if err != nil {
			return errors.New("shared-workspace paired filesystem sample has an invalid index")
		}
		value, err := strconv.ParseFloat(fields[3], 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("shared-workspace paired filesystem sample has an invalid value")
		}
		rows = append(rows, sharedWorkspacePairedSample{Metric: fields[0], Index: index, Side: fields[2], Value: value})
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read shared-workspace paired filesystem samples: %w", err)
	}
	wantRows := evidence.Methodology.Samples * len(sharedWorkspaceFilesystemControlMetrics) * 2
	if len(rows) != wantRows {
		return fmt.Errorf("shared-workspace paired filesystem sample count is %d, want %d", len(rows), wantRows)
	}
	derived := map[string][]float64{}
	offset := 0
	for _, metric := range sharedWorkspaceFilesystemControlMetrics {
		for index := 1; index <= evidence.Methodology.Samples; index++ {
			sides := []string{"candidate", "control"}
			if index%2 == 0 {
				sides[0], sides[1] = sides[1], sides[0]
			}
			for _, side := range sides {
				row := rows[offset]
				offset++
				if row.Metric != metric || row.Index != index || row.Side != side {
					return fmt.Errorf("shared-workspace paired filesystem sample order drifted at %s sample %d", metric, index)
				}
				derived[side+"/"+metric] = append(derived[side+"/"+metric], row.Value)
			}
		}
	}
	for _, metric := range sharedWorkspaceFilesystemControlMetrics {
		for _, side := range []string{"candidate", "control"} {
			descriptor := evidence.Artifacts.Candidate[metric]
			if side == "control" {
				descriptor = evidence.Artifacts.FilesystemControl[metric]
			}
			values, err := parseSharedWorkspaceSamples(artifacts[descriptor.Path])
			if err != nil {
				return fmt.Errorf("shared-workspace %s %s samples: %w", side, metric, err)
			}
			paired := derived[side+"/"+metric]
			if len(values) != len(paired) {
				return fmt.Errorf("shared-workspace %s %s samples are not derived from paired observations", side, metric)
			}
			for index := range values {
				if !approximatelyEqual(values[index], paired[index]) {
					return fmt.Errorf("shared-workspace %s %s samples are not derived from paired observations", side, metric)
				}
			}
		}
	}
	return nil
}

func validateSharedWorkspacePackageAndRuntime(actualPackage PackageIdentity, actualRuntime RuntimeBinding, expectedCommit string, expectedPackage *PackageIdentity, expectedRuntime *RuntimeExpectation) error {
	if expectedPackage == nil || expectedPackage.ValidateCandidateCommit(expectedCommit) != nil ||
		actualPackage.ValidateCandidateCommit(expectedCommit) != nil || packageStale(&actualPackage, expectedPackage) {
		return errors.New("shared-workspace evidence package identity is invalid")
	}
	if expectedRuntime == nil {
		return errors.New("shared-workspace evidence requires a trusted runtime identity")
	}
	if err := actualRuntime.Matches(*expectedRuntime); err != nil {
		return fmt.Errorf("shared-workspace evidence runtime identity: %w", err)
	}
	return nil
}

func validateSharedWorkspaceDigestMap(name string, got map[string]sharedWorkspaceArtifactDigest, wantPaths map[string]string) error {
	if len(got) != len(wantPaths) {
		return fmt.Errorf("%s artifact inventory drifted", name)
	}
	for key, path := range wantPaths {
		descriptor, ok := got[key]
		if !ok || descriptor.Path != path || !isLowerHexSHA256(descriptor.SHA256) {
			return fmt.Errorf("%s artifact %q identity is invalid", name, key)
		}
	}
	return nil
}

func validateSharedWorkspaceArtifactInventory(refs []ArtifactRef, artifacts map[string][]byte, kinds map[string]string, descriptors map[string]sharedWorkspaceArtifactDigest) error {
	if len(refs) != len(kinds) || len(artifacts) != len(kinds) {
		return errors.New("shared-workspace proof artifact inventory drifted")
	}
	wantDigests := make(map[string]string, len(kinds))
	for _, descriptor := range descriptors {
		if _, duplicate := wantDigests[descriptor.Path]; duplicate {
			return errors.New("shared-workspace evidence repeats an artifact path")
		}
		wantDigests[descriptor.Path] = descriptor.SHA256
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		kind, ok := kinds[ref.Path]
		if !ok || ref.Kind != kind || seen[ref.Path] || ref.RedactionStatus != RedactionPassed || !isLowerHexSHA256(ref.SHA256) {
			return fmt.Errorf("shared-workspace proof artifact ref %q is invalid", ref.Path)
		}
		seen[ref.Path] = true
		data, ok := artifacts[ref.Path]
		if !ok {
			return fmt.Errorf("shared-workspace proof artifact %q bytes are missing", ref.Path)
		}
		sum := sha256.Sum256(data)
		actual := hex.EncodeToString(sum[:])
		if actual != ref.SHA256 {
			return fmt.Errorf("shared-workspace proof artifact %q digest mismatch", ref.Path)
		}
		if declared := wantDigests[ref.Path]; declared != "" && declared != actual {
			return fmt.Errorf("shared-workspace semantic artifact %q digest mismatch", ref.Path)
		}
	}
	return nil
}

func validateSharedWorkspaceCorrectness(value sharedWorkspaceCorrectnessEvidence, samples int) error {
	if value.Schema != "hideout.shared-workspace-correctness/v1" ||
		!value.HostCreateVisible || !value.TargetCreateVisible || !value.HostAtomicReplaceVisible ||
		!value.TargetAtomicReplaceVisible || !value.RenameVisible || !value.DeleteVisible ||
		!value.ModeVisible || !value.FlushDurable || !value.SameRootLocksConflict ||
		!value.RootEscapeRejected || !value.SymlinkEscapeRejected || !value.WatcherStreamHealthy ||
		value.SilentShortWrites != 0 || value.FalseSuccesses != 0 ||
		value.HostWatcherSamples < samples || value.TargetWatcherSamples < samples {
		return errors.New("shared-workspace correctness artifact contains a failed or incomplete observation")
	}
	return nil
}

func validateSharedWorkspacePathCorrectness(value sharedWorkspacePathCorrectnessEvidence) error {
	if value.Schema != "hideout.shared-workspace-path-correctness/v1" ||
		value.Status != "passed" ||
		!slices.Equal(value.Tools, sharedWorkspacePathTools) ||
		!slices.Equal(value.RepresentativeAgents, []string{"claude", "codex"}) ||
		!slices.Equal(value.Limitations, sharedWorkspacePathLimitations) {
		return errors.New("shared-workspace path correctness identity is invalid")
	}
	return validateExactBooleanChecks(
		"shared-workspace path correctness",
		value.Checks,
		requiredSharedWorkspacePathChecks,
	)
}

func validateSharedWorkspacePathNegativeFixture(value sharedWorkspacePathCorrectnessEvidence) error {
	if value.Schema != "hideout.shared-workspace-path-correctness/v1" ||
		value.Status != "failed" ||
		!slices.Equal(value.Tools, sharedWorkspacePathTools) ||
		!slices.Equal(value.RepresentativeAgents, []string{"claude", "codex"}) ||
		!slices.Equal(value.Limitations, sharedWorkspacePathLimitations) ||
		len(value.Checks) != len(requiredSharedWorkspacePathChecks) {
		return errors.New("shared-workspace path negative fixture identity is invalid")
	}
	for _, check := range requiredSharedWorkspacePathChecks {
		observed, ok := value.Checks[check]
		want := check != "logicalPhysicalSameObject"
		if !ok || observed != want {
			return fmt.Errorf("shared-workspace path negative fixture check %q is invalid", check)
		}
	}
	return nil
}

func validateSharedWorkspaceResearchDecision(data []byte) error {
	var decision workspaceattach.ResearchDecision
	if err := decodeStrictEvidence(data, &decision); err != nil {
		return fmt.Errorf("shared-workspace research decision artifact: %w", err)
	}
	return validateSharedWorkspaceResearchDecisionValue(decision, data)
}

func validateSharedWorkspaceResearchDecisionValue(decision workspaceattach.ResearchDecision, data []byte) error {
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != sharedWorkspaceResearchDecisionSHA256 ||
		decision.Schema != workspaceattach.ResearchSchema || decision.Feature != workspaceattach.ResearchFeature ||
		decision.Result != workspaceattach.ResearchAccepted || decision.SelectedCandidate != workspaceattach.CandidatePortal ||
		decision.Limits == nil || !isLowerHexSHA256(decision.Provenance.FixtureDigest) {
		return errors.New("shared-workspace evidence is not bound to the accepted workspace-portal research decision")
	}
	return nil
}

func validateSharedWorkspacePackageManifests(packageData, helperData []byte, expected PackageIdentity) error {
	var manifest packagekit.Manifest
	if err := decodeStrictEvidence(packageData, &manifest); err != nil {
		return fmt.Errorf("shared-workspace package manifest: %w", err)
	}
	if manifest.Schema != packagekit.ArtifactSchema || manifest.Source.Dirty ||
		manifest.Release.ProductVersion != expected.ProductVersion || manifest.Source.Commit != expected.SourceCommit ||
		manifest.Target.HostOS != expected.HostOS || manifest.Target.HostArch != expected.HostArch {
		return errors.New("shared-workspace package manifest identity does not match the candidate package")
	}
	var helper helperbin.Manifest
	if err := decodeStrictEvidence(helperData, &helper); err != nil {
		return fmt.Errorf("shared-workspace helper manifest: %w", err)
	}
	expectedPath := "bin/" + helperbin.LinuxWorkspacePortalCommand + "-linux-" + manifest.Target.LinuxGuestArch
	if helper.Version != helperbin.ManifestVersion || helper.Command != helperbin.LinuxWorkspacePortalCommand ||
		helper.TargetOS != "linux" || helper.TargetArch != manifest.Target.LinuxGuestArch ||
		helper.Artifact != strings.TrimPrefix(expectedPath, "bin/") || !isLowerHexSHA256(helper.SHA256) {
		return errors.New("shared-workspace packaged helper manifest identity is invalid")
	}
	for _, file := range manifest.Files {
		if file.Path == expectedPath {
			if file.Kind != "linux-helper" || file.SHA256 != helper.SHA256 || !file.Executable {
				return errors.New("shared-workspace packaged helper file does not match its manifest")
			}
			return nil
		}
	}
	return errors.New("shared-workspace package manifest omits the workspace portal helper")
}

func parseSharedWorkspaceSamples(data []byte) ([]float64, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	values := make([]float64, 0, 100)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) != line || line == "" {
			return nil, errors.New("sample line is empty or not canonical")
		}
		value, err := strconv.ParseFloat(line, 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, errors.New("sample is not a finite non-negative number")
		}
		values = append(values, value)
		if len(values) > 10000 {
			return nil, errors.New("sample inventory exceeds the bounded limit")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, errors.New("sample inventory is empty")
	}
	return values, nil
}

func validateSharedWorkspaceSampleSummary(summary sharedWorkspaceSampleSummary, values []float64, wantSamples int) error {
	if len(values) != wantSamples || summary.Samples != wantSamples {
		return fmt.Errorf("sample count=%d summary=%d want=%d", len(values), summary.Samples, wantSamples)
	}
	median, p95, err := recomputeEvidenceStats(values)
	if err != nil {
		return err
	}
	if !approximatelyEqual(median, summary.MedianMS) || !approximatelyEqual(p95, summary.P95MS) {
		return errors.New("summary statistics are not derived from raw samples")
	}
	return nil
}
