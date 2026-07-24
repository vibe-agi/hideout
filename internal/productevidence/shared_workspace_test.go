package productevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/packagekit"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestProofRegistryCovers035WithStrictRealAndSupportingNotRunEvidence(t *testing.T) {
	want := []string{
		Proof035Gate0Mechanics,
		Proof035RealBehavior,
		Proof035RealPerformance,
		Proof035RealGate2NotRun,
		Proof035DocsClaimBoundary,
	}
	requirements := RequirementsForFeature(Feature035)
	if len(requirements) != len(want) || len(Required035ProofIDs) != len(want) {
		t.Fatalf("035 requirements=%d requiredIDs=%d want %d", len(requirements), len(Required035ProofIDs), len(want))
	}
	seen := map[string]ProofRequirement{}
	covered := map[string]bool{}
	for _, requirement := range requirements {
		seen[requirement.ProofID] = requirement
		for _, claimID := range requirement.ClaimIDs {
			covered[claimID] = true
		}
	}
	for _, proofID := range want {
		if _, ok := seen[proofID]; !ok {
			t.Fatalf("035 proof %s is not registered", proofID)
		}
	}
	for _, proofID := range []string{Proof035RealBehavior, Proof035RealPerformance} {
		requirement := seen[proofID]
		if requirement.Layer != LayerRealGate || requirement.RequiredFor != RequiredForReleaseCandidate ||
			requirement.RuntimePolicy != RuntimePolicyExactReal || requirement.ArtifactPolicy == ArtifactPolicyNone ||
			requirement.RequiredEvidenceClass == "" || requirement.ArtifactValidator == ArtifactValidatorNone {
			t.Fatalf("035 real proof %s has weak scope: %+v", proofID, requirement)
		}
	}
	if seen[Proof035RealGate2NotRun].RequiredFor != RequiredForSupportingOnly ||
		seen[Proof035RealGate2NotRun].RuntimePolicy != RuntimePolicyNone {
		t.Fatalf("035 not-run proof could satisfy a real claim: %+v", seen[Proof035RealGate2NotRun])
	}
	for index := 1; index <= 41; index++ {
		claimID := fmt.Sprintf("035.FR-%03d", index)
		if !covered[claimID] {
			t.Fatalf("035 registry does not cover %s", claimID)
		}
	}
	for index := 1; index <= 23; index++ {
		claimID := fmt.Sprintf("035.SC-%03d", index)
		if !covered[claimID] {
			t.Fatalf("035 registry does not cover %s", claimID)
		}
	}
	if !slices.Contains(Required035ProofIDs, Proof035DocsClaimBoundary) {
		t.Fatal("035 registry JSON source omitted docs claim-boundary proof")
	}
}

func TestRetainedSharedWorkspaceEvidencePassesProductionEvaluator(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("HIDEOUT_035_EVIDENCE_DIR"))
	if root == "" {
		t.Skip("set HIDEOUT_035_EVIDENCE_DIR to validate retained real Gate 2 evidence")
	}
	manifest, err := ReadFile(filepath.Join(root, "product-hardening-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.PackageIdentity == nil {
		t.Fatal("retained 035 evidence has no package identity")
	}
	var runtimeBinding *RuntimeBinding
	for _, proof := range manifest.Proofs {
		if proof.ProofID != Proof035RealBehavior && proof.ProofID != Proof035RealPerformance {
			continue
		}
		if proof.Runtime == nil {
			t.Fatalf("real proof %s has no runtime binding", proof.ProofID)
		}
		if runtimeBinding == nil {
			copy := *proof.Runtime
			runtimeBinding = &copy
		} else if *runtimeBinding != *proof.Runtime {
			t.Fatalf("real proofs carry different runtime bindings: %+v != %+v", *runtimeBinding, *proof.Runtime)
		}
	}
	if runtimeBinding == nil {
		t.Fatal("retained 035 evidence has no real proofs")
	}
	requirements := make([]ProofRequirement, 0, 2)
	for _, requirement := range RequirementsForFeature(Feature035) {
		if requirement.ProofID == Proof035RealBehavior || requirement.ProofID == Proof035RealPerformance {
			requirements = append(requirements, requirement)
		}
	}
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements:    requirements,
		Target:          RequiredForReleaseCandidate,
		ExpectedCommit:  manifest.Commit,
		ExpectedPackage: manifest.PackageIdentity,
		ArtifactRoots: map[string]string{
			Proof035RealBehavior:    root,
			Proof035RealPerformance: root,
		},
		ExpectedRuntime: &RuntimeExpectation{
			Family: runtimeBinding.Family, Revision: runtimeBinding.Revision,
			ArtifactSHA256: runtimeBinding.ArtifactSHA256,
			HostOS:         runtimeBinding.HostOS, HostArch: runtimeBinding.HostArch,
			GuestArch: runtimeBinding.GuestArch, BuildCommit: runtimeBinding.BuildCommit,
			RequireClean: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := report.RequireSatisfied(); err != nil {
		t.Fatalf("retained 035 evidence did not satisfy production evaluation: %v\n%+v", err, report.Results)
	}
}

func TestSharedWorkspaceBehaviorValidatorRejectsFalseGreenArtifacts(t *testing.T) {
	fixture := newSharedWorkspaceBehaviorFixture(t)
	if err := fixture.validate(); err != nil {
		t.Fatalf("valid behavior evidence: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*sharedWorkspaceBehaviorTestFixture)
		want   string
	}{
		{name: "failed top-level check", mutate: func(f *sharedWorkspaceBehaviorTestFixture) {
			f.evidence.Checks["disjointIsolation"] = false
		}, want: "disjointIsolation"},
		{name: "failed relation check", mutate: func(f *sharedWorkspaceBehaviorTestFixture) {
			var value sharedWorkspaceRelationsEvidence
			mustDecodeSharedWorkspaceTest(t, f.artifacts["artifacts/behavior/relations.json"], &value)
			value.Checks["sameBootAcrossNestedRoots"] = false
			f.artifacts["artifacts/behavior/relations.json"] = mustMarshalSharedWorkspaceTest(t, value)
		}, want: "sameBootAcrossNestedRoots"},
		{name: "dirty package", mutate: func(f *sharedWorkspaceBehaviorTestFixture) {
			var value packagekit.Manifest
			mustDecodeSharedWorkspaceTest(t, f.artifacts["artifacts/behavior/package-manifest.json"], &value)
			value.Source.Dirty = true
			f.artifacts["artifacts/behavior/package-manifest.json"] = mustMarshalSharedWorkspaceTest(t, value)
		}, want: "package manifest identity"},
		{name: "wrong helper identity", mutate: func(f *sharedWorkspaceBehaviorTestFixture) {
			var value helperbin.Manifest
			mustDecodeSharedWorkspaceTest(t, f.artifacts["artifacts/behavior/workspace-portal-helper.manifest.json"], &value)
			value.Command = "arbitrary-host-command"
			f.artifacts["artifacts/behavior/workspace-portal-helper.manifest.json"] = mustMarshalSharedWorkspaceTest(t, value)
		}, want: "helper manifest identity"},
		{name: "altered research decision", mutate: func(f *sharedWorkspaceBehaviorTestFixture) {
			var value workspaceattach.ResearchDecision
			mustDecodeSharedWorkspaceTest(t, f.artifacts["artifacts/behavior/research-decision.json"], &value)
			value.SelectedCandidate = workspaceattach.CandidateVZ
			f.artifacts["artifacts/behavior/research-decision.json"] = mustMarshalSharedWorkspaceTest(t, value)
		}, want: "accepted workspace-portal research decision"},
		{name: "missing artifact", mutate: func(f *sharedWorkspaceBehaviorTestFixture) {
			delete(f.artifacts, "artifacts/behavior/lifecycle.json")
		}, want: "inventory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := newSharedWorkspaceBehaviorFixture(t)
			tc.mutate(&current)
			current.sync(t)
			if err := current.validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want substring %q", err, tc.want)
			}
		})
	}
}

func TestSharedWorkspacePerformanceValidatorRecomputesRawSamplesAndThresholds(t *testing.T) {
	fixture := newSharedWorkspacePerformanceFixture(t)
	if err := fixture.validate(); err != nil {
		t.Fatalf("valid performance evidence: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*sharedWorkspacePerformanceTestFixture)
		want   string
	}{
		{name: "summary not derived", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			f.artifacts[sharedWorkspaceCandidateRawPaths["git-status"]] = sharedWorkspaceSampleBytes(101, 30)
		}, want: "not derived"},
		{name: "too few samples", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			f.artifacts[sharedWorkspaceCandidateRawPaths["mount-ready"]] = sharedWorkspaceSampleBytes(100, 29)
		}, want: "sample count"},
		{name: "threshold regression relabeled passed", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			f.artifacts[sharedWorkspaceCandidateRawPaths["git-status"]] = sharedWorkspaceSampleBytes(3000, 30)
			f.artifacts[sharedWorkspacePairedSamplesPath] = sharedWorkspacePairedSampleBytes(3000, 100, 100, 100, 30)
			for index := range f.evidence.Metrics {
				if f.evidence.Metrics[index].ID == "git-status" {
					f.evidence.Metrics[index].Candidate.MedianMS = 3000
					f.evidence.Metrics[index].Candidate.P95MS = 3000
				}
			}
		}, want: "thresholds did not pass"},
		{name: "accepted threshold edited", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			f.evidence.Methodology.GitMedianAbsoluteMS = 4000
		}, want: "thresholds drifted"},
		{name: "fixture mismatch", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			f.artifacts["artifacts/performance/research-baseline/fixture.sha256"] = []byte(strings.Repeat("f", 64) + "\n")
		}, want: "fixture file"},
		{name: "failed correctness hidden", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			var value sharedWorkspaceCorrectnessEvidence
			mustDecodeSharedWorkspaceTest(t, f.artifacts["artifacts/performance/correctness.json"], &value)
			value.SymlinkEscapeRejected = false
			f.artifacts["artifacts/performance/correctness.json"] = mustMarshalSharedWorkspaceTest(t, value)
		}, want: "not derived"},
		{name: "missing raw artifact", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			delete(f.artifacts, sharedWorkspaceResearchBaselineRawPaths["first-byte"])
		}, want: "inventory"},
		{name: "control commit differs from candidate", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			f.evidence.FilesystemControl.Commit = strings.Repeat("b", 40)
		}, want: "filesystem control identity"},
		{name: "dirty filesystem control", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			f.evidence.FilesystemControl.Dirty = true
		}, want: "filesystem control identity"},
		{name: "filesystem control mechanism drifted", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			f.evidence.FilesystemControl.Mechanism = "producer-asserted-baseline"
		}, want: "filesystem control identity"},
		{name: "paired sample sides swapped", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			f.artifacts[sharedWorkspacePairedSamplesPath] = sharedWorkspacePairedSampleBytes(100, 200, 100, 100, 30)
		}, want: "not derived from paired observations"},
		{name: "filesystem control manifest tampered", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			f.control.GuestRoot = "/tmp/not-a-static-mount"
		}, want: "control manifest identity"},
		{name: "filesystem control manifest missing", mutate: func(f *sharedWorkspacePerformanceTestFixture) {
			f.omitControlManifest = true
		}, want: "artifact paths drifted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := newSharedWorkspacePerformanceFixture(t)
			tc.mutate(&current)
			current.sync(t)
			if err := current.validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want substring %q", err, tc.want)
			}
		})
	}
}

func TestSharedWorkspaceValidatorsRejectUnknownSummaryFields(t *testing.T) {
	fixture := newSharedWorkspacePerformanceFixture(t)
	var value map[string]any
	if err := json.Unmarshal(fixture.artifacts["performance.json"], &value); err != nil {
		t.Fatal(err)
	}
	value["allGoodAccordingToProducer"] = true
	fixture.artifacts["performance.json"] = mustMarshalSharedWorkspaceTest(t, value)
	fixture.refreshRefs()
	if err := fixture.validate(); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown performance field accepted: %v", err)
	}
}

type sharedWorkspaceBehaviorTestFixture struct {
	evidence  sharedWorkspaceBehaviorEvidence
	artifacts map[string][]byte
	refs      []ArtifactRef
	packageID PackageIdentity
	runtime   RuntimeExpectation
}

func newSharedWorkspaceBehaviorFixture(t *testing.T) sharedWorkspaceBehaviorTestFixture {
	t.Helper()
	packageID := *evalPackageIdentity(runtimePackageCommitFixture, "a")
	runtimeExpectation := runtimeExpectationFixture()
	helperDigest := strings.Repeat("d", 64)
	relations := sharedWorkspaceRelationsEvidence{
		Schema: "hideout.shared-workspace-relations/v1", Status: "passed",
		EnvironmentCount: 1, InstanceCount: 1, Checks: map[string]bool{},
	}
	for _, check := range requiredSharedWorkspaceRelationChecks {
		relations.Checks[check] = true
	}
	lifecycle := sharedWorkspaceLifecycleEvidence{
		Schema: "hideout.shared-workspace-lifecycle/v1", Status: "passed",
		FirstGeneration: 1, RestartGeneration: 2, ElapsedSeconds: 1, Checks: map[string]bool{},
	}
	for _, check := range requiredSharedWorkspaceLifecycleChecks {
		lifecycle.Checks[check] = true
	}
	manifest := packagekit.Manifest{
		Schema:  packagekit.ArtifactSchema,
		Release: packagekit.ReleaseInfo{ProductVersion: packageID.ProductVersion},
		Source:  packagekit.SourceInfo{Commit: packageID.SourceCommit},
		Target:  packagekit.Target{HostOS: packageID.HostOS, HostArch: packageID.HostArch, LinuxGuestArch: "arm64"},
		Files:   []packagekit.File{{Path: "bin/hideout-workspace-portal-linux-arm64", Kind: "linux-helper", SHA256: helperDigest, Executable: true}},
	}
	helper := helperbin.Manifest{
		Version: helperbin.ManifestVersion, Command: helperbin.LinuxWorkspacePortalCommand,
		TargetOS: "linux", TargetArch: "arm64", Artifact: "hideout-workspace-portal-linux-arm64", SHA256: helperDigest,
	}
	fixture := sharedWorkspaceBehaviorTestFixture{
		packageID: packageID, runtime: runtimeExpectation,
		artifacts: map[string][]byte{
			"artifacts/behavior/relations.json":                        mustMarshalSharedWorkspaceTest(t, relations),
			"artifacts/behavior/lifecycle.json":                        mustMarshalSharedWorkspaceTest(t, lifecycle),
			"artifacts/behavior/correctness.json":                      mustMarshalSharedWorkspaceTest(t, validSharedWorkspaceCorrectness(30)),
			"artifacts/behavior/research-decision.json":                readSharedWorkspaceResearchDecision(t),
			"artifacts/behavior/package-manifest.json":                 mustMarshalSharedWorkspaceTest(t, manifest),
			"artifacts/behavior/workspace-portal-helper.manifest.json": mustMarshalSharedWorkspaceTest(t, helper),
		},
	}
	fixture.evidence = sharedWorkspaceBehaviorEvidence{
		Schema: "hideout.shared-workspace-real-gate2/v1", Status: "passed",
		Commit: runtimePackageCommitFixture, Backend: "lima", HostOS: "darwin", HostArch: "arm64",
		Transport: "workspace-portal", PackageIdentity: packageID, Runtime: runtimeBindingFixture(),
		Artifacts: map[string]sharedWorkspaceArtifactDigest{}, Checks: map[string]bool{},
	}
	for _, check := range requiredSharedWorkspaceBehaviorChecks {
		fixture.evidence.Checks[check] = true
	}
	fixture.sync(t)
	return fixture
}

func (f *sharedWorkspaceBehaviorTestFixture) sync(t *testing.T) {
	t.Helper()
	paths := map[string]string{
		"relations": "artifacts/behavior/relations.json", "lifecycle": "artifacts/behavior/lifecycle.json",
		"correctness": "artifacts/behavior/correctness.json", "researchDecision": "artifacts/behavior/research-decision.json",
		"packageManifest":               "artifacts/behavior/package-manifest.json",
		"workspacePortalHelperManifest": "artifacts/behavior/workspace-portal-helper.manifest.json",
	}
	for key, path := range paths {
		if data, ok := f.artifacts[path]; ok {
			f.evidence.Artifacts[key] = sharedWorkspaceTestDigest(path, data)
		}
	}
	f.artifacts["behavior.json"] = mustMarshalSharedWorkspaceTest(t, f.evidence)
	f.refreshRefs()
}

func (f *sharedWorkspaceBehaviorTestFixture) refreshRefs() {
	f.refs = sharedWorkspaceTestRefs(f.artifacts)
}

func (f sharedWorkspaceBehaviorTestFixture) validate() error {
	return validateSharedWorkspaceBehaviorArtifact(f.refs, f.artifacts, runtimePackageCommitFixture, &f.packageID, &f.runtime)
}

type sharedWorkspacePerformanceTestFixture struct {
	evidence            sharedWorkspacePerformanceEvidence
	control             sharedWorkspaceFilesystemControlEvidence
	artifacts           map[string][]byte
	refs                []ArtifactRef
	packageID           PackageIdentity
	runtime             RuntimeExpectation
	omitControlManifest bool
}

func newSharedWorkspacePerformanceFixture(t *testing.T) sharedWorkspacePerformanceTestFixture {
	t.Helper()
	packageID := *evalPackageIdentity(runtimePackageCommitFixture, "a")
	runtimeExpectation := runtimeExpectationFixture()
	decisionData := readSharedWorkspaceResearchDecision(t)
	var decision workspaceattach.ResearchDecision
	mustDecodeSharedWorkspaceTest(t, decisionData, &decision)
	fixture := sharedWorkspacePerformanceTestFixture{
		packageID: packageID, runtime: runtimeExpectation,
		artifacts: map[string][]byte{
			"artifacts/performance/research-baseline/fixture.sha256": []byte(decision.Provenance.FixtureDigest + "\n"),
			"artifacts/performance/correctness.json":                 mustMarshalSharedWorkspaceTest(t, validSharedWorkspaceCorrectness(30)),
			"artifacts/performance/saturation.json":                  []byte("{\"teardownMs\":100}\n"),
			"artifacts/performance/research-decision.json":           decisionData,
			sharedWorkspacePairedSamplesPath:                         sharedWorkspacePairedSampleBytes(100, 100, 100, 100, 30),
		},
	}
	for _, path := range sharedWorkspaceCandidateRawPaths {
		fixture.artifacts[path] = sharedWorkspaceSampleBytes(100, map[bool]int{true: 100, false: 30}[strings.Contains(path, "saturation")])
	}
	for _, path := range sharedWorkspaceFilesystemControlRawPaths {
		fixture.artifacts[path] = sharedWorkspaceSampleBytes(100, 30)
	}
	for _, path := range sharedWorkspaceResearchBaselineRawPaths {
		fixture.artifacts[path] = sharedWorkspaceSampleBytes(100, 30)
	}
	fixture.evidence.Schema = "hideout.shared-workspace-gate2-evaluation/v1"
	fixture.evidence.Result = "passed"
	fixture.evidence.ThresholdsPassed = true
	fixture.evidence.FixtureSHA256 = decision.Provenance.FixtureDigest
	fixture.evidence.Candidate.Commit = runtimePackageCommitFixture
	fixture.evidence.ResearchBaseline.Commit = decision.Provenance.Commit
	fixture.evidence.ResearchBaseline.Dirty = decision.Provenance.Dirty
	fixture.evidence.FilesystemControl = sharedWorkspaceFilesystemControlIdentity{
		Commit: runtimePackageCommitFixture, Mechanism: "profile-cache-static-virtiofs",
		GuestRoot: "/hideout/profile/cache/035-static-virtiofs-control", SampleOrder: "alternating-pairs",
	}
	fixture.evidence.PackageIdentity = packageID
	fixture.evidence.Runtime = runtimeBindingFixture()
	fixture.evidence.Methodology.Samples = 30
	fixture.evidence.Methodology.Warmups = 1
	fixture.evidence.Methodology.FilesystemSampleOrder = "alternating-pairs"
	fixture.evidence.Methodology.FirstByteSampleOrder = "one-warmup-then-measured"
	fixture.evidence.Methodology.GitMedianAbsoluteMS = 2000
	fixture.evidence.Methodology.GitMedianBaselineRatio = 2
	fixture.evidence.Methodology.PackageMedianBaselineRatio = 3
	fixture.evidence.Methodology.AtomicVisibilityP95MS = 250
	fixture.evidence.Methodology.MountReadyP95MS = 1000
	fixture.evidence.Methodology.FirstByteAbsoluteAllowanceMS = 500
	fixture.evidence.Methodology.FirstByteBaselineAllowance = 0.15
	fixture.evidence.Methodology.SaturationTeardownMS = 5000
	for _, id := range sharedWorkspaceCandidateMetrics {
		metric := sharedWorkspacePerformanceMetric{ID: id, Candidate: sharedWorkspaceSampleSummary{Samples: 30, MedianMS: 100, P95MS: 100}, ReferenceKind: "absolute-threshold", Passed: true}
		if slices.Contains(sharedWorkspaceFilesystemControlMetrics, id) {
			reference := sharedWorkspaceSampleSummary{Samples: 30, MedianMS: 100, P95MS: 100}
			metric.Reference = &reference
			metric.ReferenceKind = "paired-static-virtiofs"
		} else if slices.Contains(sharedWorkspaceResearchBaselineMetrics, id) {
			reference := sharedWorkspaceSampleSummary{Samples: 30, MedianMS: 100, P95MS: 100}
			metric.Reference = &reference
			metric.ReferenceKind = "retained-research-baseline"
		}
		fixture.evidence.Metrics = append(fixture.evidence.Metrics, metric)
	}
	fixture.evidence.Correctness.Passed = true
	fixture.evidence.Correctness.Observation = validSharedWorkspaceCorrectness(30)
	fixture.evidence.Saturation.Passed = true
	fixture.evidence.Saturation.Observation.TeardownMS = 100
	fixture.evidence.Saturation.Metadata = sharedWorkspaceSampleSummary{Samples: 100, MedianMS: 100, P95MS: 100}
	fixture.control = sharedWorkspaceFilesystemControlEvidence{
		Schema: "hideout.shared-workspace-paired-control/v1", Commit: runtimePackageCommitFixture,
		Mechanism: "profile-cache-static-virtiofs", GuestRoot: "/hideout/profile/cache/035-static-virtiofs-control",
		FixtureSHA256: decision.Provenance.FixtureDigest, Samples: 30, Warmups: 1, SampleOrder: "alternating-pairs",
	}
	fixture.sync(t)
	return fixture
}

func (f *sharedWorkspacePerformanceTestFixture) sync(t *testing.T) {
	t.Helper()
	f.evidence.Artifacts.Candidate = map[string]sharedWorkspaceArtifactDigest{}
	for id, path := range sharedWorkspaceCandidateRawPaths {
		if data, ok := f.artifacts[path]; ok {
			f.evidence.Artifacts.Candidate[id] = sharedWorkspaceTestDigest(path, data)
		}
	}
	f.evidence.Artifacts.FilesystemControl = map[string]sharedWorkspaceArtifactDigest{}
	for id, path := range sharedWorkspaceFilesystemControlRawPaths {
		if data, ok := f.artifacts[path]; ok {
			f.evidence.Artifacts.FilesystemControl[id] = sharedWorkspaceTestDigest(path, data)
		}
	}
	f.evidence.Artifacts.ResearchBaseline = map[string]sharedWorkspaceArtifactDigest{}
	for id, path := range sharedWorkspaceResearchBaselineRawPaths {
		if data, ok := f.artifacts[path]; ok {
			f.evidence.Artifacts.ResearchBaseline[id] = sharedWorkspaceTestDigest(path, data)
		}
	}
	for path, target := range map[string]*sharedWorkspaceArtifactDigest{
		"artifacts/performance/research-baseline/fixture.sha256": &f.evidence.Artifacts.Fixture,
		sharedWorkspacePairedSamplesPath:                         &f.evidence.Artifacts.PairedSamples,
		"artifacts/performance/correctness.json":                 &f.evidence.Artifacts.Correctness,
		"artifacts/performance/saturation.json":                  &f.evidence.Artifacts.Saturation,
		"artifacts/performance/research-decision.json":           &f.evidence.Artifacts.ResearchDecision,
	} {
		if data, ok := f.artifacts[path]; ok {
			*target = sharedWorkspaceTestDigest(path, data)
		}
	}
	f.control.Artifacts = f.evidence.Artifacts.FilesystemControl
	f.control.PairedSamples = f.evidence.Artifacts.PairedSamples
	controlPath := "artifacts/performance/filesystem-control/manifest.json"
	delete(f.artifacts, controlPath)
	f.evidence.Artifacts.FilesystemControlManifest = sharedWorkspaceArtifactDigest{}
	if !f.omitControlManifest {
		f.artifacts[controlPath] = mustMarshalSharedWorkspaceTest(t, f.control)
		f.evidence.Artifacts.FilesystemControlManifest = sharedWorkspaceTestDigest(controlPath, f.artifacts[controlPath])
	}
	f.artifacts["performance.json"] = mustMarshalSharedWorkspaceTest(t, f.evidence)
	f.refreshRefs()
}

func (f *sharedWorkspacePerformanceTestFixture) refreshRefs() {
	f.refs = sharedWorkspaceTestRefs(f.artifacts)
}

func (f sharedWorkspacePerformanceTestFixture) validate() error {
	return validateSharedWorkspacePerformanceArtifact(f.refs, f.artifacts, runtimePackageCommitFixture, &f.packageID, &f.runtime)
}

func validSharedWorkspaceCorrectness(samples int) sharedWorkspaceCorrectnessEvidence {
	return sharedWorkspaceCorrectnessEvidence{
		Schema:            "hideout.shared-workspace-correctness/v1",
		HostCreateVisible: true, TargetCreateVisible: true,
		HostAtomicReplaceVisible: true, TargetAtomicReplaceVisible: true,
		RenameVisible: true, DeleteVisible: true, ModeVisible: true, FlushDurable: true,
		SameRootLocksConflict: true, RootEscapeRejected: true, SymlinkEscapeRejected: true,
		WatcherStreamHealthy: true, HostWatcherSamples: samples, TargetWatcherSamples: samples,
	}
}

func readSharedWorkspaceResearchDecision(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "dist", "workspace-research", "035", "decision.json"))
	if errors.Is(err, os.ErrNotExist) {
		// The Phase R research artifact is deliberately retained outside the
		// repository (gitignored dist/workspace-research); a checkout without
		// it has nothing to verify. The 035 claim stays bound to the retained
		// evidence manifest, not to this local re-read.
		t.Skipf("workspace-research decision artifact is not retained in this checkout: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sharedWorkspaceSampleBytes(value float64, count int) []byte {
	return []byte(strings.Repeat(strconvFormatSharedWorkspace(value)+"\n", count))
}

func sharedWorkspacePairedSampleBytes(gitCandidate, gitControl, packageCandidate, packageControl float64, count int) []byte {
	var out strings.Builder
	values := map[string]map[string]float64{
		"git-status":   {"candidate": gitCandidate, "control": gitControl},
		"package-scan": {"candidate": packageCandidate, "control": packageControl},
	}
	for _, metric := range sharedWorkspaceFilesystemControlMetrics {
		for index := 1; index <= count; index++ {
			sides := []string{"candidate", "control"}
			if index%2 == 0 {
				sides[0], sides[1] = sides[1], sides[0]
			}
			for _, side := range sides {
				fmt.Fprintf(&out, "%s\t%d\t%s\t%.6f\n", metric, index, side, values[metric][side])
			}
		}
	}
	return []byte(out.String())
}

func strconvFormatSharedWorkspace(value float64) string {
	return fmt.Sprintf("%.6f", value)
}

func mustMarshalSharedWorkspaceTest(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func mustDecodeSharedWorkspaceTest(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func sharedWorkspaceTestDigest(path string, data []byte) sharedWorkspaceArtifactDigest {
	sum := sha256.Sum256(data)
	return sharedWorkspaceArtifactDigest{Path: path, SHA256: hex.EncodeToString(sum[:])}
}

func sharedWorkspaceTestRefs(artifacts map[string][]byte) []ArtifactRef {
	paths := make([]string, 0, len(artifacts))
	for path := range artifacts {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	refs := make([]ArtifactRef, 0, len(paths))
	for _, path := range paths {
		kind := "manifest"
		if strings.HasSuffix(path, ".values") || strings.HasSuffix(path, ".tsv") {
			kind = "log"
		}
		digest := sharedWorkspaceTestDigest(path, artifacts[path])
		refs = append(refs, ArtifactRef{Kind: kind, Path: path, SHA256: digest.SHA256, RedactionStatus: RedactionPassed})
	}
	return refs
}
