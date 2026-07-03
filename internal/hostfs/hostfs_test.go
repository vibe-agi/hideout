package hostfs

import (
	"errors"
	"os"
	"path/filepath"
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

func TestHardDenyGrantFailsValidation(t *testing.T) {
	_, err := Build(BuildInput{
		Profile: Config{Grants: []Rule{readGrant("/Users/alice/.ssh/id_ed25519", ScopeExactFile, "ssh key")}},
	})
	if err == nil || !strings.Contains(err.Error(), "hard-deny") {
		t.Fatalf("expected hard-deny validation failure, got %v", err)
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

func TestRejectsInvalidGlobPattern(t *testing.T) {
	_, err := Build(BuildInput{
		Profile: Config{Grants: []Rule{readGrant("/Users/alice/Downloads/[.txt", ScopeGlob, "bad glob")}},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid HostFS glob pattern") {
		t.Fatalf("expected invalid glob pattern failure, got %v", err)
	}
}

func TestRejectsBroadRecursiveHomeGrant(t *testing.T) {
	_, err := Build(BuildInput{
		Profile: Config{Grants: []Rule{readGrant("/Users/alice", ScopeRecursiveDir, "home")}},
	})
	if err == nil || !strings.Contains(err.Error(), "broad HostFS grant") {
		t.Fatalf("expected broad grant validation failure, got %v", err)
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
