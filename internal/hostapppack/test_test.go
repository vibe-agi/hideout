package hostapppack

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRunQualityTestsPassFailAndNotRun(t *testing.T) {
	manifest := validHostAppManifest()
	manifest.Tests = []TestVector{{
		ID: "open", BindingID: manifest.Bindings[0].ID,
		Argv:     []string{"cursor", "src/main.go"},
		Expected: TestExpectation{Resource: "/workspace/src/main.go", WindowMode: "reuse"},
	}}
	passed, err := RunQualityTests(manifest, "rev_1234", time.Unix(1, 0).UTC())
	if err != nil || passed.Status != TestPassed || passed.Passed != 1 || passed.Failed != 0 {
		t.Fatalf("quality test did not pass: %+v err=%v", passed, err)
	}

	manifest.Tests[0].Expected.Resource = "/workspace/wrong"
	failed, err := RunQualityTests(manifest, "rev_1234", time.Unix(1, 0).UTC())
	if err != nil || failed.Status != TestFailed || failed.Failed != 1 || len(failed.Failures) != 1 {
		t.Fatalf("quality test did not fail honestly: %+v err=%v", failed, err)
	}

	manifest.Tests = []TestVector{}
	notRun, err := RunQualityTests(manifest, "rev_1234", time.Unix(1, 0).UTC())
	if err != nil || notRun.Status != TestNotRun || notRun.Passed != 0 || notRun.Failed != 0 {
		t.Fatalf("missing tests were not visible as not-run: %+v err=%v", notRun, err)
	}
}

func TestRunQualityTestsHasNoAuthorityOrSecurityBadge(t *testing.T) {
	manifest := validHostAppManifest()
	manifest.Tests = []TestVector{{
		ID: "unknown-flag", BindingID: manifest.Bindings[0].ID,
		Argv:     []string{"cursor", "--execute-host", "."},
		Expected: TestExpectation{Resource: "/workspace", WindowMode: "reuse"},
	}}
	result, err := RunQualityTests(manifest, "rev_1234", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != TestFailed || !strings.Contains(strings.Join(result.Failures, " "), "unrecognized") {
		t.Fatalf("authority-like argv did not fail strict grammar: %+v", result)
	}
	encoded := strings.ToLower(result.Status + " " + strings.Join(result.Failures, " "))
	for _, forbidden := range []string{"certified", "secure", "approved", "trusted"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("quality result invented security status %q: %+v", forbidden, result)
		}
	}
}

func TestRunQualityTestsBoundsPersistedFailureDetails(t *testing.T) {
	manifest := validHostAppManifest()
	manifest.Tests[0].Argv = []string{"cursor", "--" + strings.Repeat("界", 300)}
	result, err := RunQualityTests(manifest, "rev_1234", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != TestFailed || len(result.Results) != 1 || len(result.Failures) != 1 {
		t.Fatalf("unexpected bounded failure result: %+v", result)
	}
	for label, value := range map[string]string{
		"reason":  result.Results[0].Reason,
		"failure": result.Failures[0],
	} {
		if len(value) > MaxDescriptionBytes || !utf8.ValidString(value) {
			t.Fatalf("%s is not bounded valid UTF-8: %q", label, value)
		}
	}
	if err := validateTestResult(result); err != nil {
		t.Fatalf("runner produced an unstorable quality result: %v", err)
	}
}
