package productevidence

import (
	"strings"
	"testing"
	"time"
)

func validManifest() Manifest {
	m := NewManifest("abc123", false)
	start := time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Second)
	m.Proofs = []ProofEntry{{
		ProofID:        Proof021EvidenceSchema,
		FeatureID:      Feature021,
		Mode:           "schema",
		EvidenceClass:  "local-fast",
		Status:         StatusPassed,
		CommandSummary: "go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json product-hardening-evidence.json",
		CoveredClaims:  []CoveredClaim{Claim021EvidenceSchema},
		Prerequisites:  []PrerequisiteStatus{{Name: "schema-validator", Status: "available"}},
		Artifacts: []ArtifactRef{{
			Kind:            "schema",
			Path:            "schemas/product-hardening-evidence.schema.json",
			RedactionStatus: RedactionPassed,
		}},
		RedactionStatus: RedactionPassed,
		StartedAt:       &start,
		EndedAt:         &end,
		Host:            &HostSummary{OS: "darwin", Arch: "arm64"},
	}}
	return m
}

func TestManifestValidates(t *testing.T) {
	if err := validManifest().Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestManifestRejectsNotRunWithoutReason(t *testing.T) {
	m := validManifest()
	m.Proofs[0].Status = StatusNotRun
	m.Proofs[0].RedactionStatus = RedactionNotRun
	m.Proofs[0].NotRunReason = ""
	m.Proofs[0].Prerequisites = nil
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "not-run") {
		t.Fatalf("not-run without reason err=%v, want not-run rejection", err)
	}
}

func TestManifestRejectsPassedProofWithoutRedaction(t *testing.T) {
	m := validManifest()
	m.Proofs[0].RedactionStatus = RedactionNotRun
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "redactionStatus=passed") {
		t.Fatalf("passed proof without redaction err=%v, want redaction rejection", err)
	}
}

func TestArtifactPathMustBeRelative(t *testing.T) {
	m := validManifest()
	m.Proofs[0].Artifacts[0].Path = "/tmp/secret.log"
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("absolute artifact err=%v, want relative path rejection", err)
	}
}

func TestNotRunProofHelperValidates(t *testing.T) {
	m := NewManifest("abc123", false)
	m.Proofs = []ProofEntry{
		NotRunProof(
			Proof021WebUIBrowserConsole,
			Feature021,
			"browser-e2e",
			"local-ui-e2e",
			"scripts/test-ui-e2e.sh --browser --out <evidence-dir>",
			"browser prerequisite missing",
			[]CoveredClaim{Claim021BrowserNotRun},
			[]PrerequisiteStatus{{Name: "browser", Status: "missing", Reason: "not installed"}},
		),
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("not-run proof rejected: %v", err)
	}
}
