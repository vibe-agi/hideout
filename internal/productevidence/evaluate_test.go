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
	"time"
)

func TestEvaluateMissingProofReportsRegistryFeature(t *testing.T) {
	requirement := req("026-test-feature", "026.required-proof", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyNone, "026.FR-001")
	manifest := evalManifestWithProofs(evalProof(req("other-feature", "other.proof", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyNone, "other.FR-001")))
	report, err := EvaluateManifest(manifest, EvaluationOptions{Requirements: []ProofRequirement{requirement}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Satisfied() {
		t.Fatal("missing proof should not satisfy report")
	}
	got := report.Results[0]
	if got.FeatureID != "026-test-feature" || got.ProofID != "026.required-proof" || got.Status != EvalMissing {
		t.Fatalf("missing result = %+v", got)
	}
	if err := report.RequireSatisfied(); err == nil || !strings.Contains(err.Error(), "026-test-feature/026.required-proof=missing") {
		t.Fatalf("missing proof error did not include feature/proof: %v", err)
	}
}

func TestEvaluateRequiresExactFeatureAndAllRegisteredClaims(t *testing.T) {
	requirement := req("026-feature", "026.identity", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyNone, "026.FR-001", "026.FR-002")

	t.Run("feature id", func(t *testing.T) {
		proof := evalProof(requirement)
		proof.FeatureID = "026-spoofed-feature"
		report, err := EvaluateManifest(evalManifestWithProofs(proof), EvaluationOptions{Requirements: []ProofRequirement{requirement}})
		if err != nil {
			t.Fatal(err)
		}
		if got := report.Results[0].Status; got != EvalMissing {
			t.Fatalf("feature-spoofed proof status=%s want %s", got, EvalMissing)
		}
	})

	t.Run("full claim set", func(t *testing.T) {
		proof := evalProof(requirement)
		proof.CoveredClaims = proof.CoveredClaims[:1]
		report, err := EvaluateManifest(evalManifestWithProofs(proof), EvaluationOptions{Requirements: []ProofRequirement{requirement}})
		if err != nil {
			t.Fatal(err)
		}
		result := report.Results[0]
		if result.Status != EvalFailed || !strings.Contains(result.Summary, "026.FR-002") {
			t.Fatalf("partial claim set was accepted: %+v", result)
		}
	})
}

func TestEvaluateFailedNotRunAndRedaction(t *testing.T) {
	reqs := []ProofRequirement{
		req("026-test-feature", "026.failed", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyNone, "026.FR-failed"),
		req("026-test-feature", "026.not-run", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyNone, "026.FR-not-run"),
		req("026-test-feature", "026.redaction", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyNone, "026.FR-redaction"),
	}
	failed := evalProof(reqs[0])
	failed.Status = StatusFailed
	notRun := evalProof(reqs[1])
	notRun.Status = StatusNotRun
	notRun.RedactionStatus = RedactionNotRun
	notRun.NotRunReason = "fixture"
	redaction := evalProof(reqs[2])
	redaction.Status = StatusFailed
	redaction.RedactionStatus = RedactionFailed
	manifest := evalManifestWithProofs(failed, notRun, redaction)
	report, err := EvaluateManifest(manifest, EvaluationOptions{Requirements: reqs})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"026.failed":    EvalFailed,
		"026.not-run":   EvalNotRun,
		"026.redaction": EvalRedactionFailed,
	}
	for _, result := range report.Results {
		if result.Status != want[result.ProofID] {
			t.Fatalf("%s status=%s want %s", result.ProofID, result.Status, want[result.ProofID])
		}
	}
}

func TestEvaluateStaleByCommitAndPackage(t *testing.T) {
	oldCommit := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newCommit := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	commitReq := req("026-test-feature", "026.commit", LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommit, ArtifactPolicyNone, "026.FR-commit")
	packageReq := req("026-test-feature", "026.package", LayerProductHardening, RequiredForTargetedCompletion, FreshnessSamePackage, ArtifactPolicyNone, "026.FR-package")
	manifest := evalManifestWithProofs(evalProof(commitReq), evalProof(packageReq))
	manifest.Commit = oldCommit
	manifest.PackageIdentity = evalPackageIdentity(oldCommit, "a")
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements:    []ProofRequirement{commitReq, packageReq},
		ExpectedCommit:  newCommit,
		ExpectedPackage: evalPackageIdentity(newCommit, "b"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range report.Results {
		if result.Status != EvalStale {
			t.Fatalf("%s status=%s want stale", result.ProofID, result.Status)
		}
	}
	for _, proof := range manifest.Proofs {
		if proof.Status == EvalStale {
			t.Fatal("manifest proof status must not be stale")
		}
	}
}

func TestEvaluateFreshnessIgnoresProofNotes(t *testing.T) {
	staleCommit := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	expectedCommit := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	requirement := req("026-test-feature", "026.commit-note-bypass", LayerProductHardening, RequiredForTargetedCompletion, FreshnessSameCommitAndPackage, ArtifactPolicyNone, "026.FR-commit")
	proof := evalProof(requirement)
	proof.Notes = []string{"commit=expected", "package=hideout@expected"}
	manifest := evalManifestWithProofs(proof)
	manifest.Commit = staleCommit
	manifest.PackageIdentity = evalPackageIdentity(staleCommit, "a")
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements:    []ProofRequirement{requirement},
		ExpectedCommit:  expectedCommit,
		ExpectedPackage: evalPackageIdentity(expectedCommit, "b"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Results[0].Status; got != EvalStale {
		t.Fatalf("proof notes overrode manifest freshness: status=%s", got)
	}
}

func TestEvaluateArtifactMissingAndDigestMismatch(t *testing.T) {
	root := t.TempDir()
	missingReq := req("026-test-feature", "026.artifact-missing", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyExists, "026.FR-artifact")
	digestReq := req("026-test-feature", "026.artifact-digest", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyExistsAndDigestIfSupplied, "026.FR-digest")
	missingProof := evalProof(missingReq)
	missingProof.Artifacts = []ArtifactRef{{Kind: "log", Path: "missing.log", RedactionStatus: RedactionPassed}}
	digestProof := evalProof(digestReq)
	if err := os.WriteFile(filepath.Join(root, "actual.log"), []byte("actual"), 0o600); err != nil {
		t.Fatal(err)
	}
	digestProof.Artifacts = []ArtifactRef{{Kind: "log", Path: "actual.log", SHA256: strings.Repeat("0", 64), RedactionStatus: RedactionPassed}}
	manifest := evalManifestWithProofs(missingProof, digestProof)
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements: []ProofRequirement{missingReq, digestReq},
		ArtifactRoot: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"026.artifact-missing": EvalArtifactMissing,
		"026.artifact-digest":  EvalArtifactDigestMismatch,
	}
	for _, result := range report.Results {
		if result.Status != want[result.ProofID] {
			t.Fatalf("%s status=%s want %s", result.ProofID, result.Status, want[result.ProofID])
		}
	}
}

func TestEvaluateArtifactDigestSatisfied(t *testing.T) {
	root := t.TempDir()
	req := req("026-test-feature", "026.artifact-ok", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyExistsAndDigestIfSupplied, "026.FR-artifact-ok")
	path := filepath.Join(root, "actual.log")
	data := []byte("actual")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	proof := evalProof(req)
	proof.Artifacts = []ArtifactRef{{Kind: "log", Path: "actual.log", SHA256: hex.EncodeToString(sum[:]), RedactionStatus: RedactionPassed}}
	report, err := EvaluateManifest(evalManifestWithProofs(proof), EvaluationOptions{Requirements: []ProofRequirement{req}, ArtifactRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Satisfied() {
		t.Fatalf("artifact digest should satisfy report: %+v", report.Results)
	}
}

func TestEvaluateArtifactRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "proof.log"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	requirement := req("026-test-feature", "026.artifact-symlink", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyExists, "026.FR-artifact")
	proof := evalProof(requirement)
	proof.Artifacts = []ArtifactRef{{Kind: "log", Path: "linked/proof.log", RedactionStatus: RedactionPassed}}
	report, err := EvaluateManifest(evalManifestWithProofs(proof), EvaluationOptions{Requirements: []ProofRequirement{requirement}, ArtifactRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Results[0].Status; got != EvalArtifactMissing {
		t.Fatalf("symlink escape status=%s want %s", got, EvalArtifactMissing)
	}
}

func TestEvaluateTargetMarksOtherRequiredForRowsNotRequired(t *testing.T) {
	targetReq := req("026-target", "026.target", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyNone, "026.FR-target")
	localReq := req("026-local", "026.local", LayerProductHardening, RequiredForLocalDogfood, FreshnessNone, ArtifactPolicyNone, "026.FR-local")
	report, err := EvaluateManifest(evalManifestWithProofs(evalProof(targetReq)), EvaluationOptions{
		Requirements: []ProofRequirement{targetReq, localReq},
		Target:       RequiredForTargetedCompletion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Satisfied() {
		t.Fatalf("targeted report should be satisfied with local-dogfood proof not required: %+v", report.Results)
	}
	status := map[string]string{}
	for _, result := range report.Results {
		status[result.ProofID] = result.Status
	}
	if status[targetReq.ProofID] != EvalSatisfied {
		t.Fatalf("target proof status=%s", status[targetReq.ProofID])
	}
	if status[localReq.ProofID] != EvalNotRequired {
		t.Fatalf("local proof status=%s want not-required", status[localReq.ProofID])
	}
}

func TestEvaluateLocalDogfoodDoesNotRequireTargetedRows(t *testing.T) {
	targetReq := req("026-target", "026.target", LayerProductHardening, RequiredForTargetedCompletion, FreshnessNone, ArtifactPolicyNone, "026.FR-target")
	localReq := req("026-local", "026.local", LayerProductHardening, RequiredForLocalDogfood, FreshnessNone, ArtifactPolicyNone, "026.FR-local")
	report, err := EvaluateManifest(evalManifestWithProofs(evalProof(localReq)), EvaluationOptions{
		Requirements: []ProofRequirement{targetReq, localReq},
		Target:       RequiredForLocalDogfood,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Satisfied() {
		t.Fatalf("local report should be satisfied with targeted proof not required: %+v", report.Results)
	}
	for _, result := range report.Results {
		if result.ProofID == targetReq.ProofID && result.Status != EvalNotRequired {
			t.Fatalf("targeted proof status=%s want not-required", result.Status)
		}
	}
}

func TestEvaluateRuntimeProofRequiresExactCleanRealBinding(t *testing.T) {
	requirement := runtimeReq(Feature031, "031.runtime.fixture", LayerRealGate, RequiredForReleaseCandidate, FreshnessNone, ArtifactPolicyNone, "031.FR-018")
	expected := runtimeExpectationFixture()
	valid := runtimeProofFixture(requirement, runtimeBindingFixture())

	cases := []struct {
		name   string
		mutate func(*ProofEntry)
		want   string
	}{
		{name: "exact", want: EvalSatisfied},
		{name: "missing binding", mutate: func(p *ProofEntry) { p.Runtime = nil }, want: EvalRuntimeMismatch},
		{name: "local fixture", mutate: func(p *ProofEntry) { p.Mode = "local-fast" }, want: EvalRuntimeMismatch},
		{name: "wrong revision", mutate: func(p *ProofEntry) { p.Runtime.Revision = "other" }, want: EvalRuntimeMismatch},
		{name: "wrong digest", mutate: func(p *ProofEntry) { p.Runtime.ArtifactSHA256 = strings.Repeat("b", 64) }, want: EvalRuntimeMismatch},
		{name: "wrong environment", mutate: func(p *ProofEntry) { p.Runtime.EnvironmentID = "env_20260711t000000z1111111111111111111" }, want: EvalSatisfied},
		{name: "dirty image build", mutate: func(p *ProofEntry) { p.Runtime.BuildDirty = true }, want: EvalRuntimeMismatch},
		{name: "wrong image build commit", mutate: func(p *ProofEntry) { p.Runtime.BuildCommit = "abcdef012345" }, want: EvalRuntimeMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proof := valid
			binding := *valid.Runtime
			proof.Runtime = &binding
			if tc.mutate != nil {
				tc.mutate(&proof)
			}
			manifest := runtimeManifestWithProofs(expected, proof)
			report, err := EvaluateManifest(manifest, runtimeEvaluationOptions([]ProofRequirement{requirement}, expected))
			if err != nil {
				t.Fatal(err)
			}
			if got := report.Results[0].Status; got != tc.want {
				t.Fatalf("status=%s want %s result=%+v", got, tc.want, report.Results[0])
			}
		})
	}
}

func TestEvaluateRuntimeProofsAllowIndependentValidEnvironmentIdentities(t *testing.T) {
	a := runtimeReq(Feature031, "031.runtime.gate2", LayerRealGate, RequiredForReleaseCandidate, FreshnessNone, ArtifactPolicyNone, "031.FR-018")
	b := runtimeReq(Feature031, "031.runtime.gate3", LayerRealGate, RequiredForReleaseCandidate, FreshnessNone, ArtifactPolicyNone, "031.FR-018")
	first := runtimeProofFixture(a, runtimeBindingFixture())
	secondBinding := runtimeBindingFixture()
	secondBinding.EnvironmentID = "env_20260711t000000z1111111111111111111"
	second := runtimeProofFixture(b, secondBinding)
	expected := runtimeExpectationFixture()
	report, err := EvaluateManifest(runtimeManifestWithProofs(expected, first, second), runtimeEvaluationOptions([]ProofRequirement{a, b}, expected))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Satisfied() {
		t.Fatalf("independent real-gate environment identities were rejected: %+v", report.Results)
	}
}

func TestEvaluate034RejectsFalseRealGateEvidence(t *testing.T) {
	requirements := RequirementsForFeature(Feature034)
	var realRequirements []ProofRequirement
	for _, requirement := range requirements {
		if requirement.RequiredFor == RequiredForReleaseCandidate {
			realRequirements = append(realRequirements, requirement)
		}
	}
	if len(realRequirements) != 2 {
		t.Fatalf("034 real requirements=%d want 2", len(realRequirements))
	}
	expected := runtimeExpectationFixture()

	newFixture := func(t *testing.T) (Manifest, EvaluationOptions) {
		t.Helper()
		root := t.TempDir()
		proofs := make([]ProofEntry, 0, len(realRequirements))
		for i, requirement := range realRequirements {
			var data []byte
			var marshalErr error
			switch requirement.ArtifactValidator {
			case ArtifactValidatorConcurrentIsolationV1:
				data, marshalErr = json.Marshal(concurrentIsolationFixture())
			case ArtifactValidatorConcurrentPerformanceV1:
				data, marshalErr = json.Marshal(concurrentPerformanceFixture(expected))
			default:
				t.Fatalf("unexpected 034 validator %q", requirement.ArtifactValidator)
			}
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			name := fmt.Sprintf("proof-%d.json", i)
			if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			proof := runtimeProofFixture(requirement, runtimeBindingFixture())
			proof.EvidenceClass = requirement.RequiredEvidenceClass
			proof.Artifacts = []ArtifactRef{{
				Kind: "manifest", Path: name, SHA256: hex.EncodeToString(sum[:]),
				RedactionStatus: RedactionPassed,
			}}
			proofs = append(proofs, proof)
		}
		manifest := runtimeManifestWithProofs(expected, proofs...)
		opts := runtimeEvaluationOptions(realRequirements, expected)
		opts.ArtifactRoot = root
		return manifest, opts
	}

	cases := []struct {
		name   string
		mutate func(*Manifest, *EvaluationOptions)
		proof  string
		want   string
	}{
		{name: "exact", proof: Proof034RealIsolation, want: EvalSatisfied},
		{name: "missing performance", mutate: func(m *Manifest, _ *EvaluationOptions) {
			m.Proofs = m.Proofs[:1]
		}, proof: Proof034RealPerformance, want: EvalMissing},
		{name: "not run", mutate: func(m *Manifest, _ *EvaluationOptions) {
			m.Proofs[0].Status = StatusNotRun
			m.Proofs[0].RedactionStatus = RedactionNotRun
			m.Proofs[0].NotRunReason = "fixture"
		}, proof: Proof034RealIsolation, want: EvalNotRun},
		{name: "synthetic local", mutate: func(m *Manifest, _ *EvaluationOptions) {
			m.Proofs[0].Mode = "local-fast"
		}, proof: Proof034RealIsolation, want: EvalRuntimeMismatch},
		{name: "stale dirty candidate", mutate: func(m *Manifest, _ *EvaluationOptions) {
			m.Dirty = true
		}, proof: Proof034RealIsolation, want: EvalStale},
		{name: "wrong runtime", mutate: func(m *Manifest, _ *EvaluationOptions) {
			m.Proofs[0].Runtime.ArtifactSHA256 = strings.Repeat("b", 64)
		}, proof: Proof034RealIsolation, want: EvalRuntimeMismatch},
		{name: "artifact digest mismatch", mutate: func(m *Manifest, _ *EvaluationOptions) {
			m.Proofs[1].Artifacts[0].SHA256 = strings.Repeat("f", 64)
		}, proof: Proof034RealPerformance, want: EvalArtifactDigestMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest, opts := newFixture(t)
			if tc.mutate != nil {
				tc.mutate(&manifest, &opts)
			}
			report, err := EvaluateManifest(manifest, opts)
			if err != nil {
				t.Fatal(err)
			}
			for _, result := range report.Results {
				if result.ProofID == tc.proof {
					if result.Status != tc.want {
						t.Fatalf("%s status=%s want %s: %+v", tc.proof, result.Status, tc.want, result)
					}
					return
				}
			}
			t.Fatalf("missing evaluation result for %s", tc.proof)
		})
	}
}

func TestRuntimeBindingRejectsEmptyOrInvalidEnvironmentIdentity(t *testing.T) {
	for _, environmentID := range []string{"", "env_INVALID"} {
		binding := runtimeBindingFixture()
		binding.EnvironmentID = environmentID
		if err := binding.Validate(); err == nil || !strings.Contains(err.Error(), "environmentId") {
			t.Fatalf("environmentId %q validation error=%v", environmentID, err)
		}
	}
}

func TestEvaluateReleaseRuntimeProofRequiresTrustedPackageAndArtifactDigest(t *testing.T) {
	root := t.TempDir()
	requirement := runtimeReq(Feature031, "031.runtime.release", LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "031.FR-018")
	expected := runtimeExpectationFixture()
	artifactData := []byte("observed real runtime\n")
	artifactPath := filepath.Join(root, "runtime.log")
	if err := os.WriteFile(artifactPath, artifactData, 0o600); err != nil {
		t.Fatal(err)
	}
	artifactSum := sha256.Sum256(artifactData)

	newFixture := func() (Manifest, EvaluationOptions) {
		proof := runtimeProofFixture(requirement, runtimeBindingFixture())
		proof.Artifacts = []ArtifactRef{{
			Kind: "log", Path: "runtime.log", SHA256: hex.EncodeToString(artifactSum[:]),
			RedactionStatus: RedactionPassed,
		}}
		manifest := runtimeManifestWithProofs(expected, proof)
		opts := runtimeEvaluationOptions([]ProofRequirement{requirement}, expected)
		opts.ArtifactRoot = root
		return manifest, opts
	}

	cases := []struct {
		name   string
		mutate func(*Manifest, *EvaluationOptions)
		want   string
	}{
		{name: "exact", want: EvalSatisfied},
		{name: "missing trusted package", mutate: func(_ *Manifest, opts *EvaluationOptions) { opts.ExpectedPackage = nil }, want: EvalStale},
		{name: "missing proof package", mutate: func(m *Manifest, _ *EvaluationOptions) { m.PackageIdentity = nil }, want: EvalStale},
		{name: "wrong proof package", mutate: func(m *Manifest, _ *EvaluationOptions) { m.PackageIdentity.SourceCommit = strings.Repeat("b", 40) }, want: EvalStale},
		{name: "changed proof archive", mutate: func(m *Manifest, _ *EvaluationOptions) { m.PackageIdentity.ArtifactSHA256 = strings.Repeat("b", 64) }, want: EvalStale},
		{name: "wrong trusted package", mutate: func(_ *Manifest, opts *EvaluationOptions) {
			opts.ExpectedPackage.SourceCommit = strings.Repeat("b", 40)
		}, want: EvalStale},
		{name: "dirty manifest", mutate: func(m *Manifest, _ *EvaluationOptions) { m.Dirty = true }, want: EvalStale},
		{name: "noncanonical serialized commit", mutate: func(m *Manifest, _ *EvaluationOptions) { m.Commit = " 0123456789ab " }, want: EvalStale},
		{name: "stale manifest with forged notes", mutate: func(m *Manifest, _ *EvaluationOptions) {
			m.Commit = "unrelated"
			m.Proofs[0].Notes = []string{"commit=" + expected.BuildCommit, "package=hideout@pkg-0123456789ab"}
		}, want: EvalStale},
		{name: "missing artifact digest", mutate: func(m *Manifest, _ *EvaluationOptions) { m.Proofs[0].Artifacts[0].SHA256 = "" }, want: EvalArtifactDigestMismatch},
		{name: "artifact digest mismatch", mutate: func(m *Manifest, _ *EvaluationOptions) { m.Proofs[0].Artifacts[0].SHA256 = strings.Repeat("f", 64) }, want: EvalArtifactDigestMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest, opts := newFixture()
			if tc.mutate != nil {
				tc.mutate(&manifest, &opts)
			}
			report, err := EvaluateManifest(manifest, opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := report.Results[0].Status; got != tc.want {
				t.Fatalf("status=%s want %s result=%+v", got, tc.want, report.Results[0])
			}
		})
	}
}

func TestEvaluateReleaseCandidateScansArtifactBytes(t *testing.T) {
	requirement := runtimeReq(Feature031, "031.runtime.secret-scan", LayerRealGate, RequiredForReleaseCandidate, FreshnessSameCommitAndPackage, ArtifactPolicyExistsAndDigestIfSupplied, "031.FR-017")
	expected := runtimeExpectationFixture()
	secrets := map[string]string{
		"hideout secret":   "HIDEOUT_SECRET_DEFAULT_PROXY=socks5://user:pass@127.0.0.1:1080",
		"capability token": "cap_0123456789abcdef0123456789abcdef",
		"setup key":        "setupPrivateKey=real-private-key-material",
		"machine id":       "machineId=0123456789abcdef0123456789abcdef",
	}
	for name, secret := range secrets {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			data := []byte("observed=" + secret + "\n")
			if err := os.WriteFile(filepath.Join(root, "runtime.log"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			proof := runtimeProofFixture(requirement, runtimeBindingFixture())
			proof.Artifacts = []ArtifactRef{{
				Kind: "log", Path: "runtime.log", SHA256: hex.EncodeToString(sum[:]), RedactionStatus: RedactionPassed,
			}}
			opts := runtimeEvaluationOptions([]ProofRequirement{requirement}, expected)
			opts.ArtifactRoot = root
			report, err := EvaluateManifest(runtimeManifestWithProofs(expected, proof), opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := report.Results[0]; got.Status != EvalRedactionFailed || !strings.Contains(got.Summary, "control-plane") {
				t.Fatalf("self-attested redaction accepted secret bytes: %+v", got)
			}
		})
	}
}

func runtimeBindingFixture() RuntimeBinding {
	return RuntimeBinding{
		Schema: RuntimeBindingSchema, Family: "developer-standard", Revision: "2026.07.0",
		ArtifactSHA256: strings.Repeat("a", 64), EnvironmentID: "env_20260711t000000z0123456789abcdef0123",
		HostOS: "darwin", HostArch: "arm64", GuestArch: "aarch64",
		BuildCommit: "0123456789ab", BuildDirty: false,
	}
}

const runtimePackageCommitFixture = "abcdef012345abcdef012345abcdef012345abcd"

func runtimeExpectationFixture() RuntimeExpectation {
	binding := runtimeBindingFixture()
	return RuntimeExpectation{
		Family: binding.Family, Revision: binding.Revision, ArtifactSHA256: binding.ArtifactSHA256,
		HostOS: binding.HostOS, HostArch: binding.HostArch, GuestArch: binding.GuestArch,
		BuildCommit: binding.BuildCommit, RequireClean: true,
	}
}

func runtimeProofFixture(requirement ProofRequirement, binding RuntimeBinding) ProofEntry {
	proof := evalProof(requirement)
	proof.Mode = "real-gate"
	proof.Runtime = &binding
	return proof
}

func runtimeManifestWithProofs(_ RuntimeExpectation, proofs ...ProofEntry) Manifest {
	manifest := evalManifestWithProofs(proofs...)
	manifest.Commit = runtimePackageCommitFixture
	manifest.PackageIdentity = evalPackageIdentity(runtimePackageCommitFixture, "a")
	return manifest
}

func runtimeEvaluationOptions(requirements []ProofRequirement, expected RuntimeExpectation) EvaluationOptions {
	return EvaluationOptions{
		Requirements:    requirements,
		Target:          RequiredForReleaseCandidate,
		ExpectedCommit:  runtimePackageCommitFixture,
		ExpectedPackage: evalPackageIdentity(runtimePackageCommitFixture, "a"),
		ExpectedRuntime: &expected,
	}
}

func evalPackageIdentity(commit, digestCharacter string) *PackageIdentity {
	return &PackageIdentity{
		Name: "hideout", ProductVersion: "0.1.0-alpha.1", SourceCommit: commit,
		ArtifactSHA256: strings.Repeat(digestCharacter, 64), HostOS: "darwin", HostArch: "arm64",
	}
}

func evalManifestWithProofs(proofs ...ProofEntry) Manifest {
	m := NewManifest("test", false)
	m.GeneratedAt = time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)
	m.Proofs = proofs
	if err := m.Validate(); err != nil {
		panic(err)
	}
	return m
}

func evalProof(req ProofRequirement) ProofEntry {
	return ProofEntry{
		ProofID:         req.ProofID,
		FeatureID:       req.FeatureID,
		Mode:            "unit",
		EvidenceClass:   "unit-fixture",
		Status:          StatusPassed,
		CommandSummary:  "unit fixture",
		CoveredClaims:   coveredClaimsForRequirement(req),
		Prerequisites:   []PrerequisiteStatus{{Name: "unit", Status: "available"}},
		Artifacts:       []ArtifactRef{},
		RedactionStatus: RedactionPassed,
	}
}

func coveredClaimsForRequirement(req ProofRequirement) []CoveredClaim {
	claims := make([]CoveredClaim, 0, len(req.ClaimIDs))
	for _, claimID := range req.ClaimIDs {
		claims = append(claims, CoveredClaim{ClaimID: claimID, Source: "spec", Description: "unit fixture"})
	}
	return claims
}
