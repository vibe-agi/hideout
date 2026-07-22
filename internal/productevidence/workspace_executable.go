package productevidence

import (
	"errors"
	"fmt"
)

const (
	Feature041 = "041-workspace-executable-support"

	Proof041Gate0Mechanics    = "041.workspace-executable.gate0.mechanics"
	Proof041RealExecution     = "041.workspace-executable.real-gate2.execution"
	Proof041RealGate2NotRun   = "041.workspace-executable.real-gate2.not-run"
	Proof041DocsClaimBoundary = "041.workspace-executable.docs.claim-boundary"
)

var requiredWorkspaceExecutableChecks = []string{
	"checkoutWriteVisible",
	"directBinary",
	"directScript",
	"disjointIsolation",
	"escapingSymlinkRejected",
	"incompatibleFormatFailurePreserved",
	"laterSessionVisible",
	"localLauncher",
	"missingInterpreterFailurePreserved",
	"noHostFallback",
	"noWorkspaceCopy",
	"permissionFailurePreserved",
}

type workspaceExecutableEvidence struct {
	Schema                string          `json:"schema"`
	Status                string          `json:"status"`
	Commit                string          `json:"commit"`
	Dirty                 bool            `json:"dirty"`
	Backend               string          `json:"backend"`
	HostOS                string          `json:"hostOS"`
	HostArch              string          `json:"hostArch"`
	GuestArch             string          `json:"guestArch"`
	WorkspaceMechanism    string          `json:"workspaceMechanism"`
	Checks                map[string]bool `json:"checks"`
	Samples               int             `json:"samples"`
	WarmFirstOutputP95MS  float64         `json:"warmFirstOutputP95Ms"`
	MedianRegressionRatio float64         `json:"medianRegressionRatio"`
	NonClaims             struct {
		StaticVirtiofs string `json:"staticVirtiofs"`
	} `json:"nonClaims"`
}

func validateWorkspaceExecutableArtifact(data []byte, expectedCommit string) error {
	var evidence workspaceExecutableEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("workspace executable evidence: %w", err)
	}
	if evidence.Schema != "hideout.workspace-executable-gate2/v1" ||
		evidence.Status != StatusPassed || evidence.Backend != "lima" ||
		evidence.HostOS != "darwin" || evidence.HostArch != "arm64" ||
		evidence.GuestArch != "aarch64" || evidence.WorkspaceMechanism != "workspace-portal" {
		return errors.New("workspace executable evidence identity, platform, or mechanism is invalid")
	}
	if len(evidence.Commit) != 40 || !IsCanonicalCommit(evidence.Commit) ||
		(expectedCommit != "" && evidence.Commit != expectedCommit) || evidence.Dirty {
		return errors.New("workspace executable evidence is not bound to the clean candidate commit")
	}
	if len(evidence.Checks) != len(requiredWorkspaceExecutableChecks) {
		return errors.New("workspace executable evidence check inventory drifted")
	}
	for _, check := range requiredWorkspaceExecutableChecks {
		if !evidence.Checks[check] {
			return fmt.Errorf("workspace executable evidence check %q did not pass", check)
		}
	}
	if evidence.Samples < 30 {
		return errors.New("workspace executable evidence has fewer than 30 samples")
	}
	if evidence.WarmFirstOutputP95MS <= 0 || evidence.WarmFirstOutputP95MS > 2000 {
		return errors.New("workspace executable warm first-output p95 exceeds two seconds")
	}
	if evidence.MedianRegressionRatio <= 0 || evidence.MedianRegressionRatio > 1.10 {
		return errors.New("workspace executable median regression exceeds ten percent")
	}
	if evidence.NonClaims.StaticVirtiofs != "not-claimed" {
		return errors.New("workspace executable evidence overclaims static virtiofs")
	}
	return nil
}
