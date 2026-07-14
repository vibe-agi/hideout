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

func TestRedactPublicEvidenceRemovesControlPlaneAndLocalPaths(t *testing.T) {
	input := []byte("token=cap_0123456789abcdef0123456789abcdef\n" +
		"mac=/Users/alice/private/project\n" +
		"temp=/private/var/folders/aa/bb/T/candidate.json\n" +
		"posix=/tmp/hideout-candidate/evidence.json\n" +
		`windows={"path":"C:\\Users\\alice\\private\\project"}` + "\n" +
		"marker=gate2: passed\n")

	redacted, review, err := RedactPublicEvidence(input)
	if err != nil {
		t.Fatal(err)
	}
	want := "token=REDACTED\n" +
		"mac=<redacted:local-path>\n" +
		"temp=<redacted:local-path>\n" +
		"posix=<redacted:local-path>\n" +
		`windows={"path":"<redacted:local-path>"}` + "\n" +
		"marker=gate2: passed\n"
	if string(redacted) != want {
		t.Fatalf("redacted evidence mismatch:\n got=%q\nwant=%q", redacted, want)
	}
	if review.Decision.Mode != DecisionRedact || len(review.Stages) != 2 {
		t.Fatalf("review=%+v", review)
	}
	again, _, err := RedactPublicEvidence(input)
	if err != nil || string(again) != string(redacted) {
		t.Fatalf("redaction is not deterministic: err=%v again=%q", err, again)
	}
}
