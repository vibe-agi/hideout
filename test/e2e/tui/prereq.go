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
	ShellPath  string
	SttyPath   string
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
	shellPath := strings.TrimSpace(os.Getenv("HIDEOUT_TUI_SHELL_PATH"))
	if shellPath == "" {
		if found, err := exec.LookPath("sh"); err == nil {
			shellPath = found
		}
	}
	if shellPath == "" {
		return Prerequisites{ScriptPath: scriptPath, GoPath: goPath, Reason: "sh is required for the TUI PTY proof"}
	}
	sttyPath := strings.TrimSpace(os.Getenv("HIDEOUT_TUI_STTY_PATH"))
	if sttyPath == "" {
		if found, err := exec.LookPath("stty"); err == nil {
			sttyPath = found
		}
	}
	if sttyPath == "" {
		return Prerequisites{
			ScriptPath: scriptPath,
			GoPath:     goPath,
			ShellPath:  shellPath,
			Reason:     "stty is required for the TUI PTY proof",
		}
	}
	return Prerequisites{
		ScriptPath: scriptPath,
		GoPath:     goPath,
		ShellPath:  shellPath,
		SttyPath:   sttyPath,
	}
}

func (p Prerequisites) Available() bool {
	return p.ScriptPath != "" && p.GoPath != "" && p.ShellPath != "" &&
		p.SttyPath != "" && p.Reason == ""
}
