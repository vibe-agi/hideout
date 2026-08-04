package lima

import (
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestMigrationDestinationLimaSSHKeyInitializesOnceAndReusesExactPair(t *testing.T) {
	home := t.TempDir()
	var calls atomic.Int32
	generate := func(_ context.Context, path string) error {
		calls.Add(1)
		return writeMigrationLimaSSHKeyFixture(path, 0x31)
	}

	results := make([]string, 2)
	errorsByCall := make([]error, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errorsByCall[index] = destinationLimaSSHKeyWithGenerator(
				context.Background(), home, generate,
			)
		}(index)
	}
	wait.Wait()
	for _, err := range errorsByCall {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 || results[0] == "" || results[0] != results[1] {
		t.Fatalf("calls=%d results=%q", calls.Load(), results)
	}
	if _, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(results[0] + "\n")); err != nil || len(options) != 0 || len(rest) != 0 {
		t.Fatalf("canonical destination key is invalid: options=%v rest=%q err=%v", options, rest, err)
	}
	for path, mode := range map[string]os.FileMode{
		filepath.Join(home, "_config"):             0o700,
		filepath.Join(home, "_config", "user"):     0o600,
		filepath.Join(home, "_config", "user.pub"): 0o644,
	} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("path=%s mode=%v want=%v err=%v", path, info, mode, err)
		}
	}
	if _, err := destinationLimaSSHKeyWithGenerator(
		context.Background(), home,
		func(context.Context, string) error { return errors.New("generator must not run") },
	); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationDestinationLimaSSHKeyRejectsIncompleteMismatchedAndAliasedPairs(t *testing.T) {
	t.Run("incomplete", func(t *testing.T) {
		home := t.TempDir()
		config := filepath.Join(home, "_config")
		if err := os.Mkdir(config, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(config, "user.pub"), []byte("present"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := destinationLimaSSHKeyWithGenerator(
			context.Background(), home,
			func(context.Context, string) error { return errors.New("generator must not run") },
		); err == nil || !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("incomplete pair error=%v", err)
		}
	})

	t.Run("mismatched", func(t *testing.T) {
		home := t.TempDir()
		config := filepath.Join(home, "_config")
		if err := os.Mkdir(config, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := writeMigrationLimaSSHKeyFixture(filepath.Join(config, "first"), 0x41); err != nil {
			t.Fatal(err)
		}
		if err := writeMigrationLimaSSHKeyFixture(filepath.Join(config, "second"), 0x42); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(config, "first"), filepath.Join(config, "user")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(config, "second.pub"), filepath.Join(config, "user.pub")); err != nil {
			t.Fatal(err)
		}
		if _, err := destinationLimaSSHKeyWithGenerator(
			context.Background(), home,
			func(context.Context, string) error { return errors.New("generator must not run") },
		); err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("mismatched pair error=%v", err)
		}
	})

	t.Run("aliased", func(t *testing.T) {
		home := t.TempDir()
		config := filepath.Join(home, "_config")
		if err := os.Mkdir(config, 0o700); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(home, "outside")
		if err := os.WriteFile(outside, []byte("not a key"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(config, "user")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(config, "user.pub"), []byte("present"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := destinationLimaSSHKeyWithGenerator(
			context.Background(), home,
			func(context.Context, string) error { return errors.New("generator must not run") },
		); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("aliased pair error=%v", err)
		}
	})
}

func writeMigrationLimaSSHKeyFixture(privatePath string, seed byte) error {
	private := ed25519.NewKeyFromSeed([]byte(strings.Repeat(string([]byte{seed}), ed25519.SeedSize)))
	block, err := ssh.MarshalPrivateKey(private, "lima")
	if err != nil {
		return err
	}
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(block), 0o600); err != nil {
		return err
	}
	public, err := ssh.NewPublicKey(private.Public())
	if err != nil {
		return err
	}
	return os.WriteFile(privatePath+".pub", ssh.MarshalAuthorizedKey(public), 0o644)
}
