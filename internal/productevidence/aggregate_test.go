package productevidence

import (
	"strings"
	"testing"
	"time"
)

func TestAggregateManifestsRequiresBrowserAndTUILanes(t *testing.T) {
	manifest := aggregateTestManifest(t, Required021ProofIDs...)
	agg, err := AggregateManifests(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := agg.RequirePassed(Required021ProofIDs...); err != nil {
		t.Fatal(err)
	}
	if err := Require021Complete(manifest); err != nil {
		t.Fatal(err)
	}
}

func TestAggregateManifestsRejectsNotRunAsCompletion(t *testing.T) {
	manifest := aggregateTestManifest(t, Required021ProofIDs...)
	for i := range manifest.Proofs {
		if manifest.Proofs[i].ProofID == Proof021TUIPTYConsole {
			manifest.Proofs[i].Status = StatusNotRun
			manifest.Proofs[i].RedactionStatus = RedactionNotRun
			manifest.Proofs[i].NotRunReason = "missing pty"
			break
		}
	}
	agg, err := AggregateManifests(manifest)
	if err != nil {
		t.Fatal(err)
	}
	err = agg.RequirePassed(Proof021TUIPTYConsole)
	if err == nil || !strings.Contains(err.Error(), "non-passing") {
		t.Fatalf("expected non-passing proof error, got %v", err)
	}
	if err := Require021Complete(manifest); err == nil {
		t.Fatal("completion should reject not-run proof")
	}
}

func TestAggregateManifestsRejectsDuplicateProofID(t *testing.T) {
	manifest := aggregateTestManifest(t, Proof021EvidenceSchema)
	manifest.Proofs = append(manifest.Proofs, manifest.Proofs[0])
	if _, err := AggregateManifests(manifest); err == nil {
		t.Fatal("duplicate proof ids should fail")
	}
}

func TestRequire022LocalFastComplete(t *testing.T) {
	manifest := NewManifest("test", false)
	manifest.Proofs = FirstRunLocalFastProofs()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := Require022LocalFastComplete(manifest); err != nil {
		t.Fatal(err)
	}
}

func TestRequire022LocalFastCompleteRejectsMissingProof(t *testing.T) {
	manifest := NewManifest("test", false)
	manifest.Proofs = FirstRunLocalFastProofs()
	manifest.Proofs = manifest.Proofs[:len(manifest.Proofs)-1]
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	err := Require022LocalFastComplete(manifest)
	if err == nil || !strings.Contains(err.Error(), Proof022FailureFixtures) {
		t.Fatalf("expected missing failure-fixtures proof, got %v", err)
	}
}

func TestRequire023LocalFastComplete(t *testing.T) {
	manifest := NewManifest("test", false)
	manifest.Proofs = HostFSDecisionLocalFastProofs()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := Require023LocalFastComplete(manifest); err != nil {
		t.Fatal(err)
	}
}

func TestRequire023LocalFastCompleteRejectsMissingProof(t *testing.T) {
	manifest := NewManifest("test", false)
	manifest.Proofs = HostFSDecisionLocalFastProofs()
	manifest.Proofs = manifest.Proofs[:len(manifest.Proofs)-1]
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	err := Require023LocalFastComplete(manifest)
	if err == nil || !strings.Contains(err.Error(), Proof023LocalFastRedaction) {
		t.Fatalf("expected missing redaction proof, got %v", err)
	}
}

func TestRequire023LocalFastCompleteRejectsRealPassSubstitution(t *testing.T) {
	manifest := NewManifest("test", false)
	manifest.Proofs = HostFSDecisionLocalFastProofs()
	manifest.Proofs = append(manifest.Proofs, HostFSDecisionProof(
		Proof023RealGate2Lifecycle,
		"real-gate",
		"real gate proof must be separate from local-fast completion",
		[]CoveredClaim{Claim023GuestStagedRead, Claim023HostLowerBeforeApply, Claim023Apply},
	))
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	err := Require023LocalFastComplete(manifest)
	if err == nil || !strings.Contains(err.Error(), "real Gate 2") {
		t.Fatalf("expected real Gate 2 substitution rejection, got %v", err)
	}
}

func TestRequire024LocalFastComplete(t *testing.T) {
	manifest := NewManifest("test", false)
	manifest.Proofs = DoctorPackageRecoveryLocalFastProofs()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := Require024LocalFastComplete(manifest); err != nil {
		t.Fatal(err)
	}
}

func TestRequire024LocalFastCompleteRejectsMissingProof(t *testing.T) {
	manifest := NewManifest("test", false)
	manifest.Proofs = DoctorPackageRecoveryLocalFastProofs()
	manifest.Proofs = manifest.Proofs[:len(manifest.Proofs)-1]
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	err := Require024LocalFastComplete(manifest)
	if err == nil || !strings.Contains(err.Error(), Proof024Redaction) {
		t.Fatalf("expected missing recovery redaction proof, got %v", err)
	}
}

func TestRequire025Complete(t *testing.T) {
	manifest := NewManifest("test", false)
	manifest.Proofs = DocsTruthProofs()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := Require025Complete(manifest); err != nil {
		t.Fatal(err)
	}
}

func TestRequire025CompleteRejectsMissingProof(t *testing.T) {
	manifest := NewManifest("test", false)
	manifest.Proofs = DocsTruthProofs()
	manifest.Proofs = manifest.Proofs[:len(manifest.Proofs)-1]
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	err := Require025Complete(manifest)
	if err == nil || !strings.Contains(err.Error(), Proof025CrossDoc) {
		t.Fatalf("expected missing cross-doc proof, got %v", err)
	}
}

func aggregateTestManifest(t *testing.T, proofIDs ...string) Manifest {
	t.Helper()
	proofs := make([]ProofEntry, 0, len(proofIDs))
	for _, proofID := range proofIDs {
		var requirement ProofRequirement
		for _, candidate := range ProductHardeningRequirements() {
			if candidate.ProofID == proofID {
				requirement = candidate
				break
			}
		}
		if requirement.ProofID == "" {
			t.Fatalf("missing registry requirement for %s", proofID)
		}
		proofs = append(proofs, ProofEntry{
			ProofID:         proofID,
			FeatureID:       requirement.FeatureID,
			Mode:            aggregateTestMode(proofID),
			EvidenceClass:   "local-ui-e2e",
			Status:          StatusPassed,
			CommandSummary:  "test command",
			CoveredClaims:   coveredClaimsForRequirement(requirement),
			Prerequisites:   []PrerequisiteStatus{{Name: "test", Status: "available"}},
			RedactionStatus: RedactionPassed,
		})
	}
	manifest := Manifest{
		Version:     Schema,
		GeneratedAt: time.Now().UTC(),
		Commit:      "test",
		Proofs:      proofs,
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func aggregateTestMode(proofID string) string {
	switch {
	case strings.Contains(proofID, ".webui."):
		return "browser-e2e"
	case strings.Contains(proofID, ".tui."):
		return "pty-e2e"
	case strings.Contains(proofID, ".docs."):
		return "docs"
	default:
		return "schema"
	}
}
