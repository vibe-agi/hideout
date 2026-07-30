package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	doctorpkg "github.com/vibe-agi/hideout/internal/doctor"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestDoctorActivityReportsAuthoritativeCoveragePrivacyAndRetentionHeadroom(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_doctor",
		"lima",
		"hideout-default:4:boot-doctor",
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := manager.OperatorSnapshot{
		GeneratedAt: now,
		Sessions: []manager.OperatorSessionProjection{{
			ID: "ses_doctor", Profile: "default", State: "running",
		}},
		ActivityRetention: []manager.OperatorActivityRetentionProjection{{
			Owner: owner, EarliestAt: now.Add(-15 * time.Minute), LatestAt: now,
			UsedBytes: 64 << 20, LimitBytes: 256 << 20,
			MaxAgeSeconds: int64(time.Hour / time.Second),
		}},
		ActivityStoreRetention: &manager.OperatorActivityStoreRetentionProjection{
			UsedBytes: 80 << 20, LimitBytes: 1 << 30,
			DefaultOwnerLimitBytes: 256 << 20,
			ActiveSegmentBytes:     4 << 20,
			Owners:                 1,
			Segments:               2,
		},
	}
	for index, subsystem := range []string{
		workloadtypes.SubsystemProcess,
		workloadtypes.SubsystemFile,
		workloadtypes.SubsystemNetwork,
		workloadtypes.SubsystemDNS,
	} {
		snapshot.Coverage = append(snapshot.Coverage, workloadtypes.CoverageInterval{
			Owner: owner, SessionID: "ses_doctor", Subsystem: subsystem,
			State:     workloadtypes.CoverageAvailable,
			StartedAt: now.Add(time.Duration(index) * time.Second),
		})
	}

	var gotQuery manager.OperatorSnapshotQuery
	a := app{activitySnapshot: func(
		_ context.Context,
		_ string,
		query manager.OperatorSnapshotQuery,
	) (manager.OperatorSnapshot, error) {
		gotQuery = query
		return snapshot, nil
	}}
	finding := activityDoctorFindingForTest(
		t,
		a,
		profile.Store{Root: t.TempDir()},
		profile.Default("default"),
	)
	if gotQuery.Profile != "default" || gotQuery.ActivityLimit != 1 {
		t.Fatalf("activity doctor query=%+v", gotQuery)
	}
	if finding.Status != doctorpkg.StatusPass {
		t.Fatalf("activity finding status=%s: %+v", finding.Status, finding)
	}
	facts := fmt.Sprint(finding.Details["observedFacts"])
	for _, want := range []string{
		"observationScope=top-level-command-and-descendants",
		"localPathVisibility=visible-in-authenticated-local-view",
		"notCaptured=file-content,environment-values,keystrokes,full-pty,packet-payload",
		"coverageNonClaim=no-events-does-not-prove-no-behavior",
		"retentionOwner=exact-environment-or-disposable-session-plus-backend-incarnation",
		"globalHeadroomBytes=",
		"minimumOwnerHeadroomBytes=",
		"minimumApproxTTLHeadroomSeconds=",
		"activeSegmentAllowanceBytes=",
	} {
		if !strings.Contains(facts, want) {
			t.Fatalf("activity doctor facts missing %q:\n%s", want, facts)
		}
	}
}

func TestDoctorActivityDoesNotTurnUnavailableEvidenceIntoNoActivityClaim(t *testing.T) {
	a := app{activitySnapshot: func(
		context.Context,
		string,
		manager.OperatorSnapshotQuery,
	) (manager.OperatorSnapshot, error) {
		return manager.OperatorSnapshot{}, errors.New("daemon unavailable")
	}}
	finding := activityDoctorFindingForTest(
		t,
		a,
		profile.Store{Root: t.TempDir()},
		profile.Default("default"),
	)
	if finding.Status != doctorpkg.StatusWarn {
		t.Fatalf("activity finding status=%s: %+v", finding.Status, finding)
	}
	all := finding.Summary + " " +
		fmt.Sprint(finding.Details) + " " +
		strings.Join(finding.NextActions, " ")
	if !strings.Contains(all, "authoritativeSnapshot=unavailable") ||
		!strings.Contains(all, "no-events-does-not-prove-no-behavior") {
		t.Fatalf("unavailable activity finding lacks non-claim: %+v", finding)
	}
	for _, forbidden := range []string{"no activity occurred", "no harmful behavior"} {
		if strings.Contains(strings.ToLower(all), forbidden) {
			t.Fatalf("unavailable evidence became an absence claim: %s", all)
		}
	}
}

func activityDoctorFindingForTest(
	t *testing.T,
	a app,
	store profile.Store,
	p profile.Profile,
) doctorpkg.Finding {
	t.Helper()
	request := doctorpkg.Request{
		Profile: "default", Backend: "lima", Level: doctorpkg.LevelLight,
		Features: []string{"activity"},
	}
	builder := doctorpkg.NewBuilder(request)
	a.addDoctorFeatureDiagnostics(
		request,
		store,
		p,
		"lima",
		"",
		"",
		builder,
	)
	for _, finding := range builder.Report().Findings {
		if finding.CheckID == "feature-activity" {
			return finding
		}
	}
	t.Fatalf("feature-activity finding missing: %+v", builder.Report())
	return doctorpkg.Finding{}
}
