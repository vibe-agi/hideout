package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

func TestOperatorSnapshotRouteCoversHealthyIdleConcurrentBlockedAndScopedViews(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	scenarios := []struct {
		name         string
		path         string
		health       string
		sessions     []OperatorSessionProjection
		risks        []RiskFinding
		capabilities []OperatorCapabilityProjection
		wantSessions int
		wantProfile  string
		wantSession  string
	}{
		{
			name: "healthy", path: "/api/v1/operator/snapshot",
			health: "live",
			sessions: []OperatorSessionProjection{{
				ID: "ses_healthy", Profile: "default", State: "running", Command: "claude",
			}},
			wantSessions: 1,
		},
		{
			name: "idle", path: "/api/v1/operator/snapshot",
			health: "idle-live", wantSessions: 0,
		},
		{
			name: "concurrent", path: "/api/v1/operator/snapshot",
			health: "live",
			sessions: []OperatorSessionProjection{
				{ID: "ses_alpha", Profile: "alpha", State: "running", Command: "claude"},
				{ID: "ses_beta", Profile: "beta", State: "running", Command: "codex"},
			},
			wantSessions: 2,
		},
		{
			name: "blocked", path: "/api/v1/operator/snapshot",
			health: "live",
			sessions: []OperatorSessionProjection{{
				ID: "ses_blocked", Profile: "default", State: "blocked", Command: "claude",
			}},
			risks: []RiskFinding{{
				ID: "risk_fixture0001", RuleID: "coverage.missing", RuleVersion: "v1",
				Severity: "high", Title: "File activity unavailable", Confidence: "limited",
			}},
			capabilities: []OperatorCapabilityProjection{{
				ID: "activity.file", State: "unavailable", Reason: "collector unavailable",
			}},
			wantSessions: 1,
		},
		{
			name: "scoped", path: "/api/v1/operator/snapshot?profile=alpha&session=ses_alpha&activityLimit=25",
			health: "live",
			sessions: []OperatorSessionProjection{
				{ID: "ses_alpha", Profile: "alpha", State: "running", Command: "claude"},
				{ID: "ses_beta", Profile: "beta", State: "running", Command: "codex"},
			},
			wantSessions: 1, wantProfile: "alpha", wantSession: "ses_alpha",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			var observed OperatorSnapshotQuery
			api := API{
				Token:     "ui_token",
				ExpiresAt: now.Add(time.Hour),
				Now:       func() time.Time { return now },
				OperatorSnapshotProvider: OperatorSnapshotProviderFunc(
					func(_ context.Context, query OperatorSnapshotQuery) (OperatorSnapshot, error) {
						observed = query
						sessions := slices.Clone(scenario.sessions)
						if query.Profile != "" {
							sessions = slices.DeleteFunc(sessions, func(session OperatorSessionProjection) bool {
								return session.Profile != query.Profile
							})
						}
						if query.Session != "" {
							sessions = slices.DeleteFunc(sessions, func(session OperatorSessionProjection) bool {
								return session.ID != query.Session
							})
						}
						return OperatorSnapshot{
							Schema: OperatorSnapshotSchema, GeneratedAt: now,
							InstanceID: "daemon_fixture", CredentialGeneration: 4, Sequence: 8,
							StreamHealth: OperatorStreamHealth{State: scenario.health},
							Profiles:     []ProfileProjection{}, Sessions: sessions,
							Activity: []ActivityProjection{}, Coverage: []CoverageProjection{},
							Risks: slices.Clone(scenario.risks), Operations: []Operation{},
							Capabilities: slices.Clone(scenario.capabilities), NextActions: []string{},
						}, nil
					},
				),
			}
			request := newAPIRequest(http.MethodGet, scenario.path)
			request.Header.Set("Authorization", "Bearer ui_token")
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var envelope struct {
				Version  string           `json:"version"`
				Resource string           `json:"resource"`
				Data     OperatorSnapshot `json:"data"`
				Errors   []string         `json:"errors"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Version != APIVersion || envelope.Resource != "operator/snapshot" ||
				envelope.Data.Schema != OperatorSnapshotSchema || len(envelope.Errors) != 0 {
				t.Fatalf("snapshot envelope=%+v", envelope)
			}
			if len(envelope.Data.Sessions) != scenario.wantSessions ||
				envelope.Data.StreamHealth.State != scenario.health {
				t.Fatalf("snapshot scenario mismatch: %+v", envelope.Data)
			}
			if observed.Profile != scenario.wantProfile || observed.Session != scenario.wantSession {
				t.Fatalf("query=%+v want profile=%q session=%q", observed, scenario.wantProfile, scenario.wantSession)
			}
			wantLimit := 100
			if scenario.name == "scoped" {
				wantLimit = 25
			}
			if observed.ActivityLimit != wantLimit {
				t.Fatalf("activityLimit=%d want %d", observed.ActivityLimit, wantLimit)
			}
		})
	}
}

func TestOperatorSnapshotRouteRejectsInvalidScopeAndLimitBeforeProvider(t *testing.T) {
	called := false
	api := API{
		Token:     "ui_token",
		ExpiresAt: time.Now().Add(time.Hour),
		OperatorSnapshotProvider: OperatorSnapshotProviderFunc(
			func(context.Context, OperatorSnapshotQuery) (OperatorSnapshot, error) {
				called = true
				return OperatorSnapshot{}, nil
			},
		),
	}
	for _, path := range []string{
		"/api/v1/operator/snapshot?profile=bad%2Fname",
		"/api/v1/operator/snapshot?session=not-a-session",
		"/api/v1/operator/snapshot?activityLimit=-1",
		"/api/v1/operator/snapshot?activityLimit=501",
		"/api/v1/operator/snapshot?activityLimit=not-a-number",
		"/api/v1/operator/snapshot?unknown=value",
	} {
		request := newAPIRequest(http.MethodGet, path)
		request.Header.Set("Authorization", "Bearer ui_token")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s response is cacheable", path)
		}
	}
	if called {
		t.Fatal("invalid query reached the authoritative snapshot provider")
	}
}

func TestProfileProjectionRouteUsesAuthoritativeOperatorProjection(
	t *testing.T,
) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	projection := operatorProfileProjectionFixture("default", now)
	projection.Effective = ProfileEffective{
		Status: EffectiveCurrent,
		Network: &EffectiveNetwork{
			Mode:       "direct",
			DNS:        "system",
			ObservedAt: now,
		},
		Sessions: []EffectiveSessionSnapshot{},
	}
	var observed OperatorSnapshotQuery
	api := API{
		Token:     "ui_token",
		ExpiresAt: now.Add(time.Hour),
		Now:       func() time.Time { return now },
		OperatorSnapshotProvider: OperatorSnapshotProviderFunc(
			func(
				_ context.Context,
				query OperatorSnapshotQuery,
			) (OperatorSnapshot, error) {
				observed = query
				return OperatorSnapshot{
					Schema: OperatorSnapshotSchema,
					Profiles: []ProfileProjection{
						projection,
					},
				}, nil
			},
		),
	}
	request := newAPIRequest(
		http.MethodGet,
		"/api/v1/profiles/default/projection",
	)
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	var envelope struct {
		Resource string            `json:"resource"`
		Data     ProfileProjection `json:"data"`
	}
	if err := json.Unmarshal(
		response.Body.Bytes(),
		&envelope,
	); err != nil {
		t.Fatal(err)
	}
	if envelope.Resource != "profile/projection" ||
		envelope.Data.Effective.Status != EffectiveCurrent ||
		envelope.Data.Effective.Network == nil ||
		envelope.Data.Effective.Network.Mode != "direct" ||
		observed.Profile != "default" ||
		observed.Session != "" ||
		observed.ActivityLimit != 0 {
		t.Fatalf(
			"projection parity envelope=%+v query=%+v",
			envelope,
			observed,
		)
	}
}

func TestOperatorSnapshotRouteIsRegisteredAsReadOnlyPrivateManagerAuthority(t *testing.T) {
	spec, ok := RecognizeManagerRoute(http.MethodGet, "/api/v1/operator/snapshot")
	if !ok {
		t.Fatal("operator snapshot route is not in Manager inventory")
	}
	if spec.Resource != "operator/snapshot" || !spec.NoStore || !spec.NoBodyLog ||
		spec.Sensitive || spec.MaxRequestBodyBytes != 0 {
		t.Fatalf("operator snapshot route metadata=%+v", spec)
	}
	if _, ok := RecognizeManagerRoute(http.MethodPost, "/api/v1/operator/snapshot"); ok {
		t.Fatal("operator snapshot route unexpectedly authorizes POST")
	}
}
