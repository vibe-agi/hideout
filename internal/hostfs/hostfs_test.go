package hostfs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildComposesProfileEnvironmentAndRunGrants(t *testing.T) {
	policy, err := Build(BuildInput{
		Profile: Config{Grants: []Rule{readGrant("/Users/alice/Downloads/file.txt", ScopeExactFile, "profile grant")}},
		Environment: Config{Grants: []Rule{
			readGrant("/Users/alice/project-data", ScopeDir, "environment grant"),
		}},
		Run: Config{Grants: []Rule{
			readGrant("/tmp/one-run.txt", ScopeExactFile, "run grant"),
		}},
		Now: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Grants) != 3 {
		t.Fatalf("grant count=%d want 3: %+v", len(policy.Grants), policy.Grants)
	}
	tests := []struct {
		path   string
		source Source
	}{
		{"/Users/alice/Downloads/file.txt", SourceProfile},
		{"/Users/alice/project-data/data.csv", SourceEnvironment},
		{"/tmp/one-run.txt", SourceRun},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			decision := policy.Decide(OpRead, tt.path)
			if !decision.Allowed || decision.Source != tt.source {
				t.Fatalf("decision=%+v want allow source=%s", decision, tt.source)
			}
		})
	}
}

func TestDenyWinsOverAllow(t *testing.T) {
	policy, err := Build(BuildInput{
		Profile: Config{
			Grants: []Rule{withRuleID(readGrant("/Users/alice/Downloads", ScopeRecursiveDir, "downloads"), "hfs_allow_downloads")},
			Deny: []Rule{{
				ID:       "hfs_deny_private",
				HostPath: "/Users/alice/Downloads/private.txt",
				Scope:    ScopeExactFile,
				Reason:   "private file",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision := policy.Decide(OpRead, "/Users/alice/Downloads/public.txt"); !decision.Allowed {
		t.Fatalf("public read denied: %+v", decision)
	} else if decision.Effect != "allow" || decision.RuleID != "hfs_allow_downloads" || decision.Source != SourceProfile {
		t.Fatalf("public read decision missing allow metadata: %+v", decision)
	}
	if decision := policy.Decide(OpRead, "/Users/alice/Downloads/private.txt"); decision.Allowed || !strings.Contains(decision.Reason, "denied") {
		t.Fatalf("private read should be denied by deny rule: %+v", decision)
	} else if decision.Effect != "deny" || decision.RuleID != "hfs_deny_private" || decision.Source != SourceProfile {
		t.Fatalf("private read decision missing deny metadata: %+v", decision)
	}
}

func TestRunGrantDoesNotOverrideProfileDeny(t *testing.T) {
	policy, err := Build(BuildInput{
		Profile: Config{
			Deny: []Rule{{
				HostPath: "/Users/alice/Downloads/private.txt",
				Scope:    ScopeExactFile,
				Reason:   "profile deny",
			}},
		},
		Run: Config{
			Grants: []Rule{readGrant("/Users/alice/Downloads/private.txt", ScopeExactFile, "run grant")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := policy.Decide(OpRead, "/Users/alice/Downloads/private.txt")
	if decision.Allowed || !strings.Contains(decision.Reason, "denied") {
		t.Fatalf("run grant should not override profile deny: %+v", decision)
	}
}

func TestSensitiveUserFileGrantsAreUserAuthoritative(t *testing.T) {
	for _, path := range []string{
		"/Users/alice/.ssh/id_ed25519",
		"/Users/alice/.android/debug.keystore",
		"/Users/alice/Library/Keychains/login.keychain-db",
		"/Users/alice/Library/Application Support/Google/Chrome/Default/Cookies",
		"/Users/alice/Library/MobileDevice/Provisioning Profiles/dev.mobileprovision",
		"/Users/alice/Downloads/AuthKey_ABC123.p8",
		"/Users/alice/Downloads/release.jks",
		"/Users/alice/.npmrc",
		"/home/alice/.pypirc",
		"/home/alice/.config/google-chrome/Default/Cookies",
		"/home/alice/.mozilla/firefox/profile/cookies.sqlite",
		"/home/alice/.m2/settings.xml",
		"/Users/alice/.gradle/gradle.properties",
	} {
		t.Run(path, func(t *testing.T) {
			policy, err := Build(BuildInput{
				Profile: Config{Grants: []Rule{readGrant(path, ScopeExactFile, "sensitive credential")}},
			})
			if err != nil {
				t.Fatalf("explicit user grant for %s should be valid: %v", path, err)
			}
			decision := policy.Decide(OpRead, path)
			if !decision.Allowed {
				t.Fatalf("explicit user grant for %s should allow read: %+v", path, decision)
			}
		})
	}
}

func TestSensitiveUserFilesInsideGrantedDirectoryFollowUserGrant(t *testing.T) {
	policy, err := Build(BuildInput{
		Profile: Config{Grants: []Rule{readGrant("/Users/alice/Downloads", ScopeRecursiveDir, "downloads")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/Users/alice/Downloads/AuthKey_ABC123.p8",
		"/Users/alice/Downloads/release.keystore",
		"/Users/alice/Downloads/app.mobileprovision",
	} {
		t.Run(path, func(t *testing.T) {
			decision := policy.Decide(OpRead, path)
			if !decision.Allowed {
				t.Fatalf("sensitive file should follow explicit directory grant: %+v", decision)
			}
		})
	}
}

func TestStoreReservedRootGrantsAreNotExpressible(t *testing.T) {
	storeRoot := "/tmp/hideout-dogfood/.hideout"
	tests := []struct {
		name string
		rule Rule
	}{
		{
			name: "exact file inside store",
			rule: readGrant("/tmp/hideout-dogfood/.hideout/profiles/default/profile.json", ScopeExactFile, "store file"),
		},
		{
			name: "store directory",
			rule: readGrant("/tmp/hideout-dogfood/.hideout", ScopeDir, "store dir"),
		},
		{
			name: "store tree",
			rule: readGrant("/tmp/hideout-dogfood/.hideout", ScopeRecursiveDir, "store tree"),
		},
		{
			name: "ancestor tree",
			rule: readGrant("/tmp/hideout-dogfood", ScopeRecursiveDir, "dogfood root tree"),
		},
		{
			name: "glob inside store",
			rule: readGrant("/tmp/hideout-dogfood/.hideout/**/*.json", ScopeGlob, "store glob"),
		},
		{
			name: "ancestor glob",
			rule: readGrant("/tmp/hideout-dogfood/*", ScopeGlob, "dogfood root glob"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build(BuildInput{
				Profile:   Config{Grants: []Rule{tt.rule}},
				StoreRoot: storeRoot,
			})
			if err == nil || !strings.Contains(err.Error(), "control-plane store") {
				t.Fatalf("expected store reserved root rejection, got %v", err)
			}
		})
	}
}

func TestStoreReservedRootDoesNotRejectNonCoveringParentGlob(t *testing.T) {
	_, err := Build(BuildInput{
		Profile: Config{Grants: []Rule{
			readGrant("/tmp/hideout-dogfood/*.txt", ScopeGlob, "text files outside store"),
		}},
		StoreRoot: "/tmp/hideout-dogfood/.hideout",
	})
	if err != nil {
		t.Fatalf("non-covering parent glob should be allowed: %v", err)
	}
}

func TestStoreReservedRootDenyIsRuntimeEnforced(t *testing.T) {
	storeRoot := "/tmp/hideout-dogfood/.hideout"
	policy := EffectivePolicy{
		ReservedRoots: []string{storeRoot},
		Grants: []Grant{{
			Rule: readGrant(storeRoot, ScopeRecursiveDir, "legacy broad store grant"),
		}},
	}
	decision := policy.Decide(OpRead, "/tmp/hideout-dogfood/.hideout/sessions/ses_1/audit.jsonl")
	if decision.Allowed || decision.Effect != "deny" || decision.Reason != ReservedRootReason {
		t.Fatalf("reserved root should be denied at runtime: %+v", decision)
	}
}

func TestStoreReservedRootIsFilteredFromDirectoryList(t *testing.T) {
	root := t.TempDir()
	storeRoot := filepath.Join(root, ".hideout")
	if err := os.MkdirAll(filepath.Join(storeRoot, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	visible := filepath.Join(root, "visible.txt")
	if err := os.WriteFile(visible, []byte("visible"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := EffectivePolicy{
		ReservedRoots: []string{storeRoot},
		Grants: []Grant{{
			Rule: Rule{
				HostPath: root,
				Ops:      []Op{OpRead, OpStat, OpList},
				Scope:    ScopeDir,
				Reason:   "legacy parent dir grant",
			},
		}},
	}
	service := NewService(policy)
	entries, err := service.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "visible.txt" {
		t.Fatalf("reserved store root leaked or visible file missing: %+v", entries)
	}
}

func TestWriteClassGrantFailsValidation(t *testing.T) {
	rule := readGrant("/Users/alice/Downloads/file.txt", ScopeExactFile, "write")
	rule.Ops = []Op{OpWrite}
	_, err := Build(BuildInput{Profile: Config{Grants: []Rule{rule}}})
	if err == nil || !strings.Contains(err.Error(), "write-class") {
		t.Fatalf("expected write-class validation failure, got %v", err)
	}
}

func TestExpiredGrantIsIgnored(t *testing.T) {
	now := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	rule := readGrant("/Users/alice/Downloads/file.txt", ScopeExactFile, "expired")
	expiresAt := now.Add(-time.Second)
	rule.ExpiresAt = &expiresAt
	policy, err := Build(BuildInput{
		Profile: Config{Grants: []Rule{rule}},
		Now:     now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Grants) != 0 {
		t.Fatalf("expired grant should be absent: %+v", policy.Grants)
	}
	if decision := policy.Decide(OpRead, "/Users/alice/Downloads/file.txt"); decision.Allowed {
		t.Fatalf("expired grant allowed read: %+v", decision)
	} else if decision.Effect != "none" {
		t.Fatalf("expired grant should produce none effect: %+v", decision)
	}
}

func TestScopeMatching(t *testing.T) {
	policy, err := Build(BuildInput{Profile: Config{Grants: []Rule{
		readGrant("/Users/alice/Downloads/file.txt", ScopeExactFile, "file"),
		readGrant("/Users/alice/one-level", ScopeDir, "dir"),
		readGrant("/Users/alice/tree", ScopeRecursiveDir, "recursive"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		op   Op
		path string
		want bool
	}{
		{"exact file read", OpRead, "/Users/alice/Downloads/file.txt", true},
		{"exact parent synthetic list", OpList, "/Users/alice/Downloads", true},
		{"exact sibling denied", OpRead, "/Users/alice/Downloads/other.txt", false},
		{"dir child read", OpRead, "/Users/alice/one-level/data.csv", true},
		{"dir child list denied", OpList, "/Users/alice/one-level/nested", false},
		{"dir nested denied", OpRead, "/Users/alice/one-level/nested/data.csv", false},
		{"recursive nested read", OpRead, "/Users/alice/tree/nested/data.csv", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := policy.Decide(tt.op, tt.path)
			if decision.Allowed != tt.want {
				t.Fatalf("allowed=%t want %t decision=%+v", decision.Allowed, tt.want, decision)
			}
		})
	}
}

func TestGlobScopeMatching(t *testing.T) {
	policy, err := Build(BuildInput{Profile: Config{
		Grants: []Rule{withRuleID(readGrant("/Users/alice/Downloads/*.txt", ScopeGlob, "text files"), "hfs_txt")},
		Deny: []Rule{{
			ID:       "hfs_private",
			HostPath: "/Users/alice/Downloads/private-*.txt",
			Ops:      []Op{OpRead},
			Scope:    ScopeGlob,
			Reason:   "private text files",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		op        Op
		path      string
		wantAllow bool
		wantID    string
	}{
		{"matching read", OpRead, "/Users/alice/Downloads/notes.txt", true, "hfs_txt"},
		{"matching stat", OpStat, "/Users/alice/Downloads/notes.txt", true, "hfs_txt"},
		{"parent list", OpList, "/Users/alice/Downloads", true, "hfs_txt"},
		{"non matching extension", OpRead, "/Users/alice/Downloads/photo.jpg", false, ""},
		{"does not cross directory", OpRead, "/Users/alice/Downloads/nested/notes.txt", false, ""},
		{"glob deny wins", OpRead, "/Users/alice/Downloads/private-notes.txt", false, "hfs_private"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := policy.Decide(tt.op, tt.path)
			if decision.Allowed != tt.wantAllow {
				t.Fatalf("allowed=%t want %t decision=%+v", decision.Allowed, tt.wantAllow, decision)
			}
			if tt.wantID != "" && decision.RuleID != tt.wantID {
				t.Fatalf("ruleID=%q want %q decision=%+v", decision.RuleID, tt.wantID, decision)
			}
		})
	}
}

func TestGlobDenyMatchesCaseInsensitiveFilesystems(t *testing.T) {
	if !globMatchesWithCaseMode("/Users/alice/Downloads/private-*.txt", "/Users/alice/Downloads/Private-report.txt", true) {
		t.Fatal("case-insensitive glob should match path case variants")
	}
	policy, err := Build(BuildInput{Profile: Config{
		Grants: []Rule{withRuleID(readGrant("/Users/alice/Downloads/*.txt", ScopeGlob, "text files"), "hfs_txt")},
		Deny: []Rule{{
			ID:       "hfs_private",
			HostPath: "/Users/alice/Downloads/private-*.txt",
			Ops:      []Op{OpRead},
			Scope:    ScopeGlob,
			Reason:   "private text files",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	decision := policy.Decide(OpRead, "/Users/alice/Downloads/Private-report.txt")
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if decision.Allowed || decision.Effect != "deny" || decision.RuleID != "hfs_private" {
			t.Fatalf("case-insensitive platform should deny path case variants: %+v", decision)
		}
	} else if !decision.Allowed {
		t.Fatalf("case-sensitive platform should keep case-sensitive glob semantics: %+v", decision)
	}
}

func TestGlobDoesNotImplicitlyMatchDotfiles(t *testing.T) {
	policy, err := Build(BuildInput{Profile: Config{Grants: []Rule{
		withRuleID(readGrant("/Users/alice/project/*", ScopeGlob, "project files"), "hfs_project"),
		withRuleID(readGrant("/Users/alice/project/.*", ScopeGlob, "project dotfiles"), "hfs_dotfiles"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if decision := policy.Decide(OpRead, "/Users/alice/project/app.js"); !decision.Allowed || decision.RuleID != "hfs_project" {
		t.Fatalf("non-dotfile should match wildcard grant: %+v", decision)
	}
	if decision := policy.Decide(OpRead, "/Users/alice/project/.env"); !decision.Allowed || decision.RuleID != "hfs_dotfiles" {
		t.Fatalf("dotfile should require explicit dotfile grant: %+v", decision)
	}
	policy, err = Build(BuildInput{Profile: Config{Grants: []Rule{withRuleID(readGrant("/Users/alice/project/*", ScopeGlob, "project files"), "hfs_project")}}})
	if err != nil {
		t.Fatal(err)
	}
	if decision := policy.Decide(OpRead, "/Users/alice/project/.env"); decision.Allowed {
		t.Fatalf("implicit wildcard must not grant dotfiles: %+v", decision)
	}
}

func TestParseRuleSpecEscapesLiteralGlobMeta(t *testing.T) {
	rule, err := ParseRuleSpec("--fs", `read:/Users/alice/Downloads/\[2026\]-draft\?.txt`, "literal meta")
	if err != nil {
		t.Fatalf("ParseRuleSpec exact literal: %v", err)
	}
	if rule.Scope != ScopeExactFile || rule.HostPath != "/Users/alice/Downloads/[2026]-draft?.txt" {
		t.Fatalf("escaped literal selector should become exact file, got %+v", rule)
	}
	policy, err := Build(BuildInput{Profile: Config{Grants: []Rule{withRuleID(rule, "hfs_literal")}}})
	if err != nil {
		t.Fatal(err)
	}
	if decision := policy.Decide(OpRead, "/Users/alice/Downloads/[2026]-draft?.txt"); !decision.Allowed || decision.RuleID != "hfs_literal" {
		t.Fatalf("literal meta filename should be allowed: %+v", decision)
	}
	if decision := policy.Decide(OpRead, "/Users/alice/Downloads/2-draftX.txt"); decision.Allowed {
		t.Fatalf("escaped literal selector must not become a glob: %+v", decision)
	}

	backslashRule, err := ParseRuleSpec("--fs", `read:/Users/alice/Downloads/name\\with-backslash.txt`, "literal backslash")
	if err != nil {
		t.Fatalf("ParseRuleSpec literal backslash: %v", err)
	}
	if backslashRule.Scope != ScopeExactFile || backslashRule.HostPath != `/Users/alice/Downloads/name\with-backslash.txt` {
		t.Fatalf("escaped backslash selector should become exact file, got %+v", backslashRule)
	}

	globRule, err := ParseRuleSpec("--fs", `read:/Users/alice/Downloads/\[2026\]-*.txt`, "literal prefix glob")
	if err != nil {
		t.Fatalf("ParseRuleSpec mixed literal/glob: %v", err)
	}
	if globRule.Scope != ScopeGlob || globRule.HostPath != "/Users/alice/Downloads/[[]2026]-*.txt" {
		t.Fatalf("escaped literal prefix should be preserved for glob matcher, got %+v", globRule)
	}
	if !globMatches(globRule.HostPath, "/Users/alice/Downloads/[2026]-report.txt") {
		t.Fatalf("mixed literal/glob selector should match literal bracket prefix")
	}
	if globMatches(globRule.HostPath, "/Users/alice/Downloads/2-report.txt") {
		t.Fatalf("mixed literal/glob selector must not treat escaped bracket as character class")
	}
}

func TestRejectsInvalidGlobPattern(t *testing.T) {
	_, err := Build(BuildInput{
		Profile: Config{Grants: []Rule{readGrant("/Users/alice/Downloads/[.txt", ScopeGlob, "bad glob")}},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid HostFS glob pattern") {
		t.Fatalf("expected invalid glob pattern failure, got %v", err)
	}
}

func TestAllowsUserAuthorizedBroadRecursiveGrant(t *testing.T) {
	policy, err := Build(BuildInput{
		Profile: Config{Grants: []Rule{readGrant("/Users/alice", ScopeRecursiveDir, "home")}},
	})
	if err != nil {
		t.Fatalf("broad grant is an explicit user policy and should build: %v", err)
	}
	decision := policy.Decide(OpRead, "/Users/alice/Documents/file.txt")
	if !decision.Allowed {
		t.Fatalf("broad grant should allow matching path: %+v", decision)
	}
}

func TestRejectsMismatchedSourceSubjectAndTTL(t *testing.T) {
	rule := readGrant("/Users/alice/Downloads/file.txt", ScopeExactFile, "bad subject")
	rule.Subject = SubjectEnvironment
	_, err := Build(BuildInput{Profile: Config{Grants: []Rule{rule}}})
	if err == nil || !strings.Contains(err.Error(), "subject") {
		t.Fatalf("expected subject validation failure, got %v", err)
	}

	rule = readGrant("/Users/alice/Downloads/file.txt", ScopeExactFile, "bad ttl")
	rule.TTL = TTLRun
	_, err = Build(BuildInput{Profile: Config{Grants: []Rule{rule}}})
	if err == nil || !strings.Contains(err.Error(), "ttl") {
		t.Fatalf("expected ttl validation failure, got %v", err)
	}
}

func TestServiceReadAllowedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "visible.txt")
	if err := os.WriteFile(path, []byte("hello hostfs"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := mustBuildPolicy(t, Config{Grants: []Rule{{
		HostPath: path,
		Ops:      []Op{OpRead},
		Scope:    ScopeExactFile,
		Reason:   "test",
	}}})
	service := NewService(policy)
	result, err := service.Read(path, 0, 5)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(result.Data) != "hello" || result.Bytes != 5 || result.Info.Kind != "file" {
		t.Fatalf("unexpected read result: %+v data=%q", result, result.Data)
	}
}

func TestServiceListFiltersUngrantSiblings(t *testing.T) {
	root := t.TempDir()
	visible := filepath.Join(root, "visible.txt")
	hidden := filepath.Join(root, "hidden.txt")
	if err := os.WriteFile(visible, []byte("visible"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hidden, []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := mustBuildPolicy(t, Config{Grants: []Rule{{
		HostPath: visible,
		Ops:      []Op{OpRead},
		Scope:    ScopeExactFile,
		Reason:   "one file",
	}}})
	service := NewService(policy)
	entries, err := service.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "visible.txt" {
		t.Fatalf("list leaked sibling or missed grant: %+v", entries)
	}
}

func TestServiceGlobGrantFiltersDirectoryList(t *testing.T) {
	root := t.TempDir()
	visible := filepath.Join(root, "visible.txt")
	hidden := filepath.Join(root, "hidden.jpg")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	link := filepath.Join(root, "link.txt")
	if err := os.WriteFile(visible, []byte("visible"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hidden, []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	policy := mustBuildPolicy(t, Config{Grants: []Rule{{
		HostPath: filepath.Join(root, "*.txt"),
		Ops:      []Op{OpRead},
		Scope:    ScopeGlob,
		Reason:   "text files",
	}}})
	service := NewService(policy)
	entries, err := service.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "visible.txt" {
		t.Fatalf("list leaked non-matching or symlink entry: %+v", entries)
	}
	result, err := service.Read(visible, 0, 0)
	if err != nil {
		t.Fatalf("Read visible: %v", err)
	}
	if ReadResultDataString(result) != "visible" {
		t.Fatalf("visible data=%q", ReadResultDataString(result))
	}
	if _, err := service.Read(hidden, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden jpg should be denied, got %v", err)
	}
	if _, err := service.Read(link, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("glob symlink escape should be denied, got %v", err)
	}
}

func TestServiceSynthesizesGrantParentDirectories(t *testing.T) {
	root := t.TempDir()
	grantedDir := filepath.Join(root, "alice", "Downloads")
	if err := os.MkdirAll(grantedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	grantedFile := filepath.Join(grantedDir, "visible.txt")
	if err := os.WriteFile(grantedFile, []byte("visible"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bob"), 0o700); err != nil {
		t.Fatal(err)
	}
	policy := mustBuildPolicy(t, Config{Grants: []Rule{{
		HostPath: grantedFile,
		Ops:      []Op{OpRead},
		Scope:    ScopeExactFile,
		Reason:   "one file",
	}}})
	service := NewService(policy)
	info, err := service.Stat(root)
	if err != nil {
		t.Fatalf("synthetic parent stat: %v", err)
	}
	if info.Kind != "dir" {
		t.Fatalf("synthetic parent kind=%q", info.Kind)
	}
	entries, err := service.List(root)
	if err != nil {
		t.Fatalf("synthetic parent list: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "alice" || entries[0].Kind != "dir" {
		t.Fatalf("synthetic parent leaked or missed next component: %+v", entries)
	}
	entries, err = service.List(grantedDir)
	if err != nil {
		t.Fatalf("immediate parent list: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "visible.txt" || entries[0].Kind != "file" {
		t.Fatalf("immediate parent list mismatch: %+v", entries)
	}
}

func TestServiceDeniesDirectoryGrantSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	policy := mustBuildPolicy(t, Config{Grants: []Rule{{
		HostPath: root,
		Ops:      []Op{OpRead},
		Scope:    ScopeDir,
		Reason:   "directory should not follow symlink escape",
	}}})
	service := NewService(policy)
	_, err := service.Read(link, 0, 0)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected symlink escape to be hidden as not found, got %v", err)
	}
}

func TestServiceRejectsUnsupportedWrite(t *testing.T) {
	if err := NewService(EffectivePolicy{}).WriteUnsupported(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("write should be unsupported, got %v", err)
	}
}

func mustBuildPolicy(t *testing.T, c Config) EffectivePolicy {
	t.Helper()
	policy, err := Build(BuildInput{Profile: c})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func readGrant(path string, scope Scope, reason string) Rule {
	return Rule{
		HostPath: path,
		Ops:      []Op{OpRead},
		Scope:    scope,
		Reason:   reason,
	}
}

func withRuleID(rule Rule, id string) Rule {
	rule.ID = id
	return rule
}

func TestWorkspaceShadowedRules(t *testing.T) {
	workspace := t.TempDir()
	inside := filepath.Join(workspace, "secrets")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	cfg := Config{
		Grants: []Rule{{ID: "g1", HostPath: filepath.Join(inside, "*.txt"), Scope: ScopeGlob}},
		Deny:   []Rule{{ID: "d1", HostPath: inside, Scope: ScopeDir}, {ID: "d2", HostPath: outside, Scope: ScopeDir}},
	}
	shadowed := WorkspaceShadowedRules(cfg, workspace)
	ids := map[string]bool{}
	for _, rule := range shadowed {
		ids[rule.ID] = true
	}
	if !ids["g1"] || !ids["d1"] {
		t.Fatalf("in-workspace rules should be shadowed: %+v", shadowed)
	}
	if ids["d2"] {
		t.Fatalf("outside rule must not be shadowed: %+v", shadowed)
	}
	if len(WorkspaceShadowedRules(cfg, outside)) != 1 {
		t.Fatalf("only the outside rule shadows the other workspace")
	}
}
