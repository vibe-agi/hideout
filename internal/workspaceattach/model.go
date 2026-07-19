package workspaceattach

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/session"
)

const SelectedTransport = CandidatePortal

type AttachmentState string

const (
	AttachmentPlanned          AttachmentState = "planned"
	AttachmentProviderStarting AttachmentState = "provider-starting"
	AttachmentProviderReady    AttachmentState = "provider-ready"
	AttachmentViewMounting     AttachmentState = "view-mounting"
	AttachmentReady            AttachmentState = "ready"
	AttachmentDraining         AttachmentState = "draining"
	AttachmentReleased         AttachmentState = "released"
	AttachmentUnproved         AttachmentState = "unproved"
)

type CleanupProof struct {
	Status     string    `json:"status"`
	ObservedAt time.Time `json:"observedAt"`
	ReasonCode string    `json:"reasonCode,omitempty"`
}

const (
	CleanupAbsent   = "absent"
	CleanupUnproved = "unproved"
)

func (proof CleanupProof) Validate() error {
	if proof.ObservedAt.IsZero() {
		return errors.New("workspace cleanup observation time is required")
	}
	switch proof.Status {
	case CleanupAbsent:
		if proof.ReasonCode != "" {
			return errors.New("proved-absent workspace cleanup cannot carry a reason code")
		}
	case CleanupUnproved:
		if strings.TrimSpace(proof.ReasonCode) == "" || len(proof.ReasonCode) > 128 {
			return errors.New("unproved workspace cleanup requires a bounded reason code")
		}
	default:
		return errors.New("workspace cleanup status is invalid")
	}
	return nil
}

type Attachment struct {
	ID                 string                   `json:"attachmentId"`
	SessionID          string                   `json:"sessionId"`
	EnvironmentID      string                   `json:"environmentId"`
	Incarnation        lifecycle.EnvironmentRef `json:"incarnation"`
	WorkspaceID        string                   `json:"workspaceId"`
	CanonicalHostRoot  string                   `json:"canonicalHostRoot"`
	RootFileIdentity   RootFileIdentity         `json:"rootFileIdentity"`
	RootHandleIdentity string                   `json:"rootHandleIdentity"`
	LogicalGuestRoot   string                   `json:"logicalGuestRoot"`
	PhysicalGuestRoot  string                   `json:"physicalGuestRoot"`
	Transport          string                   `json:"transport"`
	ProviderRef        lifecycle.ResourceRef    `json:"providerRef"`
	GuestViewRef       lifecycle.ResourceRef    `json:"guestViewRef"`
	State              AttachmentState          `json:"state"`
	CreatedAt          time.Time                `json:"createdAt"`
	CleanupProof       *CleanupProof            `json:"cleanupProof,omitempty"`
}

func NewAttachmentID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "att_" + hex.EncodeToString(value[:]), nil
}

func (attachment Attachment) Validate() error {
	if !validBoundedID(attachment.ID, "att_") || !session.ValidID(attachment.SessionID) ||
		!strings.HasPrefix(attachment.EnvironmentID, "env_") || !validWorkspaceID(attachment.WorkspaceID) || attachment.CreatedAt.IsZero() {
		return errors.New("workspace attachment identity is invalid")
	}
	if err := attachment.Incarnation.Validate(attachment.Incarnation.BootID != ""); err != nil || attachment.Incarnation.EnvironmentID != attachment.EnvironmentID {
		return errors.New("workspace attachment incarnation is invalid")
	}
	if !filepath.IsAbs(attachment.CanonicalHostRoot) || filepath.Clean(attachment.CanonicalHostRoot) != attachment.CanonicalHostRoot ||
		attachment.LogicalGuestRoot != LogicalWorkspaceRoot || attachment.PhysicalGuestRoot != PhysicalWorkspaceBase+"/"+attachment.WorkspaceID ||
		attachment.Transport != SelectedTransport || strings.TrimSpace(attachment.RootHandleIdentity) == "" {
		return errors.New("workspace attachment root or transport is invalid")
	}
	if err := attachment.RootFileIdentity.Validate(); err != nil {
		return err
	}
	if err := attachment.ProviderRef.Validate(); err != nil {
		return err
	}
	if attachment.ProviderRef.Kind != lifecycle.KindWorkspaceHostProvider {
		return errors.New("workspace attachment provider reference kind is invalid")
	}
	if err := attachment.GuestViewRef.Validate(); err != nil {
		return err
	}
	if attachment.GuestViewRef.Kind != lifecycle.KindWorkspaceGuestView {
		return errors.New("workspace attachment guest-view reference kind is invalid")
	}
	if !slices.Contains([]AttachmentState{
		AttachmentPlanned, AttachmentProviderStarting, AttachmentProviderReady, AttachmentViewMounting,
		AttachmentReady, AttachmentDraining, AttachmentReleased, AttachmentUnproved,
	}, attachment.State) {
		return errors.New("workspace attachment state is invalid")
	}
	if (attachment.State == AttachmentReleased || attachment.State == AttachmentUnproved) && attachment.CleanupProof == nil {
		return errors.New("terminal workspace attachment requires cleanup proof")
	}
	if attachment.CleanupProof != nil {
		if err := attachment.CleanupProof.Validate(); err != nil {
			return err
		}
		if attachment.State == AttachmentReleased && attachment.CleanupProof.Status != CleanupAbsent {
			return errors.New("released workspace attachment requires proved-absent cleanup")
		}
		if attachment.State == AttachmentUnproved && attachment.CleanupProof.Status != CleanupUnproved {
			return errors.New("unproved workspace attachment requires unproved cleanup")
		}
	}
	return nil
}

func validBoundedID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) || len(value) > 128 {
		return false
	}
	for _, r := range strings.TrimPrefix(value, prefix) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
