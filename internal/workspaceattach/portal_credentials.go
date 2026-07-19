package workspaceattach

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type portalCredentialEntry struct {
	credential PortalCredential
	changed    chan struct{}
	revoked    bool
}

func WritePortalCredential(path string, credential PortalCredential) error {
	payload, err := encodePortalCredential(credential)
	if err != nil {
		return err
	}
	data := append([]byte{'H', 'W', 'C', '1'}, payload...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".workspace-credential-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func ReadPortalCredential(path string) (PortalCredential, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PortalCredential{}, err
	}
	if len(data) < 4 || string(data[:4]) != "HWC1" {
		return PortalCredential{}, ErrPortalProtocol
	}
	return decodePortalCredential(data[4:])
}

type PortalCredentialAuthority struct {
	mu      sync.Mutex
	entries map[string]*portalCredentialEntry
	now     func() time.Time
}

type portalCredentialLease struct {
	credential PortalCredential
	changed    <-chan struct{}
}

func NewPortalCredentialAuthority() *PortalCredentialAuthority {
	return &PortalCredentialAuthority{entries: make(map[string]*portalCredentialEntry), now: time.Now}
}

func (authority *PortalCredentialAuthority) Issue(sessionID, environment, incarnation, audience string, ttl time.Duration) (PortalCredential, error) {
	if sessionID == "" || environment == "" || incarnation == "" || audience != PortalAudience || ttl <= 0 {
		return PortalCredential{}, errors.New("invalid workspace portal credential scope")
	}
	token := make([]byte, portalCredentialBytes)
	if _, err := rand.Read(token); err != nil {
		return PortalCredential{}, err
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	generation := uint64(1)
	if old := authority.entries[sessionID]; old != nil {
		generation = old.credential.Generation + 1
		if !old.revoked {
			old.revoked = true
			close(old.changed)
		}
	}
	credential := PortalCredential{
		SessionID: sessionID, Environment: environment, Incarnation: incarnation,
		Audience: audience, Token: token, Generation: generation,
		ExpiresAt: authority.now().Add(ttl),
	}
	authority.entries[sessionID] = &portalCredentialEntry{credential: clonePortalCredential(credential), changed: make(chan struct{})}
	return clonePortalCredential(credential), nil
}

func (authority *PortalCredentialAuthority) Rotate(sessionID string, ttl time.Duration) (PortalCredential, error) {
	authority.mu.Lock()
	entry := authority.entries[sessionID]
	if entry == nil || entry.revoked {
		authority.mu.Unlock()
		return PortalCredential{}, ErrPortalAuthentication
	}
	environment, incarnation, audience := entry.credential.Environment, entry.credential.Incarnation, entry.credential.Audience
	authority.mu.Unlock()
	return authority.Issue(sessionID, environment, incarnation, audience, ttl)
}

func (authority *PortalCredentialAuthority) Revoke(sessionID string) error {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	entry := authority.entries[sessionID]
	if entry == nil || entry.revoked {
		return ErrPortalAuthentication
	}
	entry.revoked = true
	close(entry.changed)
	return nil
}

func (authority *PortalCredentialAuthority) authenticate(candidate PortalCredential) (portalCredentialLease, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	entry := authority.entries[candidate.SessionID]
	if entry == nil || entry.revoked {
		return portalCredentialLease{}, ErrPortalAuthentication
	}
	expected := entry.credential
	if candidate.Environment != expected.Environment || candidate.Incarnation != expected.Incarnation ||
		candidate.Audience != expected.Audience || candidate.Generation != expected.Generation ||
		len(candidate.Token) != len(expected.Token) || subtle.ConstantTimeCompare(candidate.Token, expected.Token) != 1 {
		return portalCredentialLease{}, ErrPortalAuthentication
	}
	if !authority.now().Before(expected.ExpiresAt) {
		return portalCredentialLease{}, ErrPortalCredentialExpired
	}
	return portalCredentialLease{credential: clonePortalCredential(expected), changed: entry.changed}, nil
}

func clonePortalCredential(value PortalCredential) PortalCredential {
	value.Token = append([]byte(nil), value.Token...)
	return value
}

func encodePortalCredential(value PortalCredential) ([]byte, error) {
	if len(value.Token) != portalCredentialBytes {
		return nil, fmt.Errorf("workspace portal credential token length = %d", len(value.Token))
	}
	var encoder portalEncoder
	encoder.string(value.SessionID)
	encoder.string(value.Environment)
	encoder.string(value.Incarnation)
	encoder.string(value.Audience)
	encoder.bytes(value.Token)
	encoder.uint64(value.Generation)
	encoder.int64(value.ExpiresAt.UnixNano())
	return encoder.Bytes(), nil
}

func decodePortalCredential(payload []byte) (PortalCredential, error) {
	decoder := newPortalDecoder(payload)
	sessionID, err := decoder.string(256)
	if err != nil {
		return PortalCredential{}, ErrPortalProtocol
	}
	environment, err := decoder.string(256)
	if err != nil {
		return PortalCredential{}, ErrPortalProtocol
	}
	incarnation, err := decoder.string(256)
	if err != nil {
		return PortalCredential{}, ErrPortalProtocol
	}
	audience, err := decoder.string(256)
	if err != nil {
		return PortalCredential{}, ErrPortalProtocol
	}
	token, err := decoder.bytes(portalCredentialBytes)
	if err != nil || len(token) != portalCredentialBytes {
		return PortalCredential{}, ErrPortalProtocol
	}
	generation, err := decoder.uint64()
	if err != nil {
		return PortalCredential{}, ErrPortalProtocol
	}
	expiresAt, err := decoder.int64()
	if err != nil || decoder.done() != nil {
		return PortalCredential{}, ErrPortalProtocol
	}
	return PortalCredential{
		SessionID: sessionID, Environment: environment, Incarnation: incarnation,
		Audience: audience, Token: token, Generation: generation,
		ExpiresAt: time.Unix(0, expiresAt),
	}, nil
}
