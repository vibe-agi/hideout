package workspaceattach

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

const AttachmentSummarySchema = "hideout.workspace-attachment/v1"

type AttachmentSummary struct {
	Schema            string                   `json:"schema"`
	AttachmentID      string                   `json:"attachmentId"`
	SessionID         string                   `json:"sessionId"`
	EnvironmentID     string                   `json:"environmentId"`
	Incarnation       lifecycle.EnvironmentRef `json:"incarnation"`
	WorkspaceID       string                   `json:"workspaceId"`
	DisplayLabel      string                   `json:"displayLabel"`
	LogicalGuestRoot  string                   `json:"logicalGuestRoot"`
	PhysicalGuestRoot string                   `json:"physicalGuestRoot"`
	Transport         string                   `json:"transport"`
	ProviderRef       lifecycle.ResourceRef    `json:"providerRef"`
	GuestViewRef      lifecycle.ResourceRef    `json:"guestViewRef"`
	State             AttachmentState          `json:"state"`
	CreatedAt         time.Time                `json:"createdAt"`
	CleanupProof      *CleanupProof            `json:"cleanupProof,omitempty"`
}

func (attachment Attachment) Summary() AttachmentSummary {
	return AttachmentSummary{
		Schema: AttachmentSummarySchema, AttachmentID: attachment.ID, SessionID: attachment.SessionID,
		EnvironmentID: attachment.EnvironmentID, Incarnation: attachment.Incarnation,
		WorkspaceID: attachment.WorkspaceID, DisplayLabel: attachmentDisplayLabel(attachment.CanonicalHostRoot, attachment.WorkspaceID),
		LogicalGuestRoot: attachment.LogicalGuestRoot, PhysicalGuestRoot: attachment.PhysicalGuestRoot,
		Transport: attachment.Transport, ProviderRef: attachment.ProviderRef, GuestViewRef: attachment.GuestViewRef,
		State: attachment.State, CreatedAt: attachment.CreatedAt, CleanupProof: attachment.CleanupProof,
	}
}

func attachmentDisplayLabel(hostRoot, workspaceID string) string {
	name := filepath.Base(filepath.Clean(hostRoot))
	if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
		name = "workspace"
	}
	shortID := strings.TrimPrefix(workspaceID, "wrk_")
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return fmt.Sprintf("%s [%s]", name, shortID)
}
