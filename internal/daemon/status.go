package daemon

import (
	"encoding/hex"
	"strings"

	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const (
	statusVersion      = "hideout.daemon-status/v1"
	stopReceiptVersion = "hideout.daemon-stop-receipt/v1"
)

// StopReceipt identifies the exact daemon instance whose ordered shutdown was
// accepted. Clients use it to prove that instance has relinquished ownership
// before reporting a completed stop.
type StopReceipt struct {
	Version    string `json:"version"`
	InstanceID string `json:"instanceId"`
	Status     string `json:"status"`
}

// Status is the daemon status/inventory shape (schemas/daemon-status.schema.json).
type Status struct {
	Version string `json:"version"`
	BuildID string `json:"buildId"`
	// LimaHome is the lima world this daemon resolved at startup and uses for
	// every backend inventory observation and control command. Clients refuse
	// a daemon whose lima home differs from their own resolution: such a
	// daemon would observe and mutate the wrong machine inventory.
	LimaHome             string                              `json:"limaHome,omitempty"`
	State                string                              `json:"state"`
	InstanceID           string                              `json:"instanceId,omitempty"`
	StartedAt            string                              `json:"startedAt,omitempty"`
	CredentialGeneration uint64                              `json:"credentialGeneration,omitempty"`
	Transport            StatusTransport                     `json:"transport"`
	Sessions             []SessionStatus                     `json:"sessions,omitempty"`
	WorkspaceAttachments []workspaceattach.AttachmentSummary `json:"workspaceAttachments,omitempty"`
	Background           []BackgroundStatus                  `json:"background,omitempty"`
	Lifecycle            []lifecycle.Status                  `json:"lifecycle,omitempty"`
}

type StatusTransport struct {
	Socket          string `json:"socket"`
	SessionSocket   string `json:"sessionSocket,omitempty"`
	SessionProtocol string `json:"sessionProtocol,omitempty"`
}

type SessionStatus struct {
	Schema            string `json:"schema"`
	ID                string `json:"id"`
	EnvironmentID     string `json:"environmentId"`
	Profile           string `json:"profile"`
	Backend           string `json:"backend"`
	WorkspaceID       string `json:"workspaceId,omitempty"`
	SessionSnapshotID string `json:"sessionSnapshotId"`
	State             string `json:"state"`
	OwnerStatus       string `json:"ownerStatus"`
	TerminalMode      string `json:"terminalMode"`
	StartedAt         string `json:"startedAt"`
	CommandClass      string `json:"commandClass,omitempty"`
}

// BackgroundStatus reports a background operation (populated in the US3 slice).
type BackgroundStatus struct {
	ID     string `json:"id"`
	Op     string `json:"op"`
	Status string `json:"status"`
}

func validBuildID(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
