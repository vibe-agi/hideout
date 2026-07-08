package adapterpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunTestsRequiresTestsAndCoreValidation(t *testing.T) {
	dir := testPackDir(t, "function decideCommandAdapter(){ return {outcome:'deny', reason:'blocked unsafe'} }")
	store := NewStore(t.TempDir())
	entry, rev, err := store.Install(SourceSpec{Kind: SourceLocal, Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.RunTests(t.Context(), entry.PackID, rev.RevisionID)
	if err != nil {
		t.Fatalf("run tests: %v", err)
	}
	if result.Status != TestPassed || result.Passed != 1 {
		t.Fatalf("expected passing tests, got %+v", result)
	}
}

func TestRunTestsBlocksMissingTests(t *testing.T) {
	manifest := validManifest()
	manifest.Tests = nil
	dir := t.TempDir()
	writePackManifest(t, dir, manifest)
	if err := writeAdapterScript(t, dir, "function decideCommandAdapter(){ return {outcome:'deny', reason:'blocked'} }"); err != nil {
		t.Fatal(err)
	}
	store := NewStore(t.TempDir())
	entry, rev, err := store.Install(SourceSpec{Kind: SourceLocal, Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.RunTests(t.Context(), entry.PackID, rev.RevisionID)
	if err != nil {
		t.Fatalf("run tests: %v", err)
	}
	if result.Status != TestBlocked || !strings.Contains(strings.Join(result.Failures, "\n"), "no tests") {
		t.Fatalf("expected blocked missing-test result, got %+v", result)
	}
}

func TestRunTestsFailsClosedOnInvalidOutcome(t *testing.T) {
	dir := testPackDir(t, "function decideCommandAdapter(){ return {outcome:'launchMissiles', reason:'bad'} }")
	store := NewStore(t.TempDir())
	entry, rev, err := store.Install(SourceSpec{Kind: SourceLocal, Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.RunTests(t.Context(), entry.PackID, rev.RevisionID)
	if err != nil {
		t.Fatalf("run tests should record failure, not crash: %v", err)
	}
	if result.Status != TestFailed || len(result.Failures) == 0 {
		t.Fatalf("expected failed result, got %+v", result)
	}
	if !strings.Contains(strings.Join(result.Failures, "\n"), "unsupported") {
		t.Fatalf("expected strict outcome failure, got %+v", result.Failures)
	}
}

func TestRunTestsFailsClosedOnScriptExceptionTimeoutAndForbiddenAPI(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		wantReason string
	}{
		{
			name:       "exception",
			source:     `function decideCommandAdapter(){ throw new Error("boom") }`,
			wantReason: "boom",
		},
		{
			name:       "timeout",
			source:     `function decideCommandAdapter(){ while (true) {} }`,
			wantReason: "timed out",
		},
		{
			name:       "forbidden-api",
			source:     `function decideCommandAdapter(){ return {outcome:"deny", reason:"blocked require="+String(typeof require)+",process="+String(typeof process)} }`,
			wantReason: "undefined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := testPackDir(t, tt.source)
			store := NewStore(t.TempDir())
			entry, rev, err := store.Install(SourceSpec{Kind: SourceLocal, Path: dir})
			if err != nil {
				t.Fatal(err)
			}
			result, err := store.RunTests(t.Context(), entry.PackID, rev.RevisionID)
			if err != nil {
				t.Fatalf("run tests should record result, not crash: %v", err)
			}
			joined := strings.Join(result.Failures, "\n")
			if tt.name == "forbidden-api" {
				if result.Status != TestPassed {
					t.Fatalf("script should be able to prove forbidden globals are absent, got %+v", result)
				}
				return
			}
			if result.Status != TestFailed || !strings.Contains(joined, tt.wantReason) {
				t.Fatalf("expected failure containing %q, got %+v", tt.wantReason, result)
			}
		})
	}
}

func writeAdapterScript(t *testing.T, dir, source string) error {
	t.Helper()
	return os.WriteFile(filepath.Join(dir, "adapter.js"), []byte(source), 0o600)
}
