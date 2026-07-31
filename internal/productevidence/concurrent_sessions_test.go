package productevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
)

func TestConcurrentIsolationSemanticValidatorRejectsArbitraryOrIncompleteEvidence(t *testing.T) {
	valid := concurrentIsolationFixture()
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateConcurrentIsolationArtifact(data, runtimePackageCommitFixture); err != nil {
		t.Fatalf("valid isolation evidence: %v", err)
	}
	delete(valid.Checks, "siblingPidHidden")
	data, _ = json.Marshal(valid)
	if err := validateConcurrentIsolationArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "inventory") {
		t.Fatalf("incomplete isolation evidence accepted: %v", err)
	}
	if err := validateConcurrentIsolationArtifact([]byte("all checks passed\n"), runtimePackageCommitFixture); err == nil {
		t.Fatal("arbitrary text satisfied the isolation validator")
	}
	valid = concurrentIsolationFixture()
	valid.Metrics.OwnerReconcileMS = 1000.001
	data, _ = json.Marshal(valid)
	if err := validateConcurrentIsolationArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "within one second") {
		t.Fatalf("slow owner reconciliation accepted: %v", err)
	}
	valid = concurrentIsolationFixture()
	valid.Artifacts.SessionPTYEvidenceSHA256 = ""
	data, _ = json.Marshal(valid)
	if err := validateConcurrentIsolationArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "PTY evidence digest") {
		t.Fatalf("missing PTY evidence digest accepted: %v", err)
	}
	valid = concurrentIsolationFixture()
	valid.Checks["daemonCrashTargetsReaped"] = false
	data, _ = json.Marshal(valid)
	if err := validateConcurrentIsolationArtifact(data, runtimePackageCommitFixture); err == nil || !strings.Contains(err.Error(), "daemonCrashTargetsReaped") {
		t.Fatalf("failed daemon-crash check accepted: %v", err)
	}
}

func TestConcurrentPerformanceSemanticValidatorRecomputesStatisticsAndIdentity(t *testing.T) {
	expected := runtimeExpectationFixture()
	valid := concurrentPerformanceFixture(expected)
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateConcurrentPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err != nil {
		t.Fatalf("valid performance evidence: %v", err)
	}

	valid.WarmAttach.P95MS = 1
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil || !strings.Contains(err.Error(), "not derived") {
		t.Fatalf("tampered statistics accepted: %v", err)
	}
	valid = concurrentPerformanceFixture(expected)
	valid.Methodology.CandidateSampling = "inner-workload-only"
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(data, runtimePackageCommitFixture, &expected); err == nil || !strings.Contains(err.Error(), "sampling boundary") {
		t.Fatalf("misrepresented sampling boundary accepted: %v", err)
	}
}

func TestConcurrentPerformanceV3ValidatesReferenceIntegrityAndDiagnostics(t *testing.T) {
	expected := runtimeExpectationFixture()
	valid := concurrentPerformanceV3Fixture(t, expected)
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateConcurrentPerformanceArtifact(
		data,
		runtimePackageCommitFixture,
		&expected,
	); err != nil {
		t.Fatalf("valid v3 performance evidence: %v", err)
	}

	reference := valid["referenceWorkload"].(map[string]any)
	integrity := reference["observationIntegrity"].(map[string]any)
	integrity["noReportedLoss"] = false
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(
		data,
		runtimePackageCommitFixture,
		&expected,
	); err == nil || !strings.Contains(err.Error(), "reported loss") {
		t.Fatalf("reported observer loss accepted: %v", err)
	}

	valid = concurrentPerformanceV3Fixture(t, expected)
	reference = valid["referenceWorkload"].(map[string]any)
	integrity = reference["observationIntegrity"].(map[string]any)
	integrity["fileBPFObjectSHA256"] = strings.Repeat("0", 64)
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(
		data,
		runtimePackageCommitFixture,
		&expected,
	); err == nil || !strings.Contains(err.Error(), "BPF") {
		t.Fatalf("wrong BPF digest accepted: %v", err)
	}

	valid = concurrentPerformanceV3Fixture(t, expected)
	reference = valid["referenceWorkload"].(map[string]any)
	overhead := reference["elapsedOverhead"].(map[string]any)
	overhead["samples"] = []float64{1}
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(
		data,
		runtimePackageCommitFixture,
		&expected,
	); err == nil || !strings.Contains(err.Error(), "overhead") {
		t.Fatalf("tampered overhead samples accepted: %v", err)
	}

	valid = concurrentPerformanceV3Fixture(t, expected)
	reference = valid["referenceWorkload"].(map[string]any)
	integrity = reference["observationIntegrity"].(map[string]any)
	coverageSamples := integrity["coverageSamples"].([]any)
	fileCounters := integrity["fileCollectorCounters"].([]any)
	firstSessionID := coverageSamples[0].(map[string]any)["sessionId"]
	coverageSamples[1].(map[string]any)["sessionId"] = firstSessionID
	fileCounters[1].(map[string]any)["sessionId"] = firstSessionID
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(
		data,
		runtimePackageCommitFixture,
		&expected,
	); err == nil || !strings.Contains(err.Error(), "reused") {
		t.Fatalf("reused reference session accepted: %v", err)
	}
}

func TestConcurrentPerformanceV4RequiresQuietHostAndMedianConfidence(t *testing.T) {
	expected := runtimeExpectationFixture()
	valid := concurrentPerformanceV4Fixture(t, expected)
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateConcurrentPerformanceArtifact(
		data,
		runtimePackageCommitFixture,
		&expected,
	); err != nil {
		t.Fatalf("valid v4 performance evidence: %v", err)
	}

	valid = concurrentPerformanceV4Fixture(t, expected)
	methodology := valid["methodology"].(map[string]any)
	methodology["hostQuietConfirmed"] = false
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(
		data,
		runtimePackageCommitFixture,
		&expected,
	); err == nil || !strings.Contains(err.Error(), "quiet-host") {
		t.Fatalf("unconfirmed quiet host accepted: %v", err)
	}

	valid = concurrentPerformanceV4Fixture(t, expected)
	reference := valid["referenceWorkload"].(map[string]any)
	overhead := reference["elapsedOverhead"].(map[string]any)
	delete(overhead, "confidence")
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(
		data,
		runtimePackageCommitFixture,
		&expected,
	); err == nil || !strings.Contains(err.Error(), "confidence receipt") {
		t.Fatalf("missing confidence receipt accepted: %v", err)
	}

	valid = concurrentPerformanceV4Fixture(t, expected)
	reference = valid["referenceWorkload"].(map[string]any)
	overhead = reference["elapsedOverhead"].(map[string]any)
	confidence := overhead["confidence"].(map[string]any)
	confidence["rank"] = 19
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(
		data,
		runtimePackageCommitFixture,
		&expected,
	); err == nil || !strings.Contains(err.Error(), "confidence rank") {
		t.Fatalf("tampered confidence rank accepted: %v", err)
	}

	valid = concurrentPerformanceV4Fixture(t, expected)
	reference = valid["referenceWorkload"].(map[string]any)
	baselineArm := reference["baseline"].(map[string]any)
	observedArm := reference["observed"].(map[string]any)
	baseline := make([]float64, 30)
	observed := make([]float64, 30)
	overheadSamples := make([]float64, 30)
	for index := range 30 {
		baseline[index] = 100
		if index < 19 {
			observed[index] = 105
			overheadSamples[index] = 5
		} else {
			observed[index] = 112
			overheadSamples[index] = 12
		}
	}
	baselineArm["samples"] = baseline
	baselineArm["median"] = float64(100)
	baselineArm["p95"] = float64(100)
	observedArm["samples"] = observed
	observedArm["median"] = float64(105)
	observedArm["p95"] = float64(112)
	overhead = reference["elapsedOverhead"].(map[string]any)
	overhead["samples"] = overheadSamples
	overhead["median"] = float64(5)
	overhead["thresholdPassed"] = true
	confidence = overhead["confidence"].(map[string]any)
	confidence["upperBound"] = float64(12)
	confidence["thresholdPassed"] = true
	data, _ = json.Marshal(valid)
	if err := validateConcurrentPerformanceArtifact(
		data,
		runtimePackageCommitFixture,
		&expected,
	); err == nil || !strings.Contains(err.Error(), "confidence threshold") {
		t.Fatalf("noisy median-only pass accepted: %v", err)
	}
}

func TestMedianUpperConfidenceRankIsExactForThirtySamples(t *testing.T) {
	rank, err := medianUpperConfidenceRank(30, 0.95)
	if err != nil {
		t.Fatal(err)
	}
	if rank != 20 {
		t.Fatalf("rank=%d want=20", rank)
	}
	if _, err := medianUpperConfidenceRank(3, 0.95); err == nil {
		t.Fatal("three samples unexpectedly provided a one-sided 95% median bound")
	}
}

func TestEvaluateConcurrentIsolationUsesRegisteredSemanticValidator(t *testing.T) {
	var requirement ProofRequirement
	for _, candidate := range RequirementsForFeature(Feature034) {
		if candidate.ProofID == Proof034RealIsolation {
			requirement = candidate
			break
		}
	}
	if requirement.ProofID == "" {
		t.Fatal("registered isolation requirement missing")
	}
	root := t.TempDir()
	data := []byte("passed according to a shell script\n")
	path := filepath.Join(root, "result.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	expected := runtimeExpectationFixture()
	proof := runtimeProofFixture(requirement, runtimeBindingFixture())
	proof.EvidenceClass = requirement.RequiredEvidenceClass
	proof.Artifacts = []ArtifactRef{{Kind: "manifest", Path: "result.json", SHA256: hex.EncodeToString(sum[:]), RedactionStatus: RedactionPassed}}
	opts := runtimeEvaluationOptions([]ProofRequirement{requirement}, expected)
	opts.ArtifactRoot = root
	report, err := EvaluateManifest(runtimeManifestWithProofs(expected, proof), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Results[0]; got.Status != EvalArtifactInvalid {
		t.Fatalf("arbitrary artifact status=%+v", got)
	}
}

func concurrentIsolationFixture() concurrentIsolationEvidence {
	evidence := concurrentIsolationEvidence{
		Schema: "hideout.concurrent-sessions-gate2/v1", Status: "passed",
		GeneratedAt: "2026-07-16T12:00:00Z", Commit: runtimePackageCommitFixture,
		Backend: "lima", Host: "macos-arm64", Checks: map[string]bool{},
	}
	for _, check := range requiredConcurrentIsolationChecks {
		evidence.Checks[check] = true
	}
	evidence.NonClaims.GuestRootContainment = "not-claimed"
	evidence.Metrics.OwnerReconcileMS = 125
	evidence.Artifacts.SessionPTYEvidenceSHA256 = strings.Repeat("d", 64)
	return evidence
}

func concurrentPerformanceFixture(expected RuntimeExpectation) concurrentPerformanceEvidence {
	var evidence concurrentPerformanceEvidence
	evidence.Schema = "hideout.concurrent-sessions-performance/v2"
	evidence.Status = "passed"
	evidence.GeneratedAt = "2026-07-16T12:00:00Z"
	evidence.Candidate.Commit = runtimePackageCommitFixture
	evidence.Candidate.EnvironmentID = "env_candidate"
	evidence.Candidate.Instance = "candidate"
	evidence.Host.OS = "darwin"
	evidence.Host.Arch = "arm64"
	evidence.Runtime.Family = expected.Family
	evidence.Runtime.Revision = expected.Revision
	evidence.Runtime.ArtifactSHA256 = expected.ArtifactSHA256
	evidence.Runtime.BuildCommit = expected.BuildCommit
	evidence.Methodology.Samples = 30
	evidence.Methodology.Warmups = 3
	evidence.Methodology.ReadyThresholdMS = 2000
	evidence.Methodology.FixtureSHA256 = strings.Repeat("c", 64)
	evidence.Methodology.CandidateSampling = "per-run-host-invocation-to-first-target-byte"
	evidence.Methodology.MeasurementClock = "host-monotonic-observed-first-byte"
	for range 30 {
		evidence.WarmAttach.SamplesMS = append(evidence.WarmAttach.SamplesMS, 100)
	}
	evidence.WarmAttach.MedianMS = 100
	evidence.WarmAttach.P95MS = 100
	return evidence
}

func concurrentPerformanceV3Fixture(
	t *testing.T,
	expected RuntimeExpectation,
) map[string]any {
	t.Helper()
	base, err := json.Marshal(concurrentPerformanceFixture(expected))
	if err != nil {
		t.Fatal(err)
	}
	var evidence map[string]any
	if err := json.Unmarshal(base, &evidence); err != nil {
		t.Fatal(err)
	}
	evidence["schema"] = "hideout.concurrent-sessions-performance/v3"
	evidence["referenceWorkload"] = concurrentReferenceWorkloadFixture(t, 30, 3)
	return evidence
}

func concurrentPerformanceV4Fixture(
	t *testing.T,
	expected RuntimeExpectation,
) map[string]any {
	t.Helper()
	evidence := concurrentPerformanceV3Fixture(t, expected)
	evidence["schema"] = "hideout.concurrent-sessions-performance/v4"
	methodology := evidence["methodology"].(map[string]any)
	methodology["hostContentionPolicy"] =
		"operator-confirmed-quiet-host-known-contention-invalidates-run"
	methodology["hostQuietConfirmed"] = true
	reference := evidence["referenceWorkload"].(map[string]any)
	overhead := reference["elapsedOverhead"].(map[string]any)
	overhead["confidence"] = map[string]any{
		"level":           0.95,
		"method":          "one-sided-exact-binomial-order-statistic",
		"rank":            20,
		"upperBound":      5,
		"thresholdPassed": true,
	}
	return evidence
}

func concurrentReferenceWorkloadFixture(
	t *testing.T,
	samples, warmups int,
) map[string]any {
	t.Helper()
	manifest, err := observerbpf.EmbeddedFileArtifactManifest()
	if err != nil {
		t.Fatal(err)
	}
	baseline := make([]float64, samples)
	observed := make([]float64, samples)
	overhead := make([]float64, samples)
	for index := range samples {
		baseline[index] = 100
		observed[index] = 105
		overhead[index] = 5
	}
	coverageSamples := make([]any, 0, samples+warmups)
	fileCounters := make([]any, 0, samples+warmups)
	resourceSamples := make([]any, 0, samples+warmups)
	for index := 1; index <= samples+warmups; index++ {
		recorded := index > warmups
		counters := map[string]any{
			"matchedEvents":     100 + index,
			"reservedEvents":    100 + index,
			"ringbufDrops":      0,
			"stateDrops":        0,
			"stateDegradations": 2,
			"pathFailures":      0,
			"identityFailures":  0,
		}
		sessionID := fmt.Sprintf("ses_performance_%d", index)
		coverageSamples = append(coverageSamples, map[string]any{
			"sampleIndex":       index,
			"recorded":          recorded,
			"sessionId":         sessionID,
			"droppedEventCount": 0,
			"ringOverflow":      false,
			"kernelDropped":     0,
			"ringDropped":       0,
			"localDropped": map[string]any{
				"process": 0, "file": 0, "network": 0, "dns": 0,
			},
			"fileCollectorCounters": counters,
		})
		counterReceipt := map[string]any{
			"sampleIndex": index,
			"recorded":    recorded,
			"sessionId":   sessionID,
		}
		for key, value := range counters {
			counterReceipt[key] = value
		}
		fileCounters = append(fileCounters, counterReceipt)
		resourceSamples = append(resourceSamples, map[string]any{
			"sampleIndex": index,
			"recorded":    recorded,
			"baseline": map[string]any{
				"userMs": 80, "systemMs": 10,
				"voluntaryContextSwitches":   2,
				"involuntaryContextSwitches": 3,
			},
			"observed": map[string]any{
				"userMs": 84, "systemMs": 12,
				"voluntaryContextSwitches":   3,
				"involuntaryContextSwitches": 5,
			},
		})
	}
	return map[string]any{
		"methodology": map[string]any{
			"workload": "single Python process parses 288MiB of source payload across 96 files, performs four in-memory SHA-256 passes per record, and writes bounded derived metadata",
			"samples":  samples, "warmups": warmups,
			"sampleOrder":              "alternating-baseline-observed",
			"samplePairing":            "index-aligned-adjacent-counterbalanced",
			"overheadAggregation":      "nearest-rank-median-of-paired-percent-deltas",
			"fixturePreparation":       "once-via-control-before-all-warmup-and-recorded-samples",
			"pairProximity":            "adjacent-halves-reuse-one-immutable-warmed-source-with-no-drain-sleep",
			"backgroundObserverPolicy": "concurrent-anchor-plus-arm-equivalent-inert-baseline-session",
			"clock":                    "guest-python-time.monotonic_ns",
			"percentile":               "nearest-rank-ceiling",
			"uid":                      1000,
			"outputSHA256":             strings.Repeat("d", 64),
		},
		"baseline": map[string]any{
			"unit": "milliseconds", "samples": baseline,
			"median": 100, "p95": 100,
		},
		"observed": map[string]any{
			"unit": "milliseconds", "samples": observed,
			"median": 105, "p95": 105,
		},
		"observationIntegrity": map[string]any{
			"fileBPFObjectSHA256":   manifest.ObjectSHA256,
			"coverageSamples":       coverageSamples,
			"fileCollectorCounters": fileCounters,
			"noReportedLoss":        true,
		},
		"resourceUsage": map[string]any{
			"scope":             "reference-workload-child-process",
			"source":            "getrusage(RUSAGE_CHILDREN)",
			"cpuTimeUnit":       "milliseconds",
			"contextSwitchUnit": "count",
			"acceptanceFilter":  false,
			"samples":           resourceSamples,
		},
		"elapsedOverhead": map[string]any{
			"unit": "percent", "samples": overhead,
			"median": 5, "threshold": 10, "thresholdPassed": true,
		},
	}
}
