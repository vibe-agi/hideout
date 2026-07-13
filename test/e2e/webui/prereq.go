package webui

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type Prerequisites struct {
	Node       string
	ChromePath string
	Missing    []string
}

func DiscoverPrerequisites() Prerequisites {
	var p Prerequisites
	if node, err := exec.LookPath("node"); err == nil {
		p.Node = node
	} else {
		p.Missing = append(p.Missing, "node")
	}
	if chrome := strings.TrimSpace(os.Getenv("HIDEOUT_CHROME_PATH")); chrome != "" {
		if _, err := os.Stat(chrome); err == nil {
			p.ChromePath = chrome
		} else {
			p.Missing = append(p.Missing, "chrome")
		}
	} else if chrome := defaultChromePath(); chrome != "" {
		p.ChromePath = chrome
	} else {
		p.Missing = append(p.Missing, "chrome")
	}
	return p
}

func (p Prerequisites) Available() bool {
	return len(p.Missing) == 0
}

func (p Prerequisites) Err() error {
	if p.Available() {
		return nil
	}
	return errors.New("missing browser prerequisites: " + strings.Join(p.Missing, ", "))
}

func defaultChromePath() string {
	candidates := []string{}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		)
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge"} {
		if path, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, path)
		}
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
