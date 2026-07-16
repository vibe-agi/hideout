package daemon

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
)

// credentialManager owns the rotating daemon operator credential. Only the
// current raw token is retained so it can be returned to local daemon wiring;
// the prior generation is represented only by its fixed-size digest.
type credentialManager struct {
	mu sync.RWMutex

	runtimeDir string
	ttl        time.Duration
	grace      time.Duration
	now        func() time.Time

	currentToken string
	currentHash  [sha256.Size]byte
	issuedAt     time.Time
	rotateAt     time.Time

	previousHash  [sha256.Size]byte
	previousUntil time.Time
	hasPrevious   bool

	generation uint64
}

// newCredentialManager creates generation one and atomically publishes it for
// local clients. ttl is both the current-token validity bound and the rotation
// deadline; grace controls how long the immediately prior generation remains
// valid after a successful rotation.
func newCredentialManager(runtimeDir string, ttl, grace time.Duration, now func() time.Time) (*credentialManager, error) {
	if runtimeDir == "" {
		return nil, errors.New("daemon credential runtime directory is required")
	}
	if ttl <= 0 {
		return nil, errors.New("daemon credential TTL must be positive")
	}
	if grace < 0 {
		return nil, errors.New("daemon credential grace must not be negative")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	token, err := mintToken(runtimeDir)
	if err != nil {
		return nil, err
	}
	issuedAt := now().UTC()
	return &credentialManager{
		runtimeDir:   runtimeDir,
		ttl:          ttl,
		grace:        grace,
		now:          now,
		currentToken: token,
		currentHash:  sha256.Sum256([]byte(token)),
		issuedAt:     issuedAt,
		rotateAt:     issuedAt.Add(ttl),
		generation:   1,
	}, nil
}

// Token returns the current operator token for local daemon wiring. It must not
// be included in status, errors, audit, events, or guest-visible state.
func (m *credentialManager) Token() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentToken
}

// Generation returns the current non-secret rotation generation.
func (m *credentialManager) Generation() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.generation
}

// RotateAt returns the deadline at which the current token must be rotated.
func (m *credentialManager) RotateAt() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rotateAt
}

// Rotate mints and atomically publishes a new generation. In-memory state is
// changed only after the token-file rename succeeds.
func (m *credentialManager) Rotate() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rotateLocked(m.now().UTC())
}

// RotateIfDue rotates only when the current token has reached its deadline.
// It is intended for the daemon's rotation worker; validation itself never
// performs filesystem mutation.
func (m *credentialManager) RotateIfDue() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	if now.Before(m.rotateAt) {
		return false, nil
	}
	_, err := m.rotateLocked(now)
	return err == nil, err
}

func (m *credentialManager) rotateLocked(now time.Time) (string, error) {
	token, err := manager.NewUIToken()
	if err != nil {
		return "", err
	}
	if err := writeTokenFile(m.runtimeDir, token); err != nil {
		return "", err
	}

	previousUntil := now.Add(m.grace)
	// A delayed rotation must not resurrect a token beyond its originally
	// bounded lifetime plus grace.
	if limit := m.rotateAt.Add(m.grace); limit.Before(previousUntil) {
		previousUntil = limit
	}
	m.previousHash = m.currentHash
	m.previousUntil = previousUntil
	m.hasPrevious = m.grace > 0 && now.Before(previousUntil)
	m.currentToken = token
	m.currentHash = sha256.Sum256([]byte(token))
	m.issuedAt = now
	m.rotateAt = now.Add(m.ttl)
	m.generation++
	return token, nil
}

// Validate checks current and immediately-prior credentials using fixed-size,
// constant-time digest comparisons. Expiry and grace decisions do not depend on
// token contents.
func (m *credentialManager) Validate(token string) bool {
	_, ok := m.ValidateGeneration(token)
	return ok
}

// ValidateGeneration returns the generation carried by a valid current or
// grace credential. The generation is non-secret and binds renewal frames to
// the exact token that was checked.
func (m *credentialManager) ValidateGeneration(token string) (uint64, bool) {
	digest := sha256.Sum256([]byte(token))
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := m.now().UTC()
	currentMatch := subtle.ConstantTimeCompare(digest[:], m.currentHash[:]) == 1
	previousMatch := subtle.ConstantTimeCompare(digest[:], m.previousHash[:]) == 1
	currentValid := now.Before(m.rotateAt)
	previousValid := m.hasPrevious && now.Before(m.previousUntil)
	if currentMatch && currentValid {
		return m.generation, true
	}
	if previousMatch && previousValid && m.generation > 1 {
		return m.generation - 1, true
	}
	return 0, false
}
