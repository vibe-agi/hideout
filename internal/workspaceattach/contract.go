package workspaceattach

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

var (
	ErrProviderOverloaded = errors.New("workspace provider admission limit reached")
	ErrProviderUnproved   = errors.New("workspace provider state is unproved")
)

type ProviderSpec struct {
	ProviderID         string
	EnvironmentID      string
	Incarnation        lifecycle.EnvironmentRef
	WorkspaceID        string
	CanonicalHostRoot  string
	RootFileIdentity   RootFileIdentity
	RootHandleIdentity string
	Implementation     string
	Limits             LimitSet
}

func ProviderSpecFromAttachment(attachment Attachment, limits LimitSet) (ProviderSpec, error) {
	if err := attachment.Validate(); err != nil {
		return ProviderSpec{}, err
	}
	spec := ProviderSpec{
		ProviderID: attachment.ProviderRef.ID, EnvironmentID: attachment.EnvironmentID,
		Incarnation: attachment.Incarnation, WorkspaceID: attachment.WorkspaceID,
		CanonicalHostRoot: attachment.CanonicalHostRoot, RootFileIdentity: attachment.RootFileIdentity,
		RootHandleIdentity: attachment.RootHandleIdentity, Implementation: attachment.Transport, Limits: limits,
	}
	if err := spec.Validate(); err != nil {
		return ProviderSpec{}, err
	}
	return spec, nil
}

func (spec ProviderSpec) Validate() error {
	if !validBoundedID(spec.ProviderID, "provider-") && !validBoundedID(spec.ProviderID, "wsp_") {
		return errors.New("workspace provider id is invalid")
	}
	if !strings.HasPrefix(spec.EnvironmentID, "env_") || !validWorkspaceID(spec.WorkspaceID) ||
		spec.Implementation != SelectedTransport || !filepath.IsAbs(spec.CanonicalHostRoot) ||
		filepath.Clean(spec.CanonicalHostRoot) != spec.CanonicalHostRoot || strings.TrimSpace(spec.RootHandleIdentity) == "" {
		return errors.New("workspace provider authority is incomplete")
	}
	if err := spec.Incarnation.Validate(spec.Incarnation.BootID != ""); err != nil || spec.Incarnation.EnvironmentID != spec.EnvironmentID {
		return errors.New("workspace provider incarnation is invalid")
	}
	if err := spec.RootFileIdentity.Validate(); err != nil {
		return err
	}
	return spec.Limits.Validate()
}

type ViewSpec struct {
	Attachment         Attachment
	CredentialAudience string
}

func (spec ViewSpec) Validate(provider ProviderSpec) error {
	if err := provider.Validate(); err != nil {
		return err
	}
	if err := spec.Attachment.Validate(); err != nil {
		return err
	}
	if spec.Attachment.ProviderRef.ID != provider.ProviderID ||
		spec.Attachment.EnvironmentID != provider.EnvironmentID ||
		spec.Attachment.Incarnation != provider.Incarnation ||
		spec.Attachment.WorkspaceID != provider.WorkspaceID ||
		spec.Attachment.RootFileIdentity != provider.RootFileIdentity ||
		spec.Attachment.RootHandleIdentity != provider.RootHandleIdentity ||
		spec.Attachment.CanonicalHostRoot != provider.CanonicalHostRoot {
		return errors.New("workspace view does not match provider authority")
	}
	if strings.TrimSpace(spec.CredentialAudience) == "" || len(spec.CredentialAudience) > 128 {
		return errors.New("workspace view credential audience is invalid")
	}
	return nil
}

type ObservationState string

const (
	ObservationReady    ObservationState = "ready"
	ObservationAbsent   ObservationState = "absent"
	ObservationUnproved ObservationState = "unproved"
)

type Observation struct {
	State      ObservationState
	ObservedAt time.Time
	ReasonCode string
}

func (observation Observation) Validate() error {
	if observation.ObservedAt.IsZero() {
		return errors.New("workspace provider observation time is required")
	}
	switch observation.State {
	case ObservationReady, ObservationAbsent:
		if observation.ReasonCode != "" {
			return errors.New("proved workspace provider observation cannot carry a reason code")
		}
	case ObservationUnproved:
		if strings.TrimSpace(observation.ReasonCode) == "" || len(observation.ReasonCode) > 128 {
			return errors.New("unproved workspace provider observation requires a bounded reason code")
		}
	default:
		return errors.New("workspace provider observation state is invalid")
	}
	return nil
}

type ProviderFactory interface {
	Start(context.Context, ProviderSpec) (Provider, error)
	Observe(context.Context, lifecycle.ResourceRef) (Observation, error)
}

type Provider interface {
	Spec() ProviderSpec
	Endpoint() string
	BindIncarnation(context.Context, lifecycle.EnvironmentRef) error
	Attach(context.Context, ViewSpec) (GuestView, error)
	Observe(context.Context) (Observation, error)
	Release(context.Context) (Observation, error)
}

type GuestView interface {
	Attachment() Attachment
	Observe(context.Context) (Observation, error)
	Revoke(context.Context) error
	Flush(context.Context) error
	Release(context.Context) (Observation, error)
}
