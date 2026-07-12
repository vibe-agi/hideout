package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/hostfs/readgrant"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestHostAppResourceManagerAuthorityRequiresLiveExactOwnerAndCurrentProfilePolicy(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	privacy, err := store.LoadOrInit("privacy")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.LoadOrInit("other")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(target, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	privacy.HostFS = hostfs.Config{Grants: []hostfs.Rule{{
		ID: "hfs_report", HostPath: target, Ops: []hostfs.Op{hostfs.OpRead}, Scope: hostfs.ScopeExactFile,
		Subject: hostfs.SubjectProfile, TTL: hostfs.TTLProfile, Reason: "operator content grant",
	}}}
	if err := store.Save(privacy); err != nil {
		t.Fatal(err)
	}
	other.HostFS = privacy.HostFS
	if err := store.Save(other); err != nil {
		t.Fatal(err)
	}
	policy, err := hostfs.Build(hostfs.BuildInput{Profile: privacy.HostFS, StoreRoot: store.Root})
	if err != nil {
		t.Fatal(err)
	}
	core := New(store)
	sessionID := "ses_host_app_resource"
	readDir := filepath.Join(store.Root, "sessions", sessionID, "hostfs-read")
	ownerPath := filepath.Join(readDir, "owner.lock")
	owner, err := readgrant.AcquireOwner(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := newHostFSReadProvider(core, sessionID, readDir, ownerPath, policy)
	if err != nil {
		_ = owner.Close()
		t.Fatal(err)
	}
	service := hostfs.NewService(policy)
	service.ReadAuthority = newHostAppRunResourceAuthority(provider, privacy.Name)
	exactOwner := hostfs.HostAppResourceOwner{SessionID: sessionID, Profile: privacy.Name}
	canonicalTarget, _ := filepath.EvalSymlinks(target)
	directCheck := hostfs.HostAppResourceCheck{
		Owner: exactOwner, RequestedPath: target, CanonicalPath: canonicalTarget, Operation: hostfs.OpRead,
		RequestedDecision: service.Policy.Decide(hostfs.OpRead, target), CanonicalDecision: service.Policy.Decide(hostfs.OpRead, canonicalTarget),
	}
	if err := provider.ValidateHostAppResource(directCheck); err != nil {
		_ = owner.Close()
		t.Fatalf("manager authority adapter rejected current policy: %v requested=%+v canonical=%+v", err, directCheck.RequestedDecision, directCheck.CanonicalDecision)
	}

	resource, err := service.ResolveHostAppResource(exactOwner, target)
	if err != nil {
		_ = owner.Close()
		t.Fatalf("current same-session authority: %v", err)
	}
	if resource.HostPath() != canonicalTarget || resource.RelativeTarget() != filepath.Base(target) {
		_ = owner.Close()
		t.Fatalf("unexpected resource: host=%q relative=%q", resource.HostPath(), resource.RelativeTarget())
	}
	if _, err := service.ResolveHostAppResource(hostfs.HostAppResourceOwner{SessionID: sessionID, Profile: "other"}, target); !errors.Is(err, hostfs.ErrHostAppResourceOwner) {
		_ = owner.Close()
		t.Fatalf("cross-profile owner error=%v", err)
	}

	privacy.HostFS = hostfs.Config{}
	if err := store.Save(privacy); err != nil {
		_ = owner.Close()
		t.Fatal(err)
	}
	if _, err := service.ResolveHostAppResource(exactOwner, target); !errors.Is(err, hostfs.ErrHostAppResourceUnauthorized) {
		_ = owner.Close()
		t.Fatalf("profile grant revocation error=%v", err)
	}

	privacy.HostFS = hostfs.Config{Grants: []hostfs.Rule{{
		ID: "hfs_report", HostPath: target, Ops: []hostfs.Op{hostfs.OpRead}, Scope: hostfs.ScopeExactFile,
		Subject: hostfs.SubjectProfile, TTL: hostfs.TTLProfile, Reason: "operator content grant",
	}}}
	if err := store.Save(privacy); err != nil {
		_ = owner.Close()
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveHostAppResource(exactOwner, target); !errors.Is(err, hostfs.ErrHostAppResourceOwner) {
		t.Fatalf("ended session owner error=%v", err)
	}
}

func TestHostAppResourceManagerAuthorityConsumesFreshAppliedReadGrant(t *testing.T) {
	fixture := newHostFSReadFixture(t)
	decisionID := activateHostFSReadFixture(t, fixture, fixture.file)
	service := hostfs.NewService(fixture.policy)
	service.ReadAuthority = fixture.provider
	owner := hostfs.HostAppResourceOwner{SessionID: fixture.sessionID, Profile: "default"}

	resource, err := service.ResolveHostAppResource(owner, fixture.file)
	if err != nil {
		t.Fatalf("active exact HostFS read grant: %v", err)
	}
	canonicalFile, _ := filepath.EvalSymlinks(fixture.file)
	if resource.HostPath() != canonicalFile || resource.RelativeTarget() != filepath.Base(fixture.file) {
		t.Fatalf("resource=%q relative=%q", resource.HostPath(), resource.RelativeTarget())
	}

	store, err := fixture.core.decisionStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailAppliedProviderDecision(decisionID, "hostfs.read"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveHostAppResource(owner, fixture.file); !errors.Is(err, hostfs.ErrHostAppResourceUnauthorized) {
		t.Fatalf("stale applied read decision error=%v", err)
	}
}

func TestHostAppResourceManagerAuthorityUsesCurrentExpiry(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p, err := store.LoadOrInit("privacy")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "expiring.txt")
	if err := os.WriteFile(target, []byte("expiring"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expires := now.Add(time.Minute)
	p.HostFS = hostfs.Config{Grants: []hostfs.Rule{{
		ID: "hfs_expiring", HostPath: target, Ops: []hostfs.Op{hostfs.OpRead}, Scope: hostfs.ScopeExactFile,
		Subject: hostfs.SubjectProfile, TTL: hostfs.TTLProfile, CreatedAt: &now, ExpiresAt: &expires, Reason: "expiring content",
	}}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	policy, err := hostfs.Build(hostfs.BuildInput{Profile: p.HostFS, StoreRoot: store.Root, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "ses_host_app_expiry"
	readDir := filepath.Join(store.Root, "sessions", sessionID, "hostfs-read")
	ownerPath := filepath.Join(readDir, "owner.lock")
	ownerLock, err := readgrant.AcquireOwner(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ownerLock.Close()
	provider, err := newHostFSReadProvider(New(store), sessionID, readDir, ownerPath, policy)
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return expires.Add(time.Second) }
	service := hostfs.NewService(policy)
	service.ReadAuthority = provider
	if _, err := service.ResolveHostAppResource(hostfs.HostAppResourceOwner{SessionID: sessionID, Profile: p.Name}, target); !errors.Is(err, hostfs.ErrHostAppResourceUnauthorized) {
		t.Fatalf("expired profile authority error=%v", err)
	}
}

func TestHostAppResourceManagerRevalidationRejectsLiveChildDeny(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	p, err := store.LoadOrInit("privacy")
	if err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	private := filepath.Join(tree, "private.txt")
	if err := os.WriteFile(private, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	treeGrant := hostfs.Rule{
		ID: "hfs_tree", HostPath: tree, Ops: []hostfs.Op{hostfs.OpRead, hostfs.OpList}, Scope: hostfs.ScopeRecursiveDir,
		Subject: hostfs.SubjectProfile, TTL: hostfs.TTLProfile, Reason: "operator tree grant",
	}
	p.HostFS = hostfs.Config{Grants: []hostfs.Rule{treeGrant}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	policy, err := hostfs.Build(hostfs.BuildInput{Profile: p.HostFS, StoreRoot: store.Root})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "ses_host_app_tree_deny"
	readDir := filepath.Join(store.Root, "sessions", sessionID, "hostfs-read")
	ownerPath := filepath.Join(readDir, "owner.lock")
	ownerLock, err := readgrant.AcquireOwner(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ownerLock.Close()
	provider, err := newHostFSReadProvider(New(store), sessionID, readDir, ownerPath, policy)
	if err != nil {
		t.Fatal(err)
	}
	service := hostfs.NewService(policy)
	service.ReadAuthority = provider
	owner := hostfs.HostAppResourceOwner{SessionID: sessionID, Profile: p.Name}
	resource, err := service.ResolveHostAppResource(owner, tree)
	if err != nil {
		t.Fatalf("initial tree authority: %v", err)
	}

	p.HostFS = hostfs.Config{
		Grants: []hostfs.Rule{treeGrant},
		Deny: []hostfs.Rule{{
			ID: "hfs_private", HostPath: private, Scope: hostfs.ScopeExactFile,
			Subject: hostfs.SubjectProfile, TTL: hostfs.TTLProfile, Reason: "live child deny",
		}},
	}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := service.RevalidateHostAppResource(owner, resource); !errors.Is(err, hostfs.ErrHostAppResourceUnauthorized) {
		t.Fatalf("live child deny revalidation error=%v", err)
	}
}

func TestStartRunDataPlaneBindsCompleteForbiddenRootsIntoInitialAndFinalIdentityChecks(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if _, err := store.LoadOrInit("default"); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	core.HostAppPlatform = hostcap.PlatformDarwin
	configureManagerHostAppIdentity(t, &core, t.TempDir())
	packSource := writeManagerHostAppPack(t, t.TempDir(), "community.identity-boundary", "boundary-editor")
	addPlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packSource, ProfileName: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(addPlan); err != nil {
		t.Fatal(err)
	}
	baseResolver := core.HostAppIdentityResolver
	core.HostAppIdentityRevalidator = nil

	workspace := t.TempDir()
	plan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: "native", Workspace: workspace, Command: []string{"echo", "identity-boundary"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runSession, err := core.BeginRunSession(plan, RunEnvironment{}, RunSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = core.CloseRunSession(runSession) }()
	runNetwork, err := core.PrepareRunNetwork(runSession, RunNetworkOptions{})
	if err != nil {
		t.Fatal(err)
	}

	writableRoot := t.TempDir()
	globWritableRoot := t.TempDir()
	readOnlyRoot := t.TempDir()
	activeRoot := t.TempDir()
	finalActiveRoot := t.TempDir()
	activeRoots := []string{activeRoot}
	activeChecks := 0
	core.HostAppForbiddenRoots = func(profileName string) ([]string, error) {
		activeChecks++
		if profileName != runSession.Plan.ProfileName {
			t.Fatalf("forbidden-root profile=%q want %q", profileName, runSession.Plan.ProfileName)
		}
		return append([]string(nil), activeRoots...), nil
	}

	baseRequired := []string{
		store.Root,
		os.TempDir(),
		workspace,
		runSession.Layout.Dir,
		runSession.Layout.TmpDir,
		runSession.Layout.ShimDir,
		runSession.Layout.BrokerSock,
		runSession.Layout.BrokerEndpointPath,
		runSession.Layout.HostFSReadDir,
		runSession.ProfileDir,
		runSession.IdentityDir,
		runSession.RuntimeSessionDir,
		runSession.RuntimeShimDir,
		runSession.Env.Synthetic["HOME"],
		runSession.Env.Synthetic["TMPDIR"],
		writableRoot,
		globWritableRoot,
		packSource,
	}
	var identityRootChecks [][]string
	var missingChecks [][]string
	core.HostAppIdentityResolver = func(expectation hostcap.ApplicationExpectation, forbidden []string) (hostcap.ObservedApplicationIdentity, error) {
		identityRootChecks = append(identityRootChecks, append([]string(nil), forbidden...))
		required := append(append([]string(nil), baseRequired...), activeRoots...)
		if missing := missingCanonicalManagerTestPaths(forbidden, required); len(missing) > 0 {
			missingChecks = append(missingChecks, missing)
		}
		return baseResolver(expectation, forbidden)
	}

	dataPlane, err := core.StartRunDataPlane(context.Background(), runSession, runNetwork, RunDataPlaneOptions{HostFSRun: hostfs.Config{Grants: []hostfs.Rule{
		{ID: "hfs_write_tree", HostPath: writableRoot, Ops: []hostfs.Op{hostfs.OpWrite}, Overlay: true, Scope: hostfs.ScopeRecursiveDir, Reason: "writable tree"},
		{ID: "hfs_write_glob", HostPath: filepath.Join(globWritableRoot, "*.txt"), Ops: []hostfs.Op{hostfs.OpWrite}, Overlay: true, Scope: hostfs.ScopeGlob, Reason: "writable glob"},
		{ID: "hfs_read_tree", HostPath: readOnlyRoot, Ops: []hostfs.Op{hostfs.OpRead, hostfs.OpList}, Scope: hostfs.ScopeRecursiveDir, Reason: "read-only content"},
	}}})
	if err != nil {
		t.Fatalf("StartRunDataPlane: %v", err)
	}
	defer func() { _ = core.CloseRunDataPlane(dataPlane) }()
	if len(identityRootChecks) == 0 || len(missingChecks) != 0 {
		t.Fatalf("initial identity checks did not receive the complete production root set: checks=%d missing=%v", len(identityRootChecks), missingChecks)
	}
	if containsCanonicalManagerTestPath(identityRootChecks[0], readOnlyRoot) {
		t.Fatalf("read-only HostFS content was incorrectly treated as guest-writable: %v", identityRootChecks[0])
	}

	binding, ok := dataPlane.Broker.HostApp.Bindings.ResolveCommand("code")
	if !ok {
		t.Fatal("built-in projection binding is missing")
	}
	checksBeforeFinal := len(identityRootChecks)
	activeRoots = append(activeRoots, finalActiveRoot)
	current, err := dataPlane.Broker.HostApp.RevalidateIdentity(binding.Application, binding.ObservedIdentity)
	if err != nil || current.IdentityDigest() != binding.ObservedIdentity.IdentityDigest() {
		t.Fatalf("production final identity revalidation: identity=%+v err=%v", current, err)
	}
	if len(identityRootChecks) <= checksBeforeFinal || len(missingChecks) != 0 || !containsCanonicalManagerTestPath(identityRootChecks[len(identityRootChecks)-1], finalActiveRoot) {
		t.Fatalf("final identity check did not rebuild the complete live root set: checks=%d activeChecks=%d missing=%v roots=%v", len(identityRootChecks), activeChecks, missingChecks, identityRootChecks[len(identityRootChecks)-1])
	}
	if activeChecks < 2 {
		t.Fatalf("active forbidden roots were not re-read at final identity check: checks=%d", activeChecks)
	}
}

func missingCanonicalManagerTestPaths(got, want []string) []string {
	var missing []string
	for _, path := range want {
		if path != "" && !containsCanonicalManagerTestPath(got, path) {
			missing = append(missing, path)
		}
	}
	return missing
}

func containsCanonicalManagerTestPath(paths []string, want string) bool {
	want = filepath.Clean(want)
	if canonical, err := filepath.EvalSymlinks(want); err == nil {
		want = canonical
	}
	for _, path := range paths {
		if filepath.Clean(path) == want {
			return true
		}
	}
	return false
}
