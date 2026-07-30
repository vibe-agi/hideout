package store_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	"github.com/vibe-agi/hideout/internal/workloadobs/store"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestStoreCreatesOnlyHostPrivateDirectoriesAndFiles(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	owner := securityOwner(t, "env_private", "incarnation-private")
	activity, err := store.Open(securityOptions(root))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	record := securityRecord(owner, "ses_private", 1, "/workspace/private.txt")
	if err := activity.Append(context.Background(), record); err != nil {
		t.Fatalf("append record: %v", err)
	}
	if _, err := activity.Seal(context.Background(), owner); err != nil {
		t.Fatalf("seal record: %v", err)
	}
	if err := activity.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("store contains a symlink: %s", path)
			return nil
		}
		if info.IsDir() {
			if got := info.Mode().Perm(); got != 0o700 {
				t.Errorf("directory %s mode = %04o, want 0700", path, got)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			t.Errorf("store contains non-regular file %s (%s)", path, info.Mode())
		} else if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("file %s mode = %04o, want 0600", path, got)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("target/group/other readability leaked for %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk store: %v", err)
	}
}

func TestStoreRejectsSymlinkAndHardlinkReplacement(t *testing.T) {
	t.Parallel()

	t.Run("root symlink", func(t *testing.T) {
		parent := t.TempDir()
		realRoot := filepath.Join(parent, "real")
		if err := os.Mkdir(realRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		linkRoot := filepath.Join(parent, "link")
		if err := os.Symlink(realRoot, linkRoot); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Open(securityOptions(linkRoot)); !errors.Is(err, store.ErrInsecurePath) {
			t.Fatalf("root symlink error = %v", err)
		}
	})

	t.Run("owners directory symlink", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "activity")
		target := filepath.Join(t.TempDir(), "owners")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, "owners")); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Open(securityOptions(root)); !errors.Is(err, store.ErrInsecurePath) {
			t.Fatalf("owners symlink error = %v", err)
		}
	})

	t.Run("owner directory replacement", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "activity")
		owner := securityOwner(t, "env_ownerlink", "incarnation-ownerlink")
		activity, err := store.Open(securityOptions(root))
		if err != nil {
			t.Fatal(err)
		}
		if err := activity.Append(
			context.Background(),
			securityRecord(owner, "ses_ownerlink", 1, "/workspace/a"),
		); err != nil {
			t.Fatal(err)
		}
		if err := activity.Close(); err != nil {
			t.Fatal(err)
		}
		ownerRoot := filepath.Join(root, "owners", owner.Key())
		moved := filepath.Join(t.TempDir(), "moved-owner")
		if err := os.Rename(ownerRoot, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, ownerRoot); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Open(securityOptions(root)); !errors.Is(err, store.ErrInsecurePath) {
			t.Fatalf("owner replacement error = %v", err)
		}
	})

	t.Run("active file symlink", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "activity")
		owner := securityOwner(t, "env_filelink", "incarnation-filelink")
		activity, err := store.Open(securityOptions(root))
		if err != nil {
			t.Fatal(err)
		}
		if err := activity.Append(
			context.Background(),
			securityRecord(owner, "ses_filelink", 1, "/workspace/a"),
		); err != nil {
			t.Fatal(err)
		}
		if err := activity.Close(); err != nil {
			t.Fatal(err)
		}
		active := filepath.Join(root, "owners", owner.Key(), "active.seg")
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(active); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, active); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Open(securityOptions(root)); !errors.Is(err, store.ErrInsecurePath) {
			t.Fatalf("active symlink error = %v", err)
		}
	})

	t.Run("active file hardlink", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "activity")
		owner := securityOwner(t, "env_hardlink", "incarnation-hardlink")
		activity, err := store.Open(securityOptions(root))
		if err != nil {
			t.Fatal(err)
		}
		if err := activity.Append(
			context.Background(),
			securityRecord(owner, "ses_hardlink", 1, "/workspace/a"),
		); err != nil {
			t.Fatal(err)
		}
		if err := activity.Close(); err != nil {
			t.Fatal(err)
		}
		active := filepath.Join(root, "owners", owner.Key(), "active.seg")
		outside := filepath.Join(t.TempDir(), "outside-hardlink")
		if err := os.Link(active, outside); err != nil {
			t.Skipf("filesystem does not support hardlink fixture: %v", err)
		}
		if _, err := store.Open(securityOptions(root)); !errors.Is(err, store.ErrInsecurePath) {
			t.Fatalf("active hardlink error = %v", err)
		}
	})
}

func TestStoreRejectsTraversalWhileHashingUntrustedOwnerLabels(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	nonCanonical := parent + string(filepath.Separator) +
		"activity" + string(filepath.Separator) + ".." +
		string(filepath.Separator) + "escape"
	if _, err := store.Open(securityOptions(nonCanonical)); !errors.Is(err, store.ErrInvalidOptions) {
		t.Fatalf("non-canonical root error = %v", err)
	}

	root := filepath.Join(parent, "activity")
	outside := filepath.Join(parent, "must-not-be-created")
	owner, err := workloadtypes.NewReusableOwner(
		"env_traversal", "../../lima", "../../must-not-be-created",
	)
	if err != nil {
		t.Fatalf("owner fixture should be valid but untrusted: %v", err)
	}
	activity, err := store.Open(securityOptions(root))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := activity.Append(
		context.Background(),
		securityRecord(owner, "ses_traversal", 1, "/workspace/a"),
	); err != nil {
		t.Fatalf("append untrusted-label owner: %v", err)
	}
	if err := activity.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owner label escaped the store root: %v", err)
	}
	ownerRoot := filepath.Join(root, "owners", owner.Key())
	if info, err := os.Stat(ownerRoot); err != nil || !info.IsDir() {
		t.Fatalf("hashed owner root missing: info=%v err=%v", info, err)
	}
}

func TestQueryCursorIsSignedAndBoundToExactOwner(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "activity")
	ownerA := securityOwner(t, "env_cursor_a", "incarnation-cursor-a")
	ownerB := securityOwner(t, "env_cursor_b", "incarnation-cursor-b")
	activity, err := store.Open(securityOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = activity.Close() })
	for sequence, owner := range []workloadtypes.ActivityOwner{ownerA, ownerA, ownerB} {
		record := securityRecord(
			owner,
			map[bool]string{true: "ses_cursor_a", false: "ses_cursor_b"}[owner.Equal(ownerA)],
			uint64(sequence+1),
			fmt.Sprintf("/workspace/%d", sequence),
		)
		if err := activity.Append(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	service, err := activity.NewQueryService(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.Events(context.Background(), workloadquery.EventsQuery{
		Owner: ownerA, Limit: 1,
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if page.NextCursor == "" {
		t.Fatal("first page did not produce a cursor")
	}
	if _, err := service.Events(context.Background(), workloadquery.EventsQuery{
		Owner: ownerB, Cursor: page.NextCursor, Limit: 1,
	}); !errors.Is(err, workloadquery.ErrCursorOwnerMismatch) {
		t.Fatalf("cross-owner cursor error = %v", err)
	}
	tampered := page.NextCursor[:len(page.NextCursor)-1] + "A"
	if tampered == page.NextCursor {
		tampered = page.NextCursor[:len(page.NextCursor)-1] + "B"
	}
	if _, err := service.Events(context.Background(), workloadquery.EventsQuery{
		Owner: ownerA, Cursor: tampered, Limit: 1,
	}); !errors.Is(err, workloadquery.ErrCursorInvalid) {
		t.Fatalf("tampered cursor error = %v", err)
	}
}

func TestUnauthenticatedActivityAPILeaksNoStoreEvidence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 7, 0, 0, 0, time.UTC)
	api := manager.API{
		Token: "operator-token", ExpiresAt: now.Add(time.Hour),
		Now: func() time.Time { return now },
	}
	for _, target := range []string{
		"/api/v1/activity/summary?environment=env_private&incarnation=secret",
		"/api/v1/activity/events?session=ses_private",
		"/api/v1/activity/executions?session=ses_private",
		"/api/v1/activity/coverage?session=ses_private",
		"/api/v1/activity/risks?session=ses_private",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Host = "127.0.0.1"
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", target, response.Code)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s missing no-store response", target)
		}
		body := response.Body.String()
		for _, forbidden := range []string{
			"/workspace", "incarnation=secret", "ses_private",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s leaked %q in %q", target, forbidden, body)
			}
		}
	}
}

func securityOptions(root string) store.Options {
	return store.Options{
		Root: root, ActiveSegmentBytes: 1 << 20,
		PerOwnerBytes: 8 << 20, GlobalBytes: 32 << 20,
		Now: func() time.Time {
			return time.Date(2026, 7, 29, 7, 0, 0, 0, time.UTC)
		},
	}
}

func securityOwner(
	t *testing.T,
	environmentID, incarnationID string,
) workloadtypes.ActivityOwner {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner(
		environmentID, "lima", incarnationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func securityRecord(
	owner workloadtypes.ActivityOwner,
	sessionID string,
	sequence uint64,
	path string,
) workloadtypes.ActivityRecord {
	at := time.Date(2026, 7, 29, 7, 0, int(sequence), 0, time.UTC)
	return workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		ID:     fmt.Sprintf("act_security%08d", sequence),
		Owner:  owner, SessionID: sessionID,
		Kind: workloadtypes.ActivityFile, Operation: "read",
		Subject: workloadtypes.FileSubject{
			Kind: workloadtypes.ActivityFile, Path: path,
			PathState: "resolved", PathClass: "workspace",
			FileType: "regular",
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: at, LastAt: at,
		FirstSequence: sequence, LastSequence: sequence,
		Attribution:     workloadtypes.AttributionExact,
		CoverageID:      fmt.Sprintf("cov_security%08d", sequence),
		RedactionStatus: workloadtypes.RedactionPassed,
	}
}
