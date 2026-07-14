package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/privilege"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
	"github.com/vibe-agi/hideout/internal/runtimeverify"
)

func TestRuntimeCatalogAndInspectionUseOneCoreModel(t *testing.T) {
	core := New(profile.Store{Root: t.TempDir()})
	core.RuntimeCatalogLoader = func() (runtimecatalog.Catalog, error) { return testRuntimeCatalogView(), nil }
	list, err := core.RuntimeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if list.CatalogRelease != "2026.07.0" || len(list.Families) != 1 || list.Families[0].ID != "developer-standard" {
		t.Fatalf("catalog view=%+v", list)
	}
	inspect, err := core.InspectRuntime("developer-standard", "")
	if err != nil {
		t.Fatal(err)
	}
	if !inspect.Current || inspect.Revision.ID != "2026.07.0" || len(inspect.Contract.Observations) != len(runtimecatalog.V1RequiredObservations()) {
		t.Fatalf("inspection=%+v", inspect)
	}
	if _, err := core.InspectRuntime("missing", ""); err == nil {
		t.Fatal("unknown family should fail")
	}
}

func TestRuntimeCatalogManagerRouteUsesCoreModel(t *testing.T) {
	core := New(profile.Store{Root: t.TempDir()})
	core.RuntimeCatalogLoader = func() (runtimecatalog.Catalog, error) { return testRuntimeCatalogView(), nil }
	api := NewAPI(core, "ui_token", time.Minute)
	for _, path := range []string{
		"/api/v1/runtime/catalog",
		"/api/v1/runtime/catalog?family=developer-standard",
	} {
		req := newAPIRequest(http.MethodGet, path)
		req.Header.Set("Authorization", "Bearer ui_token")
		resp := httptest.NewRecorder()
		api.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, resp.Code, resp.Body.String())
		}
		var envelope APIResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Resource != "runtime/catalog" || envelope.Data == nil {
			t.Fatalf("%s envelope=%+v", path, envelope)
		}
	}
}

func TestRuntimeSelectionChangesNoEffectiveAuthority(t *testing.T) {
	base := profile.Default("runtime-authority")
	base.Network.Mode = "privacy"
	base.Network.ProxySecretRef = "env:HIDEOUT_SECRET_DEFAULT_PROXY"
	base.Network.MediatedResolver = "1.1.1.1"
	base.EndpointExposure.HostToGuest = []profile.EndpointCandidate{{
		ID: "docs", Owner: "operator", Proto: "tcp", TargetAddress: "127.0.0.1:4173",
	}}
	base.CommandAdapters.Adapters = map[string]profile.CommandAdapter{
		"tooling": {
			Enabled: true, Builtin: "root-sensitive-v1", Commands: []string{"apt"},
			AllowedProposalCapabilities: []string{"guest.privilege.plan"},
		},
	}
	base.Policy.ScriptRefs = []profile.ScriptRef{{
		ID: "runtime-authority", Path: "policy.js", Entrypoints: []string{"decideCommand"},
	}}
	base.HostFS.Grants = append(base.HostFS.Grants, hostfs.Rule{
		ID: "runtime-authority", HostPath: t.TempDir(), Ops: []hostfs.Op{hostfs.OpDiscover, hostfs.OpRead},
		Scope: hostfs.ScopeRecursiveDir, Subject: hostfs.SubjectProfile, TTL: hostfs.TTLProfile,
	})

	before := snapshotRuntimeAuthority(base)
	selected := base
	_, provenance := runtimeRunCatalogFixture()
	selected.Environment.BaseImage = ""
	selected.Environment.Runtime = &provenance
	after := snapshotRuntimeAuthority(selected)

	if !reflect.DeepEqual(after, before) {
		t.Fatalf("runtime selection changed effective authority:\nbefore=%+v\nafter=%+v", before, after)
	}
	if selected.BaseImageOrBuiltin() != provenance.ImageRef() {
		t.Fatalf("runtime selection did not change only the image identity: %q", selected.BaseImageOrBuiltin())
	}
}

type runtimeAuthoritySnapshot struct {
	HostFS           hostfs.Config
	Network          profile.Network
	Endpoint         profile.EndpointExposure
	HostApplication  profile.HostCapabilities
	CommandProxy     profile.CommandProxy
	CommandAdapters  profile.CommandAdapters
	MaxCapabilities  []string
	Scripts          []profile.ScriptRef
	TargetIdentity   profile.Identity
	WorkspacePosture profile.Workspace
}

func snapshotRuntimeAuthority(p profile.Profile) runtimeAuthoritySnapshot {
	return runtimeAuthoritySnapshot{
		HostFS: p.HostFS, Network: p.Network, Endpoint: p.EndpointExposure,
		HostApplication: p.HostCapabilities, CommandProxy: p.CommandProxy,
		CommandAdapters: p.CommandAdapters,
		MaxCapabilities: append([]string(nil), p.Policy.MaxCapabilities...),
		Scripts:         append([]profile.ScriptRef(nil), p.Policy.ScriptRefs...),
		TargetIdentity:  p.Identity, WorkspacePosture: p.Workspace,
	}
}

func TestRuntimeVerifyPlanApplyUsesProbeOnlyAndReplacesReceipt(t *testing.T) {
	core, record := runtimeVerifyCoreFixture(t)
	before, err := (environment.Store{Root: core.Store.Root}).Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := core.PlanRuntimeVerify(record.Name)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != RuntimeVerifyPlanVersion || plan.EnvironmentID != record.ID || len(plan.Effects) != 3 {
		t.Fatalf("plan=%+v", plan)
	}
	afterPlan, err := (environment.Store{Root: core.Store.Root}).Load(record.ID)
	if err != nil || !reflect.DeepEqual(afterPlan, before) {
		t.Fatalf("plan mutated environment: before=%+v after=%+v err=%v", before, afterPlan, err)
	}
	if _, err := (runtimeverify.Store{Root: core.Store.Root}).Load(record.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan wrote receipt: %v", err)
	}

	fake := &applyRunFakeBackend{name: "lima"}
	fake.verifyFunc = func(session *backend.Session) error {
		if got := session.RuntimeInstanceExpected.PackageInventorySHA256; got != strings.TrimPrefix(record.Runtime.PackageInventoryDigest, "sha256:") {
			return fmt.Errorf("runtime expectation package inventory sha256=%q", got)
		}
		if err := session.PrivilegeStatusSink(privilege.Status{Status: privilege.StatusEnforced}); err != nil {
			return err
		}
		return session.RuntimeResultSink(runtimePassingReport(session))
	}
	result, err := core.ApplyRuntimeVerify(context.Background(), plan, fake)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Status != runtimeverify.StatusPreviewReady || result.ReceiptRef == "" {
		t.Fatalf("result=%+v", result)
	}
	receipt, err := (runtimeverify.Store{Root: core.Store.Root}).Load(record.ID)
	if err != nil || receipt.Instance.ActiveBuildIdentity != record.Runtime.PackageInventoryDigest {
		t.Fatalf("active build identity receipt=%+v err=%v", receipt.Instance, err)
	}
	if strings.Join(fake.calls, ",") != "prepare,verify-runtime,cleanup" {
		t.Fatalf("runtime verify executed an unexpected backend path: %v", fake.calls)
	}
	updated, err := (environment.Store{Root: core.Store.Root}).Load(record.ID)
	if err != nil || updated.Status != "ready" || updated.LastCommand != before.LastCommand {
		t.Fatalf("verify changed target identity/command state: %+v err=%v", updated, err)
	}
}

func TestRuntimeVerifyCancellationInvalidatesHistoricalSuccess(t *testing.T) {
	core, record := runtimeVerifyCoreFixture(t)
	plan, err := core.PlanRuntimeVerify(record.Name)
	if err != nil {
		t.Fatal(err)
	}
	old := runtimeReceiptFixture(record)
	if err := (runtimeverify.Store{Root: core.Store.Root}).Write(old); err != nil {
		t.Fatal(err)
	}
	fake := &applyRunFakeBackend{name: "lima", verifyFunc: func(*backend.Session) error { return context.Canceled }}
	_, err = core.ApplyRuntimeVerify(context.Background(), plan, fake)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("verify error=%v", err)
	}
	if _, err := (runtimeverify.Store{Root: core.Store.Root}).Load(record.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled verification left historical success current: %v", err)
	}
}

func TestRuntimeVerifyPreservesFreshBoundaryFailureReceiptAndRunningState(t *testing.T) {
	core, record := runtimeVerifyCoreFixture(t)
	plan, err := core.PlanRuntimeVerify(record.Name)
	if err != nil {
		t.Fatal(err)
	}
	fake := &applyRunFakeBackend{name: "lima"}
	fake.verifyFunc = func(session *backend.Session) error {
		if err := session.PrivilegeStatusSink(privilege.Status{Status: privilege.StatusEnforced}); err != nil {
			return err
		}
		report := runtimePassingReport(session)
		report.Results[0].Matched = false
		report.Results[0].Reason = "boundary-fixture"
		if err := session.RuntimeResultSink(report); err != nil {
			return err
		}
		return backend.RuntimeBoundaryError{FailedIDs: []string{report.Results[0].ID}}
	}
	result, err := core.ApplyRuntimeVerify(context.Background(), plan, fake)
	var boundaryErr backend.RuntimeBoundaryError
	if !errors.As(err, &boundaryErr) {
		t.Fatalf("verify error=%v", err)
	}
	if result.Status.Status != runtimeverify.StatusPreviewFailed || len(result.Status.FailedIDs) == 0 || result.ReceiptRef == "" {
		t.Fatalf("fresh failure was hidden: %+v", result)
	}
	receipt, err := (runtimeverify.Store{Root: core.Store.Root}).Load(record.ID)
	if err != nil || receipt.Status != runtimeverify.StatusPreviewFailed || len(receipt.FailedIDs) == 0 {
		t.Fatalf("failed receipt=%+v err=%v", receipt, err)
	}
	updated, err := (environment.Store{Root: core.Store.Root}).Load(record.ID)
	if err != nil || updated.Status != "ready" || updated.LastSessionID == "" {
		t.Fatalf("failed verification lost actual running state: %+v err=%v", updated, err)
	}
}

func TestRuntimeVerifyCleanupFailureInvalidatesNewReceipt(t *testing.T) {
	core, record := runtimeVerifyCoreFixture(t)
	plan, err := core.PlanRuntimeVerify(record.Name)
	if err != nil {
		t.Fatal(err)
	}
	fake := &applyRunFakeBackend{name: "lima", cleanupErr: errors.New("cleanup failed")}
	fake.verifyFunc = func(session *backend.Session) error {
		if err := session.PrivilegeStatusSink(privilege.Status{Status: privilege.StatusEnforced}); err != nil {
			return err
		}
		return session.RuntimeResultSink(runtimePassingReport(session))
	}
	_, err = core.ApplyRuntimeVerify(context.Background(), plan, fake)
	if err == nil || !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("cleanup verification error=%v", err)
	}
	if _, err := (runtimeverify.Store{Root: core.Store.Root}).Load(record.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup failure left a runtime receipt: %v", err)
	}
}

func TestRuntimeVerifyHonorsEnvironmentLockAndPlanDrift(t *testing.T) {
	core, record := runtimeVerifyCoreFixture(t)
	plan, err := core.PlanRuntimeVerify(record.Name)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := (environment.Store{Root: core.Store.Root}).Lock(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	fake := &applyRunFakeBackend{name: "lima"}
	if _, err := core.ApplyRuntimeVerify(context.Background(), plan, fake); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("locked verify error=%v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	plan.ImageRef = strings.Replace(plan.ImageRef, "#sha256:", "-drift#sha256:", 1)
	if _, err := core.ApplyRuntimeVerify(context.Background(), plan, fake); err == nil || !strings.Contains(err.Error(), "no longer matches") {
		t.Fatalf("drifted plan error=%v", err)
	}
}

func TestRuntimeStatusRejectsCatalogAndBootIdentityDrift(t *testing.T) {
	for _, mutation := range []struct {
		name   string
		mutate func(*Core, runtimecatalog.Catalog)
	}{
		{name: "withdrawn", mutate: func(core *Core, catalog runtimecatalog.Catalog) {
			catalog.Families[0].Revisions[0].Status = runtimecatalog.RevisionWithdrawn
			core.RuntimeCatalogLoader = func() (runtimecatalog.Catalog, error) { return catalog, nil }
		}},
		{name: "artifact replaced", mutate: func(core *Core, catalog runtimecatalog.Catalog) {
			catalog.Families[0].Revisions[0].Artifacts[0].SHA256 = strings.Repeat("9", 64)
			core.RuntimeCatalogLoader = func() (runtimecatalog.Catalog, error) { return catalog, nil }
		}},
		{name: "revision removed", mutate: func(core *Core, catalog runtimecatalog.Catalog) {
			catalog.CatalogRelease = runtimecatalog.UnpromotedRelease
			catalog.Families = nil
			catalog.Contract.Observations = nil
			core.RuntimeCatalogLoader = func() (runtimecatalog.Catalog, error) { return catalog, nil }
		}},
		{name: "boot replaced", mutate: func(core *Core, catalog runtimecatalog.Catalog) {
			core.RuntimeInstanceInspector = func(_ context.Context, instanceName string, expected backend.RuntimeInstanceExpectation) (backend.RuntimeInstanceObservation, error) {
				return backend.RuntimeInstanceObservation{
					InstanceName: instanceName, Status: "Running", VMType: expected.VMType,
					HostOS: expected.HostOS, HostArch: expected.HostArch, GuestArch: expected.GuestArch,
					ImageLocation: expected.ImageLocation, ImageSHA256: expected.ImageSHA256,
					PackageInventorySHA256: expected.PackageInventorySHA256,
					BootID:                 "ffffffff-ffff-ffff-ffff-ffffffffffff",
				}, nil
			}
		}},
		{name: "active build mutated", mutate: func(core *Core, catalog runtimecatalog.Catalog) {
			core.RuntimeInstanceInspector = func(_ context.Context, instanceName string, expected backend.RuntimeInstanceExpectation) (backend.RuntimeInstanceObservation, error) {
				return backend.RuntimeInstanceObservation{
					InstanceName: instanceName, Status: "Running", VMType: expected.VMType,
					HostOS: expected.HostOS, HostArch: expected.HostArch, GuestArch: expected.GuestArch,
					ImageLocation: expected.ImageLocation, ImageSHA256: expected.ImageSHA256,
					PackageInventorySHA256: strings.Repeat("9", 64),
					BootID:                 "01234567-89ab-cdef-0123-456789abcdef",
				}, nil
			}
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			core, record := runtimeVerifyCoreFixture(t)
			catalog, _ := runtimeRunCatalogFixture()
			verifyRuntimeReady(t, &core, record)
			mutation.mutate(&core, catalog)
			status, err := core.RuntimeStatus(record.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status.Status != runtimeverify.StatusUnknown {
				t.Fatalf("drift kept runtime ready: %+v", status)
			}
		})
	}
}

func TestOrdinaryRunFailureAndCleanupInvalidateHistoricalReceipt(t *testing.T) {
	for _, tc := range []struct {
		name       string
		runErr     error
		cleanupErr error
		writeProbe bool
	}{
		{name: "canceled before probe", runErr: context.Canceled},
		{name: "failed cleanup after passing probe", cleanupErr: errors.New("cleanup failed"), writeProbe: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, record := runtimeVerifyCoreFixture(t)
			if err := (runtimeverify.Store{Root: core.Store.Root}).Write(runtimeReceiptFixture(record)); err != nil {
				t.Fatal(err)
			}
			plan, err := core.PlanRun(RunPlanOptions{
				ProfileName: record.Profile, Backend: "lima", Workspace: record.Workspace,
				GuestWorkspace: record.GuestWorkspace, Command: []string{"true"},
			})
			if err != nil {
				t.Fatal(err)
			}
			fake := &applyRunFakeBackend{name: "lima", runErr: tc.runErr, cleanupErr: tc.cleanupErr}
			if tc.writeProbe {
				fake.runFunc = func(session *backend.Session) error {
					if err := session.PrivilegeStatusSink(privilege.Status{Status: privilege.StatusEnforced}); err != nil {
						return err
					}
					return session.RuntimeResultSink(runtimePassingReport(session))
				}
			}
			_, err = core.ApplyRun(context.Background(), plan, ApplyRunOptions{
				Backend: fake, RequestedBackend: "lima", Environment: RunEnvironmentOptions{EnvName: record.Name, Create: true},
			})
			if err == nil {
				t.Fatal("failed run unexpectedly succeeded")
			}
			runErr := err
			if got, loadErr := (runtimeverify.Store{Root: core.Store.Root}).Load(record.ID); !errors.Is(loadErr, os.ErrNotExist) {
				t.Fatalf("failed run left historical receipt current: loadErr=%v runErr=%v receipt=%+v attached=%t completion=%t", loadErr, runErr, got, fake.spec.RuntimeContract != nil, fake.spec.RuntimeCompletionSink != nil)
			}
		})
	}
}

func TestUnsafeWorkspaceCanRunOnlyWithoutTrustedRuntimeReadiness(t *testing.T) {
	core, record := runtimeVerifyCoreFixture(t)
	verifyRuntimeReady(t, &core, record)
	record, err := (environment.Store{Root: core.Store.Root}).Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Workspace = core.Store.Root
	if err := (environment.Store{Root: core.Store.Root}).Save(record); err != nil {
		t.Fatal(err)
	}
	status, err := core.RuntimeStatus(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != runtimeverify.StatusUnknown || !strings.Contains(status.Reason, "receipt authority") {
		t.Fatalf("unsafe workspace retained trusted readiness: %+v", status)
	}
	if _, err := core.PlanRuntimeVerify(record.ID); err == nil || !strings.Contains(err.Error(), "receipt authority") {
		t.Fatalf("unsafe explicit verify error=%v", err)
	}

	p, err := core.Store.Load(record.Profile)
	if err != nil {
		t.Fatal(err)
	}
	runEnv := selectedRunEnvironment(environment.Store{Root: core.Store.Root}, record, true, false, false)
	runSession, err := core.BeginRunSession(runtimeVerificationRunPlan(record, p), runEnv, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = core.CloseRunSession(runSession) }()
	runSession, err = core.OpenRunSessionAudit(runSession, RunAuditOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec := core.runSpec(runSession, runEnv, RunDataPlane{Env: append([]string(nil), runSession.Env.Env...)}, RunNetwork{})
	if err := core.attachRuntimeVerification(&spec, runSession, runEnv, "lima"); err != nil {
		t.Fatal(err)
	}
	if spec.RuntimeContract == nil || spec.RuntimeInstanceExpected == nil || spec.RuntimeResultSink == nil || spec.PrivilegeStatusSink == nil {
		t.Fatalf("unsafe workspace disabled runtime enforcement: %+v", spec)
	}
	if err := spec.PrivilegeStatusSink(privilege.Status{Status: privilege.StatusEnforced}); err != nil {
		t.Fatal(err)
	}
	backendSession := &backend.Session{
		ID: runSession.Layout.ID, EnvironmentID: record.ID, InstanceName: record.InstanceName,
		RuntimeContract: spec.RuntimeContract, RuntimeInstanceExpected: spec.RuntimeInstanceExpected,
	}
	if err := spec.RuntimeResultSink(runtimePassingReport(backendSession)); err != nil {
		t.Fatal(err)
	}
	if _, err := (runtimeverify.Store{Root: core.Store.Root}).Load(record.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe workspace persisted trusted runtime receipt: %v", err)
	}
}

func verifyRuntimeReady(t *testing.T, core *Core, record environment.Record) {
	t.Helper()
	plan, err := core.PlanRuntimeVerify(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	fake := &applyRunFakeBackend{name: "lima"}
	fake.verifyFunc = func(session *backend.Session) error {
		if err := session.PrivilegeStatusSink(privilege.Status{Status: privilege.StatusEnforced}); err != nil {
			return err
		}
		return session.RuntimeResultSink(runtimePassingReport(session))
	}
	result, err := core.ApplyRuntimeVerify(context.Background(), plan, fake)
	if err != nil || result.Status.Status != runtimeverify.StatusPreviewReady {
		t.Fatalf("ready verification result=%+v err=%v", result, err)
	}
}

func runtimeVerifyCoreFixture(t *testing.T) (Core, environment.Record) {
	t.Helper()
	setFakeLinuxShim(t)
	store := profile.Store{Root: t.TempDir()}
	catalog, provenance := runtimeRunCatalogFixture()
	p := profile.Default("runtime")
	p.Environment.BaseImage = ""
	p.Environment.Runtime = &provenance
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	p, err := store.Load("runtime")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	envStore := environment.Store{Root: store.Root}
	record, err := envStore.Create(RunEnvironmentSpec(p, "lima", workspace, "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	record.InstanceName = "hideout-runtime-test"
	record.Status = "stopped"
	if err := envStore.Save(record); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	core.RuntimeCatalogLoader = func() (runtimecatalog.Catalog, error) { return catalog, nil }
	core.RuntimeDiskCheck = func(string, int64) error { return nil }
	core.RuntimeInstanceInspector = func(_ context.Context, instanceName string, expected backend.RuntimeInstanceExpectation) (backend.RuntimeInstanceObservation, error) {
		return backend.RuntimeInstanceObservation{
			InstanceName: instanceName, Status: "Running", VMType: expected.VMType,
			HostOS: expected.HostOS, HostArch: expected.HostArch, GuestArch: expected.GuestArch,
			ImageLocation: expected.ImageLocation, ImageSHA256: expected.ImageSHA256,
			PackageInventorySHA256: expected.PackageInventorySHA256,
			BootID:                 "01234567-89ab-cdef-0123-456789abcdef",
		}, nil
	}
	return core, record
}

func runtimePassingReport(session *backend.Session) backend.RuntimeObservationReport {
	contract := session.RuntimeContract
	expected := session.RuntimeInstanceExpected
	report := backend.RuntimeObservationReport{
		ContractID: contract.ID, ContractDigest: contract.Digest, PrivilegeStatus: "enforced",
		Instance: backend.RuntimeInstanceObservation{
			InstanceName: session.InstanceName, Status: "Running", VMType: expected.VMType,
			HostOS: expected.HostOS, HostArch: expected.HostArch, GuestArch: expected.GuestArch,
			ImageLocation: expected.ImageLocation, ImageSHA256: expected.ImageSHA256,
			PackageInventorySHA256: expected.PackageInventorySHA256,
			BootID:                 "01234567-89ab-cdef-0123-456789abcdef", SessionID: session.ID, EnvironmentID: session.EnvironmentID,
		},
	}
	for _, observation := range contract.Observations {
		report.Results = append(report.Results, backend.RuntimeObservationResult{
			ID: observation.ID, Class: observation.Class, Command: observation.Command,
			Present: true, Matched: true, Output: observation.Command + " version", Reason: "ok",
		})
	}
	return report
}

func runtimeReceiptFixture(record environment.Record) runtimeverify.Receipt {
	contract := backend.RuntimeContract{ID: record.Runtime.ContractID, Digest: record.Runtime.ContractDigest, Observations: []backend.RuntimeObservation{
		{ID: "boundary.id", Class: backend.RuntimeObservationBoundary, Command: "id"},
		{ID: "baseline.git", Class: backend.RuntimeObservationBaseline, Command: "git"},
	}}
	report := runtimePassingReport(&backend.Session{
		ID: "ses_old", EnvironmentID: record.ID, InstanceName: record.InstanceName,
		RuntimeContract: &contract, RuntimeInstanceExpected: func() *backend.RuntimeInstanceExpectation {
			expected := runtimeInstanceExpectation(*record.Runtime)
			return &expected
		}(),
	})
	receipt, _, err := runtimeReceipt(selectedRunEnvironment(environment.Store{}, record, true, false, false), contract, report, "lima")
	if err != nil {
		panic(err)
	}
	return receipt
}

func testRuntimeCatalogView() runtimecatalog.Catalog {
	contract := runtimecatalog.Contract{
		Schema:       runtimecatalog.ContractSchema,
		ID:           "developer-standard/v1",
		Observations: testRuntimeCatalogObservations(),
	}
	artifact := runtimecatalog.Artifact{
		HostOS: "darwin", HostArch: "arm64", GuestArch: "aarch64", Format: "qcow2",
		Location: "https://example.invalid/runtime/2026.07.0/developer-standard.qcow2", SHA256: strings.Repeat("a", 64),
		DownloadBytes: 512 << 20, VirtualBytes: 12 << 30, SupplyMode: "hideout-built",
		Source: runtimecatalog.ArtifactSource{
			BaseLocation: "https://example.invalid/base/20260706/base.qcow2", BaseSHA512: strings.Repeat("b", 128),
			BaseSHA256: strings.Repeat("c", 64), BuildCommit: strings.Repeat("d", 12), SourceLockSHA256: strings.Repeat("e", 64), LicenseReview: "reviewed",
		},
		PackageInventoryDigest: "sha256:" + strings.Repeat("f", 64),
		SBOM:                   runtimecatalog.SBOM{Status: "unavailable-preview"},
	}
	revision := runtimecatalog.Revision{
		ID: "2026.07.0", Status: runtimecatalog.RevisionPreview, ContractID: contract.ID,
		ContractDigest: "sha256:" + strings.Repeat("1", 64), ReviewedAt: "2026-07-11T00:00:00Z", Artifacts: []runtimecatalog.Artifact{artifact},
	}
	return runtimecatalog.Catalog{
		Schema: runtimecatalog.CatalogSchema, CatalogRelease: "2026.07.0", GeneratedAt: "2026-07-11T00:00:00Z", Contract: contract,
		Families: []runtimecatalog.Family{{ID: "developer-standard", DisplayName: "Developer Standard", Maturity: "preview", CurrentRevision: revision.ID, Revisions: []runtimecatalog.Revision{revision}}},
	}
}

func testRuntimeCatalogObservations() []runtimecatalog.Observation {
	required := runtimecatalog.V1RequiredObservations()
	out := make([]runtimecatalog.Observation, 0, len(required))
	for _, item := range required {
		observation := runtimecatalog.Observation{ID: item.ID, Class: item.Class, Command: item.Command, Description: item.ID}
		if item.Command == "git" {
			observation.VersionArgs = []string{"--version"}
			observation.OutputPattern = "^git version .+$"
		}
		out = append(out, observation)
	}
	return out
}
