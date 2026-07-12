package manager

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/privilege"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
	"github.com/vibe-agi/hideout/internal/runtimeverify"
)

func TestApplyRunBindsRuntimeReceiptAuditAndBoundaryOutcome(t *testing.T) {
	for _, tc := range []struct {
		name          string
		boundaryPass  bool
		baselinePass  bool
		wantTargetRun bool
		wantStatus    string
		wantRecovery  string
	}{
		{name: "ready", boundaryPass: true, baselinePass: true, wantTargetRun: true, wantStatus: runtimeverify.StatusPreviewReady},
		{name: "baseline degraded continues", boundaryPass: true, baselinePass: false, wantTargetRun: true, wantStatus: runtimeverify.StatusPreviewFailed, wantRecovery: "runtime.baseline.missing"},
		{name: "boundary failure blocks target", boundaryPass: false, baselinePass: true, wantTargetRun: false, wantStatus: runtimeverify.StatusPreviewFailed, wantRecovery: "runtime.boundary.missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := profile.Store{Root: t.TempDir()}
			workspace := t.TempDir()
			catalog, provenance := runtimeRunCatalogFixture()
			p := profile.Default("runtime")
			p.Environment.BaseImage = ""
			p.Environment.Runtime = &provenance
			if err := store.Save(p); err != nil {
				t.Fatal(err)
			}
			core := New(store)
			core.RuntimeCatalogLoader = func() (runtimecatalog.Catalog, error) { return catalog, nil }
			core.RuntimeDiskCheck = func(string, int64) error { return nil }
			plan, err := core.PlanRun(RunPlanOptions{
				ProfileName: "runtime", Backend: "lima", Workspace: workspace, Command: []string{"tool"},
			})
			if err != nil {
				t.Fatal(err)
			}
			targetRan := false
			var observedReceipt runtimeverify.Receipt
			fake := &applyRunFakeBackend{name: "lima"}
			fake.runFunc = func(session *backend.Session) error {
				if session.RuntimeContract == nil || session.RuntimeResultSink == nil {
					return errors.New("runtime verification was not attached")
				}
				if err := session.PrivilegeStatusSink(privilege.Status{Status: privilege.StatusEnforced}); err != nil {
					return err
				}
				report := runtimePassingReport(session)
				for i := range report.Results {
					switch report.Results[i].ID {
					case "boundary.getent":
						report.Results[i].Present = tc.boundaryPass
						report.Results[i].Matched = tc.boundaryPass
						report.Results[i].Output = "getent cap_0123456789abcdef"
						report.Results[i].Reason = runtimeReason(tc.boundaryPass)
					case "baseline.git":
						report.Results[i].Present = tc.baselinePass
						report.Results[i].Matched = tc.baselinePass
						report.Results[i].Output = "git version 2.47"
						report.Results[i].Reason = runtimeReason(tc.baselinePass)
					}
				}
				if !tc.boundaryPass {
					report.BoundaryFailed = []string{"boundary.getent"}
				}
				if !tc.baselinePass {
					report.BaselineFailed = []string{"baseline.git"}
				}
				if err := session.RuntimeResultSink(report); err != nil {
					return err
				}
				loaded, loadErr := (runtimeverify.Store{Root: store.Root}).Load(session.EnvironmentID)
				if loadErr != nil {
					return loadErr
				}
				observedReceipt = loaded
				if len(report.BoundaryFailed) > 0 {
					return backend.RuntimeBoundaryError{FailedIDs: report.BoundaryFailed}
				}
				targetRan = true
				return nil
			}
			result, runErr := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
				Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{Create: true}, Opener: broker.NoopOpener{},
			})
			if tc.wantTargetRun && runErr != nil {
				t.Fatalf("ApplyRun: %v", runErr)
			}
			if !tc.wantTargetRun {
				var boundaryErr backend.RuntimeBoundaryError
				if !errors.As(runErr, &boundaryErr) {
					t.Fatalf("boundary run error=%v", runErr)
				}
			}
			if targetRan != tc.wantTargetRun {
				t.Fatalf("targetRan=%t want %t", targetRan, tc.wantTargetRun)
			}
			if _, err := (runtimeverify.Store{Root: store.Root}).Load(result.EnvironmentID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("ordinary run cleanup left a current receipt: %v", err)
			}
			if observedReceipt.Status != tc.wantStatus || observedReceipt.RecoveryCode != tc.wantRecovery || observedReceipt.Provenance != provenance {
				t.Fatalf("operation receipt=%+v", observedReceipt)
			}
			if strings.Contains(observedReceipt.Results[0].VersionOutput, "cap_0123456789abcdef") {
				t.Fatalf("receipt leaked control-plane token: %+v", observedReceipt.Results[0])
			}
			auditData, err := os.ReadFile(result.AuditPath)
			if err != nil {
				t.Fatal(err)
			}
			body := string(auditData)
			if strings.Count(body, `"action":"runtime.verify"`) != 1 || !strings.Contains(body, `"contractDigest"`) || strings.Contains(body, "git version") || strings.Contains(body, "cap_0123456789abcdef") {
				t.Fatalf("runtime aggregate audit mismatch: %s", body)
			}
		})
	}
}

func TestApplyRunMapsExactRuntimeCommandMissWithoutTargetSideEffect(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	catalog, provenance := runtimeRunCatalogFixture()
	p := profile.Default("runtime")
	p.Environment.BaseImage = ""
	p.Environment.Runtime = &provenance
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	core.RuntimeCatalogLoader = func() (runtimecatalog.Catalog, error) { return catalog, nil }
	core.RuntimeDiskCheck = func(string, int64) error { return nil }
	plan, err := core.PlanRun(RunPlanOptions{ProfileName: "runtime", Backend: "lima", Workspace: workspace, Command: []string{"missing-tool"}})
	if err != nil {
		t.Fatal(err)
	}
	targetSideEffect := false
	fake := &applyRunFakeBackend{name: "lima"}
	fake.runFunc = func(session *backend.Session) error {
		if err := session.PrivilegeStatusSink(privilege.Status{Status: privilege.StatusEnforced}); err != nil {
			return err
		}
		if err := session.RuntimeResultSink(runtimePassingReport(session)); err != nil {
			return err
		}
		var commandCheckErr error = backend.CommandNotFoundError{Backend: "lima", Command: "missing-tool", Path: "/usr/bin:/bin", Workspace: "/workspace"}
		if commandCheckErr != nil {
			return commandCheckErr
		}
		targetSideEffect = true
		return nil
	}
	_, err = core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{Create: true}, Opener: broker.NoopOpener{},
	})
	var recoveryErr RuntimeRecoveryError
	if !errors.As(err, &recoveryErr) || recoveryErr.Code != "runtime.command.missing" {
		t.Fatalf("runtime command error=%T %v", err, err)
	}
	if targetSideEffect {
		t.Fatal("missing exact command reached target side effect")
	}
}

func runtimeReason(pass bool) string {
	if pass {
		return "ok"
	}
	return "command-missing"
}

func runtimeRunCatalogFixture() (runtimecatalog.Catalog, environment.RuntimeProvenance) {
	catalog := testRuntimeCatalogView()
	digest := "sha256:" + strings.Repeat("c", 64)
	catalog.Families[0].Revisions[0].ContractDigest = digest
	artifact := catalog.Families[0].Revisions[0].Artifacts[0]
	provenance := environment.RuntimeProvenance{
		Family: catalog.Families[0].ID, Revision: catalog.Families[0].Revisions[0].ID, CatalogRelease: catalog.CatalogRelease,
		ContractID: catalog.Contract.ID, ContractDigest: digest,
		ArtifactLocation: artifact.Location, ArtifactSHA256: artifact.SHA256,
		PackageInventoryDigest: artifact.PackageInventoryDigest,
		DownloadBytes:          artifact.DownloadBytes, VirtualBytes: artifact.VirtualBytes,
		HostOS: artifact.HostOS, HostArch: artifact.HostArch, GuestArch: artifact.GuestArch,
		Maturity: catalog.Families[0].Maturity,
	}
	return catalog, provenance
}
