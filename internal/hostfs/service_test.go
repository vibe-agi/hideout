package hostfs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServiceReturnsCoarseLockedDiscoverResults(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "report.txt")
	dir := filepath.Join(root, "nested")
	link := filepath.Join(root, "report-link")
	if err := os.WriteFile(file, []byte("top-secret-content"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	service := discoverService(t, Config{Grants: []Rule{{
		ID: "hfs_see_dir", HostPath: root, Ops: []Op{OpDiscover}, Scope: ScopeDir, Reason: "navigate",
	}}})

	info, err := service.Stat(file)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Kind != "file" || !info.Locked || !info.Coarse || info.Size != 0 || info.Mode != "" || !info.ModTime.IsZero() || len(info.Caps) != 1 || info.Caps[0] != "discover" {
		t.Fatalf("locked stat leaked metadata or omitted posture: %+v", info)
	}
	linkInfo, err := service.Stat(link)
	if err != nil {
		t.Fatalf("Stat symlink: %v", err)
	}
	if linkInfo.Kind != "symlink" || !linkInfo.Locked || !linkInfo.Coarse {
		t.Fatalf("symlink target/kind contract failed: %+v", linkInfo)
	}
	entries, err := service.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries=%+v", entries)
	}
	for _, entry := range entries {
		if !entry.Locked || !entry.Coarse || entry.Size != 0 || entry.Mode != "" || len(entry.Caps) != 1 || entry.Caps[0] != "discover" {
			t.Fatalf("entry leaked metadata: %+v", entry)
		}
	}
	if _, err := service.Read(file, 0, 0); !errors.Is(err, ErrReadApprovalRequired) {
		t.Fatalf("locked read error=%v want approval eligible", err)
	}
}

func TestServiceSeeDirIsOneLevelAndExactDirectoryIsNotEnumerable(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "deep.txt"), []byte("deep"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := discoverService(t, Config{Grants: []Rule{{
		ID: "hfs_one", HostPath: root, Ops: []Op{OpDiscover}, Scope: ScopeDir, Reason: "one level",
	}}})
	if _, err := service.Stat(nested); err != nil {
		t.Fatalf("one-level child lookup: %v", err)
	}
	if _, err := service.List(nested); !errors.Is(err, ErrDirectoryNotEnumerable) {
		t.Fatalf("child directory list error=%v want not enumerable", err)
	}
	if _, err := service.Stat(filepath.Join(nested, "deep.txt")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deep child should remain hidden, got %v", err)
	}

	exact := discoverService(t, Config{Grants: []Rule{{
		ID: "hfs_exact_dir", HostPath: nested, Ops: []Op{OpDiscover}, Scope: ScopeExactFile, Reason: "exact dir",
	}}})
	if info, err := exact.Stat(nested); err != nil || info.Kind != "dir" {
		t.Fatalf("exact directory lookup failed: info=%+v err=%v", info, err)
	}
	if _, err := exact.List(nested); !errors.Is(err, ErrDirectoryNotEnumerable) {
		t.Fatalf("exact directory list error=%v", err)
	}
}

func TestServiceDiscoverListIsCompleteOrError(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		name := filepath.Join(root, string(rune('a'+i))+".txt")
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := discoverService(t, Config{Grants: []Rule{{
		ID: "hfs_tree", HostPath: root, Ops: []Op{OpDiscover}, Scope: ScopeRecursiveDir, Reason: "tree",
	}}})
	service.MaxListEntries = 2
	if _, err := service.List(root); !errors.Is(err, ErrDirectoryIncomplete) {
		t.Fatalf("overflow error=%v want incomplete", err)
	}

	service.MaxListEntries = 4096
	service.lstat = func(path string) (os.FileInfo, error) {
		if filepath.Base(path) == "b.txt" {
			return nil, os.ErrPermission
		}
		return os.Lstat(path)
	}
	if _, err := service.List(root); !errors.Is(err, ErrHostPrerequisite) {
		t.Fatalf("child inspection error=%v want host prerequisite", err)
	}
}

func TestServiceDiscoverTreeDepthLimitIsExplicitIncomplete(t *testing.T) {
	root := t.TempDir()
	path := root
	for i := 0; i < MaxDiscoverDepth+1; i++ {
		path = filepath.Join(path, "d")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	service := discoverService(t, Config{Grants: []Rule{{
		ID: "hfs_tree", HostPath: root, Ops: []Op{OpDiscover}, Scope: ScopeRecursiveDir, Reason: "tree",
	}}})
	if _, err := service.Stat(path); !errors.Is(err, ErrDirectoryIncomplete) {
		t.Fatalf("depth overflow error=%v want incomplete", err)
	}
}

func TestServiceDiscoverListOmitsDiscoverDeniedEvenWithExactContentGrant(t *testing.T) {
	root := t.TempDir()
	hidden := filepath.Join(root, "hidden.txt")
	readable := filepath.Join(root, "readable.txt")
	visible := filepath.Join(root, "visible.txt")
	for _, path := range []string{hidden, readable, visible} {
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := discoverService(t, Config{
		Grants: []Rule{
			{ID: "hfs_tree", HostPath: root, Ops: []Op{OpDiscover}, Scope: ScopeRecursiveDir, Reason: "tree"},
			withRuleID(readGrant(readable, ScopeExactFile, "exact read"), "hfs_readable"),
		},
		Deny: []Rule{
			{ID: "hfs_hide", HostPath: hidden, Ops: []Op{OpDiscover}, Scope: ScopeExactFile, Reason: "hide"},
			{ID: "hfs_hide_readable", HostPath: readable, Ops: []Op{OpDiscover}, Scope: ScopeExactFile, Reason: "broad exclusion"},
		},
	})
	entries, err := service.List(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	sort.Strings(names)
	if got := names; len(got) != 1 || got[0] != "visible.txt" {
		t.Fatalf("unexpected visible names: %v", got)
	}
	info, err := service.Stat(readable)
	if err != nil || info.Coarse || info.Size == 0 {
		t.Fatalf("exact content grant did not retain ordinary stat: info=%+v err=%v", info, err)
	}
	read, err := service.Read(readable, 0, 64)
	if err != nil || ReadResultDataString(read) != "readable.txt" {
		t.Fatalf("discover deny revoked exact content grant: read=%+v err=%v", read, err)
	}
}

func TestServiceManualHomeTreeImplicitlyHidesCatalogRootsWithoutRevokingExactRead(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	sshDir := filepath.Join(home, ".ssh")
	storeRoot := filepath.Join(home, ".hideout")
	key := filepath.Join(sshDir, "id_test")
	visible := filepath.Join(home, "notes.txt")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("exact-key-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(visible, []byte("visible"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	policy, err := Build(BuildInput{
		StoreRoot: storeRoot,
		Profile: Config{Grants: []Rule{
			{ID: "hfs_home", HostPath: home, Ops: []Op{OpDiscover}, Scope: ScopeRecursiveDir, Reason: "manual home visibility"},
			withRuleID(readGrant(key, ScopeExactFile, "exact operator read"), "hfs_key_read"),
		}, Deny: []Rule{{
			ID: "hfs_exact_hide", HostPath: sshDir, Ops: []Op{OpDiscover}, Scope: ScopeExactFile, Reason: "narrow user exclusion",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(policy)
	entries, err := service.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "notes.txt" || !entries[0].Coarse {
		t.Fatalf("manual home discovery exposed a hidden catalog root: %+v", entries)
	}
	if _, err := service.List(sshDir); !errors.Is(err, ErrNotFound) {
		t.Fatalf("direct enumeration of discover-denied root err=%v want ErrNotFound", err)
	}
	ancestor, err := service.Stat(sshDir)
	if err != nil || ancestor.Kind != "dir" || !ancestor.Coarse {
		t.Fatalf("exact content grant lost the synthetic ancestor needed for direct lookup: info=%+v err=%v", ancestor, err)
	}
	visibility := service.Policy.Visibility(key)
	if !visibility.DiscoverDenied || visibility.State != VisibilityContentGranted {
		t.Fatalf("exact content visibility did not retain discover denial: %+v", visibility)
	}
	read, err := service.Read(key, 0, 64)
	if err != nil || ReadResultDataString(read) != "exact-key-content" {
		t.Fatalf("implicit discover deny revoked exact read: read=%+v err=%v", read, err)
	}
}

func TestServiceContentTreeListCannotBypassDiscoverDeny(t *testing.T) {
	root := t.TempDir()
	hidden := filepath.Join(root, "hidden.txt")
	visible := filepath.Join(root, "visible.txt")
	for _, path := range []string{hidden, visible} {
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	service := discoverService(t, Config{
		Grants: []Rule{
			{ID: "hfs_see_tree", HostPath: root, Ops: []Op{OpDiscover}, Scope: ScopeRecursiveDir, Reason: "navigate"},
			{ID: "hfs_content_tree", HostPath: root, Ops: []Op{OpRead, OpList}, Scope: ScopeRecursiveDir, Reason: "explicit content tree"},
		},
		Deny: []Rule{{ID: "hfs_hide", HostPath: hidden, Ops: []Op{OpDiscover}, Scope: ScopeExactFile, Reason: "hide name"}},
	})
	entries, err := service.List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "visible.txt" || entries[0].Size == 0 {
		t.Fatalf("content list bypassed discover deny or lost granted metadata: %+v", entries)
	}
	read, err := service.Read(hidden, 0, 64)
	if err != nil || ReadResultDataString(read) != "hidden.txt" {
		t.Fatalf("discover deny revoked explicit content tree: read=%+v err=%v", read, err)
	}
}

func TestServiceBoundsConcurrentDiscoveryEnumeration(t *testing.T) {
	root := t.TempDir()
	service := discoverService(t, Config{Grants: []Rule{{
		ID: "hfs_tree", HostPath: root, Ops: []Op{OpDiscover}, Scope: ScopeRecursiveDir, Reason: "tree",
	}}})
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	service.readDir = func(path string) ([]os.DirEntry, error) {
		entered <- struct{}{}
		<-release
		return os.ReadDir(path)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = service.List(root)
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("enumeration did not enter")
		}
	}
	if _, err := service.List(root); !errors.Is(err, ErrDirectoryIncomplete) {
		t.Fatalf("fifth concurrent enumeration error=%v", err)
	}
	close(release)
	wg.Wait()
}

func TestServiceUnauthorizedWriteChangesOnlyInsideExplicitDiscoverDomain(t *testing.T) {
	root := t.TempDir()
	explicitPath := filepath.Join(root, "explicit.txt")
	legacyPath := filepath.Join(root, "legacy.txt")
	for _, path := range []string{explicitPath, legacyPath} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := Build(BuildInput{Run: Config{Grants: []Rule{
		{ID: "hfs_see", HostPath: explicitPath, Ops: []Op{OpDiscover}, Scope: ScopeExactFile, Reason: "explicit visibility"},
		{ID: "hfs_read", HostPath: legacyPath, Ops: []Op{OpRead}, Scope: ScopeExactFile, Reason: "legacy content"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(policy)
	if _, err := service.StageWrite(WriteRequest{Op: OpWrite, Path: explicitPath, Data: []byte("x")}); !errors.Is(err, ErrWriteUnauthorized) {
		t.Fatalf("explicit discover write err=%v want ErrWriteUnauthorized", err)
	}
	if _, err := service.StageWrite(WriteRequest{Op: OpWrite, Path: legacyPath, Data: []byte("x")}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("legacy grant-implied write err=%v want prior ErrUnsupported", err)
	}
}

type hostAppResourceAuthorityStub struct {
	sessionID   string
	profile     string
	ownerActive bool
	readAllowed bool
	checks      []HostAppResourceCheck
}

func (s *hostAppResourceAuthorityStub) AllowsRead(ReadGrantCheck) (bool, error) {
	return s.readAllowed, nil
}

func (s *hostAppResourceAuthorityStub) ValidateHostAppResource(check HostAppResourceCheck) error {
	s.checks = append(s.checks, check)
	if !s.ownerActive || check.Owner.SessionID != s.sessionID || check.Owner.Profile != s.profile {
		return errors.New("portal owner is not live")
	}
	return nil
}

func TestHostAppResourceRequiresLiveContentOrTreeAuthority(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "content", "report.txt")
	dir := filepath.Join(root, "tree")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := Build(BuildInput{Profile: Config{Grants: []Rule{
		{ID: "hfs_content", HostPath: file, Ops: []Op{OpRead}, Scope: ScopeExactFile, Reason: "content"},
		{ID: "hfs_tree", HostPath: dir, Ops: []Op{OpRead, OpList}, Scope: ScopeRecursiveDir, Reason: "tree"},
		{ID: "hfs_see", HostPath: root, Ops: []Op{OpDiscover}, Scope: ScopeRecursiveDir, Reason: "names only"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	authority := &hostAppResourceAuthorityStub{sessionID: "ses_hostapp", profile: "privacy", ownerActive: true}
	service := NewService(policy)
	service.ReadAuthority = authority
	owner := HostAppResourceOwner{SessionID: authority.sessionID, Profile: authority.profile}

	fileResource, err := service.ResolveHostAppResource(owner, file)
	if err != nil {
		t.Fatalf("file content authority: %v", err)
	}
	canonicalFile, _ := filepath.EvalSymlinks(file)
	if fileResource.HostPath() != canonicalFile || fileResource.RelativeTarget() != "report.txt" || fileResource.ResourceType() != HostAppResourceFile {
		t.Fatalf("unexpected file resource: host=%q relative=%q type=%q", fileResource.HostPath(), fileResource.RelativeTarget(), fileResource.ResourceType())
	}
	treeResource, err := service.ResolveHostAppResource(owner, dir)
	if err != nil {
		t.Fatalf("tree content authority: %v", err)
	}
	canonicalDir, _ := filepath.EvalSymlinks(dir)
	if treeResource.HostPath() != canonicalDir || treeResource.RelativeTarget() != filepath.Base(dir) || treeResource.ResourceType() != HostAppResourceTree {
		t.Fatalf("unexpected tree resource: host=%q relative=%q type=%q", treeResource.HostPath(), treeResource.RelativeTarget(), treeResource.ResourceType())
	}
	if len(authority.checks) != 2 {
		t.Fatalf("owner checks=%d want 2", len(authority.checks))
	}

	encoded, err := json.Marshal(fileResource)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), root) || string(encoded) != "{}" {
		t.Fatalf("HostFS authority handle exposed a lower path: %s", encoded)
	}
}

func TestHostAppResourceRejectsNarrowOrPuncturedDirectoryAuthority(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "tree")
	private := filepath.Join(tree, "private.txt")
	if err := os.MkdirAll(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		grants []Rule
		deny   []Rule
	}{
		{
			name: "root-only directory",
			grants: []Rule{{
				ID: "hfs_dir", HostPath: tree, Ops: []Op{OpRead, OpList}, Scope: ScopeDir, Reason: "one directory",
			}},
		},
		{
			name: "list-only recursive tree",
			grants: []Rule{{
				ID: "hfs_list_tree", HostPath: tree, Ops: []Op{OpList}, Scope: ScopeRecursiveDir, Reason: "names without content",
			}},
		},
		{
			name: "recursive tree with child deny",
			grants: []Rule{{
				ID: "hfs_tree", HostPath: tree, Ops: []Op{OpRead, OpList}, Scope: ScopeRecursiveDir, Reason: "tree with a hole",
			}},
			deny: []Rule{{
				ID: "hfs_private", HostPath: private, Scope: ScopeExactFile, Reason: "private child",
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := Build(BuildInput{Profile: Config{Grants: tc.grants, Deny: tc.deny}})
			if err != nil {
				t.Fatal(err)
			}
			authority := &hostAppResourceAuthorityStub{sessionID: "ses_hostapp", profile: "privacy", ownerActive: true}
			service := NewService(policy)
			service.ReadAuthority = authority
			if _, err := service.ResolveHostAppResource(HostAppResourceOwner{SessionID: authority.sessionID, Profile: authority.profile}, tree); !errors.Is(err, ErrHostAppResourceUnauthorized) {
				t.Fatalf("directory authority error=%v", err)
			}
			if len(authority.checks) != 0 {
				t.Fatalf("invalid tree reached Manager owner validation: %+v", authority.checks)
			}
		})
	}
}

func TestHostAppResourceRejectsSeeOnlyReservedExpiredAndEndedOwner(t *testing.T) {
	now := time.Now().UTC()
	root := t.TempDir()
	seeOnly := filepath.Join(root, "see-only.txt")
	expiring := filepath.Join(root, "expiring.txt")
	storeRoot := filepath.Join(root, "store")
	reserved := filepath.Join(storeRoot, "control.json")
	for _, path := range []string{seeOnly, expiring, reserved} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	expires := now.Add(time.Minute)
	policy := EffectivePolicy{
		Now: now, ReservedRoots: []string{storeRoot},
		Grants: []Grant{
			{Rule: Rule{ID: "hfs_see", HostPath: seeOnly, Ops: []Op{OpDiscover}, Scope: ScopeExactFile}, Source: SourceProfile},
			{Rule: Rule{ID: "hfs_expiring", HostPath: expiring, Ops: []Op{OpRead}, Scope: ScopeExactFile, ExpiresAt: &expires}, Source: SourceProfile},
			{Rule: Rule{ID: "hfs_reserved", HostPath: reserved, Ops: []Op{OpRead}, Scope: ScopeExactFile}, Source: SourceProfile},
		},
	}
	authority := &hostAppResourceAuthorityStub{sessionID: "ses_hostapp", profile: "privacy", ownerActive: true}
	service := NewService(policy)
	service.ReadAuthority = authority
	owner := HostAppResourceOwner{SessionID: authority.sessionID, Profile: authority.profile}

	if _, err := service.ResolveHostAppResource(owner, seeOnly); !errors.Is(err, ErrHostAppResourceUnauthorized) {
		t.Fatalf("see-only error=%v", err)
	}
	if _, err := service.ResolveHostAppResource(owner, reserved); !errors.Is(err, ErrHostAppResourceUnauthorized) {
		t.Fatalf("reserved-root error=%v", err)
	}
	service.now = func() time.Time { return expires.Add(time.Second) }
	if _, err := service.ResolveHostAppResource(owner, expiring); !errors.Is(err, ErrHostAppResourceUnauthorized) {
		t.Fatalf("expired authority error=%v", err)
	}
	service.now = func() time.Time { return now }
	authority.ownerActive = false
	if _, err := service.ResolveHostAppResource(owner, expiring); !errors.Is(err, ErrHostAppResourceOwner) {
		t.Fatalf("ended-owner error=%v", err)
	}
	if _, err := service.ResolveHostAppResource(HostAppResourceOwner{SessionID: "ses_other", Profile: "privacy"}, expiring); !errors.Is(err, ErrHostAppResourceOwner) {
		t.Fatalf("wrong-owner error=%v", err)
	}
}

func TestHostAppResourceConsumesActiveExactReadWithoutCreatingAuthority(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "locked.txt")
	if err := os.WriteFile(file, []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := Build(BuildInput{Profile: Config{Grants: []Rule{{
		ID: "hfs_see", HostPath: file, Ops: []Op{OpDiscover}, Scope: ScopeExactFile, Reason: "visible name",
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	authority := &hostAppResourceAuthorityStub{sessionID: "ses_hostapp", profile: "privacy", ownerActive: true, readAllowed: true}
	service := NewService(policy)
	service.ReadAuthority = authority
	resource, err := service.ResolveHostAppResource(HostAppResourceOwner{SessionID: authority.sessionID, Profile: authority.profile}, file)
	if err != nil {
		t.Fatalf("active exact-read authority: %v", err)
	}
	if !authority.checks[0].DynamicRead || resource.RelativeTarget() != filepath.Base(file) {
		t.Fatalf("dynamic authority was not retained as exact content: check=%+v relative=%q", authority.checks[0], resource.RelativeTarget())
	}
	authority.readAllowed = false
	if _, err := service.ResolveHostAppResource(HostAppResourceOwner{SessionID: authority.sessionID, Profile: authority.profile}, file); !errors.Is(err, ErrHostAppResourceUnauthorized) {
		t.Fatalf("revoked exact read error=%v", err)
	}
}

func TestHostAppResourceFinalRevalidationRejectsSymlinkRetarget(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.txt")
	second := filepath.Join(root, "second.txt")
	link := filepath.Join(root, "current.txt")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	policy, err := Build(BuildInput{Profile: Config{Grants: []Rule{{
		ID: "hfs_tree", HostPath: root, Ops: []Op{OpRead, OpList}, Scope: ScopeRecursiveDir, Reason: "content tree",
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	authority := &hostAppResourceAuthorityStub{sessionID: "ses_hostapp", profile: "privacy", ownerActive: true}
	service := NewService(policy)
	service.ReadAuthority = authority
	owner := HostAppResourceOwner{SessionID: authority.sessionID, Profile: authority.profile}
	resource, err := service.ResolveHostAppResource(owner, link)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	if err := service.RevalidateHostAppResource(owner, resource); !errors.Is(err, ErrHostAppResourceChanged) {
		t.Fatalf("retarget revalidation error=%v", err)
	}
}

func discoverService(t *testing.T, config Config) Service {
	t.Helper()
	policy, err := Build(BuildInput{Profile: config})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return NewService(policy)
}
