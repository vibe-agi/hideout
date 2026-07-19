package workspaceattach

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	LogicalWorkspaceRoot     = "/workspace"
	PhysicalWorkspaceBase    = "/hideout/workspaces"
	PathIdentityInputSchema  = "hideout.workspace-path-identity-input/v1"
	PathIdentityReportSchema = "hideout.workspace-path-identity/v1"
)

var researchPathTools = []string{"bash", "git", "node", "python", "go", "claude", "codex"}

type PathIdentityProbeObservation struct {
	Tool                  string `json:"tool"`
	Version               string `json:"version"`
	LogicalPWD            string `json:"logicalPWD"`
	PhysicalCWD           string `json:"physicalCWD"`
	ProjectKey            string `json:"projectKey"`
	RepresentativeFixture bool   `json:"representativeFixture"`
	AfterCdDot            string `json:"afterCdDot"`
	AfterCdLogical        string `json:"afterCdLogical"`
	SubprocessCWD         string `json:"subprocessCWD"`
	ShellReentryCWD       string `json:"shellReentryCWD"`
}

type PathIdentityProbeInput struct {
	Schema             string                         `json:"schema"`
	WorkspaceID        string                         `json:"workspaceId"`
	GitSafeDirectories []string                       `json:"gitSafeDirectories"`
	UnboundGitRejected bool                           `json:"unboundGitRejected"`
	Observations       []PathIdentityProbeObservation `json:"observations"`
}

type PathIdentityProbeReport struct {
	Schema             string                         `json:"schema"`
	Result             string                         `json:"result"`
	WorkspaceID        string                         `json:"workspaceId"`
	LogicalRoot        string                         `json:"logicalRoot"`
	PhysicalRoot       string                         `json:"physicalRoot"`
	Mechanism          string                         `json:"mechanism"`
	GitSafeDirectories []string                       `json:"gitSafeDirectories"`
	UnboundGitRejected bool                           `json:"unboundGitRejected"`
	Observations       []PathIdentityProbeObservation `json:"observations"`
}

func ResearchPhysicalWorkspaceRoot(workspaceID string) (string, error) {
	if !validResearchWorkspaceID(workspaceID) {
		return "", errors.New("research workspace id is invalid")
	}
	return filepath.ToSlash(filepath.Join(PhysicalWorkspaceBase, workspaceID)), nil
}

func ValidatePathIdentityProbe(workspaceID string, observations []PathIdentityProbeObservation) error {
	physicalRoot, err := ResearchPhysicalWorkspaceRoot(workspaceID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, observation := range observations {
		if !containsResearchPathTool(observation.Tool) || seen[observation.Tool] {
			return fmt.Errorf("invalid or duplicate path identity tool %q", observation.Tool)
		}
		seen[observation.Tool] = true
		if strings.TrimSpace(observation.Version) == "" {
			return fmt.Errorf("path identity tool %s has no version", observation.Tool)
		}
		fixtureExpected := observation.Tool == "claude" || observation.Tool == "codex"
		if observation.RepresentativeFixture != fixtureExpected {
			return fmt.Errorf("path identity tool %s has an incorrect fixture classification", observation.Tool)
		}
		if observation.LogicalPWD != LogicalWorkspaceRoot {
			return fmt.Errorf("path identity tool %s lost logical /workspace", observation.Tool)
		}
		if observation.PhysicalCWD != physicalRoot || observation.ProjectKey != physicalRoot {
			return fmt.Errorf("path identity tool %s did not retain the opaque physical project identity", observation.Tool)
		}
		for name, value := range map[string]string{
			"physical cwd": observation.PhysicalCWD, "project key": observation.ProjectKey,
			"cd dot": observation.AfterCdDot, "cd logical": observation.AfterCdLogical,
			"subprocess cwd": observation.SubprocessCWD, "shell re-entry cwd": observation.ShellReentryCWD,
		} {
			if value != physicalRoot {
				return fmt.Errorf("path identity tool %s has incorrect %s %q", observation.Tool, name, value)
			}
		}
		encoded, err := json.Marshal(observation)
		if err != nil {
			return err
		}
		if bytes.Contains(encoded, []byte("/Users/")) {
			return fmt.Errorf("path identity tool %s exposed host topology", observation.Tool)
		}
	}
	for _, tool := range researchPathTools {
		if !seen[tool] {
			return fmt.Errorf("path identity probe is missing %s", tool)
		}
	}
	return nil
}

func EvaluatePathIdentityProbe(input PathIdentityProbeInput, mechanism string) (PathIdentityProbeReport, error) {
	if input.Schema != PathIdentityInputSchema {
		return PathIdentityProbeReport{}, fmt.Errorf("unexpected path identity input schema %q", input.Schema)
	}
	if strings.TrimSpace(mechanism) == "" {
		return PathIdentityProbeReport{}, errors.New("path identity mechanism is required")
	}
	if !input.UnboundGitRejected {
		return PathIdentityProbeReport{}, errors.New("unbound physical Git path was not rejected")
	}
	physicalRoot, err := ResearchPhysicalWorkspaceRoot(input.WorkspaceID)
	if err != nil {
		return PathIdentityProbeReport{}, err
	}
	seenSafeDirectory := map[string]bool{}
	for _, safeDirectory := range input.GitSafeDirectories {
		if safeDirectory != LogicalWorkspaceRoot && safeDirectory != physicalRoot {
			return PathIdentityProbeReport{}, fmt.Errorf("Git safe.directory contains unbound path %q", safeDirectory)
		}
		if seenSafeDirectory[safeDirectory] {
			return PathIdentityProbeReport{}, fmt.Errorf("Git safe.directory duplicates %q", safeDirectory)
		}
		seenSafeDirectory[safeDirectory] = true
	}
	if err := ValidatePathIdentityProbe(input.WorkspaceID, input.Observations); err != nil {
		return PathIdentityProbeReport{}, err
	}
	return PathIdentityProbeReport{
		Schema: PathIdentityReportSchema, Result: ResearchCheckPassed,
		WorkspaceID: input.WorkspaceID, LogicalRoot: LogicalWorkspaceRoot,
		PhysicalRoot: physicalRoot, Mechanism: mechanism, GitSafeDirectories: input.GitSafeDirectories,
		UnboundGitRejected: input.UnboundGitRejected, Observations: input.Observations,
	}, nil
}

func LoadPathIdentityProbeInput(path string) (PathIdentityProbeInput, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return PathIdentityProbeInput{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return PathIdentityProbeInput{}, err
	}
	if len(data) > 1<<20 {
		return PathIdentityProbeInput{}, errors.New("path identity input exceeds 1 MiB")
	}
	var input PathIdentityProbeInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return PathIdentityProbeInput{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return PathIdentityProbeInput{}, errors.New("path identity input contains multiple JSON values")
		}
		return PathIdentityProbeInput{}, fmt.Errorf("path identity input trailing data: %w", err)
	}
	return input, nil
}

func validResearchWorkspaceID(value string) bool {
	if len(value) < 8 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return value != "." && value != ".."
}

func containsResearchPathTool(value string) bool {
	for _, tool := range researchPathTools {
		if value == tool {
			return true
		}
	}
	return false
}
