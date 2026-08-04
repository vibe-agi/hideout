//go:build linux

package main

import (
	"errors"
	"strings"

	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
	processcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/process"
	"github.com/vibe-agi/hideout/internal/workspacepath"
)

var errObserverWorkspacePath = errors.New("observer workspace path does not match the session attachment")

const unboundObservedWorkspaceArgument = "[UNBOUND_WORKSPACE_PATH]"

func normalizeObservedWorkspacePath(
	value string,
	binding *workspacepath.Binding,
) (string, bool, error) {
	if binding == nil || value == "" {
		return value, false, nil
	}
	resolved, err := binding.Resolve(value)
	if err == nil {
		return resolved.LogicalPath, true, nil
	}
	if value == binding.LogicalRoot ||
		strings.HasPrefix(value, binding.LogicalRoot+"/") ||
		value == workspacepath.PhysicalBase ||
		strings.HasPrefix(value, workspacepath.PhysicalBase+"/") {
		return "", false, errObserverWorkspacePath
	}
	return value, false, nil
}

func normalizeObservedWorkspaceFileEvent(
	event filecollector.Event,
	binding *workspacepath.Binding,
) (filecollector.Event, error) {
	pathValue, workspace, err := normalizeObservedWorkspacePath(event.Path, binding)
	if err != nil {
		return filecollector.Event{}, err
	}
	targetValue, _, err := normalizeObservedWorkspacePath(event.TargetPath, binding)
	if err != nil {
		return filecollector.Event{}, err
	}
	event.Path = pathValue
	event.TargetPath = targetValue
	if workspace {
		event.PathClass = "workspace"
	}
	return event, nil
}

func normalizeObservedWorkspaceProcessEvent(
	event processcollector.Event,
	binding *workspacepath.Binding,
) (processcollector.Event, error) {
	var err error
	event.Cwd, _, err = normalizeObservedWorkspacePath(event.Cwd, binding)
	if err != nil {
		return processcollector.Event{}, err
	}
	event.Executable, _, err = normalizeObservedWorkspacePath(event.Executable, binding)
	if err != nil {
		return processcollector.Event{}, err
	}
	for index, argument := range event.Argv {
		normalized, mapped, pathErr := normalizeObservedWorkspacePath(argument, binding)
		if pathErr != nil {
			event.Argv[index] = unboundObservedWorkspaceArgument
			continue
		}
		if mapped {
			event.Argv[index] = normalized
		}
	}
	return event, nil
}
