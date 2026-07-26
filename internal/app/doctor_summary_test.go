package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDoctorDefaultIsConciseAndDetailedModesRetainFindings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setSafeBrowserPathForAppTest(t)
	workspace := t.TempDir()

	var concise, stderr bytes.Buffer
	args := []string{"doctor", "--backend", "native", "--workspace", workspace}
	if code := Main(args, &concise, &stderr); code != 0 {
		t.Fatalf("concise doctor exit=%d stderr=%s stdout=%s", code, stderr.String(), concise.String())
	}
	for _, want := range []string{
		"Hideout doctor: Needs attention",
		"Profile: default",
		"Isolation: native development harness",
		"Network: direct — network origin visible",
		"Next: hideout setup",
		"Details: hideout doctor --verbose",
	} {
		if !strings.Contains(concise.String(), want) {
			t.Fatalf("concise doctor missing %q:\n%s", want, concise.String())
		}
	}
	for _, unwanted := range []string{"manager: ok", "mount: ok", "scripts/test-"} {
		if strings.Contains(concise.String(), unwanted) {
			t.Fatalf("concise doctor exposed %q:\n%s", unwanted, concise.String())
		}
	}

	for _, extra := range [][]string{
		{"--verbose"},
		{"--level", "deep"},
		{"--feature", "workspace"},
	} {
		var out, errOut bytes.Buffer
		detailedArgs := append(append([]string(nil), args...), extra...)
		if code := Main(detailedArgs, &out, &errOut); code != 0 {
			t.Fatalf("doctor %v exit=%d stderr=%s stdout=%s", extra, code, errOut.String(), out.String())
		}
		if !strings.Contains(out.String(), "manager: ok") ||
			!strings.Contains(out.String(), "summary: pass=") {
			t.Fatalf("doctor %v did not retain detailed findings:\n%s", extra, out.String())
		}
	}
}

func TestDoctorJSONRemainsCompleteWhenVerboseIsOmitted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setSafeBrowserPathForAppTest(t)

	var out, stderr bytes.Buffer
	if code := Main([]string{"doctor", "--backend", "native", "--format", "json"}, &out, &stderr); code != 0 {
		t.Fatalf("doctor JSON exit=%d stderr=%s stdout=%s", code, stderr.String(), out.String())
	}
	var decoded struct {
		Schema   string `json:"schema"`
		Findings []struct {
			CheckID string `json:"checkId"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}
	if decoded.Schema != "hideout.doctor-report/v1" || len(decoded.Findings) < 10 {
		t.Fatalf("JSON report was compacted: schema=%q findings=%d", decoded.Schema, len(decoded.Findings))
	}
}

func TestDoctorConciseBlockedResultHasReasonAndSafeAction(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")

	var out, stderr bytes.Buffer
	if code := Main([]string{"doctor", "--backend", "lima"}, &out, &stderr); code == 0 {
		t.Fatalf("missing Lima unexpectedly passed:\n%s", out.String())
	}
	for _, want := range []string{
		"Hideout doctor: Blocked",
		"Problem [backend]: lima unavailable",
		"Next: brew install lima",
		"Details: hideout doctor --verbose",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("blocked doctor missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "scripts/test-") {
		t.Fatalf("blocked doctor exposed maintainer action:\n%s", out.String())
	}
}
