package readgrant

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestOwnerLockProvesOnlyHeldLiveSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hostfs-read", "owner.lock")
	owner, err := AcquireOwner(path)
	if err != nil {
		t.Fatalf("AcquireOwner: %v", err)
	}
	if err := ProbeOwner(path); err != nil {
		t.Fatalf("held owner was not live: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("Close owner: %v", err)
	}
	if err := ProbeOwner(path); !errors.Is(err, ErrSessionNotLive) {
		t.Fatalf("released owner error=%v want not live", err)
	}
}

func TestOwnerLockMissingOrUnreadableIsUnprovable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "owner.lock")
	if err := ProbeOwner(path); !errors.Is(err, ErrSessionUnprovable) {
		t.Fatalf("missing owner error=%v", err)
	}
}
