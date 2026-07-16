package productevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
)

const (
	Feature034 = "034-concurrent-run-sessions"

	Proof034Gate0Mechanics    = "034.concurrent-sessions.gate0.mechanics"
	Proof034RealIsolation     = "034.concurrent-sessions.real-gate2.isolation"
	Proof034RealPerformance   = "034.concurrent-sessions.real-gate2.performance"
	Proof034RealGate2NotRun   = "034.concurrent-sessions.real-gate2.not-run"
	Proof034DocsClaimBoundary = "034.concurrent-sessions.docs.claim-boundary"
)

var requiredConcurrentIsolationChecks = []string{
	"threeSameWorkspaceOwners",
	"distinctSessionIds",
	"distinctPidNamespaces",
	"distinctMountNamespaces",
	"nonRootTargets",
	"privateProc",
	"siblingPidHidden",
	"siblingRuntimeHidden",
	"guestRootPositiveControl",
	"hostfsOverlaySessionLocal",
	"forcedInterruptionTargetGone",
	"siblingSurvivedInterruption",
	"ownerReconciled",
	"stopRefusedWithLiveOwners",
	"lastSessionPreservedVm",
	"explicitStopStoppedVm",
}

type concurrentIsolationEvidence struct {
	Schema      string `json:"schema"`
	Status      string `json:"status"`
	GeneratedAt string `json:"generatedAt"`
	Commit      string `json:"commit"`
	Dirty       bool   `json:"dirty"`
	Backend     string `json:"backend"`
	Host        string `json:"host"`
	Metrics     struct {
		OwnerReconcileMS float64 `json:"ownerReconcileMs"`
	} `json:"metrics"`
	Checks    map[string]bool `json:"checks"`
	NonClaims struct {
		GuestRootContainment string `json:"guestRootContainment"`
	} `json:"nonClaims"`
}

type concurrentPerformanceEvidence struct {
	Schema      string `json:"schema"`
	Status      string `json:"status"`
	GeneratedAt string `json:"generatedAt"`
	Candidate   struct {
		Commit        string `json:"commit"`
		Dirty         bool   `json:"dirty"`
		EnvironmentID string `json:"environmentId"`
		Instance      string `json:"instance"`
	} `json:"candidate"`
	Baseline struct {
		Commit        string `json:"commit"`
		Dirty         bool   `json:"dirty"`
		EnvironmentID string `json:"environmentId"`
		Instance      string `json:"instance"`
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
		Samples               int     `json:"samples"`
		Warmups               int     `json:"warmups"`
		ReadyThresholdMS      float64 `json:"readyThresholdMs"`
		FixtureRatioThreshold float64 `json:"fixtureRatioThreshold"`
		FixtureSHA256         string  `json:"fixtureSHA256"`
	} `json:"methodology"`
	WarmAttach struct {
		SamplesMS []float64 `json:"samplesMs"`
		MedianMS  float64   `json:"medianMs"`
		P95MS     float64   `json:"p95Ms"`
	} `json:"warmAttach"`
	WorkspaceFixture struct {
		CandidateSamplesMS []float64 `json:"candidateSamplesMs"`
		BaselineSamplesMS  []float64 `json:"baselineSamplesMs"`
		CandidateMedianMS  float64   `json:"candidateMedianMs"`
		CandidateP95MS     float64   `json:"candidateP95Ms"`
		BaselineMedianMS   float64   `json:"baselineMedianMs"`
		BaselineP95MS      float64   `json:"baselineP95Ms"`
		P95Ratio           float64   `json:"p95Ratio"`
	} `json:"workspaceFixture"`
}

func validateRegisteredArtifact(validator string, refs []ArtifactRef, artifacts map[string][]byte, expectedCommit string, expectedRuntime *RuntimeExpectation) error {
	switch validator {
	case ArtifactValidatorNone:
		return nil
	case ArtifactValidatorConcurrentIsolationV1:
		data, err := singleJSONArtifact(refs, artifacts)
		if err != nil {
			return err
		}
		return validateConcurrentIsolationArtifact(data, expectedCommit)
	case ArtifactValidatorConcurrentPerformanceV1:
		data, err := singleJSONArtifact(refs, artifacts)
		if err != nil {
			return err
		}
		return validateConcurrentPerformanceArtifact(data, expectedCommit, expectedRuntime)
	default:
		return fmt.Errorf("unsupported artifact validator %q", validator)
	}
}

func singleJSONArtifact(refs []ArtifactRef, artifacts map[string][]byte) ([]byte, error) {
	if len(refs) != 1 {
		return nil, errors.New("semantic proof must contain exactly one authoritative artifact")
	}
	data, ok := artifacts[refs[0].Path]
	if !ok {
		return nil, errors.New("authoritative artifact bytes were not loaded")
	}
	return data, nil
}

func validateConcurrentIsolationArtifact(data []byte, expectedCommit string) error {
	var evidence concurrentIsolationEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("concurrent isolation evidence: %w", err)
	}
	if evidence.Schema != "hideout.concurrent-sessions-gate2/v1" || evidence.Status != "passed" || evidence.Backend != "lima" || evidence.Host != "macos-arm64" {
		return errors.New("concurrent isolation evidence identity or platform is invalid")
	}
	if !IsCanonicalCommit(evidence.Commit) || (expectedCommit != "" && evidence.Commit != expectedCommit) || evidence.Dirty {
		return errors.New("concurrent isolation evidence is not bound to the clean candidate commit")
	}
	if evidence.Metrics.OwnerReconcileMS <= 0 || evidence.Metrics.OwnerReconcileMS > 1000 {
		return errors.New("concurrent isolation owner liveness did not reconcile within one second")
	}
	if len(evidence.Checks) != len(requiredConcurrentIsolationChecks) {
		return errors.New("concurrent isolation evidence check inventory drifted")
	}
	for _, check := range requiredConcurrentIsolationChecks {
		if !evidence.Checks[check] {
			return fmt.Errorf("concurrent isolation evidence check %q did not pass", check)
		}
	}
	if evidence.NonClaims.GuestRootContainment != "not-claimed" {
		return errors.New("concurrent isolation evidence overclaims guest-root containment")
	}
	return nil
}

func validateConcurrentPerformanceArtifact(data []byte, expectedCommit string, expectedRuntime *RuntimeExpectation) error {
	var evidence concurrentPerformanceEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("concurrent performance evidence: %w", err)
	}
	if evidence.Schema != "hideout.concurrent-sessions-performance/v1" || evidence.Status != "passed" || evidence.Host.OS != "darwin" || evidence.Host.Arch != "arm64" {
		return errors.New("concurrent performance evidence identity or platform is invalid")
	}
	if !IsCanonicalCommit(evidence.Candidate.Commit) || evidence.Candidate.Dirty || (expectedCommit != "" && evidence.Candidate.Commit != expectedCommit) {
		return errors.New("concurrent performance candidate identity is invalid")
	}
	if !IsCanonicalCommit(evidence.Baseline.Commit) || evidence.Baseline.Dirty || evidence.Baseline.Commit == evidence.Candidate.Commit {
		return errors.New("concurrent performance baseline must be a different clean canonical commit")
	}
	if evidence.Methodology.Samples < 30 || evidence.Methodology.Warmups < 1 || evidence.Methodology.ReadyThresholdMS != 2000 || evidence.Methodology.FixtureRatioThreshold != 1.25 || len(evidence.Methodology.FixtureSHA256) != 64 {
		return errors.New("concurrent performance methodology is invalid")
	}
	if !lowerHex(evidence.Methodology.FixtureSHA256) {
		return errors.New("concurrent performance fixture digest is invalid")
	}
	if expectedRuntime == nil || evidence.Runtime.Family != expectedRuntime.Family || evidence.Runtime.Revision != expectedRuntime.Revision || evidence.Runtime.ArtifactSHA256 != expectedRuntime.ArtifactSHA256 || evidence.Runtime.BuildCommit != expectedRuntime.BuildCommit || evidence.Runtime.BuildDirty {
		return errors.New("concurrent performance runtime identity is invalid")
	}
	if len(evidence.WarmAttach.SamplesMS) != evidence.Methodology.Samples || len(evidence.WorkspaceFixture.CandidateSamplesMS) != evidence.Methodology.Samples || len(evidence.WorkspaceFixture.BaselineSamplesMS) != evidence.Methodology.Samples {
		return errors.New("concurrent performance sample count does not match methodology")
	}
	readyMedian, readyP95, err := recomputeEvidenceStats(evidence.WarmAttach.SamplesMS)
	if err != nil {
		return err
	}
	candidateMedian, candidateP95, err := recomputeEvidenceStats(evidence.WorkspaceFixture.CandidateSamplesMS)
	if err != nil {
		return err
	}
	baselineMedian, baselineP95, err := recomputeEvidenceStats(evidence.WorkspaceFixture.BaselineSamplesMS)
	if err != nil || baselineP95 <= 0 {
		return errors.New("concurrent performance baseline samples are invalid")
	}
	ratio := candidateP95 / baselineP95
	for name, pair := range map[string][2]float64{
		"warm median":      {readyMedian, evidence.WarmAttach.MedianMS},
		"warm p95":         {readyP95, evidence.WarmAttach.P95MS},
		"candidate median": {candidateMedian, evidence.WorkspaceFixture.CandidateMedianMS},
		"candidate p95":    {candidateP95, evidence.WorkspaceFixture.CandidateP95MS},
		"baseline median":  {baselineMedian, evidence.WorkspaceFixture.BaselineMedianMS},
		"baseline p95":     {baselineP95, evidence.WorkspaceFixture.BaselineP95MS},
		"p95 ratio":        {ratio, evidence.WorkspaceFixture.P95Ratio},
	} {
		if !approximatelyEqual(pair[0], pair[1]) {
			return fmt.Errorf("concurrent performance %s is not derived from samples", name)
		}
	}
	if readyP95 > evidence.Methodology.ReadyThresholdMS || ratio > evidence.Methodology.FixtureRatioThreshold {
		return errors.New("concurrent performance thresholds did not pass")
	}
	return nil
}

func decodeStrictEvidence(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil || !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("evidence contains trailing JSON")
		}
		return err
	}
	return nil
}

func recomputeEvidenceStats(values []float64) (float64, float64, error) {
	if len(values) == 0 {
		return 0, 0, errors.New("performance samples are empty")
	}
	copy := slices.Clone(values)
	for _, value := range copy {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return 0, 0, errors.New("performance sample is invalid")
		}
	}
	sort.Float64s(copy)
	percentile := func(p int) float64 {
		index := (len(copy)*p + 99) / 100
		return copy[index-1]
	}
	return percentile(50), percentile(95), nil
}

func approximatelyEqual(a, b float64) bool {
	tolerance := math.Max(0.0001, math.Abs(a)*0.0001)
	return math.Abs(a-b) <= tolerance
}

func lowerHex(value string) bool {
	for _, r := range strings.TrimSpace(value) {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return strings.TrimSpace(value) == value
}
