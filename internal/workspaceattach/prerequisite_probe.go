package workspaceattach

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

const HostRootPrerequisiteSchema = "hideout.workspace-host-prerequisites/v1"

type HostRootPrincipal struct {
	Process string `json:"process"`
	Role    string `json:"role"`
	State   string `json:"state"`
}

type HostRootPrerequisiteCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type HostRootPrerequisiteReport struct {
	Schema     string                      `json:"schema"`
	Status     string                      `json:"status"`
	TCCStatus  string                      `json:"tccStatus"`
	Scope      string                      `json:"scope"`
	Principals []HostRootPrincipal         `json:"principals"`
	Checks     []HostRootPrerequisiteCheck `json:"checks"`
}

func HostRootPrincipalInventory() []HostRootPrincipal {
	return []HostRootPrincipal{
		{Process: "hideoutd", Role: "production workspace root provider and watcher", State: "required"},
		{Process: "hideout", Role: "transient admission and root-identity verifier; retains no content authority", State: "required"},
		{Process: "hideout-workspace-probe", Role: "explicit doctor and release-gate prerequisite probe", State: "diagnostic-only"},
	}
}

func ProbeHostRootPrerequisite(root string) HostRootPrerequisiteReport {
	report := HostRootPrerequisiteReport{
		Schema: HostRootPrerequisiteSchema, Status: ResearchCheckPassed,
		TCCStatus: "available", Scope: "probed-root-only", Principals: HostRootPrincipalInventory(),
	}
	appendCheck := func(id string, err error) bool {
		status, reason := classifyHostRootPrerequisite(err)
		report.Checks = append(report.Checks, HostRootPrerequisiteCheck{ID: id, Status: status, Reason: reason})
		if status != ResearchCheckPassed {
			report.Status = ResearchCheckFailed
			report.TCCStatus = status
			return false
		}
		return true
	}

	canonical, err := filepath.EvalSymlinks(root)
	if !appendCheck("canonical-root", err) {
		return report
	}
	canonical, err = filepath.Abs(canonical)
	if !appendCheck("absolute-root", err) {
		return report
	}
	rooted, err := os.OpenRoot(canonical)
	if !appendCheck("root-descriptor", err) {
		return report
	}
	defer rooted.Close()
	directory, err := rooted.Open(".")
	if !appendCheck("root-open", err) {
		return report
	}
	_, readErr := directory.ReadDir(1)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	closeErr := directory.Close()
	if readErr == nil {
		readErr = closeErr
	}
	if !appendCheck("root-enumeration", readErr) {
		return report
	}
	watcher, err := fsnotify.NewWatcher()
	if !appendCheck("watcher-create", err) {
		return report
	}
	defer watcher.Close()
	appendCheck("watcher-enrollment", watcher.Add(canonical))
	return report
}

func classifyHostRootPrerequisite(err error) (string, string) {
	if err == nil {
		return ResearchCheckPassed, ""
	}
	if errors.Is(err, os.ErrPermission) {
		return "denied", "host permission denied"
	}
	return "unknown", "host prerequisite could not be proved"
}
