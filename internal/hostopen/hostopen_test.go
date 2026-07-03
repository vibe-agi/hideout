package hostopen

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type recordingRunner struct {
	lookPath map[string]string
	calls    []runnerCall
}

type runnerCall struct {
	name string
	args []string
}

func (r *recordingRunner) LookPath(file string) (string, error) {
	if path, ok := r.lookPath[file]; ok {
		return path, nil
	}
	return "", os.ErrNotExist
}

func (r *recordingRunner) Run(_ context.Context, name string, args []string, _ io.Reader, _, _ io.Writer) error {
	r.calls = append(r.calls, runnerCall{name: name, args: append([]string(nil), args...)})
	return nil
}

func TestDarwinURLCommandUsesIsolatedBrowserProfile(t *testing.T) {
	profileDir := filepath.Join(t.TempDir(), "browser")
	opener := Opener{BrowserProfileDir: profileDir, BrowserApp: "Chromium", GOOS: "darwin"}
	name, args, err := opener.URLCommand("https://example.com")
	if err != nil {
		t.Fatalf("URLCommand: %v", err)
	}
	if name != "/usr/bin/open" {
		t.Fatalf("name=%s", name)
	}
	want := []string{
		"-na", "Chromium", "--args",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--new-window",
		"https://example.com",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args=%v want %v", args, want)
	}
}

func TestDarwinURLCommandRejectsUnsupportedBrowserApp(t *testing.T) {
	opener := Opener{BrowserProfileDir: filepath.Join(t.TempDir(), "browser"), BrowserApp: "Safari", GOOS: "darwin"}
	name, args, err := opener.URLCommand("https://example.com")
	if err == nil || !strings.Contains(err.Error(), "not a supported isolated browser app") {
		t.Fatalf("expected unsupported browser app rejection, got name=%s args=%v err=%v", name, args, err)
	}
	if name != "" || args != nil {
		t.Fatalf("URL command should fail closed for unsupported browser app, got name=%s args=%v", name, args)
	}
}

func TestBrowserPathURLCommandUsesDirectBrowserBinary(t *testing.T) {
	profileDir := filepath.Join(t.TempDir(), "browser")
	opener := Opener{BrowserProfileDir: profileDir, BrowserPath: "/opt/browser", GOOS: "darwin"}
	name, args, err := opener.URLCommand("https://example.com")
	if err != nil {
		t.Fatalf("URLCommand: %v", err)
	}
	if name != "/opt/browser" {
		t.Fatalf("name=%s", name)
	}
	want := []string{"--user-data-dir=" + profileDir, "--no-first-run", "--new-window", "https://example.com"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args=%v want %v", args, want)
	}
}

func TestBrowserPathRejectsGenericURLOpeners(t *testing.T) {
	for _, browserPath := range []string{"/usr/bin/open", "/usr/bin/xdg-open"} {
		t.Run(browserPath, func(t *testing.T) {
			opener := Opener{BrowserProfileDir: filepath.Join(t.TempDir(), "browser"), BrowserPath: browserPath}
			name, args, err := opener.URLCommand("https://example.com")
			if err == nil || !strings.Contains(err.Error(), "generic URL opener") {
				t.Fatalf("expected generic opener rejection, got name=%s args=%v err=%v", name, args, err)
			}
			if name != "" || args != nil {
				t.Fatalf("URL command should fail closed for generic opener, got name=%s args=%v", name, args)
			}
		})
	}
}

func TestBrowserPathRejectsSymlinkToGenericURLOpener(t *testing.T) {
	dir := t.TempDir()
	generic := filepath.Join(dir, "xdg-open")
	if err := os.WriteFile(generic, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	browserPath := filepath.Join(dir, "browser")
	if err := os.Symlink(generic, browserPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	opener := Opener{BrowserProfileDir: filepath.Join(t.TempDir(), "browser"), BrowserPath: browserPath}
	name, args, err := opener.URLCommand("https://example.com")
	if err == nil || !strings.Contains(err.Error(), "generic URL opener") {
		t.Fatalf("expected generic opener symlink rejection, got name=%s args=%v err=%v", name, args, err)
	}
	if name != "" || args != nil {
		t.Fatalf("URL command should fail closed for generic opener symlink, got name=%s args=%v", name, args)
	}
}

func TestLinuxURLCommandRequiresIsolatedBrowserLauncher(t *testing.T) {
	runner := &recordingRunner{lookPath: map[string]string{"xdg-open": "/usr/bin/xdg-open"}}
	opener := Opener{
		BrowserProfileDir: filepath.Join(t.TempDir(), "browser"),
		Runner:            runner,
		GOOS:              "linux",
	}
	name, args, err := opener.URLCommand("https://example.com")
	if err == nil || !strings.Contains(err.Error(), "isolated browser launcher requires") {
		t.Fatalf("expected isolated browser launcher error, got name=%s args=%v err=%v", name, args, err)
	}
	if name != "" || args != nil {
		t.Fatalf("URL command should fail closed without fallback, got name=%s args=%v", name, args)
	}
}

func TestDarwinFileCommandUsesHostOpen(t *testing.T) {
	opener := Opener{GOOS: "darwin"}
	name, args, err := opener.FileCommand("/workspace/file.txt")
	if err != nil {
		t.Fatalf("FileCommand: %v", err)
	}
	if name != "/usr/bin/open" || !reflect.DeepEqual(args, []string{"/workspace/file.txt"}) {
		t.Fatalf("command=%s args=%v", name, args)
	}
}

func TestDryRunCreatesBrowserProfileAndDoesNotExecute(t *testing.T) {
	runner := &recordingRunner{}
	profileDir := filepath.Join(t.TempDir(), "browser")
	opener := Opener{BrowserProfileDir: profileDir, Runner: runner, DryRun: true, GOOS: "darwin"}
	if err := opener.OpenURL(context.Background(), "https://example.com"); err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	if _, err := os.Stat(profileDir); err != nil {
		t.Fatalf("browser profile dir missing: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("dry run executed commands: %+v", runner.calls)
	}
}
