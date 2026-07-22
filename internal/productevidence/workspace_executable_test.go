package productevidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProofRegistryCovers041WithoutLettingNotRunSatisfyRealClaims(t *testing.T) {
	want := []string{
		Proof041Gate0Mechanics,
		Proof041RealExecution,
		Proof041RealGate2NotRun,
		Proof041DocsClaimBoundary,
	}
	requirements := RequirementsForFeature(Feature041)
	if len(requirements) != len(want) || len(Required041ProofIDs) != len(want) {
		t.Fatalf("041 requirements=%d requiredIDs=%d want %d", len(requirements), len(Required041ProofIDs), len(want))
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
			t.Fatalf("041 proof %s is not registered", proofID)
		}
	}
	real := seen[Proof041RealExecution]
	if real.Layer != LayerRealGate || real.RequiredFor != RequiredForReleaseCandidate ||
		real.RuntimePolicy != RuntimePolicyExactReal || real.RequiredEvidenceClass == "" ||
		real.ArtifactValidator != ArtifactValidatorWorkspaceExecutableV1 {
		t.Fatalf("041 real proof has weak scope: %+v", real)
	}
	if seen[Proof041RealGate2NotRun].RequiredFor != RequiredForSupportingOnly ||
		seen[Proof041RealGate2NotRun].RuntimePolicy != RuntimePolicyNone {
		t.Fatalf("041 not-run proof could satisfy a real claim: %+v", seen[Proof041RealGate2NotRun])
	}
	for index := 1; index <= 16; index++ {
		claimID := fmt.Sprintf("041.FR-%03d", index)
		if !covered[claimID] {
			t.Fatalf("041 registry does not cover %s", claimID)
		}
	}
	for index := 1; index <= 7; index++ {
		claimID := fmt.Sprintf("041.SC-%03d", index)
		if !covered[claimID] {
			t.Fatalf("041 registry does not cover %s", claimID)
		}
	}
}

func TestWorkspaceExecutableValidatorRejectsFalseGreenArtifacts(t *testing.T) {
	valid := newWorkspaceExecutableFixture()
	if err := validateWorkspaceExecutableFixture(valid); err != nil {
		t.Fatalf("valid workspace executable evidence: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*workspaceExecutableEvidence)
		want   string
	}{
		{name: "dirty candidate", mutate: func(e *workspaceExecutableEvidence) { e.Dirty = true }, want: "clean candidate"},
		{name: "mechanism drift", mutate: func(e *workspaceExecutableEvidence) { e.WorkspaceMechanism = "virtiofs" }, want: "mechanism"},
		{name: "missing check", mutate: func(e *workspaceExecutableEvidence) { delete(e.Checks, "directBinary") }, want: "inventory"},
		{name: "false check", mutate: func(e *workspaceExecutableEvidence) { e.Checks["noHostFallback"] = false }, want: "noHostFallback"},
		{name: "undersampled", mutate: func(e *workspaceExecutableEvidence) { e.Samples = 29 }, want: "fewer than 30"},
		{name: "slow p95", mutate: func(e *workspaceExecutableEvidence) { e.WarmFirstOutputP95MS = 2001 }, want: "two seconds"},
		{name: "median regression", mutate: func(e *workspaceExecutableEvidence) { e.MedianRegressionRatio = 1.11 }, want: "ten percent"},
		{name: "static overclaim", mutate: func(e *workspaceExecutableEvidence) { e.NonClaims.StaticVirtiofs = "supported" }, want: "overclaims"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := newWorkspaceExecutableFixture()
			tc.mutate(&current)
			if err := validateWorkspaceExecutableFixture(current); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want substring %q", err, tc.want)
			}
		})
	}

	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data[:len(data)-1], []byte(`,"unexpected":true}`)...)
	if err := validateWorkspaceExecutableArtifact(data, valid.Commit); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error=%v", err)
	}
}

func TestRetainedWorkspaceExecutableEvidencePassesProductionEvaluator(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("HIDEOUT_041_EVIDENCE_DIR"))
	if root == "" {
		t.Skip("set HIDEOUT_041_EVIDENCE_DIR to validate retained real Gate 2 evidence")
	}
	manifest, err := ReadFile(filepath.Join(root, "product-hardening-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.PackageIdentity == nil {
		t.Fatal("retained 041 evidence has no package identity")
	}
	var proof *ProofEntry
	for index := range manifest.Proofs {
		if manifest.Proofs[index].ProofID == Proof041RealExecution {
			proof = &manifest.Proofs[index]
			break
		}
	}
	if proof == nil || proof.Runtime == nil {
		t.Fatal("retained 041 evidence has no runtime-bound real proof")
	}
	requirements := []ProofRequirement{}
	for _, requirement := range RequirementsForFeature(Feature041) {
		if requirement.ProofID == Proof041RealExecution {
			requirements = append(requirements, requirement)
		}
	}
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements:    requirements,
		Target:          RequiredForReleaseCandidate,
		ExpectedCommit:  manifest.Commit,
		ExpectedPackage: manifest.PackageIdentity,
		ArtifactRoots:   map[string]string{Proof041RealExecution: root},
		ExpectedRuntime: &RuntimeExpectation{
			Family: proof.Runtime.Family, Revision: proof.Runtime.Revision,
			ArtifactSHA256: proof.Runtime.ArtifactSHA256,
			HostOS:         proof.Runtime.HostOS, HostArch: proof.Runtime.HostArch,
			GuestArch: proof.Runtime.GuestArch, BuildCommit: proof.Runtime.BuildCommit,
			RequireClean: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := report.RequireSatisfied(); err != nil {
		t.Fatalf("retained 041 evidence did not satisfy production evaluation: %v\n%+v", err, report.Results)
	}
}

func newWorkspaceExecutableFixture() workspaceExecutableEvidence {
	value := workspaceExecutableEvidence{
		Schema:                "hideout.workspace-executable-gate2/v1",
		Status:                StatusPassed,
		Commit:                strings.Repeat("a", 40),
		Backend:               "lima",
		HostOS:                "darwin",
		HostArch:              "arm64",
		GuestArch:             "aarch64",
		WorkspaceMechanism:    "workspace-portal",
		Checks:                map[string]bool{},
		Samples:               30,
		WarmFirstOutputP95MS:  800,
		MedianRegressionRatio: 1.02,
	}
	for _, check := range requiredWorkspaceExecutableChecks {
		value.Checks[check] = true
	}
	value.NonClaims.StaticVirtiofs = "not-claimed"
	return value
}

func validateWorkspaceExecutableFixture(value workspaceExecutableEvidence) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return validateWorkspaceExecutableArtifact(data, value.Commit)
}
