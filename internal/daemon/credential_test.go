package daemon

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type credentialTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *credentialTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *credentialTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestCredentialManagerCreatesCurrentTokenWithPrivateFile(t *testing.T) {
	dir := t.TempDir()
	clock := &credentialTestClock{now: time.Unix(1_000, 0).UTC()}
	m, err := newCredentialManager(dir, 10*time.Minute, time.Minute, clock.Now)
	if err != nil {
		t.Fatalf("newCredentialManager: %v", err)
	}
	token := m.Token()
	if token == "" || !strings.HasPrefix(token, "ui_") {
		t.Fatalf("unexpected current token shape")
	}
	if m.Generation() != 1 {
		t.Fatalf("generation=%d want=1", m.Generation())
	}
	if got, want := m.RotateAt(), clock.Now().Add(10*time.Minute); !got.Equal(want) {
		t.Fatalf("rotateAt=%s want=%s", got, want)
	}
	if !m.Validate(token) {
		t.Fatal("current token was not accepted")
	}
	if m.Validate("") || m.Validate("ui_wrong") {
		t.Fatal("empty or wrong token was accepted")
	}

	data, err := os.ReadFile(filepath.Join(dir, tokenName))
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if string(data) != token+"\n" {
		t.Fatal("token file does not contain the current token")
	}
	info, err := os.Stat(filepath.Join(dir, tokenName))
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode=%#o want=0600", info.Mode().Perm())
	}
}

func TestCredentialManagerRotationAcceptsOnlyImmediatePriorDuringGrace(t *testing.T) {
	dir := t.TempDir()
	clock := &credentialTestClock{now: time.Unix(2_000, 0).UTC()}
	m, err := newCredentialManager(dir, time.Hour, 30*time.Second, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	first := m.Token()
	clock.Advance(10 * time.Minute)
	second, err := m.Rotate()
	if err != nil {
		t.Fatalf("first rotation: %v", err)
	}
	if second == first || m.Generation() != 2 {
		t.Fatalf("first rotation did not advance generation")
	}
	if !m.Validate(first) || !m.Validate(second) {
		t.Fatal("current and immediate prior tokens must both work during grace")
	}

	third, err := m.Rotate()
	if err != nil {
		t.Fatalf("second rotation: %v", err)
	}
	if m.Generation() != 3 || third == second {
		t.Fatalf("second rotation did not advance generation")
	}
	if m.Validate(first) {
		t.Fatal("token older than the immediate prior generation was accepted")
	}
	if !m.Validate(second) || !m.Validate(third) {
		t.Fatal("current and immediate prior tokens must work after second rotation")
	}

	clock.Advance(30 * time.Second)
	if m.Validate(second) {
		t.Fatal("prior token was accepted at the grace deadline")
	}
	if !m.Validate(third) {
		t.Fatal("current token stopped working before its TTL")
	}
	data, err := os.ReadFile(filepath.Join(dir, tokenName))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != third+"\n" {
		t.Fatal("rotation did not publish the latest token")
	}
}

func TestCredentialManagerRotateIfDueAndExpiry(t *testing.T) {
	dir := t.TempDir()
	clock := &credentialTestClock{now: time.Unix(3_000, 0).UTC()}
	m, err := newCredentialManager(dir, time.Minute, 10*time.Second, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	first := m.Token()
	if rotated, err := m.RotateIfDue(); err != nil || rotated {
		t.Fatalf("early RotateIfDue rotated=%t err=%v", rotated, err)
	}
	clock.Advance(time.Minute)
	if m.Validate(first) {
		t.Fatal("current token was accepted at its rotation deadline")
	}
	if rotated, err := m.RotateIfDue(); err != nil || !rotated {
		t.Fatalf("due RotateIfDue rotated=%t err=%v", rotated, err)
	}
	second := m.Token()
	if second == first || m.Generation() != 2 {
		t.Fatal("due rotation did not publish generation two")
	}
	if !m.Validate(first) || !m.Validate(second) {
		t.Fatal("due rotation did not preserve bounded prior grace")
	}
	clock.Advance(10 * time.Second)
	if m.Validate(first) {
		t.Fatal("expired prior token was accepted")
	}
}

func TestCredentialManagerAtomicRewriteUnderConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	clock := &credentialTestClock{now: time.Unix(4_000, 0).UTC()}
	m, err := newCredentialManager(dir, time.Hour, time.Minute, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, tokenName)
	stop := make(chan struct{})
	errs := make(chan string, 1)
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				data, err := os.ReadFile(path)
				if err != nil {
					select {
					case errs <- err.Error():
					default:
					}
					return
				}
				if !validCredentialFile(data) {
					select {
					case errs <- "reader observed a partial or malformed token file":
					default:
					}
					return
				}
			}
		}()
	}

	const rotations = 32
	var writers sync.WaitGroup
	for i := 0; i < 4; i++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for j := 0; j < rotations/4; j++ {
				if _, err := m.Rotate(); err != nil {
					select {
					case errs <- err.Error():
					default:
					}
					return
				}
			}
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	select {
	case msg := <-errs:
		t.Fatal(msg)
	default:
	}
	if got, want := m.Generation(), uint64(rotations+1); got != want {
		t.Fatalf("generation=%d want=%d", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode=%#o want=0600", info.Mode().Perm())
	}
	temps, err := filepath.Glob(filepath.Join(dir, "."+tokenName+".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("atomic rewrite left temporary files: %v", temps)
	}
}

func TestCredentialManagerFailedRewriteDoesNotAdvanceState(t *testing.T) {
	dir := t.TempDir()
	clock := &credentialTestClock{now: time.Unix(5_000, 0).UTC()}
	m, err := newCredentialManager(dir, time.Hour, time.Minute, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	token := m.Token()
	if err := os.Remove(filepath.Join(dir, tokenName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, tokenName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Rotate(); err == nil {
		t.Fatal("rotation unexpectedly succeeded over a token directory")
	} else if strings.Contains(err.Error(), token) {
		t.Fatalf("rotation error leaked token material: %v", err)
	}
	if m.Generation() != 1 || m.Token() != token || !m.Validate(token) {
		t.Fatal("failed rewrite changed in-memory credential state")
	}
}

func TestCredentialManagerRejectsInvalidConfiguration(t *testing.T) {
	clock := func() time.Time { return time.Unix(6_000, 0).UTC() }
	for _, tt := range []struct {
		name  string
		dir   string
		ttl   time.Duration
		grace time.Duration
	}{
		{name: "missing runtime directory", ttl: time.Minute},
		{name: "non-positive TTL", dir: t.TempDir()},
		{name: "negative grace", dir: t.TempDir(), ttl: time.Minute, grace: -time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newCredentialManager(tt.dir, tt.ttl, tt.grace, clock); err == nil {
				t.Fatal("invalid credential configuration was accepted")
			}
		})
	}
}

func validCredentialFile(data []byte) bool {
	text := string(data)
	if !strings.HasPrefix(text, "ui_") || !strings.HasSuffix(text, "\n") {
		return false
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(text, "ui_"), "\n")
	if len(encoded) != 48 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}
