package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestMigrationExportCLIRendersSharedGoldenPlan(t *testing.T) {
	encoded, err := os.ReadFile("../migration/testdata/export-plan-surface-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var plan migration.ExportPlan
	if err := json.Unmarshal(encoded, &plan); err != nil {
		t.Fatal(err)
	}
	if err := manager.VerifyMigrationExportPlan(plan); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writeMigrationExportPlan(&output, plan, []string{"dev"})
	for _, expected := range []string{
		"Included: environment-declarations, persistent-disks, portable-profiles, profile-application-state",
		"Payload estimate: 9728 bytes (complete logical payload)",
		"Environment dev (environment_source1): 9728 bytes; portable config=1024 bytes; profile state=512 bytes",
		"Disk disk_root0000001 (root): logical=8192 bytes",
		"used by=environment_source1",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("CLI golden plan omitted %q:\n%s", expected, output.String())
		}
	}
}

func TestMigrationInspectionCLIExposesProfileStateAndSensitivityWarning(t *testing.T) {
	inspection := manager.MigrationReadOnlyInspection{
		Inventory: manager.MigrationBundleInspectionProjection{
			BundleID: "migb_cliinspection01", FormatVersion: migration.BundleFormatVersion,
			Sealed: true, EncodedBytes: 1024, LogicalBytes: 512,
			Source: manager.MigrationBundleSourceProjection{
				ProductVersion: "v1.2.3", HostOS: "darwin", HostArch: "arm64",
				Backend: "lima", BackendVersion: "1.0.0",
			},
			Environments: []manager.MigrationBundleEnvironmentProjection{{
				SourceRef: "environment_source1", DisplayNameHint: "dev",
				DiskIDs: []migration.OpaqueID{"disk_root0000001"},
			}},
			ExcludedClasses: []string{"host-workspace-content"},
			Components: manager.MigrationBundleComponentCounts{
				Profiles: 1, ProfileStates: 1, Environments: 1, Disks: 1, Total: 4,
			},
			Warnings: []migration.PlanNotice{{
				Code:    "migration.bundle.full_state_may_contain_secrets",
				Summary: "Full state can contain credentials.",
			}},
		},
	}
	var output bytes.Buffer
	writeMigrationInspection(&output, inspection)
	for _, expected := range []string{
		"profile application states=1", "Environment: dev", "disks=1",
		"Warning [migration.bundle.full_state_may_contain_secrets]",
		"Full state can contain credentials.",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("inspection omitted %q:\n%s", expected, output.String())
		}
	}
}

func TestMigrateExportFullPreviewShowsSeparateQuiescencePlan(t *testing.T) {
	t.Setenv("HIDEOUT_STORE_ROOT", t.TempDir())
	var output bytes.Buffer
	plan := migrationCLIStopPlanFixture()
	calls := 0
	a := app{
		stdout: &output, stderr: io.Discard, stdin: strings.NewReader(""),
		migrationRequest: func(_ string, method, path string, body io.Reader) ([]byte, error) {
			calls++
			switch calls {
			case 1:
				if method != http.MethodGet || path != "/api/v1/environments" || body != nil {
					t.Fatalf("inventory method=%s path=%s body=%v", method, path, body)
				}
				return migrationCLIEnvelope(t, "environments", []manager.EnvironmentSummary{{
					ID: plan.Targets[0].ID, Name: "dev", Status: "running",
				}}), nil
			case 2:
				var request manager.EnvironmentActionAPIRequest
				decodeMigrationCLIRequest(t, body, &request)
				if method != http.MethodPost || path != "/api/v1/environment/stop/plan" ||
					len(request.IDs) != 1 || request.IDs[0] != plan.Targets[0].ID {
					t.Fatalf("quiescence request method=%s path=%s request=%+v", method, path, request)
				}
				return migrationCLIEnvelope(t, "environment/stop/plan", plan), nil
			default:
				t.Fatalf("preview reached mutating/export call %d", calls)
				return nil, nil
			}
		},
	}
	if err := a.migrateExport([]string{
		"--environment", "dev", "--out", "dev.bundle", "--preview",
		"--ack-guest-content",
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !strings.Contains(output.String(), "Migration VM stop preview") ||
		!strings.Contains(output.String(), "--stop") ||
		!strings.Contains(output.String(), "hideoutd remains running") {
		t.Fatalf("calls=%d output=%q", calls, output.String())
	}
}

func TestMigrateExportYesCannotImplicitlyAuthorizeVMStop(t *testing.T) {
	t.Setenv("HIDEOUT_STORE_ROOT", t.TempDir())
	plan := migrationCLIStopPlanFixture()
	calls := 0
	a := app{
		stdout: io.Discard, stderr: io.Discard, stdin: strings.NewReader(""),
		migrationRequest: func(_ string, _ string, path string, _ io.Reader) ([]byte, error) {
			calls++
			if path == "/api/v1/environments" {
				return migrationCLIEnvelope(t, "environments", []manager.EnvironmentSummary{{
					ID: plan.Targets[0].ID, Name: "dev", Status: "running",
				}}), nil
			}
			if path == "/api/v1/environment/stop/plan" {
				return migrationCLIEnvelope(t, "environment/stop/plan", plan), nil
			}
			t.Fatalf("unexpected path=%s", path)
			return nil, nil
		},
	}
	err := a.migrateExport([]string{
		"--environment", "dev", "--out", "dev.bundle", "--yes",
		"--ack-guest-content",
	})
	if err == nil || !strings.Contains(err.Error(), "separate VM stop authorization") || calls != 2 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestMigrateExportStopAppliesExactReviewedOperationBeforeExportPlan(t *testing.T) {
	t.Setenv("HIDEOUT_STORE_ROOT", t.TempDir())
	plan := migrationCLIStopPlanFixture()
	terminal := migrationCLIStopOperationFixture(plan)
	stopResult := manager.EnvironmentActionResult{
		Plan: plan,
		Applied: []manager.EnvironmentActionTarget{{
			ID: plan.Targets[0].ID, Status: "stopped",
		}},
		Skipped: []manager.EnvironmentActionTarget{}, Operation: &terminal,
	}
	errAfterQuiescence := errors.New("export-plan-reached")
	calls := 0
	a := app{
		stdout: io.Discard, stderr: io.Discard, stdin: strings.NewReader(""),
		migrationRequest: func(_ string, method, path string, body io.Reader) ([]byte, error) {
			calls++
			switch calls {
			case 1:
				return migrationCLIEnvelope(t, "environments", []manager.EnvironmentSummary{{
					ID: plan.Targets[0].ID, Name: "dev", Status: "running",
				}}), nil
			case 2:
				return migrationCLIEnvelope(t, "environment/stop/plan", plan), nil
			case 3:
				var request manager.EnvironmentActionAPIRequest
				decodeMigrationCLIRequest(t, body, &request)
				if method != http.MethodPost || path != "/api/v1/environment/stop/apply" ||
					request.OperationID != plan.OperationID || request.PlanDigest != plan.PlanDigest ||
					!request.Confirmed || len(request.IDs) != 1 || request.IDs[0] != plan.Targets[0].ID {
					t.Fatalf("stop apply method=%s path=%s request=%+v", method, path, request)
				}
				return migrationCLIEnvelope(t, "environment/stop/apply", stopResult), nil
			case 4:
				if method != http.MethodPost || path != "/api/v1/migration/export/plan" {
					t.Fatalf("post-stop method=%s path=%s", method, path)
				}
				return nil, errAfterQuiescence
			default:
				t.Fatalf("unexpected call=%d path=%s", calls, path)
				return nil, nil
			}
		},
	}
	err := a.migrateExport([]string{
		"--environment", "dev", "--out", "dev.bundle", "--stop", "--yes",
		"--ack-guest-content",
	})
	if !errors.Is(err, errAfterQuiescence) || calls != 4 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func migrationCLIStopPlanFixture() manager.EnvironmentActionPlan {
	const environmentID = "env_migrationstopfixture1"
	return manager.EnvironmentActionPlan{
		OperationID: "op_migrationstopfixture1",
		PlanDigest:  "sha256:" + strings.Repeat("a", 64),
		Action:      manager.EnvironmentActionStop, RequestedIDs: []string{environmentID},
		Targets: []manager.EnvironmentActionTarget{{
			ID: environmentID, Profile: "default", Backend: "lima",
			Status: "running", InstanceName: "hideout-migration-stop-fixture",
		}},
		Skipped: []manager.EnvironmentActionTarget{}, Total: 1,
	}
}

func migrationCLIStopOperationFixture(plan manager.EnvironmentActionPlan) manager.Operation {
	now := time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)
	return manager.Operation{
		Schema: manager.OperationSchema, ID: plan.OperationID,
		Kind: "environment.stop", Owner: manager.OperationOwner{
			Kind: "environment", ID: plan.Targets[0].ID,
		},
		PlanDigest: plan.PlanDigest, Phase: manager.OperationSucceeded,
		Effects: []manager.EffectResult{{
			ID: "environment-0", Kind: "drain", Provider: "lima",
			Status: manager.EffectSucceeded,
		}},
		Result: &manager.OperationResult{Status: "succeeded"},
		Recovery: manager.Recovery{
			Code: "none", Summary: "The environment stop completed.",
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestMigrateExportConfigPreviewSendsExplicitScopeAndRendersExclusions(t *testing.T) {
	t.Setenv("HIDEOUT_STORE_ROOT", t.TempDir())
	var output bytes.Buffer
	calls := 0
	a := app{
		stdout: &output, stderr: io.Discard, stdin: strings.NewReader(""),
		migrationRequest: func(_ string, method, path string, body io.Reader) ([]byte, error) {
			calls++
			if method != http.MethodPost || path != "/api/v1/migration/export/plan" {
				t.Fatalf("request method=%s path=%s", method, path)
			}
			var request migration.ExportRequest
			decodeMigrationCLIRequest(t, body, &request)
			if request.Mode != migration.ExportModeConfig ||
				len(request.EnvironmentNames) != 1 || request.EnvironmentNames[0] != "dev" ||
				len(request.IncludeSecretRefs) != 0 || !strings.HasSuffix(request.OutputPath, "config.bundle") {
				t.Fatalf("config export request=%+v", request)
			}
			return migrationCLIEnvelope(t, "migration/export/plan", migration.ExportPlan{
				Mode: migration.ExportModeConfig, OutputPath: request.OutputPath,
				EnvironmentRefs: []migration.OpaqueID{"env_configpreview1"},
				DiskRefs:        []migration.OpaqueID{},
				IncludedClasses: []string{"environment-declarations", "portable-profiles"},
				ExcludedClasses: []string{"host-workspace-content", "unselected-secret-values"},
				EnvironmentEstimates: []migration.ExportEnvironmentEstimate{{
					EnvironmentRef: "env_configpreview1", DisplayName: "dev",
					PortableConfigLogicalBytes: 1024,
					PortableConfigDigest:       migration.Digest("sha256:" + strings.Repeat("d", 64)),
					DiskRefs:                   []migration.OpaqueID{},
					EstimatedLogicalBytes:      1024,
				}},
				DiskEstimates:                []migration.ExportDiskEstimate{},
				EstimatedPayloadLogicalBytes: 1024,
				EstimatedPayloadComplete:     true,
				Warnings:                     []migration.PlanNotice{},
				ConfirmationText:             "Export portable configuration only.",
			}), nil
		},
	}
	if err := a.migrateExport([]string{
		"--environment", "dev", "--out", "config.bundle", "--mode", "config", "--preview",
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(output.String(), "Migration export preview (config)") ||
		!strings.Contains(output.String(), "Persistent disks: 0") ||
		!strings.Contains(output.String(), "Environment dev (env_configpreview1): 1024 bytes") ||
		!strings.Contains(output.String(), "complete logical payload") ||
		!strings.Contains(output.String(), "unselected-secret-values") {
		t.Fatalf("calls=%d output=%q", calls, output.String())
	}
}

func TestMigrateImportApproveBindsAuthenticatedProposalValue(t *testing.T) {
	const proposalID = migration.OpaqueID("authority_network001")
	inspection := manager.MigrationReadOnlyInspection{
		Inventory: manager.MigrationBundleInspectionProjection{
			Environments: []manager.MigrationBundleEnvironmentProjection{{
				SourceRef: "environment_source1", DisplayNameHint: "dev-clone",
				AuthorityProposalIDs: []migration.OpaqueID{proposalID},
			}},
			AuthorityProposals: []manager.MigrationBundleAuthorityProjection{{
				ProposalID: proposalID, Class: "network",
				SourceSummary: `{"mode":"direct"}`, State: "disabled",
			}},
		},
	}
	options := migrateImportOptions{
		bundle:             t.TempDir() + "/bundle.hideout-migration",
		authorityApprovals: []string{string(proposalID)},
	}
	draft, err := migrationImportDraftFromCLI(inspection, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.AuthorityDecisions) != 1 ||
		draft.AuthorityDecisions[0].ProposalID != proposalID ||
		draft.AuthorityDecisions[0].Decision != "approved" ||
		draft.AuthorityDecisions[0].DestinationValue != `{"mode":"direct"}` {
		t.Fatalf("authority decision=%+v", draft.AuthorityDecisions)
	}

	options.authorityApprovals = []string{string(proposalID) + `={"mode":"direct","mediatedResolver":"1.1.1.1"}`}
	draft, err = migrationImportDraftFromCLI(inspection, options)
	if err != nil {
		t.Fatal(err)
	}
	if draft.AuthorityDecisions[0].DestinationValue !=
		`{"mode":"direct","mediatedResolver":"1.1.1.1"}` {
		t.Fatalf("explicit destination value=%q", draft.AuthorityDecisions[0].DestinationValue)
	}
}

func TestParseMigrateImportApproveIsRepeatableAndValueBearing(t *testing.T) {
	options, err := parseMigrateImportOptions([]string{
		"bundle.hideout-migration", "--approve", "authority_network001",
		"--approve", `authority_endpoint01={"expose":true}`, "--preview",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(options.authorityApprovals) != 2 ||
		options.authorityApprovals[0] != "authority_network001" ||
		options.authorityApprovals[1] != `authority_endpoint01={"expose":true}` {
		t.Fatalf("approvals=%v", options.authorityApprovals)
	}
}

func TestParseMigrateImportReplaceIsExplicitAndRepeatable(t *testing.T) {
	options, err := parseMigrateImportOptions([]string{
		"bundle.hideout-migration", "--replace", "environment_source1",
		"--replace", "environment_source2", "--preview",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(options.replacements, []string{
		"environment_source1", "environment_source2",
	}) {
		t.Fatalf("replacements=%v", options.replacements)
	}
}

func TestParseMigrateImportRequiresExplicitScopeForNonInteractiveApply(t *testing.T) {
	_, err := parseMigrateImportOptions([]string{
		"bundle.hideout-migration", "--yes",
	})
	if err == nil || !strings.Contains(err.Error(), "explicit scope") ||
		!strings.Contains(err.Error(), "--preview") ||
		!strings.Contains(err.Error(), "--environment <source-ref>") {
		t.Fatalf("implicit non-interactive scope error=%v", err)
	}

	selected, err := parseMigrateImportOptions([]string{
		"bundle.hideout-migration", "--environment", "environment_source1", "--yes",
	})
	if err != nil || selected.all ||
		!slices.Equal(selected.environments, []string{"environment_source1"}) {
		t.Fatalf("explicit selected scope=%+v err=%v", selected, err)
	}
	all, err := parseMigrateImportOptions([]string{
		"bundle.hideout-migration", "--all", "--yes",
	})
	if err != nil || !all.all || len(all.environments) != 0 {
		t.Fatalf("explicit all scope=%+v err=%v", all, err)
	}
	_, err = parseMigrateImportOptions([]string{
		"bundle.hideout-migration", "--all", "--environment", "environment_source1", "--preview",
	})
	if err == nil || !strings.Contains(err.Error(), "scope is ambiguous") {
		t.Fatalf("ambiguous import scope error=%v", err)
	}
}

func TestMigrateImportReplacementUsesSeparateExactDeletePlan(t *testing.T) {
	const sourceRef = migration.OpaqueID("environment_source1")
	deletePlan := migrationCLIStopPlanFixture()
	deletePlan.Action = manager.EnvironmentActionDelete
	deletePlan.OperationID = "op_migrationdeletefixture1"
	deletePlan.PlanDigest = "sha256:" + strings.Repeat("b", 64)
	deletePlan.Targets[0].Status = "stopped"
	terminal := migrationCLIStopOperationFixture(deletePlan)
	terminal.Kind = "environment.delete"
	terminal.Effects[0].Kind = "cleanup"
	terminal.Effects[0].Provider = "daemon.lifecycle.delete"
	result := manager.EnvironmentActionResult{
		Plan: deletePlan,
		Applied: []manager.EnvironmentActionTarget{{
			ID: deletePlan.Targets[0].ID, Status: "stopped",
		}},
		Skipped: []manager.EnvironmentActionTarget{}, Operation: &terminal,
	}
	importPlan := migration.ImportPlan{
		Objects: []migration.ImportObject{{
			SourceRef: sourceRef, DestinationName: "dev",
			Mode: migration.ExportModeConfig, DiskRefs: []migration.OpaqueID{},
		}},
		ConflictActions: []migration.ConflictAction{{
			SourceRef: sourceRef, DestinationName: "dev", Kind: "environment-name",
			Decision: "refuse", ExistingEnvironmentID: deletePlan.Targets[0].ID,
			ExistingStatus: "stopped", Destructive: false,
			Effects:  []string{"No destination object changes; import remains blocked."},
			Recovery: "No recovery is needed because the conflict plan is read-only.",
		}},
		Blockers: []migration.PlanNotice{{
			Code: "migration.destination.name_conflict", SourceRef: sourceRef,
			Summary:     "The reviewed destination environment name is already in use.",
			Remediation: "Choose a new name or use replacement.",
		}},
	}
	calls := 0
	a := app{
		stdout: io.Discard, stderr: io.Discard, stdin: strings.NewReader(""),
		migrationRequest: func(_ string, method, path string, body io.Reader) ([]byte, error) {
			calls++
			var request manager.EnvironmentActionAPIRequest
			decodeMigrationCLIRequest(t, body, &request)
			switch calls {
			case 1:
				if method != http.MethodPost || path != "/api/v1/environment/delete/plan" ||
					!slices.Equal(request.IDs, deletePlan.RequestedIDs) {
					t.Fatalf("delete plan method=%s path=%s request=%+v", method, path, request)
				}
				return migrationCLIEnvelope(t, "environment/delete/plan", deletePlan), nil
			case 2:
				if path != "/api/v1/environment/delete/apply" ||
					request.OperationID != deletePlan.OperationID ||
					request.PlanDigest != deletePlan.PlanDigest || !request.Confirmed || request.Force {
					t.Fatalf("delete apply path=%s request=%+v", path, request)
				}
				return migrationCLIEnvelope(t, "environment/delete/apply", result), nil
			default:
				t.Fatalf("unexpected replacement call=%d path=%s", calls, path)
				return nil, nil
			}
		},
	}
	planned, decisions, applied, err := a.prepareMigrationImportReplacements(
		profile.Store{Root: t.TempDir()}, importPlan, []string{string(sourceRef)},
		false, true, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !applied || calls != 2 || planned == nil ||
		planned.OperationID != deletePlan.OperationID || len(decisions) != 1 ||
		decisions[0].LifecycleOperationID != deletePlan.OperationID ||
		decisions[0].LifecyclePlanDigest != migration.Digest(deletePlan.PlanDigest) {
		t.Fatalf("planned=%+v decisions=%+v applied=%t calls=%d", planned, decisions, applied, calls)
	}
}

func TestMigrateImportReplacementRefusesDeleteWhileAnotherBlockerExists(t *testing.T) {
	plan := migration.ImportPlan{
		ConflictActions: []migration.ConflictAction{{
			SourceRef: "environment_source1", DestinationName: "dev",
			Kind: "environment-name", Decision: "refuse",
			ExistingEnvironmentID: "env_migrationstopfixture1", ExistingStatus: "stopped",
			Effects: []string{"No change."}, Recovery: "No recovery is needed.",
		}},
		Blockers: []migration.PlanNotice{
			{Code: "migration.destination.name_conflict", SourceRef: "environment_source1", Summary: "conflict"},
			{Code: "migration.secret.mapping_required", SourceRef: "secret_source001", Summary: "secret"},
		},
	}
	calls := 0
	a := app{
		stdout: io.Discard, stderr: io.Discard, stdin: strings.NewReader(""),
		migrationRequest: func(string, string, string, io.Reader) ([]byte, error) {
			calls++
			return nil, nil
		},
	}
	_, _, _, err := a.prepareMigrationImportReplacements(
		profile.Store{Root: t.TempDir()}, plan, []string{"environment_source1"},
		false, true, false,
	)
	if err == nil || !strings.Contains(err.Error(), "another blocker") || calls != 0 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestMigrateResumeUsesCurrentRevisionAndOperationBoundProtectedUnlock(t *testing.T) {
	t.Setenv("HIDEOUT_STORE_ROOT", t.TempDir())
	operation := migrationCLIProjectionFixture(manager.MigrationRecoveryResume)
	updated := operation
	updated.Recovery.Required = false
	updated.Recovery.AllowedActions = []manager.MigrationRecoveryAction{}
	calls := 0
	var output bytes.Buffer
	a := app{
		stdout: &output, stderr: io.Discard, stdin: strings.NewReader(""),
		terminalInteractive: func() bool { return true },
		secretReadPassword: func() ([]byte, error) {
			return []byte("resume passphrase"), nil
		},
		migrationRequest: func(_ string, method, path string, body io.Reader) ([]byte, error) {
			calls++
			switch calls {
			case 1:
				if method != http.MethodGet || path != "/api/v1/migration/operations/"+operation.OperationID || body != nil {
					t.Fatalf("status request method=%s path=%s body=%v", method, path, body)
				}
				return migrationCLIEnvelope(t, "migration/operation", operation), nil
			case 2:
				var request manager.MigrationSecretInputAPIRequest
				decodeMigrationCLIRequest(t, body, &request)
				if method != http.MethodPost || path != "/api/v1/migration/secret-input" ||
					request.Purpose != manager.MigrationSecretPurposeExportResume ||
					request.OperationID != operation.OperationID || request.BundlePath != "" ||
					request.Passphrase != "resume passphrase" {
					t.Fatalf("secret request method=%s path=%s request=%+v", method, path, request)
				}
				return migrationCLIEnvelope(t, "migration/secret-input", manager.MigrationSecretInputHandle{
					Handle:   "migh_01234567890123456789012345678901",
					Purpose:  manager.MigrationSecretPurposeExportResume,
					BundleID: operation.BundleID, UsesRemaining: 1,
				}), nil
			case 3:
				var request manager.MigrationOperationActionAPIRequest
				decodeMigrationCLIRequest(t, body, &request)
				if method != http.MethodPost ||
					path != "/api/v1/migration/operations/"+operation.OperationID+"/resume" ||
					request.Revision != operation.Revision || request.SecretInputHandle == "" {
					t.Fatalf("resume request method=%s path=%s request=%+v", method, path, request)
				}
				return migrationCLIEnvelope(t, "migration/operation", updated), nil
			default:
				t.Fatalf("unexpected migration call %d", calls)
				return nil, nil
			}
		},
	}
	if err := a.migrateResume([]string{operation.OperationID, "--json"}); err != nil {
		t.Fatal(err)
	}
	var projected manager.MigrationOperationProjection
	if err := json.Unmarshal(output.Bytes(), &projected); err != nil {
		t.Fatal(err)
	}
	if projected.OperationID != operation.OperationID || calls != 3 {
		t.Fatalf("projection=%+v calls=%d", projected, calls)
	}
}

func TestMigrateImportResumeRequestsOperationBoundImportSecret(t *testing.T) {
	var captured manager.MigrationSecretInputAPIRequest
	a := app{
		stdout: io.Discard, stderr: io.Discard, stdin: strings.NewReader(""),
		migrationRequest: func(_ string, method, path string, body io.Reader) ([]byte, error) {
			if method != http.MethodPost || path != "/api/v1/migration/secret-input" {
				t.Fatalf("method=%s path=%s", method, path)
			}
			decodeMigrationCLIRequest(t, body, &captured)
			return migrationCLIEnvelope(t, "migration/secret-input", manager.MigrationSecretInputHandle{
				Handle:   "migh_01234567890123456789012345678901",
				Purpose:  manager.MigrationSecretPurposeImport,
				BundleID: "migb_importresume1", UsesRemaining: 1,
			}), nil
		},
	}
	passphrase := []byte("resume import passphrase")
	_, err := a.createMigrationResumeSecretInput(
		profile.Store{Root: t.TempDir()}, "op_migration_importresume1",
		manager.MigrationOperationImport, passphrase,
	)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Purpose != manager.MigrationSecretPurposeImport ||
		captured.OperationID != "op_migration_importresume1" ||
		captured.BundlePath != "" || captured.Passphrase != string(passphrase) {
		t.Fatalf("import resume secret request=%+v", captured)
	}
}

func TestMigrateCancelHasNoExportDeletionDefaultAndSendsExplicitFalse(t *testing.T) {
	t.Setenv("HIDEOUT_STORE_ROOT", t.TempDir())
	operation := migrationCLIProjectionFixture(manager.MigrationRecoveryResume)
	requestCount := 0
	requester := func(_ string, method, path string, body io.Reader) ([]byte, error) {
		requestCount++
		if method == http.MethodGet {
			return migrationCLIEnvelope(t, "migration/operation", operation), nil
		}
		var request manager.MigrationOperationActionAPIRequest
		decodeMigrationCLIRequest(t, body, &request)
		if path != "/api/v1/migration/operations/"+operation.OperationID+"/cancel" ||
			request.Revision != operation.Revision || request.RetainPartial == nil ||
			*request.RetainPartial {
			t.Fatalf("cancel path=%s request=%+v", path, request)
		}
		return migrationCLIEnvelope(t, "migration/operation", operation), nil
	}
	a := app{stdout: io.Discard, stderr: io.Discard, stdin: strings.NewReader(""), migrationRequest: requester}
	if err := a.migrateCancel([]string{operation.OperationID, "--yes", "--json"}); err == nil ||
		!strings.Contains(err.Error(), "requires exactly one") {
		t.Fatalf("missing choice error=%v", err)
	}
	if requestCount != 1 {
		t.Fatalf("missing choice mutated after %d calls", requestCount)
	}
	if err := a.migrateCancel([]string{
		operation.OperationID, "--remove-partial", "--yes", "--json",
	}); err != nil {
		t.Fatal(err)
	}
	if requestCount != 3 {
		t.Fatalf("explicit cancellation calls=%d", requestCount)
	}
}

func TestMigrationInterspersedFlagsPreserveDocumentedPositionalFirstSyntax(t *testing.T) {
	ordered, err := migrationInterspersedFlagArgs(
		[]string{"bundle.hideout-migration", "--name", "source=dest", "--preview"},
		map[string]bool{"--name": true},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--name", "source=dest", "--preview", "bundle.hideout-migration"}
	if strings.Join(ordered, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("ordered=%q want=%q", ordered, want)
	}
}

func TestMigrateExportRejectsAmbiguousOrImplicitAuthorityBeforeDaemonUse(t *testing.T) {
	t.Setenv("HIDEOUT_STORE_ROOT", t.TempDir())
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "full guest content not acknowledged",
			args: []string{"--environment", "dev", "--out", "dev.bundle", "--preview"},
			want: "full export requires --ack-guest-content",
		},
		{
			name: "config stop authority",
			args: []string{"--environment", "dev", "--mode", "config", "--out", "dev.bundle", "--stop", "--preview"},
			want: "--stop applies only to --mode full",
		},
		{
			name: "secret selected without acknowledgement",
			args: []string{"--environment", "dev", "--mode", "config", "--out", "dev.bundle", "--include-secret", "proxy", "--preview"},
			want: "--include-secret and --ack-secret-transfer must be used together",
		},
		{
			name: "secret acknowledgement without selection",
			args: []string{"--environment", "dev", "--mode", "config", "--out", "dev.bundle", "--ack-secret-transfer", "--preview"},
			want: "--include-secret and --ack-secret-transfer must be used together",
		},
		{
			name: "all and explicit environment",
			args: []string{"--all", "--environment", "dev", "--mode", "config", "--out", "dev.bundle", "--preview"},
			want: "usage: hideout migrate export",
		},
		{
			name: "preview and apply",
			args: []string{"--environment", "dev", "--mode", "config", "--out", "dev.bundle", "--preview", "--yes"},
			want: "usage: hideout migrate export",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			a := app{
				stdout: io.Discard, stderr: io.Discard, stdin: strings.NewReader(""),
				migrationRequest: func(string, string, string, io.Reader) ([]byte, error) {
					calls++
					return nil, errors.New("unexpected daemon use")
				},
			}
			err := a.migrateExport(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) || calls != 0 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
}

func migrationCLIProjectionFixture(
	action manager.MigrationRecoveryAction,
) manager.MigrationOperationProjection {
	return manager.MigrationOperationProjection{
		Schema:      manager.MigrationOperationProjectionSchema,
		OperationID: "op_migrationclitest1", Revision: 7,
		BundleID: "migb_migrationclitest1", Kind: manager.MigrationOperationExport,
		State: manager.MigrationPhaseRecoverableFailure, PhaseLabel: "Needs recovery",
		Recovery: manager.MigrationRecoveryProjection{
			Required: true, Code: "migration.operation.resume",
			AllowedActions: []manager.MigrationRecoveryAction{action},
			NextAction:     "Resume the exact operation.",
		},
		Warnings: []manager.MigrationNotice{}, Effects: []manager.MigrationEffectProjection{},
	}
}

func migrationCLIEnvelope(t *testing.T, resource string, data any) []byte {
	t.Helper()
	encoded, err := json.Marshal(manager.APIResponse{
		Version: manager.APIVersion, Resource: resource, Data: data, Errors: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func decodeMigrationCLIRequest(t *testing.T, body io.Reader, out any) {
	t.Helper()
	if body == nil {
		t.Fatal("migration request body is nil")
	}
	if err := json.NewDecoder(body).Decode(out); err != nil {
		t.Fatal(err)
	}
}
