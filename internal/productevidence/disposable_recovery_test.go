package productevidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProofRegistryCovers042AndKeepsRealRecoveryExact(t *testing.T) {
	want := []string{
		Proof042Gate0Mechanics,
		Proof042Gate0Model,
		Proof042RealRecovery,
		Proof042RealGate2NotRun,
		Proof042DocsClaimBoundary,
	}
	requirements := RequirementsForFeature(Feature042)
	if len(requirements) != len(want) || len(Required042ProofIDs) != len(want) {
		t.Fatalf("042 requirements=%d requiredIDs=%d want %d", len(requirements), len(Required042ProofIDs), len(want))
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
			t.Fatalf("042 proof %s is not registered", proofID)
		}
	}
	real := seen[Proof042RealRecovery]
	if real.Layer != LayerRealGate || real.RequiredFor != RequiredForReleaseCandidate ||
		real.RuntimePolicy != RuntimePolicyExactReal ||
		real.RequiredEvidenceClass != "disposable-recovery-real-gate2" ||
		real.ArtifactValidator != ArtifactValidatorDisposableRecoveryV1 {
		t.Fatalf("042 real proof has weak scope: %+v", real)
	}
	if seen[Proof042RealGate2NotRun].RequiredFor != RequiredForSupportingOnly ||
		seen[Proof042RealGate2NotRun].RuntimePolicy != RuntimePolicyNone {
		t.Fatalf("042 not-run proof could satisfy real recovery: %+v", seen[Proof042RealGate2NotRun])
	}
	for index := 1; index <= 24; index++ {
		claimID := fmt.Sprintf("042.FR-%03d", index)
		if !covered[claimID] {
			t.Fatalf("042 registry does not cover %s", claimID)
		}
	}
	for index := 1; index <= 8; index++ {
		claimID := fmt.Sprintf("042.SC-%03d", index)
		if !covered[claimID] {
			t.Fatalf("042 registry does not cover %s", claimID)
		}
	}
}

func TestDisposableRecoveryValidatorRejectsFalseGreenArtifacts(t *testing.T) {
	valid := newDisposableRecoveryFixture()
	if err := validateDisposableRecoveryFixture(valid); err != nil {
		t.Fatalf("valid disposable recovery evidence: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*disposableRecoveryEvidence)
		want   string
	}{
		{name: "dirty identity", mutate: func(e *disposableRecoveryEvidence) { e.Dirty = true }, want: "clean candidate"},
		{name: "missing check", mutate: func(e *disposableRecoveryEvidence) {
			delete(e.Checks, "nonDisposableRefused")
		}, want: "inventory"},
		{name: "unauthorized destruction", mutate: func(e *disposableRecoveryEvidence) {
			e.DestructiveCalls.Unauthorized = 1
		}, want: "unauthorized"},
		{name: "environment residue", mutate: func(e *disposableRecoveryEvidence) {
			e.Residue.EnvironmentRecords = 1
		}, want: "residue"},
		{name: "journal residue", mutate: func(e *disposableRecoveryEvidence) {
			e.Residue.LifecycleJournals = 1
		}, want: "residue"},
		{name: "undersampled ordinary runs", mutate: func(e *disposableRecoveryEvidence) {
			e.Samples.OrdinaryRuns = 29
		}, want: "30 ordinary"},
		{name: "undersampled schedules", mutate: func(e *disposableRecoveryEvidence) {
			e.Samples.CrashSchedules = 99
		}, want: "100 crash"},
		{name: "timeout", mutate: func(e *disposableRecoveryEvidence) {
			e.Timing.RecoveryTimeouts = 1
		}, want: "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := newDisposableRecoveryFixture()
			tc.mutate(&current)
			if err := validateDisposableRecoveryFixture(current); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want substring %q", err, tc.want)
			}
		})
	}

	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data[:len(data)-1], []byte(`,"unexpected":true}`)...)
	if err := validateDisposableRecoveryArtifact(data, valid.Commit); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error=%v", err)
	}
}

func TestRetainedDisposableRecoveryEvidencePassesProductionEvaluator(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("HIDEOUT_042_EVIDENCE_DIR"))
	if root == "" {
		t.Skip("set HIDEOUT_042_EVIDENCE_DIR to validate retained real Gate 2 evidence")
	}
	manifest, err := ReadFile(filepath.Join(root, "product-hardening-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.PackageIdentity == nil {
		t.Fatal("retained 042 evidence has no package identity")
	}
	var proof *ProofEntry
	for index := range manifest.Proofs {
		if manifest.Proofs[index].ProofID == Proof042RealRecovery {
			proof = &manifest.Proofs[index]
			break
		}
	}
	if proof == nil || proof.Runtime == nil {
		t.Fatal("retained 042 evidence has no runtime-bound real proof")
	}
	requirements := []ProofRequirement{}
	for _, requirement := range RequirementsForFeature(Feature042) {
		if requirement.ProofID == Proof042RealRecovery {
			requirements = append(requirements, requirement)
		}
	}
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements:    requirements,
		Target:          RequiredForReleaseCandidate,
		ExpectedCommit:  manifest.Commit,
		ExpectedPackage: manifest.PackageIdentity,
		ArtifactRoots:   map[string]string{Proof042RealRecovery: root},
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
		t.Fatalf("retained 042 evidence did not satisfy production evaluation: %v\n%+v", err, report.Results)
	}
}

func newDisposableRecoveryFixture() disposableRecoveryEvidence {
	value := disposableRecoveryEvidence{
		Schema:    "hideout.disposable-recovery-gate2/v1",
		Status:    StatusPassed,
		Commit:    strings.Repeat("a", 40),
		Backend:   "lima",
		HostOS:    "darwin",
		HostArch:  "arm64",
		GuestArch: "aarch64",
		Checks:    map[string]bool{},
	}
	for _, check := range requiredDisposableRecoveryChecks {
		value.Checks[check] = true
	}
	value.Samples.OrdinaryRuns = 30
	value.Samples.CrashSchedules = 100
	value.Samples.RestartCheckpoints = 4
	value.Timing.StartupStatusP95MS = 100
	value.Timing.RecoveryP95MS = 800
	value.DestructiveCalls.Authorized = 8
	value.NonClaims.HistoricalJournalOnly = "not-auto-recovered"
	value.NonClaims.OrdinaryOrphans = "report-only"
	return value
}

func validateDisposableRecoveryFixture(value disposableRecoveryEvidence) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return validateDisposableRecoveryArtifact(data, value.Commit)
}
