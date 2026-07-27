package appopen

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// editorSpec is a test fixture describing an editor-style CLI. It is not tied to
// any specific application — the renderer is generic.
func editorSpec() LaunchSpec {
	return LaunchSpec{
		SafeIsolationFlags:  []string{"--disable-extensions"},
		IsolatedDataDirFlag: "--user-data-dir",
		NewWindowFlag:       "--new-window",
		ReuseWindowFlag:     "--reuse-window",
		GotoFlag:            "--goto",
		GotoSeparator:       ":",
		ForbiddenFlags:      []string{"--disable-workspace-trust"},
	}
}

func TestRenderArgvSafeMode(t *testing.T) {
	argv, err := RenderArgv(editorSpec(), OpenRequest{
		BinaryPath: "/bin/ed", Mode: ModeSafe, HostTarget: "/Users/alice/proj", SafeUserDataDir: "/tmp/iso",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(argv, "--user-data-dir") || !slices.Contains(argv, "/tmp/iso") || !slices.Contains(argv, "--disable-extensions") {
		t.Fatalf("safe argv lacks isolation: %v", argv)
	}
	if slices.Contains(argv, "--disable-workspace-trust") {
		t.Fatalf("safe argv must never contain the forbidden flag: %v", argv)
	}
	if argv[0] != "/bin/ed" {
		t.Fatalf("argv[0] = %q", argv[0])
	}
}

func TestRenderArgvSafeRequiresDataDir(t *testing.T) {
	if _, err := RenderArgv(editorSpec(), OpenRequest{BinaryPath: "/bin/ed", Mode: ModeSafe, HostTarget: "/x"}); err == nil {
		t.Fatal("safe mode with an isolated-data-dir flag requires the dir")
	}
}

func TestRenderArgvTrustedMode(t *testing.T) {
	argv, err := RenderArgv(editorSpec(), OpenRequest{BinaryPath: "/bin/ed", Mode: ModeTrusted, HostTarget: "/Users/alice/proj"})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(argv, "--disable-extensions") || slices.Contains(argv, "--user-data-dir") {
		t.Fatalf("trusted must not force safe isolation: %v", argv)
	}
	if slices.Contains(argv, "--disable-workspace-trust") {
		t.Fatalf("trusted must never disable workspace trust: %v", argv)
	}
}

func TestRenderArgvGoto(t *testing.T) {
	argv, err := RenderArgv(editorSpec(), OpenRequest{
		BinaryPath: "/bin/ed", Mode: ModeSafe, HostTarget: "/Users/alice/a.go", Line: 12, Column: 3, SafeUserDataDir: "/tmp/x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(argv, " "), "--goto /Users/alice/a.go:12:3") {
		t.Fatalf("goto not rendered: %v", argv)
	}
}

func TestRenderArgvWindowMode(t *testing.T) {
	nw, _ := RenderArgv(editorSpec(), OpenRequest{BinaryPath: "/bin/ed", Mode: ModeSafe, HostTarget: "/x", NewWindow: true, SafeUserDataDir: "/z"})
	if !slices.Contains(nw, "--new-window") {
		t.Fatalf("new window missing: %v", nw)
	}
	rw, _ := RenderArgv(editorSpec(), OpenRequest{BinaryPath: "/bin/ed", Mode: ModeSafe, HostTarget: "/x", SafeUserDataDir: "/z"})
	if !slices.Contains(rw, "--reuse-window") {
		t.Fatalf("reuse window default missing: %v", rw)
	}
}

func TestRenderArgvHardForbiddenFloor(t *testing.T) {
	// Even a recipe that omits the forbidden flag from its own list must not be
	// able to emit the framework's hard-forbidden flag. Simulate a recipe whose
	// data would try to inject it via an isolation flag.
	spec := editorSpec()
	spec.ForbiddenFlags = nil
	spec.SafeIsolationFlags = []string{"--disable-workspace-trust"}
	if _, err := RenderArgv(spec, OpenRequest{BinaryPath: "/bin/ed", Mode: ModeSafe, HostTarget: "/x", SafeUserDataDir: "/z"}); err == nil {
		t.Fatal("hard forbidden floor must reject --disable-workspace-trust even if recipe data omits it")
	}
}

func TestExecLauncherHandoffSurvivesBrokerContextCancellation(t *testing.T) {
	if os.Getenv("HIDEOUT_APPOPEN_HELPER") == "1" {
		marker := os.Args[len(os.Args)-2]
		release := os.Args[len(os.Args)-1]
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(release); err == nil {
				_ = os.WriteFile(marker, []byte("launched"), 0o600)
				os.Exit(0)
			}
			time.Sleep(5 * time.Millisecond)
		}
		os.Exit(2)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "launched")
	release := filepath.Join(dir, "release")
	t.Setenv("HIDEOUT_APPOPEN_HELPER", "1")
	ctx, cancel := context.WithCancel(context.Background())
	if err := (ExecLauncher{}).Run(ctx, []string{os.Args[0], "-test.run=^TestExecLauncherHandoffSurvivesBrokerContextCancellation$", "--", marker, release}, nil); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := os.WriteFile(release, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "launched" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("host GUI handoff was killed with the broker request context")
}

func TestExecLauncherRefusesEffectWhenGuardConsumesRequestDeadline(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-launch")
	ctx, cancel := context.WithCancel(context.Background())
	err := (ExecLauncher{}).Run(ctx, []string{"/usr/bin/touch", marker}, func() error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("launcher error=%v want context cancellation", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("host effect crossed the cancelled launch guard: %v", statErr)
	}
}
