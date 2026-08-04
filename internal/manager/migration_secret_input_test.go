package manager

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationSecretInputIsOneShotPurposeClientAndBundleBound(t *testing.T) {
	now := time.Date(2026, 8, 2, 5, 0, 0, 0, time.UTC)
	store := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x42}, 256)),
	})
	defer store.Close()
	binding := migrationBundleFileBindingFixture()
	issued, err := store.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeImport, ClientBinding: "client-token-A",
		BundleID: "migb_fixture1234", BundleFile: &binding,
		Passphrase: []byte("correct migration passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.UsesRemaining != 1 || issued.Purpose != MigrationSecretPurposeImport ||
		issued.BundleID != "migb_fixture1234" || issued.Handle == "" {
		t.Fatalf("issued handle=%+v", issued)
	}
	if strings.Contains(issued.Handle, "passphrase") {
		t.Fatalf("handle exposed secret input: %q", issued.Handle)
	}

	var observed []byte
	err = store.Consume(MigrationSecretInputUse{
		Handle: issued.Handle, Purpose: MigrationSecretPurposeImport,
		ClientBinding: "client-token-A", BundleID: issued.BundleID,
		BundleFile: &binding,
	}, func(passphrase []byte) error {
		observed = passphrase
		if string(passphrase) != "correct migration passphrase" {
			t.Fatalf("passphrase=%q", passphrase)
		}
		return errors.New("consumer failed after derivation")
	})
	if err == nil || errors.Is(err, ErrMigrationSecretInputRequired) {
		t.Fatalf("callback error was not preserved: %v", err)
	}
	for _, value := range observed {
		if value != 0 {
			t.Fatal("consumed passphrase backing storage was not cleared")
		}
	}
	if err := store.Consume(MigrationSecretInputUse{
		Handle: issued.Handle, Purpose: MigrationSecretPurposeImport,
		ClientBinding: "client-token-A", BundleID: issued.BundleID,
		BundleFile: &binding,
	}, func([]byte) error { return nil }); !errors.Is(err, ErrMigrationSecretInputRequired) {
		t.Fatalf("second consume error=%v", err)
	}
}

func TestMigrationSecretInputConcurrentConsumeExecutesExactlyOnce(t *testing.T) {
	store := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{
		Random: bytes.NewReader(bytes.Repeat([]byte{0x51}, 256)),
	})
	defer store.Close()
	binding := migrationBundleFileBindingFixture()
	issued, err := store.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeInspect, ClientBinding: "client-token-A",
		BundleID: "migb_fixture1234", BundleFile: &binding,
		Passphrase: []byte("one-shot secret"),
	})
	if err != nil {
		t.Fatal(err)
	}

	var callbacks atomic.Int32
	start := make(chan struct{})
	errorsOut := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			errorsOut <- store.Consume(MigrationSecretInputUse{
				Handle: issued.Handle, Purpose: MigrationSecretPurposeInspect,
				ClientBinding: "client-token-A", BundleID: issued.BundleID,
				BundleFile: &binding,
			}, func([]byte) error {
				callbacks.Add(1)
				return nil
			})
		}()
	}
	close(start)
	workers.Wait()
	close(errorsOut)
	successes := 0
	for err := range errorsOut {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrMigrationSecretInputRequired) {
			t.Fatalf("consume error=%v", err)
		}
	}
	if successes != 1 || callbacks.Load() != 1 {
		t.Fatalf("successes=%d callbacks=%d", successes, callbacks.Load())
	}
}

func TestMigrationSecretInputLookupBindsApplyWithoutConsuming(t *testing.T) {
	store := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{
		Random: bytes.NewReader(bytes.Repeat([]byte{0x55}, 256)),
	})
	defer store.Close()
	issued, err := store.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportCreate, ClientBinding: "client-token-A",
		BundleID: "migb_fixture1234", Passphrase: []byte("one-shot secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := store.Lookup(MigrationSecretInputLookup{
		Handle: issued.Handle, Purpose: MigrationSecretPurposeExportCreate,
		ClientBinding: "client-token-A",
	})
	if err != nil || observed.BundleID != issued.BundleID || observed.UsesRemaining != 1 {
		t.Fatalf("lookup=%+v err=%v", observed, err)
	}
	if err := store.Consume(MigrationSecretInputUse{
		Handle: issued.Handle, Purpose: MigrationSecretPurposeExportCreate,
		ClientBinding: "client-token-A", BundleID: issued.BundleID,
	}, func(passphrase []byte) error {
		if string(passphrase) != "one-shot secret" {
			t.Fatalf("passphrase=%q", passphrase)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationSecretInputResolveBindingReturnsNoSecretAndPreservesOneShotUse(t *testing.T) {
	store := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{
		Random: bytes.NewReader(bytes.Repeat([]byte{0x57}, 256)),
	})
	defer store.Close()
	binding := migrationBundleFileBindingFixture()
	issued, err := store.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportResume, ClientBinding: "client-token-A",
		BundleID: "migb_fixture1234", BundleFile: &binding,
		Passphrase: []byte("resume-only secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolveBinding(MigrationSecretInputLookup{
		Handle: issued.Handle, Purpose: MigrationSecretPurposeExportResume,
		ClientBinding: "client-token-A",
	})
	if err != nil || resolved.Handle != issued || resolved.BundleFile == nil ||
		*resolved.BundleFile != binding {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	resolved.BundleFile.Size++
	resolvedAgain, err := store.ResolveBinding(MigrationSecretInputLookup{
		Handle: issued.Handle, Purpose: MigrationSecretPurposeExportResume,
		ClientBinding: "client-token-A",
	})
	if err != nil || resolvedAgain.BundleFile == nil || *resolvedAgain.BundleFile != binding {
		t.Fatalf("resolved binding alias leaked: %+v err=%v", resolvedAgain, err)
	}
	if err := store.Consume(MigrationSecretInputUse{
		Handle: issued.Handle, Purpose: MigrationSecretPurposeExportResume,
		ClientBinding: "client-token-A", BundleID: issued.BundleID,
		BundleFile: &binding,
	}, func(passphrase []byte) error {
		if string(passphrase) != "resume-only secret" {
			t.Fatalf("passphrase=%q", passphrase)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationSecretInputMismatchExpiryAndRestartFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 2, 5, 0, 0, 0, time.UTC)
	store := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{
		Now:    func() time.Time { return now },
		TTL:    time.Minute,
		Random: bytes.NewReader(bytes.Repeat([]byte{0x61}, 512)),
	})
	binding := migrationBundleFileBindingFixture()
	issued, err := store.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeImport, ClientBinding: "client-token-A",
		BundleID: "migb_fixture1234", BundleFile: &binding,
		Passphrase: []byte("credential://user:password@example.test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := binding
	changed.Size++
	err = store.Consume(MigrationSecretInputUse{
		Handle: issued.Handle, Purpose: MigrationSecretPurposeImport,
		ClientBinding: "client-token-A", BundleID: issued.BundleID,
		BundleFile: &changed,
	}, func([]byte) error { return nil })
	if !errors.Is(err, ErrMigrationSecretInputMismatch) ||
		strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), issued.Handle) {
		t.Fatalf("bundle mismatch error leaked or drifted: %v", err)
	}
	if err := store.Consume(MigrationSecretInputUse{
		Handle: issued.Handle, Purpose: MigrationSecretPurposeImport,
		ClientBinding: "client-token-A", BundleID: issued.BundleID,
		BundleFile: &binding,
	}, func([]byte) error { return nil }); !errors.Is(err, ErrMigrationSecretInputRequired) {
		t.Fatalf("mismatched handle was not invalidated: %v", err)
	}

	issued, err = store.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeInspect, ClientBinding: "client-token-A",
		BundleID: "migb_fixture1234", BundleFile: &binding,
		Passphrase: []byte("expiring passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if err := store.Consume(MigrationSecretInputUse{
		Handle: issued.Handle, Purpose: MigrationSecretPurposeInspect,
		ClientBinding: "client-token-A", BundleID: issued.BundleID,
		BundleFile: &binding,
	}, func([]byte) error { return nil }); !errors.Is(err, ErrMigrationSecretInputExpired) {
		t.Fatalf("expired handle error=%v", err)
	}

	issued, err = store.Create(MigrationSecretInputRequest{
		Purpose: MigrationSecretPurposeExportCreate, ClientBinding: "client-token-A",
		BundleID: "migb_fixture1234", Passphrase: []byte("restart passphrase"),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	restarted := NewMigrationSecretInputStore(MigrationSecretInputStoreOptions{})
	defer restarted.Close()
	if err := restarted.Consume(MigrationSecretInputUse{
		Handle: issued.Handle, Purpose: MigrationSecretPurposeExportCreate,
		ClientBinding: "client-token-A", BundleID: issued.BundleID,
	}, func([]byte) error { return nil }); !errors.Is(err, ErrMigrationSecretInputRequired) {
		t.Fatalf("restart retained handle: %v", err)
	}
}

func migrationBundleFileBindingFixture() MigrationBundleFileBinding {
	return MigrationBundleFileBinding{
		PathDigest:   migration.Digest("sha256:" + strings.Repeat("7", 64)),
		HeaderDigest: migration.Digest("sha256:" + strings.Repeat("8", 64)),
		Device:       42, Inode: 84, Size: 4096, ModifiedUnixNano: 1722574800000000000,
	}
}
