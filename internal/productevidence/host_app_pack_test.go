package productevidence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostAppRealGateRejectsLocalNativeSelfTestAndNotRunProofs(t *testing.T) {
	var requirement ProofRequirement
	for _, candidate := range RequirementsForFeature(Feature032) {
		if candidate.ProofID == Proof032RealGate2External {
			requirement = candidate
			break
		}
	}
	if requirement.ProofID == "" {
		t.Fatal("032 external real-gate requirement is missing")
	}
	fixture := newProjectionReadinessFixture(t, false)
	root := t.TempDir()
	for path, data := range fixture.artifacts {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := evalProof(requirement)
	base.Artifacts = append([]ArtifactRef(nil), fixture.refs...)
	base.Mode = "real-gate"
	base.EvidenceClass = "projection-readiness-real-gate2"
	runtime := fixture.evidence.Runtime
	base.Runtime = &runtime
	manifest := func(proof ProofEntry) Manifest {
		value := NewManifest(fixture.evidence.Commit, false)
		pkg := fixture.expectedPackage
		value.PackageIdentity = &pkg
		value.Proofs = []ProofEntry{proof}
		return value
	}
	options := EvaluationOptions{
		Requirements: []ProofRequirement{requirement}, Target: RequiredForReleaseCandidate,
		ArtifactRoot: root, ExpectedCommit: fixture.evidence.Commit,
		ExpectedPackage: &fixture.expectedPackage, ExpectedRuntime: &fixture.expectedRuntime,
	}

	cases := []struct {
		name   string
		mutate func(*ProofEntry)
		want   string
	}{
		{name: "local-fast", mutate: func(p *ProofEntry) { p.Mode = "local-fast" }, want: EvalRuntimeMismatch},
		{name: "native-unit", mutate: func(p *ProofEntry) { p.Mode = "unit" }, want: EvalRuntimeMismatch},
		{name: "package-self-test", mutate: func(p *ProofEntry) { p.EvidenceClass = "package-self-test" }, want: EvalProofShapeMismatch},
		{name: "not-run", mutate: func(p *ProofEntry) {
			p.Status = StatusNotRun
			p.RedactionStatus = RedactionNotRun
			p.NotRunReason = "real backend unavailable"
		}, want: EvalNotRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proof := base
			tc.mutate(&proof)
			report, err := EvaluateManifest(manifest(proof), options)
			if err != nil {
				t.Fatal(err)
			}
			if report.Satisfied() || report.Results[0].Status != tc.want {
				t.Fatalf("fixture satisfied real Gate 2: %+v", report.Results[0])
			}
		})
	}
	report, err := EvaluateManifest(manifest(base), options)
	if err != nil || !report.Satisfied() {
		t.Fatalf("correctly shaped real proof failed: report=%+v err=%v", report.Results, err)
	}
}
