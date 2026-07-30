package manager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/profile"
	runsession "github.com/vibe-agi/hideout/internal/session"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityPreparationForRunBindsExactIncarnationAndOwnerLifetime(t *testing.T) {
	incarnation := lifecycle.EnvironmentRef{
		EnvironmentID: "env_activity", StartGeneration: 7,
		InstanceName: "hideout-activity",
		BootID:       "01234567-89ab-cdef-0123-456789abcdef",
	}
	registration := activityRegistrationFixture{incarnation: incarnation}
	session := &backend.Session{
		ID: "ses_activity", EnvironmentID: incarnation.EnvironmentID,
		Backend: "lima", InstanceName: incarnation.InstanceName,
		ExpectedBootID:       incarnation.BootID,
		ObserverHelperDigest: "sha256:" + strings.Repeat("a", 64),
	}
	runtimeProfile := profile.Default("default")

	reusable, err := activityPreparationForRun(
		session,
		RunEnvironment{
			Active: true,
			Record: environment.Record{
				ID: incarnation.EnvironmentID, Backend: "lima",
				InstanceName: incarnation.InstanceName,
			},
		},
		registration,
		runtimeProfile,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantIncarnation := "hideout-activity:7:" + incarnation.BootID
	if reusable.Owner.Kind != workloadtypes.OwnerReusableEnvironment ||
		reusable.Owner.EnvironmentID != incarnation.EnvironmentID ||
		reusable.BackendIncarnationID != wantIncarnation ||
		reusable.Owner.BackendIncarnationID != wantIncarnation ||
		reusable.Retention !=
			workloadtypes.DefaultActivityRetentionPolicy() {
		t.Fatalf("reusable activity preparation=%+v", reusable)
	}

	runtimeProfile.Activity = &profile.ActivityConfig{
		Retention: profile.ActivityRetention{
			MaxBytes: 64 << 20, MaxAgeSeconds: 12 * 60 * 60,
		},
	}
	disposable, err := activityPreparationForRun(
		session,
		RunEnvironment{
			Active: true,
			Record: environment.Record{
				ID: incarnation.EnvironmentID, Backend: "lima",
				InstanceName: incarnation.InstanceName, Disposable: true,
			},
		},
		registration,
		runtimeProfile,
	)
	if err != nil {
		t.Fatal(err)
	}
	if disposable.Owner.Kind != workloadtypes.OwnerDisposableSession ||
		disposable.Owner.SessionID != session.ID ||
		disposable.Owner.EnvironmentID != "" ||
		disposable.BackendIncarnationID != wantIncarnation ||
		disposable.Retention.MaxBytes != 64<<20 ||
		disposable.Retention.MaxAgeSeconds != 12*60*60 {
		t.Fatalf("disposable activity preparation=%+v", disposable)
	}
}

func TestActivityObservationBoundaryAuditPublishesContractWithoutOwnerIdentity(
	t *testing.T,
) {
	owner, err := workloadtypes.NewReusableOwner(
		"env_activity",
		"lima",
		"hideout-activity:7:01234567-89ab-cdef-0123-456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	preparation := backend.ActivityPreparation{
		Owner: owner, SessionID: "ses_activity",
		EnvironmentID:        "env_activity",
		Backend:              "lima",
		BackendIncarnationID: owner.BackendIncarnationID,
		GuestBootID:          "01234567-89ab-cdef-0123-456789abcdef",
		ObserverGeneration:   1,
		ObserverHelperDigest: "sha256:" + strings.Repeat("a", 64),
		Retention: workloadtypes.ActivityRetentionPolicy{
			MaxBytes: 64 << 20, MaxAgeSeconds: 12 * 60 * 60,
		},
	}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	run := RunSession{
		Layout: runsession.Layout{ID: "ses_activity"},
		Plan: RunPlan{
			ProfileName: "default",
			Backend:     "lima",
		},
	}
	if err := emitActivityObservationBoundary(writer, run, preparation); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	summary := SummarizeRunBoundary(auditPath)
	if summary.ActivityObservation == nil ||
		summary.ActivityObservation.OwnerKind !=
			workloadtypes.OwnerReusableEnvironment ||
		summary.ActivityObservation.RetentionMaxBytes != 64<<20 ||
		summary.ActivityObservation.RetentionMaxAgeSeconds != 12*60*60 {
		t.Fatalf("activity boundary summary=%+v", summary.ActivityObservation)
	}
	encoded, err := json.Marshal(summary.ActivityObservation)
	if err != nil {
		t.Fatal(err)
	}
	for _, secretIdentity := range []string{
		"env_activity",
		"ses_activity",
		"hideout-activity",
		"01234567-89ab-cdef-0123-456789abcdef",
	} {
		if strings.Contains(string(encoded), secretIdentity) {
			t.Fatalf(
				"shareable activity boundary leaked %q: %s",
				secretIdentity,
				encoded,
			)
		}
	}
}

func TestMaterializeObserverCopiesOnlyManifestVerifiedHelperAndReturnsDigest(t *testing.T) {
	source := filepath.Join(t.TempDir(), "hideout-observer-linux-"+runtime.GOARCH)
	if err := os.WriteFile(source, []byte("packaged observer fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := helperbin.WriteLinuxObserverManifest(source, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	t.Setenv(helperbin.LinuxObserverPathEnvironment, source)
	destination := t.TempDir()
	digest, err := MaterializeObserver(destination, "lima", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Fatalf("observer digest=%q", digest)
	}
	projected := filepath.Join(destination, helperbin.LinuxObserverCommand)
	data, err := os.ReadFile(projected)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(projected)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "packaged observer fixture" ||
		!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != 0o700 {
		t.Fatalf("projected observer mode=%s content=%q", info.Mode(), data)
	}
}

type activityRegistrationFixture struct {
	incarnation lifecycle.EnvironmentRef
}

func (fixture activityRegistrationFixture) Incarnation() lifecycle.EnvironmentRef {
	return fixture.incarnation
}
func (activityRegistrationFixture) Root() lifecycle.ResourceRef {
	return lifecycle.ResourceRef{
		Kind: lifecycle.KindBackendIncarnation, ID: "env_activity", Generation: 7,
	}
}
func (activityRegistrationFixture) Session() lifecycle.ResourceRef {
	return lifecycle.ResourceRef{
		Kind: lifecycle.KindRunSession, ID: "ses_activity", Generation: 7,
	}
}
func (activityRegistrationFixture) Commit(context.Context) error { return nil }
func (activityRegistrationFixture) BindBoot(context.Context, string) error {
	return nil
}
func (activityRegistrationFixture) Register(
	context.Context,
	lifecycle.RegistrationSpec,
) (lifecycle.ResourceRef, error) {
	return lifecycle.ResourceRef{}, nil
}
func (activityRegistrationFixture) Transition(
	context.Context,
	lifecycle.ResourceRef,
	lifecycle.ResourceState,
) error {
	return nil
}
func (activityRegistrationFixture) Release(
	context.Context,
	lifecycle.ResourceRef,
	error,
) error {
	return nil
}
func (activityRegistrationFixture) RecordFact(
	context.Context,
	lifecycle.FactSpec,
) error {
	return nil
}
func (activityRegistrationFixture) Finish(context.Context, error) error { return nil }
