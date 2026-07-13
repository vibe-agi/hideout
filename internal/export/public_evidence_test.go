package export

import "testing"

func TestReviewPublicEvidenceRejectsControlPlaneAndLocalPaths(t *testing.T) {
	for _, value := range []string{
		`{"token":"cap_0123456789abcdef0123456789abcdef"}`,
		`{"path":"/Users/alice/private/project"}`,
		`{"path":"/home/alice/private/project"}`,
		`{"path":"/private/var/folders/aa/bb/T/candidate.json"}`,
		`{"path":"/var/folders/aa/bb/T/candidate.json"}`,
		`{"path":"/tmp/hideout-candidate/evidence.json"}`,
		`{"path":"C:\\Users\\alice\\private\\project"}`,
	} {
		if _, err := ReviewPublicEvidence([]byte(value)); err == nil {
			t.Fatalf("sensitive public evidence unexpectedly passed: %s", value)
		}
	}
}

func TestReviewPublicEvidenceReturnsExistingExportDecision(t *testing.T) {
	review, err := ReviewPublicEvidence([]byte(`{"status":"passed","path":"proofs/result.json"}`))
	if err != nil {
		t.Fatal(err)
	}
	if review.Decision.Mode != DecisionRedact || review.Decision.Channel != DecisionChannelFlag {
		t.Fatalf("decision=%+v", review.Decision)
	}
	if len(review.Stages) != 2 || review.Stages[0].Name != "control-plane" || review.Stages[1].Name != PublicEvidenceLocalPathStage {
		t.Fatalf("stages=%+v", review.Stages)
	}
}
