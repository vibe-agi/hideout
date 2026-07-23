package lima

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/sessionwire"
)

func TestSupervisorStartControlBindsProjectionExpectation(t *testing.T) {
	session := projectionReadySessionFixture()
	start, err := supervisorStartControl(
		session, []string{"code", "."}, []string{"PATH=/hideout/session/shims:/usr/bin"},
		backend.RunStreams{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if start.ProjectionReadiness == nil {
		t.Fatal("supervisor start omitted projection readiness")
	}
	if start.ProjectionReadiness.CatalogDigest != session.ProjectionReadiness.Manifest.CatalogDigest ||
		start.ProjectionReadiness.ExpectedEntries != len(session.ProjectionReadiness.Manifest.Entries) ||
		!start.ProjectionReadiness.TargetProjected {
		t.Fatalf("projection start=%+v", start.ProjectionReadiness)
	}
}

func TestApplySupervisorProjectionReadinessRejectsForeignOrIncompleteProof(t *testing.T) {
	session := projectionReadySessionFixture()
	valid := &sessionwire.SupervisorProjectionReadinessReady{
		Status: "ready", EnvironmentID: session.EnvironmentID,
		SessionSnapshotID: session.SessionSnapshotID,
		CatalogDigest:     session.ProjectionReadiness.Manifest.CatalogDigest,
		ExpectedEntries:   1, ObservedEntries: 1, DurationMillis: 7, TargetProjected: true,
	}
	if err := applySupervisorProjectionReadiness(session, valid); err != nil {
		t.Fatal(err)
	}
	if session.ProjectionReadinessObservation == nil {
		t.Fatal("matching supervisor proof did not bind an observation")
	}
	session.ProjectionReadinessObservation = nil
	foreign := *valid
	foreign.CatalogDigest = "sha256:" + strings.Repeat("e", 64)
	if err := applySupervisorProjectionReadiness(session, &foreign); err == nil {
		t.Fatal("foreign catalog readiness was accepted")
	}
	if err := applySupervisorProjectionReadiness(session, nil); err == nil {
		t.Fatal("omitted projection readiness was accepted")
	}
}

func TestPreCommitCancellationIsTypedAndBoundedWithoutTargetGrace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := preCommitCancellationError(ctx)
	var readiness *backend.ProjectionReadinessError
	if !errors.As(err, &readiness) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%T %v", err, err)
	}
	if readiness.Status != backend.ProjectionReadinessCancelled ||
		readiness.ReasonCode != backend.ProjectionReadinessCancellation ||
		!strings.Contains(readiness.Hint, "before target start") {
		t.Fatalf("cancellation disposition=%+v", readiness)
	}
}

func TestProjectionReadinessRunFailuresClassifyTimeoutAndIdentityDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status backend.ProjectionReadinessStatus
		reason backend.ProjectionReadinessReason
	}{
		{
			name:   "two second visibility timeout",
			err:    errors.New("session runtime files did not become visible before deadline"),
			status: backend.ProjectionReadinessTimedOut, reason: backend.ProjectionReadinessTimeout,
		},
		{
			name:   "boot identity drift",
			err:    errors.New("guest boot identity changed before isolated target start"),
			status: backend.ProjectionReadinessRefused, reason: backend.ProjectionReadinessIdentityDrift,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := projectionReadySessionFixture()
			err := classifyProjectionReadinessRunError(context.Background(), session, test.err)
			var readiness *backend.ProjectionReadinessError
			if !errors.As(err, &readiness) || !errors.Is(err, test.err) {
				t.Fatalf("classified error=%T %v", err, err)
			}
			if readiness.Status != test.status || readiness.ReasonCode != test.reason ||
				!strings.Contains(readiness.Hint, "retry") {
				t.Fatalf("disposition=%+v", readiness)
			}
		})
	}
}

func projectionReadySessionFixture() *backend.Session {
	snapshot := "sha256:" + strings.Repeat("c", 64)
	session := &backend.Session{
		ID: "ses_projection", EnvironmentID: "env_projection", SessionSnapshotID: snapshot,
		InstanceName: "hideout-projection", ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
		TargetUser: "developer", GuestWork: "/workspace",
		ProjectionReadiness: &backend.ProjectionReadinessExpectation{
			ManifestRelativePath: backend.ProjectionReadinessManifestFile,
			Deadline:             backend.MaxProjectionReadinessDeadline,
			TargetProjected:      true,
			Manifest: backend.ProjectionReadinessManifest{
				Schema: backend.ProjectionReadinessManifestSchema, SessionID: "ses_projection",
				EnvironmentID: "env_projection", SessionSnapshotID: snapshot,
				Entries: []backend.ProjectionReadinessEntry{{
					Name: "hideout-shim", RelativePath: "hideout-shim",
					SHA256: "sha256:" + strings.Repeat("1", 64), Kind: backend.ProjectionEntryDispatcher,
				}},
			},
		},
	}
	catalog, _ := backend.ProjectionReadinessCatalogDigest(session.ProjectionReadiness.Manifest)
	session.ProjectionReadiness.Manifest.CatalogDigest = catalog
	return session
}
