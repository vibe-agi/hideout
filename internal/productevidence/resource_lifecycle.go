package productevidence

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

const (
	Feature036 = "036-resource-lifecycle-final-session-stop"

	Proof036Gate0Mechanics    = "036.lifecycle.gate0.mechanics"
	Proof036Gate0Model        = "036.lifecycle.gate0.model-replay"
	Proof036RealLifecycle     = "036.lifecycle.real-gate2.lifecycle"
	Proof036RealPerformance   = "036.lifecycle.real-gate2.performance"
	Proof036RealGate2NotRun   = "036.lifecycle.real-gate2.not-run"
	Proof036DocsClaimBoundary = "036.lifecycle.docs.claim-boundary"
)

var requiredLifecycleLocalChecks = []string{
	"catalogValidation",
	"cleanupBeforeRelease",
	"daemonSingleWriter",
	"evidenceRedaction",
	"generationFencing",
	"providerRegistration",
	"reconciliationReadiness",
	"reconciliationRetry",
	"schemaDriftGuard",
	"shutdownBounded",
	"statusSurfaceParity",
	"stopObservationAuthority",
}

var requiredLifecycleRealChecks = []string{
	"attachStopRaceSafe",
	"attachWaitsForReconciliation",
	"auditEvidenceRetained",
	"automaticStopNonDestructive",
	"bootIdentityObserved",
	"bridgePinsEnvironment",
	"cancellationBeforeOwnerClean",
	"exactObservedStop",
	"explicitStaleRecovery",
	"finalSessionStops",
	"guestDiskRetained",
	"hostHandoffIndependent",
	"newBootGenerationObserved",
	"profileCacheRetained",
	"reconciliationRetry",
	"restartAfterOwnerFailClosed",
	"restartBeforeOwnerClean",
	"restartFreshGraceAtMostOnce",
	"restartNoInheritedAuthority",
	"retainedOverlayPreserved",
	"runBridgeClosed",
	"siblingSessionPreserved",
	"slowProbeDoesNotBlockStatus",
	"stopUnknownBlocksAttach",
}

type lifecycleLocalEvidence struct {
	Schema      string          `json:"schema"`
	Status      string          `json:"status"`
	GeneratedAt string          `json:"generatedAt"`
	Commit      string          `json:"commit"`
	Dirty       bool            `json:"dirty"`
	Checks      map[string]bool `json:"checks"`
}

type lifecycleModelEvidence struct {
	Schema                    string          `json:"schema"`
	Status                    string          `json:"status"`
	GeneratedAt               string          `json:"generatedAt"`
	Commit                    string          `json:"commit"`
	Dirty                     bool            `json:"dirty"`
	ExhaustiveSequences       int             `json:"exhaustiveSequences"`
	ExploredTransitions       int             `json:"exploredTransitions"`
	ScenarioCount             int             `json:"scenarioCount"`
	PersistedReplaySeeds      int             `json:"persistedReplaySeeds"`
	ConcurrentReplaySeeds     int             `json:"concurrentReplaySeeds"`
	StepsPerPersistedReplay   int             `json:"stepsPerPersistedReplay"`
	ConcurrentWaitBoundMS     int             `json:"concurrentWaitBoundMs"`
	CoveredEvents             []string        `json:"coveredEvents"`
	Invariants                map[string]bool `json:"invariants"`
	JournalValidAfterEachStep bool            `json:"journalValidAfterEachStep"`
	CorruptionRejected        bool            `json:"corruptionRejected"`
	StopsWithLiveClient       int             `json:"stopsWithLiveClient"`
	DuplicateGenerationStops  int             `json:"duplicateGenerationStops"`
}

type lifecycleRealEvidence struct {
	Schema      string `json:"schema"`
	Status      string `json:"status"`
	GeneratedAt string `json:"generatedAt"`
	Commit      string `json:"commit"`
	Dirty       bool   `json:"dirty"`
	Backend     string `json:"backend"`
	HostOS      string `json:"hostOS"`
	HostArch    string `json:"hostArch"`
	Metrics     struct {
		AttachStopRaces       int     `json:"attachStopRaces"`
		FinalStopMS           float64 `json:"finalStopMs"`
		StatusReadyMS         float64 `json:"statusReadyMs"`
		ReconciliationRetryMS float64 `json:"reconciliationRetryMs"`
		ShutdownMS            float64 `json:"shutdownMs"`
	} `json:"metrics"`
	Checks    map[string]bool `json:"checks"`
	NonClaims struct {
		GuestRootContainment string `json:"guestRootContainment"`
		HostAppTermination   string `json:"hostAppTermination"`
	} `json:"nonClaims"`
}

type lifecyclePerformanceEvidence struct {
	Schema      string `json:"schema"`
	Status      string `json:"status"`
	GeneratedAt string `json:"generatedAt"`
	Candidate   struct {
		Commit string `json:"commit"`
		Dirty  bool   `json:"dirty"`
	} `json:"candidate"`
	Baseline struct {
		Commit string `json:"commit"`
		Dirty  bool   `json:"dirty"`
	} `json:"baseline"`
	Host struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
	} `json:"host"`
	Runtime struct {
		Family         string `json:"family"`
		Revision       string `json:"revision"`
		ArtifactSHA256 string `json:"artifactSHA256"`
		BuildCommit    string `json:"buildCommit"`
		BuildDirty     bool   `json:"buildDirty"`
	} `json:"runtime"`
	Methodology struct {
		Command       string `json:"command"`
		Samples       int    `json:"samples"`
		Warmups       int    `json:"warmups"`
		FixtureSHA256 string `json:"fixtureSHA256"`
		SampleOrder   string `json:"sampleOrder"`
	} `json:"methodology"`
	CandidateSamplesMS []float64 `json:"candidateSamplesMs"`
	BaselineSamplesMS  []float64 `json:"baselineSamplesMs"`
	CandidateMedianMS  float64   `json:"candidateMedianMs"`
	BaselineMedianMS   float64   `json:"baselineMedianMs"`
	ObservedDeltaMS    float64   `json:"observedDeltaMs"`
	AllowedDeltaMS     float64   `json:"allowedDeltaMs"`
}

func validateLifecycleLocalArtifact(data []byte, expectedCommit string) error {
	var evidence lifecycleLocalEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("lifecycle local evidence: %w", err)
	}
	if evidence.Schema != "hideout.lifecycle-local-evidence/v1" || evidence.Status != "passed" {
		return errors.New("lifecycle local evidence identity is invalid")
	}
	if err := validateLifecycleArtifactCommit(evidence.Commit, expectedCommit); err != nil {
		return err
	}
	return validateExactBooleanChecks("lifecycle local", evidence.Checks, requiredLifecycleLocalChecks)
}

func validateLifecycleModelArtifact(data []byte, expectedCommit string) error {
	var evidence lifecycleModelEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("lifecycle model evidence: %w", err)
	}
	if evidence.Schema != "hideout.lifecycle-model-evidence/v1" || evidence.Status != "passed" {
		return errors.New("lifecycle model evidence identity is invalid")
	}
	if err := validateLifecycleArtifactCommit(evidence.Commit, expectedCommit); err != nil {
		return err
	}
	if evidence.ExhaustiveSequences != 180 || evidence.ExploredTransitions < evidence.ExhaustiveSequences*6 ||
		evidence.ScenarioCount < evidence.ExhaustiveSequences || evidence.PersistedReplaySeeds < 32 ||
		evidence.ConcurrentReplaySeeds < 32 || evidence.StepsPerPersistedReplay < 24 ||
		evidence.ConcurrentWaitBoundMS > 2000 || evidence.ConcurrentWaitBoundMS <= 0 ||
		!evidence.JournalValidAfterEachStep || !evidence.CorruptionRejected ||
		evidence.StopsWithLiveClient != 0 || evidence.DuplicateGenerationStops != 0 {
		return errors.New("lifecycle model evidence does not meet the bounded exploration contract")
	}
	if !slices.Equal(evidence.CoveredEvents, lifecycle.RequiredModelEvents()) {
		return errors.New("lifecycle model evidence event inventory is incomplete or unordered")
	}
	if err := validateExactBooleanChecks("lifecycle model invariants", evidence.Invariants, lifecycle.RequiredModelInvariants()); err != nil {
		return err
	}
	return nil
}

func validateLifecycleRealArtifact(data []byte, expectedCommit string) error {
	var evidence lifecycleRealEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("lifecycle real evidence: %w", err)
	}
	if evidence.Schema != "hideout.lifecycle-real-gate2/v1" || evidence.Status != "passed" ||
		evidence.Backend != "lima" || evidence.HostOS != "darwin" || evidence.HostArch != "arm64" {
		return errors.New("lifecycle real evidence identity or platform is invalid")
	}
	if err := validateLifecycleArtifactCommit(evidence.Commit, expectedCommit); err != nil || evidence.Dirty {
		return errors.New("lifecycle real evidence is not bound to the clean candidate commit")
	}
	if evidence.Metrics.AttachStopRaces < 100 || !boundedPositive(evidence.Metrics.FinalStopMS, 50000) ||
		!boundedPositive(evidence.Metrics.StatusReadyMS, 3000) ||
		!boundedPositive(evidence.Metrics.ReconciliationRetryMS, 5000) ||
		!boundedPositive(evidence.Metrics.ShutdownMS, 3500) {
		return errors.New("lifecycle real evidence timing or race metrics are invalid")
	}
	if err := validateExactBooleanChecks("lifecycle real", evidence.Checks, requiredLifecycleRealChecks); err != nil {
		return err
	}
	if evidence.NonClaims.GuestRootContainment != "not-claimed" || evidence.NonClaims.HostAppTermination != "not-owned" {
		return errors.New("lifecycle real evidence overclaims an independent security or host-app boundary")
	}
	return nil
}

func validateLifecyclePerformanceArtifact(data []byte, expectedCommit string, expectedRuntime *RuntimeExpectation) error {
	var evidence lifecyclePerformanceEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("lifecycle performance evidence: %w", err)
	}
	if evidence.Schema != "hideout.lifecycle-performance/v1" || evidence.Status != "passed" ||
		evidence.Host.OS != "darwin" || evidence.Host.Arch != "arm64" {
		return errors.New("lifecycle performance evidence identity or platform is invalid")
	}
	if !IsCanonicalCommit(evidence.Candidate.Commit) || evidence.Candidate.Dirty ||
		(expectedCommit != "" && evidence.Candidate.Commit != expectedCommit) ||
		!IsCanonicalCommit(evidence.Baseline.Commit) || evidence.Baseline.Dirty ||
		evidence.Baseline.Commit == evidence.Candidate.Commit {
		return errors.New("lifecycle performance candidate or baseline identity is invalid")
	}
	if expectedRuntime == nil || evidence.Runtime.Family != expectedRuntime.Family ||
		evidence.Runtime.Revision != expectedRuntime.Revision ||
		evidence.Runtime.ArtifactSHA256 != expectedRuntime.ArtifactSHA256 ||
		evidence.Runtime.BuildCommit != expectedRuntime.BuildCommit || evidence.Runtime.BuildDirty {
		return errors.New("lifecycle performance runtime identity is invalid")
	}
	if evidence.Methodology.Command != "hideout run -- git status --short" ||
		evidence.Methodology.Samples < 30 || evidence.Methodology.Warmups < 3 ||
		evidence.Methodology.SampleOrder != "paired-alternating-ab-ba" ||
		len(evidence.Methodology.FixtureSHA256) != 64 || !lowerHex(evidence.Methodology.FixtureSHA256) {
		return errors.New("lifecycle performance methodology is invalid")
	}
	if len(evidence.CandidateSamplesMS) != evidence.Methodology.Samples ||
		len(evidence.BaselineSamplesMS) != evidence.Methodology.Samples {
		return errors.New("lifecycle performance sample count does not match methodology")
	}
	candidateMedian, _, err := recomputeEvidenceStats(evidence.CandidateSamplesMS)
	if err != nil {
		return err
	}
	baselineMedian, _, err := recomputeEvidenceStats(evidence.BaselineSamplesMS)
	if err != nil || baselineMedian <= 0 {
		return errors.New("lifecycle performance baseline samples are invalid")
	}
	observedDelta := candidateMedian - baselineMedian
	allowedDelta := math.Max(baselineMedian*0.05, 10)
	for name, pair := range map[string][2]float64{
		"candidate median": {candidateMedian, evidence.CandidateMedianMS},
		"baseline median":  {baselineMedian, evidence.BaselineMedianMS},
		"observed delta":   {observedDelta, evidence.ObservedDeltaMS},
		"allowed delta":    {allowedDelta, evidence.AllowedDeltaMS},
	} {
		if !approximatelyEqual(pair[0], pair[1]) {
			return fmt.Errorf("lifecycle performance %s is not derived from samples", name)
		}
	}
	if observedDelta > allowedDelta {
		return errors.New("lifecycle performance regression exceeds 5 percent or 10 milliseconds")
	}
	return nil
}

func validateLifecycleArtifactCommit(commit, expected string) error {
	if !IsCanonicalCommit(commit) || (expected != "" && commit != strings.TrimSpace(expected)) {
		return errors.New("lifecycle evidence commit identity is invalid")
	}
	return nil
}

func validateExactBooleanChecks(name string, checks map[string]bool, required []string) error {
	if len(checks) != len(required) {
		return fmt.Errorf("%s evidence check inventory drifted", name)
	}
	for _, check := range required {
		if !checks[check] {
			return fmt.Errorf("%s evidence check %q did not pass", name, check)
		}
	}
	return nil
}

func boundedPositive(value, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0 && value <= maximum
}
