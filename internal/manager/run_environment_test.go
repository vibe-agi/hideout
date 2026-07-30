package manager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
)

func TestRuntimeConfigurationSeparatesMachineBootServiceAndSessionInputs(t *testing.T) {
	p := runtimeConfigurationTestProfile("compatibility")
	base, err := RuntimeConfigurationForProfile(p, "lima", environment.ModeShared)
	if err != nil {
		t.Fatal(err)
	}
	assertImpact := func(name string, mutate func(*profile.Profile), want environment.ChangeImpact, wantLayers ...string) {
		t.Helper()
		encoded, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("%s clone: %v", name, err)
		}
		var changedProfile profile.Profile
		if err := json.Unmarshal(encoded, &changedProfile); err != nil {
			t.Fatalf("%s clone: %v", name, err)
		}
		mutate(&changedProfile)
		changed, err := RuntimeConfigurationForProfile(changedProfile, "lima", environment.ModeShared)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		changes := environment.CompareConfigurations(base, changed)
		if got := environment.RequiredImpact(changes); got != want {
			t.Fatalf("%s impact=%q want=%q base=%+v changed=%+v", name, got, want, base.Layers, changed.Layers)
		}
		if len(wantLayers) > 0 {
			gotLayers := make(map[string]bool, len(changes))
			for _, change := range changes {
				gotLayers[change.Layer] = true
			}
			if len(gotLayers) != len(wantLayers) {
				t.Fatalf("%s changed layers=%v want=%v", name, gotLayers, wantLayers)
			}
			for _, layer := range wantLayers {
				if !gotLayers[layer] {
					t.Fatalf("%s changed layers=%v missing=%q", name, gotLayers, layer)
				}
			}
		}
	}
	assertImpact("image", func(value *profile.Profile) { value.Environment.BaseImage = "template:_images/debian-12" }, environment.ImpactRecreate, "machine")
	assertImpact("profile-reference", func(value *profile.Profile) { value.Metadata["profileId"] = "profile-other" }, environment.ImpactNewSession, "session")
	assertImpact("identity-reference", func(value *profile.Profile) { value.Metadata["identityId"] = "identity-other" }, environment.ImpactNewSession, "session")
	assertImpact("unrelated-metadata", func(value *profile.Profile) { value.Metadata["note"] = "operator-only" }, environment.ImpactNone)
	assertImpact("guest-machine-id", func(value *profile.Profile) { value.Metadata["machineId"] = strings.Repeat("b", 32) }, environment.ImpactRecreate, "machine")
	assertImpact("user", func(value *profile.Profile) { value.Identity.User = "operator2" }, environment.ImpactRecreate, "machine", "session")
	assertImpact("hostname", func(value *profile.Profile) { value.Identity.Hostname = "hideout2" }, environment.ImpactReconfigure, "boot", "session")
	assertImpact("timezone", func(value *profile.Profile) { value.Identity.Timezone = "Asia/Singapore" }, environment.ImpactNewSession, "session")
	assertImpact("locale", func(value *profile.Profile) { value.Identity.Locale = "en_US.UTF-8" }, environment.ImpactNewSession, "session")
	assertImpact("workspace-path-mode", func(value *profile.Profile) { value.Workspace.PathMode = "preserve" }, environment.ImpactNewSession, "session")
	assertImpact("public-env", func(value *profile.Profile) { value.Env.Public["FEATURE"] = "on" }, environment.ImpactNewSession, "session")
	assertImpact("deny-env", func(value *profile.Profile) { value.Env.Deny = []string{"SECRET_*"} }, environment.ImpactNewSession, "session")
	assertImpact("inherit-env", func(value *profile.Profile) { value.Env.Inherit = append(value.Env.Inherit, "EDITOR") }, environment.ImpactNewSession, "session")
	assertImpact("git", func(value *profile.Profile) { value.Git.UserEmail = "other@example.com" }, environment.ImpactNewSession, "session")
	assertImpact("network", func(value *profile.Profile) {
		value.Network.Mode = profile.NetworkModeTun2Socks
		value.Network.ProxySecretRef = "default-proxy"
		value.Network.MediatedResolver = "1.1.1.1"
	}, environment.ImpactLive, "environment-services")
	assertImpact("proxy-env-presentation", func(value *profile.Profile) { value.Network.ProxyEnvVisible = true }, environment.ImpactNewSession, "session")
	assertImpact("tools", func(value *profile.Profile) { value.Tools.ExpectedCommands = []string{"git", "node"} }, environment.ImpactNewSession, "session")
	assertImpact("host-open", func(value *profile.Profile) { value.HostCapabilities.Open.AllowURLs = false }, environment.ImpactNewSession, "session")
	assertImpact("endpoint-exposure", func(value *profile.Profile) {
		value.EndpointExposure.HostToGuest = []profile.EndpointCandidate{{ID: "dev", Owner: "target", TargetAddress: "127.0.0.1:3000"}}
	}, environment.ImpactNewSession, "session")
	assertImpact("hostfs", func(value *profile.Profile) {
		value.HostFS.Grants = []hostfs.Rule{{HostPath: "/tmp/input", Ops: []hostfs.Op{hostfs.OpRead}, Scope: hostfs.ScopeExactFile, Reason: "fixture"}}
	}, environment.ImpactNewSession, "session")
	assertImpact("command-proxy", func(value *profile.Profile) {
		value.CommandProxy.Commands["browse"] = profile.CommandProxyCommand{Route: "host-broker", Action: "host.open", ArgvSchema: "open-target-v1"}
	}, environment.ImpactNewSession, "session")
	assertImpact("command-adapter", func(value *profile.Profile) {
		value.CommandAdapters.Adapters = map[string]profile.CommandAdapter{"fixture": {Enabled: true, Builtin: "fixture"}}
	}, environment.ImpactNewSession, "session")
	assertImpact("activity-retention", func(value *profile.Profile) {
		value.Activity = &profile.ActivityConfig{
			Retention: profile.ActivityRetention{
				MaxBytes:      64 << 20,
				MaxAgeSeconds: 7 * 24 * 60 * 60,
			},
		}
	}, environment.ImpactNone)
	assertImpact("policy-capability", func(value *profile.Profile) {
		value.Policy.MaxCapabilities = append(value.Policy.MaxCapabilities, "host.fs.write.plan")
	}, environment.ImpactNewSession, "session")
	assertImpact("policy-source", func(value *profile.Profile) {
		value.Policy.ScriptRefs = []profile.ScriptRef{{ID: "fixture", Path: "policy.js", Entrypoints: []string{"decideCommand"}}}
	}, environment.ImpactNewSession, "session")
	assertImpact("audit", func(value *profile.Profile) { value.Audit.Enabled = false }, environment.ImpactNewSession, "session")

	first := RunEnvironmentSpec(p, "lima", "/Users/alice/project-a", "/workspace")
	second := RunEnvironmentSpec(p, "lima", "/Users/bob/project-b", "/different-session-root")
	if first.MachineIdentityID != second.MachineIdentityID || first.BootConfigurationID != second.BootConfigurationID {
		t.Fatalf("workspace/session facts changed machine identity: %q != %q", first.MachineIdentityID, second.MachineIdentityID)
	}
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"/Users/alice/project-a", "ses_secret", "git status", "xterm-256color", "cap_secret", "env://HIDEOUT_SECRET_PROXY"} {
		if strings.Contains(string(encoded), excluded) {
			t.Fatalf("machine compatibility leaked session-only fact %q: %s", excluded, encoded)
		}
	}
}

func TestRuntimeConfigurationCoversEveryTopLevelProfileDomain(t *testing.T) {
	// This is a drift guard, not the lifecycle algorithm. A new profile domain
	// must be deliberately added here and to RuntimeConfigurationForProfile
	// instead of silently inheriting machine/recreate semantics or no semantics.
	want := map[string]string{
		"schemaVersion":    "validation-only",
		"name":             "stable-slot-selection",
		"identity":         "machine+boot+session",
		"workspace":        "mode-dependent-machine+session",
		"env":              "session",
		"git":              "session",
		"network":          "environment-service+session-presentation",
		"tools":            "session",
		"environment":      "image-machine+runtime-contract-session",
		"hostCapabilities": "session",
		"endpointExposure": "session",
		"hostfs":           "session",
		"commandProxy":     "session",
		"commandAdapters":  "session",
		"activity":         "manager-control-only",
		"policy":           "session",
		"audit":            "session",
		"metadata":         "selected-machine+session+control-only",
	}
	typeOfProfile := reflect.TypeOf(profile.Profile{})
	got := make(map[string]bool, typeOfProfile.NumField())
	for index := 0; index < typeOfProfile.NumField(); index++ {
		field := typeOfProfile.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("profile field %s has no lifecycle-addressable JSON name", field.Name)
		}
		if _, ok := want[name]; !ok {
			t.Fatalf("profile domain %q (%s) has no explicit lifecycle classification", name, field.Name)
		}
		got[name] = true
	}
	if len(got) != len(want) {
		for name, classification := range want {
			if !got[name] {
				t.Fatalf("stale lifecycle classification %q=%q has no profile field", name, classification)
			}
		}
	}
}

func TestSharedSlotIsStableAcrossProjectsAndPostureDrift(t *testing.T) {
	first := environment.SharedSlotID(" Default ")
	if first != environment.SharedSlotID("default") || first != environment.SharedSlotID("DEFAULT") {
		t.Fatalf("shared slot is not canonical: %q", first)
	}
	if first == environment.SharedSlotID("other") {
		t.Fatal("distinct profiles share an automatic slot")
	}
	p := runtimeConfigurationTestProfile("default")
	before, err := RuntimeConfigurationForProfile(p, "lima", environment.ModeShared)
	if err != nil {
		t.Fatal(err)
	}
	p.Identity.Hostname = "changed-host"
	after, err := RuntimeConfigurationForProfile(p, "lima", environment.ModeShared)
	if err != nil {
		t.Fatal(err)
	}
	if before.Layers.MachineID != after.Layers.MachineID || before.Layers.BootID == after.Layers.BootID || first != environment.SharedSlotID(p.Name) {
		t.Fatalf("hostname must change boot configuration but not machine or slot: before=%+v after=%+v slot=%q", before.Layers, after.Layers, first)
	}
}

func TestRuntimeCatalogMetadataDoesNotBecomeMachineIdentity(t *testing.T) {
	p := runtimeConfigurationTestProfile("runtime-layers")
	_, provenance := runtimeRunCatalogFixture()
	p.Environment.Runtime = &provenance
	base, err := RuntimeConfigurationForProfile(p, "lima", environment.ModeShared)
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimeImpact := func(name string, mutate func(*environment.RuntimeProvenance), want environment.ChangeImpact, wantLayers ...string) {
		t.Helper()
		changedProfile := p
		changedProvenance := provenance
		mutate(&changedProvenance)
		changedProfile.Environment.Runtime = &changedProvenance
		changed, err := RuntimeConfigurationForProfile(changedProfile, "lima", environment.ModeShared)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		changes := environment.CompareConfigurations(base, changed)
		if got := environment.RequiredImpact(changes); got != want {
			t.Fatalf("%s impact=%q want=%q changes=%+v", name, got, want, changes)
		}
		gotLayers := make(map[string]bool, len(changes))
		for _, change := range changes {
			gotLayers[change.Layer] = true
		}
		if len(gotLayers) != len(wantLayers) {
			t.Fatalf("%s changed layers=%v want=%v", name, gotLayers, wantLayers)
		}
		for _, layer := range wantLayers {
			if !gotLayers[layer] {
				t.Fatalf("%s changed layers=%v missing=%q", name, gotLayers, layer)
			}
		}
	}

	assertRuntimeImpact("catalog-release", func(value *environment.RuntimeProvenance) {
		value.CatalogRelease = "2026.08.0"
	}, environment.ImpactNone)
	assertRuntimeImpact("artifact-location", func(value *environment.RuntimeProvenance) {
		value.ArtifactLocation = "https://downloads.example.test/runtime-mirror.qcow2"
	}, environment.ImpactNone)
	assertRuntimeImpact("contract", func(value *environment.RuntimeProvenance) {
		value.ContractDigest = "sha256:" + strings.Repeat("d", 64)
	}, environment.ImpactNewSession, "session")
	assertRuntimeImpact("package-inventory", func(value *environment.RuntimeProvenance) {
		value.PackageInventoryDigest = "sha256:" + strings.Repeat("e", 64)
	}, environment.ImpactNewSession, "session")
	assertRuntimeImpact("image-content", func(value *environment.RuntimeProvenance) {
		value.ArtifactSHA256 = strings.Repeat("f", 64)
	}, environment.ImpactRecreate, "machine")
}

func TestBackendAndIsolationStructureRemainMachineIdentity(t *testing.T) {
	p := runtimeConfigurationTestProfile("machine-structure")
	shared, err := RuntimeConfigurationForProfile(p, "lima", environment.ModeShared)
	if err != nil {
		t.Fatal(err)
	}
	dedicated, err := RuntimeConfigurationForProfile(p, "lima", environment.ModeDedicated)
	if err != nil {
		t.Fatal(err)
	}
	if changes := environment.CompareConfigurations(shared, dedicated); environment.RequiredImpact(changes) != environment.ImpactRecreate || len(changes) != 1 || changes[0].Layer != "machine" {
		t.Fatalf("workspace isolation changes=%+v", changes)
	}
	staticWorkspaceChanged := p
	staticWorkspaceChanged.Workspace.PathMode = profile.WorkspacePathModePreserve
	changedDedicated, err := RuntimeConfigurationForProfile(staticWorkspaceChanged, "lima", environment.ModeDedicated)
	if err != nil {
		t.Fatal(err)
	}
	staticChanges := environment.CompareConfigurations(dedicated, changedDedicated)
	if environment.RequiredImpact(staticChanges) != environment.ImpactRecreate {
		t.Fatalf("static workspace impact=%q changes=%+v", environment.RequiredImpact(staticChanges), staticChanges)
	}
	staticLayers := map[string]bool{}
	for _, change := range staticChanges {
		staticLayers[change.Layer] = true
	}
	if !staticLayers["machine"] || !staticLayers["session"] {
		t.Fatalf("static workspace changed layers=%v", staticLayers)
	}
	native, err := RuntimeConfigurationForProfile(p, "native", environment.ModeShared)
	if err != nil {
		t.Fatal(err)
	}
	changes := environment.CompareConfigurations(shared, native)
	if environment.RequiredImpact(changes) != environment.ImpactRecreate {
		t.Fatalf("backend impact=%q changes=%+v", environment.RequiredImpact(changes), changes)
	}
	gotLayers := map[string]bool{}
	for _, change := range changes {
		gotLayers[change.Layer] = true
	}
	if !gotLayers["machine"] || !gotLayers["boot"] {
		t.Fatalf("backend changed layers=%v", gotLayers)
	}
	changedNative, err := RuntimeConfigurationForProfile(staticWorkspaceChanged, "native", environment.ModeShared)
	if err != nil {
		t.Fatal(err)
	}
	if nativeChanges := environment.CompareConfigurations(native, changedNative); environment.RequiredImpact(nativeChanges) != environment.ImpactNewSession || len(nativeChanges) != 1 || nativeChanges[0].Layer != "session" {
		t.Fatalf("native workspace changes=%+v", nativeChanges)
	}
}

func runtimeConfigurationTestProfile(name string) profile.Profile {
	p := profile.Default(name)
	p.Metadata = map[string]string{
		"profileId":  "profile-fixture-" + name,
		"identityId": "identity-fixture-" + name,
		"machineId":  strings.Repeat("a", 32),
	}
	return p
}

func TestRuntimeConfigurationAcceptsMaterializedNativeProfile(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p, err := store.LoadOrInit("default")
	if err != nil {
		t.Fatal(err)
	}
	p.Network.Mode = profile.NetworkModeTun2Socks
	p.Network.ProxySecretRef = "missing-proxy"
	p.Network.MediatedResolver = "1.1.1.1"

	configuration, err := RuntimeConfigurationForProfile(p, "native", environment.ModeWorkspaceBound)
	if err != nil {
		t.Fatalf("runtime configuration: %v", err)
	}
	if configuration.Layers.MachineID == "" || configuration.Layers.BootID == "" || configuration.Layers.ServicesID == "" || configuration.Layers.SessionID == "" {
		t.Fatalf("runtime configuration has an empty layer: %+v", configuration.Layers)
	}
}

func TestSelectRunEnvironmentByNameBindsRecord(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	core := New(store)
	p, err := store.LoadOrInit("default")
	if err != nil {
		t.Fatal(err)
	}
	created, err := core.CreateEnvironment(EnvironmentCreateOptions{Name: "work", Profile: "default", Backend: "lima", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}

	selected, err := core.SelectRunEnvironment(RunPlan{
		Backend:        "lima",
		Workspace:      workspace,
		GuestWorkspace: created.GuestWorkspaceRoot(),
		RuntimeProfile: p,
	}, RunEnvironmentOptions{EnvName: "work", Create: true})
	if err != nil {
		t.Fatalf("select by name: %v", err)
	}
	if selected.Record.ID != created.ID || selected.Record.Name != "work" {
		t.Fatalf("wrong record selected: %+v", selected.Record)
	}

	bootChanged := p
	bootChanged.Identity.Hostname = "hideout-work"
	reconfigured, err := core.SelectRunEnvironment(RunPlan{
		Backend:        "lima",
		Workspace:      workspace,
		GuestWorkspace: created.GuestWorkspaceRoot(),
		RuntimeProfile: bootChanged,
	}, RunEnvironmentOptions{EnvName: "work", Create: true})
	if err != nil {
		t.Fatalf("hostname-only change must not require recreate: %v", err)
	}
	if !reconfigured.BootReconfigure || reconfigured.Configuration.Boot.Hostname != bootChanged.Identity.Hostname {
		t.Fatalf("hostname change was not classified as boot reconfigure: %+v", reconfigured)
	}

	// unknown name points at env list
	_, err = core.SelectRunEnvironment(RunPlan{
		Backend: "lima", Workspace: workspace, GuestWorkspace: created.GuestWorkspaceRoot(), RuntimeProfile: p,
	}, RunEnvironmentOptions{EnvName: "ghost", Create: true})
	if err == nil || !strings.Contains(err.Error(), "env list") {
		t.Fatalf("unknown name should mention env list, got %v", err)
	}

	// record ids are not accepted as names
	_, err = core.SelectRunEnvironment(RunPlan{
		Backend: "lima", Workspace: workspace, GuestWorkspace: created.GuestWorkspaceRoot(), RuntimeProfile: p,
	}, RunEnvironmentOptions{EnvName: created.ID, Create: true})
	if err == nil {
		t.Fatal("record id must not resolve as a name")
	}

	// conflicting profile fails closed
	other := profile.Default("other")
	_, err = core.SelectRunEnvironment(RunPlan{
		Backend: "lima", Workspace: workspace, GuestWorkspace: created.GuestWorkspaceRoot(), RuntimeProfile: other,
	}, RunEnvironmentOptions{EnvName: "work", Create: true})
	if err == nil || !strings.Contains(err.Error(), "profile") {
		t.Fatalf("conflicting profile must fail closed, got %v", err)
	}

	// conflicting backend fails closed
	_, err = core.SelectRunEnvironment(RunPlan{
		Backend: "native", Workspace: workspace, GuestWorkspace: created.GuestWorkspaceRoot(), RuntimeProfile: p,
	}, RunEnvironmentOptions{EnvName: "work", Create: true})
	if err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("conflicting backend must fail closed, got %v", err)
	}

	// conflicting workspace fails closed
	_, err = core.SelectRunEnvironment(RunPlan{
		Backend: "lima", Workspace: t.TempDir(), GuestWorkspace: created.GuestWorkspaceRoot(), RuntimeProfile: p,
	}, RunEnvironmentOptions{EnvName: "work", Create: true})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("conflicting workspace must fail closed, got %v", err)
	}
}

func TestSelectRunEnvironmentAutoNamedResolution(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	workspace := t.TempDir()
	core := New(store)
	p, err := store.LoadOrInit("default")
	if err != nil {
		t.Fatal(err)
	}
	plan := RunPlan{Backend: "lima", Workspace: workspace, GuestWorkspace: workspace, RuntimeProfile: p}

	first, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	wantName := environment.SharedDisplayName(p.Name)
	if !first.Created || first.Record.Name != wantName || !first.Record.AutoNamed {
		t.Fatalf("first use should create the stable shared environment %q: %+v", wantName, first.Record)
	}
	if first.Record.Mode != environment.ModeShared || first.Record.SharedSlot != environment.SharedSlotID(p.Name) {
		t.Fatalf("first use did not select the shared slot: %+v", first.Record)
	}
	if first.Record.ImageRef != p.BaseImageOrBuiltin() {
		t.Fatalf("auto-named environment should pin the profile image default: %+v", first.Record)
	}
	if data, err := os.ReadFile(filepath.Join(store.Root, "logs", "environment-audit.jsonl")); err != nil || !strings.Contains(string(data), `"env.create"`) {
		t.Fatalf("auto-named first-use create must audit env.create: err=%v data=%s", err, data)
	}

	second, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.Record.ID != first.Record.ID {
		t.Fatalf("rerun should reuse the auto-named environment: %+v", second.Record)
	}

	// --rm creates its own dedicated disposable environment instead of touching
	// the auto-named reusable record.
	rm, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{RemoveAfterRun: true, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rm.Active || !rm.Record.Disposable || rm.Record.ID == first.Record.ID {
		t.Fatalf("--rm should own a distinct disposable environment: %+v", rm)
	}

	// --ephemeral resolves the SAME shared environment as a normal run; only
	// identity is session-local (see RunIdentityDir/IdentityMode). It must not
	// stay record-less, or the lima daemon's isolated ready proof would lack the
	// EnvironmentID/InstanceName it requires.
	ephPlan := plan
	ephPlan.Ephemeral = true
	eph, err := core.SelectRunEnvironment(ephPlan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if !eph.Active {
		t.Fatalf("--ephemeral must resolve the active shared environment: %+v", eph)
	}
	if eph.Created || eph.Record.ID != first.Record.ID {
		t.Fatalf("--ephemeral must reuse the shared environment, not create a new one: %+v", eph.Record)
	}

	// MRU-style flags are gone from the options surface: resuming by id and
	// --new no longer exist. (Compile-time: RunEnvironmentOptions has no such
	// fields; runtime assertion below guards the listing count.) The --rm
	// selection above owns the only extra record, and it is explicitly marked
	// disposable rather than a silent reusable environment.
	envStore := environment.Store{Root: store.Root}
	records, err := envStore.List()
	if err != nil {
		t.Fatal(err)
	}
	reusable := 0
	disposable := 0
	for _, rec := range records {
		if rec.Disposable {
			disposable++
			continue
		}
		reusable++
	}
	if reusable != 1 || disposable != 1 {
		t.Fatalf("no silent extra environments may exist (want 1 reusable + 1 disposable): %+v", records)
	}
}

func TestSelectionAndLifecycleRejectForeignVersionRecords(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	p, err := store.LoadOrInit("default")
	if err != nil {
		t.Fatal(err)
	}
	// plant a foreign-version record whose name would collide with auto-name
	envStore := environment.Store{Root: store.Root}
	id := "env_20260701t000000zaabbccddee0000000001"
	dir := filepath.Join(store.Root, "environments", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"version":"hideout.environment/v1","id":"` + id + `","profile":"default","backend":"lima","workspace":"` + workspace + `","guestWorkspace":"` + workspace + `","status":"ready","createdAt":"2026-07-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "environment.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	// selection ignores foreign records and creates fresh
	selected, err := core.SelectRunEnvironment(RunPlan{Backend: "lima", Workspace: workspace, GuestWorkspace: workspace, RuntimeProfile: p}, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatalf("selection must not read through foreign records: %v", err)
	}
	if selected.Record.ID == id {
		t.Fatal("selection must never reuse a foreign-version record")
	}

	// lifecycle ops on the foreign id stop with guidance
	_, err = core.PlanEnvironmentStop(EnvironmentActionOptions{IDs: []string{id}})
	if err == nil || !errors.Is(err, environment.ErrUnsupportedVersion) {
		t.Fatalf("stop plan should reject foreign record with guidance, got %v", err)
	}
	if _, err := envStore.Load(id); !errors.Is(err, environment.ErrUnsupportedVersion) {
		t.Fatalf("load should reject foreign record, got %v", err)
	}
	// listing shows it as unsupported
	records, err := envStore.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rec := range records {
		if rec.ID == id && rec.Status == environment.StatusUnsupportedVersion {
			found = true
		}
	}
	if !found {
		t.Fatalf("foreign record should list as unsupported-version: %+v", records)
	}
}

func TestDriftReportAxesAndSameFileWorkspace(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	p, err := store.LoadOrInit("default")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := core.CreateEnvironment(EnvironmentCreateOptions{Name: "drifty", Profile: "default", Backend: "lima", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}

	// Machine identity drift names the exact lifecycle layer and recreate action.
	envStore := environment.Store{Root: store.Root}
	currentMachineID := rec.MachineIdentityID
	rec.MachineIdentityID = "sha256:" + strings.Repeat("0", 64)
	if err := envStore.Save(rec); err != nil {
		t.Fatal(err)
	}
	_, err = core.SelectRunEnvironment(RunPlan{Backend: "lima", Workspace: workspace, GuestWorkspace: rec.GuestWorkspaceRoot(), RuntimeProfile: p}, RunEnvironmentOptions{EnvName: "drifty", Create: true})
	if err == nil {
		t.Fatal("machine identity drift must fail closed")
	}
	msg := err.Error()
	for _, want := range []string{"machine", rec.MachineIdentityID, "hideout env recreate drifty"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("drift report missing %q: %s", want, msg)
		}
	}
	// audit env.drift.denied
	data, err := os.ReadFile(filepath.Join(store.Root, "logs", "environment-audit.jsonl"))
	if err != nil || !strings.Contains(string(data), `"env.drift.denied"`) {
		t.Fatalf("env.drift.denied audit missing: err=%v data=%s", err, data)
	}

	// workspace drift via a genuinely different directory
	rec.MachineIdentityID = currentMachineID
	if err := envStore.Save(rec); err != nil {
		t.Fatal(err)
	}
	otherWS := t.TempDir()
	_, err = core.SelectRunEnvironment(RunPlan{Backend: "lima", Workspace: otherWS, GuestWorkspace: otherWS, RuntimeProfile: p}, RunEnvironmentOptions{EnvName: "drifty", Create: true})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("workspace drift must fail closed naming the axis, got %v", err)
	}

	// a symlink to the pinned workspace is the same file identity: no drift
	linked := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(workspace, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := core.SelectRunEnvironment(RunPlan{Backend: "lima", Workspace: linked, GuestWorkspace: rec.GuestWorkspaceRoot(), RuntimeProfile: p}, RunEnvironmentOptions{EnvName: "drifty", Create: true}); err != nil {
		t.Fatalf("same file identity must not drift: %v", err)
	}
}

func TestRecreateEnvironmentGuardForceAndRefresh(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	workspace := t.TempDir()
	rec, err := core.CreateEnvironment(EnvironmentCreateOptions{Name: "rebuild", Profile: "default", Backend: "lima", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	envStore := environment.Store{Root: store.Root}
	rec.MachineIdentityID = "sha256:" + strings.Repeat("0", 64)
	rec.Status = "running"
	if err := envStore.Save(rec); err != nil {
		t.Fatal(err)
	}

	// running without force: refuse with stop hint
	_, err = core.RecreateEnvironment(context.Background(), "rebuild", false, EnvironmentApplyOptions{Operator: fakeEnvOperator{}})
	if err == nil || !strings.Contains(err.Error(), "hideout stop rebuild") {
		t.Fatalf("running guest must refuse recreate without force, got %v", err)
	}

	// force: stop then rebuild under the same name and id, refreshed config
	rebuilt, err := core.RecreateEnvironment(context.Background(), "rebuild", true, EnvironmentApplyOptions{Operator: fakeEnvOperator{}})
	if err != nil {
		t.Fatalf("forced recreate: %v", err)
	}
	if rebuilt.Name != "rebuild" || rebuilt.ID != rec.ID {
		t.Fatalf("recreate must keep name and record id: %+v", rebuilt)
	}
	if rebuilt.MachineIdentityID == rec.MachineIdentityID || rebuilt.BootConfigurationID == "" {
		t.Fatalf("recreate must refresh machine and boot identities: %+v", rebuilt)
	}
	if rebuilt.ImageRef != rec.ImageRef || rebuilt.HostWorkspace() != rec.HostWorkspace() {
		t.Fatalf("recreate must keep pinned declaration and workspace: %+v", rebuilt)
	}
	if rebuilt.Status != "ready" {
		t.Fatalf("recreate should leave a ready record: %+v", rebuilt)
	}
	data, err := os.ReadFile(filepath.Join(store.Root, "logs", "environment-audit.jsonl"))
	if err != nil || !strings.Contains(string(data), `"env.recreate"`) || !strings.Contains(string(data), `"force":true`) {
		t.Fatalf("env.recreate audit with force missing: %s", data)
	}

	// remove with force on a running guest stops then removes
	rec2, err := core.CreateEnvironment(EnvironmentCreateOptions{Name: "removable", Profile: "default", Backend: "lima", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	rec2.Status = "running"
	if err := envStore.Save(rec2); err != nil {
		t.Fatal(err)
	}
	if _, err := core.RemoveEnvironment(context.Background(), "removable", false, EnvironmentApplyOptions{Operator: fakeEnvOperator{}}); err == nil {
		t.Fatal("remove without force must refuse a running guest")
	}
	if _, err := core.RemoveEnvironment(context.Background(), "removable", true, EnvironmentApplyOptions{Operator: fakeEnvOperator{}}); err != nil {
		t.Fatalf("forced remove: %v", err)
	}
}

func TestSelectRunEnvironmentRechecksPinnedRuntimeDiskBeforeCreate(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p, err := store.LoadOrInit("runtime")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := testManagerRuntimeResolver(runtimecatalog.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	p.Environment.BaseImage = ""
	p.Environment.Runtime = &resolved.Provenance
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	plan := RunPlan{Backend: "lima", Workspace: workspace, GuestWorkspace: workspace, RuntimeProfile: p}
	core := New(store)
	core.RuntimeDiskCheck = func(string, int64) error { return errors.New("available 1 byte") }
	if _, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true}); err == nil || !strings.Contains(err.Error(), "runtime.disk.insufficient") {
		t.Fatalf("run should fail before environment creation on low disk, got %v", err)
	}
	records, err := (environment.Store{Root: store.Root}).List()
	if err != nil || len(records) != 0 {
		t.Fatalf("low-disk run wrote environment state: records=%+v err=%v", records, err)
	}
	core.RuntimeDiskCheck = func(string, int64) error { return nil }
	selected, err := core.SelectRunEnvironment(plan, RunEnvironmentOptions{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Record.Runtime == nil || *selected.Record.Runtime != resolved.Provenance {
		t.Fatalf("auto environment lost pinned runtime provenance: %+v", selected.Record)
	}

	// Ephemeral resolves the same shared environment, so the runtime-disk
	// precheck must gate it too — it no longer gets skipped as it did while
	// ephemeral was record-less.
	ephPlan := plan
	ephPlan.Ephemeral = true
	core.RuntimeDiskCheck = func(string, int64) error { return errors.New("available 1 byte") }
	if _, err := core.SelectRunEnvironment(ephPlan, RunEnvironmentOptions{Create: true}); err == nil || !strings.Contains(err.Error(), "runtime.disk.insufficient") {
		t.Fatalf("ephemeral run should also fail before environment creation on low disk, got %v", err)
	}
}
