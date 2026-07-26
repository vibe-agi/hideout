package productevidence

import (
	"errors"
	"fmt"
	"math"
)

const (
	Feature040 = "040-lifecycle-attach-reservation"

	Proof040Gate0Mechanics    = "040.attach-reservation.gate0.mechanics"
	Proof040Gate0Model        = "040.attach-reservation.gate0.model"
	Proof040RealLifecycle     = "040.attach-reservation.real-gate2.lifecycle"
	Proof040RealPerformance   = "040.attach-reservation.real-gate2.performance"
	Proof040RealGate2NotRun   = "040.attach-reservation.real-gate2.not-run"
	Proof040DocsClaimBoundary = "040.attach-reservation.docs.claim-boundary"

	attachReservationBaselineCommit = "322c3c6cc9561eea21d4ed20ab78172429654c54"
)

type attachReservationPerformanceEvidence struct {
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
		Command            string `json:"command"`
		Samples            int    `json:"samples"`
		Warmups            int    `json:"warmups"`
		FixtureSHA256      string `json:"fixtureSHA256"`
		SampleOrder        string `json:"sampleOrder"`
		WorkspaceIsolation string `json:"workspaceIsolation"`
	} `json:"methodology"`
	CandidateSamplesMS        []float64 `json:"candidateSamplesMs"`
	BaselineSamplesMS         []float64 `json:"baselineSamplesMs"`
	CandidateMedianMS         float64   `json:"candidateMedianMs"`
	CandidateP95MS            float64   `json:"candidateP95Ms"`
	CandidateWithinTwoSeconds int       `json:"candidateWithinTwoSeconds"`
	CandidateSampleCount      int       `json:"candidateSampleCount"`
	BaselineMedianMS          float64   `json:"baselineMedianMs"`
	ObservedDeltaMS           float64   `json:"observedDeltaMs"`
	AllowedDeltaMS            float64   `json:"allowedDeltaMs"`
}

func validateAttachReservationPerformanceArtifact(
	data []byte,
	expectedCommit string,
	expectedRuntime *RuntimeExpectation,
) error {
	var evidence attachReservationPerformanceEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("attach-reservation performance evidence: %w", err)
	}
	if evidence.Schema != "hideout.attach-reservation-performance/v1" ||
		evidence.Status != "passed" || evidence.Host.OS != "darwin" ||
		evidence.Host.Arch != "arm64" {
		return errors.New("attach-reservation performance evidence identity or platform is invalid")
	}
	if !IsCanonicalCommit(evidence.Candidate.Commit) || evidence.Candidate.Dirty ||
		(expectedCommit != "" && evidence.Candidate.Commit != expectedCommit) ||
		evidence.Baseline.Commit != attachReservationBaselineCommit ||
		evidence.Baseline.Dirty || evidence.Baseline.Commit == evidence.Candidate.Commit {
		return errors.New("attach-reservation performance candidate or baseline identity is invalid")
	}
	if expectedRuntime == nil || evidence.Runtime.Family != expectedRuntime.Family ||
		evidence.Runtime.Revision != expectedRuntime.Revision ||
		evidence.Runtime.ArtifactSHA256 != expectedRuntime.ArtifactSHA256 ||
		evidence.Runtime.BuildCommit != expectedRuntime.BuildCommit ||
		evidence.Runtime.BuildDirty {
		return errors.New("attach-reservation performance runtime identity is invalid")
	}
	if evidence.Methodology.Command != "hideout run -- git status --short" ||
		evidence.Methodology.Samples < 30 || evidence.Methodology.Warmups < 3 ||
		evidence.Methodology.SampleOrder != "paired-alternating-ab-ba" ||
		evidence.Methodology.WorkspaceIsolation != "separate-physical-fixtures" ||
		len(evidence.Methodology.FixtureSHA256) != 64 ||
		!lowerHex(evidence.Methodology.FixtureSHA256) {
		return errors.New("attach-reservation performance methodology is invalid")
	}
	if len(evidence.CandidateSamplesMS) != evidence.Methodology.Samples ||
		len(evidence.BaselineSamplesMS) != evidence.Methodology.Samples ||
		evidence.CandidateSampleCount != evidence.Methodology.Samples {
		return errors.New("attach-reservation performance sample count does not match methodology")
	}
	candidateMedian, candidateP95, err := recomputeEvidenceStats(evidence.CandidateSamplesMS)
	if err != nil {
		return err
	}
	baselineMedian, _, err := recomputeEvidenceStats(evidence.BaselineSamplesMS)
	if err != nil || baselineMedian <= 0 {
		return errors.New("attach-reservation performance baseline samples are invalid")
	}
	withinTwoSeconds := 0
	for _, sample := range evidence.CandidateSamplesMS {
		if sample <= 2000 {
			withinTwoSeconds++
		}
	}
	observedDelta := candidateMedian - baselineMedian
	allowedDelta := math.Max(baselineMedian*0.05, 10)
	for name, pair := range map[string][2]float64{
		"candidate median": {candidateMedian, evidence.CandidateMedianMS},
		"candidate p95":    {candidateP95, evidence.CandidateP95MS},
		"baseline median":  {baselineMedian, evidence.BaselineMedianMS},
		"observed delta":   {observedDelta, evidence.ObservedDeltaMS},
		"allowed delta":    {allowedDelta, evidence.AllowedDeltaMS},
	} {
		if !approximatelyEqual(pair[0], pair[1]) {
			return fmt.Errorf("attach-reservation performance %s is not derived from samples", name)
		}
	}
	if evidence.CandidateWithinTwoSeconds != withinTwoSeconds {
		return errors.New("attach-reservation performance two-second count is not derived from samples")
	}
	if candidateP95 > 2000 || withinTwoSeconds*100 < evidence.CandidateSampleCount*95 {
		return errors.New("attach-reservation performance exceeds the two-second warm target contract")
	}
	return nil
}
