package hostopen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error
	LookPath(file string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (ExecRunner) Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

type Opener struct {
	BrowserProfileDir string
	BrowserPath       string
	BrowserApp        string
	Runner            Runner
	Stdout            io.Writer
	Stderr            io.Writer
	DryRun            bool
	GOOS              string
}

func (o Opener) OpenURL(ctx context.Context, target string) error {
	if o.BrowserProfileDir == "" {
		return errors.New("browser profile directory is required")
	}
	if err := os.MkdirAll(o.BrowserProfileDir, 0o700); err != nil {
		return err
	}
	name, args, err := o.urlCommand(target)
	if err != nil {
		return err
	}
	if o.DryRun {
		return nil
	}
	return o.runner().Run(ctx, name, args, nil, o.Stdout, o.Stderr)
}

func (o Opener) OpenFile(ctx context.Context, target string) error {
	name, args, err := o.fileCommand(target)
	if err != nil {
		return err
	}
	if o.DryRun {
		return nil
	}
	return o.runner().Run(ctx, name, args, nil, o.Stdout, o.Stderr)
}

func (o Opener) URLCommand(target string) (string, []string, error) {
	return o.urlCommand(target)
}

func (o Opener) FileCommand(target string) (string, []string, error) {
	return o.fileCommand(target)
}

func (o Opener) BrowserProfile() string {
	return o.BrowserProfileDir
}

func (o Opener) urlCommand(target string) (string, []string, error) {
	if o.BrowserPath != "" {
		if isGenericURLOpener(o.BrowserPath) {
			return "", nil, fmt.Errorf("browser path must be a direct isolated browser binary, not generic URL opener %q", filepath.Base(o.BrowserPath))
		}
		return o.BrowserPath, []string{
			"--user-data-dir=" + o.BrowserProfileDir,
			"--no-first-run",
			"--new-window",
			target,
		}, nil
	}
	switch o.goos() {
	case "darwin":
		app := o.BrowserApp
		if app == "" {
			app = "Google Chrome"
		}
		if !isChromiumBrowserApp(app) {
			return "", nil, fmt.Errorf("browser app %q is not a supported isolated browser app; set HIDEOUT_BROWSER_PATH to a direct Chromium-compatible browser binary", app)
		}
		return "/usr/bin/open", []string{
			"-na", app,
			"--args",
			"--user-data-dir=" + o.BrowserProfileDir,
			"--no-first-run",
			"--new-window",
			target,
		}, nil
	default:
		path, err := o.runner().LookPath("chromium")
		if err != nil {
			path, err = o.runner().LookPath("google-chrome")
		}
		if err != nil {
			return "", nil, fmt.Errorf("isolated browser launcher requires HIDEOUT_BROWSER_PATH or chromium/google-chrome on %s", o.goos())
		}
		return path, []string{
			"--user-data-dir=" + o.BrowserProfileDir,
			"--no-first-run",
			"--new-window",
			target,
		}, nil
	}
}

func isChromiumBrowserApp(app string) bool {
	name := strings.TrimSpace(app)
	name = strings.TrimSuffix(name, ".app")
	name = strings.ToLower(name)
	switch name {
	case "google chrome", "google chrome canary", "chromium", "chrome", "microsoft edge", "brave browser", "vivaldi":
		return true
	default:
		return false
	}
}

func isGenericURLOpener(path string) bool {
	if isGenericURLOpenerName(filepath.Base(path)) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return isGenericURLOpenerName(filepath.Base(resolved))
}

func isGenericURLOpenerName(name string) bool {
	switch name {
	case "open", "xdg-open":
		return true
	default:
		return false
	}
}

func (o Opener) fileCommand(target string) (string, []string, error) {
	switch o.goos() {
	case "darwin":
		return "/usr/bin/open", []string{target}, nil
	default:
		path, err := o.runner().LookPath("xdg-open")
		if err != nil {
			return "", nil, fmt.Errorf("xdg-open is required for host file open on %s: %w", o.goos(), err)
		}
		return path, []string{target}, nil
	}
}

func (o Opener) runner() Runner {
	if o.Runner != nil {
		return o.Runner
	}
	return ExecRunner{}
}

func (o Opener) goos() string {
	if o.GOOS != "" {
		return o.GOOS
	}
	return runtime.GOOS
}

func BrowserProfileDir(profileDir string) string {
	return filepath.Join(profileDir, "browser")
}
