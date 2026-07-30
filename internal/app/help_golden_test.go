package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpGoldens(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		columns string
		noColor bool
		golden  string
	}{
		{
			name: "primary", columns: "80",
			args: nil, golden: "primary_80.txt",
		},
		{
			name: "contextual connect", columns: "80",
			args: []string{"help", "connect"}, golden: "contextual_connect_80.txt",
		},
		{
			name: "contextual connect narrow", columns: "48",
			args: []string{"help", "connect"}, golden: "contextual_connect_48.txt",
		},
		{
			name: "grouped filtered all", columns: "80",
			args: []string{"help", "all", "proxy"}, golden: "all_proxy_80.txt",
		},
		{
			name: "primary no color", columns: "80", noColor: true,
			args: nil, golden: "primary_no_color_80.txt",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("COLUMNS", test.columns)
			if test.noColor {
				t.Setenv("NO_COLOR", "1")
			} else {
				t.Setenv("NO_COLOR", "")
			}
			var stdout, stderr bytes.Buffer
			if code := Main(test.args, &stdout, &stderr); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, stderr.String())
			}
			if strings.Contains(stdout.String(), "\x1b[") {
				t.Fatalf("help contains terminal control sequences: %q", stdout.String())
			}
			assertHelpGolden(t, test.golden, stdout.String())
		})
	}
}

func TestHelpFindsCommonTaskInAtMostTwoInvocations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("primary to proxy configuration", func(t *testing.T) {
		first := runHelpInvocation(t, []string{"help"})
		if !strings.Contains(first, "hideout help connect") {
			t.Fatalf("primary help has no proxy task route:\n%s", first)
		}
		second := runHelpInvocation(t, []string{"help", "connect"})
		for _, want := range []string{
			"Purpose:",
			"Usage:",
			"Before:",
			"Effects:",
			"Safety:",
			"Recovery:",
			"Next:",
			"hideout secret set <ref>",
		} {
			if !strings.Contains(second, want) {
				t.Fatalf("contextual help missing %q:\n%s", want, second)
			}
		}
	})

	t.Run("search to secret migration", func(t *testing.T) {
		first := runHelpInvocation(t, []string{"help", "search", "proxy"})
		if !strings.Contains(first, "connect") || !strings.Contains(first, "secret") {
			t.Fatalf("proxy search did not find connection and secret tasks:\n%s", first)
		}
		second := runHelpInvocation(t, []string{"help", "secret"})
		for _, want := range []string{
			"hideout secret set local-proxy",
			"daemon startup environment",
			"stopping or recreating the VM is not required",
		} {
			if !strings.Contains(second, want) {
				t.Fatalf("secret help missing %q:\n%s", want, second)
			}
		}
	})

	if _, err := os.Lstat(filepath.Join(home, ".hideout")); !os.IsNotExist(err) {
		t.Fatalf("help journey created store state: %v", err)
	}
}

func runHelpInvocation(t *testing.T, args []string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Main(args, &stdout, &stderr); code != 0 {
		t.Fatalf("%v exit=%d stderr=%s", args, code, stderr.String())
	}
	return stdout.String()
}

func assertHelpGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "help", name)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("help differs from %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}
