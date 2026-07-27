// Package appopen is a generic host-application launcher: it renders host argv
// from a declarative LaunchSpec and a validated OpenRequest, and runs it. It is
// intentionally application-agnostic — there is no editor- or vendor-specific
// vocabulary here. Each supported application is DATA (a LaunchSpec provided by
// the Core app registry), not code.
package appopen

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// Mode is the launch posture.
type Mode string

const (
	ModeSafe    Mode = "safe"
	ModeTrusted Mode = "trusted-host-app"
)

// hardForbiddenFlags are refused regardless of any app recipe. These are
// framework security invariants, not application preferences, so they live in
// Go and cannot be re-enabled by editing recipe data. Disabling workspace trust
// would remove the safe-mode protection layer.
var hardForbiddenFlags = []string{"--disable-workspace-trust"}

// LaunchSpec is the declarative, data-driven description of how to render argv
// for one application family. It is supplied as recipe data (see the Core app
// registry), never hardcoded in the provider.
type LaunchSpec struct {
	// SafeIsolationFlags are appended in safe mode (e.g. an extensions-disable
	// flag).
	SafeIsolationFlags []string `json:"safeIsolationFlags,omitempty"`
	// IsolatedDataDirFlag, when set, is emitted in safe mode with the isolated
	// data directory as its value (e.g. a per-Hideout user data dir).
	IsolatedDataDirFlag string `json:"isolatedDataDirFlag,omitempty"`
	NewWindowFlag       string `json:"newWindowFlag,omitempty"`
	ReuseWindowFlag     string `json:"reuseWindowFlag,omitempty"`
	// GotoFlag + GotoSeparator render a location, e.g. "--goto" and ":" produce
	// "--goto path:line:col".
	GotoFlag      string `json:"gotoFlag,omitempty"`
	GotoSeparator string `json:"gotoSeparator,omitempty"`
	// ForbiddenFlags are additional flags this app must never receive.
	ForbiddenFlags []string `json:"forbiddenFlags,omitempty"`
	// SafeConfiguration is package-owned recipe data written beneath the
	// isolated user-data directory before each safe launch.
	SafeConfiguration *SafeConfigurationSpec `json:"safeConfiguration,omitempty"`
}

type SafeConfigurationSpec struct {
	RelativePath string         `json:"relativePath"`
	Values       map[string]any `json:"values"`
}

// OpenRequest is a fully-validated, host-resolved launch request. HostTarget is
// a real host path already confirmed inside the workspace by Core.
type OpenRequest struct {
	BinaryPath      string
	Mode            Mode
	HostTarget      string
	Line            int
	Column          int
	NewWindow       bool
	SafeUserDataDir string
	// QualifiedAppRef and RunID are required by the 032 Core safety-profile
	// path. The legacy 030 renderer leaves them empty until its caller migrates
	// to BuildSafeEffect.
	QualifiedAppRef string
	RunID           string
}

// RenderArgv builds host argv generically from a LaunchSpec and request. It has
// no knowledge of any specific application. It enforces the framework's hard
// forbidden-flag floor plus the recipe's own forbidden flags.
func RenderArgv(spec LaunchSpec, req OpenRequest) ([]string, error) {
	if strings.TrimSpace(req.BinaryPath) == "" {
		return nil, errors.New("appopen: binary path is required")
	}
	if strings.TrimSpace(req.HostTarget) == "" {
		return nil, errors.New("appopen: host target is required")
	}
	if req.Line < 0 || req.Column < 0 {
		return nil, errors.New("appopen: negative location")
	}

	argv := []string{req.BinaryPath}

	switch req.Mode {
	case ModeSafe:
		if spec.IsolatedDataDirFlag != "" {
			if strings.TrimSpace(req.SafeUserDataDir) == "" {
				return nil, errors.New("appopen: safe mode requires an isolated data directory")
			}
			argv = append(argv, spec.IsolatedDataDirFlag, req.SafeUserDataDir)
		}
		argv = append(argv, spec.SafeIsolationFlags...)
	case ModeTrusted:
		// operator's normal configuration; no isolation flags.
	default:
		return nil, errors.New("appopen: unknown mode")
	}

	if req.NewWindow {
		if spec.NewWindowFlag != "" {
			argv = append(argv, spec.NewWindowFlag)
		}
	} else if spec.ReuseWindowFlag != "" {
		argv = append(argv, spec.ReuseWindowFlag)
	}

	if req.Line > 0 && spec.GotoFlag != "" {
		sep := spec.GotoSeparator
		if sep == "" {
			sep = ":"
		}
		loc := req.HostTarget + sep + strconv.Itoa(req.Line)
		if req.Column > 0 {
			loc += sep + strconv.Itoa(req.Column)
		}
		argv = append(argv, spec.GotoFlag, loc)
	} else {
		argv = append(argv, req.HostTarget)
	}

	forbidden := append(append([]string(nil), hardForbiddenFlags...), spec.ForbiddenFlags...)
	for _, f := range forbidden {
		if slices.Contains(argv, f) {
			return nil, errors.New("appopen: refusing to emit forbidden flag " + f)
		}
	}
	return argv, nil
}

// LaunchGuard performs the final resource/application identity check at the
// host side-effect boundary. It must not mutate state or rebuild argv.
type LaunchGuard func() error

// Launcher runs a built argv only after the final guard succeeds. Indirected
// for tests so replacement races can be injected immediately before guard.
type Launcher interface {
	Run(ctx context.Context, argv []string, guard LaunchGuard) error
}

// ExecLauncher runs the host command for real.
type ExecLauncher struct{}

func (ExecLauncher) Run(ctx context.Context, argv []string, guard LaunchGuard) error {
	if len(argv) == 0 {
		return errors.New("appopen: empty argv")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if guard != nil {
		if err := guard(); err != nil {
			return err
		}
	}
	// The guard may perform bounded host identity/trust work. Re-check the
	// request after it returns so a timed-out broker request can never launch a
	// delayed host effect.
	if err := ctx.Err(); err != nil {
		return err
	}
	// Launch is a handoff to a host GUI process. The broker request context must
	// not kill the application after a successful response, but the child still
	// needs to be reaped when it exits.
	// No mutable file write, path resolution, or argv rebuild may occur between
	// the guard and this exec construction. macOS does not expose an fd-based
	// GUI bundle launch contract, so a sub-kernel-scheduling race remains an
	// explicit non-claim; repeated Core identity/path checks bound that window.
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
