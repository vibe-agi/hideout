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

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
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
	"realPTYInitialSize",
	"realPTYResize",
	"fullscreenFixture",
	"interruptExitExact",
	"daemonCrashClientsUnblocked",
	"daemonCrashTerminalRestored",
	"daemonCrashTargetsReaped",
	"restartStaleOwnerFailedClosed",
	"explicitRecovery",
	"postRecoveryRun",
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
	Artifacts struct {
		SessionPTYEvidenceSHA256 string `json:"sessionPTYEvidenceSHA256"`
	} `json:"artifacts"`
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
		Samples              int     `json:"samples"`
		Warmups              int     `json:"warmups"`
		ReadyThresholdMS     float64 `json:"readyThresholdMs"`
		FixtureSHA256        string  `json:"fixtureSHA256"`
		CandidateSampling    string  `json:"candidateSampling"`
		MeasurementClock     string  `json:"measurementClock"`
		HostContentionPolicy string  `json:"hostContentionPolicy,omitempty"`
		HostQuietConfirmed   bool    `json:"hostQuietConfirmed,omitempty"`
	} `json:"methodology"`
	WarmAttach struct {
		SamplesMS []float64 `json:"samplesMs"`
		MedianMS  float64   `json:"medianMs"`
		P95MS     float64   `json:"p95Ms"`
	} `json:"warmAttach"`
	ReferenceWorkload *concurrentReferenceWorkloadEvidence `json:"referenceWorkload,omitempty"`
}

type concurrentReferenceWorkloadEvidence struct {
	Methodology struct {
		Workload                 string `json:"workload"`
		Samples                  int    `json:"samples"`
		Warmups                  int    `json:"warmups"`
		SampleOrder              string `json:"sampleOrder"`
		SamplePairing            string `json:"samplePairing"`
		OverheadAggregation      string `json:"overheadAggregation"`
		FixturePreparation       string `json:"fixturePreparation"`
		PairProximity            string `json:"pairProximity"`
		BackgroundObserverPolicy string `json:"backgroundObserverPolicy"`
		Clock                    string `json:"clock"`
		Percentile               string `json:"percentile"`
		UID                      uint64 `json:"uid"`
		OutputSHA256             string `json:"outputSHA256"`
	} `json:"methodology"`
	Baseline             concurrentReferenceElapsedArm    `json:"baseline"`
	Observed             concurrentReferenceElapsedArm    `json:"observed"`
	ObservationIntegrity concurrentObservationIntegrity   `json:"observationIntegrity"`
	ResourceUsage        concurrentReferenceResourceUsage `json:"resourceUsage"`
	ElapsedOverhead      struct {
		Unit            string                      `json:"unit"`
		Samples         []float64                   `json:"samples"`
		Median          float64                     `json:"median"`
		Threshold       float64                     `json:"threshold"`
		ThresholdPassed bool                        `json:"thresholdPassed"`
		Confidence      *concurrentMedianConfidence `json:"confidence,omitempty"`
	} `json:"elapsedOverhead"`
}

type concurrentMedianConfidence struct {
	Level           float64 `json:"level"`
	Method          string  `json:"method"`
	Rank            int     `json:"rank"`
	UpperBound      float64 `json:"upperBound"`
	ThresholdPassed bool    `json:"thresholdPassed"`
}

type concurrentReferenceElapsedArm struct {
	Unit    string    `json:"unit"`
	Samples []float64 `json:"samples"`
	Median  float64   `json:"median"`
	P95     float64   `json:"p95"`
}

type concurrentLocalDropCounters struct {
	Process uint64 `json:"process"`
	File    uint64 `json:"file"`
	Network uint64 `json:"network"`
	DNS     uint64 `json:"dns"`
}

type concurrentFileCollectorCounters struct {
	MatchedEvents     uint64 `json:"matchedEvents"`
	ReservedEvents    uint64 `json:"reservedEvents"`
	RingbufDrops      uint64 `json:"ringbufDrops"`
	StateDrops        uint64 `json:"stateDrops"`
	StateDegradations uint64 `json:"stateDegradations"`
	PathFailures      uint64 `json:"pathFailures"`
	IdentityFailures  uint64 `json:"identityFailures"`
}

type concurrentCoverageSample struct {
	SampleIndex           int                             `json:"sampleIndex"`
	Recorded              bool                            `json:"recorded"`
	SessionID             string                          `json:"sessionId"`
	DroppedEventCount     uint64                          `json:"droppedEventCount"`
	RingOverflow          bool                            `json:"ringOverflow"`
	KernelDropped         uint64                          `json:"kernelDropped"`
	RingDropped           uint64                          `json:"ringDropped"`
	LocalDropped          concurrentLocalDropCounters     `json:"localDropped"`
	FileCollectorCounters concurrentFileCollectorCounters `json:"fileCollectorCounters"`
}

type concurrentFileCounterReceipt struct {
	SampleIndex int    `json:"sampleIndex"`
	Recorded    bool   `json:"recorded"`
	SessionID   string `json:"sessionId"`
	concurrentFileCollectorCounters
}

type concurrentObservationIntegrity struct {
	FileBPFObjectSHA256   string                         `json:"fileBPFObjectSHA256"`
	CoverageSamples       []concurrentCoverageSample     `json:"coverageSamples"`
	FileCollectorCounters []concurrentFileCounterReceipt `json:"fileCollectorCounters"`
	NoReportedLoss        bool                           `json:"noReportedLoss"`
}

type concurrentReferenceResourceUsage struct {
	Scope             string                              `json:"scope"`
	Source            string                              `json:"source"`
	CPUTimeUnit       string                              `json:"cpuTimeUnit"`
	ContextSwitchUnit string                              `json:"contextSwitchUnit"`
	AcceptanceFilter  bool                                `json:"acceptanceFilter"`
	Samples           []concurrentReferenceResourceSample `json:"samples"`
}

type concurrentReferenceResourceSample struct {
	SampleIndex int                               `json:"sampleIndex"`
	Recorded    bool                              `json:"recorded"`
	Baseline    concurrentReferenceResourceValues `json:"baseline"`
	Observed    concurrentReferenceResourceValues `json:"observed"`
}

type concurrentReferenceResourceValues struct {
	UserMS                     float64 `json:"userMs"`
	SystemMS                   float64 `json:"systemMs"`
	VoluntaryContextSwitches   uint64  `json:"voluntaryContextSwitches"`
	InvoluntaryContextSwitches uint64  `json:"involuntaryContextSwitches"`
}

func validateRegisteredArtifact(validator string, refs []ArtifactRef, artifacts map[string][]byte, expectedCommit string, expectedPackage *PackageIdentity, expectedRuntime *RuntimeExpectation) error {
	switch validator {
	case ArtifactValidatorNone:
		return nil
	case ArtifactValidatorConcurrentIsolationV1:
		data, err := singleJSONArtifact(refs, artifacts)
		if err != nil {
			return err
		}
		return validateConcurrentIsolationArtifact(data, expectedCommit)
	case ArtifactValidatorConcurrentPerformanceV2:
		data, err := singleJSONArtifact(refs, artifacts)
		if err != nil {
			return err
		}
		return validateConcurrentPerformanceArtifact(data, expectedCommit, expectedRuntime)
	case ArtifactValidatorLifecycleLocalV1:
		data, err := singleJSONArtifact(refs, artifacts)
		if err != nil {
			return err
		}
		return validateLifecycleLocalArtifact(data, expectedCommit)
	case ArtifactValidatorLifecycleModelV1:
		data, err := singleJSONArtifact(refs, artifacts)
		if err != nil {
			return err
		}
		return validateLifecycleModelArtifact(data, expectedCommit)
	case ArtifactValidatorLifecycleRealV1:
		data, err := singleJSONArtifact(refs, artifacts)
		if err != nil {
			return err
		}
		return validateLifecycleRealArtifact(data, expectedCommit)
	case ArtifactValidatorLifecyclePerformanceV1:
		data, err := singleJSONArtifact(refs, artifacts)
		if err != nil {
			return err
		}
		return validateLifecyclePerformanceArtifact(data, expectedCommit, expectedRuntime)
	case ArtifactValidatorAttachReservationPerformanceV1:
		data, err := singleJSONArtifact(refs, artifacts)
		if err != nil {
			return err
		}
		return validateAttachReservationPerformanceArtifact(data, expectedCommit, expectedRuntime)
	case ArtifactValidatorSharedWorkspaceBehaviorV1:
		return validateSharedWorkspaceBehaviorArtifact(refs, artifacts, expectedCommit, expectedPackage, expectedRuntime)
	case ArtifactValidatorSharedWorkspacePerformanceV1:
		return validateSharedWorkspacePerformanceArtifact(refs, artifacts, expectedCommit, expectedPackage, expectedRuntime)
	case ArtifactValidatorWorkspaceExecutableV1:
		data, err := singleJSONArtifact(refs, artifacts)
		if err != nil {
			return err
		}
		return validateWorkspaceExecutableArtifact(data, expectedCommit)
	case ArtifactValidatorDisposableRecoveryV1:
		data, err := singleJSONArtifact(refs, artifacts)
		if err != nil {
			return err
		}
		return validateDisposableRecoveryArtifact(data, expectedCommit)
	case ArtifactValidatorProjectionReadinessV1:
		return validateProjectionReadinessArtifact(
			refs, artifacts, expectedCommit, expectedPackage, expectedRuntime, false,
		)
	case ArtifactValidatorProjectionPrivacyV1:
		return validateProjectionReadinessArtifact(
			refs, artifacts, expectedCommit, expectedPackage, expectedRuntime, true,
		)
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
	if len(evidence.Artifacts.SessionPTYEvidenceSHA256) != 64 || !lowerHex(evidence.Artifacts.SessionPTYEvidenceSHA256) {
		return errors.New("concurrent isolation PTY evidence digest is invalid")
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
	if (evidence.Schema != "hideout.concurrent-sessions-performance/v2" &&
		evidence.Schema != "hideout.concurrent-sessions-performance/v3" &&
		evidence.Schema != "hideout.concurrent-sessions-performance/v4") ||
		evidence.Status != "passed" || evidence.Host.OS != "darwin" ||
		evidence.Host.Arch != "arm64" {
		return errors.New("concurrent performance evidence identity or platform is invalid")
	}
	if evidence.Schema == "hideout.concurrent-sessions-performance/v2" &&
		evidence.ReferenceWorkload != nil {
		return errors.New("concurrent performance v2 evidence cannot carry a reference workload")
	}
	if (evidence.Schema == "hideout.concurrent-sessions-performance/v3" ||
		evidence.Schema == "hideout.concurrent-sessions-performance/v4") &&
		evidence.ReferenceWorkload == nil {
		return errors.New("concurrent performance reference evidence lacks the reference workload")
	}
	if !IsCanonicalCommit(evidence.Candidate.Commit) || evidence.Candidate.Dirty || (expectedCommit != "" && evidence.Candidate.Commit != expectedCommit) {
		return errors.New("concurrent performance candidate identity is invalid")
	}
	if evidence.Methodology.Samples < 30 || evidence.Methodology.Samples > 1000 ||
		evidence.Methodology.Warmups < 1 ||
		evidence.Methodology.ReadyThresholdMS != 2000 ||
		len(evidence.Methodology.FixtureSHA256) != 64 {
		return errors.New("concurrent performance methodology is invalid")
	}
	if evidence.Methodology.CandidateSampling != "per-run-host-invocation-to-first-target-byte" ||
		evidence.Methodology.MeasurementClock != "host-monotonic-observed-first-byte" {
		return errors.New("concurrent performance sampling boundary is invalid")
	}
	if !lowerHex(evidence.Methodology.FixtureSHA256) {
		return errors.New("concurrent performance fixture digest is invalid")
	}
	if evidence.Schema == "hideout.concurrent-sessions-performance/v4" &&
		(evidence.Methodology.HostContentionPolicy != "operator-confirmed-quiet-host-known-contention-invalidates-run" ||
			!evidence.Methodology.HostQuietConfirmed) {
		return errors.New("concurrent performance quiet-host evidence is invalid")
	}
	if expectedRuntime == nil || evidence.Runtime.Family != expectedRuntime.Family || evidence.Runtime.Revision != expectedRuntime.Revision || evidence.Runtime.ArtifactSHA256 != expectedRuntime.ArtifactSHA256 || evidence.Runtime.BuildCommit != expectedRuntime.BuildCommit || evidence.Runtime.BuildDirty {
		return errors.New("concurrent performance runtime identity is invalid")
	}
	if len(evidence.WarmAttach.SamplesMS) != evidence.Methodology.Samples {
		return errors.New("concurrent performance sample count does not match methodology")
	}
	readyMedian, readyP95, err := recomputeEvidenceStats(evidence.WarmAttach.SamplesMS)
	if err != nil {
		return err
	}
	for name, pair := range map[string][2]float64{
		"warm median": {readyMedian, evidence.WarmAttach.MedianMS},
		"warm p95":    {readyP95, evidence.WarmAttach.P95MS},
	} {
		if !approximatelyEqual(pair[0], pair[1]) {
			return fmt.Errorf("concurrent performance %s is not derived from samples", name)
		}
	}
	if readyP95 > evidence.Methodology.ReadyThresholdMS {
		return errors.New("concurrent performance thresholds did not pass")
	}
	if evidence.ReferenceWorkload != nil {
		if err := validateConcurrentReferenceWorkload(
			evidence.ReferenceWorkload,
			evidence.Methodology.Samples,
			evidence.Methodology.Warmups,
			evidence.Schema == "hideout.concurrent-sessions-performance/v4",
		); err != nil {
			return fmt.Errorf("concurrent performance reference workload: %w", err)
		}
	}
	return nil
}

func validateConcurrentReferenceWorkload(
	reference *concurrentReferenceWorkloadEvidence,
	samples, warmups int,
	requireConfidence bool,
) error {
	if reference == nil {
		return errors.New("reference workload is missing")
	}
	methodology := reference.Methodology
	if methodology.Workload != "single Python process parses 288MiB of source payload across 96 files, performs four in-memory SHA-256 passes per record, and writes bounded derived metadata" ||
		methodology.Samples != samples || methodology.Warmups != warmups ||
		methodology.SampleOrder != "alternating-baseline-observed" ||
		methodology.SamplePairing != "index-aligned-adjacent-counterbalanced" ||
		methodology.OverheadAggregation != "nearest-rank-median-of-paired-percent-deltas" ||
		methodology.FixturePreparation != "once-via-control-before-all-warmup-and-recorded-samples" ||
		methodology.PairProximity != "adjacent-halves-reuse-one-immutable-warmed-source-with-no-drain-sleep" ||
		methodology.BackgroundObserverPolicy != "concurrent-anchor-plus-arm-equivalent-inert-baseline-session" ||
		methodology.Clock != "guest-python-time.monotonic_ns" ||
		methodology.Percentile != "nearest-rank-ceiling" ||
		methodology.UID == 0 || methodology.UID > math.MaxUint32 ||
		len(methodology.OutputSHA256) != 64 ||
		!lowerHex(methodology.OutputSHA256) {
		return errors.New("reference methodology is invalid")
	}
	if err := validateConcurrentReferenceElapsedArm(
		"baseline",
		reference.Baseline,
		samples,
	); err != nil {
		return err
	}
	if err := validateConcurrentReferenceElapsedArm(
		"observed",
		reference.Observed,
		samples,
	); err != nil {
		return err
	}
	if err := validateConcurrentObservationIntegrity(
		reference.ObservationIntegrity,
		samples,
		warmups,
	); err != nil {
		return err
	}
	if err := validateConcurrentReferenceResources(
		reference.ResourceUsage,
		samples,
		warmups,
	); err != nil {
		return err
	}
	return validateConcurrentReferenceOverhead(
		reference.ElapsedOverhead.Unit,
		reference.ElapsedOverhead.Samples,
		reference.ElapsedOverhead.Median,
		reference.ElapsedOverhead.Threshold,
		reference.ElapsedOverhead.ThresholdPassed,
		reference.Baseline.Samples,
		reference.Observed.Samples,
		reference.ElapsedOverhead.Confidence,
		requireConfidence,
	)
}

func validateConcurrentReferenceElapsedArm(
	name string,
	arm concurrentReferenceElapsedArm,
	samples int,
) error {
	if arm.Unit != "milliseconds" || len(arm.Samples) != samples {
		return fmt.Errorf("reference %s sample inventory is invalid", name)
	}
	for _, value := range arm.Samples {
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
			return fmt.Errorf("reference %s sample is invalid", name)
		}
	}
	median, p95, err := recomputeEvidenceStats(arm.Samples)
	if err != nil {
		return fmt.Errorf("reference %s statistics: %w", name, err)
	}
	if !approximatelyEqual(median, arm.Median) ||
		!approximatelyEqual(p95, arm.P95) {
		return fmt.Errorf("reference %s statistics are not derived from samples", name)
	}
	return nil
}

func validateConcurrentObservationIntegrity(
	integrity concurrentObservationIntegrity,
	samples, warmups int,
) error {
	manifest, err := observerbpf.VerifyEmbeddedFileArtifacts()
	if err != nil {
		return fmt.Errorf("verify embedded file BPF artifact: %w", err)
	}
	if len(integrity.FileBPFObjectSHA256) != 64 ||
		!lowerHex(integrity.FileBPFObjectSHA256) ||
		integrity.FileBPFObjectSHA256 != manifest.ObjectSHA256 {
		return errors.New("reference file BPF object digest does not match the candidate")
	}
	if !integrity.NoReportedLoss {
		return errors.New("reference observation reported loss")
	}
	expected := samples + warmups
	if len(integrity.CoverageSamples) != expected ||
		len(integrity.FileCollectorCounters) != expected {
		return errors.New("reference observation integrity sample inventory is invalid")
	}
	sessionIDs := make(map[string]struct{}, expected)
	for index := range expected {
		coverageSample := integrity.CoverageSamples[index]
		counterReceipt := integrity.FileCollectorCounters[index]
		if !validConcurrentReferenceSample(
			coverageSample.SampleIndex,
			coverageSample.Recorded,
			coverageSample.SessionID,
			index+1,
			warmups,
		) ||
			!validConcurrentReferenceSample(
				counterReceipt.SampleIndex,
				counterReceipt.Recorded,
				counterReceipt.SessionID,
				index+1,
				warmups,
			) {
			return errors.New("reference observation sample identity is invalid")
		}
		if _, duplicate := sessionIDs[coverageSample.SessionID]; duplicate {
			return errors.New("reference observation session identity was reused")
		}
		sessionIDs[coverageSample.SessionID] = struct{}{}
		counters := coverageSample.FileCollectorCounters
		if coverageSample.DroppedEventCount != 0 ||
			coverageSample.RingOverflow || coverageSample.KernelDropped != 0 ||
			coverageSample.RingDropped != 0 ||
			coverageSample.LocalDropped != (concurrentLocalDropCounters{}) ||
			counters.MatchedEvents == 0 ||
			counters.MatchedEvents < counters.ReservedEvents ||
			counters.MatchedEvents-counters.ReservedEvents != counters.RingbufDrops ||
			counters.RingbufDrops != 0 || counters.StateDrops != 0 {
			return errors.New("reference observation reported loss or inconsistent counters")
		}
		expectedReceipt := concurrentFileCounterReceipt{
			SampleIndex:                     index + 1,
			Recorded:                        index+1 > warmups,
			SessionID:                       coverageSample.SessionID,
			concurrentFileCollectorCounters: counters,
		}
		if counterReceipt != expectedReceipt {
			return errors.New("reference file collector counter receipt diverged")
		}
	}
	return nil
}

func validateConcurrentReferenceResources(
	resources concurrentReferenceResourceUsage,
	samples, warmups int,
) error {
	if resources.Scope != "reference-workload-child-process" ||
		resources.Source != "getrusage(RUSAGE_CHILDREN)" ||
		resources.CPUTimeUnit != "milliseconds" ||
		resources.ContextSwitchUnit != "count" ||
		resources.AcceptanceFilter ||
		len(resources.Samples) != samples+warmups {
		return errors.New("reference resource diagnostics are invalid")
	}
	for index, sample := range resources.Samples {
		if !validConcurrentReferenceIndex(
			sample.SampleIndex,
			sample.Recorded,
			index+1,
			warmups,
		) ||
			!validConcurrentResourceValues(sample.Baseline) ||
			!validConcurrentResourceValues(sample.Observed) {
			return errors.New("reference resource diagnostic sample is invalid")
		}
	}
	return nil
}

func validConcurrentResourceValues(value concurrentReferenceResourceValues) bool {
	return !math.IsNaN(value.UserMS) && !math.IsInf(value.UserMS, 0) &&
		value.UserMS >= 0 && !math.IsNaN(value.SystemMS) &&
		!math.IsInf(value.SystemMS, 0) && value.SystemMS >= 0
}

func validateConcurrentReferenceOverhead(
	unit string,
	values []float64,
	median, threshold float64,
	thresholdPassed bool,
	baseline, observed []float64,
	confidence *concurrentMedianConfidence,
	requireConfidence bool,
) error {
	if unit != "percent" || len(values) != len(baseline) ||
		len(observed) != len(baseline) || len(values) == 0 {
		return errors.New("reference overhead sample inventory is invalid")
	}
	for index := range values {
		if baseline[index] <= 0 {
			return errors.New("reference overhead baseline is invalid")
		}
		expected := math.Round(
			((observed[index]-baseline[index])/baseline[index])*100000,
		) / 1000
		if math.IsNaN(values[index]) || math.IsInf(values[index], 0) ||
			!approximatelyEqual(values[index], expected) {
			return errors.New("reference overhead samples are not derived from pairs")
		}
	}
	recomputedMedian, err := nearestRankEvidence(values, 50)
	if err != nil || !approximatelyEqual(recomputedMedian, median) {
		return errors.New("reference overhead median is not derived from samples")
	}
	if !approximatelyEqual(threshold, 10) || !thresholdPassed || median > threshold {
		return errors.New("reference overhead threshold did not pass")
	}
	if err := validateConcurrentMedianConfidence(
		values,
		threshold,
		confidence,
		requireConfidence,
	); err != nil {
		return err
	}
	return nil
}

func validateConcurrentMedianConfidence(
	values []float64,
	threshold float64,
	confidence *concurrentMedianConfidence,
	required bool,
) error {
	if confidence == nil {
		if required {
			return errors.New("reference overhead confidence receipt is missing")
		}
		return nil
	}
	if !approximatelyEqual(confidence.Level, 0.95) ||
		confidence.Method != "one-sided-exact-binomial-order-statistic" {
		return errors.New("reference overhead confidence methodology is invalid")
	}
	rank, err := medianUpperConfidenceRank(len(values), confidence.Level)
	if err != nil || confidence.Rank != rank {
		return errors.New("reference overhead confidence rank is invalid")
	}
	ordered := slices.Clone(values)
	sort.Float64s(ordered)
	upperBound := ordered[rank-1]
	if !approximatelyEqual(confidence.UpperBound, upperBound) {
		return errors.New("reference overhead confidence bound is not derived from samples")
	}
	if !confidence.ThresholdPassed || upperBound > threshold {
		return errors.New("reference overhead confidence threshold did not pass")
	}
	return nil
}

func medianUpperConfidenceRank(sampleCount int, confidence float64) (int, error) {
	if sampleCount <= 0 || sampleCount > 1000 ||
		math.IsNaN(confidence) || math.IsInf(confidence, 0) ||
		confidence <= 0 || confidence >= 1 {
		return 0, errors.New("median confidence inputs are invalid")
	}
	probabilities := make([]float64, sampleCount+1)
	probabilities[0] = math.Ldexp(1, -sampleCount)
	for count := 1; count <= sampleCount; count++ {
		probabilities[count] = probabilities[count-1] *
			float64(sampleCount-count+1) / float64(count)
	}
	alpha := 1 - confidence
	for rank := 1; rank <= sampleCount; rank++ {
		var upperTail float64
		for count := rank; count <= sampleCount; count++ {
			upperTail += probabilities[count]
		}
		if upperTail <= alpha+1e-12 {
			return rank, nil
		}
	}
	return 0, errors.New("sample inventory cannot provide the requested median confidence")
}

func nearestRankEvidence(values []float64, percentile int) (float64, error) {
	if len(values) == 0 || percentile <= 0 || percentile > 100 {
		return 0, errors.New("performance samples are empty or percentile is invalid")
	}
	ordered := slices.Clone(values)
	for _, value := range ordered {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, errors.New("performance sample is invalid")
		}
	}
	sort.Float64s(ordered)
	index := (len(ordered)*percentile + 99) / 100
	return ordered[index-1], nil
}

func validConcurrentReferenceSample(
	sampleIndex int,
	recorded bool,
	sessionID string,
	expectedIndex, warmups int,
) bool {
	if !validConcurrentReferenceIndex(
		sampleIndex,
		recorded,
		expectedIndex,
		warmups,
	) ||
		!strings.HasPrefix(sessionID, "ses_") || len(sessionID) > 128 ||
		len(sessionID) == len("ses_") {
		return false
	}
	for _, value := range sessionID[len("ses_"):] {
		if (value >= 'a' && value <= 'z') ||
			(value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') || value == '_' || value == '-' {
			continue
		}
		return false
	}
	return true
}

func validConcurrentReferenceIndex(
	sampleIndex int,
	recorded bool,
	expectedIndex, warmups int,
) bool {
	return sampleIndex == expectedIndex &&
		recorded == (expectedIndex > warmups)
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
