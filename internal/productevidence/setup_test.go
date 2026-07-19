package productevidence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedSetupLocalEvidencePassesProductionEvaluator(t *testing.T) {
	root := os.Getenv("HIDEOUT_038_EVIDENCE_DIR")
	if root == "" {
		t.Skip("set HIDEOUT_038_EVIDENCE_DIR to validate retained setup evidence")
	}
	manifest, err := ReadFile(filepath.Join(root, "product-hardening-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.PackageIdentity == nil {
		t.Fatal("retained setup evidence has no package identity")
	}
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements:    RequirementsForFeature(Feature038),
		Target:          RequiredForTargetedCompletion,
		ExpectedCommit:  manifest.Commit,
		ExpectedPackage: manifest.PackageIdentity,
		ArtifactRoot:    root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := report.RequireSatisfied(); err != nil {
		t.Fatalf("retained 038 evidence did not satisfy production evaluation: %v\n%+v", err, report.Results)
	}
}

func TestRetainedSetupRealEvidenceUsesProductionReleaseEvaluator(t *testing.T) {
	root := os.Getenv("HIDEOUT_038_REAL_EVIDENCE_DIR")
	if root == "" {
		t.Skip("set HIDEOUT_038_REAL_EVIDENCE_DIR to validate retained real setup evidence")
	}
	manifest, err := ReadFile(filepath.Join(root, "product-hardening-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.PackageIdentity == nil {
		t.Fatal("retained real setup evidence has no package identity")
	}
	var binding *RuntimeBinding
	for _, proof := range manifest.Proofs {
		if proof.ProofID != Proof038RealFirstRun && proof.ProofID != Proof038RealAgentInstallRun {
			continue
		}
		if proof.Runtime == nil {
			t.Fatalf("real setup proof %s has no runtime binding", proof.ProofID)
		}
		if binding == nil {
			copy := *proof.Runtime
			binding = &copy
		} else if !binding.SameArtifactBuild(*proof.Runtime) {
			t.Fatalf("real setup proofs carry different runtime builds: %+v != %+v", *binding, *proof.Runtime)
		}
	}
	if binding == nil {
		t.Fatal("retained real setup evidence has no real proofs")
	}
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements:    RequirementsForFeature(Feature038),
		Target:          RequiredForReleaseCandidate,
		ExpectedCommit:  manifest.Commit,
		ExpectedPackage: manifest.PackageIdentity,
		ArtifactRoot:    root,
		ExpectedRuntime: &RuntimeExpectation{
			Family: binding.Family, Revision: binding.Revision,
			ArtifactSHA256: binding.ArtifactSHA256,
			HostOS:         binding.HostOS, HostArch: binding.HostArch,
			GuestArch: binding.GuestArch, BuildCommit: binding.BuildCommit,
			RequireClean: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Dirty {
		if err := report.RequireSatisfied(); err != nil {
			t.Fatalf("clean retained 038 real evidence did not satisfy production evaluation: %v\n%+v", err, report.Results)
		}
		return
	}
	for _, result := range report.Results {
		if result.RequiredFor == RequiredForReleaseCandidate && result.Status != EvalStale {
			t.Fatalf("dirty real proof escaped stale evaluation: %+v", result)
		}
	}
}
