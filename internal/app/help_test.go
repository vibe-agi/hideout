package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrimaryHelpShowsOrdinaryJourneyBeforeExpandedIndex(t *testing.T) {
	var noArgs, help, stderr bytes.Buffer
	if code := Main(nil, &noArgs, &stderr); code != 0 {
		t.Fatalf("no-args exit=%d stderr=%s", code, stderr.String())
	}
	stderr.Reset()
	if code := Main([]string{"help"}, &help, &stderr); code != 0 {
		t.Fatalf("help exit=%d stderr=%s", code, stderr.String())
	}
	if noArgs.String() != help.String() {
		t.Fatalf("no-args and help differ:\nNO ARGS:\n%s\nHELP:\n%s", noArgs.String(), help.String())
	}
	text := help.String()
	for _, want := range []string{
		"hideout setup",
		"hideout doctor",
		"hideout run -- git status --short",
		"hideout help --all",
		"direct networking does not hide",
		"macOS arm64",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("primary help missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "hideout lab portbridge") ||
		strings.Contains(text, "hideout shim build-linux") {
		t.Fatalf("primary help exposed developer/lab inventory:\n%s", text)
	}
	nonBlank := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			nonBlank++
		}
	}
	if nonBlank > 20 {
		t.Fatalf("primary help has %d non-blank lines, want <=20:\n%s", nonBlank, text)
	}
}

func TestExpandedHelpRetainsCompleteCommandInventory(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Main([]string{"help", "--all"}, &out, &stderr); code != 0 {
		t.Fatalf("help --all exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{
		"First run:",
		"Run and explain:",
		"Profile and HostFS:",
		"Inspect and manage:",
		"Advanced and developer:",
		"Lab probes:",
		"hideout adapter-pack <install|list|inspect|test|enable|disable|upgrade|revoke>",
		"hideout shim build-linux",
		"hideout lab portbridge loopback",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("expanded help missing %q:\n%s", want, out.String())
		}
	}
}

func TestContextualHelpIsSuccessfulAndWritesNoState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := [][]string{
		{"help", "setup"},
		{"setup", "--help"},
		{"help", "run"},
		{"run", "--help"},
		{"help", "doctor"},
		{"doctor", "--help"},
		{"help", "privacy"},
		{"help", "package"},
		{"package", "--help"},
		{"help", "support"},
		{"support", "--help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var out, stderr bytes.Buffer
			if code := Main(args, &out, &stderr); code != 0 {
				t.Fatalf("%v exit=%d stderr=%s stdout=%s", args, code, stderr.String(), out.String())
			}
			if out.Len() == 0 {
				t.Fatalf("%v produced empty help", args)
			}
			if _, err := os.Lstat(filepath.Join(home, ".hideout")); !os.IsNotExist(err) {
				t.Fatalf("%v created store state: %v", args, err)
			}
		})
	}
}

func TestHelpRejectsUnknownTopicWithExpandedIndexHint(t *testing.T) {
	var out, stderr bytes.Buffer
	code := Main([]string{"help", "unknown-topic"}, &out, &stderr)
	if code == 0 {
		t.Fatalf("unknown help topic succeeded: %s", out.String())
	}
	if !strings.Contains(stderr.String(), `unknown help topic "unknown-topic"`) ||
		!strings.Contains(stderr.String(), "hideout help --all") {
		t.Fatalf("unknown help error is not actionable: %s", stderr.String())
	}
}
