package workspaceattach

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

func TestSelectedLimitsMatchAcceptedResearchAndBoundDerivedCapacity(t *testing.T) {
	limits := SelectedLimits()
	if err := limits.Validate(); err != nil {
		t.Fatal(err)
	}
	if limits.ViewsPerEnvironment != 16 || limits.ViewsPerSession != 1 ||
		limits.HandlesPerSession != 4096 || limits.InFlightPerSession != 256 ||
		limits.QueuedBytesPerSession != 8<<20 || limits.FrameBytes != 1<<20 ||
		limits.DirectoryEntries != 65536 {
		t.Fatalf("selected measured limits drifted: %#v", limits)
	}
	if limits.HandlesPerProvider != limits.ViewsPerEnvironment*limits.HandlesPerSession ||
		limits.InFlightGlobal != limits.ViewsPerEnvironment*limits.InFlightPerSession ||
		limits.QueuedBytesGlobal != int64(limits.ViewsPerEnvironment)*limits.QueuedBytesPerSession {
		t.Fatalf("derived global bounds are not tied to admitted views: %#v", limits)
	}
	probe := DefaultPortalLimits()
	if probe.HandlesPerSession != limits.HandlesPerSession || probe.InFlightPerSession != limits.InFlightPerSession ||
		probe.QueuedBytesPerSession != limits.QueuedBytesPerSession || probe.FrameBytes != limits.FrameBytes ||
		probe.DirectoryEntries != limits.DirectoryEntries {
		t.Fatalf("research probe and product contract drifted: %#v vs %#v", probe, limits)
	}
}

func TestAdmissionRequestKeepsTeardownCapacityNarrowAndIndependent(t *testing.T) {
	limits := SelectedLimits()
	ordinary := AdmissionRequest{
		EnvironmentID: "env_fixture", ProviderID: "provider-fixture", SessionID: "ses_fixture", Class: AdmissionOrdinary,
		InFlight: limits.InFlightPerSession + 1,
	}
	if err := ordinary.Validate(limits); !errors.Is(err, ErrProviderOverloaded) {
		t.Fatalf("ordinary saturation error = %v", err)
	}
	teardown := AdmissionRequest{
		EnvironmentID: "env_fixture", ProviderID: "provider-fixture", SessionID: "ses_fixture", Class: AdmissionTeardown,
		InFlight: limits.TeardownInFlightPerSession,
	}
	if err := teardown.Validate(limits); err != nil {
		t.Fatalf("reserved teardown request rejected: %v", err)
	}
	teardown.Handles = 1
	if err := teardown.Validate(limits); !errors.Is(err, ErrProviderOverloaded) {
		t.Fatalf("teardown request widened into ordinary capacity: %v", err)
	}
}

func TestProviderAndViewContractsSeparateSharedRootFromSessionBinding(t *testing.T) {
	root := t.TempDir()
	canonical, identity, err := CaptureRootIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, err := DeriveWorkspaceID(bytes.Repeat([]byte{0x42}, 32), canonical, identity)
	if err != nil {
		t.Fatal(err)
	}
	attachment := Attachment{
		ID: "att_0123456789abcdef", SessionID: "ses_fixture", EnvironmentID: "env_fixture",
		Incarnation: lifecycle.EnvironmentRef{
			EnvironmentID: "env_fixture", StartGeneration: 1, InstanceName: "hideout-fixture",
			BootID: "01234567-89ab-cdef-0123-456789abcdef",
		},
		WorkspaceID: workspaceID, CanonicalHostRoot: canonical, RootFileIdentity: identity,
		RootHandleIdentity: "root-handle-fixture", LogicalGuestRoot: LogicalWorkspaceRoot,
		PhysicalGuestRoot: PhysicalWorkspaceBase + "/" + workspaceID, Transport: SelectedTransport,
		ProviderRef:  lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceHostProvider, ID: "provider-fixture", Generation: 1},
		GuestViewRef: lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceGuestView, ID: "view-fixture", Generation: 1},
		State:        AttachmentPlanned, CreatedAt: time.Now().UTC(),
	}
	provider, err := ProviderSpecFromAttachment(attachment, SelectedLimits())
	if err != nil {
		t.Fatal(err)
	}
	view := ViewSpec{Attachment: attachment, CredentialAudience: "hideout.workspace-portal/v1"}
	if err := view.Validate(provider); err != nil {
		t.Fatal(err)
	}

	other := attachment
	other.SessionID = "ses_sibling"
	other.ID = "att_fedcba9876543210"
	other.GuestViewRef.ID = "view-sibling"
	if err := (ViewSpec{Attachment: other, CredentialAudience: view.CredentialAudience}).Validate(provider); err != nil {
		t.Fatalf("same-root sibling binding should reuse provider without sharing view identity: %v", err)
	}
	other.WorkspaceID = "wrk_" + string(bytes.Repeat([]byte{'a'}, 64))
	if err := (ViewSpec{Attachment: other, CredentialAudience: view.CredentialAudience}).Validate(provider); err == nil {
		t.Fatal("different workspace authority reused provider")
	}
}

func TestProviderObservationRequiresTruthfulProof(t *testing.T) {
	now := time.Now().UTC()
	for _, observation := range []Observation{
		{State: ObservationReady, ObservedAt: now},
		{State: ObservationAbsent, ObservedAt: now},
		{State: ObservationUnproved, ObservedAt: now, ReasonCode: "provider-state-unproved"},
	} {
		if err := observation.Validate(); err != nil {
			t.Fatalf("valid observation %#v: %v", observation, err)
		}
	}
	if err := (Observation{State: ObservationUnproved, ObservedAt: now}).Validate(); err == nil {
		t.Fatal("unproved observation omitted reason")
	}
}
