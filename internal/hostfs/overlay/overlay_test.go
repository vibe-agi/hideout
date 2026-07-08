package overlay

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStageContentOperationsAreDurableAndLeaveHostUnchanged(t *testing.T) {
	for _, tt := range []struct {
		name       string
		operation  string
		base       string
		data       string
		size       int64
		want       string
		hostExists bool
	}{
		{name: "create", operation: "create", data: "new", want: "new"},
		{name: "replace", operation: "replace", base: "lower", data: "staged", want: "staged"},
		{name: "append", operation: "append", base: "lower", data: "+tail", want: "lower+tail"},
		{name: "truncate", operation: "truncate", base: "lower", size: 2, want: "lo"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "target.txt")
			if tt.base != "" {
				if err := os.WriteFile(target, []byte(tt.base), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			store := newTestStore(t, filepath.Join(root, ".overlay"))
			result, err := store.Stage(StageRequest{
				SessionID: "ses_1", Profile: "default", Backend: "native", Operation: tt.operation, Path: target,
				GrantID: "hfs_overlay", GrantSource: "profile", Data: []byte(tt.data), Size: tt.size,
				Privilege: Privilege{Status: "enforced", Reason: "target-no-sudo"},
			})
			if err != nil {
				t.Fatalf("Stage: %v", err)
			}
			if result.Operation.ID == "" || result.Decision.DecisionID == "" || result.Decision.State != StatePending {
				t.Fatalf("bad stage result: %+v", result)
			}
			ops, err := store.Operations()
			if err != nil || len(ops) != 1 {
				t.Fatalf("operations len=%d err=%v", len(ops), err)
			}
			decisions, err := store.Decisions()
			if err != nil || len(decisions) != 1 {
				t.Fatalf("decisions len=%d err=%v", len(decisions), err)
			}
			view, ok, err := store.View(target)
			if err != nil || !ok {
				t.Fatalf("overlay view ok=%v err=%v", ok, err)
			}
			if got := string(view.Data); got != tt.want {
				t.Fatalf("overlay data=%q want %q", got, tt.want)
			}
			host, err := os.ReadFile(target)
			if tt.base == "" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("create should not create lower host file, read err=%v data=%q", err, host)
				}
			} else if err != nil || string(host) != tt.base {
				t.Fatalf("lower host file changed: %q err=%v", host, err)
			}
		})
	}
}

func TestStageMetadataAndPathOperationsAreDurable(t *testing.T) {
	root := t.TempDir()
	store := newTestStore(t, filepath.Join(root, ".overlay"))
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("lower"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "newdir")
	if _, err := store.Stage(baseReq("mkdir", dir)); err != nil {
		t.Fatalf("mkdir stage: %v", err)
	}
	if view, ok, err := store.View(dir); err != nil || !ok || view.Kind != "dir" {
		t.Fatalf("mkdir view=%+v ok=%v err=%v", view, ok, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mkdir should not create lower host dir, err=%v", err)
	}

	dest := filepath.Join(root, "dest.txt")
	renameReq := baseReq("rename", source)
	renameReq.DestinationPath = dest
	if _, err := store.Stage(renameReq); err != nil {
		t.Fatalf("rename stage: %v", err)
	}
	if view, ok, err := store.View(source); err != nil || !ok || !view.Deleted {
		t.Fatalf("source should be tombstoned: %+v ok=%v err=%v", view, ok, err)
	}
	if view, ok, err := store.View(dest); err != nil || !ok || !view.Exists {
		t.Fatalf("dest should exist in overlay: %+v ok=%v err=%v", view, ok, err)
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rename should not create lower dest, err=%v", err)
	}

	deleteTarget := filepath.Join(root, "delete.txt")
	if err := os.WriteFile(deleteTarget, []byte("delete"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stage(baseReq("delete", deleteTarget)); err != nil {
		t.Fatalf("delete stage: %v", err)
	}
	if view, ok, err := store.View(deleteTarget); err != nil || !ok || !view.Deleted {
		t.Fatalf("delete view=%+v ok=%v err=%v", view, ok, err)
	}
	if _, err := os.Stat(deleteTarget); err != nil {
		t.Fatalf("delete should leave lower host target in place: %v", err)
	}

	if _, err := store.Stage(baseReq("chmod", source)); err != nil {
		t.Fatalf("chmod stage: %v", err)
	}
	chownReq := baseReq("chown", source)
	chownReq.Owner = "501"
	chownReq.Group = "20"
	if _, err := store.Stage(chownReq); err != nil {
		t.Fatalf("chown stage: %v", err)
	}
	ops, err := store.Operations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 5 {
		t.Fatalf("operations=%d want 5", len(ops))
	}
}

func TestStageRejectsUnsafeAndUnsupportedOperations(t *testing.T) {
	root := t.TempDir()
	store := newTestStore(t, filepath.Join(root, ".overlay"))
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("lower"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stage(baseReq("replace", link)); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("symlink stage should fail unsafe, got %v", err)
	}
	if _, err := store.Stage(baseReq("symlink", filepath.Join(root, "new-link"))); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported op should fail closed, got %v", err)
	}
	if _, err := NewStore(filepath.Join(target, "child")); err == nil {
		t.Fatalf("overlay store below regular file should fail")
	}
}

func TestApplyContentOperationMutatesHostAndMarksApplied(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("lower"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, filepath.Join(root, ".overlay"))
	result, err := store.Stage(StageRequest{
		SessionID: "ses_1", Profile: "default", Backend: "native", Operation: "replace", Path: target,
		GrantID: "hfs_overlay", GrantSource: "profile", Data: []byte("applied"),
		Privilege: Privilege{Status: "enforced", Reason: "target-no-sudo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimDecision(t, store, result.Decision.DecisionID)
	applyResult, op, decision, err := store.Apply(result.Decision.DecisionID)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applyResult.Status != StateApplied || op.Status != StateApplied || decision.State != StateApplied {
		t.Fatalf("apply did not mark applied: result=%+v op=%+v decision=%+v", applyResult, op, decision)
	}
	body, err := os.ReadFile(target)
	if err != nil || string(body) != "applied" {
		t.Fatalf("host content=%q err=%v", body, err)
	}
	if _, err := os.Stat(store.objectPath(result.Operation.ContentObject)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("content object should be removed after apply, err=%v", err)
	}
}

func TestApplyConflictPreventsPartialMutation(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("lower"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, filepath.Join(root, ".overlay"))
	result, err := store.Stage(StageRequest{
		SessionID: "ses_1", Profile: "default", Backend: "native", Operation: "replace", Path: target,
		GrantID: "hfs_overlay", GrantSource: "profile", Data: []byte("staged"),
		Privilege: Privilege{Status: "enforced", Reason: "target-no-sudo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimDecision(t, store, result.Decision.DecisionID)
	if err := os.WriteFile(target, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	applyResult, op, decision, err := store.Apply(result.Decision.DecisionID)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply err=%v want conflict", err)
	}
	if applyResult.Status != StateConflict || op.Status != StateConflict || decision.State != StateConflict || !applyResult.PartialMutationPrevented {
		t.Fatalf("conflict state mismatch: result=%+v op=%+v decision=%+v", applyResult, op, decision)
	}
	body, err := os.ReadFile(target)
	if err != nil || string(body) != "changed" {
		t.Fatalf("host content changed despite conflict: %q err=%v", body, err)
	}
	if _, err := os.Stat(store.objectPath(result.Operation.ContentObject)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("content object should be removed after conflict, err=%v", err)
	}
}

func TestApplyRenameDestinationAppearanceConflicts(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	dest := filepath.Join(root, "dest.txt")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, filepath.Join(root, ".overlay"))
	req := baseReq("rename", source)
	req.DestinationPath = dest
	result, err := store.Stage(req)
	if err != nil {
		t.Fatal(err)
	}
	claimDecision(t, store, result.Decision.DecisionID)
	if err := os.WriteFile(dest, []byte("dest"), 0o600); err != nil {
		t.Fatal(err)
	}
	applyResult, _, _, err := store.Apply(result.Decision.DecisionID)
	if !errors.Is(err, ErrConflict) || applyResult.Status != StateConflict {
		t.Fatalf("Apply result=%+v err=%v want conflict", applyResult, err)
	}
	if body, err := os.ReadFile(source); err != nil || string(body) != "source" {
		t.Fatalf("source changed: %q err=%v", body, err)
	}
	if body, err := os.ReadFile(dest); err != nil || string(body) != "dest" {
		t.Fatalf("dest changed: %q err=%v", body, err)
	}
}

func TestApplySymlinkSwapConflicts(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	other := filepath.Join(root, "other.txt")
	if err := os.WriteFile(target, []byte("lower"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, filepath.Join(root, ".overlay"))
	result, err := store.Stage(StageRequest{
		SessionID: "ses_1", Profile: "default", Backend: "native", Operation: "replace", Path: target,
		GrantID: "hfs_overlay", GrantSource: "profile", Data: []byte("staged"),
		Privilege: Privilege{Status: "enforced", Reason: "target-no-sudo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimDecision(t, store, result.Decision.DecisionID)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, target); err != nil {
		t.Fatal(err)
	}
	applyResult, _, _, err := store.Apply(result.Decision.DecisionID)
	if !errors.Is(err, ErrConflict) || applyResult.Status != StateConflict {
		t.Fatalf("Apply result=%+v err=%v want conflict", applyResult, err)
	}
	if link, err := os.Readlink(target); err != nil || link != other {
		t.Fatalf("symlink changed: %q err=%v", link, err)
	}
	if body, err := os.ReadFile(other); err != nil || string(body) != "other" {
		t.Fatalf("symlink target changed: %q err=%v", body, err)
	}
}

func TestApplyChownRequiringHostPrivilegeFailsClosed(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("lower"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	store := newTestStore(t, filepath.Join(root, ".overlay"))
	req := baseReq("chown", target)
	req.Owner = fmt.Sprintf("%d", stat.Uid+1)
	req.Group = fmt.Sprintf("%d", stat.Gid)
	result, err := store.Stage(req)
	if err != nil {
		t.Fatal(err)
	}
	claimDecision(t, store, result.Decision.DecisionID)
	applyResult, op, decision, err := store.Apply(result.Decision.DecisionID)
	if err == nil || applyResult.Status != StateFailed || op.Status != StateFailed || decision.State != StateFailed {
		t.Fatalf("Apply result=%+v op=%+v decision=%+v err=%v want failed", applyResult, op, decision, err)
	}
	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	afterStat := after.Sys().(*syscall.Stat_t)
	if afterStat.Uid != stat.Uid || afterStat.Gid != stat.Gid {
		t.Fatalf("ownership changed uid/gid=%d/%d want %d/%d", afterStat.Uid, afterStat.Gid, stat.Uid, stat.Gid)
	}
}

func TestApplyPathAndMetadataOperations(t *testing.T) {
	root := t.TempDir()
	for _, tt := range []struct {
		name  string
		setup func(t *testing.T) (path string, req StageRequest, assert func(t *testing.T))
	}{
		{
			name: "mkdir",
			setup: func(t *testing.T) (string, StageRequest, func(t *testing.T)) {
				path := filepath.Join(root, "newdir")
				return path, baseReq("mkdir", path), func(t *testing.T) {
					if info, err := os.Stat(path); err != nil || !info.IsDir() {
						t.Fatalf("mkdir apply info=%+v err=%v", info, err)
					}
				}
			},
		},
		{
			name: "delete",
			setup: func(t *testing.T) (string, StageRequest, func(t *testing.T)) {
				path := filepath.Join(root, "delete.txt")
				if err := os.WriteFile(path, []byte("delete"), 0o600); err != nil {
					t.Fatal(err)
				}
				return path, baseReq("delete", path), func(t *testing.T) {
					if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("delete target still exists or unexpected err=%v", err)
					}
				}
			},
		},
		{
			name: "rename",
			setup: func(t *testing.T) (string, StageRequest, func(t *testing.T)) {
				path := filepath.Join(root, "source.txt")
				dest := filepath.Join(root, "renamed.txt")
				if err := os.WriteFile(path, []byte("rename"), 0o600); err != nil {
					t.Fatal(err)
				}
				req := baseReq("rename", path)
				req.DestinationPath = dest
				return path, req, func(t *testing.T) {
					if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("rename source still exists or unexpected err=%v", err)
					}
					if body, err := os.ReadFile(dest); err != nil || string(body) != "rename" {
						t.Fatalf("rename dest body=%q err=%v", body, err)
					}
				}
			},
		},
		{
			name: "chmod",
			setup: func(t *testing.T) (string, StageRequest, func(t *testing.T)) {
				path := filepath.Join(root, "chmod.txt")
				if err := os.WriteFile(path, []byte("chmod"), 0o644); err != nil {
					t.Fatal(err)
				}
				req := baseReq("chmod", path)
				req.Mode = "0600"
				return path, req, func(t *testing.T) {
					info, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					if got := info.Mode().Perm(); got != 0o600 {
						t.Fatalf("chmod mode=%o want 0600", got)
					}
				}
			},
		},
		{
			name: "chown-constrained-noop",
			setup: func(t *testing.T) (string, StageRequest, func(t *testing.T)) {
				path := filepath.Join(root, "chown.txt")
				if err := os.WriteFile(path, []byte("chown"), 0o600); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				stat := info.Sys().(*syscall.Stat_t)
				req := baseReq("chown", path)
				req.Owner = fmt.Sprintf("%d", stat.Uid)
				req.Group = fmt.Sprintf("%d", stat.Gid)
				return path, req, func(t *testing.T) {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("chown noop removed path: %v", err)
					}
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t, filepath.Join(root, ".overlay-"+tt.name))
			_, req, assert := tt.setup(t)
			result, err := store.Stage(req)
			if err != nil {
				t.Fatalf("Stage: %v", err)
			}
			claimDecision(t, store, result.Decision.DecisionID)
			applyResult, op, decision, err := store.Apply(result.Decision.DecisionID)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if applyResult.Status != StateApplied || op.Status != StateApplied || decision.State != StateApplied {
				t.Fatalf("apply state mismatch: result=%+v op=%+v decision=%+v", applyResult, op, decision)
			}
			assert(t)
		})
	}
}

func newTestStore(t *testing.T, root string) *Store {
	t.Helper()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func baseReq(operation, path string) StageRequest {
	return StageRequest{
		SessionID: "ses_1", Profile: "default", Backend: "native", Operation: operation, Path: path,
		GrantID: "hfs_overlay", GrantSource: "profile", Data: []byte("data"), Mode: "0644",
		Privilege: Privilege{Status: "enforced", Reason: "target-no-sudo"},
	}
}

func claimDecision(t *testing.T, store *Store, decisionID string) {
	t.Helper()
	decision, err := store.Decision(decisionID)
	if err != nil {
		t.Fatal(err)
	}
	decision.State = StateClaimed
	decision.Claim = &Claim{Surface: "test", ClaimedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Minute).UTC(), TokenHash: "hash"}
	if err := store.SaveDecision(decision); err != nil {
		t.Fatal(err)
	}
}
