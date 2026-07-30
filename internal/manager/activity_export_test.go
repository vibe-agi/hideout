package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/decision"
	exportboundary "github.com/vibe-agi/hideout/internal/export"
	"github.com/vibe-agi/hideout/internal/profile"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestActivityExportPreservesLocalViewButReviewsAndRedactsArtifact(t *testing.T) {
	now := time.Date(2026, 7, 29, 16, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_export", "lima", "incarnation-export-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	record := activityExportProcessRecord(t, owner, now)
	page := ActivityEventsPage{
		Records: []workloadtypes.ActivityRecord{
			record,
			activityExportFileRecord(t, owner, now.Add(time.Second)),
		},
		Coverage: []workloadtypes.CoverageInterval{},
	}
	provider := activityExportProvider(owner, &page)
	core := New(profile.Store{Root: t.TempDir()})
	service := ActivityExportService{
		Core: core, Provider: provider, Now: func() time.Time { return now },
	}

	// Authenticated local activity remains useful for investigation. Planning an
	// export must not mutate the provider's retained record or its local view.
	api := API{
		Core: core, Token: "ui_token", ExpiresAt: now.Add(time.Hour),
		Now: func() time.Time { return now }, ActivityProvider: provider,
	}
	localRequest := newAPIRequest(
		http.MethodGet,
		"/api/v1/activity/events?environment=env_export&incarnation=incarnation-export-a",
	)
	localRequest.Header.Set("Authorization", "Bearer ui_token")
	localResponse := httptest.NewRecorder()
	api.ServeHTTP(localResponse, localRequest)
	if localResponse.Code != http.StatusOK ||
		!strings.Contains(localResponse.Body.String(), "/Users/alice/private/project") {
		t.Fatalf(
			"authenticated local view lost the full path: status=%d body=%s",
			localResponse.Code,
			localResponse.Body.String(),
		)
	}

	out := filepath.Join(t.TempDir(), "activity.json")
	plan, err := service.Plan(context.Background(), ActivityExportDraft{
		Schema: ActivityExportDraftSchema,
		Owner: ActivityOwnerSelector{
			EnvironmentID:        owner.EnvironmentID,
			BackendIncarnationID: owner.BackendIncarnationID,
		},
		Limit:                   100,
		Out:                     out,
		AcknowledgeFullFidelity: true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.PathPolicy != ActivityExportPathRedactHost ||
		plan.RecordCount != 2 ||
		plan.Destination != out ||
		plan.Review.Source != exportboundary.SourceActivity ||
		plan.Review.Decision.Mode != exportboundary.DecisionAcknowledgeFullFidelity ||
		!activityExportHasStage(plan.Review.RedactionStages, exportboundary.PublicEvidenceLocalPathStage) {
		t.Fatalf("review does not disclose the bounded path policy: %+v", plan)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planning published an artifact: %v", err)
	}
	if got := page.Records[0].Subject.(workloadtypes.ProcessSubject).Cwd; got != "/Users/alice/private/project" {
		t.Fatalf("planning mutated retained local evidence: %q", got)
	}

	result, err := service.Apply(context.Background(), ActivityExportApplyRequest{
		Schema: ActivityExportApplySchema, Plan: plan, Confirmed: true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Export.ArtifactPath != out || result.Export.RecordCount != 2 {
		t.Fatalf("result=%+v", result)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode=%#o want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{
		"/Users/alice/private/project",
		"/mnt/private-host-mount/customer/project.txt",
		"alice:password",
		"plain-password",
		"cap_0123456789abcdef0123456789abcdef",
		"token=unclassified-token",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("activity export leaked %q:\n%s", forbidden, text)
		}
	}
	for _, expected := range []string{
		"redacted:local-path",
		"[REDACTED]",
		"unknown-business-value",
		`"source": "activity"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("activity export is missing %q:\n%s", expected, text)
		}
	}
	var artifact exportboundary.Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.RecordCount != 2 ||
		artifact.Provenance.Source != exportboundary.SourceActivity ||
		!activityExportHasStage(
			artifact.Provenance.RedactionStages,
			exportboundary.PublicEvidenceLocalPathStage,
		) {
		t.Fatalf("artifact provenance=%+v", artifact.Provenance)
	}
}

func TestActivityExportSelectedUserDataUsesExistingPolicyReview(t *testing.T) {
	now := time.Date(2026, 7, 29, 16, 10, 0, 0, time.UTC)
	owner, err := workloadtypes.NewDisposableOwner(
		"ses_20260729T161000Z_export", "lima", "incarnation-export-policy",
	)
	if err != nil {
		t.Fatal(err)
	}
	store := profile.Store{Root: t.TempDir()}
	writeManagerExportProfile(t, store, "default", `
function redactAudit(ctx) {
  const details = ctx.details;
  const selected = ctx.extra.exportRedaction || [];
  if (selected.indexOf("domain") >= 0 && details.records) {
    for (let i = 0; i < details.records.length; i++) {
      const subject = details.records[i].subject;
      if (subject && subject.domain) subject.domain = "REDACTED_BY_POLICY";
    }
  }
  return { details };
}`)
	record := activityExportNetworkRecord(t, owner, now, "private.customer.example")
	page := ActivityEventsPage{
		Records:  []workloadtypes.ActivityRecord{record},
		Coverage: []workloadtypes.CoverageInterval{},
	}
	service := ActivityExportService{
		Core: New(store), Provider: activityExportProvider(owner, &page),
		Now: func() time.Time { return now },
	}
	out := filepath.Join(t.TempDir(), "selected.json")
	plan, err := service.Plan(context.Background(), ActivityExportDraft{
		Schema: ActivityExportDraftSchema,
		Owner:  ActivityOwnerSelector{SessionID: owner.SessionID},
		Limit:  100, Out: out,
		Redact: []string{"domain"}, PolicyProfile: "default",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Review.DecisionRequired ||
		plan.Review.Decision.Mode != exportboundary.DecisionRedact ||
		!activityExportHasStage(plan.Review.RedactionStages, "audit.redact") {
		t.Fatalf("selected-data review=%+v", plan.Review)
	}
	if _, err := service.Apply(context.Background(), ActivityExportApplyRequest{
		Schema: ActivityExportApplySchema, Plan: plan, Confirmed: true,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private.customer.example") ||
		!strings.Contains(string(data), "REDACTED_BY_POLICY") {
		t.Fatalf("selected user data was not redacted:\n%s", data)
	}
}

func TestActivityExportPreservePathPolicyRequiresExplicitLocalAcknowledgement(t *testing.T) {
	now := time.Date(2026, 7, 29, 16, 5, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_export", "lima", "incarnation-export-preserve",
	)
	if err != nil {
		t.Fatal(err)
	}
	page := ActivityEventsPage{
		Records: []workloadtypes.ActivityRecord{
			activityExportProcessRecord(t, owner, now),
		},
		Coverage: []workloadtypes.CoverageInterval{},
	}
	service := ActivityExportService{
		Core:     New(profile.Store{Root: t.TempDir()}),
		Provider: activityExportProvider(owner, &page),
		Now:      func() time.Time { return now },
	}
	base := ActivityExportDraft{
		Schema: ActivityExportDraftSchema,
		Owner: ActivityOwnerSelector{
			EnvironmentID:        owner.EnvironmentID,
			BackendIncarnationID: owner.BackendIncarnationID,
		},
		Limit: 100, Out: filepath.Join(t.TempDir(), "preserve.json"),
		PathPolicy: ActivityExportPathPreserve,
	}
	if _, err := service.Plan(
		context.Background(),
		base,
	); !errors.Is(err, ErrInvalidActivityExportDraft) {
		t.Fatalf("unacknowledged preserve policy error=%v", err)
	}
	base.AcknowledgeFullFidelity = true
	base.Share = true
	if _, err := service.Plan(
		context.Background(),
		base,
	); !errors.Is(err, ErrInvalidActivityExportDraft) {
		t.Fatalf("shared preserve policy error=%v", err)
	}
	base.Share = false
	plan, err := service.Plan(context.Background(), base)
	if err != nil {
		t.Fatalf("Plan preserve: %v", err)
	}
	if activityExportHasStage(
		plan.Review.RedactionStages,
		exportboundary.PublicEvidenceLocalPathStage,
	) {
		t.Fatalf("preserve plan falsely claimed path redaction: %+v", plan.Review)
	}
	if _, err := service.Apply(context.Background(), ActivityExportApplyRequest{
		Schema: ActivityExportApplySchema, Plan: plan, Confirmed: true,
	}); err != nil {
		t.Fatalf("Apply preserve: %v", err)
	}
	data, err := os.ReadFile(base.Out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/Users/alice/private/project") {
		t.Fatalf("explicit preserve policy did not preserve reviewed path:\n%s", data)
	}
}

func TestActivityExportApplyRejectsUnconfirmedTamperedAndStalePlans(t *testing.T) {
	now := time.Date(2026, 7, 29, 16, 20, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_export", "lima", "incarnation-export-stale",
	)
	if err != nil {
		t.Fatal(err)
	}
	page := ActivityEventsPage{
		Records: []workloadtypes.ActivityRecord{
			activityExportNetworkRecord(t, owner, now, "first.example"),
		},
		Coverage: []workloadtypes.CoverageInterval{},
	}
	service := ActivityExportService{
		Core:     New(profile.Store{Root: t.TempDir()}),
		Provider: activityExportProvider(owner, &page),
		Now:      func() time.Time { return now },
	}
	out := filepath.Join(t.TempDir(), "stale.json")
	plan, err := service.Plan(context.Background(), ActivityExportDraft{
		Schema: ActivityExportDraftSchema,
		Owner: ActivityOwnerSelector{
			EnvironmentID:        owner.EnvironmentID,
			BackendIncarnationID: owner.BackendIncarnationID,
		},
		Limit: 100, Out: out, AcknowledgeFullFidelity: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Apply(context.Background(), ActivityExportApplyRequest{
		Schema: ActivityExportApplySchema, Plan: plan,
	}); !errors.Is(err, ErrActivityExportConfirmationRequired) {
		t.Fatalf("unconfirmed error=%v", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unconfirmed apply wrote output: %v", err)
	}

	tampered := plan
	tampered.Destination = filepath.Join(t.TempDir(), "tampered.json")
	if _, err := service.Apply(context.Background(), ActivityExportApplyRequest{
		Schema: ActivityExportApplySchema, Plan: tampered, Confirmed: true,
	}); !errors.Is(err, ErrInvalidActivityExportPlan) {
		t.Fatalf("tampered plan error=%v", err)
	}
	if _, err := os.Stat(tampered.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tampered plan wrote output: %v", err)
	}

	page.Records[0] = activityExportNetworkRecord(
		t, owner, now.Add(time.Second), "second.example",
	)
	if _, err := service.Apply(context.Background(), ActivityExportApplyRequest{
		Schema: ActivityExportApplySchema, Plan: plan, Confirmed: true,
	}); !errors.Is(err, ErrStaleActivityExportPlan) {
		t.Fatalf("source drift error=%v", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale apply wrote output: %v", err)
	}
}

func TestActivityExportApplyTreatsLifecycleCleanupAsStale(t *testing.T) {
	now := time.Date(2026, 7, 29, 16, 25, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_export", "lima", "incarnation-export-cleanup-race",
	)
	if err != nil {
		t.Fatal(err)
	}
	page := ActivityEventsPage{
		Records: []workloadtypes.ActivityRecord{
			activityExportNetworkRecord(t, owner, now, "before-cleanup.example"),
		},
		Coverage: []workloadtypes.CoverageInterval{},
	}
	ownerPresent := true
	provider := activityAPIProvider{
		resolveOwner: func(
			_ context.Context,
			selector ActivityOwnerSelector,
		) (workloadtypes.ActivityOwner, error) {
			if !ownerPresent || !ownerMatchesSelector(owner, selector) {
				return workloadtypes.ActivityOwner{}, ErrActivityOwnerNotFound
			}
			return owner, nil
		},
		events: func(
			_ context.Context,
			query ActivityEventsQuery,
		) (ActivityEventsPage, error) {
			if !ownerPresent || !query.Owner.Equal(owner) {
				return ActivityEventsPage{}, ErrActivityOwnerNotFound
			}
			return page, nil
		},
	}
	service := ActivityExportService{
		Core:     New(profile.Store{Root: t.TempDir()}),
		Provider: provider,
		Now:      func() time.Time { return now },
	}
	out := filepath.Join(t.TempDir(), "cleanup-race.json")
	plan, err := service.Plan(context.Background(), ActivityExportDraft{
		Schema: ActivityExportDraftSchema,
		Owner: ActivityOwnerSelector{
			EnvironmentID:        owner.EnvironmentID,
			BackendIncarnationID: owner.BackendIncarnationID,
		},
		Limit: 100, Out: out, AcknowledgeFullFidelity: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Lifecycle cleanup removes the exact source after review but before apply.
	// Apply must not reinterpret absence as an empty reviewed export.
	ownerPresent = false
	if _, err := service.Apply(context.Background(), ActivityExportApplyRequest{
		Schema: ActivityExportApplySchema, Plan: plan, Confirmed: true,
	}); !errors.Is(err, ErrStaleActivityExportPlan) {
		t.Fatalf("cleanup race error=%v want stale plan", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup race wrote output: %v", err)
	}
}

func TestActivityExportShareRequiresExistingDecisionAndDoesNotPublishOnApply(t *testing.T) {
	now := time.Date(2026, 7, 29, 16, 30, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_export", "lima", "incarnation-export-share",
	)
	if err != nil {
		t.Fatal(err)
	}
	page := ActivityEventsPage{
		Records: []workloadtypes.ActivityRecord{
			activityExportProcessRecord(t, owner, now),
		},
		Coverage: []workloadtypes.CoverageInterval{},
	}
	core := New(profile.Store{Root: t.TempDir()})
	service := ActivityExportService{
		Core: core, Provider: activityExportProvider(owner, &page),
		Now: func() time.Time { return now },
	}
	out := filepath.Join(t.TempDir(), "share.json")
	plan, err := service.Plan(context.Background(), ActivityExportDraft{
		Schema: ActivityExportDraftSchema,
		Owner: ActivityOwnerSelector{
			EnvironmentID:        owner.EnvironmentID,
			BackendIncarnationID: owner.BackendIncarnationID,
		},
		Limit: 100, Out: out, AcknowledgeFullFidelity: true, Share: true,
	})
	if err != nil {
		t.Fatalf("Plan share: %v", err)
	}
	if plan.DecisionID == "" || !plan.Share ||
		plan.PathPolicy != ActivityExportPathRedactHost {
		t.Fatalf("share plan=%+v", plan)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("share plan released output: %v", err)
	}
	if _, err := service.Apply(context.Background(), ActivityExportApplyRequest{
		Schema: ActivityExportApplySchema, Plan: plan, Confirmed: true,
	}); !errors.Is(err, ErrActivityExportShareApprovalRequired) {
		t.Fatalf("share apply bypass error=%v", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("share apply bypass released output: %v", err)
	}

	claim, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: plan.DecisionID, ExpectedVersion: decision.DecisionVersion,
		Surface: "cli",
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	resolution, err := core.ApproveDecision(DecisionResolveRequest{
		DecisionID: plan.DecisionID, ExpectedVersion: decision.DecisionVersion,
		ClaimToken: claim.ClaimToken, Reason: "reviewed activity share",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if resolution.Status != "applied" || resolution.Decision != "allow" {
		t.Fatalf("resolution=%+v", resolution)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/Users/alice/private/project") {
		t.Fatalf("approved share ignored host-path policy:\n%s", data)
	}

	_, err = service.Plan(context.Background(), ActivityExportDraft{
		Schema: ActivityExportDraftSchema,
		Owner: ActivityOwnerSelector{
			EnvironmentID:        owner.EnvironmentID,
			BackendIncarnationID: owner.BackendIncarnationID,
		},
		Limit: 100, Out: "https://uploads.example/export.json",
		AcknowledgeFullFidelity: true, Share: true,
	})
	if !errors.Is(err, ErrInvalidActivityExportDraft) {
		t.Fatalf("remote publication destination error=%v", err)
	}
}

func TestActivityExportAPIRoutesRequirePlanThenExplicitApply(t *testing.T) {
	now := time.Date(2026, 7, 29, 16, 40, 0, 0, time.UTC)
	owner, err := workloadtypes.NewDisposableOwner(
		"ses_20260729T164000Z_export", "lima", "incarnation-export-api",
	)
	if err != nil {
		t.Fatal(err)
	}
	page := ActivityEventsPage{
		Records: []workloadtypes.ActivityRecord{
			activityExportNetworkRecord(t, owner, now, "api.example"),
		},
		Coverage: []workloadtypes.CoverageInterval{},
	}
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "api.json")
	api := API{
		Core:  New(profile.Store{Root: root}),
		Token: "ui_token", ExpiresAt: now.Add(time.Hour),
		Now:              func() time.Time { return now },
		ActivityProvider: activityExportProvider(owner, &page),
	}
	planRequest := ActivityExportAPIRequest{Draft: &ActivityExportDraft{
		Schema: ActivityExportDraftSchema,
		Owner:  ActivityOwnerSelector{SessionID: owner.SessionID},
		Limit:  100, Out: out, AcknowledgeFullFidelity: true,
	}}
	request := newAPIJSONRequest(
		http.MethodPost, "/api/v1/activity/export/plan", planRequest,
	)
	request.Header.Set("Authorization", "Bearer ui_token")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("plan status=%d body=%s", response.Code, response.Body.String())
	}
	var planEnvelope struct {
		Data ActivityExportPlan `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &planEnvelope); err != nil {
		t.Fatal(err)
	}
	if planEnvelope.Data.PlanDigest == "" {
		t.Fatalf("plan response=%s", response.Body.String())
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("API plan wrote output: %v", err)
	}

	applyRequest := ActivityExportAPIRequest{
		Plan: &planEnvelope.Data, Confirmed: true,
	}
	request = newAPIJSONRequest(
		http.MethodPost, "/api/v1/activity/export/apply", applyRequest,
	)
	request.Header.Set("Authorization", "Bearer ui_token")
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"artifactPath"`) {
		t.Fatalf("apply status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("API apply did not write reviewed output: %v", err)
	}

	for _, resource := range []string{
		"activity/export/plan", "activity/export/apply",
	} {
		spec, ok := RecognizeManagerResource(http.MethodPost, resource)
		if !ok || !spec.Sensitive || !spec.NoStore || !spec.NoBodyLog {
			t.Fatalf("%s route metadata=%+v ok=%v", resource, spec, ok)
		}
	}
}

func activityExportProvider(
	owner workloadtypes.ActivityOwner,
	page *ActivityEventsPage,
) activityAPIProvider {
	return activityAPIProvider{
		resolveOwner: func(
			_ context.Context,
			selector ActivityOwnerSelector,
		) (workloadtypes.ActivityOwner, error) {
			if !ownerMatchesSelector(owner, selector) {
				return workloadtypes.ActivityOwner{}, ErrActivityOwnerNotFound
			}
			return owner, nil
		},
		events: func(
			_ context.Context,
			query ActivityEventsQuery,
		) (ActivityEventsPage, error) {
			if !query.Owner.Equal(owner) {
				return ActivityEventsPage{}, ErrActivityOwnerNotFound
			}
			return *page, nil
		},
	}
}

func activityExportProcessRecord(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	at time.Time,
) workloadtypes.ActivityRecord {
	t.Helper()
	record := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		ID:     "act_activityexport01", Owner: owner,
		SessionID: "ses_20260729T160000Z_activity",
		Actor: &workloadtypes.Actor{
			ExecutionID: "exec_activityexport01",
			PID:         42, UID: 1000, GID: 1000,
		},
		Kind: workloadtypes.ActivityProcess, Operation: "exec",
		Subject: workloadtypes.ProcessSubject{
			Kind:        workloadtypes.ActivityProcess,
			ExecutionID: "exec_activityexport01",
			Executable:  "/Users/alice/private/project/bin/claude",
			Argv: []string{
				"claude",
				"https://alice:password@example.test/v1?token=unclassified-token",
				"--password", "plain-password",
				"cap_0123456789abcdef0123456789abcdef",
				"unknown-business-value",
			},
			Cwd: "/Users/alice/private/project",
			GuestIdentity: workloadtypes.GuestIdentity{
				UID: 1000, GID: 1000, User: "developer",
			},
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: 1, LastSequence: 1,
		Attribution:     workloadtypes.AttributionExact,
		CoverageID:      "cov_activityexport01",
		RedactionStatus: workloadtypes.RedactionPassed,
	}
	if err := record.ValidatePersistable(); err != nil {
		t.Fatal(err)
	}
	return record
}

func activityExportNetworkRecord(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	at time.Time,
	domain string,
) workloadtypes.ActivityRecord {
	t.Helper()
	record := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		ID:     "act_activityexport02", Owner: owner,
		SessionID: owner.SessionID,
		Actor: &workloadtypes.Actor{
			ExecutionID: "exec_activityexport02",
			PID:         43, UID: 1000, GID: 1000,
		},
		Kind: workloadtypes.ActivityConnection, Operation: "connect",
		Subject: workloadtypes.NetworkSubject{
			Kind:     workloadtypes.ActivityConnection,
			Protocol: "tcp", IP: "203.0.113.20", Port: 443,
			Domain: domain, DomainAttribution: workloadtypes.AttributionExact,
			CorrelationReason: "socket", Route: "direct", Direction: "egress",
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: 2, LastSequence: 2,
		Attribution:     workloadtypes.AttributionExact,
		CoverageID:      "cov_activityexport02",
		RedactionStatus: workloadtypes.RedactionPassed,
	}
	if owner.Kind == workloadtypes.OwnerReusableEnvironment {
		record.SessionID = "ses_20260729T160000Z_activity"
	}
	if err := record.ValidatePersistable(); err != nil {
		t.Fatal(err)
	}
	return record
}

func activityExportFileRecord(
	t *testing.T,
	owner workloadtypes.ActivityOwner,
	at time.Time,
) workloadtypes.ActivityRecord {
	t.Helper()
	record := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		ID:     "act_activityexport03", Owner: owner,
		SessionID: "ses_20260729T160000Z_activity",
		Actor: &workloadtypes.Actor{
			ExecutionID: "exec_activityexport01",
			PID:         42, UID: 1000, GID: 1000,
		},
		Kind: workloadtypes.ActivityFile, Operation: "write",
		Subject: workloadtypes.FileSubject{
			Kind:      workloadtypes.ActivityFile,
			Path:      "/mnt/private-host-mount/customer/project.txt",
			PathState: "resolved", PathClass: "hostfs", FileType: "regular",
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: 3, LastSequence: 3,
		Attribution:     workloadtypes.AttributionExact,
		CoverageID:      "cov_activityexport03",
		RedactionStatus: workloadtypes.RedactionPassed,
	}
	if err := record.ValidatePersistable(); err != nil {
		t.Fatal(err)
	}
	return record
}

func activityExportHasStage(
	stages []exportboundary.RedactionStage,
	name string,
) bool {
	for _, stage := range stages {
		if stage.Name == name {
			return true
		}
	}
	return false
}
