package manager

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestOperatorSnapshotUnscopedEmptyCollectionsEncodeAsArrays(t *testing.T) {
	query := OperatorSnapshotQuery{}
	value := struct {
		Activity   []ActivityProjection                  `json:"activity"`
		Coverage   []CoverageProjection                  `json:"coverage"`
		Retention  []OperatorActivityRetentionProjection `json:"retention"`
		Operations []Operation                           `json:"operations"`
	}{
		Activity:   scopeOperatorActivity(nil, Overview{}, query),
		Coverage:   scopeOperatorCoverage(nil, Overview{}, query),
		Retention:  scopeOperatorActivityRetention(nil, Overview{}, query),
		Operations: scopeOperatorOperations(nil, Overview{}, query),
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "null") {
		t.Fatalf("empty operator collections regressed to JSON null: %s", encoded)
	}
	for _, field := range []string{
		`"activity":[]`,
		`"coverage":[]`,
		`"retention":[]`,
		`"operations":[]`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("operator collection missing %s: %s", field, encoded)
		}
	}
}

func TestOperatorSessionWorkspaceProjectionRejectsUnprovedIdentityAndCleanup(t *testing.T) {
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	base := OperatorSessionProjection{
		ID: "ses_workspace", EnvironmentID: "env_workspace",
		State: "running", Command: "claude",
		WorkspaceID: workspaceID, WorkspaceLabel: "workspace [aaaaaaaa]",
		GuestWorkspace:     workspaceattach.LogicalWorkspaceRoot,
		WorkspaceTransport: workspaceattach.SelectedTransport,
		WorkspaceViewState: workspaceattach.AttachmentReady,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid workspace projection: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*OperatorSessionProjection)
	}{
		{
			name: "wrong logical guest root",
			mutate: func(value *OperatorSessionProjection) {
				value.GuestWorkspace = "/host/workspace"
			},
		},
		{
			name: "malformed workspace identity",
			mutate: func(value *OperatorSessionProjection) {
				value.WorkspaceID = "wrk_not-canonical"
			},
		},
		{
			name: "foreign relation identity",
			mutate: func(value *OperatorSessionProjection) {
				value.WorkspaceRelations = []workspaceattach.RootRelationNotice{{
					Relation:         workspaceattach.RootDisjoint,
					SelectedPosition: workspaceattach.RelationPositionPeer,
					WorkspaceID:      "wrk_" + strings.Repeat("b", 64),
					OtherWorkspaceID: "wrk_" + strings.Repeat("c", 64),
				}}
			},
		},
		{
			name: "released without cleanup proof",
			mutate: func(value *OperatorSessionProjection) {
				value.WorkspaceViewState = workspaceattach.AttachmentReleased
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection := base
			test.mutate(&projection)
			if err := projection.Validate(); err == nil {
				t.Fatal("invalid workspace projection was accepted")
			}
		})
	}
}

func TestOperatorSnapshotBuilderScopesEverySessionOwnedProjection(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	overview := operatorSnapshotOverviewFixture(now)
	alphaActivity := operatorActivityFixture(t, "act_alpha000001", "ses_alpha", "env_alpha", "cov_alpha000001", now)
	betaActivity := operatorActivityFixture(t, "act_beta0000001", "ses_beta", "env_beta", "cov_beta0000001", now)
	alphaCoverage := operatorCoverageFixture(t, "cov_alpha000001", "ses_alpha", "env_alpha", now)
	betaCoverage := operatorCoverageFixture(t, "cov_beta0000001", "ses_beta", "env_beta", now)
	var observedQuery OperatorSnapshotQuery

	service := OperatorSnapshotService{
		Core: Core{Store: profile.Store{Root: t.TempDir()}},
		Overview: OperatorOverviewProviderFunc(func(context.Context) (Overview, error) {
			return overview, nil
		}),
		Connection: OperatorConnectionProviderFunc(func(context.Context) (OperatorConnectionProjection, error) {
			return OperatorConnectionProjection{
				InstanceID: "daemon_fixture", CredentialGeneration: 4, Sequence: 12,
				StreamHealth: OperatorStreamHealth{State: OperatorHealthLive},
			}, nil
		}),
		Observation: OperatorObservationProviderFunc(func(
			_ context.Context,
			query OperatorSnapshotQuery,
		) (OperatorObservation, error) {
			observedQuery = query
			return OperatorObservation{
				Activity: []ActivityProjection{betaActivity, alphaActivity},
				Coverage: []CoverageProjection{betaCoverage, alphaCoverage},
				Capabilities: []OperatorCapabilityProjection{{
					ID: "activity.file", State: OperatorCapabilityAvailable, Provider: "ebpf",
				}},
			}, nil
		}),
		ProfileProjections: OperatorProfileProjectionProviderFunc(func(name string) (ProfileProjection, error) {
			return operatorProfileProjectionFixture(name, now), nil
		}),
		OperationHistory: OperatorOperationProviderFunc(func(int) ([]Operation, error) {
			return []Operation{}, nil
		}),
		Now: func() time.Time { return now },
	}

	snapshot, err := service.Build(context.Background(), OperatorSnapshotQuery{
		Profile: "alpha", Session: "ses_alpha", ActivityLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observedQuery.Profile != "alpha" || observedQuery.Session != "ses_alpha" ||
		observedQuery.ActivityLimit != 1 {
		t.Fatalf("observation query=%+v", observedQuery)
	}
	if len(snapshot.Profiles) != 1 || snapshot.Profiles[0].Profile != "alpha" {
		t.Fatalf("profile scope=%+v", snapshot.Profiles)
	}
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].ID != "ses_alpha" ||
		snapshot.Sessions[0].WorkspaceLabel != "alpha [aaaaaaaa]" ||
		snapshot.Sessions[0].WorkspaceViewState != workspaceattach.AttachmentReady {
		t.Fatalf("session scope=%+v", snapshot.Sessions)
	}
	if len(snapshot.Environments) != 1 ||
		snapshot.Environments[0].ID != "env_alpha" ||
		snapshot.Environments[0].ActiveSessions != 1 ||
		snapshot.Environments[0].ActiveWorkspaceViews != 1 ||
		snapshot.Environments[0].Mode != environment.ModeShared ||
		snapshot.Environments[0].MachineIdentityID != testEnvironmentMachineIdentityID() ||
		snapshot.Environments[0].WorkspaceProviderState != "ready" {
		t.Fatalf("environment scope=%+v", snapshot.Environments)
	}
	if len(snapshot.Activity) != 1 || snapshot.Activity[0].SessionID != "ses_alpha" {
		t.Fatalf("activity scope=%+v", snapshot.Activity)
	}
	if len(snapshot.Coverage) != 1 || snapshot.Coverage[0].SessionID != "ses_alpha" {
		t.Fatalf("coverage scope=%+v", snapshot.Coverage)
	}
	if snapshot.StreamHealth.State != OperatorHealthLive ||
		!slices.Contains(snapshot.NextActions, "activity.inspect") {
		t.Fatalf("health/actions=%+v %v", snapshot.StreamHealth, snapshot.NextActions)
	}
	if capabilityState(snapshot.Capabilities, "activity.file") != OperatorCapabilityAvailable ||
		capabilityState(snapshot.Capabilities, "activity.dns") != OperatorCapabilityUnavailable {
		t.Fatalf("capabilities=%+v", snapshot.Capabilities)
	}
}

func TestOperatorSnapshotBuilderFailsClosedAcrossMismatchedProfileAndSession(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	overview := operatorSnapshotOverviewFixture(now)
	service := OperatorSnapshotService{
		Core: Core{Store: profile.Store{Root: t.TempDir()}},
		Overview: OperatorOverviewProviderFunc(func(context.Context) (Overview, error) {
			return overview, nil
		}),
		Observation: OperatorObservationProviderFunc(func(
			context.Context,
			OperatorSnapshotQuery,
		) (OperatorObservation, error) {
			return OperatorObservation{
				Activity: []ActivityProjection{
					operatorActivityFixture(t, "act_beta0000001", "ses_beta", "env_beta", "cov_beta0000001", now),
				},
				Coverage: []CoverageProjection{
					operatorCoverageFixture(t, "cov_beta0000001", "ses_beta", "env_beta", now),
				},
				Risks: []RiskFinding{{
					ID: "risk_beta000001", RuleID: "fixture.risk", RuleVersion: "v1",
					Severity: "high", Title: "beta-only risk", Confidence: "exact",
				}},
			}, nil
		}),
		ProfileProjections: OperatorProfileProjectionProviderFunc(func(name string) (ProfileProjection, error) {
			return operatorProfileProjectionFixture(name, now), nil
		}),
		OperationHistory: OperatorOperationProviderFunc(func(int) ([]Operation, error) {
			return []Operation{}, nil
		}),
		Now: func() time.Time { return now },
	}

	snapshot, err := service.Build(context.Background(), OperatorSnapshotQuery{
		Profile: "alpha", Session: "ses_beta", ActivityLimit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions) != 0 || len(snapshot.Activity) != 0 ||
		len(snapshot.Coverage) != 0 || len(snapshot.Risks) != 0 {
		t.Fatalf("mismatched scope leaked data: %+v", snapshot)
	}
}

func TestOperatorSnapshotProjectsProvedEffectiveNetworkSessionAndTransition(
	t *testing.T,
) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	desired := profile.Default("alpha")
	desired.Network = profile.Network{
		Mode:             profile.NetworkModeTun2Socks,
		ProxySecretRef:   "local-proxy",
		MediatedResolver: "1.1.1.1",
		ProxyEnvVisible:  false,
	}
	profileStore := profile.Store{Root: t.TempDir()}
	if err := profileStore.Create(desired); err != nil {
		t.Fatal(err)
	}
	desired, err := profileStore.Load("alpha")
	if err != nil {
		t.Fatal(err)
	}
	_, snapshotID, err := SessionSnapshotForProfile(desired)
	if err != nil {
		t.Fatal(err)
	}
	overview := operatorSnapshotOverviewFixture(now)
	overview.Sessions[0].OwnerStatus = session.OwnerLive
	overview.Sessions[0].SessionSnapshotID = snapshotID

	operationStore := OperationStore{Root: t.TempDir()}
	binding := OperationBinding{
		ID: "op_effectiveprofile1", Kind: "profile.transaction",
		Owner:        OperationOwner{Kind: "profile", ID: "alpha"},
		PlanDigest:   "sha256:" + strings.Repeat("b", 64),
		BaseRevision: 1,
	}
	if _, _, err := operationStore.Reserve(
		binding,
		[]EffectResult{{
			ID: "persist-profile", Kind: "persist",
			Provider: "manager.profile", Status: EffectPending,
		}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := operationStore.Transition(
		binding.ID,
		OperationClaimed,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := operationStore.RequireRecovery(
		binding.ID,
		Recovery{
			Code:       "profile-effect-unproved",
			Summary:    "The profile effect is not yet proved.",
			NextAction: "Retry the operation.",
		},
	); err != nil {
		t.Fatal(err)
	}
	routeCalls := []string{}
	service := OperatorSnapshotService{
		Core: Core{Store: profileStore},
		Overview: OperatorOverviewProviderFunc(
			func(context.Context) (Overview, error) {
				return overview, nil
			},
		),
		ProfileProjections: OperatorProfileProjectionProviderFunc(
			func(name string) (ProfileProjection, error) {
				projection := operatorProfileProjectionFixture(
					name,
					now,
				)
				projection.Desired = desired
				projection.Revision = 7
				return projection, nil
			},
		),
		NetworkRoutes: OperatorNetworkRouteObserverFunc(
			func(
				_ context.Context,
				environmentID string,
			) (NetworkRouteConfiguration, error) {
				routeCalls = append(routeCalls, environmentID)
				if environmentID != "env_alpha" {
					return NetworkRouteConfiguration{},
						ErrNetworkTransitionProviderUnavailable
				}
				return NetworkRouteConfiguration{
					Mode:                  netpolicy.ModeTun2Socks,
					ProxySecretRef:        "local-proxy",
					ProxySecretGeneration: 4,
					MediatedResolver:      "1.1.1.1",
				}, nil
			},
		),
		OperationHistory: operationStore,
		Now:              func() time.Time { return now },
	}

	snapshot, err := service.Build(
		context.Background(),
		OperatorSnapshotQuery{
			Profile: "alpha",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Profiles) != 1 {
		t.Fatalf("profiles=%+v", snapshot.Profiles)
	}
	projection := snapshot.Profiles[0]
	if projection.Effective.Status != EffectiveCurrent ||
		projection.Effective.Network == nil ||
		projection.Effective.Network.Mode != "proxy" ||
		projection.Effective.Network.ProxySecretRef != "local-proxy" ||
		projection.Effective.Network.SecretGeneration != 4 ||
		projection.Effective.Network.DNS != "1.1.1.1" ||
		!projection.Effective.Network.ObservedAt.Equal(now) {
		t.Fatalf(
			"effective network=%+v",
			projection.Effective,
		)
	}
	if len(projection.Effective.Sessions) != 1 ||
		projection.Effective.Sessions[0].SessionID != "ses_alpha" ||
		projection.Effective.Sessions[0].SnapshotID != snapshotID ||
		!projection.Effective.Sessions[0].Current ||
		projection.Effective.Sessions[0].ProfileRevision != 7 {
		t.Fatalf(
			"effective sessions=%+v",
			projection.Effective.Sessions,
		)
	}
	if projection.Transition == nil ||
		projection.Transition.OperationID != binding.ID ||
		projection.Transition.Phase != OperationRecoveryRequired ||
		!slices.Equal(
			projection.Transition.Blockers,
			[]string{"profile-effect-unproved"},
		) {
		t.Fatalf(
			"profile transition=%+v",
			projection.Transition,
		)
	}
	if !slices.Equal(routeCalls, []string{"env_alpha"}) {
		t.Fatalf("route observations=%v", routeCalls)
	}
}

func TestOperatorSnapshotDoesNotAdoptStaleSessionOrDurableNetworkState(
	t *testing.T,
) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	overview := operatorSnapshotOverviewFixture(now)
	overview.Sessions[0].OwnerStatus = session.OwnerStale
	overview.Sessions[0].SessionSnapshotID =
		"sha256:" + strings.Repeat("c", 64)
	service := OperatorSnapshotService{
		Core: Core{Store: profile.Store{Root: t.TempDir()}},
		Overview: OperatorOverviewProviderFunc(
			func(context.Context) (Overview, error) {
				return overview, nil
			},
		),
		ProfileProjections: OperatorProfileProjectionProviderFunc(
			func(name string) (ProfileProjection, error) {
				return operatorProfileProjectionFixture(
					name,
					now,
				), nil
			},
		),
		NetworkRoutes: OperatorNetworkRouteObserverFunc(
			func(
				context.Context,
				string,
			) (NetworkRouteConfiguration, error) {
				return NetworkRouteConfiguration{},
					ErrNetworkTransitionProviderUnavailable
			},
		),
		OperationHistory: OperatorOperationProviderFunc(
			func(int) ([]Operation, error) {
				return []Operation{}, nil
			},
		),
		Now: func() time.Time { return now },
	}

	snapshot, err := service.Build(
		context.Background(),
		OperatorSnapshotQuery{Profile: "alpha"},
	)
	if err != nil {
		t.Fatal(err)
	}
	effective := snapshot.Profiles[0].Effective
	if effective.Status != EffectiveNotObserved ||
		effective.Network != nil ||
		len(effective.Sessions) != 0 {
		t.Fatalf(
			"stale restart residue became effective=%+v",
			effective,
		)
	}

	overview.Sessions[0].OwnerStatus = session.OwnerLive
	snapshot, err = service.Build(
		context.Background(),
		OperatorSnapshotQuery{Profile: "alpha"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Profiles[0].Effective.Status != EffectiveUnproved ||
		snapshot.Profiles[0].Effective.Network != nil {
		t.Fatalf(
			"live session without route proof=%+v",
			snapshot.Profiles[0].Effective,
		)
	}

	overview.Environments = overview.Environments[1:]
	routeCalls := 0
	service.NetworkRoutes = OperatorNetworkRouteObserverFunc(
		func(
			context.Context,
			string,
		) (NetworkRouteConfiguration, error) {
			routeCalls++
			return NetworkRouteConfiguration{
				Mode: netpolicy.ModeDirect,
			}, nil
		},
	)
	snapshot, err = service.Build(
		context.Background(),
		OperatorSnapshotQuery{Profile: "alpha"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Profiles[0].Effective.Status != EffectiveUnproved ||
		snapshot.Profiles[0].Effective.Network != nil ||
		routeCalls != 0 {
		t.Fatalf(
			"live session with missing environment route identity=%+v calls=%d",
			snapshot.Profiles[0].Effective,
			routeCalls,
		)
	}
}

func TestOperatorSnapshotBuilderNeverClaimsMissingObservationIsAvailable(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	service := OperatorSnapshotService{
		Core: Core{Store: profile.Store{Root: t.TempDir()}},
		Overview: OperatorOverviewProviderFunc(func(context.Context) (Overview, error) {
			return Overview{Version: "hideout.manager/v1"}, nil
		}),
		OperationHistory: OperatorOperationProviderFunc(func(int) ([]Operation, error) {
			return []Operation{}, nil
		}),
		Now: func() time.Time { return now },
	}
	snapshot, err := service.Build(context.Background(), OperatorSnapshotQuery{ActivityLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.StreamHealth.State != OperatorHealthDaemonless {
		t.Fatalf("health=%+v", snapshot.StreamHealth)
	}
	for _, id := range []string{"activity.process", "activity.file", "activity.network", "activity.dns"} {
		if state := capabilityState(snapshot.Capabilities, id); state != OperatorCapabilityUnavailable {
			t.Fatalf("%s capability=%q, want unavailable: %+v", id, state, snapshot.Capabilities)
		}
	}
	if len(snapshot.Activity) != 0 || len(snapshot.Coverage) != 0 {
		t.Fatalf("missing provider manufactured evidence: %+v", snapshot)
	}
}

func TestOperatorSnapshotUsesSharedMigrationProjectionAndRecoveryAction(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	older := operatorMigrationProjectionFixture(
		now.Add(-time.Minute), "op_migrationolder1", false,
	)
	recovery := operatorMigrationProjectionFixture(
		now, "op_migrationrecover1", true,
	)
	service := OperatorSnapshotService{
		Core: Core{Store: profile.Store{Root: t.TempDir()}},
		Overview: OperatorOverviewProviderFunc(func(context.Context) (Overview, error) {
			return Overview{Version: "hideout.manager/v1"}, nil
		}),
		Connection: OperatorConnectionProviderFunc(func(context.Context) (OperatorConnectionProjection, error) {
			return OperatorConnectionProjection{
				InstanceID: "daemon_migration", CredentialGeneration: 2, Sequence: 4,
				StreamHealth: OperatorStreamHealth{State: OperatorHealthLive},
			}, nil
		}),
		OperationHistory: OperatorOperationProviderFunc(func(int) ([]Operation, error) {
			return []Operation{}, nil
		}),
		MigrationHistory: OperatorMigrationProviderFunc(
			func(limit int) ([]MigrationOperationProjection, error) {
				if limit != DefaultOperatorMigrationLimit {
					t.Fatalf("migration limit=%d", limit)
				}
				return []MigrationOperationProjection{older, recovery}, nil
			},
		),
		Now: func() time.Time { return now },
	}

	snapshot, err := service.Build(
		context.Background(), OperatorSnapshotQuery{ActivityLimit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Migrations) != 2 ||
		snapshot.Migrations[0].OperationID != recovery.OperationID ||
		snapshot.Migrations[1].OperationID != older.OperationID {
		t.Fatalf("migration projection order=%+v", snapshot.Migrations)
	}
	if !slices.Contains(snapshot.NextActions, "migration.recover") ||
		capabilityState(snapshot.Capabilities, "migration.manage") !=
			OperatorCapabilityAvailable {
		t.Fatalf(
			"migration action/capability=%v %+v",
			snapshot.NextActions,
			snapshot.Capabilities,
		)
	}
}

func TestOperatorSnapshotRejectsInvalidMigrationHistoryAsUnavailable(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	invalid := operatorMigrationProjectionFixture(now, "op_migrationinvalid1", false)
	invalid.Progress.CurrentItem = "socks5://user:password@example.test"
	service := OperatorSnapshotService{
		Core: Core{Store: profile.Store{Root: t.TempDir()}},
		Overview: OperatorOverviewProviderFunc(func(context.Context) (Overview, error) {
			return Overview{Version: "hideout.manager/v1"}, nil
		}),
		OperationHistory: OperatorOperationProviderFunc(func(int) ([]Operation, error) {
			return []Operation{}, nil
		}),
		MigrationHistory: OperatorMigrationProviderFunc(
			func(int) ([]MigrationOperationProjection, error) {
				return []MigrationOperationProjection{invalid}, nil
			},
		),
		Now: func() time.Time { return now },
	}

	snapshot, err := service.Build(
		context.Background(), OperatorSnapshotQuery{ActivityLimit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Migrations) != 0 ||
		capabilityState(snapshot.Capabilities, "migration.manage") !=
			OperatorCapabilityUnavailable ||
		!slices.Contains(snapshot.NextActions, "doctor.migration") {
		t.Fatalf("invalid migration history did not fail closed: %+v", snapshot)
	}
}

func TestOperatorSnapshotSurfacesScopedCoverageHistoryAndActivityRetention(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	overview := operatorSnapshotOverviewFixture(now)
	alphaCurrent := operatorCoverageFixture(
		t, "cov_alpha_current1", "ses_alpha", "env_alpha", now,
	)
	alphaGap := operatorCoverageFixture(
		t, "cov_alpha_history1", "ses_alpha", "env_alpha", now.Add(-time.Hour),
	)
	alphaGap.State = workloadtypes.CoveragePartial
	alphaGap.Reason = "ring-overflow"
	alphaGap.DroppedEventCount = 4
	alphaGapEnd := now.Add(-30 * time.Minute)
	alphaGap.EndedAt = &alphaGapEnd
	alphaGapEndSequence := uint64(9)
	alphaGap.EndSequence = &alphaGapEndSequence
	beta := operatorCoverageFixture(
		t, "cov_beta_history01", "ses_beta", "env_beta", now,
	)
	alphaOwner := alphaCurrent.Owner
	betaOwner := beta.Owner

	service := OperatorSnapshotService{
		Core: Core{Store: profile.Store{Root: t.TempDir()}},
		Overview: OperatorOverviewProviderFunc(func(context.Context) (Overview, error) {
			return overview, nil
		}),
		Observation: OperatorObservationProviderFunc(func(
			context.Context,
			OperatorSnapshotQuery,
		) (OperatorObservation, error) {
			return OperatorObservation{
				Coverage: []CoverageProjection{beta, alphaCurrent, alphaGap},
				ActivityRetention: []OperatorActivityRetentionProjection{
					{
						Owner: betaOwner, EarliestAt: now.Add(-2 * time.Hour),
						LatestAt: now, UsedBytes: 2048, LimitBytes: 8192,
					},
					{
						Owner: alphaOwner, EarliestAt: now.Add(-time.Hour),
						LatestAt: now, UsedBytes: 1024, LimitBytes: 4096,
						MaxAgeSeconds: 24 * 60 * 60,
						Pruned:        true, Corrupt: true,
						Reasons: []string{"corrupt-segment", "retention-pruned"},
					},
				},
				ActivityStoreRetention: &OperatorActivityStoreRetentionProjection{
					UsedBytes: 3072, LimitBytes: 1 << 30,
					DefaultOwnerLimitBytes: 256 << 20,
					ActiveSegmentBytes:     8 << 20,
					Owners:                 2, Segments: 3,
				},
			}, nil
		}),
		ProfileProjections: OperatorProfileProjectionProviderFunc(func(name string) (ProfileProjection, error) {
			return operatorProfileProjectionFixture(name, now), nil
		}),
		OperationHistory: OperatorOperationProviderFunc(func(int) ([]Operation, error) {
			return []Operation{}, nil
		}),
		Now: func() time.Time { return now },
	}

	snapshot, err := service.Build(context.Background(), OperatorSnapshotQuery{
		Profile: "alpha", Session: "ses_alpha", ActivityLimit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Coverage) != 2 ||
		snapshot.Coverage[0].ID != alphaCurrent.ID ||
		snapshot.Coverage[1].ID != alphaGap.ID ||
		snapshot.Coverage[1].DroppedEventCount != 4 {
		t.Fatalf("coverage history=%+v", snapshot.Coverage)
	}
	if len(snapshot.ActivityRetention) != 1 {
		t.Fatalf("retention scope=%+v", snapshot.ActivityRetention)
	}
	retention := snapshot.ActivityRetention[0]
	if !retention.Owner.Equal(alphaOwner) ||
		!retention.EarliestAt.Equal(now.Add(-time.Hour)) ||
		!retention.LatestAt.Equal(now) ||
		retention.UsedBytes != 1024 || retention.LimitBytes != 4096 ||
		retention.MaxAgeSeconds != 24*60*60 ||
		!retention.Pruned || !retention.Corrupt ||
		!slices.Equal(retention.Reasons, []string{"corrupt-segment", "retention-pruned"}) {
		t.Fatalf("retention projection=%+v", retention)
	}
	if snapshot.ActivityStoreRetention == nil ||
		snapshot.ActivityStoreRetention.UsedBytes != 3072 ||
		snapshot.ActivityStoreRetention.LimitBytes != 1<<30 {
		t.Fatalf(
			"store retention projection=%+v",
			snapshot.ActivityStoreRetention,
		)
	}
}

func operatorSnapshotOverviewFixture(now time.Time) Overview {
	return Overview{
		Version:  "hideout.manager/v1",
		Profiles: []ProfileSummary{{Name: "alpha"}, {Name: "beta"}},
		Sessions: []SessionSummary{
			{
				ID: "ses_alpha", Profile: "alpha", EnvironmentID: "env_alpha",
				State: session.OwnerStateRunning, CommandClass: "claude",
				StartedAt:      now.Add(-time.Minute),
				WorkspaceID:    "wrk_" + strings.Repeat("a", 64),
				WorkspaceLabel: "alpha [aaaaaaaa]", GuestWorkspace: workspaceattach.LogicalWorkspaceRoot,
				WorkspaceTransport: workspaceattach.SelectedTransport,
				WorkspaceViewState: workspaceattach.AttachmentReady,
			},
			{ID: "ses_beta", Profile: "beta", EnvironmentID: "env_beta", State: session.OwnerStateRunning, CommandClass: "codex", StartedAt: now.Add(-time.Minute)},
		},
		Environments: []EnvironmentSummary{
			{
				ID: "env_alpha", Name: "alpha", Profile: "alpha",
				Backend: "lima", Status: "running", Mode: environment.ModeShared,
				SharedSlot: "alpha", MachineIdentityID: testEnvironmentMachineIdentityID(),
				InstanceName: "hideout-alpha", ActiveSessions: 1,
				ActiveWorkspaceViews: 1, WorkspaceProviderState: "ready",
				OwnerHealth: "live", CreatedAt: now.Add(-time.Hour),
			},
			{
				ID: "env_beta", Name: "beta", Profile: "beta",
				Backend: "lima", Status: "stopped",
				InstanceName: "hideout-beta", CreatedAt: now.Add(-2 * time.Hour),
			},
		},
	}
}

func operatorProfileProjectionFixture(name string, now time.Time) ProfileProjection {
	value := profile.Default(name)
	return ProfileProjection{
		Schema: ProfileProjectionSchema, Profile: name, Revision: 1,
		ContentDigest: "sha256:" + strings.Repeat("a", 64), Desired: value,
		Effective: ProfileEffective{Status: EffectiveNotObserved, Sessions: []EffectiveSessionSnapshot{}},
		UpdatedAt: now,
	}
}

func operatorMigrationProjectionFixture(
	now time.Time,
	operationID string,
	recovery bool,
) MigrationOperationProjection {
	projection := MigrationOperationProjection{
		Schema:      MigrationOperationProjectionSchema,
		OperationID: operationID,
		Revision:    1,
		BundleID:    "migb_projection1234",
		Kind:        MigrationOperationImport,
		State:       MigrationPhaseMaterializing,
		PhaseLabel:  migrationPhaseLabel(MigrationPhaseMaterializing),
		Progress: MigrationProgressProjection{
			LogicalTotalKnown: true, CompletedLogicalBytes: 1,
			TotalLogicalBytes: 2, ComponentsComplete: 1, ComponentsTotal: 2,
			PhaseStartedAt: now, CheckpointAt: now,
			ElapsedSeconds: 1, RemainingKnown: true, RemainingSeconds: 1,
		},
		Recovery: MigrationRecoveryProjection{
			Code: "migration.operation.none", AllowedActions: []MigrationRecoveryAction{},
		},
		Warnings: []MigrationNotice{}, Effects: []MigrationEffectProjection{},
	}
	if recovery {
		projection.State = MigrationPhaseRecoverableFailure
		projection.PhaseLabel = migrationPhaseLabel(projection.State)
		projection.Recovery = MigrationRecoveryProjection{
			Required: true, Code: migrationRecoveryCodeResume,
			AllowedActions: []MigrationRecoveryAction{MigrationRecoveryResume},
			NextAction:     migrationRecoveryNextAction(MigrationRecoveryResume),
		}
		projection.LastErrorCode = migrationRecoveryCodeResume
	}
	return projection
}

func operatorActivityFixture(
	t *testing.T,
	id, sessionID, environmentID, coverageID string,
	now time.Time,
) ActivityProjection {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(environmentID, "lima", "incarnation-fixture")
	if err != nil {
		t.Fatal(err)
	}
	record := ActivityProjection{
		Schema: workloadtypes.ActivityRecordSchema, ID: id, Owner: owner,
		SessionID: sessionID, Kind: workloadtypes.ActivityRisk, Operation: "risk.detected",
		Subject: workloadtypes.GenericSubject{Kind: workloadtypes.ActivityRisk, Code: "fixture"},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: now, LastAt: now, FirstSequence: 1, LastSequence: 1,
		Attribution: workloadtypes.AttributionExact, CoverageID: coverageID,
		RedactionStatus: workloadtypes.RedactionPassed,
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	return record
}

func operatorCoverageFixture(
	t *testing.T,
	id, sessionID, environmentID string,
	now time.Time,
) CoverageProjection {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(environmentID, "lima", "incarnation-fixture")
	if err != nil {
		t.Fatal(err)
	}
	interval := CoverageProjection{
		Schema: workloadtypes.CoverageIntervalSchema, ID: id, Owner: owner,
		SessionID: sessionID, Subsystem: workloadtypes.SubsystemFile,
		State: workloadtypes.CoverageAvailable, Reason: "observer-ready",
		CollectorGeneration: 1, StartedAt: now,
	}
	if err := interval.Validate(); err != nil {
		t.Fatal(err)
	}
	return interval
}

func capabilityState(values []OperatorCapabilityProjection, id string) string {
	for _, value := range values {
		if value.ID == id {
			return value.State
		}
	}
	return ""
}
