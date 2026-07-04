package manager

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/inittask"
	"github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestCorePlansAndAppliesInitTasks(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	seedStoreHelper(t, store.Root, "hideout-shim")
	seedStoreHelper(t, store.Root, "hideout-hostfsd")
	core := New(store)
	plan, err := core.PlanInit(inittask.Options{
		ProfileName: "default",
		Backend:     "auto",
		Network:     "direct",
		NoInput:     true,
	})
	if err != nil {
		t.Fatalf("PlanInit: %v", err)
	}
	if plan.Version != inittask.Version || len(plan.Tasks) == 0 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	result, err := core.ApplyInit(plan, inittask.ApplyOptions{NoInput: true})
	if err != nil {
		t.Fatalf("ApplyInit: %v", err)
	}
	if len(result.Applied) == 0 {
		t.Fatalf("expected init to apply tasks: %+v", result)
	}
	if result.AuditPath == "" {
		t.Fatalf("expected init audit path: %+v", result)
	}
	auditData, err := os.ReadFile(result.AuditPath)
	if err != nil {
		t.Fatalf("read init audit: %v", err)
	}
	if !strings.Contains(string(auditData), `"operation":"init.apply"`) ||
		!strings.Contains(string(auditData), `"taskKind":"profile.create"`) {
		t.Fatalf("init audit missing expected events: %s", auditData)
	}
	if _, err := store.Load("default"); err != nil {
		t.Fatalf("init did not create default profile: %v", err)
	}
	overview, err := core.Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if !overview.Init.Initialized || overview.Init.PendingTasks != 0 {
		t.Fatalf("init summary mismatch: %+v", overview.Init)
	}
	if overview.Init.AuditPath != result.AuditPath || overview.Init.AuditEvents == 0 {
		t.Fatalf("init summary audit mismatch: %+v result=%+v", overview.Init, result)
	}
}

func seedStoreHelper(t *testing.T, storeRoot, command string) {
	t.Helper()
	var path string
	switch command {
	case "hideout-shim":
		path = helperbin.DefaultLinuxShimPath(storeRoot, runtime.GOARCH)
	case "hideout-hostfsd":
		path = helperbin.DefaultLinuxHostFSDPath(storeRoot, runtime.GOARCH)
	default:
		t.Fatalf("unsupported helper command %q", command)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(command), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := helperbin.WriteStoreHelperManifest(path, command, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
}

func TestCorePlanRunOwnsProfileBackendAndWorkspace(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	p := profile.Default("alias-run")
	p.Workspace.PathMode = "alias"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}

	plan, err := New(store).PlanRun(RunPlanOptions{
		ProfileName:    "alias-run",
		Backend:        "auto",
		NetworkMode:    network.ModeTun2Socks,
		ProxySecretRef: "default-proxy",
		Workspace:      workspace,
		Command:        []string{"echo", "hi"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	if plan.Version != RunPlanVersion {
		t.Fatalf("run plan version=%q", plan.Version)
	}
	if plan.ProfileName != "alias-run" || plan.Backend != "lima" {
		t.Fatalf("profile/backend mismatch: %+v", plan)
	}
	if plan.Workspace != workspace || plan.GuestWorkspace != AliasGuestWorkspace {
		t.Fatalf("workspace mapping mismatch: %+v", plan)
	}
	if plan.PathMode != "alias" || plan.WorkspaceMode != "read-write" {
		t.Fatalf("workspace policy mismatch: %+v", plan)
	}
	if plan.NetworkMode != network.ModeTun2Socks || plan.ProxySecretRef != "default-proxy" {
		t.Fatalf("network override mismatch: %+v", plan)
	}
	if !reflect.DeepEqual(plan.Command, []string{"echo", "hi"}) {
		t.Fatalf("command mismatch: %+v", plan.Command)
	}
	loaded, err := store.Load("alias-run")
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if loaded.Network.Mode == network.ModeTun2Socks || loaded.Network.ProxySecretRef != "" {
		t.Fatalf("run-scoped network override was persisted: %+v", loaded.Network)
	}
}

func TestCorePlanRunRejectsMissingCommandBeforeProfileCreation(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	_, err := New(store).PlanRun(RunPlanOptions{
		ProfileName: "no-command",
		Backend:     "native",
		Workspace:   t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected missing command to fail")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(store.ProfilePath("no-command")); !os.IsNotExist(statErr) {
		t.Fatalf("missing command should not create profile state, stat err=%v", statErr)
	}
}

func TestRunPlanJSONMatchesSchemaAndHidesProfiles(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	plan, err := New(store).PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "native",
		Workspace:   t.TempDir(),
		Command:     []string{"echo", "hi"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, leaked := range []string{"RuntimeProfile", "Identity", "machineId", "developer@example.com"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("run plan JSON leaked internal profile field %q: %s", leaked, text)
		}
	}
	schema := compileRunPlanSchema(t)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode run plan JSON: %v", err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("run plan does not match schema: %v\n%s", err, data)
	}
}

func TestCoreBeginOpenCloseRunSessionOwnsAuditEnvAndCleanup(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "native",
		Workspace:   t.TempDir(),
		Ephemeral:   true,
		Command:     []string{"echo", "hi"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	runSession, err := core.BeginRunSession(plan, RunEnvironment{}, RunSessionOptions{})
	if err != nil {
		t.Fatalf("BeginRunSession: %v", err)
	}
	if runSession.Layout.ID == "" || runSession.ProfileDir != store.ProfileDir("default") {
		t.Fatalf("session paths missing: %+v", runSession)
	}
	if runSession.RuntimeSessionDir != runSession.Layout.Dir || runSession.RuntimeShimDir != runSession.Layout.ShimDir {
		t.Fatalf("native session should use session runtime dirs: %+v", runSession)
	}
	if want := filepath.Join(runSession.Layout.Dir, "identity"); runSession.IdentityDir != want {
		t.Fatalf("ephemeral identity dir=%q want %q", runSession.IdentityDir, want)
	}
	if got, want := runSession.Env.Synthetic["HOME"], filepath.Join(runSession.IdentityDir, "home"); got != want {
		t.Fatalf("synthetic HOME=%q want %q", got, want)
	}
	runSession, err = core.OpenRunSessionAudit(runSession, RunAuditOptions{})
	if err != nil {
		t.Fatalf("OpenRunSessionAudit: %v", err)
	}
	if runSession.AuditPath != runSession.Layout.AuditPath {
		t.Fatalf("audit path=%q want %q", runSession.AuditPath, runSession.Layout.AuditPath)
	}
	if err := runSession.Audit.Emit(audit.Event{
		Session:  runSession.Layout.ID,
		Profile:  plan.ProfileName,
		Backend:  plan.Backend,
		Action:   "test.event",
		Decision: "allow",
	}); err != nil {
		t.Fatalf("emit audit: %v", err)
	}
	result, err := core.CloseRunSession(runSession)
	if err != nil {
		t.Fatalf("CloseRunSession: %v", err)
	}
	if result.Sessions != 1 || len(result.Removed) == 0 {
		t.Fatalf("cleanup result mismatch: %+v", result)
	}
	if _, err := os.Stat(runSession.Layout.TmpDir); !os.IsNotExist(err) {
		t.Fatalf("tmp dir should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(runSession.Layout.ShimDir); !os.IsNotExist(err) {
		t.Fatalf("shim dir should be removed, stat err=%v", err)
	}
	data, err := os.ReadFile(runSession.Layout.AuditPath)
	if err != nil {
		t.Fatalf("audit file should remain after cleanup: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"action":"test.event"`) || !strings.Contains(text, `"action":"session.cleanup"`) {
		t.Fatalf("audit should contain user event and cleanup event:\n%s", text)
	}
	if strings.Contains(text, runSession.Layout.TmpDir) || strings.Contains(text, runSession.Layout.ShimDir) {
		t.Fatalf("cleanup audit should not expose removed paths:\n%s", text)
	}
}

func TestCoreBeginRunSessionExplainSkipsEnvironmentRuntimePrepare(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "lima",
		Workspace:   t.TempDir(),
		Command:     []string{"echo", "hi"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	runEnv, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: false})
	if err != nil {
		t.Fatalf("SelectRunEnvironment: %v", err)
	}
	runSession, err := core.BeginRunSession(plan, runEnv, RunSessionOptions{ExplainOnly: true})
	if err != nil {
		t.Fatalf("BeginRunSession: %v", err)
	}
	defer func() {
		if _, err := core.CloseRunSession(runSession); err != nil {
			t.Fatalf("CloseRunSession: %v", err)
		}
	}()
	if !runSession.Environment.Active || runSession.RuntimeSessionDir != runEnv.RuntimeDir || runSession.RuntimeShimDir != runEnv.ShimDir {
		t.Fatalf("lima explain session should use selected environment dirs: session=%+v env=%+v", runSession, runEnv)
	}
	if _, err := os.Stat(runEnv.RuntimeDir); !os.IsNotExist(err) {
		t.Fatalf("explain should not prepare environment runtime dir, stat err=%v", err)
	}
}

func TestCoreExplainRunOwnsSessionLifecycle(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "lima",
		Workspace:   t.TempDir(),
		Command:     []string{"echo", "hi"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	var layoutDir string
	var runtimeDir string
	err = core.ExplainRun(plan, RunExplainOptions{
		Environment: RunEnvironmentOptions{Create: true},
	}, func(explanation RunExplanation) error {
		if explanation.Plan.ProfileName != "default" || explanation.Session.Layout.ID == "" {
			t.Fatalf("explanation mismatch: %+v", explanation)
		}
		layoutDir = explanation.Session.Layout.Dir
		runtimeDir = explanation.Session.Environment.RuntimeDir
		if !explanation.Session.Environment.Active {
			t.Fatalf("lima explain should select reusable environment: %+v", explanation.Session.Environment)
		}
		if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
			t.Fatalf("explain should not prepare environment runtime dir, stat err=%v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ExplainRun: %v", err)
	}
	if layoutDir == "" {
		t.Fatal("explain callback did not receive session layout")
	}
	if _, err := os.Stat(layoutDir); !os.IsNotExist(err) {
		t.Fatalf("ExplainRun should close and clean its session, stat err=%v", err)
	}
}

func TestCorePrepareRunNetworkDirectAuditsPlan(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "native",
		Workspace:   t.TempDir(),
		Command:     []string{"echo", "hi"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	runSession, err := core.BeginRunSession(plan, RunEnvironment{}, RunSessionOptions{})
	if err != nil {
		t.Fatalf("BeginRunSession: %v", err)
	}
	runSession, err = core.OpenRunSessionAudit(runSession, RunAuditOptions{})
	if err != nil {
		t.Fatalf("OpenRunSessionAudit: %v", err)
	}
	runNetwork, err := core.PrepareRunNetwork(runSession, RunNetworkOptions{})
	if err != nil {
		t.Fatalf("PrepareRunNetwork: %v", err)
	}
	if runNetwork.Plan.Mode != network.ModeDirect || !runNetwork.Plan.Verified || runNetwork.Plan.FailClosed {
		t.Fatalf("direct network plan mismatch: %+v", runNetwork.Plan)
	}
	if _, err := core.CloseRunSession(runSession); err != nil {
		t.Fatalf("CloseRunSession: %v", err)
	}
	data, err := os.ReadFile(runSession.Layout.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"action":"network.setup"`) || !strings.Contains(text, `"decision":"allow"`) {
		t.Fatalf("network audit missing allow decision:\n%s", text)
	}
}

func TestCorePrepareRunNetworkLimaTun2SocksHidesProxySecret(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("proxy")
	p.Network.Mode = network.ModeTun2Socks
	p.Network.ProxySecretRef = "default-proxy"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	t.Setenv(network.SecretEnvName("default-proxy"), "socks5://user:pass@127.0.0.1:1080")
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "proxy",
		Backend:     "lima",
		Workspace:   t.TempDir(),
		Command:     []string{"echo", "hi"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	runSession, err := core.BeginRunSession(plan, RunEnvironment{}, RunSessionOptions{})
	if err != nil {
		t.Fatalf("BeginRunSession: %v", err)
	}
	runSession, err = core.OpenRunSessionAudit(runSession, RunAuditOptions{})
	if err != nil {
		t.Fatalf("OpenRunSessionAudit: %v", err)
	}
	runNetwork, err := core.PrepareRunNetwork(runSession, RunNetworkOptions{})
	if err != nil {
		t.Fatalf("PrepareRunNetwork: %v", err)
	}
	if !runNetwork.Plan.RuntimeVerify || runNetwork.Plan.ProxySecretPath == "" {
		t.Fatalf("lima tun2socks should prepare runtime verification with hidden secret file: %+v", runNetwork.Plan)
	}
	secretData, err := os.ReadFile(runNetwork.Plan.ProxySecretPath)
	if err != nil {
		t.Fatalf("read proxy secret file: %v", err)
	}
	if !strings.Contains(string(secretData), "socks5://user:pass@127.0.0.1:1080") {
		t.Fatalf("proxy secret file missing configured secret")
	}
	if _, err := core.CloseRunSession(runSession); err != nil {
		t.Fatalf("CloseRunSession: %v", err)
	}
	auditData, err := os.ReadFile(runSession.Layout.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	auditText := string(auditData)
	if !strings.Contains(auditText, `"proxySecretRef":"default-proxy"`) ||
		!strings.Contains(auditText, `"decision":"audit-only"`) {
		t.Fatalf("network audit missing secret ref or runtime verification decision:\n%s", auditText)
	}
	if strings.Contains(auditText, "user:pass") || strings.Contains(auditText, "127.0.0.1:1080") {
		t.Fatalf("network audit leaked proxy secret:\n%s", auditText)
	}
}

func TestCoreStartRunDataPlaneOwnsBrokerShimsHostFSAndSessionStartAudit(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "native",
		Workspace:   t.TempDir(),
		Command:     []string{"echo", "hi"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	runSession, err := core.BeginRunSession(plan, RunEnvironment{}, RunSessionOptions{})
	if err != nil {
		t.Fatalf("BeginRunSession: %v", err)
	}
	runSession, err = core.OpenRunSessionAudit(runSession, RunAuditOptions{})
	if err != nil {
		t.Fatalf("OpenRunSessionAudit: %v", err)
	}
	runNetwork, err := core.PrepareRunNetwork(runSession, RunNetworkOptions{})
	if err != nil {
		t.Fatalf("PrepareRunNetwork: %v", err)
	}
	dataPlane, err := core.StartRunDataPlane(context.Background(), runSession, runNetwork, RunDataPlaneOptions{
		HostFSRun: hostfs.Config{Grants: []hostfs.Rule{{
			HostPath: "/Users/alice/Downloads/doc.txt",
			Scope:    hostfs.ScopeExactFile,
			Ops:      []hostfs.Op{hostfs.OpRead},
			Reason:   "test grant",
		}}},
		Opener: broker.NoopOpener{},
	})
	if err != nil {
		t.Fatalf("StartRunDataPlane: %v", err)
	}
	if dataPlane.Broker == nil || dataPlane.BrokerGuestEndpoint.Network != broker.EndpointUnix {
		t.Fatalf("broker endpoint mismatch: %+v", dataPlane)
	}
	if !dataPlane.HostFSEnabled || !reflect.DeepEqual(dataPlane.HostFSGrafts, []string{"/Users/alice/Downloads"}) {
		t.Fatalf("hostfs data plane mismatch: enabled=%v grafts=%v", dataPlane.HostFSEnabled, dataPlane.HostFSGrafts)
	}
	if !containsEnvPrefixManagerTest(dataPlane.Env, broker.EnvEndpoint+"=unix://") ||
		!containsEnvPrefixManagerTest(dataPlane.Env, broker.EnvSession+"="+runSession.Layout.ID) ||
		!containsEnvPrefixManagerTest(dataPlane.Env, broker.EnvToken+"=cap_") ||
		!containsEnvPrefixManagerTest(dataPlane.Env, broker.EnvSock+"="+runSession.Layout.BrokerSock) {
		t.Fatalf("broker env missing: %+v", dataPlane.Env)
	}
	for _, shim := range []string{"open", "xdg-open"} {
		if _, err := os.Stat(filepath.Join(runSession.RuntimeShimDir, shim)); err != nil {
			t.Fatalf("shim %s missing: %v", shim, err)
		}
	}
	endpointData, err := os.ReadFile(runSession.Layout.BrokerEndpointPath)
	if err != nil {
		t.Fatalf("broker endpoint file missing: %v", err)
	}
	if !strings.Contains(string(endpointData), `"network": "unix"`) {
		t.Fatalf("broker endpoint file mismatch:\n%s", endpointData)
	}
	if err := core.CloseRunDataPlane(dataPlane); err != nil {
		t.Fatalf("CloseRunDataPlane: %v", err)
	}
	if _, err := core.CloseRunSession(runSession); err != nil {
		t.Fatalf("CloseRunSession: %v", err)
	}
	auditData, err := os.ReadFile(runSession.Layout.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	auditText := string(auditData)
	if !strings.Contains(auditText, `"action":"session.start"`) ||
		!strings.Contains(auditText, `"brokerEndpoint":"present"`) ||
		!strings.Contains(auditText, `"brokerTransport":"unix"`) {
		t.Fatalf("session.start audit mismatch:\n%s", auditText)
	}
	if strings.Contains(auditText, "cap_") || strings.Contains(auditText, runSession.Layout.BrokerSock) {
		t.Fatalf("session.start audit leaked broker token or socket path:\n%s", auditText)
	}
}

func TestCoreApplyRunOwnsBackendExecutionAndAudit(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	fake := &applyRunFakeBackend{}
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "native",
		Workspace:   workspace,
		Ephemeral:   true,
		Command:     []string{"tool", "arg"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend:            fake,
		RequestedBackend:   "native",
		AllowWeakIsolation: true,
		Environment:        RunEnvironmentOptions{Create: true},
		HostFSRun: hostfs.Config{Grants: []hostfs.Rule{{
			HostPath: "/Users/alice/Downloads/doc.txt",
			Scope:    hostfs.ScopeExactFile,
			Ops:      []hostfs.Op{hostfs.OpRead},
			Reason:   "test grant",
		}}},
		Opener: broker.NoopOpener{},
	})
	if err != nil {
		t.Fatalf("ApplyRun: %v", err)
	}
	if !reflect.DeepEqual(fake.calls, []string{"available", "prepare", "run", "cleanup"}) {
		t.Fatalf("backend call order mismatch: %v", fake.calls)
	}
	if result.Version != RunResultVersion || result.SessionID == "" || result.Backend != "native" || result.Profile != "default" {
		t.Fatalf("result mismatch: %+v", result)
	}
	if result.BoundarySummary == nil {
		t.Fatalf("result missing boundary summary: %+v", result)
	}
	if result.BoundarySummary.AuditPath != result.AuditPath {
		t.Fatalf("boundary summary audit path mismatch: %+v result=%s", result.BoundarySummary, result.AuditPath)
	}
	if result.BoundarySummary.Evidence != "available" {
		t.Fatalf("boundary summary evidence mismatch: %+v", result.BoundarySummary)
	}
	hostFSBoundary := boundarySummaryCapability(t, *result.BoundarySummary, "hostfs")
	if hostFSBoundary.Allowed != 0 || hostFSBoundary.Denied != 0 || hostFSBoundary.Unsupported != 0 {
		t.Fatalf("unexpected HostFS boundary counts without HostFS requests: %+v", hostFSBoundary)
	}
	hostOpenBoundary := boundarySummaryCapability(t, *result.BoundarySummary, "host.open")
	if hostOpenBoundary.Allowed != 0 || hostOpenBoundary.Denied != 0 {
		t.Fatalf("unexpected host.open boundary counts without host.open requests: %+v", hostOpenBoundary)
	}
	resultData, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	resultSchema := compileRunResultSchema(t)
	resultDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(resultData))
	if err != nil {
		t.Fatalf("decode run result JSON: %v", err)
	}
	if err := resultSchema.Validate(resultDoc); err != nil {
		t.Fatalf("run result does not match schema: %v\n%s", err, resultData)
	}
	spec := fake.spec
	if spec.SessionID != result.SessionID || spec.Profile.Name != "default" || spec.HostWork != workspace || spec.GuestWork != workspace {
		t.Fatalf("run spec identity/workspace mismatch: %+v", spec)
	}
	if spec.ProfileDir != store.ProfileDir("default") {
		t.Fatalf("ProfileDir=%q want persistent profile dir", spec.ProfileDir)
	}
	if spec.IdentityMode != "ephemeral" || spec.IdentityRoot == spec.ProfileDir || filepath.Base(spec.IdentityRoot) != "identity" {
		t.Fatalf("ephemeral identity spec mismatch: mode=%q root=%q profile=%q", spec.IdentityMode, spec.IdentityRoot, spec.ProfileDir)
	}
	if spec.GuestHome != filepath.Join(spec.IdentityRoot, "home") {
		t.Fatalf("GuestHome=%q should come from identity root", spec.GuestHome)
	}
	if !spec.HostFSEnabled || !reflect.DeepEqual(spec.HostFSGrafts, []string{"/Users/alice/Downloads"}) {
		t.Fatalf("HostFS spec mismatch: enabled=%v grafts=%v", spec.HostFSEnabled, spec.HostFSGrafts)
	}
	if !containsEnvPrefixManagerTest(fake.runEnv, broker.EnvEndpoint+"=unix://") ||
		!containsEnvPrefixManagerTest(fake.runEnv, broker.EnvToken+"=cap_") ||
		!containsEnvPrefixManagerTest(fake.runEnv, "HOME="+filepath.Join(spec.IdentityRoot, "home")) {
		t.Fatalf("run env missing broker or identity values: %+v", fake.runEnv)
	}
	data, err := os.ReadFile(result.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	auditText := string(data)
	for _, action := range []string{
		"backend.selected",
		"hostfs.policy",
		"workspace.mapping",
		"env.policy",
		"command.start",
		"network.setup",
		"session.start",
		"session.end",
		"backend.cleanup",
		"session.cleanup",
	} {
		if !strings.Contains(auditText, `"action":"`+action+`"`) {
			t.Fatalf("audit missing action %s:\n%s", action, auditText)
		}
	}
	if strings.Contains(auditText, "cap_") || strings.Contains(auditText, spec.IdentityRoot) {
		t.Fatalf("audit leaked broker token or identity root:\n%s", auditText)
	}
}

func TestCoreApplyRunStartsHostToGuestPortBridgeAndAudits(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	targetAddr, closeTarget := startManagerEchoServer(t)
	defer closeTarget()
	var bridgeAddr string
	fake := &applyRunFakeBackend{
		runFunc: func(session *backend.Session) error {
			if session == nil || len(session.PortBridges) != 1 {
				return fmt.Errorf("expected one port bridge endpoint, got %+v", session)
			}
			endpoint := session.PortBridges[0]
			bridgeAddr = endpoint.ListenAddress
			if endpoint.ID != "pb_preview_1" ||
				endpoint.Owner != "preview.open" ||
				endpoint.Lifetime != "run" ||
				endpoint.Direction != "host-to-guest" ||
				endpoint.ListenScope != "loopback" ||
				endpoint.TargetScope != "guest" ||
				endpoint.EndpointCategory != "host-loopback" ||
				endpoint.ListenAddress == "" {
				return fmt.Errorf("unexpected port bridge endpoint: %+v", endpoint)
			}
			got, err := managerEchoRoundTrip(endpoint.ListenAddress, "hello")
			if err != nil {
				return err
			}
			if got != "echo:hello\n" {
				return fmt.Errorf("bridge response=%q", got)
			}
			return nil
		},
	}
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "native",
		Workspace:   workspace,
		Ephemeral:   true,
		Command:     []string{"tool"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend:            fake,
		RequestedBackend:   "native",
		AllowWeakIsolation: true,
		Environment:        RunEnvironmentOptions{Create: true},
		PortBridges: []RunPortBridgeRequest{{
			ID:            "pb_preview_1",
			Owner:         "preview.open",
			TargetAddress: targetAddr,
		}},
	})
	if err != nil {
		t.Fatalf("ApplyRun: %v", err)
	}
	if len(fake.spec.PortBridges) != 1 || len(fake.runSession.PortBridges) != 1 {
		t.Fatalf("backend did not receive port bridge lease: spec=%+v session=%+v", fake.spec.PortBridges, fake.runSession.PortBridges)
	}
	if result.BoundarySummary == nil {
		t.Fatalf("result missing boundary summary")
	}
	portBridgeBoundary := boundarySummaryCapability(t, *result.BoundarySummary, "portbridge.host-to-guest")
	if portBridgeBoundary.Allowed != 1 || portBridgeBoundary.AuditOnly != 1 || portBridgeBoundary.Denied != 0 ||
		portBridgeBoundary.Owner != "preview.open" || portBridgeBoundary.Lifetime != "run" ||
		portBridgeBoundary.EndpointCategory != "host-loopback" {
		t.Fatalf("portbridge boundary mismatch: %+v", portBridgeBoundary)
	}
	auditData, err := os.ReadFile(result.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	auditText := string(auditData)
	for _, want := range []string{
		`"action":"portbridge.host-to-guest"`,
		`"decision":"allow"`,
		`"decision":"audit-only"`,
		`"status":"ready"`,
		`"status":"cleanup"`,
		`"endpointCategory":"host-loopback"`,
	} {
		if !strings.Contains(auditText, want) {
			t.Fatalf("portbridge audit missing %q:\n%s", want, auditText)
		}
	}
	for _, leaked := range []string{targetAddr, bridgeAddr} {
		if leaked != "" && strings.Contains(auditText, leaked) {
			t.Fatalf("portbridge audit leaked endpoint %q:\n%s", leaked, auditText)
		}
	}
}

func TestCoreApplyRunExposesPreviewOpenHostToGuestEndpoint(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	targetAddr, closeTarget := startManagerEchoServer(t)
	defer closeTarget()
	var bridgeAddr string
	opener := &managerRecordingOpener{}
	fake := &applyRunFakeBackend{
		runFunc: func(session *backend.Session) error {
			if session == nil || len(session.PortBridges) != 1 {
				return fmt.Errorf("expected one endpoint exposure, got %+v", session)
			}
			endpoint := session.PortBridges[0]
			bridgeAddr = endpoint.ListenAddress
			if endpoint.Action != policy.ActionEndpointExposeHostToGuest ||
				endpoint.Owner != OpenTargetPreviewOpen ||
				endpoint.Source != EndpointSourceManual ||
				endpoint.ClosePolicy != "session-end" ||
				endpoint.TargetAddress != targetAddr ||
				endpoint.Direction != "host-to-guest" ||
				endpoint.EndpointCategory != "host-loopback" {
				return fmt.Errorf("unexpected endpoint exposure: %+v", endpoint)
			}
			got, err := managerEchoRoundTrip(endpoint.ListenAddress, "preview")
			if err != nil {
				return err
			}
			if got != "echo:preview\n" {
				return fmt.Errorf("bridge response=%q", got)
			}
			if !opener.waitForURL(2 * time.Second) {
				return fmt.Errorf("preview opener was not called while command was running")
			}
			return nil
		},
	}
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "native",
		Workspace:   workspace,
		Ephemeral:   true,
		Command:     []string{"tool"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend:            fake,
		RequestedBackend:   "native",
		AllowWeakIsolation: true,
		Environment:        RunEnvironmentOptions{Create: true},
		OpenTargets: []RunOpenTargetOwner{{
			ID:   OpenTargetPreviewOpen,
			Kind: OpenTargetPreviewOpen,
		}},
		EndpointCandidates: []RunEndpointCandidate{{
			ID:            "manual_preview_1",
			Source:        EndpointSourceManual,
			Owner:         OpenTargetPreviewOpen,
			Proto:         "tcp",
			TargetAddress: targetAddr,
		}},
		EndpointExposures: []RunEndpointExposureRequest{{
			CandidateID: "manual_preview_1",
			Owner:       OpenTargetPreviewOpen,
			Kind:        OpenTargetPreviewOpen,
		}},
		Opener: opener,
	})
	if err != nil {
		t.Fatalf("ApplyRun: %v", err)
	}
	openerURLs := opener.urlSnapshot()
	if len(openerURLs) != 1 || !strings.HasPrefix(openerURLs[0], "http://127.0.0.1:") {
		t.Fatalf("preview opener mismatch: %+v", openerURLs)
	}
	if result.BoundarySummary == nil {
		t.Fatalf("result missing boundary summary")
	}
	endpointBoundary := boundarySummaryCapability(t, *result.BoundarySummary, policy.ActionEndpointExposeHostToGuest)
	if endpointBoundary.Allowed != 1 || endpointBoundary.AuditOnly != 1 || endpointBoundary.Denied != 0 ||
		endpointBoundary.Owner != OpenTargetPreviewOpen || endpointBoundary.Source != EndpointSourceManual ||
		endpointBoundary.Lifetime != "run" || endpointBoundary.EndpointCategory != "host-loopback" ||
		endpointBoundary.CloseReason != "session-end" {
		t.Fatalf("endpoint exposure boundary mismatch: %+v", endpointBoundary)
	}
	previewBoundary := boundarySummaryCapability(t, *result.BoundarySummary, "preview.open")
	if previewBoundary.Allowed != 1 || previewBoundary.Owner != OpenTargetPreviewOpen || previewBoundary.Source != EndpointSourceManual {
		t.Fatalf("preview.open boundary mismatch: %+v", previewBoundary)
	}
	auditData, err := os.ReadFile(result.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	auditText := string(auditData)
	for _, want := range []string{
		`"action":"endpoint.expose.host-to-guest"`,
		`"action":"preview.open"`,
		`"source":"manual"`,
		`"closeReason":"session-end"`,
	} {
		if !strings.Contains(auditText, want) {
			t.Fatalf("endpoint audit missing %q:\n%s", want, auditText)
		}
	}
	for _, leaked := range []string{targetAddr, bridgeAddr, openerURLs[0]} {
		if leaked != "" && strings.Contains(auditText, leaked) {
			t.Fatalf("endpoint audit leaked %q:\n%s", leaked, auditText)
		}
	}
}

func TestCoreApplyRunRejectsEndpointExposureWithoutActiveOwner(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	fake := &applyRunFakeBackend{}
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "native",
		Workspace:   workspace,
		Command:     []string{"tool"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend:            fake,
		RequestedBackend:   "native",
		AllowWeakIsolation: true,
		Environment:        RunEnvironmentOptions{Create: true},
		EndpointCandidates: []RunEndpointCandidate{{
			ID:            "manual_preview_1",
			Source:        EndpointSourceManual,
			Owner:         OpenTargetPreviewOpen,
			Proto:         "tcp",
			TargetAddress: "127.0.0.1:5173",
		}},
		EndpointExposures: []RunEndpointExposureRequest{{
			CandidateID: "manual_preview_1",
			Owner:       OpenTargetPreviewOpen,
			Kind:        OpenTargetPreviewOpen,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), `owner "preview.open" is not active`) {
		t.Fatalf("expected inactive owner error, got %v", err)
	}
	if !reflect.DeepEqual(fake.calls, []string{"available"}) {
		t.Fatalf("backend should not prepare/run after endpoint exposure rejection: %v", fake.calls)
	}
	if result.BoundarySummary == nil {
		t.Fatalf("result missing boundary summary")
	}
	endpointBoundary := boundarySummaryCapability(t, *result.BoundarySummary, policy.ActionEndpointExposeHostToGuest)
	if endpointBoundary.Denied != 1 || endpointBoundary.Allowed != 0 || endpointBoundary.Owner != OpenTargetPreviewOpen ||
		endpointBoundary.EndpointCategory != "host-loopback" {
		t.Fatalf("endpoint exposure rejection summary mismatch: %+v", endpointBoundary)
	}
}

func TestCoreApplyRunRejectsInvalidEndpointExposureRequests(t *testing.T) {
	tests := []struct {
		name             string
		backendName      string
		requestedBackend string
		openTargets      []RunOpenTargetOwner
		candidates       []RunEndpointCandidate
		exposures        []RunEndpointExposureRequest
		wantErr          string
	}{
		{
			name:             "unknown candidate",
			backendName:      "native",
			requestedBackend: "native",
			openTargets: []RunOpenTargetOwner{{
				ID:   OpenTargetPreviewOpen,
				Kind: OpenTargetPreviewOpen,
			}},
			exposures: []RunEndpointExposureRequest{{
				CandidateID: "missing_preview",
				Owner:       OpenTargetPreviewOpen,
				Kind:        OpenTargetPreviewOpen,
			}},
			wantErr: `candidate "missing_preview" is unknown`,
		},
		{
			name:             "candidate owner mismatch",
			backendName:      "native",
			requestedBackend: "native",
			openTargets: []RunOpenTargetOwner{{
				ID:   OpenTargetPreviewOpen,
				Kind: OpenTargetPreviewOpen,
			}},
			candidates: []RunEndpointCandidate{{
				ID:            "manual_preview_1",
				Source:        EndpointSourceManual,
				Owner:         "other.open",
				Proto:         "tcp",
				TargetAddress: "127.0.0.1:5173",
			}},
			exposures: []RunEndpointExposureRequest{{
				CandidateID: "manual_preview_1",
				Owner:       OpenTargetPreviewOpen,
				Kind:        OpenTargetPreviewOpen,
			}},
			wantErr: `belongs to owner "other.open"`,
		},
		{
			name:             "owner kind mismatch",
			backendName:      "native",
			requestedBackend: "native",
			openTargets: []RunOpenTargetOwner{{
				ID:   OpenTargetPreviewOpen,
				Kind: "other.open",
			}},
			candidates: []RunEndpointCandidate{{
				ID:            "manual_preview_1",
				Source:        EndpointSourceManual,
				Owner:         OpenTargetPreviewOpen,
				Proto:         "tcp",
				TargetAddress: "127.0.0.1:5173",
			}},
			exposures: []RunEndpointExposureRequest{{
				CandidateID: "manual_preview_1",
				Owner:       OpenTargetPreviewOpen,
				Kind:        OpenTargetPreviewOpen,
			}},
			wantErr: `kind "other.open" does not match "preview.open"`,
		},
		{
			name:             "lima backend without provider",
			backendName:      "lima",
			requestedBackend: "lima",
			openTargets: []RunOpenTargetOwner{{
				ID:   OpenTargetPreviewOpen,
				Kind: OpenTargetPreviewOpen,
			}},
			candidates: []RunEndpointCandidate{{
				ID:            "manual_preview_1",
				Source:        EndpointSourceManual,
				Owner:         OpenTargetPreviewOpen,
				Proto:         "tcp",
				TargetAddress: "127.0.0.1:5173",
			}},
			exposures: []RunEndpointExposureRequest{{
				CandidateID: "manual_preview_1",
				Owner:       OpenTargetPreviewOpen,
				Kind:        OpenTargetPreviewOpen,
			}},
			wantErr: "does not provide run-scoped host-to-guest bridge provider",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := profile.Store{Root: t.TempDir()}
			core := New(store)
			fake := &applyRunFakeBackend{}
			plan, err := core.PlanRun(RunPlanOptions{
				ProfileName: "default",
				Backend:     tt.backendName,
				Workspace:   t.TempDir(),
				Command:     []string{"tool"},
			})
			if err != nil {
				t.Fatalf("PlanRun: %v", err)
			}
			if tt.backendName == "lima" {
				shimPath := filepath.Join(t.TempDir(), "hideout-shim-linux")
				if err := os.WriteFile(shimPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
					t.Fatalf("write fake linux shim: %v", err)
				}
				t.Setenv("HIDEOUT_LINUX_SHIM_PATH", shimPath)
			}
			result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
				Backend:                    fake,
				RequestedBackend:           tt.requestedBackend,
				AllowWeakIsolation:         tt.backendName == "native",
				Environment:                RunEnvironmentOptions{Create: true},
				OpenTargets:                tt.openTargets,
				EndpointCandidates:         tt.candidates,
				EndpointExposures:          tt.exposures,
				Opener:                     broker.NoopOpener{},
				OpenerForSession:           nil,
				PortBridges:                nil,
				Network:                    RunNetworkOptions{},
				HostFSRun:                  hostfs.Config{},
				DisableProfileHostFSGrants: false,
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected %q error, got %v", tt.wantErr, err)
			}
			if !reflect.DeepEqual(fake.calls, []string{"available"}) {
				t.Fatalf("backend should not prepare/run after endpoint exposure rejection: %v", fake.calls)
			}
			if result.BoundarySummary == nil {
				t.Fatalf("result missing boundary summary")
			}
			endpointBoundary := boundarySummaryCapability(t, *result.BoundarySummary, policy.ActionEndpointExposeHostToGuest)
			if endpointBoundary.Denied != 1 || endpointBoundary.Allowed != 0 {
				t.Fatalf("endpoint exposure rejection summary mismatch: %+v", endpointBoundary)
			}
		})
	}
}

func TestBoundarySummaryAggregatesAuditWithoutSensitiveDetails(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	aw, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	events := []audit.Event{
		{
			Action:   "host.fs.read",
			Decision: "allow",
			Details: map[string]any{
				"status":       "ok",
				"policyEffect": "allow",
				"path":         "/Users/alice/Downloads/allowed.txt",
				"brokerToken":  "cap_secret_value",
			},
		},
		{
			Action: "host.fs.list",
			Details: map[string]any{
				"status":       "denied",
				"policyEffect": "deny",
				"path":         "/Users/alice/Downloads/private",
			},
		},
		{
			Action: "host.fs.stat",
			Details: map[string]any{
				"status":       "denied",
				"policyEffect": "none",
				"path":         "/Users/alice/.ssh/id_rsa",
			},
		},
		{
			Action: "host.fs.write",
			Details: map[string]any{
				"status":       "denied",
				"policyEffect": "unsupported",
				"path":         "/Users/alice/Downloads/allowed.txt",
			},
		},
		{
			Action:   "host.open",
			Decision: "allow",
			Details: map[string]any{
				"status": "ok",
				"target": "https://example.com/?token=secret",
			},
		},
		{
			Action:   "host.open",
			Decision: "deny",
			Details: map[string]any{
				"status": "denied",
				"target": "http://127.0.0.1:3000",
			},
		},
		{
			Action:   "host.open",
			Decision: "deny",
			Details: map[string]any{
				"status": "error",
				"target": "https://example.com",
			},
		},
		{
			Action:   "portbridge.host-to-guest",
			Decision: "allow",
			Details: map[string]any{
				"owner":            "preview.open",
				"lifetime":         "run",
				"endpointCategory": "host-loopback",
				"endpoint":         "127.0.0.1:49152",
			},
		},
	}
	for _, event := range events {
		if err := aw.Emit(event); err != nil {
			t.Fatalf("emit audit: %v", err)
		}
	}
	if err := aw.Close(); err != nil {
		t.Fatalf("close audit: %v", err)
	}

	summary := SummarizeRunBoundary(auditPath)
	if summary.Version != BoundarySummaryVersion || summary.AuditPath != auditPath {
		t.Fatalf("summary identity mismatch: %+v", summary)
	}
	if summary.Evidence != "available" {
		t.Fatalf("summary evidence mismatch: %+v", summary)
	}
	hostFSBoundary := boundarySummaryCapability(t, summary, "hostfs")
	if hostFSBoundary.Allowed != 1 || hostFSBoundary.Denied != 2 || hostFSBoundary.Unsupported != 1 {
		t.Fatalf("HostFS boundary counts mismatch: %+v", hostFSBoundary)
	}
	hostOpenBoundary := boundarySummaryCapability(t, summary, "host.open")
	if hostOpenBoundary.Allowed != 1 || hostOpenBoundary.Denied != 1 || hostOpenBoundary.Error != 1 {
		t.Fatalf("host.open boundary counts mismatch: %+v", hostOpenBoundary)
	}
	portBridgeBoundary := boundarySummaryCapability(t, summary, "portbridge.host-to-guest")
	if portBridgeBoundary.Allowed != 1 || portBridgeBoundary.Owner != "preview.open" ||
		portBridgeBoundary.Lifetime != "run" || portBridgeBoundary.EndpointCategory != "host-loopback" {
		t.Fatalf("portbridge boundary shape mismatch: %+v", portBridgeBoundary)
	}
	summaryData, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	for _, leaked := range []string{
		"/Users/alice",
		"cap_secret_value",
		"127.0.0.1:49152",
		"https://example.com",
	} {
		if strings.Contains(string(summaryData), leaked) {
			t.Fatalf("boundary summary leaked %q:\n%s", leaked, summaryData)
		}
	}
}

func TestBoundarySummaryReportsDisabledAuditAsNoEvidence(t *testing.T) {
	summary := SummarizeRunBoundary("off")
	if summary.Evidence != "disabled" || summary.AuditPath != "off" {
		t.Fatalf("disabled summary mismatch: %+v", summary)
	}
	hostFSBoundary := boundarySummaryCapability(t, summary, "hostfs")
	hostOpenBoundary := boundarySummaryCapability(t, summary, "host.open")
	if hostFSBoundary.Allowed != 0 || hostFSBoundary.Denied != 0 ||
		hostOpenBoundary.Allowed != 0 || hostOpenBoundary.Denied != 0 {
		t.Fatalf("disabled audit should not imply observed zero-count evidence: %+v %+v", hostFSBoundary, hostOpenBoundary)
	}
}

func TestBoundarySummaryCountsRealBrokerHostFSDenyAudit(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	aw, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	policy, err := hostfs.Build(hostfs.BuildInput{Run: hostfs.Config{}})
	if err != nil {
		t.Fatalf("Build HostFS policy: %v", err)
	}
	service := hostfs.NewService(policy)
	server := &broker.Server{
		SessionID: "ses_test",
		Token:     "cap_test",
		Profile:   "default",
		Backend:   "native",
		Audit:     aw,
		HostFS:    &service,
	}
	resp := server.Handle(context.Background(), broker.Request{
		ID:              "req_hostfs_denied",
		SessionID:       "ses_test",
		CapabilityToken: "cap_test",
		Subject:         "hostfs:daemon",
		Route:           "host-broker",
		Action:          "host.fs.stat",
		Args: map[string]any{
			"path": "/Users/alice/Documents/not-granted.txt",
		},
	})
	if resp.Decision != "deny" || resp.Status != "denied" {
		t.Fatalf("HostFS broker response mismatch: %+v", resp)
	}
	if err := aw.Close(); err != nil {
		t.Fatalf("close audit: %v", err)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	auditText := string(data)
	for _, want := range []string{
		`"action":"host.fs.stat"`,
		`"decision":"deny"`,
		`"policyEffect":"none"`,
		`"policyReason":"no-matching-grant"`,
	} {
		if !strings.Contains(auditText, want) {
			t.Fatalf("real broker HostFS audit missing %q:\n%s", want, auditText)
		}
	}
	summary := SummarizeRunBoundary(auditPath)
	hostFSBoundary := boundarySummaryCapability(t, summary, "hostfs")
	if hostFSBoundary.Allowed != 0 || hostFSBoundary.Denied != 1 || hostFSBoundary.Unsupported != 0 {
		t.Fatalf("real broker HostFS deny summary mismatch: %+v audit=%s", hostFSBoundary, auditText)
	}
}

func TestCoreApplyRunAppliesPendingSafeInitBeforeBackend(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	fake := &applyRunFakeBackend{}
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "native",
		Workspace:   t.TempDir(),
		Command:     []string{"tool"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	statePath := filepath.Join(store.Root, "install-state.json")
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("install state should be missing before ApplyRun, stat err=%v", err)
	}
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend:            fake,
		RequestedBackend:   "native",
		AllowWeakIsolation: true,
		Environment:        RunEnvironmentOptions{Create: true},
		Opener:             broker.NoopOpener{},
	})
	if err != nil {
		t.Fatalf("ApplyRun: %v", err)
	}
	if result.SessionID == "" || !reflect.DeepEqual(fake.calls, []string{"available", "prepare", "run", "cleanup"}) {
		t.Fatalf("run did not reach backend correctly: result=%+v calls=%v", result, fake.calls)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("ApplyRun should repair install state before backend execution: %v", err)
	}
	initAuditPath := inittask.DefaultAuditPath(store.Root)
	initAudit, err := os.ReadFile(initAuditPath)
	if err != nil {
		t.Fatalf("read run init audit: %v", err)
	}
	initAuditText := string(initAudit)
	if !strings.Contains(initAuditText, `"operation":"run.init.apply"`) ||
		!strings.Contains(initAuditText, `"taskKind":"schema.metadata.write"`) {
		t.Fatalf("run init audit missing metadata repair:\n%s", initAuditText)
	}
}

func TestCoreApplyRunDoesNotLetCleanupErrorOverrideRunError(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	runErr := errors.New("run failed")
	cleanupErr := errors.New("cleanup failed")
	fake := &applyRunFakeBackend{runErr: runErr, cleanupErr: cleanupErr}
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "native",
		Workspace:   t.TempDir(),
		Command:     []string{"tool"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	result, err := core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend:            fake,
		RequestedBackend:   "native",
		AllowWeakIsolation: true,
		Environment:        RunEnvironmentOptions{Create: true},
		Opener:             broker.NoopOpener{},
	})
	if !errors.Is(err, runErr) {
		t.Fatalf("ApplyRun error=%v want run error", err)
	}
	if result.Error != runErr.Error() || result.CleanupError != cleanupErr.Error() {
		t.Fatalf("result should preserve run and cleanup errors: %+v", result)
	}
	if !reflect.DeepEqual(fake.calls, []string{"available", "prepare", "run", "cleanup"}) {
		t.Fatalf("backend call order mismatch: %v", fake.calls)
	}
}

func TestCoreRunEnvironmentLifecycleUpdatesStatusAndClearsRuntime(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "lima",
		Workspace:   workspace,
		Command:     []string{"sh", "-c", "true"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	runEnv, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatalf("SelectRunEnvironment: %v", err)
	}
	if !runEnv.Active || !runEnv.Created || !runEnv.PreserveInstance || runEnv.RemoveAfterRun {
		t.Fatalf("unexpected selected environment: %+v", runEnv)
	}
	if err := core.PrepareRunEnvironment(runEnv); err != nil {
		t.Fatalf("PrepareRunEnvironment: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runEnv.ShimDir, "stale-shim"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	runEnv, err = core.StartRunEnvironment(runEnv, "ses_test", plan.Command)
	if err != nil {
		t.Fatalf("StartRunEnvironment: %v", err)
	}
	if runEnv.Record.Status != "running" || runEnv.Record.LastSessionID != "ses_test" || runEnv.Record.LastCommand != "sh -c true" {
		t.Fatalf("start metadata mismatch: %+v", runEnv.Record)
	}
	runEnv, err = core.FinishRunEnvironment(runEnv, nil)
	if err != nil {
		t.Fatalf("FinishRunEnvironment: %v", err)
	}
	if runEnv.Record.Status != "ready" || runEnv.Record.LastEndedAt.IsZero() {
		t.Fatalf("finish metadata mismatch: %+v", runEnv.Record)
	}
	loaded, err := (environment.Store{Root: store.Root}).Load(runEnv.Record.ID)
	if err != nil {
		t.Fatalf("load environment: %v", err)
	}
	if loaded.Status != "ready" || loaded.LastCommand != "sh -c true" || loaded.LastSessionID != "ses_test" {
		t.Fatalf("persisted environment mismatch: %+v", loaded)
	}
	if entries, err := os.ReadDir(runEnv.ShimDir); err != nil {
		t.Fatalf("read shim dir: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("runtime shims should be cleared after finish, got %v", entries)
	}
}

func TestCoreApplyRunRejectsLockedEnvironment(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	core := New(store)
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default",
		Backend:     "lima",
		Workspace:   workspace,
		Command:     []string{"sh", "-c", "true"},
	})
	if err != nil {
		t.Fatalf("PlanRun: %v", err)
	}
	runEnv, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatalf("SelectRunEnvironment: %v", err)
	}
	lock, err := (environment.Store{Root: store.Root}).Lock(runEnv.Record.ID)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer lock.Unlock()

	fake := &applyRunFakeBackend{}
	_, err = core.ApplyRun(context.Background(), plan, ApplyRunOptions{
		Backend:     fake,
		Environment: RunEnvironmentOptions{Create: true},
		Opener:      broker.NoopOpener{},
	})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("locked environment should fail closed, got %v", err)
	}
	for _, call := range fake.calls {
		if call == "prepare" || call == "run" || call == "cleanup" {
			t.Fatalf("locked environment must not reach backend execution, calls=%v", fake.calls)
		}
	}
}

func TestCoreSelectRunEnvironmentResumeRemoveDoesNotPreserveInstance(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("default")
	spec := RunEnvironmentSpec(p, "lima", t.TempDir(), "/workspace")
	envStore := environment.Store{Root: store.Root}
	rec, err := envStore.Create(spec)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec.InstanceName = "hideout-default-env-test"
	if err := envStore.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	selected, err := New(store).SelectRunEnvironment(RunPlan{
		Backend:        "lima",
		Workspace:      spec.Workspace,
		GuestWorkspace: spec.GuestWorkspace,
		RuntimeProfile: p,
	}, RunEnvironmentOptions{
		ResumeID:       rec.ID,
		RemoveAfterRun: true,
		Create:         true,
	})
	if err != nil {
		t.Fatalf("SelectRunEnvironment: %v", err)
	}
	if !selected.Active || !selected.RemoveAfterRun || selected.PreserveInstance {
		t.Fatalf("resume --rm should delete environment after run: %+v", selected)
	}
}

func TestCoreSelectRunEnvironmentToolChangesCreateNewEnvironment(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	p := profile.Default("default")
	envStore := environment.Store{Root: store.Root}
	rec, err := envStore.Create(RunEnvironmentSpec(p, "lima", workspace, workspace))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec.InstanceName = "hideout-default-env-oldtools"
	if err := envStore.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	withTool := p
	withTool.Tools.Presets = append(withTool.Tools.Presets, "node-dev")
	selected, err := New(store).SelectRunEnvironment(RunPlan{
		Backend:        "lima",
		Workspace:      workspace,
		GuestWorkspace: workspace,
		RuntimeProfile: withTool,
	}, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatalf("SelectRunEnvironment: %v", err)
	}
	if !selected.Created || selected.Record.ID == rec.ID {
		t.Fatalf("tool change should create a new environment, got %+v old=%s", selected, rec.ID)
	}
	if selected.Record.ToolsHash == "" || selected.Record.ToolsHash == rec.ToolsHash {
		t.Fatalf("environment should persist a distinct tool fingerprint: new=%q old=%q", selected.Record.ToolsHash, rec.ToolsHash)
	}

	_, err = New(store).SelectRunEnvironment(RunPlan{
		Backend:        "lima",
		Workspace:      workspace,
		GuestWorkspace: workspace,
		RuntimeProfile: withTool,
	}, RunEnvironmentOptions{ResumeID: rec.ID, Create: true})
	if err == nil || !strings.Contains(err.Error(), "tools no longer match") {
		t.Fatalf("resume of stale tool environment should fail closed, got %v", err)
	}
}

func TestCoreSelectRunEnvironmentBackendConfigChangesCreateNewEnvironment(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	p := profile.Default("default")
	currentSpec := RunEnvironmentSpec(p, "lima", workspace, workspace)
	oldSpec := currentSpec
	oldSpec.BackendConfigVersion = "lima-config/old"

	envStore := environment.Store{Root: store.Root}
	rec, err := envStore.Create(oldSpec)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec.InstanceName = "hideout-default-env-oldconfig"
	if err := envStore.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	selected, err := New(store).SelectRunEnvironment(RunPlan{
		Backend:        "lima",
		Workspace:      workspace,
		GuestWorkspace: workspace,
		RuntimeProfile: p,
	}, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatalf("SelectRunEnvironment: %v", err)
	}
	if !selected.Created || selected.Record.ID == rec.ID {
		t.Fatalf("backend config change should create a new environment, got %+v old=%s", selected, rec.ID)
	}
	if selected.Record.BackendConfigVersion != currentSpec.BackendConfigVersion {
		t.Fatalf("environment should persist backend config version: new=%q current=%q", selected.Record.BackendConfigVersion, currentSpec.BackendConfigVersion)
	}

	_, err = New(store).SelectRunEnvironment(RunPlan{
		Backend:        "lima",
		Workspace:      workspace,
		GuestWorkspace: workspace,
		RuntimeProfile: p,
	}, RunEnvironmentOptions{ResumeID: rec.ID, Create: true})
	if err == nil || !strings.Contains(err.Error(), "backend config no longer matches") {
		t.Fatalf("resume old backend config should fail closed, got %v", err)
	}
}

func TestOverviewSummarizesDomainsWithoutSecretValues(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("default")
	p.Network.Mode = network.ModeTun2Socks
	p.Network.ProxySecretRef = "default-proxy"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}

	sessionDir := filepath.Join(store.Root, "sessions", "ses_test")
	mustWriteManagerTest(t, filepath.Join(sessionDir, "audit.jsonl"), `{"action":"session.start"}`+"\n", 0o600)
	writeJSONManagerTest(t, filepath.Join(sessionDir, "broker-endpoint.json"), broker.UnixEndpoint(filepath.Join(sessionDir, "broker.sock")))
	writeJSONManagerTest(t, filepath.Join(sessionDir, "network-plan.json"), network.Plan{
		Mode:           network.ModeTun2Socks,
		Engine:         network.ModeTun2Socks,
		ProxySecretRef: "default-proxy",
		Verified:       true,
	})
	mustWriteManagerTest(t, filepath.Join(sessionDir, "network", "proxy.url"), "socks5://user:pass@127.0.0.1:1080\n", 0o600)
	if err := os.MkdirAll(filepath.Join(sessionDir, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}

	core := Core{
		Store: store,
		Backends: []BackendCheck{
			{Name: "native", Isolation: "weak"},
		},
		SecretEnv: []string{
			network.SecretEnvName("default-proxy") + "=socks5://user:pass@127.0.0.1:1080",
		},
	}
	overview, err := core.Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}

	if overview.Version != "hideout.manager/v1" || overview.StorageRoot != store.Root {
		t.Fatalf("unexpected overview header: %+v", overview)
	}
	if len(overview.Profiles) != 1 {
		t.Fatalf("profiles=%+v", overview.Profiles)
	}
	prof := overview.Profiles[0]
	if prof.Name != "default" || prof.ProfileID == "" || prof.IdentityID == "" {
		t.Fatalf("profile identity summary missing: %+v", prof)
	}
	if prof.NetworkMode != network.ModeTun2Socks || prof.ProxySecretRef != "default-proxy" {
		t.Fatalf("profile network summary mismatch: %+v", prof)
	}
	if prof.ProxyEnvVisible {
		t.Fatalf("proxy env must not be visible in default profile summary: %+v", prof)
	}
	assertContainsManagerTest(t, prof.ToolPresets, "base-dev")
	assertContainsManagerTest(t, prof.CommandProxies, "open")
	assertContainsManagerTest(t, prof.CommandProxies, "xdg-open")
	assertContainsManagerTest(t, overview.Capabilities.MaxCapabilities, "host.open")
	assertContainsManagerTest(t, overview.Capabilities.MaxCapabilities, "guest.exec")
	if overview.Capabilities.HostOpen.Mode != "brokered" ||
		!overview.Capabilities.HostOpen.AllowURLs ||
		overview.Capabilities.HostOpen.URLScope != "external-http-https-only" ||
		overview.Capabilities.HostOpen.LocalNetworkPolicy != "deny-host-local-private-cgnat-benchmark-link-local-multicast" ||
		!overview.Capabilities.HostOpen.AllowWorkspaceFiles ||
		overview.Capabilities.HostOpen.BrowserProfile != "isolated" ||
		overview.Capabilities.HostOpen.BrowserControl != "none" ||
		overview.Capabilities.HostOpen.Profiles != 1 {
		t.Fatalf("host.open capability summary mismatch: %+v", overview.Capabilities.HostOpen)
	}
	assertContainsManagerTest(t, overview.Broker.Actions, "host.open")
	assertContainsManagerTest(t, overview.Broker.CommandProxies, "open")
	assertContainsManagerTest(t, overview.Broker.CommandProxies, "xdg-open")
	if len(overview.Capabilities.CommandProxies) != 1 || overview.Capabilities.CommandProxies[0].Name != "open" {
		t.Fatalf("command proxy registrations mismatch: %+v", overview.Capabilities.CommandProxies)
	}
	if len(overview.Backends) != 1 || !overview.Backends[0].Available || overview.Backends[0].Name != "native" {
		t.Fatalf("backend summary mismatch: %+v", overview.Backends)
	}
	if overview.Audit.SessionAuditFiles != 1 {
		t.Fatalf("audit summary mismatch: %+v", overview.Audit)
	}
	if len(overview.Network.ProfileDefaults) != 1 || overview.Network.ProfileDefaults[0].ProxySecretRef != "default-proxy" || overview.Network.ProfileDefaults[0].ProxyEnvVisible {
		t.Fatalf("network summary mismatch: %+v", overview.Network)
	}
	if len(overview.Secrets) != 1 || overview.Secrets[0].Ref != "default-proxy" || !overview.Secrets[0].Available {
		t.Fatalf("secret summary mismatch: %+v", overview.Secrets)
	}
	if len(overview.Sessions) != 1 {
		t.Fatalf("sessions=%+v", overview.Sessions)
	}
	ses := overview.Sessions[0]
	if !ses.HasAudit || !ses.HasBrokerEndpoint || !ses.HasNetworkPlan || !ses.HasProxySecretFile || !ses.HasEphemeralState {
		t.Fatalf("session flags mismatch: %+v", ses)
	}
	if ses.NetworkMode != network.ModeTun2Socks {
		t.Fatalf("session details mismatch: %+v", ses)
	}

	data, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "user:pass") || strings.Contains(text, "127.0.0.1:1080") {
		t.Fatalf("overview leaked proxy secret: %s", text)
	}
	if strings.Contains(text, "brokerEndpoint") || strings.Contains(text, "broker.sock") {
		t.Fatalf("overview should expose broker endpoint presence, not endpoint address: %s", text)
	}
	if !strings.Contains(text, `"proxyEnvVisible":false`) {
		t.Fatalf("overview should expose proxy env visibility as an explicit leak check: %s", text)
	}
}

func TestOverviewReportsInvalidProfileButKeepsSnapshot(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	badProfile := filepath.Join(store.Root, "profiles", "bad", "profile.json")
	mustWriteManagerTest(t, badProfile, `{"schemaVersion":"hideout.profile/v1","name":"","network":{"mode":"direct"},"policy":{"engine":"builtin+goja"}}`+"\n", 0o600)

	core := Core{
		Store: store,
		Backends: []BackendCheck{
			{Name: "native", Isolation: "weak"},
		},
	}
	overview, err := core.Overview(context.Background())
	if err == nil {
		t.Fatal("expected invalid profile error")
	}
	if !strings.Contains(err.Error(), "profile name") {
		t.Fatalf("unexpected invalid profile error: %v", err)
	}
	if len(overview.Profiles) != 1 {
		t.Fatalf("profiles=%+v", overview.Profiles)
	}
	if overview.Profiles[0].Name != "bad" || overview.Profiles[0].ValidationError == "" {
		t.Fatalf("invalid profile summary missing validation error: %+v", overview.Profiles[0])
	}
	if len(overview.Secrets) != 0 || len(overview.Network.ProfileDefaults) != 0 {
		t.Fatalf("invalid profile should not contribute policy summaries: secrets=%+v network=%+v", overview.Secrets, overview.Network)
	}
}

func TestOverviewSkipsNonSessionDirectories(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	mustWriteManagerTest(t, filepath.Join(store.Root, "sessions", "ses_valid", "audit.jsonl"), `{"action":"session.start"}`+"\n", 0o600)
	mustWriteManagerTest(t, filepath.Join(store.Root, "sessions", "not-a-session", "audit.jsonl"), `{"action":"session.start"}`+"\n", 0o600)
	mustWriteManagerTest(t, filepath.Join(store.Root, "sessions", "not-a-session", "network", "proxy.url"), "socks5://user:pass@127.0.0.1:1080\n", 0o600)

	overview, err := Core{Store: store}.Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(overview.Sessions) != 1 || overview.Sessions[0].ID != "ses_valid" {
		t.Fatalf("overview should include only valid session dirs: %+v", overview.Sessions)
	}
	if overview.Audit.SessionAuditFiles != 1 {
		t.Fatalf("audit summary should count only valid session dirs: %+v", overview.Audit)
	}
	data, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "not-a-session") || strings.Contains(string(data), "user:pass") {
		t.Fatalf("overview leaked non-session state: %s", data)
	}
}

func TestAuditEventsFiltersAndRedactsJSONL(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	mustWriteManagerTest(t, filepath.Join(store.Root, "sessions", "ses_1", "audit.jsonl"), strings.Join([]string{
		`{"time":"2026-07-01T00:00:00Z","session":"ses_1","profile":"default","backend":"native","action":"host.open","decision":"allow","details":{"target":"https://user:pass@example.com/path?token=abc","capabilityToken":"cap_secret","identityId":"id_traceable","sourceIdentityId":"id_source","machineId":"0123456789abcdef0123456789abcdef","message":"guest machine-id 0123456789abcdef0123456789abcdef is ready","headerDump":"Authorization: Bearer tok_123\nX-API-Key: sk-123\nCookie: sid=abc"}}`,
		`{"time":"2026-07-01T00:00:01Z","session":"ses_1","profile":"default","backend":"native","action":"network.setup","decision":"allow","details":{"proxySecretRef":"default-proxy"}}`,
	}, "\n")+"\n", 0o600)
	mustWriteManagerTest(t, filepath.Join(store.Root, "sessions", "ses_2", "audit.jsonl"), `{"time":"2026-07-01T00:00:02Z","session":"ses_2","profile":"other","backend":"native","action":"host.open","decision":"deny","details":{"target":"https://example.com"}}`+"\n", 0o600)

	events, err := Core{Store: store}.AuditEvents(AuditEventFilter{
		Session:  "ses_1",
		Action:   "host.open",
		Decision: "allow",
	})
	if err != nil {
		t.Fatalf("AuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%+v", events)
	}
	event := events[0]
	if event.Session != "ses_1" || event.Action != "host.open" || event.Decision != "allow" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Details["target"] != "https://example.com/path?token=REDACTED" {
		t.Fatalf("target should be redacted: %+v", event.Details)
	}
	if event.Details["capabilityToken"] != "REDACTED" {
		t.Fatalf("capability token should be redacted: %+v", event.Details)
	}
	if event.Details["identityId"] != "id_traceable" || event.Details["sourceIdentityId"] != "id_source" {
		t.Fatalf("identity lineage IDs should be preserved: %+v", event.Details)
	}
	if event.Details["machineId"] != "REDACTED" {
		t.Fatalf("raw machine-id should be redacted: %+v", event.Details)
	}
	if event.Details["message"] != "guest machine-id REDACTED is ready" {
		t.Fatalf("string machine-id should be redacted: %+v", event.Details)
	}
	if event.Details["headerDump"] != "Authorization: Bearer REDACTED\nX-API-Key: REDACTED\nCookie: REDACTED" {
		t.Fatalf("header dump should be redacted: %+v", event.Details)
	}
}

func TestAuditEventsSkipsNonSessionDirectoriesAndRejectsInvalidFilter(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	mustWriteManagerTest(t, filepath.Join(store.Root, "sessions", "ses_valid", "audit.jsonl"), `{"time":"2026-07-01T00:00:00Z","session":"ses_valid","profile":"default","backend":"native","action":"session.start","decision":"allow"}`+"\n", 0o600)
	mustWriteManagerTest(t, filepath.Join(store.Root, "sessions", "not-a-session", "audit.jsonl"), `{"time":"2026-07-01T00:00:01Z","session":"not-a-session","profile":"default","backend":"native","action":"host.open","decision":"allow","details":{"target":"https://user:pass@example.com"}}`+"\n", 0o600)

	events, err := Core{Store: store}.AuditEvents(AuditEventFilter{})
	if err != nil {
		t.Fatalf("AuditEvents: %v", err)
	}
	if len(events) != 1 || events[0].Session != "ses_valid" {
		t.Fatalf("audit events should include only valid session dirs: %+v", events)
	}
	if _, err := (Core{Store: store}).AuditEvents(AuditEventFilter{Session: "not-a-session"}); err == nil {
		t.Fatal("expected invalid session filter to fail")
	}
}

func writeJSONManagerTest(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteManagerTest(t, path, string(append(data, '\n')), 0o600)
}

func mustWriteManagerTest(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func assertContainsManagerTest(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%q not found in %+v", want, values)
}

func containsEnvPrefixManagerTest(env []string, prefix string) bool {
	for _, value := range env {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func startManagerEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
					return
				}
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					return
				}
				_, _ = fmt.Fprintf(conn, "echo:%s", line)
			}()
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func managerEchoRoundTrip(addr, message string) (string, error) {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintln(conn, message); err != nil {
		return "", err
	}
	return bufio.NewReader(conn).ReadString('\n')
}

type applyRunFakeBackend struct {
	availableErr error
	prepareErr   error
	runErr       error
	cleanupErr   error
	calls        []string
	spec         backend.RunSpec
	runFunc      func(*backend.Session) error
	runSession   backend.Session
	runCommand   []string
	runEnv       []string
}

type managerRecordingOpener struct {
	mu    sync.Mutex
	urls  []string
	files []string
}

func (o *managerRecordingOpener) OpenURL(_ context.Context, target string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.urls = append(o.urls, target)
	return nil
}

func (o *managerRecordingOpener) OpenFile(_ context.Context, target string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.files = append(o.files, target)
	return nil
}

func (o *managerRecordingOpener) urlSnapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.urls...)
}

func (o *managerRecordingOpener) waitForURL(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(o.urlSnapshot()) > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return len(o.urlSnapshot()) > 0
}

func (b *applyRunFakeBackend) Name() string {
	return "native"
}

func (b *applyRunFakeBackend) Available(context.Context) error {
	b.calls = append(b.calls, "available")
	return b.availableErr
}

func (b *applyRunFakeBackend) Prepare(_ context.Context, spec backend.RunSpec) (*backend.Session, error) {
	b.calls = append(b.calls, "prepare")
	b.spec = spec
	if b.prepareErr != nil {
		return nil, b.prepareErr
	}
	return &backend.Session{
		ID:                        spec.SessionID,
		EnvironmentID:             spec.EnvironmentID,
		Backend:                   b.Name(),
		HostWork:                  spec.HostWork,
		GuestWork:                 spec.GuestWork,
		GuestHome:                 spec.GuestHome,
		Env:                       append([]string(nil), spec.Env...),
		ShimDir:                   spec.ShimDir,
		ProfileDir:                spec.ProfileDir,
		IdentityMode:              spec.IdentityMode,
		IdentityRoot:              spec.IdentityRoot,
		SessionDir:                spec.SessionDir,
		Broker:                    spec.Broker,
		NetworkBootstrapPath:      spec.NetworkBootstrapPath,
		NetworkBootstrapGuestPath: spec.NetworkBootstrapGuestPath,
		NetworkCleanupPath:        spec.NetworkCleanupPath,
		NetworkCleanupGuestPath:   spec.NetworkCleanupGuestPath,
		HostFSEnabled:             spec.HostFSEnabled,
		HostFSGrafts:              append([]string(nil), spec.HostFSGrafts...),
		PortBridges:               append([]backend.PortBridgeEndpoint(nil), spec.PortBridges...),
		InstanceName:              spec.InstanceName,
		PreserveInstance:          spec.PreserveInstance,
	}, nil
}

func (b *applyRunFakeBackend) Run(_ context.Context, session *backend.Session, command []string, env []string) error {
	b.calls = append(b.calls, "run")
	if session != nil {
		b.runSession = *session
		b.runSession.Env = append([]string(nil), session.Env...)
		b.runSession.HostFSGrafts = append([]string(nil), session.HostFSGrafts...)
		b.runSession.PortBridges = append([]backend.PortBridgeEndpoint(nil), session.PortBridges...)
	}
	b.runCommand = append([]string(nil), command...)
	b.runEnv = append([]string(nil), env...)
	if b.runFunc != nil {
		return b.runFunc(session)
	}
	return b.runErr
}

func (b *applyRunFakeBackend) Cleanup(context.Context, *backend.Session) error {
	b.calls = append(b.calls, "cleanup")
	return b.cleanupErr
}

func compileRunPlanSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "run-plan.schema.json"))
	if err != nil {
		t.Fatalf("read run plan schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode run plan schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("run-plan.schema.json", doc); err != nil {
		t.Fatalf("add run plan schema: %v", err)
	}
	schema, err := compiler.Compile("run-plan.schema.json")
	if err != nil {
		t.Fatalf("compile run plan schema: %v", err)
	}
	return schema
}

func compileRunResultSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "run-result.schema.json"))
	if err != nil {
		t.Fatalf("read run result schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode run result schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("run-result.schema.json", doc); err != nil {
		t.Fatalf("add run result schema: %v", err)
	}
	schema, err := compiler.Compile("run-result.schema.json")
	if err != nil {
		t.Fatalf("compile run result schema: %v", err)
	}
	return schema
}

func boundarySummaryCapability(t *testing.T, summary BoundarySummary, capability string) BoundaryCapabilitySummary {
	t.Helper()
	for _, item := range summary.Capabilities {
		if item.Capability == capability {
			return item
		}
	}
	t.Fatalf("boundary summary missing capability %q: %+v", capability, summary.Capabilities)
	return BoundaryCapabilitySummary{}
}
