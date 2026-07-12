package tui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type Prerequisites struct {
	ScriptPath string
	GoPath     string
	Reason     string
}

func DiscoverPrerequisites() Prerequisites {
	if runtime.GOOS == "windows" {
		return Prerequisites{Reason: "TUI PTY proof requires a Unix-like script(1) command"}
	}
	scriptPath := strings.TrimSpace(os.Getenv("HIDEOUT_TUI_SCRIPT_PATH"))
	if scriptPath == "" {
		if found, err := exec.LookPath("script"); err == nil {
			scriptPath = found
		}
	}
	if scriptPath == "" {
		return Prerequisites{Reason: "script(1) is required for the TUI PTY proof"}
	}
	goPath := strings.TrimSpace(os.Getenv("HIDEOUT_TUI_GO_PATH"))
	if goPath == "" {
		if found, err := exec.LookPath("go"); err == nil {
			goPath = found
		}
	}
	if goPath == "" {
		return Prerequisites{ScriptPath: scriptPath, Reason: "go is required for the TUI PTY proof"}
	}
	return Prerequisites{ScriptPath: scriptPath, GoPath: goPath}
}

func (p Prerequisites) Available() bool {
	return p.ScriptPath != "" && p.GoPath != "" && p.Reason == ""
}
