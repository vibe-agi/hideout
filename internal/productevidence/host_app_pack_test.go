package productevidence

import (
	"crypto/sha256"
	"encoding/hex"
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
	root := t.TempDir()
	artifact := []byte("external real gate fixture")
	if err := os.WriteFile(filepath.Join(root, "gate.log"), artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(artifact)
	base := evalProof(requirement)
	base.Artifacts = []ArtifactRef{{Kind: "log", Path: "gate.log", SHA256: hex.EncodeToString(sum[:]), RedactionStatus: RedactionPassed}}
	base.Mode = "real-gate"
	base.EvidenceClass = "host-app-pack-external-real-gate2"

	cases := []struct {
		name   string
		mutate func(*ProofEntry)
		want   string
	}{
		{name: "local-fast", mutate: func(p *ProofEntry) { p.Mode = "local-fast" }, want: EvalProofShapeMismatch},
		{name: "native-unit", mutate: func(p *ProofEntry) { p.Mode = "unit" }, want: EvalProofShapeMismatch},
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
			report, err := EvaluateManifest(evalManifestWithProofs(proof), EvaluationOptions{
				Requirements: []ProofRequirement{requirement}, Target: RequiredForReleaseCandidate, ArtifactRoot: root,
			})
			if err != nil {
				t.Fatal(err)
			}
			if report.Satisfied() || report.Results[0].Status != tc.want {
				t.Fatalf("fixture satisfied real Gate 2: %+v", report.Results[0])
			}
		})
	}
	report, err := EvaluateManifest(evalManifestWithProofs(base), EvaluationOptions{
		Requirements: []ProofRequirement{requirement}, Target: RequiredForReleaseCandidate, ArtifactRoot: root,
	})
	if err != nil || !report.Satisfied() {
		t.Fatalf("correctly shaped real proof failed: report=%+v err=%v", report.Results, err)
	}
}
