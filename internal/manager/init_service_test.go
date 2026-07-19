package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/inittask"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/profiletemplate"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
)

func TestInitServiceSetupPrepareUsesFixedProjection(t *testing.T) {
	service := InitService{Core: New(profile.Store{Root: t.TempDir()})}
	prepared, err := service.Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Review.State != InitStateFresh || !prepared.Review.RequiresConfirmation {
		t.Fatalf("review=%+v", prepared.Review)
	}
	if prepared.Plan.Profile != "default" || prepared.Plan.TemplateID != "dev" || prepared.Plan.Backend != "lima" || prepared.Plan.Network != "direct" {
		t.Fatalf("plan=%+v", prepared.Plan)
	}
	if prepared.Plan.RuntimeSelection == nil || prepared.Plan.RuntimeSelection.Family != "developer-standard" {
		t.Fatalf("runtime=%+v", prepared.Plan.RuntimeSelection)
	}
	if prepared.Review.Workspace.GuestPath != "/workspace" || prepared.Review.Workspace.Mode != "read-write" {
		t.Fatalf("workspace=%+v", prepared.Review.Workspace)
	}
	if len(prepared.Review.PlanDigest) != 64 {
		t.Fatalf("digest=%q", prepared.Review.PlanDigest)
	}
}

func TestInitServiceRejectsSetupOverrides(t *testing.T) {
	req := SetupInitServiceRequest()
	req.Network = "tun2socks"
	_, err := (InitService{Core: New(profile.Store{Root: t.TempDir()})}).Prepare(req)
	if err == nil || !strings.Contains(err.Error(), "does not accept") {
		t.Fatalf("err=%v", err)
	}
}

func TestInitServiceApplyRejectsMutatedPreparedPlan(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	service := InitService{Core: New(store)}
	prepared, err := service.Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	prepared.Plan.Network = "tun2socks"
	_, err = service.Apply(prepared, &InitConfirmation{
		ReviewVersion: prepared.Review.Version,
		PlanDigest:    prepared.Review.PlanDigest,
		Confirmed:     true,
	})
	if !errors.Is(err, ErrInitPlanStale) {
		t.Fatalf("err=%v", err)
	}
	if _, loadErr := store.Load("default"); !errors.Is(loadErr, os.ErrNotExist) {
		t.Fatalf("profile changed: %v", loadErr)
	}
}

func TestInitServiceSemanticDigestIsStableAndEffectSensitive(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	service := InitService{Core: New(store)}
	first, err := service.Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	if first.Review.PlanDigest != second.Review.PlanDigest {
		t.Fatalf("equivalent plans are unstable: %s != %s", first.Review.PlanDigest, second.Review.PlanDigest)
	}

	presentation := clonePreparedInit(t, first)
	presentation.Review.Notices = []InitNotice{{Code: "presentation", Summary: "changed"}}
	presentation.Plan.ReviewLines = []string{"changed presentation"}
	presentation.Plan.Warnings = []string{"changed warning"}
	presentation.Plan.NonClaims = []string{"changed non-claim"}
	presentation.Plan.NextSteps = []inittask.NextStep{{ID: "changed", Label: "changed", Command: "changed", Message: "changed"}}
	if len(presentation.Plan.Tasks) != 0 {
		presentation.Plan.Tasks[0].Message = "changed presentation"
		presentation.Plan.Tasks[0].Outputs = []string{"/generated/value"}
	}
	digest, err := digestPreparedInit(presentation)
	if err != nil {
		t.Fatal(err)
	}
	if digest != first.Review.PlanDigest {
		t.Fatalf("presentation-only change altered semantic digest: %s != %s", digest, first.Review.PlanDigest)
	}

	mutations := map[string]func(*PreparedInit){
		"network": func(value *PreparedInit) { value.Plan.Network = "tun2socks" },
		"profile": func(value *PreparedInit) { value.Plan.Profile = "other" },
		"runtime": func(value *PreparedInit) {
			value.Plan.RuntimeSelection.ArtifactSHA256 = strings.Repeat("f", 64)
		},
	}
	if len(first.Plan.Tasks) != 0 {
		mutations["task-input"] = func(value *PreparedInit) {
			value.Plan.Tasks[0].Inputs = append(value.Plan.Tasks[0].Inputs, "effect-change")
		}
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := clonePreparedInit(t, first)
			mutate(&changed)
			changedDigest, err := digestPreparedInit(changed)
			if err != nil {
				t.Fatal(err)
			}
			if changedDigest == first.Review.PlanDigest {
				t.Fatalf("effect mutation %s did not alter digest", name)
			}
		})
	}
}

func TestInitServiceApplyRejectsRuntimeCatalogDrift(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	drift := false
	resolver := func(selection runtimecatalog.Selection) (runtimecatalog.Resolution, error) {
		resolved, err := runtimecatalog.ResolveEmbedded(selection)
		if err == nil && drift {
			resolved.Provenance.ArtifactSHA256 = strings.Repeat("f", 64)
			resolved.Artifact.SHA256 = resolved.Provenance.ArtifactSHA256
		}
		return resolved, err
	}
	service := InitService{Core: New(store), ResolveRuntime: resolver}
	prepared, err := service.Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	drift = true
	_, err = service.Apply(prepared, &InitConfirmation{
		ReviewVersion: prepared.Review.Version,
		PlanDigest:    prepared.Review.PlanDigest,
		Confirmed:     true,
	})
	if !errors.Is(err, ErrInitPlanStale) {
		t.Fatalf("runtime drift error = %v", err)
	}
	if _, loadErr := store.Load("default"); !errors.Is(loadErr, os.ErrNotExist) {
		t.Fatalf("runtime drift changed profile: %v", loadErr)
	}
}

func TestInitServiceApplyRejectsProfileCreatedAfterReview(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	service := InitService{Core: New(store)}
	prepared, err := service.Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.ProfilePath("default"))
	if err != nil {
		t.Fatal(err)
	}
	beforeTree, err := snapshotInitAuthorityTree(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Apply(prepared, &InitConfirmation{
		ReviewVersion: prepared.Review.Version,
		PlanDigest:    prepared.Review.PlanDigest,
		Confirmed:     true,
	})
	if !errors.Is(err, ErrInitPlanStale) {
		t.Fatalf("err=%v", err)
	}
	after, err := os.ReadFile(store.ProfilePath("default"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("stale apply changed the winning profile")
	}
	afterTree, err := snapshotInitAuthorityTree(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterTree, beforeTree) {
		t.Fatalf("stale apply changed durable authority state:\nbefore=%+v\nafter=%+v", beforeTree, afterTree)
	}
}

func TestInitServiceReadyIsPureReadAndNotApplicable(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p := profile.Default("default")
	// Non-default customization: the ready review must echo these observed
	// values, so a regression that renders the normalized setup request
	// ("direct", empty template) instead of the stored profile fails here.
	p.Network.Mode = "tun2socks"
	p.Network.ProxySecretRef = "ready-proxy"
	p.Network.MediatedResolver = "1.1.1.1"
	p.Git.UserName = "Custom Operator"
	p.Env.Public["PROJECT_MODE"] = "custom"
	if p.Metadata == nil {
		p.Metadata = map[string]string{}
	}
	p.Metadata["templateId"] = "custom-posture-template"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	before, err := snapshotInitTree(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	service := InitService{Core: New(store)}
	prepared, err := service.Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Review.State != InitStateReady || prepared.Review.RequiresConfirmation {
		t.Fatalf("review=%+v observation=%+v", prepared.Review, prepared.Observation)
	}
	if prepared.Review.Backend != "" || prepared.Review.Network != "tun2socks" {
		t.Fatalf("ready review invented non-profile state: %+v", prepared.Review)
	}
	if prepared.Review.Template != "custom-posture-template" {
		t.Fatalf("ready review did not echo the stored template: %+v", prepared.Review)
	}
	if _, err := service.Apply(prepared, nil); !errors.Is(err, ErrInitNotApplicable) {
		t.Fatalf("apply err=%v", err)
	}
	after, err := snapshotInitTree(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("ready setup changed store tree:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestInitServiceBlocksMalformedAndUnsafeProfilesWithoutLeakingDetails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, store profile.Store)
	}{
		{name: "malformed", mutate: func(t *testing.T, store profile.Store) {
			writeInitProfileRaw(t, store, []byte("{secret-host-path"), 0o600)
		}},
		{name: "unknown-field", mutate: func(t *testing.T, store profile.Store) {
			data, err := os.ReadFile(store.ProfilePath("default"))
			if err != nil {
				t.Fatal(err)
			}
			data = append([]byte(`{"unknownSecret":"cap_0123456789abcdef" ,`), data[1:]...)
			writeInitProfileRaw(t, store, data, 0o600)
		}},
		{name: "trailing-json", mutate: func(t *testing.T, store profile.Store) {
			data, err := os.ReadFile(store.ProfilePath("default"))
			if err != nil {
				t.Fatal(err)
			}
			writeInitProfileRaw(t, store, append(data, []byte("{}\n")...), 0o600)
		}},
		{name: "public-mode", mutate: func(t *testing.T, store profile.Store) {
			if err := os.Chmod(store.ProfilePath("default"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "public-profile-directory", mutate: func(t *testing.T, store profile.Store) {
			if err := os.Chmod(store.ProfileDir("default"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "public-store-root", mutate: func(t *testing.T, store profile.Store) {
			if err := os.Chmod(store.Root, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked-profile", mutate: func(t *testing.T, store profile.Store) {
			outside := t.TempDir()
			outsideStore := profile.Store{Root: outside}
			outsideProfile := profile.Default("default")
			outsideProfile.Metadata = map[string]string{"templateId": "attacker-template"}
			if err := outsideStore.Save(outsideProfile); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(store.ProfileDir("default")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outsideStore.ProfileDir("default"), store.ProfileDir("default")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := profile.Store{Root: t.TempDir()}
			if err := store.Save(profile.Default("default")); err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, store)
			before, err := snapshotInitTree(store.Root)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := (InitService{Core: New(store)}).Prepare(SetupInitServiceRequest())
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Review.State != InitStateBlocked || prepared.Review.RequiresConfirmation {
				t.Fatalf("review=%+v observation=%+v", prepared.Review, prepared.Observation)
			}
			if len(prepared.Review.Notices) != 1 || prepared.Review.Notices[0].Code != "setup.profile.blocked" ||
				len(prepared.Observation.Reason) > 160 || strings.ContainsAny(prepared.Observation.Reason, "\r\n") {
				t.Fatalf("blocked recovery is not bounded and typed: %+v", prepared)
			}
			for _, forbidden := range []string{store.Root, "cap_0123456789abcdef", "secret-host-path", "unknownSecret", "attacker-template"} {
				if strings.Contains(prepared.Observation.Reason, forbidden) || strings.Contains(prepared.Review.Notices[0].Summary, forbidden) {
					t.Fatalf("blocked reason leaked %q: %+v", forbidden, prepared)
				}
			}
			if prepared.Review.Template != "" || prepared.Review.EffectivePosture != "" || prepared.Review.Runtime.Family != "" {
				t.Fatalf("blocked review loaded unsafe profile presentation: %+v", prepared.Review)
			}
			after, err := snapshotInitTree(store.Root)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("blocked observation changed state:\nbefore=%+v\nafter=%+v", before, after)
			}
		})
	}
}

func TestInitServiceExistingMissingIdentityIsRepairableWithoutWrite(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if err := store.Save(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.ProfileDir("default"), "machine", "machine-id")); err != nil {
		t.Fatal(err)
	}
	prepared, err := (InitService{Core: New(store)}).Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Review.State != InitStateRepairable || prepared.Review.RequiresConfirmation {
		t.Fatalf("review=%+v", prepared.Review)
	}
	if len(prepared.Review.Notices) != 1 || prepared.Review.Notices[0].Code != "setup.profile.repair-required" {
		t.Fatalf("repairable recovery is not typed: %+v", prepared.Review.Notices)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("default"), "machine", "machine-id")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prepare repaired identity: %v", err)
	}
}

func TestInitServiceExistingPublicIdentityIsRepairableWithoutWrite(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if err := store.Save(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	machineID := filepath.Join(store.ProfileDir("default"), "machine", "machine-id")
	if err := os.Chmod(machineID, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := snapshotInitTree(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := (InitService{Core: New(store)}).Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Review.State != InitStateRepairable || prepared.Review.RequiresConfirmation {
		t.Fatalf("review=%+v", prepared.Review)
	}
	after, err := snapshotInitTree(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("repairable observation changed state:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestInitServiceExistingUnverifiableRuntimeIsBlockedWithoutWrite(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	baseline := InitService{Core: New(store)}
	fresh, err := baseline.Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	p := profile.Default("default")
	p.Environment.BaseImage = ""
	p.Environment.Runtime = fresh.Plan.RuntimeSelection
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	before, err := snapshotInitTree(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	service := InitService{
		Core: New(store),
		ResolveRuntime: func(runtimecatalog.Selection) (runtimecatalog.Resolution, error) {
			return runtimecatalog.Resolution{}, errors.New("catalog fixture contains cap_0123456789abcdef")
		},
	}
	prepared, err := service.Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Observation.State != InitStateBlocked || prepared.Review.State != InitStateBlocked ||
		len(prepared.Review.Notices) != 1 || prepared.Review.Notices[0].Code != "setup.profile.blocked" {
		t.Fatalf("unverifiable runtime did not block setup: %+v", prepared)
	}
	if strings.Contains(prepared.Review.Notices[0].Summary, "cap_0123456789abcdef") {
		t.Fatalf("runtime failure leaked detail: %+v", prepared.Review.Notices)
	}
	after, err := snapshotInitTree(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("runtime observation changed state:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestInitServiceAdvancedNativeApplyUsesBoundPlan(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	service := InitService{Core: New(store)}
	req := InitServiceRequest{
		Version: InitServiceRequestVersion, Mode: InitModeInit,
		ProfileName: "advanced", TemplateID: "dev", Backend: "native", Network: "direct",
		Onboarding: true, ExplicitProfile: true, ExplicitTemplate: true,
		ExplicitBackend: true, ExplicitNetwork: true, NoInput: true,
	}
	prepared, err := service.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(prepared, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "configured" || len(result.Result.Applied) == 0 {
		t.Fatalf("result=%+v", result)
	}
	loaded, err := store.Load("advanced")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Network.Mode != "direct" || loaded.Metadata["templateId"] != "dev" {
		t.Fatalf("profile=%+v", loaded)
	}
}

func TestInitServiceSetupPlanMatchesEquivalentExplicitInit(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	service := InitService{Core: New(store)}
	setup, err := service.Prepare(SetupInitServiceRequest())
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := service.Prepare(InitServiceRequest{
		Version: InitServiceRequestVersion, Mode: InitModeInit,
		ProfileName: "default", TemplateID: profiletemplate.Dev, Backend: "lima", Network: "direct",
		RuntimeFamily: "developer-standard", HostFSVisibility: profiletemplate.VisibilityNone,
		Onboarding: true, ExplicitProfile: true, ExplicitTemplate: true,
		ExplicitBackend: true, ExplicitNetwork: true, ExplicitVisibility: true, NoInput: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(setup.Plan, explicit.Plan) {
		setupJSON, _ := json.MarshalIndent(setup.Plan, "", "  ")
		explicitJSON, _ := json.MarshalIndent(explicit.Plan, "", "  ")
		t.Fatalf("setup and explicit init effects differ:\nsetup=%s\nexplicit=%s", setupJSON, explicitJSON)
	}
}

func TestCoreApplyInitSerializesThroughOneLockOwner(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	plan, err := core.PlanInit(inittask.Options{ProfileName: "direct", Backend: "native", Network: "direct", NoInput: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyInit(plan, inittask.ApplyOptions{NoInput: true}); err != nil {
		t.Fatal(err)
	}
}

type initTreeEntry struct {
	Mode    os.FileMode
	ModTime time.Time
	Digest  string
}

func snapshotInitTree(root string) (map[string]initTreeEntry, error) {
	out := map[string]initTreeEntry{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := initTreeEntry{Mode: info.Mode(), ModTime: info.ModTime()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			entry.Digest = hex.EncodeToString(sum[:])
		}
		out[relative] = entry
		return nil
	})
	return out, err
}

func snapshotInitAuthorityTree(root string) (map[string]initTreeEntry, error) {
	out := map[string]initTreeEntry{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == ".locks" || strings.HasPrefix(relative, ".locks"+string(filepath.Separator)) ||
			relative == "daemon" || strings.HasPrefix(relative, "daemon"+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		entry := initTreeEntry{Mode: info.Mode(), ModTime: info.ModTime()}
		if relative == "." {
			// Creating the store-rooted mutation lock is a permitted runtime
			// effect. It can update the store directory mtime without changing
			// any profile, identity, environment, runtime, audit, or evidence.
			entry.ModTime = time.Time{}
		}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			entry.Digest = hex.EncodeToString(sum[:])
		}
		out[relative] = entry
		return nil
	})
	return out, err
}

func writeInitProfileRaw(t *testing.T, store profile.Store, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(store.ProfilePath("default"), data, mode); err != nil {
		t.Fatal(err)
	}
}

func clonePreparedInit(t *testing.T, value PreparedInit) PreparedInit {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out PreparedInit
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
